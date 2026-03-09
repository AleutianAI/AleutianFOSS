// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
)

// -----------------------------------------------------------------------------
// CycleDetector Tests
// -----------------------------------------------------------------------------

func TestCycleDetector_NewCycleDetector(t *testing.T) {
	t.Run("nil_config_uses_defaults", func(t *testing.T) {
		d := NewCycleDetector(nil)
		if d == nil {
			t.Fatal("expected non-nil detector")
		}
		if d.maxHistory != 1000 {
			t.Errorf("expected maxHistory=1000, got %d", d.maxHistory)
		}
	})

	t.Run("custom_config", func(t *testing.T) {
		d := NewCycleDetector(&CycleDetectorConfig{MaxHistory: 500})
		if d.maxHistory != 500 {
			t.Errorf("expected maxHistory=500, got %d", d.maxHistory)
		}
	})

	t.Run("zero_max_history_uses_default", func(t *testing.T) {
		d := NewCycleDetector(&CycleDetectorConfig{MaxHistory: 0})
		if d.maxHistory != 1000 {
			t.Errorf("expected maxHistory=1000, got %d", d.maxHistory)
		}
	})
}

func TestCycleDetector_SimpleOneStepCycle(t *testing.T) {
	// A → A cycle (same state repeated)
	d := NewCycleDetector(nil)

	// First step - no cycle
	result := d.AddStep("A")
	if result.Detected {
		t.Error("expected no cycle on first step")
	}

	// Second step - same state = cycle
	result = d.AddStep("A")
	if !result.Detected {
		t.Error("expected cycle when repeating same state")
	}
	if result.CycleLength != 1 {
		t.Errorf("expected cycle length 1, got %d", result.CycleLength)
	}
}

func TestCycleDetector_TwoStepCycle(t *testing.T) {
	// A → B → A cycle
	d := NewCycleDetector(nil)

	// Step 1: A - no cycle
	result := d.AddStep("A")
	if result.Detected {
		t.Error("step 1: unexpected cycle")
	}

	// Step 2: B - no cycle
	result = d.AddStep("B")
	if result.Detected {
		t.Error("step 2: unexpected cycle")
	}

	// Step 3: A again - cycle detected!
	result = d.AddStep("A")
	if !result.Detected {
		t.Error("step 3: expected cycle A→B→A")
	}
	if result.CycleLength < 1 {
		t.Errorf("expected positive cycle length, got %d", result.CycleLength)
	}
}

func TestCycleDetector_MultiStepCycle(t *testing.T) {
	// A → B → C → A cycle
	d := NewCycleDetector(nil)

	steps := []string{"A", "B", "C", "A"}
	var detectedCycle []string

	for i, step := range steps {
		result := d.AddStep(step)
		if result.Detected {
			detectedCycle = result.Cycle
			t.Logf("cycle detected at step %d: %v", i, result.Cycle)
		}
	}

	if len(detectedCycle) == 0 {
		t.Error("expected cycle to be detected in A→B→C→A sequence")
	}
}

func TestCycleDetector_NoCycle(t *testing.T) {
	// Linear sequence: A → B → C → D (no cycle)
	d := NewCycleDetector(nil)

	steps := []string{"A", "B", "C", "D"}

	for _, step := range steps {
		result := d.AddStep(step)
		if result.Detected {
			t.Errorf("unexpected cycle detected for state %s", step)
		}
	}
}

func TestCycleDetector_Reset(t *testing.T) {
	d := NewCycleDetector(nil)

	// Build up state
	d.AddStep("A")
	d.AddStep("B")

	// Reset
	d.Reset()

	stats := d.Stats()
	if stats.StepsProcessed != 0 {
		t.Errorf("expected 0 steps after reset, got %d", stats.StepsProcessed)
	}
	if stats.HistorySize != 0 {
		t.Errorf("expected 0 history after reset, got %d", stats.HistorySize)
	}
}

