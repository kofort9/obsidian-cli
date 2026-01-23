package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var vaultPath string

var rootCmd = &cobra.Command{
	Use:   "obsidian-cli",
	Short: "Fast CLI for Obsidian vault operations",
	Long: `obsidian-cli is a high-performance command-line tool for Obsidian vaults.

Built in Go for speed - 20-40x faster than Python equivalents.
Uses concurrent file scanning for large vaults.

Examples:
  obsidian-cli health --vault ~/Documents/Obsidian
  obsidian-cli stats --vault ~/Documents/Obsidian`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&vaultPath, "vault", "v", "", "Path to Obsidian vault (required for most commands)")
	// Note: vault is not globally required because the 'patterns' command doesn't need it.
	// Commands that need vault should validate it in their RunE function.
}

// RequireVault validates that the vault flag was provided.
// Commands that need vault should call this at the start of their RunE function.
func RequireVault() error {
	if vaultPath == "" {
		return fmt.Errorf("required flag \"vault\" not set")
	}
	return nil
}
