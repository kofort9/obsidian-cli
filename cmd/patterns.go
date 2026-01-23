package cmd

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// Pattern flags
var (
	patternsDir         string
	patternDomain       string
	patternType         string
	patternKeywords     string
	patternSimilar      string
	patternRecent       int
	patternMinConf      float64
	patternLimit        int
	patternVerbose      bool
	patternJSON         bool
	patternStats        bool
	patternIncludeDeprecated bool
	patternNoStalenessDecay  bool

	// Surfacing event flags
	patternLogAction    string
	patternEventID      string
	patternActionNotes  string
	patternLogOutcome   string
	patternOutcomeNotes string

	// Surfacing stats flags
	patternSurfacingStats bool
	patternSurfacingDays  int
)

// Pattern represents a pattern record from JSONL storage.
type Pattern struct {
	ID          string      `json:"id"`
	Domain      string      `json:"domain"`
	PatternType string      `json:"pattern_type"`
	Observation string      `json:"observation"`
	Timestamp   string      `json:"timestamp"`
	Confidence  interface{} `json:"confidence"`
	Source      string      `json:"source,omitempty"`
	Reasoning   interface{} `json:"reasoning,omitempty"`
	Indicators  []string    `json:"indicators,omitempty"`

	// Computed fields
	EffectiveConfidence float64 `json:"_effective_confidence,omitempty"`
	StalenessLevel      string  `json:"_staleness_level,omitempty"`
	AgeDays             int     `json:"_age_days,omitempty"`
	MatchScore          int     `json:"_match_score,omitempty"`
	Similarity          float64 `json:"_similarity,omitempty"`
}

