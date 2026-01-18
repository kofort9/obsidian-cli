package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	tagsFormat string
	tagsFilter string
	tagsLimit  int
)

var tagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List tags or find notes by tag",
	Long: `Lists all tags in your vault with counts, or filters notes by a specific tag.

Tags are detected from:
  - YAML frontmatter: tags: [tag1, tag2] or tags: tag1, tag2
  - Inline hashtags: #tag-name (excluding headings)

Examples:
  obsidian-cli tags --vault ~/Documents/Obsidian
  obsidian-cli tags --vault ~/Documents/Obsidian --tag project
  obsidian-cli tags --vault ~/Documents/Obsidian --format json
  obsidian-cli tags --vault ~/Documents/Obsidian --tag work --format paths`,
	RunE: runTags,
}

func init() {
	rootCmd.AddCommand(tagsCmd)
	tagsCmd.Flags().StringVar(&tagsFormat, "format", "text", "Output format: text, json, paths")
	tagsCmd.Flags().StringVarP(&tagsFilter, "tag", "t", "", "Filter notes by specific tag")
	tagsCmd.Flags().IntVarP(&tagsLimit, "limit", "n", 0, "Limit number of results (0 = no limit)")
}

// TagInfo represents a tag with its usage count and associated files.
type TagInfo struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Files []string `json:"files,omitempty"`
}

// TagScanResult holds the results of a tag scan.
type TagScanResult struct {
	Tags    map[string]*TagInfo
	Elapsed time.Duration
}

var (
	// Matches inline #tags (not headings, not in code blocks)
	inlineTagRegex = regexp.MustCompile(`(?:^|[^\w&])#([\w][\w/-]*)`)
	// Matches YAML array tags: [tag1, tag2]
	yamlArrayTagRegex = regexp.MustCompile(`^tags:\s*\[(.*)\]`)
	// Matches YAML list or inline tags: tag1, tag2 or - tag1
	yamlListTagRegex = regexp.MustCompile(`^tags:\s*(.+)`)
	// Matches YAML list item: - tag
	yamlListItemRegex = regexp.MustCompile(`^\s*-\s*(.+)`)
)

func runTags(cmd *cobra.Command, args []string) error {
	if tagsFormat == "text" {
		printScanHeader("Scanning tags")
	}

	result, err := scanTags()
	if err != nil {
		return err
	}

	if tagsFilter != "" {
		return outputFilteredByTag(cmd, result)
	}
	return outputAllTags(cmd, result)
}

func scanTags() (*TagScanResult, error) {
	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("invalid vault path: %w", err)
	}

	tags := make(map[string]*TagInfo)

	err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		// Security: Check for symlinks that escape vault boundary
		if d.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil // Skip unresolvable symlinks
			}
			if !isPathWithinVault(target, absPath) {
				return nil // Skip symlinks pointing outside vault
			}
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			relPath, _ := filepath.Rel(absPath, path)
			extractTagsFromFile(path, relPath, tags)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	return &TagScanResult{
		Tags:    tags,
		Elapsed: time.Since(start),
	}, nil
}

func extractTagsFromFile(path, relPath string, tags map[string]*TagInfo) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inFrontmatter := false
	frontmatterDone := false
	inTagsList := false
	inCodeBlock := false
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Handle frontmatter boundaries
		if lineNum == 1 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter && line == "---" {
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		// Parse frontmatter tags
		if inFrontmatter {
			// Check for tags array: tags: [tag1, tag2]
			if matches := yamlArrayTagRegex.FindStringSubmatch(line); matches != nil {
				parseFrontmatterTags(matches[1], relPath, tags)
				inTagsList = false
				continue
			}

			// Check for tags start: tags: tag1, tag2 or tags:
			if matches := yamlListTagRegex.FindStringSubmatch(line); matches != nil {
				tagContent := strings.TrimSpace(matches[1])
				if tagContent != "" && !strings.HasPrefix(tagContent, "-") {
					// Inline tags: tags: tag1, tag2
					parseFrontmatterTags(tagContent, relPath, tags)
					inTagsList = false
				} else {
					// Start of list format
					inTagsList = true
				}
				continue
			}

			// Handle list items if we're in a tags list
			if inTagsList {
				if matches := yamlListItemRegex.FindStringSubmatch(line); matches != nil {
					addTag(strings.TrimSpace(matches[1]), relPath, tags)
					continue
				}
				// Non-list line ends the tags section
				if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inTagsList = false
				}
			}
			continue
		}

		// Parse inline #tags (only after frontmatter)
		if frontmatterDone || lineNum > 1 {
			// Track code block state (fenced code blocks with ```)
			if strings.HasPrefix(line, "```") {
				inCodeBlock = !inCodeBlock
				continue
			}

			// Skip content inside code blocks
			if inCodeBlock {
				continue
			}

			// Skip indented code blocks (4 spaces)
			if strings.HasPrefix(line, "    ") {
				continue
			}

			// Skip headings (# Heading)
			if strings.HasPrefix(strings.TrimSpace(line), "# ") {
				continue
			}

			matches := inlineTagRegex.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				if len(match) > 1 {
					addTag(match[1], relPath, tags)
				}
			}
		}
	}
}

