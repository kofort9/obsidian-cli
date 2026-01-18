package cmd

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"

	"github.com/kofifort/obsidian-cli/internal/vault"
	"github.com/spf13/cobra"
)

var (
	deadlinksLimit  int
	deadlinksFormat string
	deadlinksGroup  string
)

var deadlinksCmd = &cobra.Command{
	Use:   "deadlinks",
	Short: "List dead links (broken [[wikilinks]])",
	Long: `Lists all broken internal links in your vault.

Dead links are [[wikilinks]] that point to non-existent files.
This helps identify broken references that need to be fixed or removed.

Examples:
  obsidian-cli deadlinks --vault ~/Documents/Obsidian
  obsidian-cli deadlinks --vault ~/Documents/Obsidian --limit 50
  obsidian-cli deadlinks --vault ~/Documents/Obsidian --group target
  obsidian-cli deadlinks --vault ~/Documents/Obsidian --format json`,
	RunE: runDeadlinks,
}

func init() {
	rootCmd.AddCommand(deadlinksCmd)
	deadlinksCmd.Flags().IntVarP(&deadlinksLimit, "limit", "n", 0, "Limit number of results (0 = no limit)")
	deadlinksCmd.Flags().StringVar(&deadlinksFormat, "format", "text", "Output format: text, json, csv")
	deadlinksCmd.Flags().StringVarP(&deadlinksGroup, "group", "g", "source", "Group by: source, target")
}

func runDeadlinks(cmd *cobra.Command, args []string) error {
	if deadlinksFormat == "text" {
		printScanHeader("Scanning vault")
	}

	scan, err := scanVaultWithTiming()
	if err != nil {
		return err
	}

	total := len(scan.DeadLinks)
	deadLinks := applyLimit(scan.DeadLinks, deadlinksLimit)

	switch deadlinksFormat {
	case "json":
		return encodeJSON(cmd, toJSONDeadLinks(deadLinks))

	case "csv":
		return writeDeadLinksCSV(cmd, deadLinks)

	default:
		printDeadLinksText(deadLinks, total)
		printLimitNote(total, deadlinksLimit)
		printScanFooter(scan.Elapsed)
	}

	return nil
}

type jsonDeadLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Line   int    `json:"line"`
}

func toJSONDeadLinks(deadLinks []vault.DeadLink) []jsonDeadLink {
	result := make([]jsonDeadLink, len(deadLinks))
	for i, dl := range deadLinks {
		result[i] = jsonDeadLink{
			Source: dl.SourceFile,
			Target: dl.Target,
			Line:   dl.Line,
		}
	}
	return result
}

func writeDeadLinksCSV(cmd *cobra.Command, deadLinks []vault.DeadLink) error {
	w := csv.NewWriter(cmd.OutOrStdout())
	w.Write([]string{"source", "target", "line"})
	for _, dl := range deadLinks {
		w.Write([]string{dl.SourceFile, dl.Target, strconv.Itoa(dl.Line)})
	}
	w.Flush()
	return w.Error()
}

func printDeadLinksText(deadLinks []vault.DeadLink, total int) {
	fmt.Printf("%s Dead Links %s\n\n", colors.Red("!"), colors.Dim(fmt.Sprintf("(%d total)", total)))

	if len(deadLinks) == 0 {
		fmt.Println("  No dead links found.")
		return
	}

	if deadlinksGroup == "target" {
		printDeadLinksByTarget(deadLinks)
	} else {
		printDeadLinksBySource(deadLinks)
	}
}

func printDeadLinksByTarget(deadLinks []vault.DeadLink) {
	byTarget := make(map[string][]vault.DeadLink)
	for _, dl := range deadLinks {
		byTarget[dl.Target] = append(byTarget[dl.Target], dl)
	}

	// Sort by frequency (most referenced first)
	type targetCount struct {
		target string
		count  int
	}
	targets := make([]targetCount, 0, len(byTarget))
	for t, links := range byTarget {
		targets = append(targets, targetCount{t, len(links)})
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].count > targets[j].count
	})

	for _, tc := range targets {
		links := byTarget[tc.target]
		fmt.Printf("  %s %s\n", colors.Red("[["+tc.target+"]]"), colors.Dim(fmt.Sprintf("(%d references)", tc.count)))
		for _, dl := range links {
			fmt.Printf("    %s:%d\n", dl.SourceFile, dl.Line)
		}
		fmt.Println()
	}
}

func printDeadLinksBySource(deadLinks []vault.DeadLink) {
	bySource := make(map[string][]vault.DeadLink)
	for _, dl := range deadLinks {
		bySource[dl.SourceFile] = append(bySource[dl.SourceFile], dl)
	}

	for _, source := range sortedKeys(bySource) {
		links := bySource[source]
		fmt.Printf("  %s %s\n", colors.Cyan(source), colors.Dim(fmt.Sprintf("(%d)", len(links))))
		for _, dl := range links {
			fmt.Printf("    :%d -> %s\n", dl.Line, colors.Red("[["+dl.Target+"]]"))
		}
		fmt.Println()
	}
}
