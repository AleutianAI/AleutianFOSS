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

import (
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

func TestScoreSynthesisQuality_EmptyInputs(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		result := scoreSynthesisQuality("", []agent.ToolResult{
			{Success: true, Output: "Found 5 hotspots"},
		})
		if result.Score != 0.0 {
			t.Errorf("Score = %.2f, want 0.0", result.Score)
		}
		if result.Reason != "empty_response_or_no_results" {
			t.Errorf("Reason = %q, want empty_response_or_no_results", result.Reason)
		}
	})

	t.Run("no tool results", func(t *testing.T) {
		result := scoreSynthesisQuality("Some response", nil)
		if result.Score != 0.0 {
			t.Errorf("Score = %.2f, want 0.0", result.Score)
		}
	})

	t.Run("empty tool results slice", func(t *testing.T) {
		result := scoreSynthesisQuality("Some response", []agent.ToolResult{})
		if result.Score != 0.0 {
			t.Errorf("Score = %.2f, want 0.0", result.Score)
		}
	})
}

func TestScoreSynthesisQuality_HighQuality(t *testing.T) {
	toolResults := []agent.ToolResult{
		{
			Success: true,
			Output:  "Found 20 hotspots:\n\n1. `ParseConfig` (complexity: 45)\n2. `HandleRequest` (complexity: 38)\n3. `ValidateInput` (complexity: 32)\n",
		},
	}

	response := "The codebase has 20 hotspots. The most complex function is ParseConfig with a complexity of 45, " +
		"followed by HandleRequest (38) and ValidateInput (32)."

	result := scoreSynthesisQuality(response, toolResults)

	if result.Score < 0.8 {
		t.Errorf("Score = %.2f, want >= 0.8 for high quality response", result.Score)
	}
	if result.SymbolsFound < 3 {
		t.Errorf("SymbolsFound = %d, want >= 3", result.SymbolsFound)
	}
	if !result.HasResultCount {
		t.Error("HasResultCount = false, want true (response mentions '20')")
	}
}

func TestScoreSynthesisQuality_LowQuality_IgnoresResults(t *testing.T) {
	toolResults := []agent.ToolResult{
		{
			Success: true,
			Output:  "Found 20 hotspots:\n\n1. `ParseConfig` (complexity: 45)\n2. `HandleRequest` (complexity: 38)\n",
		},
	}

	// Response that ignores the tool output entirely
	response := "I don't have enough information to answer your question about the codebase."

	result := scoreSynthesisQuality(response, toolResults)

	if result.Score >= 0.5 {
		t.Errorf("Score = %.2f, want < 0.5 for response that ignores results", result.Score)
	}
	if result.SymbolsFound != 0 {
		t.Errorf("SymbolsFound = %d, want 0", result.SymbolsFound)
	}
}

func TestScoreSynthesisQuality_MediumQuality_PartialSymbols(t *testing.T) {
	toolResults := []agent.ToolResult{
		{
			Success: true,
			Output:  "Found 5 implementations:\n\n• Iterator (interface) in iter.go:1\n• Table (struct) in table.go:50\n• Stream (struct) in stream.go:10\n• Batch (struct) in batch.go:20\n• Pipeline (struct) in pipeline.go:5\n",
		},
	}

	// Mentions only 2 of 5 symbols
	response := "There are 5 implementations. Iterator is the main interface, implemented by Table."

	result := scoreSynthesisQuality(response, toolResults)

	if result.Score < 0.3 || result.Score > 0.8 {
		t.Errorf("Score = %.2f, want between 0.3 and 0.8 for partial coverage", result.Score)
	}
	if result.SymbolsExpected != 5 {
		t.Errorf("SymbolsExpected = %d, want 5", result.SymbolsExpected)
	}
	if result.SymbolsFound != 2 {
		t.Errorf("SymbolsFound = %d, want 2 (Iterator, Table)", result.SymbolsFound)
	}
}

func TestScoreSynthesisQuality_ScopeAwareness(t *testing.T) {
	t.Run("scope relevant and mentioned", func(t *testing.T) {
		toolResults := []agent.ToolResult{
			{
				Success: true,
				Output:  "Found 20 hotspots globally (no results in filtered package):\n\n1. `ParseConfig` (complexity: 45)\n",
			},
		}

		response := "There are 20 hotspots globally across the codebase. ParseConfig is the most complex. " +
			"No hotspots were found in the specific package you asked about."

		result := scoreSynthesisQuality(response, toolResults)

		if !result.ScopeRelevant {
			t.Error("ScopeRelevant = false, want true")
		}
		if !result.MentionsScope {
			t.Error("MentionsScope = false, want true")
		}
	})

	t.Run("scope relevant but not mentioned", func(t *testing.T) {
		toolResults := []agent.ToolResult{
			{
				Success: true,
				Output:  "Found 20 hotspots globally (no results in filtered package):\n\n1. `ParseConfig` (complexity: 45)\n",
			},
		}

		// Response that ignores the scope aspect
		response := "ParseConfig is the most complex function with complexity 45."

		result := scoreSynthesisQuality(response, toolResults)

		if !result.ScopeRelevant {
			t.Error("ScopeRelevant = false, want true")
		}
		if result.MentionsScope {
			t.Error("MentionsScope = true, want false (response doesn't mention scope)")
		}
		// Score should be penalized for missing scope
		if result.Score > 0.8 {
			t.Errorf("Score = %.2f, want <= 0.8 (scope mention missing)", result.Score)
		}
	})
}