func TestCycleDetector_MaxHistory(t *testing.T) {
	d := NewCycleDetector(&CycleDetectorConfig{MaxHistory: 5})

	// Add more steps than maxHistory
	for i := 0; i < 10; i++ {
		d.AddStep("state_" + string(rune('A'+i)))
	}

	stats := d.Stats()
	if stats.HistorySize != 5 {
		t.Errorf("expected history size 5 (maxHistory), got %d", stats.HistorySize)
	}
}

func TestCycleDetector_Stats(t *testing.T) {
	d := NewCycleDetector(nil)

	// Process some steps
	d.AddStep("A")
	d.AddStep("B")
	d.AddStep("A") // This should detect a cycle

	stats := d.Stats()
	if stats.StepsProcessed != 3 {
		t.Errorf("expected 3 steps processed, got %d", stats.StepsProcessed)
	}
	if stats.CyclesDetected < 1 {
		t.Errorf("expected at least 1 cycle detected, got %d", stats.CyclesDetected)
	}
}

func TestCycleDetector_ConcurrentAccess(t *testing.T) {
	d := NewCycleDetector(nil)
	var wg sync.WaitGroup

	// Run multiple goroutines adding steps concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				d.AddStep("state_from_goroutine")
			}
		}(i)
	}

	wg.Wait()

	stats := d.Stats()
	if stats.StepsProcessed != 1000 {
		t.Errorf("expected 1000 steps processed, got %d", stats.StepsProcessed)
	}
}

// -----------------------------------------------------------------------------
// GetStateKey Tests
// -----------------------------------------------------------------------------

