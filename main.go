package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type CodeLine struct {
	Content    string
	File       string
	CreatedAt  time.Time
	DeletedAt  *time.Time
	CommitHash string    // Track which commit created/modified this line
	LastSeen   time.Time // Last time this line was seen in the codebase
}

type Stats struct {
	HalfLife     float64 // days
	TotalLines   int
	MedianAge    float64 // days
	SurvivalRate []float64
	// Validation metrics
	OldestLine     string    // Content of the oldest surviving line
	OldestLineAge  float64   // Age in days of oldest surviving line
	NewestLine     string    // Content of the newest line
	NewestLineAge  float64   // Age in days of newest line
	DeletedLines   int       // Number of deleted lines
	SurvivingLines int       // Number of lines still alive
	FirstCommit    time.Time // Timestamp of first commit
	LastCommit     time.Time // Timestamp of last commit
}

type ValidationReport struct {
	Stats    Stats
	Samples  map[string]*CodeLine // Sample of interesting lines for validation
	Timeline []TimelineEvent
}

type TimelineEvent struct {
	Time         time.Time
	CommitHash   string
	Action       string // "create", "delete"
	File         string
	Line         string
	LinesSoFar   int
	DeletedSoFar int
}

func analyzeRepository(repoPath string, filePattern string, validateMode bool) (interface{}, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("error opening repository: %v", err)
	}

	// Try to get main or master branch
	var mainRef *plumbing.Reference
	for _, name := range []string{"main", "master"} {
		ref, err := repo.Reference(plumbing.NewBranchReferenceName(name), true)
		if err == nil {
			mainRef = ref
			break
		}
	}

	if mainRef == nil {
		return nil, fmt.Errorf("could not find main or master branch")
	}

	codeLines := make(map[string]*CodeLine)
	var timeline []TimelineEvent

	// Process commits from oldest to newest
	commits := make([]*object.Commit, 0)
	commitIter, err := repo.Log(&git.LogOptions{From: mainRef.Hash()})
	if err != nil {
		return nil, fmt.Errorf("error getting commit iterator: %v", err)
	}

	err = commitIter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error iterating commits: %v", err)
	}

	// Reverse commits to go from oldest to newest
	for i := len(commits)/2 - 1; i >= 0; i-- {
		opp := len(commits) - 1 - i
		commits[i], commits[opp] = commits[opp], commits[i]
	}

	// Process each commit
	for i, commit := range commits {
		if i == 0 {
			// For first commit, record all lines as new
			tree, err := commit.Tree()
			if err != nil {
				continue
			}

			err = tree.Files().ForEach(func(f *object.File) error {
				if !matchesPattern(f.Name, filePattern) {
					return nil
				}

				content, err := f.Contents()
				if err != nil {
					return nil
				}

				for _, line := range strings.Split(content, "\n") {
					if strings.TrimSpace(line) == "" {
						continue
					}
					key := fmt.Sprintf("%s:%s", f.Name, line)
					codeLines[key] = &CodeLine{
						Content:    line,
						File:       f.Name,
						CreatedAt:  commit.Author.When,
						LastSeen:   commit.Author.When,
						CommitHash: commit.Hash.String(),
					}
					if validateMode {
						timeline = append(timeline, TimelineEvent{
							Time:       commit.Author.When,
							CommitHash: commit.Hash.String(),
							Action:     "create",
							File:       f.Name,
							Line:       line,
							LinesSoFar: len(codeLines),
						})
					}
				}
				return nil
			})
			if err != nil {
				log.Printf("Warning: error processing initial commit: %v", err)
			}
			continue
		}

		parent := commits[i-1]
		patch, err := commit.Patch(parent)
		if err != nil {
			continue
		}

		for _, filePatch := range patch.FilePatches() {
			from, to := filePatch.Files()
			if to == nil && from == nil {
				continue
			}

			var path string
			if to != nil {
				path = to.Path()
			} else {
				path = from.Path()
			}

			if !matchesPattern(path, filePattern) {
				continue
			}

			for _, chunk := range filePatch.Chunks() {
				switch chunk.Type() {
				case diff.Add:
					for _, line := range strings.Split(chunk.Content(), "\n") {
						if strings.TrimSpace(line) == "" {
							continue
						}
						key := fmt.Sprintf("%s:%s", path, line)
						if _, exists := codeLines[key]; !exists {
							codeLines[key] = &CodeLine{
								Content:    line,
								File:       path,
								CreatedAt:  commit.Author.When,
								LastSeen:   commit.Author.When,
								CommitHash: commit.Hash.String(),
							}
							if validateMode {
								timeline = append(timeline, TimelineEvent{
									Time:       commit.Author.When,
									CommitHash: commit.Hash.String(),
									Action:     "create",
									File:       path,
									Line:       line,
									LinesSoFar: len(codeLines),
								})
							}
						} else {
							codeLines[key].LastSeen = commit.Author.When
						}
					}
				case diff.Delete:
					for _, line := range strings.Split(chunk.Content(), "\n") {
						if strings.TrimSpace(line) == "" {
							continue
						}
						key := fmt.Sprintf("%s:%s", path, line)
						if cl, exists := codeLines[key]; exists {
							deletedAt := commit.Author.When
							cl.DeletedAt = &deletedAt
							if validateMode {
								timeline = append(timeline, TimelineEvent{
									Time:         commit.Author.When,
									CommitHash:   commit.Hash.String(),
									Action:       "delete",
									File:         path,
									Line:         line,
									LinesSoFar:   len(codeLines),
									DeletedSoFar: countDeletedLines(codeLines),
								})
							}
						}
					}
				}
			}
		}
	}

	// Calculate statistics
	var lifetimes []float64
	now := time.Now()
	totalLines := 0
	survivingLines := 0
	var oldestLine, newestLine *CodeLine
	var oldestAge, newestAge float64

	for _, line := range codeLines {
		if line.DeletedAt == nil {
			survivingLines++
			age := now.Sub(line.CreatedAt).Hours() / 24
			if age > 0 {
				lifetimes = append(lifetimes, age)
				if oldestLine == nil || age > oldestAge {
					oldestLine = line
					oldestAge = age
				}
				if newestLine == nil || age < newestAge {
					newestLine = line
					newestAge = age
				}
			}
		} else {
			lifetime := line.DeletedAt.Sub(line.CreatedAt).Hours() / 24
			if lifetime > 0 {
				lifetimes = append(lifetimes, lifetime)
			}
		}
		totalLines++
	}

	if len(lifetimes) == 0 {
		return nil, fmt.Errorf("no valid lifetimes found")
	}

	sort.Float64s(lifetimes)

	// Calculate survival rate over time
	survivalRate := make([]float64, 0)
	maxAge := lifetimes[len(lifetimes)-1]
	timePoints := 100 // number of points to sample
	for i := 0; i < timePoints; i++ {
		timePoint := (maxAge * float64(i)) / float64(timePoints)
		survived := 0
		for _, lifetime := range lifetimes {
			if lifetime >= timePoint {
				survived++
			}
		}
		survivalRate = append(survivalRate, float64(survived)/float64(len(lifetimes)))
	}

	// Find where survival rate crosses 0.5 to get half-life
	var halfLife float64
	for i, rate := range survivalRate {
		if rate <= 0.5 {
			timePoint := (maxAge * float64(i)) / float64(timePoints)
			halfLife = timePoint
			break
		}
	}

	// If we never cross 0.5, use the median lifetime
	if halfLife == 0 {
		halfLife = lifetimes[len(lifetimes)/2]
	}

	stats := Stats{
		HalfLife:       halfLife,
		TotalLines:     totalLines,
		MedianAge:      lifetimes[len(lifetimes)/2],
		SurvivalRate:   survivalRate,
		OldestLine:     oldestLine.Content,
		OldestLineAge:  oldestAge,
		NewestLine:     newestLine.Content,
		NewestLineAge:  newestAge,
		DeletedLines:   totalLines - survivingLines,
		SurvivingLines: survivingLines,
		FirstCommit:    commits[0].Author.When,
		LastCommit:     commits[len(commits)-1].Author.When,
	}

	if !validateMode {
		return stats, nil
	}

	// For validation mode, collect interesting samples
	samples := make(map[string]*CodeLine)
	// Add oldest and newest lines
	samples["oldest"] = oldestLine
	samples["newest"] = newestLine

	// Add some random surviving lines
	survivingSlice := make([]*CodeLine, 0)
	for _, line := range codeLines {
		if line.DeletedAt == nil {
			survivingSlice = append(survivingSlice, line)
		}
	}
	if len(survivingSlice) > 5 {
		for i := 0; i < 5; i++ {
			idx := (i * len(survivingSlice)) / 5
			samples[fmt.Sprintf("sample_%d", i)] = survivingSlice[idx]
		}
	}

	return ValidationReport{
		Stats:    stats,
		Samples:  samples,
		Timeline: timeline,
	}, nil
}

