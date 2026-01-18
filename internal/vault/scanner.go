package vault

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// ScanResult holds the results of a vault scan
type ScanResult struct {
	TotalFiles      int64
	MarkdownFiles   int64
	Directories     int64
	Orphans         []string
	DeadLinks       []DeadLink
	FrontmatterErrs []string
	FilesByFolder   map[string]int64
	IncomingLinks   map[string]int // tracks incoming link count per file
	mu              sync.Mutex
}

// DeadLink represents a broken internal link
type DeadLink struct {
	SourceFile string
	Target     string
	Line       int
}

// FileInfo holds parsed info about a markdown file
type FileInfo struct {
	Path          string
	RelPath       string
	HasFrontmatter bool
	OutgoingLinks []string
	WordCount     int
}

var (
	// Matches [[wikilinks]] and [[wikilinks|alias]]
	wikilinkRegex = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
	// Matches YAML frontmatter
	frontmatterRegex = regexp.MustCompile(`(?s)^---\n.*?\n---`)
	// Matches embed syntax ![[file]]
	embedRegex = regexp.MustCompile(`!\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
)

// normalizeLink removes heading anchors (#) and block references (^) from links
// [[note#heading]] -> note, [[note^block-id]] -> note
func normalizeLink(link string) string {
	// Check for heading anchor first, then block reference
	if base, _, found := strings.Cut(link, "#"); found {
		return base
	}
	if base, _, found := strings.Cut(link, "^"); found {
		return base
	}
	return link
}

// isAssetFile checks if a filename is a non-markdown asset (image, PDF, etc.)
func isAssetFile(target string) bool {
	ext := strings.ToLower(filepath.Ext(target))
	assetExts := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".svg": true, ".webp": true, ".pdf": true, ".mp3": true,
		".mp4": true, ".wav": true, ".mov": true, ".zip": true,
	}
	return assetExts[ext]
}

// isExternalLink checks if a link is external (URL or protocol)
func isExternalLink(target string) bool {
	return strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:")
}

// isFolderLink checks if a link points to a folder (ends with /)
func isFolderLink(target string) bool {
	return strings.HasSuffix(target, "/")
}

// isPathWithinVault validates that a path is within the vault boundary (security check)
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

// ScanVault performs a concurrent scan of the vault
func ScanVault(vaultPath string) (*ScanResult, error) {
	// Validate vault path
	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, err
	}
	absPath = filepath.Clean(absPath) // Normalize path for security

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}

	result := &ScanResult{
		FilesByFolder: make(map[string]int64),
		IncomingLinks: make(map[string]int),
	}

	// Collect all markdown files and folders
	var mdFiles []string
	var folders []string
	err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors, continue scanning
		}

		// Skip hidden directories
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
			atomic.AddInt64(&result.Directories, 1)
			folders = append(folders, path)
			return nil
		}

		atomic.AddInt64(&result.TotalFiles, 1)

		if strings.HasSuffix(strings.ToLower(path), ".md") {
			mdFiles = append(mdFiles, path)
			atomic.AddInt64(&result.MarkdownFiles, 1)

			// Track by folder
			relPath, _ := filepath.Rel(absPath, path)
			folder := filepath.Dir(relPath)
			if folder == "." {
				folder = "root"
			} else {
				// Get top-level folder only
				parts := strings.Split(folder, string(filepath.Separator))
				folder = parts[0]
			}
			result.mu.Lock()
			result.FilesByFolder[folder]++
			result.mu.Unlock()
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build set of existing files for orphan/dead link detection
	// Use lowercase keys for case-insensitive matching (Obsidian behavior)
	existingFiles := make(map[string]bool)
	for _, f := range mdFiles {
		relPath, _ := filepath.Rel(absPath, f)
		// Store both with and without .md extension (lowercase for case-insensitive)
		baseName := strings.TrimSuffix(relPath, ".md")
		existingFiles[strings.ToLower(baseName)] = true
		existingFiles[strings.ToLower(relPath)] = true
		// Also store just the filename (for [[note]] style links)
		existingFiles[strings.ToLower(strings.TrimSuffix(filepath.Base(f), ".md"))] = true
	}

	// Build set of existing folders for folder-style link detection
	// Folder links like [[meta/session-logs/]] are valid Obsidian links
	existingFolders := make(map[string]bool)
	for _, f := range folders {
		relPath, _ := filepath.Rel(absPath, f)
		if relPath != "." {
			// Store with trailing slash (how folder links appear)
			existingFolders[strings.ToLower(relPath+"/")] = true
			existingFolders[strings.ToLower(relPath)] = true
		}
	}

	// Process files concurrently
	var wg sync.WaitGroup

	// Cap buffer size to prevent memory exhaustion on large vaults
	bufferSize := min(len(mdFiles), 1000)
	fileChan := make(chan string, bufferSize)

	// Worker count: min of (file count, CPU count, 8)
	numWorkers := min(len(mdFiles), min(runtime.NumCPU(), 8))

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range fileChan {
				processFile(path, absPath, existingFiles, existingFolders, result)
			}
		}()
	}

	// Send files to workers
	for _, f := range mdFiles {
		fileChan <- f
	}
	close(fileChan)

	wg.Wait()

	// Find orphans (files with no incoming links)
	// Use lowercase for case-insensitive matching
	for _, f := range mdFiles {
		relPath, _ := filepath.Rel(absPath, f)
		baseName := strings.ToLower(strings.TrimSuffix(relPath, ".md"))
		fileName := strings.ToLower(strings.TrimSuffix(filepath.Base(f), ".md"))

		// Check if file has any incoming links (keys stored lowercase)
		if result.IncomingLinks[baseName] == 0 &&
			result.IncomingLinks[strings.ToLower(relPath)] == 0 &&
			result.IncomingLinks[fileName] == 0 {
			// Skip special files
			if !strings.HasPrefix(filepath.Base(f), "_") &&
				!strings.HasPrefix(filepath.Base(f), "index") {
				result.Orphans = append(result.Orphans, relPath)
			}
		}
	}

	return result, nil
}

func processFile(path, vaultPath string, existingFiles, existingFolders map[string]bool, result *ScanResult) {
	// Security: Verify file is within vault before reading
	if !isPathWithinVault(path, vaultPath) {
		return
	}

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	relPath, _ := filepath.Rel(vaultPath, path)

	scanner := bufio.NewScanner(file)
	lineNum := 0
	hasFrontmatter := false
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check frontmatter only on first line (avoid storing full content)
		if lineNum == 1 && strings.HasPrefix(line, "---") {
			hasFrontmatter = true
		}

		// Find wikilinks on this line (both regular and embeds)
		allMatches := wikilinkRegex.FindAllStringSubmatch(line, -1)
		embedMatches := embedRegex.FindAllStringSubmatch(line, -1)
		allMatches = append(allMatches, embedMatches...)

		for _, match := range allMatches {
			if len(match) > 1 {
				target := match[1]

				// Normalize: remove heading anchors and block references
				target = normalizeLink(target)

				// Skip empty targets (e.g., [[#heading]] becomes empty)
				if target == "" {
					continue
				}

				// Skip external links (URLs, mailto:, etc.)
				if isExternalLink(target) {
					continue
				}

				// Use lowercase for case-insensitive matching
				targetLower := strings.ToLower(target)

				// Track incoming link (lowercase)
				result.mu.Lock()
				result.IncomingLinks[targetLower]++
				result.mu.Unlock()

				// Skip asset files (images, PDFs) - they're not in existingFiles
				if isAssetFile(target) {
					continue
				}

				// Skip folder links that point to existing folders
				if isFolderLink(target) {
					if existingFolders[targetLower] {
						continue
					}
					// Folder link to non-existent folder is a dead link
					result.mu.Lock()
					result.DeadLinks = append(result.DeadLinks, DeadLink{
						SourceFile: relPath,
						Target:     target,
						Line:       lineNum,
					})
					result.mu.Unlock()
					continue
				}

				// Check if target exists (case-insensitive via lowercase keys)
				if !existingFiles[targetLower] &&
					!existingFiles[targetLower+".md"] {
					result.mu.Lock()
					result.DeadLinks = append(result.DeadLinks, DeadLink{
						SourceFile: relPath,
						Target:     target,
						Line:       lineNum,
					})
					result.mu.Unlock()
				}
			}
		}
	}

	// Check for scanner errors (e.g., lines too long)
	if err := scanner.Err(); err != nil {
		return // Skip file if scanner encountered errors
	}

	// Track files without frontmatter
	if !hasFrontmatter {
		result.mu.Lock()
		result.FrontmatterErrs = append(result.FrontmatterErrs, relPath)
		result.mu.Unlock()
	}
}