func TestScoreSynthesisQuality_NoSymbolsExpected(t *testing.T) {
	t.Run("no symbols but has count", func(t *testing.T) {
		toolResults := []agent.ToolResult{
			{
				Success: true,
				Output:  "No circular dependencies found.\nThis is good news! The codebase has no detectable cycles.\n",
			},
		}

		response := "No results were found in the codebase."

		result := scoreSynthesisQuality(response, toolResults)

		if result.SymbolsExpected != 0 {
			t.Errorf("SymbolsExpected = %d, want 0", result.SymbolsExpected)
		}
		// Should get partial credit for mentioning "no results"
		if result.Score < 0.5 {
			t.Errorf("Score = %.2f, want >= 0.5 for mentioning no results", result.Score)
		}
	})
}

func TestScoreSynthesisQuality_FailedToolResults(t *testing.T) {
	toolResults := []agent.ToolResult{
		{
			Success: false,
			Error:   "graph analytics not initialized",
		},
	}

	response := "The analysis could not be completed due to an initialization error."

	result := scoreSynthesisQuality(response, toolResults)

	// No successful results → no symbols expected
	if result.SymbolsExpected != 0 {
		t.Errorf("SymbolsExpected = %d, want 0", result.SymbolsExpected)
	}
}

func TestExtractSymbolNames(t *testing.T) {
	t.Run("backtick quoted", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "Found 3 hotspots:\n1. `ParseConfig` (45)\n2. `HandleRequest` (38)\n3. `ValidateInput` (32)\n"},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) != 3 {
			t.Errorf("len(symbols) = %d, want 3", len(symbols))
		}
		expected := map[string]bool{"ParseConfig": true, "HandleRequest": true, "ValidateInput": true}
		for _, s := range symbols {
			if !expected[s] {
				t.Errorf("unexpected symbol %q", s)
			}
		}
	})

	t.Run("bullet point format", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "• Iterator (interface) in iter.go:1\n• Table (struct) in table.go:50\n"},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) != 2 {
			t.Errorf("len(symbols) = %d, want 2", len(symbols))
		}
	})

	t.Run("function call notation", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "  -> ParseConfig() [config.go:10]\n  -> HandleRequest() [server.go:20]\n"},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) != 2 {
			t.Errorf("len(symbols) = %d, want 2", len(symbols))
		}
	})

	t.Run("single quoted", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "Found 5 implementations of 'Iterator':\n"},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) != 1 || symbols[0] != "Iterator" {
			t.Errorf("symbols = %v, want [Iterator]", symbols)
		}
	})

	t.Run("skips failed results", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: false, Output: "`SomeSymbol` in error output"},
			{Success: true, Output: "`RealSymbol` found"},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) != 1 || symbols[0] != "RealSymbol" {
			t.Errorf("symbols = %v, want [RealSymbol]", symbols)
		}
	})

	t.Run("deduplicates", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "`ParseConfig` is called by `HandleRequest`.\n`ParseConfig` also used here."},
		}
		symbols := extractSymbolNames(results)
		found := 0
		for _, s := range symbols {
			if s == "ParseConfig" {
				found++
			}
		}
		if found != 1 {
			t.Errorf("ParseConfig appeared %d times, want 1 (dedup)", found)
		}
	})

	t.Run("caps at 50", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 60; i++ {
			sb.WriteString("`Symbol")
			sb.WriteString(strings.Repeat("X", i)) // Unique names, all start with uppercase
			sb.WriteString("` found\n")
		}
		results := []agent.ToolResult{
			{Success: true, Output: sb.String()},
		}
		symbols := extractSymbolNames(results)
		if len(symbols) > 50 {
			t.Errorf("len(symbols) = %d, want <= 50", len(symbols))
		}
	})
}

func TestHasResultCountMention(t *testing.T) {
	t.Run("mentions count from tool output", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "Found 20 hotspots"},
		}
		if !hasResultCountMention("there are 20 hotspots in the codebase", results) {
			t.Error("want true: response mentions count '20'")
		}
	})

	t.Run("mentions no results", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "No results found"},
		}
		if !hasResultCountMention("no results were found", results) {
			t.Error("want true: response mentions 'no results'")
		}
	})

	t.Run("no mention at all", func(t *testing.T) {
		results := []agent.ToolResult{
			{Success: true, Output: "Found 20 hotspots"},
		}
		if hasResultCountMention("the codebase has some functions", results) {
			t.Error("want false: response doesn't mention counts")
		}
	})
}