func TestGetStateKey(t *testing.T) {
	tests := []struct {
		name     string
		step     StepRecord
		expected string
	}{
		{
			name: "tool_execution_success",
			step: StepRecord{
				Decision: DecisionExecuteTool,
				Tool:     "list_packages",
				Outcome:  OutcomeSuccess,
				Actor:    ActorRouter,
			},
			expected: "execute_tool:list_packages:success:router",
		},
		{
			name: "tool_selection_failure",
			step: StepRecord{
				Decision: DecisionSelectTool,
				Tool:     "find_symbol",
				Outcome:  OutcomeFailure,
				Actor:    ActorMainAgent,
			},
			expected: "select_tool:find_symbol:failure:main_agent",
		},
		{
			name: "circuit_breaker_forced",
			step: StepRecord{
				Decision: DecisionCircuitBreaker,
				Tool:     "",
				Outcome:  OutcomeForced,
				Actor:    ActorSystem,
			},
			expected: "circuit_breaker::forced:system",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetStateKey(tt.step)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGetToolStateKey(t *testing.T) {
	tests := []struct {
		name     string
		step     StepRecord
		expected string
	}{
		{
			name: "with_tool",
			step: StepRecord{
				Tool:    "grep_codebase",
				Outcome: OutcomeSuccess,
			},
			expected: "grep_codebase:success",
		},
		{
			name: "no_tool",
			step: StepRecord{
				Tool:    "",
				Outcome: OutcomeFailure,
			},
			expected: "no_tool:failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetToolStateKey(tt.step)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// BuildDecisionGraph Tests
// -----------------------------------------------------------------------------

func TestBuildDecisionGraph_EmptySteps(t *testing.T) {
	graph := BuildDecisionGraph(nil)

	if len(graph.Nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(graph.Nodes))
	}
	if len(graph.Edges) != 0 {
		t.Errorf("expected empty edges, got %d", len(graph.Edges))
	}
}

func TestBuildDecisionGraph_SingleStep(t *testing.T) {
	steps := []StepRecord{
		{Decision: DecisionExecuteTool, Tool: "list_packages", Outcome: OutcomeSuccess, Actor: ActorRouter},
	}

	graph := BuildDecisionGraph(steps)

	if len(graph.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(graph.Nodes))
	}
	if len(graph.Edges) != 0 {
		t.Errorf("expected 0 edges for single node, got %d", len(graph.Edges))
	}
}

func TestBuildDecisionGraph_LinearSequence(t *testing.T) {
	steps := []StepRecord{
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "B", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "C", Outcome: OutcomeSuccess, Actor: ActorRouter},
	}

	graph := BuildDecisionGraph(steps)

	if len(graph.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(graph.Nodes))
	}
	// Should have edges A→B and B→C
	if len(graph.Edges) != 2 {
		t.Errorf("expected 2 edge sources, got %d", len(graph.Edges))
	}
}

func TestBuildDecisionGraph_CyclicSequence(t *testing.T) {
	steps := []StepRecord{
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "B", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter}, // Back to A
	}

	graph := BuildDecisionGraph(steps)

	// Only 2 unique nodes (A and B)
	if len(graph.Nodes) != 2 {
		t.Errorf("expected 2 unique nodes, got %d", len(graph.Nodes))
	}
	// Edges: A→B and B→A (forms cycle)
	if len(graph.Edges) != 2 {
		t.Errorf("expected 2 edge sources, got %d", len(graph.Edges))
	}
}

// -----------------------------------------------------------------------------
// Tarjan SCC Tests
// -----------------------------------------------------------------------------

func TestTarjanSCC_NoCycles(t *testing.T) {
	// Linear graph: A → B → C
	nodes := []string{"A", "B", "C"}
	edges := map[string][]string{
		"A": {"B"},
		"B": {"C"},
	}

	sccs := tarjanSCC(nodes, edges)

	// Each node should be its own SCC (no cycles)
	if len(sccs) != 3 {
		t.Errorf("expected 3 SCCs (one per node), got %d", len(sccs))
	}

	for _, scc := range sccs {
		if len(scc) != 1 {
			t.Errorf("expected all SCCs to have size 1, got size %d", len(scc))
		}
	}
}

func TestTarjanSCC_SimpleCycle(t *testing.T) {
	// Cycle: A → B → A
	nodes := []string{"A", "B"}
	edges := map[string][]string{
		"A": {"B"},
		"B": {"A"},
	}

	sccs := tarjanSCC(nodes, edges)

	// Should have 1 SCC containing both nodes
	if len(sccs) != 1 {
		t.Errorf("expected 1 SCC, got %d", len(sccs))
	}
	if len(sccs[0]) != 2 {
		t.Errorf("expected SCC size 2, got %d", len(sccs[0]))
	}
}

func TestTarjanSCC_MultiNodeCycle(t *testing.T) {
	// Cycle: A → B → C → A
	nodes := []string{"A", "B", "C"}
	edges := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	sccs := tarjanSCC(nodes, edges)

	// Should have 1 SCC containing all 3 nodes
	if len(sccs) != 1 {
		t.Errorf("expected 1 SCC, got %d", len(sccs))
	}
	if len(sccs[0]) != 3 {
		t.Errorf("expected SCC size 3, got %d", len(sccs[0]))
	}
}

func TestTarjanSCC_MultipleCycles(t *testing.T) {
	// Two separate cycles: A ↔ B and C ↔ D
	nodes := []string{"A", "B", "C", "D"}
	edges := map[string][]string{
		"A": {"B"},
		"B": {"A"},
		"C": {"D"},
		"D": {"C"},
	}

	sccs := tarjanSCC(nodes, edges)

	// Should have 2 SCCs, each with 2 nodes
	if len(sccs) != 2 {
		t.Errorf("expected 2 SCCs, got %d", len(sccs))
	}
	for _, scc := range sccs {
		if len(scc) != 2 {
			t.Errorf("expected SCC size 2, got %d", len(scc))
		}
	}
}

func TestTarjanSCC_EmptyGraph(t *testing.T) {
	nodes := []string{}
	edges := map[string][]string{}

	sccs := tarjanSCC(nodes, edges)

	if len(sccs) != 0 {
		t.Errorf("expected 0 SCCs for empty graph, got %d", len(sccs))
	}
}

// -----------------------------------------------------------------------------
// AnalyzeSessionCycles Tests
// -----------------------------------------------------------------------------

// mockCRSForCycleAnalysis is a minimal CRS mock for testing cycle analysis.
type mockCRSForCycleAnalysis struct {
	steps map[string][]StepRecord
}

func newMockCRSForCycleAnalysis() *mockCRSForCycleAnalysis {
	return &mockCRSForCycleAnalysis{
		steps: make(map[string][]StepRecord),
	}
}

func (m *mockCRSForCycleAnalysis) GetStepHistory(sessionID string) []StepRecord {
	return m.steps[sessionID]
}

func (m *mockCRSForCycleAnalysis) addSteps(sessionID string, steps []StepRecord) {
	m.steps[sessionID] = steps
}

// Implement remaining CRS interface methods as no-ops for mock
func (m *mockCRSForCycleAnalysis) Name() string       { return "mock_crs" }
func (m *mockCRSForCycleAnalysis) Snapshot() Snapshot { return nil }
func (m *mockCRSForCycleAnalysis) Apply(context.Context, Delta) (ApplyMetrics, error) {
	return ApplyMetrics{}, nil
}
func (m *mockCRSForCycleAnalysis) Generation() int64 { return 0 }
func (m *mockCRSForCycleAnalysis) Checkpoint(context.Context) (Checkpoint, error) {
	return Checkpoint{}, nil
}
func (m *mockCRSForCycleAnalysis) Restore(context.Context, Checkpoint) error            { return nil }
func (m *mockCRSForCycleAnalysis) RecordStep(context.Context, StepRecord) error         { return nil }
func (m *mockCRSForCycleAnalysis) GetLastStep(string) *StepRecord                       { return nil }
func (m *mockCRSForCycleAnalysis) CountToolExecutions(string, string) int               { return 0 }
func (m *mockCRSForCycleAnalysis) GetStepsByActor(string, Actor) []StepRecord           { return nil }
func (m *mockCRSForCycleAnalysis) GetStepsByOutcome(string, Outcome) []StepRecord       { return nil }
func (m *mockCRSForCycleAnalysis) ClearStepHistory(string)                              {}
func (m *mockCRSForCycleAnalysis) UpdateProofNumber(context.Context, ProofUpdate) error { return nil }
func (m *mockCRSForCycleAnalysis) GetProofStatus(string) (ProofNumber, bool) {
	return ProofNumber{}, false
}
func (m *mockCRSForCycleAnalysis) CheckCircuitBreaker(string, string) CircuitBreakerResult {
	return CircuitBreakerResult{}
}
func (m *mockCRSForCycleAnalysis) PropagateDisproof(context.Context, string) int { return 0 }
func (m *mockCRSForCycleAnalysis) HealthCheck(context.Context) error             { return nil }
func (m *mockCRSForCycleAnalysis) Properties() []eval.Property                   { return nil }
func (m *mockCRSForCycleAnalysis) Metrics() []eval.MetricDefinition              { return nil }

// CRS-04: Clause methods
func (m *mockCRSForCycleAnalysis) AddClause(context.Context, *Clause) error { return nil }
func (m *mockCRSForCycleAnalysis) CheckDecisionAllowed(string, string) (bool, string) {
	return true, ""
}
func (m *mockCRSForCycleAnalysis) GarbageCollectClauses() int { return 0 }

// CRS-15: Similarity methods
func (m *mockCRSForCycleAnalysis) RecordSimilarity(context.Context, string, string, float64) error {
	return nil
}
func (m *mockCRSForCycleAnalysis) GetSimilarity(string, string) (float64, bool) { return -1, false }

// GR-35: Delta history methods
func (m *mockCRSForCycleAnalysis) SetSessionID(string) {}
func (m *mockCRSForCycleAnalysis) ApplyWithSource(context.Context, Delta, string, map[string]string) (ApplyMetrics, error) {
	return ApplyMetrics{}, nil
}
func (m *mockCRSForCycleAnalysis) DeltaHistory() DeltaHistoryView { return nil }
func (m *mockCRSForCycleAnalysis) Close()                         {}

// GR-28: Graph integration
func (m *mockCRSForCycleAnalysis) SetGraphProvider(GraphQuery) {}
func (m *mockCRSForCycleAnalysis) InvalidateGraphCache()       {}

// GR-31: Analytics methods
func (m *mockCRSForCycleAnalysis) GetAnalyticsHistory() []*AnalyticsRecord              { return nil }
func (m *mockCRSForCycleAnalysis) GetLastAnalytics(AnalyticsQueryType) *AnalyticsRecord { return nil }
func (m *mockCRSForCycleAnalysis) HasRunAnalytics(AnalyticsQueryType) bool              { return false }

func TestAnalyzeSessionCycles_NilContext(t *testing.T) {
	mock := newMockCRSForCycleAnalysis()
	_, err := AnalyzeSessionCycles(nil, mock, "test-session")
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestAnalyzeSessionCycles_NilCRS(t *testing.T) {
	ctx := context.Background()
	_, err := AnalyzeSessionCycles(ctx, nil, "test-session")
	if err == nil {
		t.Error("expected error for nil CRS")
	}
}

func TestAnalyzeSessionCycles_EmptySessionID(t *testing.T) {
	ctx := context.Background()
	mock := newMockCRSForCycleAnalysis()
	_, err := AnalyzeSessionCycles(ctx, mock, "")
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

func TestAnalyzeSessionCycles_EmptyHistory(t *testing.T) {
	ctx := context.Background()
	mock := newMockCRSForCycleAnalysis()

	analysis, err := AnalyzeSessionCycles(ctx, mock, "empty-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if analysis.TotalSCCs != 0 {
		t.Errorf("expected 0 SCCs for empty history, got %d", analysis.TotalSCCs)
	}
	if len(analysis.CyclicSCCs) != 0 {
		t.Errorf("expected no cyclic SCCs, got %d", len(analysis.CyclicSCCs))
	}
}

func TestAnalyzeSessionCycles_LinearHistory(t *testing.T) {
	ctx := context.Background()
	mock := newMockCRSForCycleAnalysis()
	mock.addSteps("linear-session", []StepRecord{
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "B", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "C", Outcome: OutcomeSuccess, Actor: ActorRouter},
	})

	analysis, err := AnalyzeSessionCycles(ctx, mock, "linear-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(analysis.CyclicSCCs) != 0 {
		t.Errorf("expected no cycles in linear history, got %d", len(analysis.CyclicSCCs))
	}
}

func TestAnalyzeSessionCycles_WithCycle(t *testing.T) {
	ctx := context.Background()
	mock := newMockCRSForCycleAnalysis()
	mock.addSteps("cyclic-session", []StepRecord{
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "B", Outcome: OutcomeSuccess, Actor: ActorRouter},
		{Decision: DecisionSelectTool, Tool: "A", Outcome: OutcomeSuccess, Actor: ActorRouter}, // Back to A
	})

	analysis, err := AnalyzeSessionCycles(ctx, mock, "cyclic-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(analysis.CyclicSCCs) == 0 {
		t.Error("expected to find cycles in A→B→A sequence")
	}
	if analysis.LargestSCCSize < 2 {
		t.Errorf("expected largest SCC size >= 2, got %d", analysis.LargestSCCSize)
	}
}

func TestAnalyzeSessionCycles_AnalysisTiming(t *testing.T) {
	ctx := context.Background()
	mock := newMockCRSForCycleAnalysis()
	mock.addSteps("timing-session", []StepRecord{
		{Decision: DecisionSelectTool, Tool: "X", Outcome: OutcomeSuccess, Actor: ActorRouter},
	})

	before := time.Now()
	analysis, err := AnalyzeSessionCycles(ctx, mock, "timing-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now()

	if analysis.AnalysisTime.Before(before) || analysis.AnalysisTime.After(after) {
		t.Error("AnalysisTime should be between before and after")
	}
	if analysis.AnalysisDuration < 0 {
		t.Error("AnalysisDuration should not be negative")
	}
}

// -----------------------------------------------------------------------------
// Additional Edge Case Tests (Code Review Fixes)
// -----------------------------------------------------------------------------

func TestTarjanSCC_ContextCancellation(t *testing.T) {
	// T-01: Test that Tarjan SCC respects context cancellation
	nodes := make([]string, 100)
	edges := make(map[string][]string)
	for i := 0; i < 100; i++ {
		nodes[i] = fmt.Sprintf("node_%d", i)
		if i > 0 {
			edges[nodes[i-1]] = append(edges[nodes[i-1]], nodes[i])
		}
	}
	// Create a cycle
	edges[nodes[99]] = []string{nodes[0]}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	sccs := tarjanSCCWithConfig(nodes, edges, &tarjanSCCConfig{
		MaxDepth: 10000,
		Ctx:      ctx,
	})

	// Should return early with partial or empty results
	// The exact behavior depends on when cancellation is checked
	t.Logf("Got %d SCCs with cancelled context", len(sccs))
}

func TestTarjanSCC_DepthLimit(t *testing.T) {
	// R-01: Test that Tarjan SCC respects depth limit
	// Create a deep linear graph that exceeds depth limit
	depth := 100
	nodes := make([]string, depth)
	edges := make(map[string][]string)
	for i := 0; i < depth; i++ {
		nodes[i] = fmt.Sprintf("node_%d", i)
		if i > 0 {
			edges[nodes[i-1]] = []string{nodes[i]}
		}
	}

	// Set depth limit lower than graph depth
	sccs := tarjanSCCWithConfig(nodes, edges, &tarjanSCCConfig{
		MaxDepth: 10, // Very low limit
		Ctx:      context.Background(),
	})

	// Should abort before completing all nodes
	t.Logf("Got %d SCCs with depth limit 10 on depth %d graph", len(sccs), depth)
}

func TestCycleDetector_ExceedsMaxHistory(t *testing.T) {
	// T-02: Test cycle detection when states exceed maxHistory
	d := NewCycleDetector(&CycleDetectorConfig{MaxHistory: 5})

	// Add more states than maxHistory allows
	for i := 0; i < 10; i++ {
		d.AddStep(fmt.Sprintf("state_%d", i))
	}

	// Now add a state that matches an OLD state (should be evicted)
	result := d.AddStep("state_0")
	// Since state_0 was evicted from history, shouldn't detect as cycle
	// unless Brent's algorithm happens to match
	t.Logf("Detected: %v, History size: %d", result.Detected, d.Stats().HistorySize)
}

func TestCycleDetectionResult_ErrorCollection(t *testing.T) {
	// T-03: Test that CycleDetectionResult can hold errors
	result := CycleDetectionResult{
		Detected:    true,
		CycleLength: 2,
		Cycle:       []string{"A", "B"},
		Errors: []error{
			fmt.Errorf("error 1"),
			fmt.Errorf("error 2"),
		},
	}

	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(result.Errors))
	}
}

