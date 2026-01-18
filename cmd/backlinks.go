package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var (
	backlinksFormat  string
	backlinksContext bool
)

var backlinksCmd = &cobra.Command{
	Use:   "backlinks <note>",
	Short: "Show all notes linking to a specific note",
	Long: `Finds all notes that link to the specified note.

The note can be specified as:
  - Just the filename: "my-note" or "my-note.md"
  - A relative path: "concepts/my-note"

Examples:
  obsidian-cli backlinks "my-note" --vault ~/Documents/Obsidian
  obsidian-cli backlinks "concepts/idea" --vault ~/Documents/Obsidian
  obsidian-cli backlinks "note.md" --vault ~/Documents/Obsidian --context
  obsidian-cli backlinks "note" --vault ~/Documents/Obsidian --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runBacklinks,
}

func init() {
	rootCmd.AddCommand(backlinksCmd)
	backlinksCmd.Flags().StringVar(&backlinksFormat, "format", "text", "Output format: text, json, paths")
	backlinksCmd.Flags().BoolVarP(&backlinksContext, "context", "c", false, "Show surrounding context for each link")
}

// BacklinkResult represents a single backlink finding.
type BacklinkResult struct {
	SourceFile string `json:"source"`
	Line       int    `json:"line"`
	Context    string `json:"context,omitempty"`
}

func runBacklinks(cmd *cobra.Command, args []string) error {
	targetNote := strings.TrimSuffix(args[0], ".md")

	if backlinksFormat == "text" {
		fmt.Printf("\n%s Finding backlinks to: %s\n\n", colors.Cyan("=>"), colors.Green(targetNote))
	}

	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return fmt.Errorf("invalid vault path: %w", err)
	}

	mdFiles, err := collectMarkdownFiles(absPath)
	if err != nil {
		return err
	}

	backlinks := findBacklinks(absPath, mdFiles, targetNote)
	elapsed := time.Since(start)

	sortBacklinks(backlinks)

	switch backlinksFormat {
	case "json":
		return encodeJSON(cmd, backlinks)

	case "paths":
		printBacklinkPaths(backlinks)

	default:
		printBacklinksText(backlinks, targetNote)
		fmt.Printf("  %s %s (%d files)\n", colors.Cyan("Scanned in:"), elapsed.Round(time.Millisecond), len(mdFiles))
	}

	return nil
}

func collectMarkdownFiles(absPath string) ([]string, error) {
	var mdFiles []string
	err := filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			mdFiles = append(mdFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}
	return mdFiles, nil
}

func findBacklinks(absPath string, mdFiles []string, targetNote string) []BacklinkResult {
	targetLower := strings.ToLower(targetNote)
	targetBaseName := strings.ToLower(filepath.Base(targetNote))

	var backlinks []BacklinkResult
	for _, filePath := range mdFiles {
		relPath, _ := filepath.Rel(absPath, filePath)

		if isTargetFile(filePath, relPath, targetBaseName, targetLower) {
			continue
		}

		fileBacklinks := scanFileForBacklinks(filePath, relPath, targetBaseName, targetLower)
		backlinks = append(backlinks, fileBacklinks...)
	}
	return backlinks
}

func isTargetFile(filePath, relPath, targetBaseName, targetLower string) bool {
	fileBaseName := strings.ToLower(strings.TrimSuffix(filepath.Base(filePath), ".md"))
	fileRelName := strings.ToLower(strings.TrimSuffix(relPath, ".md"))
	return fileBaseName == targetBaseName || fileRelName == targetLower
}

func scanFileForBacklinks(filePath, relPath, targetBaseName, targetLower string) []BacklinkResult {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var results []BacklinkResult
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if result, found := findBacklinkInLine(line, lineNum, relPath, targetBaseName, targetLower); found {
			results = append(results, result)
		}
	}
	return results
}

func findBacklinkInLine(line string, lineNum int, relPath, targetBaseName, targetLower string) (BacklinkResult, bool) {
	matches := vault.WikilinkRegex.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) <= 1 {
			continue
		}

		linkTarget := strings.ToLower(vault.NormalizeLink(match[1]))
		linkBaseName := strings.ToLower(filepath.Base(linkTarget))

		if linkBaseName == targetBaseName || linkTarget == targetLower {
			result := BacklinkResult{
				SourceFile: relPath,
				Line:       lineNum,
			}
			if backlinksContext {
				result.Context = strings.TrimSpace(line)
			}
			return result, true
		}
	}
	return BacklinkResult{}, false
}

func sortBacklinks(backlinks []BacklinkResult) {
	sort.Slice(backlinks, func(i, j int) bool {
		if backlinks[i].SourceFile == backlinks[j].SourceFile {
			return backlinks[i].Line < backlinks[j].Line
		}
		return backlinks[i].SourceFile < backlinks[j].SourceFile
	})
}

func printBacklinkPaths(backlinks []BacklinkResult) {
	seen := make(map[string]bool)
	for _, bl := range backlinks {
		if !seen[bl.SourceFile] {
			fmt.Println(filepath.Join(vaultPath, bl.SourceFile))
			seen[bl.SourceFile] = true
		}
	}
}

func printBacklinksText(backlinks []BacklinkResult, targetNote string) {
	fmt.Printf("%s Backlinks %s\n\n", colors.Green("<-"), colors.Dim(fmt.Sprintf("(%d found)", len(backlinks))))

	if len(backlinks) == 0 {
		fmt.Printf("  No backlinks found for %s\n", colors.Green("[["+targetNote+"]]"))
		return
	}

	bySource := make(map[string][]BacklinkResult)
	for _, bl := range backlinks {
		bySource[bl.SourceFile] = append(bySource[bl.SourceFile], bl)
	}

	for _, source := range sortedKeys(bySource) {
		links := bySource[source]
		fmt.Printf("  %s\n", colors.Cyan(source))
		for _, bl := range links {
			if backlinksContext && bl.Context != "" {
				ctx := truncateRunes(bl.Context, 80)
				fmt.Printf("    :%d  %s\n", bl.Line, colors.Dim(ctx))
			} else {
				fmt.Printf("    :%d\n", bl.Line)
			}
		}
	}
	fmt.Println()
}
