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

// execute_synthesis.go contains response synthesis and formatting functions
// extracted from execute.go as part of CB-30c Phase 2 decomposition.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
)

// -----------------------------------------------------------------------------
// Response Synthesis
// -----------------------------------------------------------------------------

// synthesizeFromToolResults builds a summary response from gathered tool results.
//
// Description:
//
//	When the LLM returns an empty response (often due to context overflow),
//	this function creates a useful response from the tool results already
//	collected. This provides graceful degradation instead of failing.
//
//	cb_30b enhancement: Added TraceSteps fallback for cases where ToolResults
//	is empty but tools executed (visible in TraceSteps). Also includes tool
//	errors in the synthesis so users understand what failed.
//
// Inputs:
//
//	deps - Phase dependencies containing tool results.
//
// Outputs:
//
//	string - Synthesized summary, empty if nothing to synthesize.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) synthesizeFromToolResults(deps *Dependencies) string {
	// CB-31 Enhancement: Diagnostic logging at entry for debugging empty response issues
	toolResultsCount := 0
	if deps.Context != nil {
		toolResultsCount = len(deps.Context.ToolResults)
	}
	traceStepsCount := 0
	toolCallStepsCount := 0
	if deps.Session != nil {
		steps := deps.Session.GetTraceSteps()
		traceStepsCount = len(steps)
		for _, step := range steps {
			if step.Action == "tool_call" || step.Action == "tool_call_forced" {
				toolCallStepsCount++
			}
		}
	}

	sessionID := "nil"
	if deps.Session != nil {
		sessionID = deps.Session.ID
	}

	slog.Debug("CB-31: synthesizeFromToolResults entering",
		slog.String("session_id", sessionID),
		slog.Bool("has_context", deps.Context != nil),
		slog.Int("tool_results_count", toolResultsCount),
		slog.Int("trace_steps_count", traceStepsCount),
		slog.Int("tool_call_steps_count", toolCallStepsCount),
	)

	// Primary path: Use ToolResults (preferred, has full output)
	if deps.Context != nil && len(deps.Context.ToolResults) > 0 {
		result := p.synthesizeFromToolResultsSlice(deps.Context.ToolResults)

		// Record synthesis in CRS for observability
		if deps.Session != nil {
			if result != "" {
				deps.Session.RecordTraceStep(crs.TraceStep{
					Action: "synthesis",
					Tool:   "tool_results",
					Metadata: map[string]string{
						"source":       "ToolResults",
						"result_count": fmt.Sprintf("%d", len(deps.Context.ToolResults)),
						"output_len":   fmt.Sprintf("%d", len(result)),
					},
				})
			} else {
				// CB-31: Log when ToolResults exist but synthesis returned empty
				// This catches edge cases like all results being duplicates or errors
				slog.Warn("CB-31: synthesizeFromToolResultsSlice returned empty despite having results",
					slog.String("session_id", deps.Session.ID),
					slog.Int("tool_results_count", len(deps.Context.ToolResults)),
				)
				// Fall through to TraceSteps fallback instead of returning empty
			}
		}

		// Only return if we got content; otherwise fall through to TraceSteps fallback
		if result != "" {
			return result
		}
	}

	// Fallback: Use TraceSteps if ToolResults is empty but tools executed
	// This catches edge cases where ToolResults wasn't populated
	if deps.Session != nil {
		steps := deps.Session.GetTraceSteps()
		toolSteps := filterToolCallSteps(steps)
		if len(toolSteps) > 0 {
			slog.Warn("synthesizeFromToolResults using TraceSteps fallback",
				slog.String("session_id", deps.Session.ID),
				slog.Int("trace_steps", len(toolSteps)),
				slog.Int("tool_results", 0),
			)

			result := p.synthesizeFromTraceSteps(toolSteps)

			// Record fallback synthesis in CRS - this is important for debugging
			if result != "" {
				deps.Session.RecordTraceStep(crs.TraceStep{
					Action: "synthesis",
					Tool:   "trace_steps_fallback",
					Metadata: map[string]string{
						"source":     "TraceSteps",
						"step_count": fmt.Sprintf("%d", len(toolSteps)),
						"output_len": fmt.Sprintf("%d", len(result)),
						"reason":     "ToolResults was empty",
					},
				})
			}

			return result
		}
	}

	// No content available for synthesis
	if deps.Session != nil {
		slog.Warn("synthesizeFromToolResults: no content available",
			slog.String("session_id", deps.Session.ID),
			slog.Bool("has_context", deps.Context != nil),
			slog.Int("tool_results", func() int {
				if deps.Context != nil {
					return len(deps.Context.ToolResults)
				}
				return 0
			}()),
		)
	}

	return ""
}

