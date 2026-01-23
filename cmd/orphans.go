package cmd

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var (
	orphansLimit  int
	orphansFolder string
	orphansFormat string
)

var orphansCmd = &cobra.Command{
	Use:   "orphans",
	Short: "List orphan files (no incoming links)",
	Long: `Lists all markdown files in your vault that have no incoming links.

Orphan files are notes that aren't linked from any other note,
which may indicate unused or forgotten content.

Examples:
  obsidian-cli orphans --vault ~/Documents/Obsidian
  obsidian-cli orphans --vault ~/Documents/Obsidian --limit 20
  obsidian-cli orphans --vault ~/Documents/Obsidian --folder concepts
  obsidian-cli orphans --vault ~/Documents/Obsidian --format json`,
	RunE: runOrphans,
}

func init() {
	rootCmd.AddCommand(orphansCmd)
	orphansCmd.Flags().IntVarP(&orphansLimit, "limit", "n", 0, "Limit number of results (0 = no limit)")
	orphansCmd.Flags().StringVarP(&orphansFolder, "folder", "f", "", "Filter by top-level folder")
	orphansCmd.Flags().StringVar(&orphansFormat, "format", "text", "Output format: text, json, paths")
}

func runOrphans(cmd *cobra.Command, args []string) error {
	if err := RequireVault(); err != nil {
		return err
	}

	if orphansFormat == "text" {
		printScanHeader("Scanning vault")
	}

	scan, err := scanVaultWithTiming()
	if err != nil {
		return err
	}

	orphans := filterByFolder(scan.Orphans, orphansFolder)
	sort.Strings(orphans)

	total := len(orphans)
	orphans = applyLimit(orphans, orphansLimit)

	switch orphansFormat {
	case "json":
		return encodeJSON(cmd, orphans)

	case "paths":
		for _, o := range orphans {
			fmt.Println(filepath.Join(vaultPath, o))
		}

	default:
		printOrphansText(orphans, total)
		printLimitNote(total, orphansLimit)
		printScanFooter(scan.Elapsed)
	}

	return nil
}

func printOrphansText(orphans []string, total int) {
	fmt.Printf("%s Orphan Files %s\n\n", colors.Yellow("!"), colors.Dim(fmt.Sprintf("(%d total)", total)))

	if len(orphans) == 0 {
		fmt.Println("  No orphans found.")
		return
	}

	byFolder := groupByFolder(orphans)
	for _, folder := range sortedKeys(byFolder) {
		files := byFolder[folder]
		fmt.Printf("  %s %s\n", colors.Cyan(folder+"/"), colors.Dim(fmt.Sprintf("(%d)", len(files))))
		for _, f := range files {
			fmt.Printf("    %s\n", filepath.Base(f))
		}
		fmt.Println()
	}
}
