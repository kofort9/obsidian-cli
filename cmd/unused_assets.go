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
	unusedFormat string
	unusedLimit  int
)

var unusedAssetsCmd = &cobra.Command{
	Use:   "unused-assets",
	Short: "Find assets (images, PDFs) not embedded anywhere",
	Long: `Lists all non-markdown files that aren't referenced in any note.

Detects references via:
  - Embed syntax: ![[image.png]]
  - Link syntax: [[document.pdf]]
  - Markdown images: ![alt](image.png)

Supported asset types:
  Images: .png, .jpg, .jpeg, .gif, .svg, .webp, .bmp, .ico
  Documents: .pdf, .doc, .docx, .xls, .xlsx, .ppt, .pptx
  Media: .mp3, .mp4, .wav, .mov, .webm, .ogg
  Archives: .zip, .tar, .gz, .rar

Examples:
  obsidian-cli unused-assets --vault ~/Documents/Obsidian
  obsidian-cli unused-assets --vault ~/Documents/Obsidian --limit 20
  obsidian-cli unused-assets --vault ~/Documents/Obsidian --format json
  obsidian-cli unused-assets --vault ~/Documents/Obsidian --format paths`,
	RunE: runUnusedAssets,
}

func init() {
	rootCmd.AddCommand(unusedAssetsCmd)
	unusedAssetsCmd.Flags().StringVar(&unusedFormat, "format", "text", "Output format: text, json, paths")
	unusedAssetsCmd.Flags().IntVarP(&unusedLimit, "limit", "n", 0, "Limit number of results (0 = no limit)")
}

// AssetInfo represents an unused asset file.
type AssetInfo struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SizeHuman string `json:"size_human"`
	Type     string `json:"type"`
}

// UnusedAssetsResult holds the scan results.
type UnusedAssetsResult struct {
	TotalAssets    int         `json:"total_assets"`
	UnusedAssets   []AssetInfo `json:"unused_assets"`
	TotalSize      int64       `json:"total_size"`
	TotalSizeHuman string      `json:"total_size_human"`
	Elapsed        time.Duration `json:"-"`
}

var (
	// Asset extensions to track
	assetExtensions = map[string]string{
		// Images
		".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image",
		".svg": "image", ".webp": "image", ".bmp": "image", ".ico": "image",
		// Documents
		".pdf": "document", ".doc": "document", ".docx": "document",
		".xls": "document", ".xlsx": "document", ".ppt": "document", ".pptx": "document",
		// Media
		".mp3": "media", ".mp4": "media", ".wav": "media", ".mov": "media",
		".webm": "media", ".ogg": "media", ".m4a": "media", ".flac": "media",
		// Archives
		".zip": "archive", ".tar": "archive", ".gz": "archive", ".rar": "archive",
		// Other common
		".csv": "data", ".json": "data", ".xml": "data",
	}

	// Matches ![[embed]] and [[link]] syntax
	embedRegex = regexp.MustCompile(`!?\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
	// Matches markdown image syntax ![alt](path)
	markdownImageRegex = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
)

func runUnusedAssets(cmd *cobra.Command, args []string) error {
	if unusedFormat == "text" {
		printScanHeader("Scanning for unused assets")
	}

	result, err := scanUnusedAssets()
	if err != nil {
		return err
	}

	return outputUnusedAssets(cmd, result)
}

func scanUnusedAssets() (*UnusedAssetsResult, error) {
	start := time.Now()

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("invalid vault path: %w", err)
	}

	// Phase 1: Collect all assets and markdown files
	var assets []string
	var mdFiles []string

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
		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".md" {
			mdFiles = append(mdFiles, path)
		} else if _, isAsset := assetExtensions[ext]; isAsset {
			assets = append(assets, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	// Phase 2: Build set of referenced assets
	referenced := collectReferencedAssets(absPath, mdFiles)

	// Phase 3: Find unused assets
	var unused []AssetInfo
	var totalSize int64

	for _, assetPath := range assets {
		relPath, _ := filepath.Rel(absPath, assetPath)
		assetName := filepath.Base(assetPath)
		assetNameLower := strings.ToLower(assetName)
		relPathLower := strings.ToLower(relPath)

		// Check if referenced (case-insensitive)
		if referenced[assetNameLower] || referenced[relPathLower] {
			continue
		}

		// Get file info
		info, err := os.Stat(assetPath)
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(assetPath))
		unused = append(unused, AssetInfo{
			Path:      relPath,
			Size:      info.Size(),
			SizeHuman: humanizeBytes(info.Size()),
			Type:      assetExtensions[ext],
		})
		totalSize += info.Size()
	}

	// Sort by size (largest first)
	sort.Slice(unused, func(i, j int) bool {
		return unused[i].Size > unused[j].Size
	})

	return &UnusedAssetsResult{
		TotalAssets:    len(assets),
		UnusedAssets:   unused,
		TotalSize:      totalSize,
		TotalSizeHuman: humanizeBytes(totalSize),
		Elapsed:        time.Since(start),
	}, nil
}

func collectReferencedAssets(absPath string, mdFiles []string) map[string]bool {
	referenced := make(map[string]bool)

	for _, mdFile := range mdFiles {
		file, err := os.Open(mdFile)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()

			// Find wikilink embeds and links
			matches := embedRegex.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				if len(match) > 1 {
					target := strings.ToLower(match[1])
					// Store both full path and basename
					referenced[target] = true
					referenced[strings.ToLower(filepath.Base(target))] = true
				}
			}

			// Find markdown image syntax
			imgMatches := markdownImageRegex.FindAllStringSubmatch(line, -1)
			for _, match := range imgMatches {
				if len(match) > 1 {
					target := strings.ToLower(match[1])
					// Skip URLs
					if !strings.HasPrefix(target, "http") {
						referenced[target] = true
						referenced[strings.ToLower(filepath.Base(target))] = true
					}
				}
			}
		}
		// Check for scanner errors (buffer overflow, etc.)
		if err := scanner.Err(); err != nil {
			file.Close()
			continue
		}
		file.Close()
	}

	return referenced
}

func humanizeBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func outputUnusedAssets(cmd *cobra.Command, result *UnusedAssetsResult) error {
	total := len(result.UnusedAssets)
	assets := applyLimit(result.UnusedAssets, unusedLimit)

	switch unusedFormat {
	case "json":
		return encodeJSON(cmd, result)

	case "paths":
		for _, a := range assets {
			fmt.Println(filepath.Join(vaultPath, a.Path))
		}

	default:
		fmt.Printf("%s Unused Assets %s\n\n",
			colors.Yellow("!"),
			colors.Dim(fmt.Sprintf("(%d of %d assets, %s)", total, result.TotalAssets, result.TotalSizeHuman)))

		if len(assets) == 0 {
			fmt.Println("  No unused assets found.")
			return nil
		}

		// Group by type
		byType := make(map[string][]AssetInfo)
		for _, a := range assets {
			byType[a.Type] = append(byType[a.Type], a)
		}

		for _, assetType := range sortedKeys(byType) {
			typeAssets := byType[assetType]
			var typeSize int64
			for _, a := range typeAssets {
				typeSize += a.Size
			}

			fmt.Printf("  %s %s\n", colors.Cyan(assetType), colors.Dim(fmt.Sprintf("(%d files, %s)", len(typeAssets), humanizeBytes(typeSize))))

			for _, a := range typeAssets {
				fmt.Printf("    %s %s\n", a.Path, colors.Dim(a.SizeHuman))
			}
			fmt.Println()
		}

		printLimitNote(total, unusedLimit)
		printScanFooter(result.Elapsed)
	}

	return nil
}
