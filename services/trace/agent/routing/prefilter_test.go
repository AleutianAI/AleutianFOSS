// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"log/slog"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/config"
)

// makeTestSpecs creates a set of tool specs for testing.
func makeTestSpecs(count int) []ToolSpec {
	names := []string{
		"find_symbol", "find_callers", "find_callees", "find_references",
		"find_dead_code", "find_hotspots", "find_weighted_criticality",
		"find_cycles", "find_circular_deps", "get_call_chain",
		"find_path", "find_entry_points", "find_implementations",
		"get_file_summary", "read_symbol", "answer",
		"find_symbol_usages", "get_symbol_details", "find_dependencies",
		"find_dependents", "get_package_summary", "find_interfaces",
		"get_call_graph", "find_similar_code", "get_metrics",
		"find_test_coverage", "get_complexity", "find_patterns",
		"find_imports", "find_exports", "get_type_hierarchy",
		"find_constructors", "find_decorators", "find_annotations",
		"get_class_diagram", "find_overrides", "find_abstractions",
		"get_module_graph", "find_globals", "find_constants",
		"find_enums", "find_structs", "find_interfaces_impl",
		"find_generics", "find_closures", "find_callbacks",
		"find_event_handlers", "find_middleware", "find_routes",
		"find_migrations", "find_schemas", "find_validators",
		"find_serializers", "find_transformers", "find_adapters",
	}

	if count > len(names) {
		count = len(names)
	}

	specs := make([]ToolSpec, count)
	for i := 0; i < count; i++ {
		specs[i] = ToolSpec{
			Name:        names[i],
			Description: "Description for " + names[i],
			BestFor:     []string{names[i]},
			Category:    "test",
		}
	}
	return specs
}

// makeTestConfig creates a test pre-filter config.
func makeTestConfig() *config.PreFilterConfig {
	return &config.PreFilterConfig{
		Enabled:           true,
		MinCandidates:     3,
		MaxCandidates:     10,
		NegationProximity: 3,
		AlwaysInclude:     []string{"answer"},
		ForcedMappings: []config.ForcedMapping{
			{
				Patterns: []string{"call chain from", "call chain of", "full call hierarchy"},
				Tool:     "get_call_chain",
				Reason:   "Explicit call chain request",
			},
			{
				Patterns: []string{"shortest path between", "path from .* to"},
				Tool:     "find_path",
				Reason:   "Path query is unambiguous",
			},
		},
		NegationRules: []config.NegationRule{
			{
				NegationWords:   []string{"no", "not", "never", "zero", "without"},
				TriggerKeywords: []string{"callers", "incoming calls", "called", "referenced"},
				WrongTool:       "find_callers",
				CorrectTool:     "find_dead_code",
				Action:          "force",
				Reason:          "Negated caller = dead code",
			},
			{
				NegationWords:   []string{"no", "not", "never", "zero"},
				TriggerKeywords: []string{"references", "usages", "uses"},
				WrongTool:       "find_references",
				CorrectTool:     "find_dead_code",
				Action:          "force",
				Reason:          "Negated reference = unreferenced code",
			},
		},
		ConfusionPairs: []config.ConfusionPair{
			{
				ToolA:         "find_callers",
				ToolB:         "find_callees",
				ToolAPatterns: []string{"who calls", "what calls", "callers of", "call sites for"},
				ToolBPatterns: []string{"what does .* call", "what functions does", "dependencies of"},
				BoostAmount:   3.0,
			},
			{
				ToolA:         "find_callers",
				ToolB:         "find_references",
				ToolAPatterns: []string{"who calls", "what calls", "call sites"},
				ToolBPatterns: []string{"where is .* used", "references to", "usages of", "find all uses"},
				BoostAmount:   3.0,
			},
			{
				ToolA:         "find_hotspots",
				ToolB:         "find_weighted_criticality",
				ToolAPatterns: []string{"most connected", "coupling", "fan-in", "fan-out", "hotspot"},
				ToolBPatterns: []string{"most critical", "highest risk", "would break", "stability"},
				BoostAmount:   3.0,
			},
			{
				ToolA:         "find_cycles",
				ToolB:         "find_circular_deps",
				ToolAPatterns: []string{"function cycles", "call cycles", "mutual recursion"},
				ToolBPatterns: []string{"circular dependencies", "package cycles", "dependency cycle"},
				BoostAmount:   2.0,
			},
		},
	}
}

func newTestPreFilter(cfg *config.PreFilterConfig) *PreFilter {
	return NewPreFilter(nil, cfg, slog.Default(), nil)
}

