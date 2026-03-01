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
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/routing"
)

// TestGR38_DuplicateToolCallPrevention tests that hard forcing is skipped
// when the tool has already been called in the session.
func TestGR38_DuplicateToolCallPrevention(t *testing.T) {
	t.Run("tool already called prevents hard forcing", func(t *testing.T) {
		// Create a session with existing tool history
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Simulate a previous tool call by adding to trace
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_call",
			Tool:      "find_entry_points",
		})

		// Build tool history (this is what the fix checks)
		history := buildToolHistoryFromSession(session)

		// Check that the tool appears in history
		found := false
		for _, entry := range history {
			if entry.Tool == "find_entry_points" {
				found = true
				break
			}
		}

		if !found {
			t.Error("find_entry_points should be in tool history after being recorded")
		}
	})

	t.Run("empty history allows hard forcing", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// No tool calls recorded
		history := buildToolHistoryFromSession(session)

		if len(history) != 0 {
			t.Errorf("Expected empty history, got %d entries", len(history))
		}
	})

	t.Run("different tool allows hard forcing", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Record a call to a different tool
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_call",
			Tool:      "find_symbol",
		})

		history := buildToolHistoryFromSession(session)

		// Check that find_entry_points is NOT in history
		found := false
		for _, entry := range history {
			if entry.Tool == "find_entry_points" {
				found = true
				break
			}
		}

		if found {
			t.Error("find_entry_points should NOT be in history when only find_symbol was called")
		}
	})

	t.Run("forced calls are also counted", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Record a forced tool call (CB-31d format)
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_call_forced",
			Tool:      "find_entry_points",
		})

		history := buildToolHistoryFromSession(session)

		// Both tool_call and tool_call_forced should be counted
		found := false
		for _, entry := range history {
			if entry.Tool == "find_entry_points" {
				found = true
				break
			}
		}

		if !found {
			t.Error("tool_call_forced should be counted in tool history (CB-31d fix)")
		}
	})
}

// TestGR38_ToolHistoryLimiting tests that tool history is limited to prevent memory growth.
func TestGR38_ToolHistoryLimiting(t *testing.T) {
	session, err := agent.NewSession("/test/project", nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Record many tool calls
	for i := 0; i < maxToolHistoryEntries+10; i++ {
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_call",
			Tool:      "test_tool",
		})
	}

	history := buildToolHistoryFromSession(session)

	if len(history) > maxToolHistoryEntries {
		t.Errorf("History should be limited to %d, got %d", maxToolHistoryEntries, len(history))
	}
}

// =============================================================================
// GR-38 Issue 17: Semantic Tool Call Tracking Tests
// =============================================================================

// TestGR38_SemanticToolHistoryFromSession tests that semantic history is built correctly.
func TestGR38_SemanticToolHistoryFromSession(t *testing.T) {
	t.Run("empty session returns empty history", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		history := buildSemanticToolHistoryFromSession(session)
		if history.Len() != 0 {
			t.Errorf("Expected empty history, got %d entries", history.Len())
		}
	})

	t.Run("nil session returns empty history", func(t *testing.T) {
		history := buildSemanticToolHistoryFromSession(nil)
		if history.Len() != 0 {
			t.Errorf("Expected empty history for nil session, got %d entries", history.Len())
		}
	})

	t.Run("tool_routing steps are captured with query", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Record a tool_routing step with query in metadata
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_routing",
			Target:    "Grep",
			Metadata: map[string]string{
				"query":      "Where is parseConfig defined?",
				"confidence": "0.95",
			},
		})

		history := buildSemanticToolHistoryFromSession(session)
		if history.Len() != 1 {
			t.Errorf("Expected 1 entry, got %d", history.Len())
		}

		// Verify the query was captured by checking similarity
		status, similarity, _ := routing.CheckSemanticStatus(
			history,
			"Grep",
			"Where is parseConfig defined?",
		)
		if status != "blocked" || similarity != 1.0 {
			t.Errorf("Expected exact duplicate to be blocked with similarity 1.0, got status=%s similarity=%.2f",
				status, similarity)
		}
	})

	t.Run("tool_call steps are NOT captured (only tool_routing)", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Record a tool_call step (not tool_routing)
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_call",
			Tool:      "Grep",
		})

		history := buildSemanticToolHistoryFromSession(session)
		if history.Len() != 0 {
			t.Errorf("Expected 0 entries for tool_call (not tool_routing), got %d", history.Len())
		}
	})
}

