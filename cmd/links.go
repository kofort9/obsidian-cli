package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var (
	linksFormat          string
	linksDeadOnly        bool
	linksValidOnly       bool
	linksIncludeExternal bool
)

var linksCmd = &cobra.Command{
	Use:   "links <note-name>",
	Short: "List outgoing links from a note",
	Long: `Shows all links that a note contains (what it links TO).

This is the inverse of backlinks:
  - backlinks <note> → who links TO this note (incoming)
  - links <note>     → what this note links TO (outgoing)

Useful for:
  - Auditing a note's dependencies
  - Finding broken/dead links in a specific note
  - Identifying MOC (Map of Content) notes with many outlinks

Examples:
  obsidian-cli links "api-design" --vault ~/Documents/Obsidian
  obsidian-cli links "concepts/my-note" --vault ~/Documents/Obsidian --dead-only
  obsidian-cli links "note" --vault ~/Documents/Obsidian --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runLinks,
}

func init() {
	rootCmd.AddCommand(linksCmd)
	linksCmd.Flags().StringVar(&linksFormat, "format", "text", "Output format: text, json, paths")
	linksCmd.Flags().BoolVar(&linksDeadOnly, "dead-only", false, "Show only dead/broken links")
	linksCmd.Flags().BoolVar(&linksValidOnly, "valid-only", false, "Show only valid links")
	linksCmd.Flags().BoolVar(&linksIncludeExternal, "include-external", false, "Include external http/https links")
}

// LinkInfo represents a single outgoing link.
type LinkInfo struct {
	Target   string `json:"target"`
	Valid    bool   `json:"valid"`
	Line     int    `json:"line"`
	FullPath string `json:"full_path,omitempty"`
}

// LinksResult holds all outgoing links from a note.
type LinksResult struct {
	SourceFile    string        `json:"source_file"`
	ValidLinks    []LinkInfo    `json:"valid_links"`
	DeadLinks     []LinkInfo    `json:"dead_links"`
	ExternalLinks []string      `json:"external_links,omitempty"`
	TotalLinks    int           `json:"total_links"`
	Elapsed       time.Duration `json:"-"`
}

// Regex for external URLs
var externalURLRegex = regexp.MustCompile(`https?://[^\s\)\]]+`)

func runLinks(cmd *cobra.Command, args []string) error {
	noteName := strings.TrimSuffix(args[0], ".md")

	if linksFormat == "text" {
		printScanHeader("Analyzing links")
	}

	result, err := analyzeLinks(noteName)
	if err != nil {
		return err
	}

	return outputLinksResults(cmd, result)
}

func analyzeLinks(noteName string) (*LinksResult, error) {
	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("invalid vault path: %w", err)
	}

	// Find the source note
	sourceFile, err := findNoteFile(absPath, noteName)
	if err != nil {
		return nil, err
	}

	relSource, _ := filepath.Rel(absPath, sourceFile)

	// Read the file and extract links
	file, err := os.Open(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var validLinks []LinkInfo
	var deadLinks []LinkInfo
	var externalLinks []string
	seenLinks := make(map[string]bool)
	seenExternal := make(map[string]bool)

	scanner := newLargeScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Extract wikilinks
		matches := vault.WikilinkRegex.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) <= 1 {
				continue
			}

			linkTarget := vault.NormalizeLink(match[1])
			if linkTarget == "" || seenLinks[strings.ToLower(linkTarget)] {
				continue
			}
			seenLinks[strings.ToLower(linkTarget)] = true

			// Check if target exists
			targetPath := resolveWikilink(absPath, linkTarget)
			if targetPath != "" {
				validLinks = append(validLinks, LinkInfo{
					Target:   linkTarget,
					Valid:    true,
					Line:     lineNum,
					FullPath: mustRelPath(absPath, targetPath),
				})
			} else {
				deadLinks = append(deadLinks, LinkInfo{
					Target: linkTarget,
					Valid:  false,
					Line:   lineNum,
				})
			}
		}

		// Extract external URLs if requested
		if linksIncludeExternal {
			urlMatches := externalURLRegex.FindAllString(line, -1)
			for _, url := range urlMatches {
				url = strings.TrimRight(url, ".,;:!?") // Clean trailing punctuation
				if !seenExternal[url] {
					seenExternal[url] = true
					externalLinks = append(externalLinks, url)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Sort links alphabetically
	sort.Slice(validLinks, func(i, j int) bool {
		return validLinks[i].Target < validLinks[j].Target
	})
	sort.Slice(deadLinks, func(i, j int) bool {
		return deadLinks[i].Target < deadLinks[j].Target
	})
	sort.Strings(externalLinks)

	return &LinksResult{
		SourceFile:    relSource,
		ValidLinks:    validLinks,
		DeadLinks:     deadLinks,
		ExternalLinks: externalLinks,
		TotalLinks:    len(validLinks) + len(deadLinks),
		Elapsed:       time.Since(start),
	}, nil
}

// resolveWikilink attempts to find the target file for a wikilink.
// Returns the full path if found, empty string if not.
func resolveWikilink(absPath, linkTarget string) string {
	// Handle heading/block references - strip the # or ^ part
	if idx := strings.Index(linkTarget, "#"); idx != -1 {
		linkTarget = linkTarget[:idx]
	}
	if idx := strings.Index(linkTarget, "^"); idx != -1 {
		linkTarget = linkTarget[:idx]
	}
	if linkTarget == "" {
		return "" // Link to heading in same file
	}

	// Try direct path first
	directPath := filepath.Join(absPath, linkTarget+".md")
	// Security: Validate path is within vault before stat
	if isPathWithinVault(directPath, absPath) {
		if _, err := os.Stat(directPath); err == nil {
			return directPath
		}
	}

	// Search for the file (case-insensitive, basename match)
	found, err := findNoteFile(absPath, linkTarget)
	if err == nil {
		return found
	}

	return ""
}

func outputLinksResults(cmd *cobra.Command, result *LinksResult) error {
	// Apply filters
	validLinks := result.ValidLinks
	deadLinks := result.DeadLinks

	if linksDeadOnly {
		validLinks = nil
	}
	if linksValidOnly {
		deadLinks = nil
	}

	switch linksFormat {
	case "json":
		output := map[string]interface{}{
			"source_file": result.SourceFile,
			"valid_links": validLinks,
			"dead_links":  deadLinks,
			"total_links": result.TotalLinks,
			"valid_count": len(result.ValidLinks),
			"dead_count":  len(result.DeadLinks),
		}
		if linksIncludeExternal {
			output["external_links"] = result.ExternalLinks
		}
		return encodeJSON(cmd, output)

	case "paths":
		// Output resolved paths for valid links only
		for _, link := range validLinks {
			if link.FullPath != "" {
				fmt.Println(filepath.Join(vaultPath, link.FullPath))
			}
		}

	default:
		fmt.Printf("%s Links from: %s\n\n", colors.Green("→"), colors.Cyan(result.SourceFile))

		if len(validLinks) > 0 {
			fmt.Printf("  %s Valid %s\n", colors.Green("✓"), colors.Dim(fmt.Sprintf("(%d)", len(validLinks))))
			for _, link := range validLinks {
				fmt.Printf("    [[%s]]\n", link.Target)
			}
			fmt.Println()
		}

		if len(deadLinks) > 0 {
			fmt.Printf("  %s Dead %s\n", colors.Red("✗"), colors.Dim(fmt.Sprintf("(%d)", len(deadLinks))))
			for _, link := range deadLinks {
				fmt.Printf("    [[%s]] %s\n", colors.Yellow(link.Target), colors.Dim("(not found)"))
			}
			fmt.Println()
		}

		if linksIncludeExternal && len(result.ExternalLinks) > 0 {
			fmt.Printf("  %s External %s\n", colors.Cyan("↗"), colors.Dim(fmt.Sprintf("(%d)", len(result.ExternalLinks))))
			for _, url := range result.ExternalLinks {
				fmt.Printf("    %s\n", colors.Dim(truncateRunes(url, 70)))
			}
			fmt.Println()
		}

		if len(validLinks) == 0 && len(deadLinks) == 0 {
			fmt.Println("  No wikilinks found in this note.")
		}

		printScanFooter(result.Elapsed)
	}

	return nil
}