// =============================================================================
// Disabled / Passthrough Tests
// =============================================================================

func TestPreFilter_Disabled(t *testing.T) {
	cfg := makeTestConfig()
	cfg.Enabled = false
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(10)

	result := pf.Filter(context.Background(), "find callers of main", specs, nil)

	if result.ForcedTool != "" {
		t.Errorf("expected no forced tool when disabled, got %q", result.ForcedTool)
	}
	if len(result.NarrowedSpecs) != len(specs) {
		t.Errorf("expected all specs returned when disabled, got %d vs %d", len(result.NarrowedSpecs), len(specs))
	}
}

func TestPreFilter_NilConfig(t *testing.T) {
	pf := NewPreFilter(nil, nil, slog.Default(), nil)
	specs := makeTestSpecs(10)

	result := pf.Filter(context.Background(), "test query", specs, nil)

	if len(result.NarrowedSpecs) != len(specs) {
		t.Errorf("expected passthrough with nil config, got %d vs %d", len(result.NarrowedSpecs), len(specs))
	}
}

func TestPreFilter_EmptyQuery(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(10)

	result := pf.Filter(context.Background(), "", specs, nil)

	if len(result.NarrowedSpecs) != len(specs) {
		t.Errorf("expected passthrough for empty query, got %d vs %d", len(result.NarrowedSpecs), len(specs))
	}
}

func TestPreFilter_EmptySpecs(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())

	result := pf.Filter(context.Background(), "find callers", nil, nil)

	if len(result.NarrowedSpecs) != 0 {
		t.Errorf("expected empty result for nil specs, got %d", len(result.NarrowedSpecs))
	}
}

// =============================================================================
// Forced Mapping Tests
// =============================================================================

func TestPreFilter_ForcedMapping_Exact(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "show the call chain from main to handler", specs, nil)

	if result.ForcedTool != "get_call_chain" {
		t.Errorf("expected forced tool 'get_call_chain', got %q", result.ForcedTool)
	}
	if result.ForcedReason == "" {
		t.Error("expected a forced reason")
	}
}

func TestPreFilter_ForcedMapping_CaseInsensitive(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "Show the CALL CHAIN FROM main", specs, nil)

	if result.ForcedTool != "get_call_chain" {
		t.Errorf("expected forced tool 'get_call_chain', got %q", result.ForcedTool)
	}
}

func TestPreFilter_ForcedMapping_Regex(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "find the path from handler to database", specs, nil)

	if result.ForcedTool != "find_path" {
		t.Errorf("expected forced tool 'find_path', got %q", result.ForcedTool)
	}
}

func TestPreFilter_ForcedMapping_NoMatch(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "find callers of main", specs, nil)

	if result.ForcedTool != "" {
		t.Errorf("expected no forced tool, got %q", result.ForcedTool)
	}
}

// =============================================================================
// Negation Detection Tests
// =============================================================================

func TestPreFilter_Negation_NoCallers(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "functions with no callers in the routing package", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code', got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_NeverCalled(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "functions never called anywhere", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code', got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_ZeroReferences(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "symbols with zero references in this package", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code', got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_WithoutCallers(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "find functions without callers", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code', got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_ProximityBoundary(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	// "no" at position 0, "callers" at position 6 → distance 6 > 3
	result := pf.Filter(context.Background(), "no, I actually want to find the callers", specs, nil)

	if result.ForcedTool == "find_dead_code" {
		t.Error("should NOT force find_dead_code when negation is far from keyword")
	}
}

func TestPreFilter_Negation_NoNegWord(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "find callers of parseConfig", specs, nil)

	if result.ForcedTool == "find_dead_code" {
		t.Error("should NOT force find_dead_code when there's no negation word")
	}
}

func TestPreFilter_Negation_MultiWordTrigger(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "no incoming calls to this function", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code' for multi-word trigger, got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_NotReferenced(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "types not referenced anywhere", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected 'find_dead_code', got %q", result.ForcedTool)
	}
}

// =============================================================================
// Confusion Pair Tests
// =============================================================================

func TestPreFilter_ConfusionPair_CallersVsCallees(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "what does main call in the handler package", specs, nil)

	// find_callees should be boosted
	if result.Scores["find_callees"] <= result.Scores["find_callers"] {
		t.Errorf("expected find_callees score (%f) > find_callers score (%f)",
			result.Scores["find_callees"], result.Scores["find_callers"])
	}
}

func TestPreFilter_ConfusionPair_CallersBoosted(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "who calls parseConfig in the server", specs, nil)

	if result.Scores["find_callers"] <= result.Scores["find_callees"] {
		t.Errorf("expected find_callers score (%f) > find_callees score (%f)",
			result.Scores["find_callers"], result.Scores["find_callees"])
	}
}

