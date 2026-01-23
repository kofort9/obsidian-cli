package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNormalizeConfidence tests confidence value normalization
func TestNormalizeConfidence(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected float64
	}{
		{"float64", 0.75, 0.75},
		{"int", 1, 1.0},
		{"string high", "high", 0.9},
		{"string medium", "medium", 0.6},
		{"string low", "low", 0.3},
		{"string HIGH (case)", "HIGH", 0.9},
		{"string unknown", "unknown", 0.5},
		{"nil", nil, 0.5},
		{"empty string", "", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeConfidence(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeConfidence(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestParseTimestamp tests timestamp parsing with various formats
func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantYear  int
		wantMonth time.Month
		wantDay   int
	}{
		{"RFC3339", "2024-06-15T10:30:00Z", false, 2024, 6, 15},
		{"RFC3339 with offset", "2024-06-15T10:30:00+00:00", false, 2024, 6, 15},
		{"ISO without timezone", "2024-06-15T10:30:00", false, 2024, 6, 15},
		{"date only", "2024-06-15", false, 2024, 6, 15},
		{"empty string", "", true, 0, 0, 0},
		{"invalid format", "not-a-date", true, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTimestamp(tt.input)
			if tt.wantNil {
				if result != nil {
					t.Errorf("parseTimestamp(%q) = %v, want nil", tt.input, result)
				}
				return
			}
			if result == nil {
				t.Errorf("parseTimestamp(%q) = nil, want non-nil", tt.input)
				return
			}
			if result.Year() != tt.wantYear || result.Month() != tt.wantMonth || result.Day() != tt.wantDay {
				t.Errorf("parseTimestamp(%q) = %v, want %d-%02d-%02d", tt.input, result, tt.wantYear, tt.wantMonth, tt.wantDay)
			}
		})
	}
}

// TestGetStalenessLevel tests staleness bucket boundaries
func TestGetStalenessLevel(t *testing.T) {
	tests := []struct {
		name     string
		ageDays  int
		expected string
	}{
		// Fresh: 0-30
		{"day 0 is fresh", 0, "fresh"},
		{"day 15 is fresh", 15, "fresh"},
		{"day 29 is fresh", 29, "fresh"},
		// Recent: 30-90
		{"day 30 is recent", 30, "recent"},
		{"day 60 is recent", 60, "recent"},
		{"day 89 is recent", 89, "recent"},
		// Aging: 90-180
		{"day 90 is aging", 90, "aging"},
		{"day 120 is aging", 120, "aging"},
		{"day 179 is aging", 179, "aging"},
		// Stale: 180-365
		{"day 180 is stale", 180, "stale"},
		{"day 270 is stale", 270, "stale"},
		{"day 364 is stale", 364, "stale"},
		// Ancient: 365+
		{"day 365 is ancient", 365, "ancient"},
		{"day 500 is ancient", 500, "ancient"},
		{"day 1000 is ancient", 1000, "ancient"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStalenessLevel(tt.ageDays)
			if result != tt.expected {
				t.Errorf("getStalenessLevel(%d) = %q, want %q", tt.ageDays, result, tt.expected)
			}
		})
	}
}

// TestApplyStalenessDecay tests decay multiplier application
func TestApplyStalenessDecay(t *testing.T) {
	now := time.Now()

	patterns := []Pattern{
		{ID: "fresh", Timestamp: now.Format(time.RFC3339), Confidence: 1.0},
		{ID: "recent", Timestamp: now.AddDate(0, 0, -45).Format(time.RFC3339), Confidence: 1.0},
		{ID: "aging", Timestamp: now.AddDate(0, 0, -120).Format(time.RFC3339), Confidence: 1.0},
		{ID: "stale", Timestamp: now.AddDate(0, 0, -250).Format(time.RFC3339), Confidence: 1.0},
		{ID: "ancient", Timestamp: now.AddDate(0, 0, -400).Format(time.RFC3339), Confidence: 1.0},
	}

	// With decay enabled
	result := applyStalenessDecay(patterns, true)

	expectedDecays := map[string]float64{
		"fresh":   1.0,
		"recent":  0.95,
		"aging":   0.85,
		"stale":   0.70,
		"ancient": 0.50,
	}

	for _, p := range result {
		expected := expectedDecays[p.ID]
		if p.EffectiveConfidence != expected {
			t.Errorf("Pattern %s: EffectiveConfidence = %v, want %v", p.ID, p.EffectiveConfidence, expected)
		}
	}

	// With decay disabled
	result = applyStalenessDecay(patterns, false)
	for _, p := range result {
		if p.EffectiveConfidence != 1.0 {
			t.Errorf("Pattern %s with decay disabled: EffectiveConfidence = %v, want 1.0", p.ID, p.EffectiveConfidence)
		}
	}
}

