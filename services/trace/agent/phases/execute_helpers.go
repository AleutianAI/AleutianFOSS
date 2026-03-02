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

// execute_helpers.go contains standalone utility functions: string truncation,
// parameter extraction, parsing, and tool name helpers. Extracted from the
// original file as part of D3a decomposition.

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// Semantic Correction Cache (GR-Phase1)
// -----------------------------------------------------------------------------

// semanticCorrectionCache tracks which corrections have been applied per session.
// This is a simple in-memory cache to avoid duplicate warnings when Execute()
// is called multiple times for the same query.
var (
	semanticCorrectionCache   = make(map[string]bool) // key: "sessionID:queryHash:tool"
	semanticCorrectionCacheMu sync.RWMutex
)

// markSemanticCorrectionApplied records that a semantic correction was applied.
func markSemanticCorrectionApplied(sessionID, query, correctedTool string) {
	key := buildSemanticCorrectionKey(sessionID, query, correctedTool)
	semanticCorrectionCacheMu.Lock()
	semanticCorrectionCache[key] = true
	semanticCorrectionCacheMu.Unlock()
}

// hasSemanticCorrectionInCache checks if a correction was already applied.
func hasSemanticCorrectionInCache(sessionID, query, correctedTool string) bool {
	key := buildSemanticCorrectionKey(sessionID, query, correctedTool)
	semanticCorrectionCacheMu.RLock()
	defer semanticCorrectionCacheMu.RUnlock()
	return semanticCorrectionCache[key]
}

// buildSemanticCorrectionKey creates a cache key from session, query, and tool.
func buildSemanticCorrectionKey(sessionID, query, correctedTool string) string {
	// Use first 50 chars of query to avoid huge keys
	queryKey := query
	if len(queryKey) > 50 {
		queryKey = queryKey[:50]
	}
	return fmt.Sprintf("%s:%s:%s", sessionID, queryKey, correctedTool)
}

// ClearSemanticCorrectionCache clears the cache (for testing).
func ClearSemanticCorrectionCache() {
	semanticCorrectionCacheMu.Lock()
	semanticCorrectionCache = make(map[string]bool)
	semanticCorrectionCacheMu.Unlock()
}

// -----------------------------------------------------------------------------
// String Truncation Utilities
// -----------------------------------------------------------------------------

// truncateString truncates a string to maxLen with ellipsis.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// truncateQuery truncates a query string for logging.
func truncateQuery(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateOutput truncates a string to maxLen characters.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// truncateForLog truncates a string for logging, attempting word boundaries.
//
// # Inputs
//
//   - s: String to truncate.
//   - maxLen: Maximum length.
//
// # Outputs
//
//   - string: Truncated string with "..." suffix if truncated.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space before maxLen
	truncated := s[:maxLen]
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}

// -----------------------------------------------------------------------------
// Parameter Extraction Utilities
// -----------------------------------------------------------------------------

// getStringParamFromToolParams extracts a string parameter from tool parameters.
//
// Inputs:
//
//	params - The tool parameters.
//	key - The parameter key to extract.
//
// Outputs:
//
//	string - The parameter value, or empty string if not found
func getStringParamFromToolParams(params *agent.ToolParameters, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params.GetString(key); ok {
		return v
	}
	return ""
}

// toolParamsToMap converts ToolParameters to a map for internal tool execution.
//
// Inputs:
//
//	params - The tool parameters
//
// Outputs:
//
//	map[string]any - Parameters as a map
func toolParamsToMap(params *agent.ToolParameters) map[string]any {
	result := make(map[string]any)
	if params == nil {
		return result
	}

	for k, v := range params.StringParams {
		result[k] = v
	}
	for k, v := range params.IntParams {
		result[k] = v
	}
	for k, v := range params.BoolParams {
		result[k] = v
	}

	return result
}

// -----------------------------------------------------------------------------
// Tool Name Utilities
// -----------------------------------------------------------------------------

// getAvailableToolNames extracts tool names from tool definitions.
//
// Inputs:
//
//	toolDefs - Tool definitions.
//
// Outputs:
//
//	[]string - List of tool names.
func getAvailableToolNames(toolDefs []tools.ToolDefinition) []string {
	names := make([]string, len(toolDefs))
	for i, def := range toolDefs {
		names[i] = def.Name
	}
	return names
}

// parseInt attempts to parse a string as an integer.
//
// Description:
//
//	Wrapper around strconv.Atoi that returns 0 on error.
//	Used by parameter extraction logic to parse numeric values from queries.
//
// Inputs:
//
//	s - The string to parse.
//
// Outputs:
//
//	int - The parsed integer, or 0 if parsing fails.
//
// Thread Safety: Safe for concurrent use.
func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// splitCamelCase inserts spaces at camelCase and PascalCase word boundaries.
// It preserves dots, underscores, and leading underscores.
//
// Examples:
//
//	"sceneGraphUpdate"  → "scene Graph Update"
//	"HTMLParser"        → "HTML Parser"
//	"getHTTPResponse"   → "get HTTP Response"
//	"my_variable"       → "my_variable"
func splitCamelCase(s string) string {
	if len(s) <= 1 {
		return s
	}
	var result strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			// Insert space before uppercase if preceded by lowercase
			if unicode.IsLower(prev) {
				result.WriteRune(' ')
			} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				// Insert space before start of new word in acronym sequence
				// e.g., "HTMLParser" → "HTML Parser" (space before 'P')
				result.WriteRune(' ')
			}
		}
		result.WriteRune(r)
	}
	return result.String()
}
