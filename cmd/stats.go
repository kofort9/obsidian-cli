package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show vault statistics",
	Long: `Displays detailed statistics about your Obsidian vault.

Shows:
  - Total note count
  - Breakdown by top-level folder
  - File type distribution

Example:
  obsidian-cli stats --vault ~/Documents/Obsidian`,
	RunE: runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func runStats(cmd *cobra.Command, args []string) error {
	cyan := color.New(color.FgCyan).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	fmt.Printf("\n%s Scanning vault: %s\n\n", cyan("=>"), vaultPath)

	start := time.Now()
	result, err := vault.ScanVault(vaultPath)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}
	elapsed := time.Since(start)

	fmt.Printf("%s %s\n\n", "📊", bold("Vault Statistics"))

	// Total notes
	fmt.Printf("  %s %d\n", bold("Total Notes:"), result.MarkdownFiles)

	// Sort folders by count (descending)
	type folderCount struct {
		name  string
		count int64
	}
	var folders []folderCount
	for name, count := range result.FilesByFolder {
		folders = append(folders, folderCount{name, count})
	}
	sort.Slice(folders, func(i, j int) bool {
		return folders[i].count > folders[j].count
	})

	// Show folder breakdown
	fmt.Printf("\n  %s\n", bold("By Folder:"))

	// Show top 10 folders with bar chart
	displayCount := min(10, len(folders))
	maxCount := int64(1) // Avoid division by zero
	if len(folders) > 0 {
		maxCount = folders[0].count
	}

	for i := 0; i < displayCount; i++ {
		f := folders[i]
		barLen := max(1, int(float64(f.count)/float64(maxCount)*20))
		bar := strings.Repeat("█", barLen)

		name := f.name
		if len(name) > 15 {
			name = name[:12] + "..."
		}
		fmt.Printf("    %-15s %s %s\n", name, cyan(bar), dim(fmt.Sprintf("(%d)", f.count)))
	}

	if len(folders) > displayCount {
		remaining := len(folders) - displayCount
		var otherCount int64
		for i := displayCount; i < len(folders); i++ {
			otherCount += folders[i].count
		}
		fmt.Printf("    %-15s %s %s\n", fmt.Sprintf("...%d more", remaining), dim(""), dim(fmt.Sprintf("(%d)", otherCount)))
	}

	// Summary stats
	fmt.Printf("\n  %s\n", bold("Summary:"))
	fmt.Printf("    Total files:      %d\n", result.TotalFiles)
	fmt.Printf("    Markdown files:   %d\n", result.MarkdownFiles)
	fmt.Printf("    Directories:      %d\n", result.Directories)
	fmt.Printf("    Top-level folders: %d\n", len(folders))

	// Health indicators
	fmt.Printf("\n  %s\n", bold("Health:"))
	fmt.Printf("    Orphan files:     %d\n", len(result.Orphans))
	fmt.Printf("    Dead links:       %d\n", len(result.DeadLinks))
	fmt.Printf("    No frontmatter:   %d\n", len(result.FrontmatterErrs))

	// Performance
	fmt.Printf("\n  %s %s\n", cyan("Scanned in:"), elapsed.Round(time.Millisecond))

	return nil
}