// TestFilterByDomain tests domain filtering
func TestFilterByDomain(t *testing.T) {
	patterns := []Pattern{
		{ID: "1", Domain: "workflow"},
		{ID: "2", Domain: "architecture"},
		{ID: "3", Domain: "workflow"},
		{ID: "4", Domain: "security"},
	}

	result := filterByDomain(patterns, "workflow")
	if len(result) != 2 {
		t.Errorf("filterByDomain returned %d patterns, want 2", len(result))
	}

	// Case insensitive
	result = filterByDomain(patterns, "WORKFLOW")
	if len(result) != 2 {
		t.Errorf("filterByDomain (case insensitive) returned %d patterns, want 2", len(result))
	}

	// No match
	result = filterByDomain(patterns, "nonexistent")
	if len(result) != 0 {
		t.Errorf("filterByDomain (no match) returned %d patterns, want 0", len(result))
	}
}

// TestFilterByType tests type filtering
func TestFilterByType(t *testing.T) {
	patterns := []Pattern{
		{ID: "1", PatternType: "success"},
		{ID: "2", PatternType: "correction"},
		{ID: "3", PatternType: "success"},
	}

	result := filterByType(patterns, "success")
	if len(result) != 2 {
		t.Errorf("filterByType returned %d patterns, want 2", len(result))
	}
}

// TestFilterByKeywords tests keyword filtering and scoring
func TestFilterByKeywords(t *testing.T) {
	patterns := []Pattern{
		{ID: "1", Observation: "batch processing with parallel execution"},
		{ID: "2", Observation: "single thread processing"},
		{ID: "3", Observation: "batch upload to API"},
	}

	result := filterByKeywords(patterns, []string{"batch", "parallel"})

	if len(result) != 2 {
		t.Errorf("filterByKeywords returned %d patterns, want 2", len(result))
	}

	// First result should have higher match score (matches both keywords)
	if result[0].ID != "1" {
		t.Errorf("filterByKeywords: top result ID = %s, want 1 (highest match score)", result[0].ID)
	}
	if result[0].MatchScore != 2 {
		t.Errorf("filterByKeywords: top result MatchScore = %d, want 2", result[0].MatchScore)
	}
}

// TestFilterByRecency tests recency filtering
func TestFilterByRecency(t *testing.T) {
	now := time.Now()

	patterns := []Pattern{
		{ID: "recent", Timestamp: now.AddDate(0, 0, -3).Format(time.RFC3339)},
		{ID: "old", Timestamp: now.AddDate(0, 0, -30).Format(time.RFC3339)},
		{ID: "very-old", Timestamp: now.AddDate(0, 0, -100).Format(time.RFC3339)},
	}

	result := filterByRecency(patterns, 7)
	if len(result) != 1 {
		t.Errorf("filterByRecency(7) returned %d patterns, want 1", len(result))
	}
	if result[0].ID != "recent" {
		t.Errorf("filterByRecency(7) returned ID %s, want 'recent'", result[0].ID)
	}
}

// TestFilterByConfidence tests confidence filtering
func TestFilterByConfidence(t *testing.T) {
	patterns := []Pattern{
		{ID: "high", Confidence: 0.9},
		{ID: "medium", Confidence: 0.6},
		{ID: "low", Confidence: 0.2},
	}

	result := filterByConfidence(patterns, 0.5)
	if len(result) != 2 {
		t.Errorf("filterByConfidence(0.5) returned %d patterns, want 2", len(result))
	}
}

// TestFilterByEffectiveConfidence tests effective confidence filtering
func TestFilterByEffectiveConfidence(t *testing.T) {
	patterns := []Pattern{
		{ID: "high", EffectiveConfidence: 0.9},
		{ID: "medium", EffectiveConfidence: 0.6},
		{ID: "low", EffectiveConfidence: 0.2},
	}

	result := filterByEffectiveConfidence(patterns, 0.5)
	if len(result) != 2 {
		t.Errorf("filterByEffectiveConfidence(0.5) returned %d patterns, want 2", len(result))
	}
}

