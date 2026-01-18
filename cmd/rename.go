package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var renameDryRun bool

var renameCmd = &cobra.Command{
	Use:   "rename <old-name> <new-name>",
	Short: "Rename a note and update all backlinks",
	Long: `Renames a note and updates all wikilinks pointing to it.

This is a safe refactoring operation that:
  1. Finds the source note
  2. Locates all files linking to it
  3. Updates those links to point to the new name
  4. Renames the file

Use --dry-run to preview changes without modifying files.

The note can be specified as:
  - Just the filename: "my-note" or "my-note.md"
  - A relative path: "concepts/my-note"

Examples:
  obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian --dry-run
  obsidian-cli rename "concepts/idea" "concepts/better-idea" --vault ~/Documents/Obsidian
  obsidian-cli rename "note.md" "renamed.md" --vault ~/Documents/Obsidian`,
	Args: cobra.ExactArgs(2),
	RunE: runRename,
}

func init() {
	rootCmd.AddCommand(renameCmd)
	renameCmd.Flags().BoolVar(&renameDryRun, "dry-run", false, "Preview changes without modifying files")
}

// RenameChange represents a single file modification.
type RenameChange struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

// RenameResult holds the rename operation results.
type RenameResult struct {
	SourceFile     string         `json:"source_file"`
	DestFile       string         `json:"dest_file"`
	BacklinkCount  int            `json:"backlink_count"`
	Changes        []RenameChange `json:"changes"`
	FilesModified  int            `json:"files_modified"`
	LinksUpdated   int            `json:"links_updated"`
	Executed       bool           `json:"executed"`
}

func runRename(cmd *cobra.Command, args []string) error {
	oldName := strings.TrimSuffix(args[0], ".md")
	newName := strings.TrimSuffix(args[1], ".md")

	fmt.Printf("\n%s Rename: %s -> %s\n\n", colors.Cyan("=>"), colors.Yellow(oldName), colors.Green(newName))

	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return fmt.Errorf("invalid vault path: %w", err)
	}

	// Validate input names
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return fmt.Errorf("note names cannot be empty")
	}

	// Find the source file
	sourceFile, err := findNoteFile(absPath, oldName)
	if err != nil {
		return err
	}

	// Determine destination path
	destFile := computeDestPath(absPath, sourceFile, newName)

	// Security: Validate destination is within vault boundary
	if !isPathWithinVault(destFile, absPath) {
		return fmt.Errorf("destination path escapes vault boundary: %s", newName)
	}

	// Check destination doesn't exist
	if _, err := os.Stat(destFile); err == nil {
		return fmt.Errorf("destination file already exists: %s", destFile)
	}

	// Find all backlinks
	relSource, _ := filepath.Rel(absPath, sourceFile)
	mdFiles, err := collectMarkdownFiles(absPath)
	if err != nil {
		return err
	}

	backlinks := findBacklinksForRename(absPath, mdFiles, oldName)
	elapsed := time.Since(start)

	// Prepare result
	result := &RenameResult{
		SourceFile:    relSource,
		DestFile:      mustRelPath(absPath, destFile),
		BacklinkCount: len(backlinks),
		Changes:       []RenameChange{},
		Executed:      !renameDryRun,
	}

	// Calculate all changes
	filesAffected := make(map[string]bool)
	for _, bl := range backlinks {
		change := RenameChange{
			File:       bl.SourceFile,
			Line:       bl.Line,
			OldContent: bl.Context,
			NewContent: computeNewLinkContent(bl.Context, oldName, newName),
		}
		result.Changes = append(result.Changes, change)
		filesAffected[bl.SourceFile] = true
	}
	result.FilesModified = len(filesAffected)
	result.LinksUpdated = len(result.Changes)

	// Output preview
	printRenamePreview(result, elapsed)

	if renameDryRun {
		fmt.Printf("  %s Run without --dry-run to execute\n\n", colors.Yellow("!"))
		return nil
	}

	// Execute the rename
	return executeRename(absPath, sourceFile, destFile, result.Changes, oldName, newName)
}

func findNoteFile(absPath, noteName string) (string, error) {
	noteLower := strings.ToLower(noteName)
	noteBaseLower := strings.ToLower(filepath.Base(noteName))

	var found string
	err := filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			relPath, _ := filepath.Rel(absPath, path)
			baseName := strings.TrimSuffix(filepath.Base(path), ".md")
			relName := strings.TrimSuffix(relPath, ".md")

			// Match by full path or basename
			if strings.ToLower(relName) == noteLower ||
				strings.ToLower(baseName) == noteBaseLower {
				if found != "" {
					// Prefer exact path match
					if strings.ToLower(relName) == noteLower {
						found = path
					}
				} else {
					found = path
				}
			}
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("note not found: %s", noteName)
	}
	return found, nil
}

func computeDestPath(absPath, sourceFile, newName string) string {
	// If newName contains path separators, treat as path relative to vault root
	if strings.Contains(newName, "/") || strings.Contains(newName, string(filepath.Separator)) {
		return filepath.Join(absPath, newName+".md")
	}

	// Otherwise, rename in same directory as source
	dir := filepath.Dir(sourceFile)
	return filepath.Join(dir, newName+".md")
}

func mustRelPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

func findBacklinksForRename(absPath string, mdFiles []string, targetNote string) []BacklinkResult {
	targetLower := strings.ToLower(targetNote)
	targetBaseName := strings.ToLower(filepath.Base(targetNote))

	var backlinks []BacklinkResult
	for _, filePath := range mdFiles {
		relPath, _ := filepath.Rel(absPath, filePath)

		// Skip the target file itself
		fileBaseName := strings.ToLower(strings.TrimSuffix(filepath.Base(filePath), ".md"))
		fileRelName := strings.ToLower(strings.TrimSuffix(relPath, ".md"))
		if fileBaseName == targetBaseName || fileRelName == targetLower {
			continue
		}

		fileBacklinks := scanFileForRenameBacklinks(filePath, relPath, targetBaseName, targetLower)
		backlinks = append(backlinks, fileBacklinks...)
	}
	return backlinks
}

