// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

// execute_synthesis_quality.go implements CRS-20: Synthesis Quality Gate for Small Models.
//
// This module scores how well the LLM synthesis response reflects tool results.
// It uses simple string-matching heuristics (< 100ms) — NOT LLM-based validation.
// The score is observability-only: it does not block or retry responses.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

// symbolNamePattern matches common symbol name formats in tool output:
//   - backtick-quoted: `MyFunction`
//   - parenthesized function: MyFunction()
//   - "Found N implementations of 'Symbol'"
//   - "• SymbolName (struct|interface|class|func) in file.go:123"
var symbolNamePattern = regexp.MustCompile(
	"`([A-Z][A-Za-z0-9_]+)`" + // backtick-quoted PascalCase
		`|'([A-Z][A-Za-z0-9_]+)'` + // single-quoted PascalCase
		`|([A-Z][A-Za-z0-9_]+)\(\)` + // function call notation
		`|• ([A-Z][A-Za-z0-9_]+) \(`, // bullet-point struct/interface
)

// SynthesisQualityResult holds the quality assessment of a synthesis response.
//
// Description:
//
//	Contains the quality score and diagnostic details for observability.
//	Used by handleCompletion to record synthesis quality in trace steps
//	and Prometheus metrics.
//
// Thread Safety: This struct is immutable after creation.
type SynthesisQualityResult struct {
	// Score is the quality score from 0.0 to 1.0.
	//   1.0: Response references specific symbols from results
	//   0.5: Response mentions general findings but no specific symbols
	//   0.0: Response contradicts or ignores tool results
	Score float64

	// SymbolsExpected is the number of distinct symbols extracted from tool results.
	SymbolsExpected int

	// SymbolsFound is the number of expected symbols mentioned in the response.
	SymbolsFound int

	// HasResultCount indicates whether the response mentions result counts from tools.
	HasResultCount bool

	// MentionsScope indicates whether the response mentions scope when scope was applied.
	MentionsScope bool

	// ScopeRelevant indicates whether scope was applied to any tool result.
	ScopeRelevant bool

	// Reason is a short diagnostic string explaining the score.
	Reason string
}

// scoreSynthesisQuality evaluates how well the LLM response reflects tool results.
//
// Description:
//
//	Compares the synthesis response against tool result content using string
//	matching heuristics. Extracts symbol names from tool outputs and checks
//	whether the response mentions them. Also checks for result count mentions
//	and scope awareness.
//
//	This function is designed to run in < 100ms (simple string operations only).
//
// Inputs:
//
//   - response: The LLM synthesis response text.
//   - toolResults: The tool results that were available during synthesis.
//
// Outputs:
//
//   - SynthesisQualityResult: Quality assessment with score and diagnostics.
//
// Limitations:
//
//   - Cannot detect semantic correctness (e.g., wrong interpretation of data).
//   - Cannot detect hallucinated symbols that happen to not appear in results.
//   - Score is a rough heuristic, not a ground-truth quality measure.
//
// Assumptions:
//
//   - Tool results contain symbol names in recognizable formats.
//   - Response text is non-empty (caller should handle empty responses separately).
func scoreSynthesisQuality(response string, toolResults []agent.ToolResult) SynthesisQualityResult {
	result := SynthesisQualityResult{}

	if response == "" || len(toolResults) == 0 {
		result.Score = 0.0
		result.Reason = "empty_response_or_no_results"
		return result
	}

	responseLower := strings.ToLower(response)

	// Step 1: Extract symbol names from tool results.
	symbols := extractSymbolNames(toolResults)
	result.SymbolsExpected = len(symbols)

	// Step 2: Check which symbols appear in the response.
	for _, sym := range symbols {
		if strings.Contains(responseLower, strings.ToLower(sym)) {
			result.SymbolsFound++
		}
	}

	// Step 3: Check if response mentions result counts.
	// Look for patterns like "Found 20", "20 hotspots", "no results", "0 results"
	result.HasResultCount = hasResultCountMention(responseLower, toolResults)

	// Step 4: Check scope awareness.
	result.ScopeRelevant, result.MentionsScope = checkScopeAwareness(responseLower, toolResults)

	// Step 5: Compute composite score.
	result.Score, result.Reason = computeQualityScore(result)

	return result
}

// extractSymbolNames extracts distinct symbol names from tool result outputs.
//
// Description:
//
//	Parses tool output text for recognizable symbol name patterns using regex.
//	Returns deduplicated list of symbols. Limits to 50 symbols to bound
//	processing time.
//
// Inputs:
//
//   - toolResults: Tool results containing output text.
//
// Outputs:
//
//   - []string: Deduplicated symbol names extracted from outputs.
func extractSymbolNames(toolResults []agent.ToolResult) []string {
	seen := make(map[string]bool)
	var symbols []string
	const maxSymbols = 50

	for _, tr := range toolResults {
		if !tr.Success || tr.Output == "" {
			continue
		}

		matches := symbolNamePattern.FindAllStringSubmatch(tr.Output, -1)
		for _, match := range matches {
			// FindAllStringSubmatch returns the full match + capture groups.
			// Only one capture group will be non-empty per match.
			for i := 1; i < len(match); i++ {
				if match[i] != "" {
					sym := match[i]
					if !seen[sym] && len(sym) >= 3 { // Skip very short names
						seen[sym] = true
						symbols = append(symbols, sym)
						if len(symbols) >= maxSymbols {
							return symbols
						}
					}
					break
				}
			}
		}
	}
	return symbols
}