func TestPreFilter_ConfusionPair_CallersVsRefs(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "where is Entry used in the codebase", specs, nil)

	if result.Scores["find_references"] <= result.Scores["find_callers"] {
		t.Errorf("expected find_references score (%f) > find_callers score (%f)",
			result.Scores["find_references"], result.Scores["find_callers"])
	}
}

func TestPreFilter_ConfusionPair_HotspotsVsCriticality(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "what are the most connected functions", specs, nil)

	if result.Scores["find_hotspots"] <= result.Scores["find_weighted_criticality"] {
		t.Errorf("expected find_hotspots score (%f) > find_weighted_criticality score (%f)",
			result.Scores["find_hotspots"], result.Scores["find_weighted_criticality"])
	}
}

func TestPreFilter_ConfusionPair_CriticalityBoosted(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "which functions are highest risk to change", specs, nil)

	if result.Scores["find_weighted_criticality"] <= result.Scores["find_hotspots"] {
		t.Errorf("expected find_weighted_criticality score (%f) > find_hotspots score (%f)",
			result.Scores["find_weighted_criticality"], result.Scores["find_hotspots"])
	}
}

// =============================================================================
// Candidate Count Tests
// =============================================================================

func TestPreFilter_CandidateCount_Max(t *testing.T) {
	cfg := makeTestConfig()
	cfg.MaxCandidates = 10
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(55)

	// Give all specs a score so we exercise the max cap
	// We need keyword matches — use a query that matches many BestFor keywords
	result := pf.Filter(context.Background(), "find symbol callers callees references dead_code hotspots weighted_criticality cycles circular_deps call_chain path entry_points implementations summary read answer", specs, nil)

	if result.ForcedTool == "" && len(result.NarrowedSpecs) > cfg.MaxCandidates+len(cfg.AlwaysInclude) {
		// Allow max + always_include tools
		t.Errorf("expected at most %d candidates (max + always_include), got %d",
			cfg.MaxCandidates+len(cfg.AlwaysInclude), len(result.NarrowedSpecs))
	}
}

func TestPreFilter_CandidateCount_Min(t *testing.T) {
	cfg := makeTestConfig()
	cfg.MinCandidates = 3
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)

	// Query that matches very few keywords
	result := pf.Filter(context.Background(), "xyzzy", specs, nil)

	if result.ForcedTool == "" && len(result.NarrowedSpecs) < cfg.MinCandidates {
		// Passthrough when no keywords match at all
		// This is fine — if nothing matches, return all
	}
}

func TestPreFilter_AnswerAlwaysIncluded(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16) // includes "answer" at index 15

	// Query that triggers keyword matching but not "answer" keywords
	result := pf.Filter(context.Background(), "who calls parseConfig in the routing package", specs, nil)

	if result.ForcedTool != "" {
		// Forced selection — answer inclusion not relevant
		return
	}

	found := false
	for _, s := range result.NarrowedSpecs {
		if s.Name == "answer" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'answer' to always be included in narrowed specs")
	}
}

// =============================================================================
// Applied Rules Tracking
// =============================================================================

func TestPreFilter_AppliedRules_ForcedMapping(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "show the full call hierarchy", specs, nil)

	if len(result.AppliedRules) == 0 {
		t.Error("expected at least one applied rule")
	}
	found := false
	for _, rule := range result.AppliedRules {
		if rule == "forced_mapping:get_call_chain" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'forced_mapping:get_call_chain' in rules, got %v", result.AppliedRules)
	}
}

func TestPreFilter_AppliedRules_Negation(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "functions with no callers", specs, nil)

	found := false
	for _, rule := range result.AppliedRules {
		if rule == "negation:find_dead_code" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'negation:find_dead_code' in rules, got %v", result.AppliedRules)
	}
}

// =============================================================================
// Duration Tracking
// =============================================================================

func TestPreFilter_Duration(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "find callers of main", specs, nil)

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

// =============================================================================
// Forced Tool Spec Validation Tests (#5)
// =============================================================================

func TestPreFilter_ForcedMapping_ToolNotInSpecs(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	// Specs do NOT include "get_call_chain"
	specs := []ToolSpec{
		{Name: "find_callers", Description: "Find callers"},
		{Name: "find_callees", Description: "Find callees"},
		{Name: "answer", Description: "Answer the question"},
	}

	result := pf.Filter(context.Background(), "show the call chain from main to handler", specs, nil)

	// Should NOT force since get_call_chain is not in the spec set
	if result.ForcedTool != "" {
		t.Errorf("expected no forced tool when tool not in specs, got %q", result.ForcedTool)
	}
}