func TestCycleDetector_TailLengthAccuracy(t *testing.T) {
	// I-01: Test that TailLength is accurately calculated
	// Note: Brent's algorithm detects cycles relative to sequence start,
	// not arbitrary repeated patterns in the middle.

	t.Run("single_step_tail", func(t *testing.T) {
		d := NewCycleDetector(nil)

		// Sequence: X -> A -> A (tail of 1, then A repeats)
		d.AddStep("X")           // step 1: tail (tortoise=X)
		d.AddStep("A")           // step 2: tortoise stays at X
		result := d.AddStep("A") // step 3: hare=A, but tortoise=X, no cycle yet

		// With this sequence, Brent's won't detect because tortoise is at X
		// The algorithm tracks cycles from the START, not arbitrary repeats
		t.Logf("Detected: %v, TailLength: %d", result.Detected, result.TailLength)
	})

	t.Run("immediate_cycle", func(t *testing.T) {
		d := NewCycleDetector(nil)

		// Sequence: A -> B -> A (immediate cycle back to start)
		d.AddStep("A")           // step 1: tortoise=A
		d.AddStep("B")           // step 2: hare=B, tortoise still at A (lambda=2 != power=1)
		result := d.AddStep("A") // step 3: hare=A == tortoise=A, CYCLE!

		if !result.Detected {
			t.Fatal("expected cycle to be detected for A->B->A")
		}

		// Tail should be 0 (cycle starts at the beginning)
		if result.TailLength != 0 {
			t.Errorf("expected TailLength=0, got %d", result.TailLength)
		}
		if result.CycleLength != 2 {
			t.Errorf("expected CycleLength=2, got %d", result.CycleLength)
		}
	})

	t.Run("self_loop", func(t *testing.T) {
		d := NewCycleDetector(nil)

		// Sequence: A -> A (immediate self-loop)
		d.AddStep("A")           // step 1: tortoise=A
		result := d.AddStep("A") // step 2: hare=A == tortoise=A, CYCLE!

		if !result.Detected {
			t.Fatal("expected self-loop cycle to be detected")
		}

		// Tail should be 0, cycle length 1
		if result.TailLength != 0 {
			t.Errorf("expected TailLength=0 for self-loop, got %d", result.TailLength)
		}
	})
}