// synthesizeFromToolResultsSlice builds summary from tool results slice.
//
// Description:
//
//	Creates a human-readable summary from collected tool results, including
//	both successful outputs and error messages. This ensures users understand
//	what was discovered and what failed.
//
//	Phase 19 enhancement: When there is exactly one successful tool result
//	that is already well-formatted (contains markdown headers), pass it
//	through verbatim instead of wrapping it in a synthesis frame. This
//	preserves authoritative tool output (e.g., graph tool "Symbol not found"
//	messages) that would otherwise be flattened by the formatting pipeline.
//
// Inputs:
//
//	results - Tool results to summarize.
//
// Outputs:
//
//	string - Synthesized summary, empty if no content.
func (p *ExecutePhase) synthesizeFromToolResultsSlice(results []agent.ToolResult) string {
	// Phase 19: Pass through single well-formatted tool results verbatim.
	// Graph tools (find_implementations, find_callers, etc.) already produce
	// well-structured markdown output with headers and explanations. Wrapping
	// them in "Based on the codebase analysis:" adds noise and loses formatting.
	if singleResult, ok := getSingleFormattedResult(results); ok {
		return singleResult
	}

	var sb strings.Builder
	sb.WriteString("Based on the codebase analysis:\n\n")

	hasContent := false
	hasErrors := false

	// CR-20-8: Deduplicate results by FNV hash to avoid storing full output strings
	// as map keys. Graph tool outputs can be 10KB+; using the full string as a key
	// creates unnecessary memory pressure and slow map operations.
	seen := make(map[uint64]bool)

	for _, result := range results {
		if result.Success && result.Output != "" {
			// Skip duplicate outputs (e.g., from circuit breaker retries)
			h := fnv.New64a()
			h.Write([]byte(result.Output))
			hash := h.Sum64()
			if seen[hash] {
				continue
			}
			seen[hash] = true

			// Format the output - try to parse JSON and format nicely
			formatted := formatToolOutput(result.Output)
			sb.WriteString(formatted)
			sb.WriteString("\n\n")
			hasContent = true
		} else if !result.Success && result.Error != "" {
			// cb_30b: Include errors so user knows what failed
			sb.WriteString(fmt.Sprintf("**Error**: %s\n\n", result.Error))
			hasErrors = true
			hasContent = true
		}
	}

	if !hasContent {
		return ""
	}

	if hasErrors {
		sb.WriteString("*Some operations encountered errors. See details above.*\n\n")
	}
	sb.WriteString("*Note: This summary was generated from tool outputs due to context limitations.*")

	return sb.String()
}

// trivialReferenceLocationThreshold is the minimum number of reference locations
// required before the synthesis LLM is invoked for find_references results. Results
// with fewer locations are passed through verbatim — the synthesis model tends to
// return an empty response when there is "too little to say," producing a worse
// result than the raw tool output. Results at or above this threshold still go
// through LLM synthesis to produce richer narrative explanations.
const trivialReferenceLocationThreshold = 3

