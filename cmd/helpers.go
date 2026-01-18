package cmd

import (
	"encoding/json"
	"fmt"
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

// truncateRunes truncates a string to at most n runes, adding "..." if truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}
