# obsidian-cli

Fast CLI for Obsidian vault operations. Built in Go for speed - 20-40x faster than Python equivalents.

## Features

- **Concurrent scanning** - Uses goroutines for parallel file processing
- **Health checks** - Detect orphan files, dead links, and frontmatter issues
- **Vault statistics** - Breakdown by folder with visual bar charts
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