func parseFrontmatterTags(content, relPath string, tags map[string]*TagInfo) {
	// Handle comma or space separated tags
	content = strings.Trim(content, "[]")
	// Split by comma first
	parts := strings.Split(content, ",")
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		tag = strings.Trim(tag, "\"'") // Remove quotes
		if tag != "" {
			addTag(tag, relPath, tags)
		}
	}
}

func addTag(tag, relPath string, tags map[string]*TagInfo) {
	// Normalize tag: lowercase, trim
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return
	}

	if _, exists := tags[tag]; !exists {
		tags[tag] = &TagInfo{Name: tag, Files: []string{}}
	}

	// Avoid duplicate files for the same tag
	info := tags[tag]
	for _, f := range info.Files {
		if f == relPath {
			return
		}
	}
	info.Files = append(info.Files, relPath)
	info.Count = len(info.Files)
}

func outputFilteredByTag(cmd *cobra.Command, result *TagScanResult) error {
	filterLower := strings.ToLower(tagsFilter)
	tagInfo, exists := result.Tags[filterLower]

	if !exists || tagInfo.Count == 0 {
		if tagsFormat == "text" {
			fmt.Printf("  No notes found with tag %s\n", colors.Yellow("#"+tagsFilter))
		} else if tagsFormat == "json" {
			return encodeJSON(cmd, []string{})
		}
		return nil
	}

	files := tagInfo.Files
	sort.Strings(files)
	total := len(files)
	files = applyLimit(files, tagsLimit)

	switch tagsFormat {
	case "json":
		return encodeJSON(cmd, files)

	case "paths":
		for _, f := range files {
			fmt.Println(filepath.Join(vaultPath, f))
		}

	default:
		fmt.Printf("%s Notes tagged %s %s\n\n", colors.Green("#"), colors.Yellow(tagsFilter), colors.Dim(fmt.Sprintf("(%d total)", total)))
		byFolder := groupByFolder(files)
		for _, folder := range sortedKeys(byFolder) {
			folderFiles := byFolder[folder]
			fmt.Printf("  %s %s\n", colors.Cyan(folder+"/"), colors.Dim(fmt.Sprintf("(%d)", len(folderFiles))))
			for _, f := range folderFiles {
				fmt.Printf("    %s\n", filepath.Base(f))
			}
			fmt.Println()
		}
		printLimitNote(total, tagsLimit)
		printScanFooter(result.Elapsed)
	}

	return nil
}

func outputAllTags(cmd *cobra.Command, result *TagScanResult) error {
	// Convert to sorted slice by count (descending)
	tagList := make([]*TagInfo, 0, len(result.Tags))
	for _, info := range result.Tags {
		tagList = append(tagList, info)
	}
	sort.Slice(tagList, func(i, j int) bool {
		if tagList[i].Count == tagList[j].Count {
			return tagList[i].Name < tagList[j].Name
		}
		return tagList[i].Count > tagList[j].Count
	})

	total := len(tagList)
	tagList = applyLimit(tagList, tagsLimit)

	switch tagsFormat {
	case "json":
		// Strip file lists for overview JSON
		simplified := make([]map[string]interface{}, len(tagList))
		for i, t := range tagList {
			simplified[i] = map[string]interface{}{
				"name":  t.Name,
				"count": t.Count,
			}
		}
		return encodeJSON(cmd, simplified)

	case "paths":
		// For paths format without --tag, just list tag names
		for _, t := range tagList {
			fmt.Println(t.Name)
		}

	default:
		fmt.Printf("%s Tags %s\n\n", colors.Green("#"), colors.Dim(fmt.Sprintf("(%d unique)", total)))

		if len(tagList) == 0 {
			fmt.Println("  No tags found in vault.")
			return nil
		}

		// Find max tag length for alignment
		maxLen := 0
		for _, t := range tagList {
			if len(t.Name) > maxLen {
				maxLen = len(t.Name)
			}
		}

		for _, t := range tagList {
			bar := strings.Repeat("█", min(t.Count, 50))
			fmt.Printf("  %-*s %s %s\n", maxLen+1, colors.Yellow("#"+t.Name), colors.Dim(fmt.Sprintf("(%d)", t.Count)), colors.Cyan(bar))
		}
		fmt.Println()
		printLimitNote(total, tagsLimit)
		printScanFooter(result.Elapsed)
	}

	return nil
}