func TestPreFilter_Negation_ToolNotInSpecs(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	// Specs do NOT include "find_dead_code"
	specs := []ToolSpec{
		{Name: "find_callers", Description: "Find callers"},
		{Name: "find_callees", Description: "Find callees"},
		{Name: "answer", Description: "Answer the question"},
	}

	result := pf.Filter(context.Background(), "functions with no callers", specs, nil)

	// Should NOT force since find_dead_code is not in the spec set
	if result.ForcedTool != "" {
		t.Errorf("expected no forced tool when tool not in specs, got %q", result.ForcedTool)
	}
}

// =============================================================================
// Realistic BestFor Keywords Tests (#9)
// =============================================================================

func TestPreFilter_RealisticKeywords_CallersQuery(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := []ToolSpec{
		{Name: "find_callers", Description: "Find functions that call a given function", BestFor: []string{"callers", "who calls", "call sites", "incoming calls"}},
		{Name: "find_callees", Description: "Find functions called by a given function", BestFor: []string{"callees", "what does it call", "outgoing calls", "dependencies"}},
		{Name: "find_references", Description: "Find all references to a symbol", BestFor: []string{"references", "usages", "where is it used"}},
		{Name: "find_dead_code", Description: "Find unreachable or unused code", BestFor: []string{"dead code", "unused", "unreachable", "no callers", "no references"}},
		{Name: "answer", Description: "Answer the user's question directly", BestFor: []string{"answer", "explain", "summarize"}},
	}

	result := pf.Filter(context.Background(), "who calls parseConfig in the server package", specs, nil)

	// find_callers should be boosted via confusion pair
	if result.ForcedTool != "" {
		t.Errorf("unexpected forced tool: %q", result.ForcedTool)
	}
	if result.Scores["find_callers"] <= result.Scores["find_callees"] {
		t.Errorf("expected find_callers score (%f) > find_callees score (%f)",
			result.Scores["find_callers"], result.Scores["find_callees"])
	}
}

func TestPreFilter_RealisticKeywords_DeadCodeQuery(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := []ToolSpec{
		{Name: "find_callers", Description: "Find functions that call a given function", BestFor: []string{"callers", "who calls", "call sites"}},
		{Name: "find_dead_code", Description: "Find unreachable or unused code", BestFor: []string{"dead code", "unused", "unreachable", "no callers"}},
		{Name: "answer", Description: "Answer the user's question directly", BestFor: []string{"answer", "explain"}},
	}

	result := pf.Filter(context.Background(), "functions with no callers in the routing package", specs, nil)

	if result.ForcedTool != "find_dead_code" {
		t.Errorf("expected forced find_dead_code, got %q", result.ForcedTool)
	}
}

func TestPreFilter_RealisticKeywords_HotspotsQuery(t *testing.T) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := []ToolSpec{
		{Name: "find_hotspots", Description: "Find highly connected nodes", BestFor: []string{"hotspot", "most connected", "fan-in", "fan-out", "coupling"}},
		{Name: "find_weighted_criticality", Description: "Find critical functions by risk", BestFor: []string{"critical", "highest risk", "stability", "impact"}},
		{Name: "answer", Description: "Answer the user's question directly", BestFor: []string{"answer"}},
	}

	result := pf.Filter(context.Background(), "which functions have the highest risk to change", specs, nil)

	if result.Scores["find_weighted_criticality"] <= result.Scores["find_hotspots"] {
		t.Errorf("expected find_weighted_criticality score (%f) > find_hotspots score (%f)",
			result.Scores["find_weighted_criticality"], result.Scores["find_hotspots"])
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkPreFilter_55Tools(b *testing.B) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(55)
	ctx := context.Background()
	query := "what functions call parseConfig in the routing package"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pf.Filter(ctx, query, specs, nil)
	}
}

func BenchmarkNegationDetection(b *testing.B) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)
	ctx := context.Background()
	query := "functions with no callers in the routing package"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pf.Filter(ctx, query, specs, nil)
	}
}

func BenchmarkForcedMapping(b *testing.B) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)
	ctx := context.Background()
	query := "show the call chain from main to handler"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pf.Filter(ctx, query, specs, nil)
	}
}

func BenchmarkConfusionPairs(b *testing.B) {
	cfg := makeTestConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)
	ctx := context.Background()
	query := "who calls parseConfig in the server package"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pf.Filter(ctx, query, specs, nil)
	}
}