// getSingleFormattedResult checks if there is exactly one successful tool result
// that is already well-formatted with markdown headers (## GRAPH RESULT, Found N, etc.).
//
// Description:
//
//	Graph tools produce authoritative, well-structured output that should be
//	preserved verbatim during empty response recovery. This function detects
//	that case and returns the output for pass-through, avoiding the generic
//	"Based on the codebase analysis:" wrapper that flattens formatting.
//
// Inputs:
//
//	results - Tool results to check.
//
// Outputs:
//
//	string - The single formatted output, if applicable.
//	bool   - True if pass-through should be used.
//
// Limitations:
//
//	Only triggers for exactly one successful result with no errors.
//	Multi-result synthesis still uses the standard formatting pipeline.
func getSingleFormattedResult(results []agent.ToolResult) (string, bool) {
	var successCount int
	var errorCount int
	var emptySuccessCount int
	var singleOutput string

	for _, result := range results {
		if result.Success && result.Output != "" {
			successCount++
			singleOutput = result.Output
		} else if result.Success && result.Output == "" {
			// CR-20-6: Track success-with-empty-output separately. This indicates
			// another tool ran but produced nothing — the full synthesis path should
			// handle it rather than passing through the single non-empty result.
			emptySuccessCount++
		} else if !result.Success && result.Error != "" {
			errorCount++
		}
	}

	// Only pass through when there is exactly one successful result with output,
	// no errors, and no empty-output successes that might indicate partial results.
	if successCount != 1 || errorCount > 0 || emptySuccessCount > 0 {
		return "", false
	}

	// CR-20-5: Check for graph-specific markers only. The previous generic "## " check
	// would match any H2 header from any tool, causing unintended pass-through for
	// non-graph tools. Graph tools produce these specific patterns:
	//   - "## GRAPH RESULT" — used by find_implementations, find_callers, etc.
	//   - "Found N" prefix — used by successful graph queries
	//   - "these results are exhaustive" — definitive answer footer
	trimmed := strings.TrimSpace(singleOutput)

	// IT-06d Bug 12: For find_references positive results, allow pass-through when the
	// result is trivially small (fewer than trivialReferenceLocationThreshold locations).
	// Trivial results cause the synthesis LLM to return an empty response — the raw
	// tool output is cleaner than the empty-response fallback dump. Larger results
	// (≥ threshold) still go through LLM synthesis for richer narrative explanations.
	// Negative results ("not found") still pass through unconditionally to prevent
	// hallucination (unchanged from IT-06c).
	if strings.Contains(trimmed, "references to '") && !strings.Contains(strings.ToLower(trimmed), "not found") {
		if countReferenceLocations(trimmed) >= trivialReferenceLocationThreshold {
			return "", false
		}
		// Trivial result: fall through to pass-through below.
	}

	// IT-09: Do NOT pass through find_cycles results. Unlike other graph tools
	// that return definitive lists (find_callers, find_implementations), find_cycles
	// returns global unscoped results that LLM synthesis must narrate in context
	// of the user's query (package scoping, sorting, relevance filtering).
	if strings.Contains(trimmed, "circular dependencies") {
		return "", false
	}

	if strings.Contains(trimmed, "## GRAPH RESULT") ||
		strings.HasPrefix(trimmed, "Found ") ||
		strings.Contains(trimmed, "these results are exhaustive") {
		return singleOutput, true
	}

	return "", false
}

// synthesizeFromTraceSteps builds summary from trace steps when ToolResults unavailable.
//
// Description:
//
//	Fallback synthesis using TraceSteps when ToolResults is empty. Extracts
//	available information from trace metadata including tool names, targets,
//	symbols found, and errors. Less detailed than ToolResults but provides
//	useful context about what was explored.
//
// Inputs:
//
//	steps - Tool call trace steps.
//
// Outputs:
//
//	string - Synthesized summary from trace.
func (p *ExecutePhase) synthesizeFromTraceSteps(steps []crs.TraceStep) string {
	var sb strings.Builder
	sb.WriteString("Based on the codebase exploration:\n\n")

	hasContent := false
	hasErrors := false
	successCount := 0
	errorCount := 0

	for _, step := range steps {
		tool := step.Tool
		if tool == "" {
			tool = step.Target
		}
		if tool == "" {
			tool = "unknown"
		}

		if step.Error != "" {
			sb.WriteString(fmt.Sprintf("- **%s** failed: %s\n", tool, step.Error))
			hasErrors = true
			hasContent = true
			errorCount++
		} else {
			successCount++
			// Check for result summary in metadata
			if summary, ok := step.Metadata["summary"]; ok && summary != "" {
				sb.WriteString(fmt.Sprintf("- **%s**: %s\n", tool, truncateString(summary, 150)))
				hasContent = true
			} else if len(step.SymbolsFound) > 0 {
				sb.WriteString(fmt.Sprintf("- **%s**: Found %d symbols\n", tool, len(step.SymbolsFound)))
				hasContent = true
			} else if step.Target != "" {
				sb.WriteString(fmt.Sprintf("- **%s**: Processed %s\n", tool, truncateString(step.Target, 50)))
				hasContent = true
			}
		}
	}

	if !hasContent {
		// Even if no detailed content, report what happened
		if successCount > 0 || errorCount > 0 {
			sb.WriteString(fmt.Sprintf("Executed %d tool calls", successCount+errorCount))
			if errorCount > 0 {
				sb.WriteString(fmt.Sprintf(" (%d failed)", errorCount))
			}
			sb.WriteString(".\n")
			hasContent = true
		}
	}

	if !hasContent {
		return ""
	}

	if hasErrors {
		sb.WriteString("\n*Some tools encountered errors. Results may be incomplete.*")
	}
	sb.WriteString("\n\n*Note: Summary generated from execution trace (detailed outputs unavailable).*")

	return sb.String()
}

