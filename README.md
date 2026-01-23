# obsidian-cli

Fast CLI for Obsidian vault operations. Built in Go for speed - 20-40x faster than Python equivalents.

## Features

- **Concurrent scanning** - Uses goroutines for parallel file processing
- **Health checks** - Detect orphan files, dead links, and frontmatter issues
- **Vault statistics** - Breakdown by folder with visual bar charts
- **Orphan listing** - Find and export unlinked files
- **Dead link listing** - Export broken links (JSON, CSV, text)
- **Backlink search** - Find all notes linking to a specific note
- **Outgoing links** - See what a note links to (valid vs dead)
- **Tag discovery** - List all tags with counts, filter notes by tag
- **Full-text search** - Search across notes with regex support
- **Safe rename** - Rename notes and update all backlinks automatically
- **Unused assets** - Find and delete orphaned images, PDFs, and media files
- **Pattern querying** - Query and manage Claude patterns with staleness decay and similarity search
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

# Filter to specific folder
obsidian-cli tags --vault ~/Documents/Obsidian --folder concepts
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

# Search within specific folder
obsidian-cli search "pattern" --vault ~/Documents/Obsidian --folder sources
```

### Links

Show outgoing links from a note (the inverse of backlinks):

```bash
# See all links from a note
obsidian-cli links "my-note" --vault ~/Documents/Obsidian

# Show only dead/broken links
obsidian-cli links "my-note" --vault ~/Documents/Obsidian --dead-only

# Include external URLs
obsidian-cli links "my-note" --vault ~/Documents/Obsidian --include-external
```

Output:
```
→ Links from: concepts/api-design.md (15 total)

  Valid (12):
    [[authentication]]
    [[rate-limiting]]
    [[error-handling]]

  Dead (2):
    [[deprecated-pattern]] (not found)
    [[old-concept]] (not found)

  External (1):
    https://example.com/docs
```

### Rename

Rename a note and update all backlinks:

```bash
# Preview changes (dry run)
obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian --dry-run

# Execute rename
obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian

# JSON output for scripting
obsidian-cli rename "old-note" "new-note" --vault ~/Documents/Obsidian --dry-run --format json
```

### Unused Assets

Find images, PDFs, and media not referenced in any note:

```bash
# List unused assets
obsidian-cli unused-assets --vault ~/Documents/Obsidian

# Delete unused assets (with confirmation)
obsidian-cli unused-assets --vault ~/Documents/Obsidian --delete

# Export paths for scripting
obsidian-cli unused-assets --vault ~/Documents/Obsidian --format paths
```

Output:
```
! Unused Assets (21 of 356 assets, 20.3 MB)

  image (15 files, 12.1 MB)
    attachments/old-screenshot.png 2.3 MB
    attachments/unused-diagram.svg 1.1 MB
    ...

  ? Delete 21 files (20.3 MB)? [y/N]: y

  ✓ Deleted 21 files, freed 20.3 MB
```

### Patterns

Query and manage Claude Code patterns (uses `--patterns-dir` instead of `--vault`):

```bash
# List recent patterns
obsidian-cli patterns --patterns-dir ~/.claude/patterns

# Filter by domain and type
obsidian-cli patterns --domain workflow --type success --limit 10

# Search by keywords
obsidian-cli patterns --keywords "batch parallel"

# Find similar patterns
obsidian-cli patterns --similar "error handling in API calls"

# Show patterns from last 7 days with staleness info
obsidian-cli patterns --recent 7 --verbose

# View statistics
obsidian-cli patterns --stats

# Log a surfacing event (track which patterns were shown)
obsidian-cli patterns --log-action accept --event-id latest

# View surfacing effectiveness
obsidian-cli patterns --surfacing-stats --surfacing-days 30
```

Output:
```
=> Patterns (42 matching, 156 total)

  workflow (18)
    batch-processing:2025-01-15  [0.85] Parallel API calls with rate limiting
    error-retry:2025-01-14       [0.72] Exponential backoff for transient failures
    ...

  architecture (12)
    hook-design:2025-01-16       [0.90] Event-driven hooks for extensibility
    ...

  Staleness: 8 fresh, 15 recent, 12 aging, 7 stale

  Scanned in: 45ms
```

**Staleness Decay:**
- Fresh (0-30 days): 100% confidence
- Recent (30-90 days): 95% confidence
- Aging (90-180 days): 85% confidence
- Stale (180-365 days): 70% confidence
- Ancient (365+ days): 50% confidence

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