func countDeletedLines(lines map[string]*CodeLine) int {
	count := 0
	for _, line := range lines {
		if line.DeletedAt != nil {
			count++
		}
	}
	return count
}

func matchesPattern(filename, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		ext := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(filename, ext)
	}
	matched, err := filepath.Match(pattern, filepath.Base(filename))
	if err != nil {
		return false
	}
	return matched
}

func generateReport(result interface{}) string {
	switch v := result.(type) {
	case Stats:
		return generateStatsReport(v)
	case ValidationReport:
		return generateValidationReport(v)
	default:
		return "Unknown report type"
	}
}

func generateStatsReport(stats Stats) string {
	return fmt.Sprintf(`
Code Half-Life Analysis Report
============================

Summary Statistics:
-----------------
- Code Half-Life: %.1f days
- Median Age: %.1f days
- Total Lines Analyzed: %d
- Currently Surviving: %d (%.1f%%)
- Deleted: %d (%.1f%%)

Repository Timespan:
------------------
- First Commit: %s
- Last Commit: %s
- Total Age: %.1f days

Survival Rate:
------------
%s
`,
		stats.HalfLife,
		stats.MedianAge,
		stats.TotalLines,
		stats.SurvivingLines,
		float64(stats.SurvivingLines)/float64(stats.TotalLines)*100,
		stats.DeletedLines,
		float64(stats.DeletedLines)/float64(stats.TotalLines)*100,
		stats.FirstCommit.Format("2006-01-02"),
		stats.LastCommit.Format("2006-01-02"),
		stats.LastCommit.Sub(stats.FirstCommit).Hours()/24,
		formatSurvivalCurve(stats.SurvivalRate),
	)
}