// -----------------------------------------------------------------------------
// Output Formatting
// -----------------------------------------------------------------------------

// formatToolOutput attempts to parse JSON tool output and format it nicely.
// Falls back to truncated raw output if parsing fails.
//
// Inputs:
//
//	output - Raw tool output string, may be JSON or plain text.
//
// Outputs:
//
//	string - Formatted, human-readable output.
func formatToolOutput(output string) string {
	// First, try to detect and parse JSON
	trimmed := strings.TrimSpace(output)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		// Not JSON, return truncated raw output
		return truncateOutput(output, 500)
	}

	// Try to parse as packages list (from list_packages tool)
	var packagesResult struct {
		Packages []struct {
			Name        string   `json:"name"`
			Path        string   `json:"path"`
			Files       []string `json:"files"`
			FileCount   int      `json:"file_count"`
			SymbolCount int      `json:"symbol_count"`
			Types       int      `json:"types"`
			Functions   int      `json:"functions"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(trimmed), &packagesResult); err == nil && len(packagesResult.Packages) > 0 {
		return formatPackagesOutput(packagesResult.Packages)
	}

	// Try to parse as config usage (from find_config_usage tool)
	var configResult struct {
		ConfigKey string   `json:"config_key"`
		UsedIn    []string `json:"used_in"`
	}
	if err := json.Unmarshal([]byte(trimmed), &configResult); err == nil && configResult.ConfigKey != "" {
		return formatConfigOutput(configResult.ConfigKey, configResult.UsedIn)
	}

	// Try to parse as generic JSON array
	var arrayResult []interface{}
	if err := json.Unmarshal([]byte(trimmed), &arrayResult); err == nil {
		return formatGenericArray(arrayResult)
	}

	// Try to parse as generic JSON object
	var objectResult map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &objectResult); err == nil {
		return formatGenericObject(objectResult)
	}

	// Fallback: truncate raw output
	return truncateOutput(output, 500)
}

// formatPackagesOutput formats a list of packages into readable text.
func formatPackagesOutput(packages []struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Files       []string `json:"files"`
	FileCount   int      `json:"file_count"`
	SymbolCount int      `json:"symbol_count"`
	Types       int      `json:"types"`
	Functions   int      `json:"functions"`
}) string {
	var sb strings.Builder
	for _, pkg := range packages {
		sb.WriteString(fmt.Sprintf("**Package `%s`** (`%s`):\n", pkg.Name, pkg.Path))
		if pkg.Functions > 0 {
			sb.WriteString(fmt.Sprintf("- %d exported functions\n", pkg.Functions))
		}
		if pkg.Types > 0 {
			sb.WriteString(fmt.Sprintf("- %d types defined\n", pkg.Types))
		}
		if pkg.SymbolCount > 0 {
			sb.WriteString(fmt.Sprintf("- %d total symbols\n", pkg.SymbolCount))
		}
		if len(pkg.Files) > 0 {
			sb.WriteString("- Files:\n")
			for i, f := range pkg.Files {
				if len(pkg.Files) > 5 && i >= 5 {
					sb.WriteString(fmt.Sprintf("  - ... and %d more files\n", len(pkg.Files)-5))
					break
				}
				sb.WriteString(fmt.Sprintf("  - `%s`\n", f))
			}
		}
	}
	return sb.String()
}

// formatConfigOutput formats config usage information.
func formatConfigOutput(configKey string, usedIn []string) string {
	var sb strings.Builder
	if configKey == "*" {
		if len(usedIn) == 0 {
			sb.WriteString("No configuration options were found in this codebase. ")
			sb.WriteString("The project may use environment variables, command-line flags, ")
			sb.WriteString("or hardcoded values instead of a formal configuration system.")
		} else {
			sb.WriteString("**Configuration files found:**\n")
			for _, loc := range usedIn {
				sb.WriteString(fmt.Sprintf("- `%s`\n", loc))
			}
		}
	} else {
		sb.WriteString(fmt.Sprintf("**Config key `%s`**:\n", configKey))
		if len(usedIn) == 0 {
			sb.WriteString("- Not found in any files\n")
		} else {
			sb.WriteString("- Used in:\n")
			for _, loc := range usedIn {
				sb.WriteString(fmt.Sprintf("  - `%s`\n", loc))
			}
		}
	}
	return sb.String()
}

// formatGenericArray formats a JSON array into readable text.
func formatGenericArray(arr []interface{}) string {
	if len(arr) == 0 {
		return "No results found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d items:\n", len(arr)))

	for i, item := range arr {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("... and %d more items\n", len(arr)-10))
			break
		}
		switch v := item.(type) {
		case string:
			sb.WriteString(fmt.Sprintf("- %s\n", truncateOutput(v, 100)))
		case map[string]interface{}:
			// Try to extract a meaningful identifier
			if name, ok := v["name"].(string); ok {
				sb.WriteString(fmt.Sprintf("- `%s`\n", name))
			} else if path, ok := v["path"].(string); ok {
				sb.WriteString(fmt.Sprintf("- `%s`\n", path))
			} else {
				sb.WriteString(fmt.Sprintf("- Item %d\n", i+1))
			}
		default:
			sb.WriteString(fmt.Sprintf("- %v\n", item))
		}
	}
	return sb.String()
}

// formatGenericObject formats a JSON object into readable text.
func formatGenericObject(obj map[string]interface{}) string {
	if len(obj) == 0 {
		return "Empty result."
	}

	var sb strings.Builder
	count := 0
	for key, val := range obj {
		if count >= 10 {
			sb.WriteString(fmt.Sprintf("... and %d more fields\n", len(obj)-10))
			break
		}
		switch v := val.(type) {
		case string:
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", key, truncateOutput(v, 100)))
		case []interface{}:
			sb.WriteString(fmt.Sprintf("- **%s**: %d items\n", key, len(v)))
		case float64:
			sb.WriteString(fmt.Sprintf("- **%s**: %.0f\n", key, v))
		case bool:
			sb.WriteString(fmt.Sprintf("- **%s**: %t\n", key, v))
		default:
			sb.WriteString(fmt.Sprintf("- **%s**: %v\n", key, val))
		}
		count++
	}
	return sb.String()
}

// -----------------------------------------------------------------------------
// Trace Step Filtering
// -----------------------------------------------------------------------------

// countReferenceLocations counts the number of file:line location entries in a
// find_references tool result output string.
//
// Description:
//
//	Parses the "Found N references" header line to extract the location count.
//	Falls back to counting bullet-prefixed lines if the header is absent or
//	unparseable. Used by getSingleFormattedResult to determine whether LLM
//	synthesis adds value or risks producing an empty response.
//
// Inputs:
//
//	output - Raw find_references tool output (trimmed).
//
// Outputs:
//
//	int - Number of reference locations reported. Returns 0 if unparseable.
func countReferenceLocations(output string) int {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Found ") {
			var n int
			if _, err := fmt.Sscanf(line, "Found %d", &n); err == nil {
				return n
			}
		}
	}
	// Fallback: count bullet-prefixed lines that contain a colon (file:line format).
	count := 0
	for _, line := range strings.Split(output, "\n") {
		trimLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimLine, "- ") && strings.ContainsRune(trimLine, ':') {
			count++
		}
	}
	return count
}

// filterToolCallSteps extracts tool_call steps from trace.
//
// Inputs:
//
//	steps - All trace steps.
//
// Outputs:
//
//	[]crs.TraceStep - Only tool_call and tool_call_forced steps.
func filterToolCallSteps(steps []crs.TraceStep) []crs.TraceStep {
	filtered := make([]crs.TraceStep, 0, len(steps))
	for _, step := range steps {
		if step.Action == "tool_call" || step.Action == "tool_call_forced" {
			filtered = append(filtered, step)
		}
	}
	return filtered
}