// hasResultCountMention checks if the response mentions result counts from tool output.
//
// Inputs:
//
//   - responseLower: Lowercased response text.
//   - toolResults: Tool results to extract counts from.
//
// Outputs:
//
//   - bool: True if response mentions a result count or "no results"/"not found".
func hasResultCountMention(responseLower string, toolResults []agent.ToolResult) bool {
	// Check for "no results" / "not found" mentions
	if strings.Contains(responseLower, "no results") ||
		strings.Contains(responseLower, "not found") ||
		strings.Contains(responseLower, "no circular") ||
		strings.Contains(responseLower, "no hotspots") ||
		strings.Contains(responseLower, "no dead code") {
		return true
	}

	// Check if response mentions specific counts from tool output
	for _, tr := range toolResults {
		if !tr.Success || tr.Output == "" {
			continue
		}
		// Extract "Found N" from tool output
		outputLower := strings.ToLower(tr.Output)
		var count int
		if _, err := fmt.Sscanf(outputLower, "found %d", &count); err == nil && count > 0 {
			// Check if response mentions this count
			countStr := fmt.Sprintf("%d", count)
			if strings.Contains(responseLower, countStr) {
				return true
			}
		}
	}

	return false
}

// checkScopeAwareness checks if the response acknowledges scope when scope was applied.
//
// Description:
//
//	Detects whether tool results indicate scope was applied (by looking for
//	scope-related keywords in tool output), and whether the response
//	acknowledges the scope.
//
// Inputs:
//
//   - responseLower: Lowercased response text.
//   - toolResults: Tool results to check for scope indicators.
//
// Outputs:
//
//   - scopeRelevant: True if scope was applied to any tool result.
//   - mentionsScope: True if response mentions the scope.
func checkScopeAwareness(responseLower string, toolResults []agent.ToolResult) (scopeRelevant bool, mentionsScope bool) {
	// Look for scope indicators in tool output.
	// Scope relaxation produces output like "retrying with wider scope" or
	// the tool output may mention "package filter" or "scope".
	// Also look for common scope patterns in the output text.
	for _, tr := range toolResults {
		if !tr.Success || tr.Output == "" {
			continue
		}
		outputLower := strings.ToLower(tr.Output)
		if strings.Contains(outputLower, "package") ||
			strings.Contains(outputLower, "scope") ||
			strings.Contains(outputLower, "filtered") ||
			strings.Contains(outputLower, "globally") {
			scopeRelevant = true
			break
		}
	}

	if !scopeRelevant {
		return false, false
	}

	// Check if response mentions scope-related terms.
	mentionsScope = strings.Contains(responseLower, "package") ||
		strings.Contains(responseLower, "scope") ||
		strings.Contains(responseLower, "globally") ||
		strings.Contains(responseLower, "across the codebase") ||
		strings.Contains(responseLower, "all packages") ||
		strings.Contains(responseLower, "no specific")

	return scopeRelevant, mentionsScope
}

// computeQualityScore computes the final quality score from component checks.
//
// Scoring:
//   - Symbol mention: 60% weight (primary quality signal)
//   - Result count mention: 20% weight
//   - Scope awareness: 20% weight (only when scope is relevant)
//
// When scope is not relevant, the weights redistribute:
//   - Symbol mention: 70% weight
//   - Result count mention: 30% weight
//
// Inputs:
//
//   - r: Quality result with component checks populated.
//
// Outputs:
//
//   - float64: Quality score 0.0–1.0.
//   - string: Short reason explaining the score.
func computeQualityScore(r SynthesisQualityResult) (float64, string) {
	var score float64
	var reasons []string

	if r.SymbolsExpected == 0 {
		// No symbols to check — can't assess symbol coverage.
		// Score based on result count mention only.
		if r.HasResultCount {
			return 0.7, "no_symbols_expected;has_count"
		}
		return 0.5, "no_symbols_expected;no_count"
	}

	// Symbol coverage ratio.
	symbolRatio := float64(r.SymbolsFound) / float64(r.SymbolsExpected)
	if symbolRatio > 1.0 {
		symbolRatio = 1.0
	}

	if r.ScopeRelevant {
		// Scope-aware scoring: symbols=60%, count=20%, scope=20%
		score = symbolRatio * 0.6

		if r.HasResultCount {
			score += 0.2
			reasons = append(reasons, "has_count")
		}
		if r.MentionsScope {
			score += 0.2
			reasons = append(reasons, "scope_aware")
		} else {
			reasons = append(reasons, "scope_missing")
		}
	} else {
		// No scope: symbols=70%, count=30%
		score = symbolRatio * 0.7

		if r.HasResultCount {
			score += 0.3
			reasons = append(reasons, "has_count")
		}
	}

	// Classify symbol coverage
	switch {
	case r.SymbolsFound == 0:
		reasons = append(reasons, "no_symbols_mentioned")
	case symbolRatio < 0.3:
		reasons = append(reasons, fmt.Sprintf("few_symbols(%d/%d)", r.SymbolsFound, r.SymbolsExpected))
	case symbolRatio < 0.7:
		reasons = append(reasons, fmt.Sprintf("some_symbols(%d/%d)", r.SymbolsFound, r.SymbolsExpected))
	default:
		reasons = append(reasons, fmt.Sprintf("good_symbols(%d/%d)", r.SymbolsFound, r.SymbolsExpected))
	}

	reason := strings.Join(reasons, ";")
	return score, reason
}
