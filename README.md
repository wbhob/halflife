# Code Half-Life Analyzer

A Go tool that analyzes the "half-life" of code in Git repositories by tracking how long lines of code survive between modifications. This helps teams understand their codebase's stability and evolution patterns.

## Features

- Tracks individual line changes throughout Git history
- Calculates code half-life (time until 50% of code is modified)
- Provides detailed statistics on code longevity
- Supports filtering by file types and extensions
- Generates comprehensive reports including:
  - Survival rates over time
  - Change frequency metrics
  - Code age distribution
  - Edit size statistics

## Installation

### Prerequisites

- Go 1.19 or later
- Git installed on your system

### Steps

1. Clone the repository:

```bash
git clone https://github.com/wbhob/halflife
cd halflife
```

2. Install dependencies:

```bash
go mod tidy
```

3. Build the tool:

```bash
go build
```

## Usage

Basic usage:

```bash
./halflife /path/to/repository [file-pattern]
```

Examples:

```bash
./halflife /path/to/repository           # Analyzes *.go files by default
./halflife /path/to/repository "*.js"    # Analyze JavaScript files
./halflife /path/to/repository "*.py"    # Analyze Python files
```

## Output Example

```
Code Half-Life Analysis Report
============================

Summary Statistics:
-----------------
- Estimated Code Half-Life: 45.2 days
- Median Lifetime: 65.3 days
- Mean Lifetime: 72.1 days
- Standard Deviation: 23.4 days

Code Coverage:
------------
- Total Lines Tracked: 15234
- Lines with Multiple Changes: 8543
- Oldest Code Age: 365.0 days
- Newest Code Age: 1.0 days

Survival Rate:
------------
  Day 1: 95.0%
  Day 7: 85.2%
  Day 30: 70.5%
  Day 90: 45.3%
  Day 180: 25.1%

Change Frequency:
---------------
  Created: 15234
  Modified: 8543
  Deleted: 3421

Additional Metrics:
----------------
- Average Edit Size: 125.34 characters
```

## How It Works

The tool:

1. Analyzes the Git history of your repository
2. Tracks when each line of code was first introduced and last modified
3. Calculates statistics based on the lifetime of code lines
4. Estimates the half-life by analyzing modification patterns

## Statistics Explained

- **Half-Life**: Estimated time until 50% of code is modified
- **Median Lifetime**: Middle value of all code lifetimes
- **Mean Lifetime**: Average lifetime of code
- **Standard Deviation**: Variation in code lifetimes
- **Total Lines Tracked**: Number of unique lines analyzed
- **Lines with Multiple Changes**: Lines that have been modified at least once

## Limitations

- Only tracks lines in their current file location (line moves between files are treated as delete + create)
- Merges and non-linear history may affect accuracy
- Very large repositories might require significant processing time
- Analysis is based on the main branch only (follows HEAD)
- Empty lines are ignored in the analysis
- File renames may be interpreted as deletes and creates
- Only analyzes files matching the specified pattern (defaults to *.go)

## Contributing

Contributions are welcome! Please feel free to submit pull requests or create issues for bugs and feature requests.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

- Uses the [go-git](https://github.com/go-git/go-git) library for Git operations
- Inspired by various studies on code churn and software evolution