func scanFileForRenameBacklinks(filePath, relPath, targetBaseName, targetLower string) []BacklinkResult {
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

		matches := vault.WikilinkRegex.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) <= 1 {
				continue
			}

			linkTarget := strings.ToLower(vault.NormalizeLink(match[1]))
			linkBaseName := strings.ToLower(filepath.Base(linkTarget))

			if linkBaseName == targetBaseName || linkTarget == targetLower {
				results = append(results, BacklinkResult{
					SourceFile: relPath,
					Line:       lineNum,
					Context:    line,
				})
				break // One result per line is sufficient
			}
		}
	}
	return results
}

func computeNewLinkContent(line, oldName, newName string) string {
	// Replace the old link with new link, preserving aliases
	// [[old-name]] -> [[new-name]]
	// [[old-name|alias]] -> [[new-name|alias]]
	// [[path/old-name]] -> [[new-name]] (update to new path)

	oldBase := filepath.Base(oldName)
	newBase := filepath.Base(newName)

	result := line

	// Find all wikilinks and replace those matching the old name
	matches := vault.WikilinkRegex.FindAllStringSubmatchIndex(line, -1)

	// Process matches in reverse order to preserve indices
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if len(match) < 4 {
			continue
		}

		// Extract the link target (group 1)
		linkStart := match[2]
		linkEnd := match[3]
		linkTarget := line[linkStart:linkEnd]

		// Check if this link matches the old name
		normalizedTarget := vault.NormalizeLink(linkTarget)
		targetBase := filepath.Base(normalizedTarget)

		if strings.EqualFold(targetBase, oldBase) ||
			strings.EqualFold(normalizedTarget, oldName) {

			// Determine the new link text
			var newLink string
			if strings.Contains(linkTarget, "/") {
				// Path-based link: replace path/old with newName (simplified)
				newLink = newName
			} else {
				// Simple link: just use new base name
				newLink = newBase
			}

			// Preserve heading/block references if present
			if idx := strings.Index(linkTarget, "#"); idx != -1 {
				newLink += linkTarget[idx:]
			} else if idx := strings.Index(linkTarget, "^"); idx != -1 {
				newLink += linkTarget[idx:]
			}

			// Replace in result
			result = result[:linkStart] + newLink + result[linkEnd:]
		}
	}

	return result
}

func printRenamePreview(result *RenameResult, elapsed time.Duration) {
	fmt.Printf("%s Rename Preview\n\n", colors.Green("→"))
	fmt.Printf("  Source: %s\n", colors.Cyan(result.SourceFile))
	fmt.Printf("  Dest:   %s\n", colors.Green(result.DestFile))
	fmt.Printf("  Backlinks: %d in %d files\n\n", result.LinksUpdated, result.FilesModified)

	if len(result.Changes) > 0 {
		fmt.Printf("  %s Link Updates:\n", colors.Yellow("!"))

		// Group by file
		byFile := make(map[string][]RenameChange)
		for _, c := range result.Changes {
			byFile[c.File] = append(byFile[c.File], c)
		}

		for _, file := range sortedKeys(byFile) {
			changes := byFile[file]
			fmt.Printf("    %s\n", colors.Cyan(file))
			for _, c := range changes {
				fmt.Printf("      :%d %s\n", c.Line, colors.Dim(truncateRunes(c.OldContent, 60)))
				fmt.Printf("         %s %s\n", colors.Green("→"), colors.Dim(truncateRunes(c.NewContent, 60)))
			}
		}
		fmt.Println()
	}

	fmt.Printf("  %s %s\n", colors.Cyan("Analyzed in:"), elapsed.Round(time.Millisecond))
}

func executeRename(absPath, sourceFile, destFile string, changes []RenameChange, oldName, newName string) error {
	fmt.Printf("\n%s Executing rename...\n\n", colors.Cyan("=>"))

	// Group changes by file to process each file only once
	// This prevents data loss when a file has multiple backlinks to the renamed note
	changesByFile := make(map[string]bool)
	for _, change := range changes {
		changesByFile[change.File] = true
	}

	// Update backlinks first (before renaming the file)
	linksUpdated := 0
	for file := range changesByFile {
		fullPath := filepath.Join(absPath, file)

		// Read the file once
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", file, err)
		}

		// Get original permissions to preserve them
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", file, err)
		}

		// Update ALL links in the file content at once
		newContent := computeNewLinkContent(string(content), oldName, newName)

		// Write back with original permissions
		if err := os.WriteFile(fullPath, []byte(newContent), info.Mode()); err != nil {
			return fmt.Errorf("failed to write %s (NOTE: %d files already modified): %w", file, linksUpdated, err)
		}
		linksUpdated++
	}

	// Security: Validate destination directory is within vault before creating
	destDir := filepath.Dir(destFile)
	if !isPathWithinVault(destDir, absPath) {
		return fmt.Errorf("destination directory escapes vault boundary")
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Rename the file
	if err := os.Rename(sourceFile, destFile); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	relDest, _ := filepath.Rel(absPath, destFile)
	fmt.Printf("  %s Renamed: %s\n", colors.Green("✓"), relDest)
	fmt.Printf("  %s Updated links in %d files\n\n", colors.Green("✓"), len(changesByFile))

	return nil
}