// TestParseKeywords tests keyword parsing
func TestParseKeywords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"space separated", "batch parallel", []string{"batch", "parallel"}},
		{"comma separated", "batch,parallel", []string{"batch", "parallel"}},
		{"mixed", "batch, parallel execution", []string{"batch", "parallel", "execution"}},
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseKeywords(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("parseKeywords(%q) returned %d items, want %d", tt.input, len(result), len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("parseKeywords(%q)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// TestShouldExcludeFile tests file exclusion rules
func TestShouldExcludeFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		excluded bool
	}{
		{"normal pattern file", "workflow.jsonl", false},
		{"events file", "events.jsonl", true},
		{"graduations file", "graduations.jsonl", true},
		{"backup file", "workflow.backup.jsonl", true},
		{"hidden file", ".hidden.jsonl", true},
		{"pre-calibration", "patterns.pre-calibration.jsonl", true},
		{"confidence audit", "confidence-audit.jsonl", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldExcludeFile(tt.filename)
			if result != tt.excluded {
				t.Errorf("shouldExcludeFile(%q) = %v, want %v", tt.filename, result, tt.excluded)
			}
		})
	}
}

// TestUnionSets tests set union helper
func TestUnionSets(t *testing.T) {
	a := map[string]bool{"x": true, "y": true}
	b := map[string]bool{"y": true, "z": true}

	result := unionSets(a, b)

	if len(result) != 3 {
		t.Errorf("unionSets: len = %d, want 3", len(result))
	}
	for _, key := range []string{"x", "y", "z"} {
		if !result[key] {
			t.Errorf("unionSets: missing key %q", key)
		}
	}
}

// TestFindSimilar tests Jaccard similarity search
func TestFindSimilar(t *testing.T) {
	patterns := []Pattern{
		{ID: "1", Observation: "batch processing with parallel API calls"},
		{ID: "2", Observation: "error handling in authentication"},
		{ID: "3", Observation: "batch upload to storage"},
	}

	result := findSimilar(patterns, "batch API processing", 10)

	if len(result) < 2 {
		t.Errorf("findSimilar returned %d results, want at least 2", len(result))
		return
	}

	// Pattern 1 should rank highest (matches batch, processing, API)
	if result[0].ID != "1" {
		t.Errorf("findSimilar: top result ID = %s, want 1", result[0].ID)
	}
}

// TestFindSimilarNoReasoningBias tests that patterns without reasoning aren't penalized
func TestFindSimilarNoReasoningBias(t *testing.T) {
	// Two identical patterns, one with reasoning and one without
	patterns := []Pattern{
		{ID: "with-reasoning", Observation: "batch processing workflow", Reasoning: "test reasoning"},
		{ID: "without-reasoning", Observation: "batch processing workflow", Reasoning: nil},
	}

	result := findSimilar(patterns, "batch processing", 10)

	if len(result) != 2 {
		t.Errorf("findSimilar returned %d results, want 2", len(result))
		return
	}

	// Both patterns should have similar scores (within 0.2)
	// The one without reasoning should NOT be significantly penalized
	scoreDiff := result[0].Similarity - result[1].Similarity
	if scoreDiff < 0 {
		scoreDiff = -scoreDiff
	}

	// Allow small difference due to reasoning bonus, but not the old 15% penalty
	if scoreDiff > 0.2 {
		t.Errorf("Reasoning bias detected: score difference = %.3f (pattern with: %.3f, without: %.3f)",
			scoreDiff, patterns[0].Similarity, patterns[1].Similarity)
	}
}

// TestValidatePatternsDir tests path validation
func TestValidatePatternsDir(t *testing.T) {
	// Save and restore global state
	oldEventID := patternEventID
	defer func() { patternEventID = oldEventID }()

	tests := []struct {
		name      string
		dir       string
		eventID   string
		wantError bool
	}{
		{"valid absolute path", "/tmp/patterns", "", false},
		{"valid relative path", "patterns", "", false},
		{"event ID with slash", "/tmp/patterns", "../foo", true},
		{"event ID with backslash", "/tmp/patterns", "..\\foo", true},
		{"valid event ID", "/tmp/patterns", "surf-20240101-abc123", false},
		{"latest event ID", "/tmp/patterns", "latest", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patternEventID = tt.eventID
			err := validatePatternsDir(tt.dir)
			if tt.wantError && err == nil {
				t.Errorf("validatePatternsDir(%q, eventID=%q) = nil, want error", tt.dir, tt.eventID)
			}
			if !tt.wantError && err != nil {
				t.Errorf("validatePatternsDir(%q, eventID=%q) = %v, want nil", tt.dir, tt.eventID, err)
			}
		})
	}
}

