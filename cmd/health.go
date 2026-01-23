package cmd

import (
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Run vault health check",
	Long: `Performs a quick health check on your Obsidian vault.

Checks for:
  - Total note count
  - Orphan files (no incoming links)
  - Dead links (broken [[wikilinks]])
  - Missing frontmatter

Example:
  obsidian-cli health --vault ~/Documents/Obsidian`,
	RunE: runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
}

func runHealth(cmd *cobra.Command, args []string) error {
	if err := RequireVault(); err != nil {
		return err
	}

	green := color.New(color.FgGreen).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	fmt.Printf("\n%s Scanning vault: %s\n\n", cyan("=>"), vaultPath)

	start := time.Now()
	result, err := vault.ScanVault(vaultPath)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}
	elapsed := time.Since(start)

	// Determine overall health
	issues := len(result.Orphans) + len(result.DeadLinks) + len(result.FrontmatterErrs)
	var statusIcon string
	if issues == 0 {
		statusIcon = green("✓")
	} else if issues < 10 {
		statusIcon = yellow("!")
	} else {
		statusIcon = red("✗")
	}

	fmt.Printf("%s %s\n\n", statusIcon, bold("Vault Health Check"))

	// Summary stats
	fmt.Printf("  %s %d\n", cyan("Notes:"), result.MarkdownFiles)

	// Helper to format count with color based on severity
	formatCount := func(count int, warnColor, okColor func(a ...interface{}) string) string {
		if count == 0 {
			return okColor("0")
		}
		return warnColor(fmt.Sprintf("%d", count))
	}

	orphanCount := len(result.Orphans)
	deadLinkCount := len(result.DeadLinks)
	fmErrCount := len(result.FrontmatterErrs)

	fmt.Printf("  %s %s\n", cyan("Orphans:"), formatCount(orphanCount, yellow, green))
	fmt.Printf("  %s %s\n", cyan("Dead Links:"), formatCount(deadLinkCount, red, green))
	fmt.Printf("  %s %s\n", cyan("Frontmatter Issues:"), formatCount(fmErrCount, yellow, green))

	// Show dead link details if any exist
	if deadLinkCount > 0 {
		showCount := deadLinkCount
		if showCount > 10 {
			showCount = 10
			fmt.Printf("\n  %s (%d total, showing first 10)\n", bold("Dead Links:"), deadLinkCount)
		} else {
			fmt.Printf("\n  %s\n", bold("Dead Links:"))
		}
		for i := 0; i < showCount; i++ {
			dl := result.DeadLinks[i]
			fmt.Printf("    %s:%d -> [[%s]]\n", dl.SourceFile, dl.Line, dl.Target)
		}
	}

	// Performance info
	fmt.Printf("\n  %s %s (%d files)\n", cyan("Scanned in:"), elapsed.Round(time.Millisecond), result.TotalFiles)

	// Return error if critical issues found (allows Cobra to handle exit)
	if deadLinkCount > 0 {
		return fmt.Errorf("vault has %d dead links", deadLinkCount)
	}

	return nil
}