func generateValidationReport(report ValidationReport) string {
	var b strings.Builder
	b.WriteString(generateStatsReport(report.Stats))

	b.WriteString("\nSample Lines for Validation:\n")
	b.WriteString("-------------------------\n")
	for label, line := range report.Samples {
		age := time.Now().Sub(line.CreatedAt).Hours() / 24
		b.WriteString(fmt.Sprintf("%s:\n", label))
		b.WriteString(fmt.Sprintf("  File: %s\n", line.File))
		b.WriteString(fmt.Sprintf("  Content: %s\n", line.Content))
		b.WriteString(fmt.Sprintf("  Age: %.1f days\n", age))
		b.WriteString(fmt.Sprintf("  Created in: %s\n", line.CommitHash))
		b.WriteString(fmt.Sprintf("  Created at: %s\n", line.CreatedAt.Format("2006-01-02")))
		if line.DeletedAt != nil {
			b.WriteString(fmt.Sprintf("  Deleted at: %s\n", line.DeletedAt.Format("2006-01-02")))
		}
		b.WriteString("\n")
	}

	b.WriteString("\nTimeline Sample (first 5 and last 5 events):\n")
	b.WriteString("----------------------------------------\n")
	timeline := report.Timeline
	numEvents := len(timeline)
	eventsToShow := 5

	// Show first 5 events
	for i := 0; i < eventsToShow && i < numEvents; i++ {
		event := timeline[i]
		b.WriteString(fmt.Sprintf("%s: %s line in %s (%d lines total, %d deleted)\n",
			event.Time.Format("2006-01-02"),
			event.Action,
			event.File,
			event.LinesSoFar,
			event.DeletedSoFar,
		))
	}

	if numEvents > eventsToShow*2 {
		b.WriteString("...\n")
	}

	// Show last 5 events
	for i := max(eventsToShow, numEvents-eventsToShow); i < numEvents; i++ {
		event := timeline[i]
		b.WriteString(fmt.Sprintf("%s: %s line in %s (%d lines total, %d deleted)\n",
			event.Time.Format("2006-01-02"),
			event.Action,
			event.File,
			event.LinesSoFar,
			event.DeletedSoFar,
		))
	}

	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatSurvivalCurve(rates []float64) string {
	if len(rates) == 0 {
		return "No survival rate data available"
	}

	var result strings.Builder
	numPoints := 5
	step := len(rates) / numPoints
	for i := 0; i < numPoints && i*step < len(rates); i++ {
		result.WriteString(fmt.Sprintf("  %.0f%%: %.1f%%\n",
			float64(i*step)/float64(len(rates))*100,
			rates[i*step]*100))
	}
	return result.String()
}

func main() {
	validateMode := flag.Bool("validate", false, "Enable validation mode with detailed output")
	jsonOutput := flag.Bool("json", false, "Output results as JSON")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Please provide repository path")
	}

	repoPath := args[0]
	filePattern := "*" // Default to all files
	if len(args) > 1 {
		filePattern = args[1]
	}

	result, err := analyzeRepository(repoPath, filePattern, *validateMode)
	if err != nil {
		log.Fatalf("Error analyzing repository: %v", err)
	}

	if *jsonOutput {
		json, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Fatalf("Error generating JSON output: %v", err)
		}
		fmt.Println(string(json))
	} else {
		fmt.Println(generateReport(result))
	}
}
