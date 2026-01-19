package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

// colors provides pre-configured color functions for consistent output styling.
var colors = struct {
	Cyan   func(a ...interface{}) string
	Green  func(a ...interface{}) string
	Yellow func(a ...interface{}) string
	Red    func(a ...interface{}) string
	Dim    func(a ...interface{}) string
}{
	Cyan:   color.New(color.FgCyan).SprintFunc(),
	Green:  color.New(color.FgGreen).SprintFunc(),
	Yellow: color.New(color.FgYellow).SprintFunc(),
	Red:    color.New(color.FgRed).SprintFunc(),
	Dim:    color.New(color.Faint).SprintFunc(),
}

// scanResult holds the result of a vault scan along with timing info.
type scanResult struct {
	*vault.ScanResult
	Elapsed time.Duration
}

// scanVaultWithTiming scans the vault and returns the result with elapsed time.
func scanVaultWithTiming() (*scanResult, error) {
	start := time.Now()
	result, err := vault.ScanVault(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("scan returned nil result")
	}
	return &scanResult{
		ScanResult: result,
		Elapsed:    time.Since(start),
	}, nil
}

// printScanHeader prints a consistent header when starting a scan.
func printScanHeader(message string) {
	fmt.Printf("\n%s %s: %s\n\n", colors.Cyan("=>"), message, vaultPath)
}

// printScanFooter prints scan timing information.
func printScanFooter(elapsed time.Duration) {
	fmt.Printf("  %s %s\n", colors.Cyan("Scanned in:"), elapsed.Round(time.Millisecond))
}

// printLimitNote prints a note about truncated results if applicable.
func printLimitNote(total, limit int) {
	if limit > 0 && total > limit {
		fmt.Printf("  %s\n\n", colors.Dim(fmt.Sprintf("...and %d more (use --limit 0 to show all)", total-limit)))
	}
}

// encodeJSON writes data as indented JSON to the command's output.
func encodeJSON(cmd *cobra.Command, data interface{}) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// applyLimit truncates a slice to the specified limit. Returns the original slice if limit is 0.
func applyLimit[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// groupByFolder groups file paths by their top-level folder.
// Files in the root directory are grouped under "root".
func groupByFolder(paths []string) map[string][]string {
	byFolder := make(map[string][]string)
	for _, p := range paths {
		parts := strings.Split(p, string(filepath.Separator))
		folder := "root"
		if len(parts) > 1 {
			folder = parts[0]
		}
		byFolder[folder] = append(byFolder[folder], p)
	}
	return byFolder
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// filterByFolder returns only paths that are in the specified top-level folder.
func filterByFolder(paths []string, folder string) []string {
	if folder == "" {
		return paths
	}
	folderLower := strings.ToLower(folder)
	var filtered []string
	for _, p := range paths {
		parts := strings.Split(p, string(filepath.Separator))
		if len(parts) > 0 && strings.ToLower(parts[0]) == folderLower {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// isPathWithinVault validates that a path is within the vault boundary (security check).
func isPathWithinVault(path, vaultPath string) bool {
	absVault, err := filepath.Abs(vaultPath)
	if err != nil {
		return false
	}
	absVault = filepath.Clean(absVault)

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)

	// Use path separator to prevent prefix attacks
	vaultPrefix := absVault + string(filepath.Separator)
	return absPath == absVault || strings.HasPrefix(absPath, vaultPrefix)
}

// shouldSkipEntry checks if a directory entry should be skipped during vault traversal.
// Returns true if the entry is a hidden directory, an unresolvable symlink, or a symlink
// pointing outside the vault boundary.
func shouldSkipEntry(path string, d os.DirEntry, absVaultPath string) (skip bool, skipDir bool) {
	// Skip hidden directories
	if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
		return true, true
	}

	// Security: Check for symlinks that escape vault boundary
	if d.Type()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return true, false // Skip unresolvable symlinks
		}
		if !isPathWithinVault(target, absVaultPath) {
			return true, false // Skip symlinks pointing outside vault
		}
	}

	return false, false
}

// truncateRunes truncates a string to at most n runes, adding "..." if truncated.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

// newLargeScanner creates a bufio.Scanner with a 1MB buffer limit.
// The default 64KB limit can cause "token too long" errors on files
// with very long lines (e.g., embedded base64, long URLs).
func newLargeScanner(file *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // Max 1MB per line
	return scanner
}

// mustRelPath returns a relative path or the original path if relativization fails.
func mustRelPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

// findNoteFile finds a note by name within the vault, supporting case-insensitive matching.
// noteName can be a basename ("my-note") or a relative path ("concepts/my-note").
// Returns an error if multiple files match the basename (use full path to disambiguate).
func findNoteFile(absPath, noteName string) (string, error) {
	noteLower := strings.ToLower(noteName)
	noteBaseLower := strings.ToLower(filepath.Base(noteName))

	var exactMatch string    // Exact path match
	var baseMatches []string // Basename-only matches

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

			// Exact path match takes priority
			if strings.ToLower(relName) == noteLower {
				exactMatch = path
				return filepath.SkipAll // Found exact match, stop searching
			}

			// Track basename matches
			if strings.ToLower(baseName) == noteBaseLower {
				baseMatches = append(baseMatches, relPath)
			}
		}
		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Exact path match always wins
	if exactMatch != "" {
		// Security: Verify symlinks don't escape vault
		info, err := os.Lstat(exactMatch)
		if err != nil {
			return "", fmt.Errorf("cannot access %s: %w", noteName, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(exactMatch)
			if err != nil {
				return "", fmt.Errorf("cannot resolve symlink %s: %w", noteName, err)
			}
			if !isPathWithinVault(target, absPath) {
				return "", fmt.Errorf("note symlink escapes vault: %s", noteName)
			}
		}
		return exactMatch, nil
	}

	// Handle basename matches
	if len(baseMatches) == 0 {
		return "", fmt.Errorf("note not found: %s", noteName)
	}
	if len(baseMatches) > 1 {
		return "", fmt.Errorf("ambiguous note name %q matches multiple files: %v (use full path to disambiguate)", noteName, baseMatches)
	}

	foundPath := filepath.Join(absPath, baseMatches[0])

	// Security: Verify symlinks don't escape vault
	info, err := os.Lstat(foundPath)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", baseMatches[0], err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(foundPath)
		if err != nil {
			return "", fmt.Errorf("cannot resolve symlink %s: %w", baseMatches[0], err)
		}
		if !isPathWithinVault(target, absPath) {
			return "", fmt.Errorf("note symlink escapes vault: %s", baseMatches[0])
		}
	}

	return foundPath, nil
}