// SurfacingEvent represents a pattern surfacing event.
type SurfacingEvent struct {
	EventID          string            `json:"event_id"`
	EventType        string            `json:"event_type"`
	Timestamp        string            `json:"timestamp"`
	PatternIDs       []string          `json:"pattern_ids"`
	PatternCount     int               `json:"pattern_count"`
	Context          string            `json:"context"`
	Source           string            `json:"source"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	UserAction       *string           `json:"user_action"`
	ActionTimestamp  *string           `json:"action_timestamp"`
	ActionNotes      *string           `json:"action_notes"`
	Outcome          *string           `json:"outcome"`
	OutcomeTimestamp *string           `json:"outcome_timestamp"`
	OutcomeNotes     *string           `json:"outcome_notes"`
}

// Staleness levels (days thresholds)
var stalenessLevels = map[string][2]int{
	"fresh":   {0, 30},
	"recent":  {30, 90},
	"aging":   {90, 180},
	"stale":   {180, 365},
	"ancient": {365, -1}, // -1 means no upper bound
}

// Staleness decay multipliers
var stalenessDecay = map[string]float64{
	"fresh":   1.0,
	"recent":  0.95,
	"aging":   0.85,
	"stale":   0.70,
	"ancient": 0.50,
}

// Staleness badges for display
var stalenessBadges = map[string]string{
	"recent":  "·",
	"aging":   "○",
	"stale":   "◔",
	"ancient": "◉",
}

// Valid user actions and outcomes
var validUserActions = map[string]bool{
	"accept": true, "reject": true, "ignore": true, "partial": true, "defer": true,
}
var validOutcomes = map[string]bool{
	"success": true, "failure": true, "partial": true, "unknown": true,
}

// Files and directories to exclude from pattern loading
var excludedFiles = map[string]bool{
	"graduations.jsonl":         true,
	"events.jsonl":              true,
	"confidence-audit.jsonl":    true,
	"all_decisions.jsonl":       true,
	"recurrence-index.jsonl":    true,
}

var excludedPrefixes = []string{".", "backup"}
var excludedSuffixes = []string{".backup.jsonl", ".pre-calibration.jsonl"}

var patternsCmd = &cobra.Command{
	Use:   "patterns",
	Short: "Query and manage pattern storage",
	Long: `Query and manage the pattern learning system storage at ~/.claude/patterns/.

This command provides fast pattern lookup with staleness decay, similarity search,
and surfacing event tracking for the pattern graduation system.

Examples:
  # Basic querying
  obsidian-cli patterns --patterns-dir ~/.claude/patterns
  obsidian-cli patterns --domain workflow --limit 5
  obsidian-cli patterns --type correction --recent 7
  obsidian-cli patterns --keywords "batch parallel"

  # Similarity search
  obsidian-cli patterns --similar "error handling in API"

  # Statistics
  obsidian-cli patterns --stats
  obsidian-cli patterns --surfacing-stats --surfacing-days 30

  # Log user actions
  obsidian-cli patterns --log-action accept --event-id latest
  obsidian-cli patterns --log-outcome success --outcome-notes "Pattern prevented bug"`,
	RunE: runPatterns,
}

func init() {
	rootCmd.AddCommand(patternsCmd)

	// Directory - with fallback if UserHomeDir fails
	defaultPatternsDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		defaultPatternsDir = filepath.Join(home, ".claude", "patterns")
	}
	patternsCmd.Flags().StringVar(&patternsDir, "patterns-dir", defaultPatternsDir, "Path to patterns directory")

	// Filtering
	patternsCmd.Flags().StringVar(&patternDomain, "domain", "", "Filter by domain (workflow, architecture, security, discovery, career)")
	patternsCmd.Flags().StringVar(&patternType, "type", "", "Filter by pattern type (success, correction, novel, principle)")
	patternsCmd.Flags().StringVar(&patternKeywords, "keywords", "", "Space-separated keywords to search")
	patternsCmd.Flags().StringVar(&patternSimilar, "similar", "", "Find patterns similar to this text")
	patternsCmd.Flags().IntVar(&patternRecent, "recent", 0, "Patterns from last N days")
	patternsCmd.Flags().Float64Var(&patternMinConf, "min-confidence", 0.3, "Minimum confidence threshold")
	patternsCmd.Flags().IntVarP(&patternLimit, "limit", "n", 10, "Max results")

	// Output
	patternsCmd.Flags().BoolVarP(&patternVerbose, "verbose", "V", false, "Verbose output")
	patternsCmd.Flags().BoolVar(&patternJSON, "json", false, "Output as JSON")
	patternsCmd.Flags().BoolVar(&patternStats, "stats", false, "Show pattern statistics")
	patternsCmd.Flags().BoolVar(&patternIncludeDeprecated, "include-deprecated", false, "Include deprecated patterns")
	patternsCmd.Flags().BoolVar(&patternNoStalenessDecay, "no-staleness-decay", false, "Disable confidence decay based on age")

	// Surfacing event logging
	patternsCmd.Flags().StringVar(&patternLogAction, "log-action", "", "Log user action (accept, reject, ignore, partial, defer)")
	patternsCmd.Flags().StringVar(&patternEventID, "event-id", "latest", "Event ID for action/outcome logging")
	patternsCmd.Flags().StringVar(&patternActionNotes, "action-notes", "", "Notes about the user action")
	patternsCmd.Flags().StringVar(&patternLogOutcome, "log-outcome", "", "Log outcome (success, failure, partial, unknown)")
	patternsCmd.Flags().StringVar(&patternOutcomeNotes, "outcome-notes", "", "Notes about the outcome")

	// Surfacing stats
	patternsCmd.Flags().BoolVar(&patternSurfacingStats, "surfacing-stats", false, "Show surfacing effectiveness stats")
	patternsCmd.Flags().IntVar(&patternSurfacingDays, "surfacing-days", 30, "Days to include in surfacing stats")
}

// validatePatternsDir validates the patterns directory path for security.
func validatePatternsDir(dir string) error {
	// Security: Check raw input for traversal BEFORE cleaning
	// (filepath.Clean would resolve away the .., making this check useless after)
	if strings.Contains(dir, "..") {
		return fmt.Errorf("invalid patterns directory: path contains traversal sequences")
	}

	// Must be absolute path
	if !filepath.IsAbs(dir) {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("cannot resolve patterns directory: %w", err)
		}
		dir = absDir
	}

	// Validate the event ID doesn't contain path separators (prevent injection in event operations)
	if patternEventID != "" && patternEventID != "latest" {
		if strings.ContainsAny(patternEventID, "/\\") {
			return fmt.Errorf("invalid event ID: cannot contain path separators")
		}
	}

	return nil
}

func runPatterns(cmd *cobra.Command, args []string) error {
	// Validate patterns directory
	if patternsDir == "" {
		return fmt.Errorf("patterns directory not specified. Use --patterns-dir or set HOME environment variable")
	}

	// Security: Validate path doesn't contain traversal sequences
	if err := validatePatternsDir(patternsDir); err != nil {
		return err
	}

	// Handle user action logging
	if patternLogAction != "" {
		return logUserAction(patternEventID, patternLogAction, patternActionNotes)
	}

	// Handle outcome logging
	if patternLogOutcome != "" {
		return logOutcome(patternEventID, patternLogOutcome, patternOutcomeNotes)
	}

	// Handle surfacing stats
	if patternSurfacingStats {
		return showSurfacingStats(cmd, patternSurfacingDays)
	}

	// Load all patterns
	patterns, err := loadAllPatterns(patternsDir)
	if err != nil {
		return err
	}

	// Filter deprecated
	if !patternIncludeDeprecated {
		patterns = filterDeprecated(patterns, patternsDir)
	}

	// Show stats if requested
	if patternStats {
		return showPatternStats(cmd, patterns)
	}

	// Apply staleness decay
	enableDecay := !patternNoStalenessDecay
	patterns = applyStalenessDecay(patterns, enableDecay)

	// Apply filters
	if patternDomain != "" {
		patterns = filterByDomain(patterns, patternDomain)
	}
	if patternType != "" {
		patterns = filterByType(patterns, patternType)
	}
	if patternKeywords != "" {
		keywords := parseKeywords(patternKeywords)
		patterns = filterByKeywords(patterns, keywords)
	}
	if patternSimilar != "" {
		patterns = findSimilar(patterns, patternSimilar, patternLimit)
	}
	if patternRecent > 0 {
		patterns = filterByRecency(patterns, patternRecent)
	}

	// Apply confidence filter
	if enableDecay {
		patterns = filterByEffectiveConfidence(patterns, patternMinConf)
	} else {
		patterns = filterByConfidence(patterns, patternMinConf)
	}

	// Limit results
	if patternLimit > 0 && len(patterns) > patternLimit {
		patterns = patterns[:patternLimit]
	}

	// Output results
	return outputPatternResults(cmd, patterns)
}

// loadAllPatterns loads patterns from all JSONL files in the patterns directory.
func loadAllPatterns(dir string) ([]Pattern, error) {
	var patterns []Pattern

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return patterns, nil
	}

	// Resolve the canonical path for symlink boundary checking
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canonicalDir = dir // Fall back to original if resolution fails
	}
	canonicalDir = filepath.Clean(canonicalDir)

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip directories
		if d.IsDir() {
			// Skip hidden and backup directories
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "backups" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .jsonl files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}

		// Check exclusions
		if shouldExcludeFile(d.Name()) {
			return nil
		}

		// Security: Resolve symlinks and verify path stays within patterns directory
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil // Skip broken symlinks
		}
		realPath = filepath.Clean(realPath)
		if !strings.HasPrefix(realPath, canonicalDir+string(filepath.Separator)) && realPath != canonicalDir {
			// Path escapes the patterns directory via symlink
			return nil
		}

		// Load patterns from file
		filePatterns, err := loadJSONLFile(realPath)
		if err != nil {
			// Skip files with errors, don't fail entire operation
			return nil
		}

		patterns = append(patterns, filePatterns...)
		return nil
	})

	return patterns, err
}

func shouldExcludeFile(name string) bool {
	if excludedFiles[name] {
		return true
	}
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, suffix := range excludedSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func loadJSONLFile(path string) ([]Pattern, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var patterns []Pattern
	scanner := bufio.NewScanner(file)
	// Use large buffer for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var p Pattern
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue // Skip malformed lines
		}
		patterns = append(patterns, p)
	}

	return patterns, scanner.Err()
}

func loadDeprecatedIDs(dir string) map[string]bool {
	ids := make(map[string]bool)
	deprecatedPath := filepath.Join(dir, "deprecated.jsonl")

	// Security: Resolve symlinks and verify path stays within patterns directory
	realPath, err := filepath.EvalSymlinks(deprecatedPath)
	if err != nil {
		return ids // File doesn't exist or symlink is broken
	}
	realPath = filepath.Clean(realPath)

	// Verify the resolved path is still within the patterns directory
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canonicalDir = filepath.Clean(dir)
	} else {
		canonicalDir = filepath.Clean(canonicalDir)
	}
	if !strings.HasPrefix(realPath, canonicalDir+string(filepath.Separator)) && realPath != canonicalDir {
		return ids // Path escapes directory via symlink
	}

	file, err := os.Open(realPath)
	if err != nil {
		return ids
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		if patternID, ok := record["pattern_id"].(string); ok && patternID != "" {
			ids[patternID] = true
		}
	}

	return ids
}

func filterDeprecated(patterns []Pattern, dir string) []Pattern {
	deprecatedIDs := loadDeprecatedIDs(dir)
	if len(deprecatedIDs) == 0 {
		return patterns
	}

	var filtered []Pattern
	for _, p := range patterns {
		if !deprecatedIDs[p.ID] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func filterByDomain(patterns []Pattern, domain string) []Pattern {
	domainLower := strings.ToLower(domain)
	var filtered []Pattern
	for _, p := range patterns {
		if strings.ToLower(p.Domain) == domainLower {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func filterByType(patterns []Pattern, patternType string) []Pattern {
	typeLower := strings.ToLower(patternType)
	var filtered []Pattern
	for _, p := range patterns {
		if strings.ToLower(p.PatternType) == typeLower {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func parseKeywords(keywords string) []string {
	// Split on commas or whitespace
	keywords = strings.ReplaceAll(keywords, ",", " ")
	parts := strings.Fields(keywords)
	var result []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func filterByKeywords(patterns []Pattern, keywords []string) []Pattern {
	var results []Pattern

	for i := range patterns {
		p := &patterns[i]
		observation := strings.ToLower(p.Observation)
		indicators := strings.ToLower(strings.Join(p.Indicators, " "))
		searchable := observation + " " + indicators

		score := 0
		for _, kw := range keywords {
			if strings.Contains(searchable, strings.ToLower(kw)) {
				score++
			}
		}

		if score > 0 {
			p.MatchScore = score
			results = append(results, *p)
		}
	}

	// Sort by match score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].MatchScore > results[j].MatchScore
	})

	return results
}

func filterByRecency(patterns []Pattern, days int) []Pattern {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	var filtered []Pattern

	for _, p := range patterns {
		dt := parseTimestamp(p.Timestamp)
		if dt != nil && dt.After(cutoff) {
			filtered = append(filtered, p)
		}
	}

	return filtered
}

func parseTimestamp(ts string) *time.Time {
	if ts == "" {
		return nil
	}

	// Try ISO formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	ts = strings.ReplaceAll(ts, "Z", "+00:00")

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return &t
		}
	}

	return nil
}

func normalizeConfidence(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		mapping := map[string]float64{
			"high": 0.9, "medium": 0.6, "low": 0.3,
		}
		if f, ok := mapping[strings.ToLower(v)]; ok {
			return f
		}
		return 0.5
	default:
		return 0.5
	}
}

func getPatternAgeDays(p *Pattern) int {
	dt := parseTimestamp(p.Timestamp)
	if dt == nil {
		return 999
	}
	return int(time.Since(*dt).Hours() / 24)
}

func getStalenessLevel(ageDays int) string {
	for _, level := range []string{"fresh", "recent", "aging", "stale", "ancient"} {
		bounds := stalenessLevels[level]
		minDays, maxDays := bounds[0], bounds[1]
		if maxDays == -1 {
			if ageDays >= minDays {
				return level
			}
		} else {
			if ageDays >= minDays && ageDays < maxDays {
				return level
			}
		}
	}
	return "fresh"
}

func applyStalenessDecay(patterns []Pattern, enableDecay bool) []Pattern {
	for i := range patterns {
		p := &patterns[i]
		ageDays := getPatternAgeDays(p)
		staleness := getStalenessLevel(ageDays)

		p.AgeDays = ageDays
		p.StalenessLevel = staleness

		baseConf := normalizeConfidence(p.Confidence)
		decayFactor := 1.0
		if enableDecay {
			decayFactor = stalenessDecay[staleness]
		}
		p.EffectiveConfidence = baseConf * decayFactor
	}
	return patterns
}

func filterByConfidence(patterns []Pattern, minConf float64) []Pattern {
	var filtered []Pattern
	for _, p := range patterns {
		if normalizeConfidence(p.Confidence) >= minConf {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func filterByEffectiveConfidence(patterns []Pattern, minConf float64) []Pattern {
	var filtered []Pattern
	for _, p := range patterns {
		if p.EffectiveConfidence >= minConf {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// findSimilar finds patterns similar to a query using Jaccard similarity.
func findSimilar(patterns []Pattern, query string, limit int) []Pattern {
	// Extract keywords from query (remove stopwords)
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "to": true, "for": true, "in": true,
		"on": true, "of": true, "and": true, "or": true, "with": true,
	}

	wordRegex := regexp.MustCompile(`\w+`)
	queryWordsRaw := wordRegex.FindAllString(strings.ToLower(query), -1)

	queryWords := make(map[string]bool)
	for _, w := range queryWordsRaw {
		if !stopwords[w] {
			queryWords[w] = true
		}
	}

	if len(queryWords) == 0 {
		return nil
	}

	var scored []Pattern
	reasoningWeight := 0.15

	for i := range patterns {
		p := patterns[i]

		// Primary signal: observation + indicators
		searchable := strings.ToLower(p.Observation + " " + strings.Join(p.Indicators, " "))
		obsWordsRaw := wordRegex.FindAllString(searchable, -1)

		obsWords := make(map[string]bool)
		for _, w := range obsWordsRaw {
			if !stopwords[w] {
				obsWords[w] = true
			}
		}

		// Calculate Jaccard for observation
		obsOverlap := 0
		for w := range queryWords {
			if obsWords[w] {
				obsOverlap++
			}
		}

		// Calculate observation similarity (may be 0 - pattern might match on reasoning)
		var obsSimilarity float64
		obsUnion := len(unionSets(queryWords, obsWords))
		if obsUnion > 0 && obsOverlap > 0 {
			obsSimilarity = float64(obsOverlap) / float64(obsUnion)
		}

		// Secondary signal: reasoning (only applies if pattern has reasoning)
		// IMPORTANT: Don't penalize patterns without reasoning - use full observation score
		hasReasoning := false
		reasoningSimilarity := 0.0
		if p.Reasoning != nil {
			var reasoningText string
			switch r := p.Reasoning.(type) {
			case string:
				reasoningText = r
			case map[string]interface{}:
				var parts []string
				for _, v := range r {
					if s, ok := v.(string); ok {
						parts = append(parts, s)
					}
				}
				reasoningText = strings.Join(parts, " ")
			}

			if reasoningText != "" {
				reasonWordsRaw := wordRegex.FindAllString(strings.ToLower(reasoningText), -1)
				reasonWords := make(map[string]bool)
				for _, w := range reasonWordsRaw {
					if !stopwords[w] {
						reasonWords[w] = true
					}
				}

				if len(reasonWords) > 0 {
					hasReasoning = true
					reasonOverlap := 0
					for w := range queryWords {
						if reasonWords[w] {
							reasonOverlap++
						}
					}
					reasonUnion := len(unionSets(queryWords, reasonWords))
					if reasonUnion > 0 {
						reasoningSimilarity = float64(reasonOverlap) / float64(reasonUnion)
					}
				}
			}
		}

		// Weighted combination - only include reasoning component if pattern has reasoning
		// This prevents penalizing patterns without reasoning
		var combined float64
		if hasReasoning {
			combined = (1-reasoningWeight)*obsSimilarity + reasoningWeight*reasoningSimilarity
		} else {
			combined = obsSimilarity // Full score for patterns without reasoning
		}

		// Only include patterns that have some match (observation OR reasoning)
		if combined > 0 {
			p.Similarity = combined
			scored = append(scored, p)
		}
	}

	// Sort by similarity
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Similarity > scored[j].Similarity
	})

	if limit > 0 && len(scored) > limit {
		return scored[:limit]
	}
	return scored
}

func unionSets(a, b map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k := range a {
		result[k] = true
	}
	for k := range b {
		result[k] = true
	}
	return result
}

func randomHex(n int) string {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based if random fails
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n]
	}
	return hex.EncodeToString(bytes)[:n]
}

// sanitizeNotes removes control characters from notes fields to prevent
// log injection attacks (e.g., ANSI escape sequences in terminal output).
func sanitizeNotes(notes string) string {
	// Remove control characters except newline and tab
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1 // Remove character
		}
		return r
	}, notes)
}

func showPatternStats(cmd *cobra.Command, patterns []Pattern) error {
	// Apply staleness indicators (no decay) for stats
	patterns = applyStalenessDecay(patterns, false)

	byDomain := make(map[string]int)
	byType := make(map[string]int)
	byStaleness := make(map[string]int)
	withReasoning := 0

	for _, p := range patterns {
		domain := p.Domain
		if domain == "" {
			domain = "unknown"
		}
		byDomain[domain]++

		pType := p.PatternType
		if pType == "" {
			pType = "unknown"
		}
		byType[pType]++

		staleness := p.StalenessLevel
		if staleness == "" {
			staleness = "unknown"
		}
		byStaleness[staleness]++

		if p.Reasoning != nil {
			withReasoning++
		}
	}

	total := len(patterns)

	if patternJSON {
		stats := map[string]interface{}{
			"total":          total,
			"with_reasoning": withReasoning,
			"by_domain":      byDomain,
			"by_type":        byType,
			"by_staleness":   byStaleness,
		}
		return encodeJSON(cmd, stats)
	}

	fmt.Printf("Total patterns: %d\n", total)

	// Reasoning coverage
	pct := 0.0
	if total > 0 {
		pct = 100.0 * float64(withReasoning) / float64(total)
	}
	status := "❌"
	if pct >= 30 {
		status = "✅"
	} else if pct >= 10 {
		status = "⚠️"
	}
	fmt.Printf("\nReasoning coverage: %d/%d (%.1f%%) %s\n", withReasoning, total, pct, status)
	if pct < 30 {
		fmt.Printf("  → Target: 30%% for statistical significance\n")
	}

	// By domain
	fmt.Printf("\nBy domain:\n")
	for _, d := range sortedMapKeys(byDomain) {
		fmt.Printf("  %s: %d\n", d, byDomain[d])
	}

	// By type
	fmt.Printf("\nBy type:\n")
	for _, t := range sortedMapKeys(byType) {
		fmt.Printf("  %s: %d\n", t, byType[t])
	}

	// By staleness
	fmt.Printf("\nBy staleness:\n")
	stalenessOrder := []string{"fresh", "recent", "aging", "stale", "ancient", "unknown"}
	badges := map[string]string{"fresh": "✅", "recent": "·", "aging": "○", "stale": "◔", "ancient": "◉"}
	for _, s := range stalenessOrder {
		if count, ok := byStaleness[s]; ok {
			pct := 0.0
			if total > 0 {
				pct = 100.0 * float64(count) / float64(total)
			}
			badge := badges[s]
			if badge == "" {
				badge = "?"
			}
			fmt.Printf("  %s %s: %d (%.1f%%)\n", badge, s, count, pct)
		}
	}

	return nil
}

func sortedMapKeys(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	var pairs []kv
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].v > pairs[j].v
	})
	var keys []string
	for _, p := range pairs {
		keys = append(keys, p.k)
	}
	return keys
}

func outputPatternResults(cmd *cobra.Command, patterns []Pattern) error {
	if len(patterns) == 0 {
		fmt.Println("No matching patterns found.")
		return nil
	}

	if patternJSON {
		return encodeJSON(cmd, patterns)
	}

	fmt.Printf("Found %d pattern(s):\n\n", len(patterns))

	for _, p := range patterns {
		fmt.Println(formatPattern(&p, patternVerbose))
		fmt.Println()
	}

	// Log surfacing event
	if len(patterns) > 0 {
		patternIDs := make([]string, 0, len(patterns))
		for _, p := range patterns {
			if p.ID != "" {
				patternIDs = append(patternIDs, p.ID)
			}
		}

		context := patternSimilar
		if context == "" {
			context = patternKeywords
		}
		if context == "" && patternDomain != "" {
			context = "domain:" + patternDomain
		}
		if context == "" {
			context = "query"
		}

		eventID, err := logSurfacingEvent(patternsDir, patternIDs, context, "cli")
		if err == nil && patternVerbose {
			fmt.Printf("[Surfacing event: %s]\n", eventID)
			fmt.Printf("  Log action: obsidian-cli patterns --log-action accept|reject|ignore\n")
		}
	}

	return nil
}

func formatPattern(p *Pattern, verbose bool) string {
	domain := p.Domain
	if domain == "" {
		domain = "?"
	}
	pType := p.PatternType
	if pType == "" {
		pType = "?"
	}

	obs := p.Observation
	if len(obs) > 100 {
		obs = obs[:97] + "..."
	}

	// Staleness badge
	stalenessBadge := ""
	if p.StalenessLevel != "" && p.StalenessLevel != "fresh" {
		badge := stalenessBadges[p.StalenessLevel]
		if badge == "" {
			badge = ""
		}
		stalenessBadge = fmt.Sprintf(" %s[%s]", badge, p.StalenessLevel)
	}

	// Confidence string
	baseConf := normalizeConfidence(p.Confidence)
	confStr := fmt.Sprintf("%.2f", baseConf)
	if p.EffectiveConfidence > 0 && p.EffectiveConfidence != baseConf {
		confStr = fmt.Sprintf("%.2f←%.2f", p.EffectiveConfidence, baseConf)
	}

	var lines []string
	if verbose {
		lines = append(lines, fmt.Sprintf("[%s/%s] (conf: %s)%s", domain, pType, confStr, stalenessBadge))
		lines = append(lines, fmt.Sprintf("  %s", obs))
		lines = append(lines, fmt.Sprintf("  ID: %s", p.ID))
		if p.AgeDays > 0 {
			lines = append(lines, fmt.Sprintf("  Age: %d days", p.AgeDays))
		}
	} else {
		lines = append(lines, fmt.Sprintf("[%s/%s]%s %s", domain, pType, stalenessBadge, obs))
	}

	// Add reasoning if present
	if p.Reasoning != nil {
		reasoningLines := formatReasoning(p.Reasoning)
		lines = append(lines, reasoningLines...)
	}

	return strings.Join(lines, "\n")
}

func formatReasoning(reasoning interface{}) []string {
	var lines []string

	switch r := reasoning.(type) {
	case string:
		if r != "" {
			if len(r) > 200 {
				r = r[:197] + "..."
			}
			lines = append(lines, fmt.Sprintf("  → Why: %s", r))
		}
	case map[string]interface{}:
		if decision, ok := r["decision"].(string); ok && decision != "" {
			lines = append(lines, fmt.Sprintf("  → Decision: %s", decision))
		}
		if rationale, ok := r["rationale"].(string); ok && rationale != "" {
			if len(rationale) > 200 {
				rationale = rationale[:197] + "..."
			}
			lines = append(lines, fmt.Sprintf("  → Why: %s", rationale))
		}
		if context, ok := r["context"].(string); ok && context != "" {
			if len(context) > 100 {
				context = context[:97] + "..."
			}
			lines = append(lines, fmt.Sprintf("  → Context: %s", context))
		}
	}

	return lines
}

// Surfacing event functions

func getSurfacingEventsPath(dir string) string {
	return filepath.Join(dir, "surfacing", "events.jsonl")
}

func logSurfacingEvent(dir string, patternIDs []string, context, source string) (string, error) {
	eventsPath := getSurfacingEventsPath(dir)
	eventsDir := filepath.Dir(eventsPath)

	// Security: Verify the events directory stays within patterns directory
	// Resolve any symlinks in the path before creating directories
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canonicalDir = filepath.Clean(dir)
	} else {
		canonicalDir = filepath.Clean(canonicalDir)
	}

	// Check if eventsDir would escape the patterns directory via symlinks
	// (check parent components that already exist)
	checkPath := eventsDir
	for checkPath != dir && checkPath != "." && checkPath != "/" {
		if info, err := os.Lstat(checkPath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				realPath, err := filepath.EvalSymlinks(checkPath)
				if err != nil {
					return "", fmt.Errorf("cannot resolve symlink in events path: %w", err)
				}
				if !strings.HasPrefix(filepath.Clean(realPath), canonicalDir+string(filepath.Separator)) {
					return "", fmt.Errorf("events path escapes patterns directory via symlink")
				}
			}
		}
		checkPath = filepath.Dir(checkPath)
	}

	// Ensure directory exists
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		return "", err
	}

	eventID := fmt.Sprintf("surf-%s-%s", time.Now().Format("20060102-150405"), randomHex(6))

	event := SurfacingEvent{
		EventID:      eventID,
		EventType:    "surfaced",
		Timestamp:    time.Now().Format(time.RFC3339),
		PatternIDs:   patternIDs,
		PatternCount: len(patternIDs),
		Context:      context,
		Source:       source,
	}

	// Write with file locking
	file, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Exclusive lock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}

	if _, err := file.WriteString(string(data) + "\n"); err != nil {
		return "", err
	}

	return eventID, nil
}

func logUserAction(eventID, action, notes string) error {
	if !validUserActions[action] {
		return fmt.Errorf("invalid action '%s'. Valid: accept, reject, ignore, partial, defer", action)
	}

	eventsPath := getSurfacingEventsPath(patternsDir)
	return updateSurfacingEvent(eventsPath, eventID, map[string]string{
		"user_action":      action,
		"action_timestamp": time.Now().Format(time.RFC3339),
		"action_notes":     sanitizeNotes(notes),
	}, "user_action")
}

func logOutcome(eventID, outcome, notes string) error {
	if !validOutcomes[outcome] {
		return fmt.Errorf("invalid outcome '%s'. Valid: success, failure, partial, unknown", outcome)
	}

	eventsPath := getSurfacingEventsPath(patternsDir)
	return updateSurfacingEvent(eventsPath, eventID, map[string]string{
		"outcome":           outcome,
		"outcome_timestamp": time.Now().Format(time.RFC3339),
		"outcome_notes":     sanitizeNotes(notes),
	}, "outcome")
}

func updateSurfacingEvent(eventsPath, eventID string, updates map[string]string, findLatestWithout string) error {
	// Open file for read+write with exclusive lock for the entire operation
	// O_CREATE prevents TOCTOU race if file is created between check and open
	file, err := os.OpenFile(eventsPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open events file: %w", err)
	}
	defer file.Close()

	// Exclusive lock for the entire read-modify-write operation
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	// Read all events
	var events []map[string]interface{}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read events: %w", err)
	}

	if len(events) == 0 {
		return fmt.Errorf("no surfacing events found")
	}

	// Find target event
	targetIdx := -1
	if eventID == "latest" {
		// Find most recent event where findLatestWithout field is nil
		for i := len(events) - 1; i >= 0; i-- {
			val := events[i][findLatestWithout]
			if val == nil {
				targetIdx = i
				break
			}
		}
		if targetIdx == -1 {
			targetIdx = len(events) - 1
		}
	} else {
		for i, e := range events {
			if e["event_id"] == eventID {
				targetIdx = i
				break
			}
		}
	}

	if targetIdx == -1 {
		return fmt.Errorf("event '%s' not found", eventID)
	}

	// Apply updates
	for k, v := range updates {
		if v != "" {
			events[targetIdx][k] = v
		}
	}

	// Truncate file and rewrite from beginning (same file descriptor, still locked)
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate: %w", err)
	}

	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("failed to marshal event: %w", err)
		}
		if _, err := file.WriteString(string(data) + "\n"); err != nil {
			return fmt.Errorf("failed to write event: %w", err)
		}
	}

	foundEventID := events[targetIdx]["event_id"]
	fmt.Printf("Logged %s for event %v\n", findLatestWithout, foundEventID)
	return nil
}

func showSurfacingStats(cmd *cobra.Command, days int) error {
	eventsPath := getSurfacingEventsPath(patternsDir)

	file, err := os.Open(eventsPath)
	if err != nil {
		if patternJSON {
			return encodeJSON(cmd, map[string]interface{}{
				"total":   0,
				"message": "No surfacing events recorded yet",
			})
		}
		fmt.Println("No surfacing events recorded yet")
		return nil
	}
	defer file.Close()

	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var events []map[string]interface{}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Filter by date
		ts, _ := event["timestamp"].(string)
		dt := parseTimestamp(ts)
		if dt != nil && dt.After(cutoff) {
			events = append(events, event)
		}
	}

	if len(events) == 0 {
		if patternJSON {
			return encodeJSON(cmd, map[string]interface{}{
				"total":   0,
				"days":    days,
				"message": fmt.Sprintf("No events in last %d days", days),
			})
		}
		fmt.Printf("No events in last %d days\n", days)
		return nil
	}

	// Compute stats
	byAction := make(map[string]int)
	bySource := make(map[string]int)
	byOutcome := make(map[string]int)
	totalPatterns := 0
	withAction := 0
	outcomesRecorded := 0

	for _, e := range events {
		action, _ := e["user_action"].(string)
		source, _ := e["source"].(string)
		outcome, _ := e["outcome"].(string)
		patternCount := 0
		if pc, ok := e["pattern_count"].(float64); ok {
			patternCount = int(pc)
		}

		if source == "" {
			source = "unknown"
		}
		bySource[source]++
		totalPatterns += patternCount

		if action != "" {
			byAction[action]++
			withAction++
		} else {
			byAction["pending"]++
		}

		if outcome != "" {
			byOutcome[outcome]++
			outcomesRecorded++
		} else {
			byOutcome["pending"]++
		}
	}

	// Calculate rates
	// Note: Accept/reject rates use only EXPLICIT decisions (accept, partial, reject)
	// Ignore and defer are non-decisions - the user saw but didn't evaluate
	explicitAccept := byAction["accept"] + byAction["partial"]
	explicitReject := byAction["reject"]
	explicitDecisions := explicitAccept + explicitReject
	nonDecisions := byAction["ignore"] + byAction["defer"]
	responded := withAction // Total with any action (for reference)

	var acceptRate, rejectRate, effectivenessRate *float64
	if explicitDecisions > 0 {
		ar := float64(explicitAccept) / float64(explicitDecisions)
		acceptRate = &ar
		rr := float64(explicitReject) / float64(explicitDecisions)
		rejectRate = &rr
	}

	successOutcomes := byOutcome["success"] + byOutcome["partial"]
	if outcomesRecorded > 0 {
		er := float64(successOutcomes) / float64(outcomesRecorded)
		effectivenessRate = &er
	}

	// Minimum sample size for statistical significance (based on explicit decisions)
	const minSampleSize = 30
	sampleSizeWarning := explicitDecisions < minSampleSize && explicitDecisions > 0
	_ = nonDecisions // Used in stats output below

	stats := map[string]interface{}{
		"days":                    days,
		"total_events":            len(events),
		"total_patterns_surfaced": totalPatterns,
		"by_action":               byAction,
		"by_source":               bySource,
		"by_outcome":              byOutcome,
		"responded":               responded,
		"explicit_decisions":      explicitDecisions,
		"non_decisions":           nonDecisions,
		"pending":                 len(events) - responded,
		"outcomes_recorded":       outcomesRecorded,
	}
	if acceptRate != nil {
		stats["accept_rate"] = *acceptRate
	}
	if rejectRate != nil {
		stats["reject_rate"] = *rejectRate
	}
	if effectivenessRate != nil {
		stats["effectiveness_rate"] = *effectivenessRate
	}
	if sampleSizeWarning {
		stats["sample_size_warning"] = fmt.Sprintf("Only %d explicit decisions. Rates may not be statistically significant (recommended: %d+).", explicitDecisions, minSampleSize)
	}

	if patternJSON {
		return encodeJSON(cmd, stats)
	}

	fmt.Printf("Surfacing Stats (last %d days):\n", days)
	fmt.Printf("  Total events: %d\n", len(events))
	fmt.Printf("  Total patterns surfaced: %d\n", totalPatterns)
	fmt.Printf("  Responded: %d (%d explicit decisions, %d deferred/ignored)\n", responded, explicitDecisions, nonDecisions)
	fmt.Printf("  Pending: %d\n", len(events)-responded)

	if acceptRate != nil {
		fmt.Printf("  Accept rate: %.0f%% (of explicit decisions)\n", *acceptRate*100)
	}
	if rejectRate != nil {
		fmt.Printf("  Reject rate: %.0f%% (of explicit decisions)\n", *rejectRate*100)
	}
	if outcomesRecorded > 0 {
		fmt.Printf("  Outcomes recorded: %d\n", outcomesRecorded)
		if effectivenessRate != nil {
			fmt.Printf("  Effectiveness rate: %.0f%%\n", *effectivenessRate*100)
		}
	}

	// Sample size warning
	if sampleSizeWarning {
		fmt.Printf("\n  ⚠️  Note: Only %d explicit decisions recorded. Rates may not be statistically significant.\n", explicitDecisions)
		fmt.Printf("     Recommended: %d+ explicit decisions for reliable metrics.\n", minSampleSize)
	}

	fmt.Printf("  By action: %v\n", byAction)

	// Only show by_outcome if there are non-pending outcomes
	hasOutcomes := false
	for k, v := range byOutcome {
		if k != "pending" && v > 0 {
			hasOutcomes = true
			break
		}
	}
	if hasOutcomes {
		fmt.Printf("  By outcome: %v\n", byOutcome)
	}

	fmt.Printf("  By source: %v\n", bySource)

	return nil
}