// TestGR38_SemanticSameToolDifferentParams tests that same tool with different params is allowed.
func TestGR38_SemanticSameToolDifferentParams(t *testing.T) {
	session, err := agent.NewSession("/test/project", nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Record a Grep call for "parseConfig"
	session.RecordTraceStep(crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "tool_routing",
		Target:    "Grep",
		Metadata: map[string]string{
			"query": "Where is parseConfig defined?",
		},
	})

	history := buildSemanticToolHistoryFromSession(session)

	// A completely different Grep query should be ALLOWED
	status, similarity, _ := routing.CheckSemanticStatus(
		history,
		"Grep",
		"How does the HTTP server handle requests?",
	)

	if status == "blocked" {
		t.Errorf("Different query should NOT be blocked: status=%s, similarity=%.2f", status, similarity)
	}

	// Verify similarity is low
	if similarity > routing.SemanticPenaltyThreshold {
		t.Errorf("Similarity should be low for completely different queries, got %.2f", similarity)
	}
}

// TestGR38_SemanticSimilarQueriesBlocked tests that semantically similar queries are blocked.
func TestGR38_SemanticSimilarQueriesBlocked(t *testing.T) {
	session, err := agent.NewSession("/test/project", nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Record a Grep call for "parseConfig"
	session.RecordTraceStep(crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "tool_routing",
		Target:    "Grep",
		Metadata: map[string]string{
			"query": "Find parseConfig function definition",
		},
	})

	history := buildSemanticToolHistoryFromSession(session)

	// A semantically similar query should be BLOCKED
	// "parse_config" and "parseConfig" normalize to the same terms
	status, similarity, _ := routing.CheckSemanticStatus(
		history,
		"Grep",
		"Find parseConfig function definition", // Exact duplicate
	)

	if status != "blocked" {
		t.Errorf("Exact duplicate query should be blocked: status=%s, similarity=%.2f", status, similarity)
	}

	// Test case-insensitive duplicate
	status2, similarity2, _ := routing.CheckSemanticStatus(
		history,
		"Grep",
		"find parseconfig function definition", // Case different
	)

	if status2 != "blocked" {
		t.Errorf("Case-insensitive duplicate should be blocked: status=%s, similarity=%.2f", status2, similarity2)
	}
}

// TestGR38_SemanticPenalizedButAllowed tests that similar but distinct queries are penalized.
func TestGR38_SemanticPenalizedButAllowed(t *testing.T) {
	session, err := agent.NewSession("/test/project", nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Record a Grep call for parsing config
	session.RecordTraceStep(crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "tool_routing",
		Target:    "Grep",
		Metadata: map[string]string{
			"query": "Find parse config function",
		},
	})

	history := buildSemanticToolHistoryFromSession(session)

	// A related but distinct query should be penalized but allowed
	// Shares some terms ("parse", "config") but adds new terms
	status, similarity, _ := routing.CheckSemanticStatus(
		history,
		"Grep",
		"Find parse config validation logic",
	)

	// Should be penalized (similarity between 0.3 and 0.8) but not blocked
	if status == "blocked" {
		t.Errorf("Related but distinct query should NOT be blocked: status=%s, similarity=%.2f", status, similarity)
	}

	if status == "allowed" && similarity >= routing.SemanticPenaltyThreshold {
		t.Errorf("Similar query should be penalized, not allowed: status=%s, similarity=%.2f", status, similarity)
	}
}

// TestGR38_SemanticDifferentToolSameQuery tests that different tools with same query are allowed.
func TestGR38_SemanticDifferentToolSameQuery(t *testing.T) {
	session, err := agent.NewSession("/test/project", nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Record a Grep call
	session.RecordTraceStep(crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "tool_routing",
		Target:    "Grep",
		Metadata: map[string]string{
			"query": "Where is parseConfig defined?",
		},
	})

	history := buildSemanticToolHistoryFromSession(session)

	// Same query but different tool should be ALLOWED
	status, similarity, _ := routing.CheckSemanticStatus(
		history,
		"find_symbol", // Different tool
		"Where is parseConfig defined?",
	)

	if status != "allowed" || similarity != 0.0 {
		t.Errorf("Different tool should be allowed with zero similarity: status=%s, similarity=%.2f",
			status, similarity)
	}
}

// =============================================================================
// GR-44: Circuit Breaker Recovery Tests
// =============================================================================

// TestGR44_CircuitBreakerActive tests the circuit breaker flag on sessions.
func TestGR44_CircuitBreakerActive(t *testing.T) {
	t.Run("default circuit breaker is inactive", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if session.IsCircuitBreakerActive() {
			t.Error("Expected circuit breaker to be inactive by default")
		}
	})

	t.Run("circuit breaker can be activated", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		session.SetCircuitBreakerActive(true)

		if !session.IsCircuitBreakerActive() {
			t.Error("Expected circuit breaker to be active after SetCircuitBreakerActive(true)")
		}
	})

	t.Run("circuit breaker can be deactivated", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		session.SetCircuitBreakerActive(true)
		session.SetCircuitBreakerActive(false)

		if session.IsCircuitBreakerActive() {
			t.Error("Expected circuit breaker to be inactive after SetCircuitBreakerActive(false)")
		}
	})
}

