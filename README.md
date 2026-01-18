# obsidian-cli

Fast CLI for Obsidian vault operations. Built in Go for speed - 20-40x faster than Python equivalents.

## Features

- **Concurrent scanning** - Uses goroutines for parallel file processing
- **Health checks** - Detect orphan files, dead links, and frontmatter issues
- **Vault statistics** - Breakdown by folder with visual bar charts
- **Orphan listing** - Find and export unlinked files
- **Dead link listing** - Export broken links (JSON, CSV, text)
- **Backlink search** - Find all notes linking to a specific note
- **Tag discovery** - List all tags with counts, filter notes by tag
- **Full-text search** - Search across notes with regex support
- **Safe rename** - Rename notes and update all backlinks automatically
- **Unused assets** - Find orphaned images, PDFs, and media files
- **Security hardened** - Path traversal and symlink escape protection

## Installation

```bash
go install github.com/kofifort/obsidian-cli@latest
```

Or build from source:

```bash
git clone https://github.com/kofifort/obsidian-cli.git
cd obsidian-cli
go build -o obsidian-cli .
```

## Usage

### Health Check

```bash
obsidian-cli health --vault ~/Documents/Obsidian
```

Output:
```
=> Scanning vault: /Users/you/Documents/Obsidian

✓ Vault Health Check

  Notes: 5,847
  Orphans: 12
  Dead Links: 3
  Frontmatter Issues: 0

  Scanned in: 312ms (6,203 files)
```

### Statistics

```bash
obsidian-cli stats --vault ~/Documents/Obsidian
```

Output:
```
=> Scanning vault: /Users/you/Documents/Obsidian

📊 Vault Statistics

  Total Notes: 5,847

  By Folder:
    concepts        ████████████████████ (2,090)
    sources         ████████████ (1,432)
    insights        ██████████ (1,200)
    Media           ███ (548)
    ...

  Summary:
    Total files:      6,203
    Markdown files:   5,847
    Directories:      142
    Top-level folders: 12

  Health:
    Orphan files:     12
    Dead links:       3
    No frontmatter:   5

  Scanned in: 298ms
```

### Tags

List all tags or find notes by tag:

```bash
# List all tags with counts
obsidian-cli tags --vault ~/Documents/Obsidian

# Find notes with a specific tag
obsidian-cli tags --vault ~/Documents/Obsidian --tag project
```

Output:
```
# Tags (4150 unique)

  #concept (3398) ██████████████████████████████████████████████████
  #book    (68)   ██████████████████████████████████████████████████
  #project (45)   ████████████████████████████████████████████
```

### Search

Full-text search across notes:

```bash
# Simple search
obsidian-cli search "authentication" --vault ~/Documents/Obsidian

# Regex search
obsidian-cli search "func.*Error" --vault ~/Documents/Obsidian --regex

# Case-sensitive with context
obsidian-cli search "TODO" --vault ~/Documents/Obsidian --case-sensitive --context 2
```

### Rename

Rename a note and update all backlinks:

```bash
# Preview changes (dry run)
obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian --dry-run

# Execute rename
obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian
```

### Unused Assets

Find images, PDFs, and media not referenced in any note:

```bash
obsidian-cli unused-assets --vault ~/Documents/Obsidian
```

Output:
```
! Unused Assets (21 of 356 assets, 20.3 MB)

  image (15 files, 12.1 MB)
    attachments/old-screenshot.png 2.3 MB
    attachments/unused-diagram.svg 1.1 MB
    ...
```

## Performance

| Vault Size | Python (typical) | obsidian-cli |
|------------|------------------|--------------|
| 1,000 files | ~2s | ~50ms |
| 5,000 files | ~10s | ~200ms |
| 10,000 files | ~20s | ~400ms |

## How It Works

1. **Concurrent file discovery** - `filepath.WalkDir` collects all markdown files
2. **Worker pool** - Configurable workers (capped at 8) process files in parallel
3. **Wikilink parsing** - Regex extraction of `[[links]]` and `![[embeds]]`
4. **Orphan detection** - Files with zero incoming links (excluding special files)
5. **Dead link detection** - Links pointing to non-existent files
6. **Folder link support** - Recognizes `[[folder/]]` links as valid

## Security

- Path normalization with `filepath.Clean()` prevents traversal attacks
- Symlink boundary checking prevents escape from vault directory
- Case-insensitive matching mirrors Obsidian's behavior

## License

MIT