// TestLoadJSONLFile tests JSONL file loading
func TestLoadJSONLFile(t *testing.T) {
	// Create temp file with test data
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")

	content := `{"id": "test-1", "domain": "workflow", "pattern_type": "success", "observation": "test 1", "timestamp": "2024-01-01T00:00:00Z", "confidence": 0.8}
{"id": "test-2", "domain": "architecture", "pattern_type": "correction", "observation": "test 2", "timestamp": "2024-01-02T00:00:00Z", "confidence": 0.6}
# comment line
{"malformed json
{"id": "test-3", "domain": "security", "pattern_type": "novel", "observation": "test 3", "timestamp": "2024-01-03T00:00:00Z", "confidence": 0.9}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	patterns, err := loadJSONLFile(testFile)
	if err != nil {
		t.Fatalf("loadJSONLFile failed: %v", err)
	}

	// Should load 3 valid patterns (skipping comment and malformed)
	if len(patterns) != 3 {
		t.Errorf("loadJSONLFile returned %d patterns, want 3", len(patterns))
	}

	// Verify first pattern
	if patterns[0].ID != "test-1" {
		t.Errorf("First pattern ID = %q, want 'test-1'", patterns[0].ID)
	}
	if patterns[0].Domain != "workflow" {
		t.Errorf("First pattern Domain = %q, want 'workflow'", patterns[0].Domain)
	}
}

// TestLoadAllPatterns tests recursive pattern loading with exclusions
func TestLoadAllPatterns(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	// Create subdirectories
	workflowDir := filepath.Join(tmpDir, "workflow")
	backupsDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("Failed to create workflow dir: %v", err)
	}
	if err := os.MkdirAll(backupsDir, 0755); err != nil {
		t.Fatalf("Failed to create backups dir: %v", err)
	}

	// Create test files
	pattern1 := `{"id": "p1", "domain": "workflow", "observation": "test"}`
	pattern2 := `{"id": "p2", "domain": "architecture", "observation": "test"}`
	backup := `{"id": "backup", "domain": "workflow", "observation": "backup"}`
	events := `{"event_id": "e1", "event_type": "surfaced"}`

	os.WriteFile(filepath.Join(tmpDir, "main.jsonl"), []byte(pattern1), 0644)
	os.WriteFile(filepath.Join(workflowDir, "sub.jsonl"), []byte(pattern2), 0644)
	os.WriteFile(filepath.Join(backupsDir, "backup.jsonl"), []byte(backup), 0644) // Should be skipped (backups dir)
	os.WriteFile(filepath.Join(tmpDir, "events.jsonl"), []byte(events), 0644)     // Should be skipped (excluded file)

	patterns, err := loadAllPatterns(tmpDir)
	if err != nil {
		t.Fatalf("loadAllPatterns failed: %v", err)
	}

	// Should load 2 patterns (main.jsonl + workflow/sub.jsonl)
	// Should skip backups dir and events.jsonl
	if len(patterns) != 2 {
		t.Errorf("loadAllPatterns returned %d patterns, want 2", len(patterns))
	}
}

// TestGetPatternAgeDays tests age calculation
func TestGetPatternAgeDays(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		timestamp   string
		expectedAge int
	}{
		{"today", now.Format(time.RFC3339), 0},
		{"5 days ago", now.AddDate(0, 0, -5).Format(time.RFC3339), 5},
		{"30 days ago", now.AddDate(0, 0, -30).Format(time.RFC3339), 30},
		{"empty timestamp", "", 999},
		{"invalid timestamp", "not-a-date", 999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pattern{Timestamp: tt.timestamp}
			result := getPatternAgeDays(p)
			// Allow 1 day tolerance for edge cases around midnight
			if result < tt.expectedAge-1 || result > tt.expectedAge+1 {
				t.Errorf("getPatternAgeDays(%q) = %d, want ~%d", tt.timestamp, result, tt.expectedAge)
			}
		})
	}
}

// TestSanitizeNotes tests control character removal from notes
func TestSanitizeNotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"clean text", "normal text", "normal text"},
		{"with newlines", "line1\nline2", "line1\nline2"},
		{"with tabs", "col1\tcol2", "col1\tcol2"},
		{"ANSI escape sequence", "text\x1b[31mred\x1b[0m", "text[31mred[0m"},
		{"null byte", "before\x00after", "beforeafter"},
		{"bell character", "alert\x07here", "alerthere"},
		{"carriage return", "win\r\nlines", "win\nlines"},
		{"form feed", "page\x0cbreak", "pagebreak"},
		{"mixed control chars", "\x01\x02hello\x1b[0m\x03world\n", "hello[0mworld\n"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeNotes(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeNotes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