// TestGR44_CircuitBreakerSkipsToolRequirement documents the expected behavior:
// When circuit breaker fires, the execute phase should NOT require tool usage.
func TestGR44_CircuitBreakerSkipsToolRequirement(t *testing.T) {
	t.Run("documentation of expected behavior", func(t *testing.T) {
		// This test documents the expected behavior after GR-44 fix:
		//
		// 1. Circuit breaker fires in tryToolRouterSelection
		// 2. SetCircuitBreakerActive(true) is called
		// 3. Response validation sees no tool calls
		// 4. IsCircuitBreakerActive() returns true
		// 5. retryWithStrongerToolChoice is SKIPPED
		// 6. Response proceeds to completion
		//
		// The actual integration test would require mocking the LLM.
		// This test just verifies the flag mechanism works.

		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Simulate circuit breaker firing
		session.SetCircuitBreakerActive(true)

		// Verify the flag is set (this is what execute phase checks)
		if !session.IsCircuitBreakerActive() {
			t.Error("IsCircuitBreakerActive() should return true after circuit breaker fires")
		}
	})
}

// TestGR59Rev4_GraphToolSubstantiveResults tests the session-level flag that
// prevents LLM Grep/Glob loops after forced graph tools return authoritative results.
func TestGR59Rev4_GraphToolSubstantiveResults(t *testing.T) {
	t.Run("default is false", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if session.GraphToolHadSubstantiveResults() {
			t.Error("Expected GraphToolHadSubstantiveResults to be false by default")
		}
	})

	t.Run("can be set to true", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		session.SetGraphToolHadSubstantiveResults(true)

		if !session.GraphToolHadSubstantiveResults() {
			t.Error("Expected GraphToolHadSubstantiveResults to be true after setting")
		}
	})

	t.Run("can be reset to false", func(t *testing.T) {
		session, err := agent.NewSession("/test/project", nil)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		session.SetGraphToolHadSubstantiveResults(true)
		session.SetGraphToolHadSubstantiveResults(false)

		if session.GraphToolHadSubstantiveResults() {
			t.Error("Expected GraphToolHadSubstantiveResults to be false after reset")
		}
	})
}

// TestGR59Rev4_HasSubstantiveGraphResult_NoImplementations verifies that
// find_implementations zero-result output is correctly detected as non-substantive.
func TestGR59Rev4_HasSubstantiveGraphResult_NoImplementations(t *testing.T) {
	t.Run("zero implementations symbol not found", func(t *testing.T) {
		output := "## GRAPH RESULT: Symbol 'Router' not found\n\nNo interface or class named 'Router' exists in this codebase.\nThe graph has been fully indexed - this is the definitive answer.\n\n**Do NOT use Grep to search further** - the graph already analyzed all source files."
		if hasSubstantiveGraphResult(output) {
			t.Error("Expected false for zero-result find_implementations output (symbol not found)")
		}
	})

	t.Run("zero implementations type exists", func(t *testing.T) {
		output := "## GRAPH RESULT: Implementations of 'Router' not found\n\nThe type 'Router' exists but has no implementing or extending types.\nThe graph has been fully indexed - this is the definitive answer.\n\n**Do NOT use Grep to search further** - the graph already analyzed all source files."
		if hasSubstantiveGraphResult(output) {
			t.Error("Expected false for zero-result find_implementations output (type exists, no implementations)")
		}
	})

	t.Run("positive implementations are substantive", func(t *testing.T) {
		output := "Found 3 implementations/subclasses of 'Iterator':\n\nInterface: y/iterator.go:74:Iterator\n  - Iterator (struct) in table/iterator.go:165\n  - UniIterator (struct) in skl/skl.go:461\n  - SimpleIterator (struct) in table/merge_iterator_test.go:17\n\n---\nThe graph has been fully indexed — these results are exhaustive."
		if !hasSubstantiveGraphResult(output) {
			t.Error("Expected true for positive find_implementations results")
		}
	})

	t.Run("zero callers still detected", func(t *testing.T) {
		output := "## GRAPH RESULT: Callers of 'doSomething' not found\n\nThe function 'doSomething' is not called anywhere (dead code or entry point)."
		if hasSubstantiveGraphResult(output) {
			t.Error("Expected false for zero callers")
		}
	})

	t.Run("zero callees still detected", func(t *testing.T) {
		output := "## GRAPH RESULT: Callees of 'leafFunc' not found\n\nThe function 'leafFunc' does not call any other functions."
		if hasSubstantiveGraphResult(output) {
			t.Error("Expected false for zero callees")
		}
	})
}
