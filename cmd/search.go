package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	searchFormat        string
	searchLimit         int
	searchContext       int
	searchCaseSensitive bool
	searchRegex         bool
	searchFolder        string
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search across notes",
	Long: `Searches for text across all markdown files in your vault.

By default, search is case-insensitive and matches literal strings.
Use --regex for regular expression patterns.

Examples:
  obsidian-cli search "authentication" --vault ~/Documents/Obsidian
  obsidian-cli search "TODO" --vault ~/Documents/Obsidian --case-sensitive
  obsidian-cli search "func.*Error" --vault ~/Documents/Obsidian --regex
  obsidian-cli search "important" --vault ~/Documents/Obsidian --context 2
  obsidian-cli search "project" --vault ~/Documents/Obsidian --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringVar(&searchFormat, "format", "text", "Output format: text, json, paths")
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "n", 0, "Limit number of results (0 = no limit)")
	searchCmd.Flags().IntVarP(&searchContext, "context", "C", 0, "Lines of context around matches")
	searchCmd.Flags().BoolVarP(&searchCaseSensitive, "case-sensitive", "s", false, "Case-sensitive search")
	searchCmd.Flags().BoolVarP(&searchRegex, "regex", "r", false, "Treat query as regular expression")
	searchCmd.Flags().StringVarP(&searchFolder, "folder", "f", "", "Filter to specific folder")
}

// SearchMatch represents a single search match.
type SearchMatch struct {
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Content string   `json:"content"`
	Context []string `json:"context,omitempty"`
}

// SearchResult holds all search results.
type SearchResult struct {
	Query   string        `json:"query"`
	Matches []SearchMatch `json:"matches"`
	Elapsed time.Duration `json:"-"`
}

func runSearch(cmd *cobra.Command, args []string) error {
	if err := RequireVault(); err != nil {
		return err
	}

	query := args[0]

	if searchFormat == "text" {
		printScanHeader("Searching vault")
	}

	result, err := executeSearch(query)
	if err != nil {
		return err
	}

	return outputSearchResults(cmd, result)
}

func executeSearch(query string) (*SearchResult, error) {
	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("invalid vault path: %w", err)
	}

	// Determine scan root (vault root or specific folder)
	scanRoot := absPath
	if searchFolder != "" {
		scanRoot = filepath.Join(absPath, searchFolder)
		if !isPathWithinVault(scanRoot, absPath) {
			return nil, fmt.Errorf("folder path escapes vault boundary: %s", searchFolder)
		}
		if _, err := os.Stat(scanRoot); os.IsNotExist(err) {
			return nil, fmt.Errorf("folder not found: %s", searchFolder)
		}
	}

	// Build the search pattern
	patternStr := query
	if !searchRegex {
		patternStr = regexp.QuoteMeta(query)
	}
	if !searchCaseSensitive {
		patternStr = "(?i)" + patternStr
	}

	pattern, err := regexp.Compile(patternStr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	var matches []SearchMatch

	err = filepath.WalkDir(scanRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if skip, skipDir := shouldSkipEntry(path, d, absPath); skip {
			if skipDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			relPath, _ := filepath.Rel(absPath, path)
			fileMatches := searchFile(path, relPath, pattern)
			matches = append(matches, fileMatches...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return &SearchResult{
		Query:   query,
		Matches: matches,
		Elapsed: time.Since(start),
	}, nil
}

func searchFile(path, relPath string, pattern *regexp.Regexp) []SearchMatch {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var matches []SearchMatch
	var lines []string

	scanner := newLargeScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	for i, line := range lines {
		if pattern.MatchString(line) {
			match := SearchMatch{
				File:    relPath,
				Line:    i + 1,
				Content: strings.TrimSpace(line),
			}

			// Add context lines if requested
			if searchContext > 0 {
				match.Context = getContextLines(lines, i, searchContext)
			}

			matches = append(matches, match)
		}
	}

	return matches
}

func getContextLines(lines []string, matchIndex, contextSize int) []string {
	start := matchIndex - contextSize
	if start < 0 {
		start = 0
	}
	end := matchIndex + contextSize + 1
	if end > len(lines) {
		end = len(lines)
	}

	context := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		if i == matchIndex {
			continue // Skip the match line itself (it's in Content)
		}
		context = append(context, lines[i])
	}
	return context
}

func outputSearchResults(cmd *cobra.Command, result *SearchResult) error {
	total := len(result.Matches)
	matches := applyLimit(result.Matches, searchLimit)

	switch searchFormat {
	case "json":
		return encodeJSON(cmd, result)

	case "paths":
		// Unique file paths only
		seen := make(map[string]bool)
		for _, m := range matches {
			if !seen[m.File] {
				fmt.Println(filepath.Join(vaultPath, m.File))
				seen[m.File] = true
			}
		}

	default:
		fmt.Printf("%s Search Results %s\n\n", colors.Green("?"), colors.Dim(fmt.Sprintf("(%d matches)", total)))

		if len(matches) == 0 {
			fmt.Printf("  No matches found for %s\n", colors.Yellow("\""+result.Query+"\""))
			return nil
		}

		// Group by file
		byFile := make(map[string][]SearchMatch)
		for _, m := range matches {
			byFile[m.File] = append(byFile[m.File], m)
		}

		// Sort files alphabetically
		files := sortedKeys(byFile)
		for _, file := range files {
			fileMatches := byFile[file]
			fmt.Printf("  %s %s\n", colors.Cyan(file), colors.Dim(fmt.Sprintf("(%d)", len(fileMatches))))

			for _, m := range fileMatches {
				// Highlight the match in the content
				content := truncateRunes(m.Content, 80)
				fmt.Printf("    :%d  %s\n", m.Line, colors.Dim(content))

				// Show context if available
				if len(m.Context) > 0 {
					for _, ctx := range m.Context {
						ctxTrunc := truncateRunes(strings.TrimSpace(ctx), 76)
						fmt.Printf("         %s\n", colors.Dim(ctxTrunc))
					}
				}
			}
			fmt.Println()
		}

		printLimitNote(total, searchLimit)
		printScanFooter(result.Elapsed)
	}

	return nil
}
