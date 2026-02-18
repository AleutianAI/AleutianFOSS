// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package file

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// GrepTool implements the Grep content search operation.
//
// Thread Safety: GrepTool is safe for concurrent use.
type GrepTool struct {
	config *Config
}

// NewGrepTool creates a new Grep tool with the given configuration.
func NewGrepTool(config *Config) *GrepTool {
	return &GrepTool{config: config}
}

// Name returns the tool name.
func (t *GrepTool) Name() string {
	return "Grep"
}

// Category returns the tool category.
func (t *GrepTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *GrepTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Grep",
		Description: "Search file contents using regex, fuzzy (fzf-style), or approximate (agrep-style) patterns. Returns matching lines with optional context.",
		Parameters: map[string]tools.ParamDef{
			"pattern": {
				Type:        tools.ParamTypeString,
				Description: "Pattern to search for (regex by default, or literal when fuzzy/approximate enabled)",
				Required:    true,
			},
			"path": {
				Type:        tools.ParamTypeString,
				Description: "File or directory to search. Defaults to working directory.",
				Required:    false,
			},
			"glob": {
				Type:        tools.ParamTypeString,
				Description: "File pattern filter (e.g., '*.go', '*.{ts,js}')",
				Required:    false,
			},
			"context_lines": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Lines of context before and after match (max %d)", MaxContextLines),
				Required:    false,
				Default:     0,
			},
			"case_insensitive": {
				Type:        tools.ParamTypeBool,
				Description: "Enable case-insensitive matching",
				Required:    false,
				Default:     false,
			},
			"limit": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Maximum matches to return. Defaults to %d, max %d.", DefaultGrepLimit, MaxGrepLimit),
				Required:    false,
				Default:     DefaultGrepLimit,
			},
			"fuzzy": {
				Type:        tools.ParamTypeBool,
				Description: "Enable fzf-style fuzzy matching (chars in order, not adjacent). Example: 'prsfil' matches 'parseFile'",
				Required:    false,
				Default:     false,
			},
			"approximate": {
				Type:        tools.ParamTypeBool,
				Description: "Enable agrep-style approximate matching using edit distance. Example: 'functon' matches 'function'",
				Required:    false,
				Default:     false,
			},
			"max_errors": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Maximum edit distance for approximate matching (default 2, max %d)", MaxFuzzyErrors),
				Required:    false,
				Default:     2,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    85,
		SideEffects: false,
		Timeout:     60 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Find function definitions (regex)",
				Parameters: map[string]any{
					"pattern": "func\\s+\\w+\\(",
					"glob":    "*.go",
				},
			},
			{
				Description: "Fuzzy search for parseFile",
				Parameters: map[string]any{
					"pattern": "prsfil",
					"fuzzy":   true,
					"glob":    "*.go",
				},
			},
			{
				Description: "Approximate search allowing typos",
				Parameters: map[string]any{
					"pattern":     "function",
					"approximate": true,
					"max_errors":  1,
				},
			},
		},
	}
}