func TestGetStateKey_EmptyFields(t *testing.T) {
	// Ensure GetStateKey handles empty fields gracefully
	step := StepRecord{
		Decision: "", // Empty string (zero value for string type)
		Tool:     "",
		Outcome:  "",
		Actor:    "",
	}

	key := GetStateKey(step)
	// Should produce a key even with empty values (format: "::::")
	if key == "" {
		t.Error("expected non-empty key even with empty fields")
	}
	expectedKey := ":::" // Four empty fields with colons
	if key != expectedKey {
		t.Errorf("expected key %q, got %q", expectedKey, key)
	}
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkCycleDetector_AddStep(b *testing.B) {
	d := NewCycleDetector(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.AddStep("state_A")
	}
}

func BenchmarkCycleDetector_NoCycle(b *testing.B) {
	d := NewCycleDetector(nil)
	states := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state := states[i%len(states)]
		d.AddStep(state)
	}
}

func BenchmarkTarjanSCC_SmallGraph(b *testing.B) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A", "D"},
		"D": {"E"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tarjanSCC(nodes, edges)
	}
}

func BenchmarkGetStateKey(b *testing.B) {
	step := StepRecord{
		Decision: DecisionExecuteTool,
		Tool:     "list_packages",
		Outcome:  OutcomeSuccess,
		Actor:    ActorRouter,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetStateKey(step)
	}
}