// CRS-22 AC-4: Interaction tests.

// TestCRS22_ScopeRelaxSynthesisInteraction verifies that the quality
// scorer properly handles scope relaxation results (two tool outputs:
// scoped empty + global populated).
func TestCRS22_ScopeRelaxSynthesisInteraction(t *testing.T) {
	// Simulates scope relaxation: first result is scoped (empty),
	// second result is global (populated).
	toolResults := []agent.ToolResult{
		{
			// Scoped result (empty) — added to context before relaxation
			Success: true,
			Output:  "No hotspots found in package 'materials'. 0 results after filtering.",
		},
		{
			// Global result (populated) — added after relaxation
			Success: true,
			Output:  "Found 20 hotspots globally:\n\n1. `ParseConfig` (complexity: 45)\n2. `HandleRequest` (complexity: 38)\n",
		},
	}

	t.Run("good response mentions both scope and symbols", func(t *testing.T) {
		response := "No hotspots were found in the materials package. " +
			"However, across the codebase globally, ParseConfig is the most complex function (45), " +
			"followed by HandleRequest (38). There are 20 hotspots in total."

		result := scoreSynthesisQuality(response, toolResults)
		if result.Score < 0.7 {
			t.Errorf("Score = %.2f, want >= 0.7 for good scope-aware response", result.Score)
		}
		if !result.ScopeRelevant {
			t.Error("ScopeRelevant should be true (tool output mentions 'globally')")
		}
	})

	t.Run("bad response ignores scoped empty result", func(t *testing.T) {
		response := "ParseConfig has a complexity of 45. HandleRequest has 38."
		result := scoreSynthesisQuality(response, toolResults)
		// Should be penalized for not mentioning scope/global/package
		if result.Score > 0.8 {
			t.Errorf("Score = %.2f, want <= 0.8 (missing scope awareness)", result.Score)
		}
	})
}

// TestCRS22_MispredictReextractInteraction verifies quality scoring
// works correctly when tool results come from re-extraction path.
func TestCRS22_MispredictReextractInteraction(t *testing.T) {
	// After mispredict recovery, the tool result should contain the
	// actual tool's output (not the mispredicted tool's).
	toolResults := []agent.ToolResult{
		{
			Success: true,
			Tool:    "find_hotspots",
			Output:  "Found 15 hotspots:\n\n1. `Renderer` (complexity: 52)\n2. `SceneManager` (complexity: 41)\n3. `MeshBuilder` (complexity: 39)\n",
		},
	}

	response := "The codebase has 15 hotspots. The Renderer class is the most complex at 52, " +
		"followed by SceneManager (41) and MeshBuilder (39)."

	result := scoreSynthesisQuality(response, toolResults)
	if result.Score < 0.8 {
		t.Errorf("Score = %.2f, want >= 0.8 for complete response", result.Score)
	}
	if result.SymbolsFound < 3 {
		t.Errorf("SymbolsFound = %d, want >= 3 (Renderer, SceneManager, MeshBuilder)", result.SymbolsFound)
	}
}

func TestComputeQualityScore(t *testing.T) {
	t.Run("all symbols found with count", func(t *testing.T) {
		r := SynthesisQualityResult{
			SymbolsExpected: 5,
			SymbolsFound:    5,
			HasResultCount:  true,
			ScopeRelevant:   false,
		}
		score, _ := computeQualityScore(r)
		if score != 1.0 {
			t.Errorf("Score = %.2f, want 1.0", score)
		}
	})

	t.Run("no symbols found no count", func(t *testing.T) {
		r := SynthesisQualityResult{
			SymbolsExpected: 5,
			SymbolsFound:    0,
			HasResultCount:  false,
			ScopeRelevant:   false,
		}
		score, _ := computeQualityScore(r)
		if score != 0.0 {
			t.Errorf("Score = %.2f, want 0.0", score)
		}
	})

	t.Run("scope relevant and mentioned", func(t *testing.T) {
		r := SynthesisQualityResult{
			SymbolsExpected: 5,
			SymbolsFound:    5,
			HasResultCount:  true,
			ScopeRelevant:   true,
			MentionsScope:   true,
		}
		score, _ := computeQualityScore(r)
		if score != 1.0 {
			t.Errorf("Score = %.2f, want 1.0", score)
		}
	})

	t.Run("scope relevant but missing", func(t *testing.T) {
		r := SynthesisQualityResult{
			SymbolsExpected: 5,
			SymbolsFound:    5,
			HasResultCount:  true,
			ScopeRelevant:   true,
			MentionsScope:   false,
		}
		score, _ := computeQualityScore(r)
		if score >= 1.0 {
			t.Errorf("Score = %.2f, want < 1.0 (scope missing)", score)
		}
		if score < 0.7 {
			t.Errorf("Score = %.2f, want >= 0.7 (symbols+count good)", score)
		}
	})
}