// Execute searches for content matching the pattern.
func (t *GrepTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	p := &GrepParams{}
	if pattern, ok := params["pattern"].(string); ok {
		p.Pattern = pattern
	}
	if path, ok := params["path"].(string); ok {
		p.Path = path
	}
	if glob, ok := params["glob"].(string); ok {
		p.Glob = glob
	}
	if contextLines, ok := getIntParam(params, "context_lines"); ok {
		p.ContextLines = contextLines
	}
	if caseInsensitive, ok := params["case_insensitive"].(bool); ok {
		p.CaseInsensitive = caseInsensitive
	}
	if limit, ok := getIntParam(params, "limit"); ok {
		p.Limit = limit
	}
	if fuzzy, ok := params["fuzzy"].(bool); ok {
		p.Fuzzy = fuzzy
	}
	if approximate, ok := params["approximate"].(bool); ok {
		p.Approximate = approximate
	}
	// Track if max_errors was explicitly provided
	maxErrorsProvided := false
	if maxErrors, ok := getIntParam(params, "max_errors"); ok {
		p.MaxErrors = maxErrors
		maxErrorsProvided = true
	}

	// Set defaults
	if p.Limit == 0 {
		p.Limit = DefaultGrepLimit
	}
	if p.Path == "" {
		p.Path = t.config.WorkingDir
	} else if !filepath.IsAbs(p.Path) {
		// GR-38: Normalize relative paths to absolute by joining with working directory
		p.Path = filepath.Join(t.config.WorkingDir, p.Path)
	}
	// Only set default max_errors if not explicitly provided
	if p.Approximate && !maxErrorsProvided {
		p.MaxErrors = 2 // Default max errors for approximate matching
	}

	// Validate
	if err := p.Validate(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Create matcher based on mode
	var matcher lineMatcher
	if p.Fuzzy {
		matcher = &fuzzyMatcher{
			pattern:         p.Pattern,
			caseInsensitive: p.CaseInsensitive,
		}
	} else if p.Approximate {
		matcher = &approximateMatcher{
			pattern:         p.Pattern,
			maxErrors:       p.MaxErrors,
			caseInsensitive: p.CaseInsensitive,
		}
	} else {
		// Standard regex mode
		regexPattern := p.Pattern

		// GR-59 Group D: Auto-fix common regex errors before compiling.
		regexPattern = fixCommonRegexErrors(regexPattern)

		if p.CaseInsensitive {
			regexPattern = "(?i)" + regexPattern
		}
		re, err := regexp.Compile(regexPattern)
		if err != nil {
			return &tools.Result{
				Success:  false,
				Error:    fmt.Sprintf("invalid regex pattern: %v", err),
				Duration: time.Since(start),
			}, nil
		}
		matcher = &regexMatcher{re: re}
	}

	// Check if path is within allowed directories
	if !t.config.IsPathAllowed(p.Path) {
		return &tools.Result{
			Success:  false,
			Error:    "path is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Perform search
	matches, filesSearched, totalCount, err := t.grepFiles(ctx, p.Path, matcher, p.Glob, p.ContextLines, p.Limit)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	result := &GrepResult{
		Matches:       matches,
		Count:         len(matches),
		Truncated:     totalCount > p.Limit,
		FilesSearched: filesSearched,
		SearchPath:    p.Path,
	}

	// Format output as grep-style results
	var output strings.Builder
	for _, m := range matches {
		// Show context before
		for _, line := range m.ContextBefore {
			output.WriteString(fmt.Sprintf("%s-%d- %s\n", m.File, m.Line-len(m.ContextBefore), line))
		}
		// Show matching line
		output.WriteString(fmt.Sprintf("%s:%d: %s\n", m.File, m.Line, m.Content))
		// Show context after
		for i, line := range m.ContextAfter {
			output.WriteString(fmt.Sprintf("%s-%d- %s\n", m.File, m.Line+i+1, line))
		}
		if len(m.ContextBefore) > 0 || len(m.ContextAfter) > 0 {
			output.WriteString("--\n")
		}
	}

	jsonOutput, _ := json.Marshal(result)
	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: output.String(),
		Duration:   time.Since(start),
		TokensUsed: estimateTokens(string(jsonOutput)),
	}, nil
}

// grepFiles searches for pattern in files.
func (t *GrepTool) grepFiles(ctx context.Context, root string, matcher lineMatcher, globPattern string, contextLines, limit int) ([]GrepMatch, int, int, error) {
	var matches []GrepMatch
	filesSearched := 0
	totalMatches := 0

	// Build exclusion set
	exclusions := make(map[string]bool)
	for _, ex := range DefaultExclusions {
		exclusions[ex] = true
	}

	// Expand glob patterns for matching
	var filePatterns []string
	if globPattern != "" {
		filePatterns = expandBraces(globPattern)
	}

	// Check if root is a file
	info, err := os.Stat(root)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("accessing path: %w", err)
	}

	if !info.IsDir() {
		// Search single file
		fileMatches, err := t.searchFile(ctx, root, matcher, contextLines)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(fileMatches) > limit {
			return fileMatches[:limit], 1, len(fileMatches), nil
		}
		return fileMatches, 1, len(fileMatches), nil
	}

	// Walk directory
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // Skip errors
		}

		// Skip excluded directories
		if d.IsDir() {
			if exclusions[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Check glob pattern
		if len(filePatterns) > 0 {
			matched := false
			for _, p := range filePatterns {
				if m, _ := filepath.Match(p, d.Name()); m {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}

		// Skip binary files based on extension
		if isBinaryExtension(filepath.Ext(path)) {
			return nil
		}

		filesSearched++

		// Search file
		fileMatches, err := t.searchFile(ctx, path, matcher, contextLines)
		if err != nil {
			return nil // Skip file errors
		}

		totalMatches += len(fileMatches)

		// Collect matches up to limit
		for _, m := range fileMatches {
			if len(matches) >= limit {
				return filepath.SkipAll
			}
			matches = append(matches, m)
		}

		return nil
	})

	if err != nil && err != context.Canceled && err != filepath.SkipAll {
		return nil, filesSearched, totalMatches, fmt.Errorf("walking directory: %w", err)
	}

	return matches, filesSearched, totalMatches, nil
}

// searchFile searches for pattern matches in a single file.
func (t *GrepTool) searchFile(ctx context.Context, path string, matcher lineMatcher, contextLines int) ([]GrepMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []GrepMatch
	var lineBuffer []string // Rolling buffer for context
	lineNum := 0

	scanner := bufio.NewScanner(file)
	// Increase buffer for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxLineLengthChars*4)

	// Pending matches waiting for "after" context
	type pendingMatch struct {
		match       GrepMatch
		afterNeeded int
	}
	var pending []pendingMatch

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		lineNum++
		line := scanner.Text()

		// Truncate long lines
		if len(line) > MaxLineLengthChars {
			line = line[:MaxLineLengthChars] + "..."
		}

		// Add "after" context to pending matches
		for i := range pending {
			if pending[i].afterNeeded > 0 {
				pending[i].match.ContextAfter = append(pending[i].match.ContextAfter, line)
				pending[i].afterNeeded--
			}
		}

		// Move completed pending matches to results
		newPending := pending[:0]
		for _, p := range pending {
			if p.afterNeeded == 0 {
				matches = append(matches, p.match)
			} else {
				newPending = append(newPending, p)
			}
		}
		pending = newPending

		// Check for match
		if matcher.MatchString(line) {
			match := GrepMatch{
				File:    path,
				Line:    lineNum,
				Content: line,
			}

			// Add "before" context from buffer
			if contextLines > 0 && len(lineBuffer) > 0 {
				start := len(lineBuffer) - contextLines
				if start < 0 {
					start = 0
				}
				match.ContextBefore = make([]string, len(lineBuffer)-start)
				copy(match.ContextBefore, lineBuffer[start:])
			}

			if contextLines > 0 {
				pending = append(pending, pendingMatch{match: match, afterNeeded: contextLines})
			} else {
				matches = append(matches, match)
			}
		}

		// Update line buffer for context
		if contextLines > 0 {
			lineBuffer = append(lineBuffer, line)
			if len(lineBuffer) > contextLines {
				lineBuffer = lineBuffer[1:]
			}
		}
	}

	// Add remaining pending matches
	for _, p := range pending {
		matches = append(matches, p.match)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return matches, nil
}

// isBinaryExtension returns true for known binary file extensions.
func isBinaryExtension(ext string) bool {
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".bin": true, ".obj": true, ".o": true, ".a": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".ico": true, ".bmp": true, ".tiff": true, ".webp": true,
		".pdf": true, ".doc": true, ".docx": true, ".xls": true,
		".xlsx": true, ".ppt": true, ".pptx": true,
		".zip": true, ".tar": true, ".gz": true, ".bz2": true,
		".7z": true, ".rar": true, ".jar": true, ".war": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
		".wasm": true, ".pyc": true, ".pyo": true,
		".ttf": true, ".otf": true, ".woff": true, ".woff2": true,
		".db": true, ".sqlite": true, ".sqlite3": true,
	}
	return binaryExts[strings.ToLower(ext)]
}

// ============================================================================
// Matcher Implementations
// ============================================================================

// lineMatcher is the interface for different matching strategies.
type lineMatcher interface {
	// MatchString returns true if the line contains a match.
	MatchString(line string) bool
}

// regexMatcher implements lineMatcher using standard regex.
type regexMatcher struct {
	re *regexp.Regexp
}

// MatchString checks if line matches the regex.
func (m *regexMatcher) MatchString(line string) bool {
	return m.re.MatchString(line)
}

// fuzzyMatcher implements fzf-style fuzzy matching.
// Characters must appear in order but not necessarily adjacent.
// Example: "prsfil" matches "parseFile"
type fuzzyMatcher struct {
	pattern         string
	caseInsensitive bool
}

// MatchString checks if line contains a fuzzy match.
func (m *fuzzyMatcher) MatchString(line string) bool {
	pattern := m.pattern
	text := line

	if m.caseInsensitive {
		pattern = strings.ToLower(pattern)
		text = strings.ToLower(text)
	}

	return fuzzyMatch(pattern, text)
}

// fuzzyMatch checks if pattern chars appear in text in order.
// This is fzf-style matching where "abc" matches "aXbXc".
func fuzzyMatch(pattern, text string) bool {
	patternRunes := []rune(pattern)
	textRunes := []rune(text)

	patternIdx := 0
	for _, r := range textRunes {
		if patternIdx >= len(patternRunes) {
			break
		}
		if r == patternRunes[patternIdx] {
			patternIdx++
		}
	}

	return patternIdx == len(patternRunes)
}

// approximateMatcher implements agrep-style approximate matching.
// Uses Levenshtein distance to find matches within maxErrors edits.
type approximateMatcher struct {
	pattern         string
	maxErrors       int
	caseInsensitive bool
}

// MatchString checks if line contains an approximate match.
func (m *approximateMatcher) MatchString(line string) bool {
	pattern := m.pattern
	text := line

	if m.caseInsensitive {
		pattern = strings.ToLower(pattern)
		text = strings.ToLower(text)
	}

	return approximateMatch(pattern, text, m.maxErrors)
}

// approximateMatch checks if any substring of text matches pattern
// within maxErrors edit distance. Text should already be lowercased
// if case-insensitive matching is needed.
func approximateMatch(pattern, text string, maxErrors int) bool {
	patternLen := len([]rune(pattern))
	textRunes := []rune(text)
	textLen := len(textRunes)

	// For short patterns, check each word boundary
	words := splitIntoWords(text)
	for _, word := range words {
		if levenshteinDistance(pattern, word) <= maxErrors {
			return true
		}
	}

	// Also check sliding windows of pattern length +/- maxErrors
	minWindow := max(1, patternLen-maxErrors)
	maxWindow := min(textLen, patternLen+maxErrors)

	for windowSize := minWindow; windowSize <= maxWindow; windowSize++ {
		for i := 0; i <= textLen-windowSize; i++ {
			substr := string(textRunes[i : i+windowSize])
			if levenshteinDistance(pattern, substr) <= maxErrors {
				return true
			}
		}
	}

	return false
}

// splitIntoWords splits text into words (sequences of letters/digits).
func splitIntoWords(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}

	return words
}