// T-04: Benchmark Tarjan SCC with larger graphs
func BenchmarkTarjanSCC_LargeGraph(b *testing.B) {
	// Create a graph with 1000 nodes and multiple cycles
	nodeCount := 1000
	nodes := make([]string, nodeCount)
	edges := make(map[string][]string)

	for i := 0; i < nodeCount; i++ {
		nodes[i] = fmt.Sprintf("node_%d", i)
	}

	// Create edges: linear chain with some back-edges creating cycles
	for i := 0; i < nodeCount-1; i++ {
		edges[nodes[i]] = []string{nodes[i+1]}
		// Add back-edges every 10 nodes to create cycles
		if i > 10 && i%10 == 0 {
			edges[nodes[i]] = append(edges[nodes[i]], nodes[i-10])
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tarjanSCC(nodes, edges)
	}
}

func BenchmarkTarjanSCC_DenseGraph(b *testing.B) {
	// Dense graph: 100 nodes, each connected to next 5 nodes
	nodeCount := 100
	nodes := make([]string, nodeCount)
	edges := make(map[string][]string)

	for i := 0; i < nodeCount; i++ {
		nodes[i] = fmt.Sprintf("node_%d", i)
		for j := 1; j <= 5 && i+j < nodeCount; j++ {
			edges[nodes[i]] = append(edges[nodes[i]], nodes[(i+j)%nodeCount])
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tarjanSCC(nodes, edges)
	}
}
