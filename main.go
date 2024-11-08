package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type LineHistory struct {
	File      string
	Line      string
	Changes   []ChangeEvent
	FirstSeen time.Time
	LastSeen  time.Time
	DeletedAt *time.Time
}

type ChangeEvent struct {
	Timestamp time.Time
	Type      ChangeType
	NewValue  string
	Distance  int
}

type ChangeType int

const (
	Created ChangeType = iota
	Modified
	Deleted
)

type Stats struct {
	MedianLifetime    float64
	MeanLifetime      float64
	StdDevLifetime    float64
	HalfLife          float64
	TotalLinesTracked int
	LinesWithChanges  int
	OldestCode        float64
	NewestCode        float64
	SurvivalRate      map[int]float64
	ChangeFrequency   map[string]int
	AverageEditSize   float64
}

func calculateStats(histories map[string]*LineHistory) Stats {
	var lifetimes []float64
	now := time.Now()

	for _, history := range histories {
		endTime := now
		if history.DeletedAt != nil {
			endTime = *history.DeletedAt
		}
		lifetime := endTime.Sub(history.FirstSeen).Hours() / 24
		if lifetime >= 0 {
			lifetimes = append(lifetimes, lifetime)
		}
	}

	if len(lifetimes) == 0 {
		return Stats{}
	}

	sort.Float64s(lifetimes)

	// Calculate mean
	sum := 0.0
	for _, lifetime := range lifetimes {
		sum += lifetime
	}
	mean := sum / float64(len(lifetimes))

	// Calculate standard deviation
	sumSquares := 0.0
	for _, lifetime := range lifetimes {
		diff := lifetime - mean
		sumSquares += diff * diff
	}
	stdDev := math.Sqrt(sumSquares / float64(len(lifetimes)))

	// Calculate median
	var median float64
	if len(lifetimes)%2 == 0 {
		median = (lifetimes[len(lifetimes)/2-1] + lifetimes[len(lifetimes)/2]) / 2
	} else {
		median = lifetimes[len(lifetimes)/2]
	}

	return Stats{
		MedianLifetime:    median,
		MeanLifetime:      mean,
		StdDevLifetime:    stdDev,
		HalfLife:          median * math.Log(2),
		TotalLinesTracked: len(histories),
		LinesWithChanges:  len(lifetimes),
		OldestCode:        lifetimes[len(lifetimes)-1],
		NewestCode:        lifetimes[0],
	}
}

func calculateSurvivalRate(histories map[string]*LineHistory) map[int]float64 {
	timePoints := make([]float64, 0)
	now := time.Now()

	for _, history := range histories {
		endTime := now
		if history.DeletedAt != nil {
			endTime = *history.DeletedAt
		}
		age := endTime.Sub(history.FirstSeen).Hours() / 24
		if age >= 0 {
			timePoints = append(timePoints, age)
		}
	}

	sort.Float64s(timePoints)
	survival := make(map[int]float64)

	for _, t := range timePoints {
		at := int(t)
		alive := 0

		for _, history := range histories {
			age := now.Sub(history.FirstSeen).Hours() / 24
			if age >= t && (history.DeletedAt == nil ||
				history.DeletedAt.Sub(history.FirstSeen).Hours()/24 > t) {
				alive++
			}
		}

		survival[at] = float64(alive) / float64(len(histories))
	}

	return survival
}

