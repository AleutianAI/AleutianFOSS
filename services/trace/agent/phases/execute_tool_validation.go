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

// execute_tool_validation.go contains tool/query semantic validation functions.
// Extracted from execute_helpers.go as part of D3a decomposition.

import (
	"log/slog"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

// -----------------------------------------------------------------------------
// Semantic Validation
// -----------------------------------------------------------------------------

// ValidateToolQuerySemantics checks if the selected tool matches the query semantics.
//
// Description:
//
//	GR-Phase1: Post-router validation to catch obvious semantic mismatches.
//	Specifically designed to detect find_callers vs find_callees confusion.
//
// Inputs:
//
//	query - The user's query string.
//	selectedTool - The tool selected by the router.
//
// Outputs:
//
//	correctedTool - The validated/corrected tool name.
//	wasChanged - True if the tool was changed from the original selection.
//	reason - Explanation if the tool was changed.
func ValidateToolQuerySemantics(query, selectedTool string) (correctedTool string, wasChanged bool, reason string) {
	lowerQuery := strings.ToLower(query)

	// IT-05 R1: Detect call chain queries misrouted to find_callers or find_callees.
	// "Show the call chain from X" should always use get_call_chain.
	callChainPatterns := []string{
		"call chain",
		"call graph",
		"call hierarchy",
		"call tree",
		"transitive call",
		"recursive call",
		"full call",
	}
	if selectedTool == "find_callers" || selectedTool == "find_callees" {
		for _, pattern := range callChainPatterns {
			if strings.Contains(lowerQuery, pattern) {
				return "get_call_chain", true, "Query contains '" + pattern + "' which indicates get_call_chain, not " + selectedTool
			}
		}
	}

	// Pattern detection for callers vs callees confusion
	// Callees patterns: "what does X call", "what functions does X call", "what X calls"
	// Callers patterns: "who calls X", "what calls X", "callers of X"

	// Strong callees indicators (asking what a function calls, not who calls it)
	calleesPatterns := []string{
		"what does",      // "what does main call"
		"what functions", // "what functions does main call"
		"functions that", // "functions that main calls"
		"called by main", // "functions called by main" (main is the caller)
	}

	// Strong callers indicators (asking who/what calls a function)
	callersPatterns := []string{
		"who calls",     // "who calls parseConfig"
		"what calls",    // "what calls parseConfig"
		"callers of",    // "callers of parseConfig"
		"usages of",     // "usages of parseConfig"
		"uses of",       // "uses of parseConfig"
		"references to", // "references to parseConfig"
	}

	// Check for find_callers mismatch (should be find_callees)
	if selectedTool == "find_callers" {
		for _, pattern := range calleesPatterns {
			if strings.Contains(lowerQuery, pattern) {
				// Special case: "called by X" where X is the target means callers of X
				// But "functions called by X" where X is a function means callees of X
				if pattern == "called by main" {
					// Check if query is about a specific function being the caller
					// e.g., "what functions are called by main" → callees
					if strings.Contains(lowerQuery, "functions") ||
						strings.Contains(lowerQuery, "what is") ||
						strings.Contains(lowerQuery, "what are") {
						return "find_callees", true, "Query asks 'functions called BY X' which means callees (downstream), not callers"
					}
				} else {
					return "find_callees", true, "Query pattern '" + pattern + "' indicates callees (what X calls), not callers (who calls X)"
				}
			}
		}
	}

	// Check for find_callees mismatch (should be find_callers)
	if selectedTool == "find_callees" {
		for _, pattern := range callersPatterns {
			if strings.Contains(lowerQuery, pattern) {
				return "find_callers", true, "Query pattern '" + pattern + "' indicates callers (who calls X), not callees (what X calls)"
			}
		}
	}

	// No mismatch detected
	return selectedTool, false, ""
}

// hasSemanticCorrectionForQuery checks if a semantic correction has already been
// applied for the given query in this session.
//
// Description:
//
//	GR-Phase1: Prevents duplicate semantic correction warnings when Execute()
//	is called multiple times for the same query (e.g., after hard-forced tool
//	execution returns StateExecute).
//
// Inputs:
//
//	session - The agent session containing trace steps.
//	query - The user's query string.
//	correctedTool - The tool that was corrected to.
//
// Outputs:
//
//	bool - True if a semantic correction was already recorded for this query.
func hasSemanticCorrectionForQuery(session *agent.Session, query, correctedTool string) bool {
	if session == nil {
		return false
	}

	steps := session.GetTraceSteps()

	// GR-Phase1 Debug: Log what we're checking
	semanticCount := 0
	for _, s := range steps {
		if s.Action == "semantic_correction" {
			semanticCount++
		}
	}

	queryPreview := query
	if len(queryPreview) > 100 {
		queryPreview = queryPreview[:100]
	}

	if semanticCount > 0 || len(steps) > 5 {
		slog.Debug("GR-Phase1: hasSemanticCorrectionForQuery called",
			slog.Int("steps", len(steps)),
			slog.Int("semantic_corrections", semanticCount),
			slog.String("looking_for", correctedTool),
			slog.String("query_prefix", queryPreview[:min(30, len(queryPreview))]),
		)
	}

	if len(steps) == 0 {
		return false
	}

	for _, step := range steps {
		if step.Action != "semantic_correction" {
			continue
		}
		if step.Target != correctedTool {
			continue
		}

		// Check if this correction was for the same query
		// Use looser matching to handle truncation differences
		stepQuery, ok := step.Metadata["query_preview"]
		if !ok {
			// If no query recorded, consider it a match for safety
			// (older correction, same tool)
			slog.Debug("GR-Phase1: found match (no query metadata)",
				slog.String("target", step.Target),
			)
			return true
		}

		// Match if queries share a significant prefix (first 50 chars)
		minLen := 50
		if len(queryPreview) < minLen {
			minLen = len(queryPreview)
		}
		if len(stepQuery) < minLen {
			minLen = len(stepQuery)
		}
		if minLen > 0 && queryPreview[:minLen] == stepQuery[:minLen] {
			slog.Debug("GR-Phase1: found match (prefix)",
				slog.String("target", step.Target),
			)
			return true
		}

		// Also match if one is a prefix of the other
		if strings.HasPrefix(stepQuery, queryPreview) || strings.HasPrefix(queryPreview, stepQuery) {
			slog.Debug("GR-Phase1: found match (hasPrefix)",
				slog.String("target", step.Target),
			)
			return true
		}

		slog.Debug("GR-Phase1: near miss in semantic correction check",
			slog.String("action", step.Action),
			slog.String("target", step.Target),
		)
	}

	return false
}