// levenshteinDistance computes the edit distance between two strings.
// Uses the Wagner-Fischer algorithm with O(min(m,n)) space.
func levenshteinDistance(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)

	// Ensure r1 is the shorter string for space efficiency
	if len(r1) > len(r2) {
		r1, r2 = r2, r1
	}

	m, n := len(r1), len(r2)

	// Single row of the DP matrix
	prev := make([]int, m+1)
	curr := make([]int, m+1)

	// Initialize first row
	for i := 0; i <= m; i++ {
		prev[i] = i
	}

	// Fill the matrix row by row
	for j := 1; j <= n; j++ {
		curr[0] = j

		for i := 1; i <= m; i++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}

			curr[i] = min(
				prev[i]+1,      // deletion
				curr[i-1]+1,    // insertion
				prev[i-1]+cost, // substitution
			)
		}

		// Swap rows
		prev, curr = curr, prev
	}

	return prev[m]
}

// fixCommonRegexErrors attempts to fix the most common LLM regex mistake: unclosed parentheses.
//
// Description:
//
//	LLMs frequently generate regex patterns with unbalanced parentheses, e.g.,
//	"func (.*) Next(" where the trailing "(" is intended as a literal but is
//	unescaped. This function escapes the last unmatched open paren to produce
//	a compilable pattern.
//
// Inputs:
//
//   - pattern: The regex pattern string (max 1024 chars per MaxGrepPattern).
//
// Outputs:
//
//   - string: The fixed pattern. Unchanged if no fix was needed.
//
// Thread Safety: This function is safe for concurrent use.
func fixCommonRegexErrors(pattern string) string {
	openParens := strings.Count(pattern, "(") - strings.Count(pattern, "\\(")
	closeParens := strings.Count(pattern, ")") - strings.Count(pattern, "\\)")
	if openParens > closeParens {
		// Escape the last unmatched open paren.
		lastOpen := strings.LastIndex(pattern, "(")
		if lastOpen >= 0 && (lastOpen == 0 || pattern[lastOpen-1] != '\\') {
			pattern = pattern[:lastOpen] + "\\(" + pattern[lastOpen+1:]
		}
	}
	return pattern
}