func analyzeRepository(repoPath string, filePattern string) (Stats, map[string]*LineHistory, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return Stats{}, nil, fmt.Errorf("error opening repository: %v", err)
	}

	// Get the HEAD reference
	ref, err := repo.Head()
	if err != nil {
		return Stats{}, nil, fmt.Errorf("error getting HEAD: %v", err)
	}

	// Create commit iterator
	commitIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return Stats{}, nil, fmt.Errorf("error creating commit iterator: %v", err)
	}

	lineHistories := make(map[string]*LineHistory)
	changeFrequency := make(map[string]int)
	var totalEditSize int64
	var totalEdits int64
	var totalLines int

	currentLines := 0
	head, err := repo.Head()
	if err == nil {
		headCommit, err := repo.CommitObject(head.Hash())
		if err == nil {
			tree, err := headCommit.Tree()
			if err == nil {
				tree.Files().ForEach(func(f *object.File) error {
					if strings.HasSuffix(f.Name, strings.TrimPrefix(filePattern, "*")) {
						contents, err := f.Contents()
						if err == nil {
							currentLines += len(strings.Split(contents, "\n"))
						}
					}
					return nil
				})
			}
		}
	}

	err = commitIter.ForEach(func(commit *object.Commit) error {
		// Get the tree for this commit
		tree, err := commit.Tree()
		if err != nil {
			return nil
		}

		// Count total lines in current state
		tree.Files().ForEach(func(f *object.File) error {
			if strings.HasSuffix(f.Name, strings.TrimPrefix(filePattern, "*")) {
				contents, err := f.Contents()
				if err == nil {
					totalLines += len(strings.Split(contents, "\n"))
				}
			}
			return nil
		})

		if commit.NumParents() > 0 {
			parent, err := commit.Parent(0)
			if err != nil {
				return nil // Skip if can't get parent
			}

			// Get changes between commits
			patch, err := commit.Patch(parent)
			if err != nil {
				return nil // Skip if can't get patch
			}

			// Analyze each file diff
			for _, filePatch := range patch.FilePatches() {
				_, to := filePatch.Files()
				if to == nil {
					continue
				}

				// Check if file matches pattern
				if !strings.HasSuffix(to.Path(), strings.TrimPrefix(filePattern, "*")) {
					continue
				}

				// Analyze changes
				for _, chunk := range filePatch.Chunks() {
					lineNum := 1
					switch chunk.Type() {
					case diff.Add:
						lines := strings.Split(chunk.Content(), "\n")
						for _, line := range lines {
							if len(strings.TrimSpace(line)) == 0 {
								continue
							}
							key := fmt.Sprintf("%s:%d:%s", to.Path(), lineNum, line)
							if _, exists := lineHistories[key]; !exists {
								lineHistories[key] = &LineHistory{
									File: to.Path(),
									Line: line,
									Changes: []ChangeEvent{{
										Timestamp: commit.Author.When,
										Type:      Created,
										NewValue:  line,
									}},
									FirstSeen: commit.Author.When,
									LastSeen:  commit.Author.When,
								}
							}
						}
					case diff.Delete:
						lines := strings.Split(chunk.Content(), "\n")
						for _, line := range lines {
							if len(strings.TrimSpace(line)) == 0 {
								continue
							}
							key := fmt.Sprintf("%s:%d:%s", to.Path(), lineNum, line)
							if history, exists := lineHistories[key]; exists {
								deletedAt := commit.Author.When
								history.DeletedAt = &deletedAt
								history.Changes = append(history.Changes, ChangeEvent{
									Timestamp: commit.Author.When,
									Type:      Deleted,
									NewValue:  "",
								})
							}
						}
					case diff.Equal:
						// Handle modifications by comparing old and new lines
						oldLines := strings.Split(chunk.Content(), "\n")
						for _, line := range oldLines {
							if len(strings.TrimSpace(line)) == 0 {
								continue
							}
							key := fmt.Sprintf("%s:%d:%s", to.Path(), lineNum, line)
							if history, exists := lineHistories[key]; exists {
								history.LastSeen = commit.Author.When
								if history.DeletedAt == nil {
									history.Changes = append(history.Changes, ChangeEvent{
										Timestamp: commit.Author.When,
										Type:      Modified,
										NewValue:  line,
									})
								}
							}
						}
					}

					if chunk.Type() != diff.Equal {
						totalEditSize += int64(len(chunk.Content()))
						totalEdits++
					}
				}
			}
		}

		// Inside the commit iteration loop, update change frequency
		for _, history := range lineHistories {
			for _, change := range history.Changes {
				switch change.Type {
				case Created:
					changeFrequency["Created"]++
				case Modified:
					changeFrequency["Modified"]++
				case Deleted:
					changeFrequency["Deleted"]++
				}
			}
		}

		return nil
	})
	if err != nil {
		return Stats{}, nil, fmt.Errorf("error analyzing commits: %v", err)
	}

	// Calculate stats once after processing all commits
	stats := calculateStats(lineHistories)
	stats.TotalLinesTracked = currentLines
	stats.SurvivalRate = calculateSurvivalRate(lineHistories)
	stats.ChangeFrequency = changeFrequency

	// Calculate average edit size before returning
	if totalEdits > 0 {
		stats.AverageEditSize = float64(totalEditSize) / float64(totalEdits)
	}

	return stats, lineHistories, nil
}

func generateReport(stats Stats) string {
	return fmt.Sprintf(`
Code Half-Life Analysis Report
============================

Summary Statistics:
-----------------
- Estimated Code Half-Life: %.1f days
- Median Lifetime: %.1f days
- Mean Lifetime: %.1f days
- Standard Deviation: %.1f days

Code Coverage:
------------
- Total Lines Tracked: %d
- Lines with Multiple Changes: %d
- Oldest Code Age: %.1f days
- Newest Code Age: %.1f days

Survival Rate:
------------
%s

Change Frequency:
---------------
%s

Additional Metrics:
----------------
- Average Edit Size: %.2f characters
`,
		stats.HalfLife,
		stats.MedianLifetime,
		stats.MeanLifetime,
		stats.StdDevLifetime,
		stats.TotalLinesTracked,
		stats.LinesWithChanges,
		stats.OldestCode,
		stats.NewestCode,
		formatSurvivalRate(stats.SurvivalRate),
		formatChangeFrequency(stats.ChangeFrequency),
		stats.AverageEditSize,
	)
}

func formatSurvivalRate(survivalRate map[int]float64) string {
	if len(survivalRate) == 0 {
		return "No survival rate data available"
	}

	var days []int
	for day := range survivalRate {
		days = append(days, day)
	}
	sort.Ints(days)

	var result strings.Builder
	for _, day := range days[:min(5, len(days))] { // Show first 5 data points
		result.WriteString(fmt.Sprintf("  Day %d: %.1f%%\n", day, survivalRate[day]*100))
	}
	return result.String()
}

func formatChangeFrequency(frequency map[string]int) string {
	if len(frequency) == 0 {
		return "No change frequency data available"
	}

	var entries []string
	for change, count := range frequency {
		entries = append(entries, fmt.Sprintf("  %s: %d", change, count))
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Please provide repository path")
	}

	repoPath := os.Args[1]
	filePattern := "*.go" // Default to Go files
	if len(os.Args) > 2 {
		filePattern = os.Args[2]
	}

	stats, _, err := analyzeRepository(repoPath, filePattern)
	if err != nil {
		log.Fatalf("Error analyzing repository: %v", err)
	}

	fmt.Println(generateReport(stats))
}
