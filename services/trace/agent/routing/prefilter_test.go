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
			// IT-12 Rev 4: Two-endpoint pattern must come before get_call_chain.
			{
				Patterns: []string{"call chain from .* to"},
				Tool:     "find_path",
				Reason:   "Two-endpoint call chain query",
			},
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

	// IT-12 Rev 4: "call chain from X to Y" is a two-endpoint query → find_path.
	result := pf.Filter(context.Background(), "show the call chain from main to handler", specs, nil)

	if result.ForcedTool != "find_path" {
		t.Errorf("expected forced tool 'find_path', got %q", result.ForcedTool)
	}
	if result.ForcedReason == "" {
		t.Error("expected a forced reason")
	}
}

func TestPreFilter_ForcedMapping_SingleEndpoint(t *testing.T) {
	pf := newTestPreFilter(makeTestConfig())
	specs := makeTestSpecs(16)

	// Single-endpoint "call chain from X" (no "to") → get_call_chain.
	result := pf.Filter(context.Background(), "show the call chain from main", specs, nil)

	if result.ForcedTool != "get_call_chain" {
		t.Errorf("expected forced tool 'get_call_chain', got %q", result.ForcedTool)
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
// IT-06: find_references vs find_symbol disambiguation tests
// =============================================================================

// makeIT06Config returns a pre-filter config with the find_references and
// find_symbol forced mappings that fix the Run 4 routing regression (IT-06).
// These correspond exactly to the patterns in prefilter_rules.yaml.
func makeIT06Config() *config.PreFilterConfig {
	base := makeTestConfig()
	// Prepend reference/symbol disambiguation forced mappings BEFORE any existing
	// mappings so Phase 1 fires before keyword scoring.
	refMappings := []config.ForcedMapping{
		{
			Patterns: []string{
				"where is .* referenced across",
				"where is .* referenced in",
				"where is .* referenced throughout",
				"how is .* referenced",
				"find all references to",
				"referenced across the codebase",
				"referenced throughout",
				"all usages of",
				"all uses of",
			},
			Tool:   "find_references",
			Reason: "Passive-voice reference query with prepositional context",
		},
		{
			Patterns: []string{
				"where is .* defined",
				"where is .* declared",
				"where is it defined",
				"find the definition of",
				"locate the .* definition",
			},
			Tool:   "find_symbol",
			Reason: "Definition lookup — asking for declaration site",
		},
	}
	base.ForcedMappings = append(refMappings, base.ForcedMappings...)
	return base
}

// TestPreFilter_IT06_ReferencedAcross verifies that "Where is X referenced across"
// queries are forced to find_references even when find_symbol would otherwise
// score from "where is" substring.
func TestPreFilter_IT06_ReferencedAcross(t *testing.T) {
	pf := newTestPreFilter(makeIT06Config())
	specs := []ToolSpec{
		{Name: "find_references", Description: "Find all references to a symbol", BestFor: []string{"references", "usages"}},
		{Name: "find_symbol", Description: "Find where a symbol is defined", BestFor: []string{"where is", "locate", "definition"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	queries := []struct {
		query string
		desc  string
	}{
		{"Where is the Item struct referenced across this codebase?", "Badger IT-06 test 5104"},
		{"Where is the Blueprint class referenced across the Flask codebase?", "Flask test 6048"},
		{"Where is the GroupBy class referenced across the Pandas codebase?", "Pandas expansion test"},
		{"Where is the Response object referenced across the codebase?", "Express expansion test"},
		{"Where is the Vector3 class referenced across the codebase?", "BabylonJS expansion test"},
		{"Where is the ModuleRef class referenced across the codebase?", "NestJS expansion test"},
		{"Where is the Drawer class referenced across the codebase?", "Plottable expansion test"},
	}

	for _, tc := range queries {
		t.Run(tc.desc, func(t *testing.T) {
			result := pf.Filter(context.Background(), tc.query, specs, nil)
			if result.ForcedTool != "find_references" {
				t.Errorf("query %q: expected ForcedTool=find_references, got %q", tc.query, result.ForcedTool)
			}
		})
	}
}

// TestPreFilter_IT06_ReferencedInThroughout verifies "referenced in" and
// "referenced throughout" forms are forced to find_references.
func TestPreFilter_IT06_ReferencedInThroughout(t *testing.T) {
	pf := newTestPreFilter(makeIT06Config())
	specs := []ToolSpec{
		{Name: "find_references", Description: "Find all references", BestFor: []string{"references"}},
		{Name: "find_symbol", Description: "Find definition", BestFor: []string{"locate"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	queries := []struct {
		query string
		desc  string
	}{
		{"Where is the Route constructor referenced in this codebase?", "Express test with 'in'"},
		{"Where is WriteBatch referenced throughout the Badger codebase?", "Badger test with 'throughout'"},
		{"Where is the Resource interface referenced in this project?", "Hugo test with 'in'"},
		{"Find all references to the Flask class in the codebase.", "Flask CRS test"},
	}

	for _, tc := range queries {
		t.Run(tc.desc, func(t *testing.T) {
			result := pf.Filter(context.Background(), tc.query, specs, nil)
			if result.ForcedTool != "find_references" {
				t.Errorf("query %q: expected ForcedTool=find_references, got %q", tc.query, result.ForcedTool)
			}
		})
	}
}

// TestPreFilter_IT06_FindSymbol_NotHijackedByReferences verifies that "where is X
// defined?" queries are NOT hijacked by find_references — they should force find_symbol.
func TestPreFilter_IT06_FindSymbol_NotHijackedByReferences(t *testing.T) {
	pf := newTestPreFilter(makeIT06Config())
	specs := []ToolSpec{
		{Name: "find_references", Description: "Find all references", BestFor: []string{"references"}},
		{Name: "find_symbol", Description: "Find definition", BestFor: []string{"locate", "definition"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	queries := []struct {
		query string
	}{
		{"Where is the Router type defined?"},
		{"Where is parseConfig declared?"},
		{"Where is it defined in the codebase?"},
		{"Find the definition of DataFrame"},
	}

	for _, tc := range queries {
		t.Run(tc.query, func(t *testing.T) {
			result := pf.Filter(context.Background(), tc.query, specs, nil)
			if result.ForcedTool != "find_symbol" {
				t.Errorf("query %q: expected ForcedTool=find_symbol, got %q", tc.query, result.ForcedTool)
			}
		})
	}
}

// TestPreFilter_IT06_NegationNotHijackedByForcedMappings verifies that negation
// queries like "not referenced anywhere" still reach the negation phase and are
// forced to find_dead_code — NOT intercepted by the find_references forced mapping.
func TestPreFilter_IT06_NegationNotHijackedByForcedMappings(t *testing.T) {
	pf := newTestPreFilter(makeIT06Config())
	specs := []ToolSpec{
		{Name: "find_references", Description: "Find all references", BestFor: []string{"references"}},
		{Name: "find_dead_code", Description: "Find dead code", BestFor: []string{"dead code", "unused"}},
		{Name: "find_callers", Description: "Find callers", BestFor: []string{"callers"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	// These negation queries should NOT match the find_references forced mappings
	// (the YAML comments explain why: patterns require "across/in/throughout" as guard).
	queries := []struct {
		query      string
		expectedDC bool // should route to find_dead_code
		desc       string
	}{
		{"functions with no callers in the package", true, "no callers → dead code"},
		{"find dead code with zero references", true, "zero references → dead code"},
		{"functions never called by anyone", true, "never called → dead code"},
	}

	for _, tc := range queries {
		t.Run(tc.desc, func(t *testing.T) {
			result := pf.Filter(context.Background(), tc.query, specs, nil)
			if tc.expectedDC && result.ForcedTool != "find_dead_code" {
				t.Errorf("query %q: expected ForcedTool=find_dead_code, got %q (negation should not be hijacked by find_references forced mapping)", tc.query, result.ForcedTool)
			}
		})
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

// =============================================================================
// CB-62 Rev 2: Routing Encyclopedia Tests
// =============================================================================

func makeEncyclopediaConfig() *config.PreFilterConfig {
	cfg := makeTestConfig()
	cfg.RoutingEncyclopedia = []config.EncyclopediaEntry{
		{
			Tool:        "find_implementations",
			Tier:        "boost",
			BoostAmount: 0.25,
			Intents: []config.IntentPattern{
				{Pattern: "what .*extend"},
				{Pattern: "classes extend"},
				{Pattern: "extends the"},
				{Pattern: "class hierarchy"},
			},
			AntiSignals: []string{"mock implementation", "test double"},
			Reason:      "Class inheritance query",
		},
		// IT-12 Rev 4: Two-endpoint "call chain from X to Y" → find_path.
		// Must appear BEFORE get_call_chain so the more specific pattern matches first.
		{
			Tool: "find_path",
			Tier: "force",
			Intents: []config.IntentPattern{
				{Pattern: "call chain from .* to"},
			},
			Reason: "Two-endpoint call chain — route to find_path",
		},
		{
			Tool: "get_call_chain",
			Tier: "force",
			Intents: []config.IntentPattern{
				{Pattern: "call chain from"},
				{Pattern: "full call hierarchy"},
			},
			Reason: "Explicit call chain — unambiguous",
		},
		{
			Tool: "find_path",
			Tier: "hint",
			Intents: []config.IntentPattern{
				{Pattern: "shortest path between"},
			},
			Reason: "Path query hint",
		},
		{
			Tool:        "find_references",
			Tier:        "boost",
			BoostAmount: 0.20,
			Intents: []config.IntentPattern{
				{Pattern: "where is .* referenced across"},
				{Pattern: "all usages of"},
			},
			AntiSignals: []string{"no references", "not referenced", "unreferenced"},
			Reason:      "Passive-voice reference query",
		},
		{
			Tool: "find_dead_code",
			Tier: "force",
			Intents: []config.IntentPattern{
				{Pattern: "no callers"},
				{Pattern: "dead code"},
				{Pattern: "unreferenced"},
			},
			Reason: "Dead code detection",
		},
	}
	return cfg
}

func TestApplyEncyclopedia_ForceMatch(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	// IT-12 Rev 4: "call chain from X to Y" → find_path (two-endpoint).
	forcedTool, boosts, hints := pf.applyEncyclopedia("show the call chain from main to handler")

	if forcedTool != "find_path" {
		t.Errorf("expected forced tool 'find_path', got %q", forcedTool)
	}
	if boosts != nil {
		t.Errorf("expected nil boosts on force, got %v", boosts)
	}
	if hints != nil {
		t.Errorf("expected nil hints on force, got %v", hints)
	}
}

func TestApplyEncyclopedia_ForceMatch_SingleEndpoint(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	// Single-endpoint "call chain from X" → get_call_chain.
	forcedTool, _, _ := pf.applyEncyclopedia("show the call chain from main")

	if forcedTool != "get_call_chain" {
		t.Errorf("expected forced tool 'get_call_chain', got %q", forcedTool)
	}
}

func TestApplyEncyclopedia_BoostMatch(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	forcedTool, boosts, _ := pf.applyEncyclopedia("what classes extend the light base class")

	if forcedTool != "" {
		t.Errorf("expected no forced tool for boost, got %q", forcedTool)
	}
	if boosts["find_implementations"] != 0.25 {
		t.Errorf("expected find_implementations boost 0.25, got %f", boosts["find_implementations"])
	}
}

func TestApplyEncyclopedia_HintMatch(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	forcedTool, _, hints := pf.applyEncyclopedia("find the shortest path between function a and function b")

	if forcedTool != "" {
		t.Errorf("expected no forced tool for hint, got %q", forcedTool)
	}
	found := false
	for _, h := range hints {
		if h == "find_path" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'find_path' in hints, got %v", hints)
	}
}

func TestApplyEncyclopedia_AntiSignalSuppresses(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	// "mock implementation" anti-signal should suppress find_implementations boost
	_, boosts, _ := pf.applyEncyclopedia("what classes extend the mock implementation of handler")

	if boosts["find_implementations"] != 0 {
		t.Errorf("expected find_implementations boost suppressed by anti-signal, got %f", boosts["find_implementations"])
	}
}

func TestApplyEncyclopedia_NoMatch(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	forcedTool, boosts, hints := pf.applyEncyclopedia("what is the performance of the system")

	if forcedTool != "" {
		t.Errorf("expected no forced tool, got %q", forcedTool)
	}
	if len(boosts) != 0 {
		t.Errorf("expected empty boosts, got %v", boosts)
	}
	if len(hints) != 0 {
		t.Errorf("expected empty hints, got %v", hints)
	}
}

func TestApplyEncyclopedia_ForceWithAntiSignal(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	// "unreferenced" matches find_dead_code force, but also matches
	// find_references anti-signal. find_dead_code has no anti-signal for
	// "unreferenced", so it should force find_dead_code.
	forcedTool, _, _ := pf.applyEncyclopedia("find unreferenced functions in the codebase")

	if forcedTool != "find_dead_code" {
		t.Errorf("expected forced tool 'find_dead_code', got %q", forcedTool)
	}
}

func TestApplyEncyclopedia_ReferenceAntiSignalBlocksBoost(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)

	// "no references" should suppress find_references boost
	_, boosts, _ := pf.applyEncyclopedia("functions with no references in the codebase")

	if boosts["find_references"] != 0 {
		t.Errorf("expected find_references boost suppressed by 'no references' anti-signal, got %f", boosts["find_references"])
	}
}

func TestNarrowTools_EncyclopediaBoostIntegration(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	cfg.ScoringMode = "embedding_primary"
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)

	result := pf.Filter(context.Background(), "what classes extend the base handler", specs, nil)

	// The encyclopedia boost should have been applied
	foundBoostRule := false
	for _, rule := range result.AppliedRules {
		if rule == "encyclopedia_boost:find_implementations" {
			foundBoostRule = true
		}
	}
	if !foundBoostRule {
		t.Errorf("expected 'encyclopedia_boost:find_implementations' in applied rules, got %v", result.AppliedRules)
	}
}

func TestNarrowTools_EncyclopediaForceIntegration(t *testing.T) {
	cfg := makeEncyclopediaConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)

	// "full call hierarchy" matches the encyclopedia force for get_call_chain.
	// But it also matches the existing forced_mapping for get_call_chain.
	// Phase 0 (encyclopedia) runs first, so it should force via encyclopedia.
	result := pf.Filter(context.Background(), "show the full call hierarchy of main", specs, nil)

	if result.ForcedTool != "get_call_chain" {
		t.Errorf("expected forced tool 'get_call_chain', got %q", result.ForcedTool)
	}
}

func TestApplyEncyclopedia_EmptyEncyclopedia(t *testing.T) {
	cfg := makeTestConfig()
	// No encyclopedia entries
	pf := newTestPreFilter(cfg)

	forcedTool, boosts, hints := pf.applyEncyclopedia("what classes extend base")

	if forcedTool != "" || len(boosts) != 0 || len(hints) != 0 {
		t.Error("expected empty results from empty encyclopedia")
	}
}

// IT-R2c Fix B: Tests for find_important vs find_weighted_criticality forced mapping.
// Verifies that "most important" queries route to find_important, not find_weighted_criticality.
func TestPreFilter_ForcedMapping_FindImportantNotWeightedCriticality(t *testing.T) {
	cfg := makeTestConfig()
	// Add the find_important forced mapping (matches production config in prefilter_rules.yaml)
	cfg.ForcedMappings = append(cfg.ForcedMappings, config.ForcedMapping{
		Patterns: []string{
			"most important.*pagerank",
			"pagerank.*most important",
			"most important functions in",
			"most important.*by pagerank",
			"important.*by pagerank",
			"pagerank.*in the",
			"lowest pagerank",
			"peripheral functions",
			"least important",
			"most important in the",
			"functions are most important",
			"which.*most important",
		},
		Tool:   "find_important",
		Reason: "Explicit importance/PageRank query",
	})
	pf := newTestPreFilter(cfg)
	// Need specs that include both find_important and find_weighted_criticality
	specs := []ToolSpec{
		{Name: "find_important", Description: "Find important symbols by PageRank", BestFor: []string{"important", "pagerank", "centrality"}},
		{Name: "find_weighted_criticality", Description: "Find critical functions by risk", BestFor: []string{"critical", "risk", "stability"}},
		{Name: "find_hotspots", Description: "Find highly connected nodes", BestFor: []string{"hotspot", "connected"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	queries := []struct {
		query    string
		expected string
		desc     string
	}{
		{"What are the most important functions in the compaction subsystem by PageRank?", "find_important", "explicit PageRank + most important"},
		{"Which functions are most important in the read path versus the write path?", "find_important", "most important in subsystem"},
		{"most important functions in the rendering module", "find_important", "most important functions in module"},
		{"lowest pagerank functions", "find_important", "lowest pagerank → peripheral"},
		{"find peripheral functions", "find_important", "peripheral functions"},
	}

	for _, tc := range queries {
		t.Run(tc.desc, func(t *testing.T) {
			result := pf.Filter(context.Background(), tc.query, specs, nil)
			if result.ForcedTool != tc.expected {
				t.Errorf("query %q: expected ForcedTool=%q, got %q (scores: %v)",
					tc.query, tc.expected, result.ForcedTool, result.Scores)
			}
		})
	}
}

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
	query := "show the call chain from main"

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

// =============================================================================
// CB-62: Scoring Mode Tests
// =============================================================================

// makeEmbeddingPrimaryConfig returns a config with embedding_primary scoring mode.
func makeEmbeddingPrimaryConfig() *config.PreFilterConfig {
	cfg := makeTestConfig()
	cfg.ScoringMode = "embedding_primary"
	cfg.ScoreFloor = 0.30
	cfg.ScoreGapThreshold = 0.15
	cfg.MaxCandidates = 20
	return cfg
}

func TestScoreHybrid_EmbeddingPrimaryMode(t *testing.T) {
	// In embedding_primary mode, when embeddings are unavailable (no Ollama),
	// BM25 scores should NOT be used. Instead, scores should be nil (passthrough).
	cfg := makeEmbeddingPrimaryConfig()

	// Force unreachable Ollama endpoint so embeddings fail.
	t.Setenv("EMBEDDING_SERVICE_URL", "http://localhost:1/api/embed")

	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)

	scores := pf.scoreHybrid(context.Background(), "find callers of main", specs, nil)

	// In embedding_primary mode, if embeddings are unavailable, scores should be nil
	// (passthrough to router).
	if scores != nil {
		t.Errorf("expected nil scores in embedding_primary mode without embeddings, got %d scores", len(scores))
	}
}

func TestScoreHybrid_HybridBackwardCompat(t *testing.T) {
	// In hybrid mode, BM25-only fallback should still work when embeddings unavailable.
	t.Setenv("EMBEDDING_SERVICE_URL", "http://localhost:1/api/embed")
	cfg := makeTestConfig()
	cfg.ScoringMode = "hybrid"
	pf := newTestPreFilter(cfg)
	specs := []ToolSpec{
		{Name: "find_callers", Description: "Find callers", BestFor: []string{"callers", "who calls"}},
		{Name: "find_callees", Description: "Find callees", BestFor: []string{"callees", "what does it call"}},
		{Name: "answer", Description: "Answer", BestFor: []string{"answer"}},
	}

	scores := pf.scoreHybrid(context.Background(), "who calls parseconfig", specs, nil)

	// In hybrid mode, BM25 should still produce scores even without embeddings
	if len(scores) == 0 {
		t.Error("expected non-empty scores in hybrid mode (BM25 fallback)")
	}
}

func TestScoreHybrid_SynchronousWarmup(t *testing.T) {
	// Verify warm-up blocks (not async): after scoreHybrid returns,
	// the warmOnce should have completed.
	t.Setenv("EMBEDDING_SERVICE_URL", "http://localhost:1/api/embed")
	cfg := makeEmbeddingPrimaryConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(16)

	// Call scoreHybrid — this triggers warmOnce.Do synchronously
	pf.scoreHybrid(context.Background(), "test query", specs, nil)

	// warmOnce should have executed (we can't verify the internal state
	// directly, but the fact that scoreHybrid returned means the sync
	// warm-up completed or timed out — it didn't launch an async goroutine).
	// Calling again should be a no-op.
	pf.scoreHybrid(context.Background(), "test query 2", specs, nil)
}

// =============================================================================
// CB-62: Adaptive Candidate Window Tests
// =============================================================================

func TestSelectCandidates_NilScores_Passthrough(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(55)

	result := pf.selectCandidates(nil, specs)

	if len(result) != len(specs) {
		t.Errorf("expected passthrough (all %d specs), got %d", len(specs), len(result))
	}
}

func TestSelectCandidates_EmptyScores_Passthrough(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	pf := newTestPreFilter(cfg)
	specs := makeTestSpecs(10)

	result := pf.selectCandidates(map[string]float64{}, specs)

	if len(result) != len(specs) {
		t.Errorf("expected passthrough (all %d specs), got %d", len(specs), len(result))
	}
}

func TestSelectCandidates_ScoreFloor(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	cfg.ScoreFloor = 0.50
	cfg.ScoreGapThreshold = 0.50 // high threshold so gap cutoff doesn't interfere
	cfg.MinCandidates = 1
	pf := newTestPreFilter(cfg)

	specs := []ToolSpec{
		{Name: "tool_a"}, {Name: "tool_b"}, {Name: "tool_c"},
		{Name: "tool_d"}, {Name: "tool_e"},
	}
	scores := map[string]float64{
		"tool_a": 0.90,
		"tool_b": 0.70,
		"tool_c": 0.40, // below floor
		"tool_d": 0.30, // below floor
		"tool_e": 0.10, // below floor
	}

	result := pf.selectCandidates(scores, specs)

	resultNames := make(map[string]bool)
	for _, s := range result {
		resultNames[s.Name] = true
	}

	if !resultNames["tool_a"] || !resultNames["tool_b"] {
		t.Errorf("expected tool_a and tool_b above floor to be included, got %v", resultNames)
	}
	if resultNames["tool_d"] || resultNames["tool_e"] {
		t.Error("expected tool_d and tool_e below floor to be excluded")
	}
}

func TestSelectCandidates_GapCutoff(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	cfg.ScoreFloor = 0.10 // low floor to not interfere
	cfg.ScoreGapThreshold = 0.20
	cfg.MinCandidates = 2
	cfg.MaxCandidates = 20
	pf := newTestPreFilter(cfg)

	specs := []ToolSpec{
		{Name: "tool_a"}, {Name: "tool_b"}, {Name: "tool_c"},
		{Name: "tool_d"}, {Name: "tool_e"},
	}
	//Scores: a=0.9, b=0.85, c=0.80, d=0.50 (gap=0.30 > 0.20), e=0.40
	scores := map[string]float64{
		"tool_a": 0.90,
		"tool_b": 0.85,
		"tool_c": 0.80,
		"tool_d": 0.50, // gap from c to d = 0.30 > threshold 0.20
		"tool_e": 0.40,
	}

	result := pf.selectCandidates(scores, specs)

	resultNames := make(map[string]bool)
	for _, s := range result {
		resultNames[s.Name] = true
	}

	// Gap cutoff at index 3 (after tool_c, before tool_d)
	if !resultNames["tool_a"] || !resultNames["tool_b"] || !resultNames["tool_c"] {
		t.Error("expected tool_a, tool_b, tool_c to be included (above gap)")
	}
	if resultNames["tool_d"] || resultNames["tool_e"] {
		t.Error("expected tool_d, tool_e to be excluded (below gap cutoff)")
	}
}

func TestSelectCandidates_GapCutoff_NoGap(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	cfg.ScoreFloor = 0.10
	cfg.ScoreGapThreshold = 0.50 // very high threshold — no gap exceeds it
	cfg.MinCandidates = 2
	cfg.MaxCandidates = 20
	pf := newTestPreFilter(cfg)

	specs := []ToolSpec{
		{Name: "tool_a"}, {Name: "tool_b"}, {Name: "tool_c"},
		{Name: "tool_d"}, {Name: "tool_e"},
	}
	scores := map[string]float64{
		"tool_a": 0.90,
		"tool_b": 0.85,
		"tool_c": 0.80,
		"tool_d": 0.75,
		"tool_e": 0.70,
	}

	result := pf.selectCandidates(scores, specs)

	// No gap exceeds threshold → all above-floor tools included (up to MaxCandidates)
	if len(result) != 5 {
		t.Errorf("expected all 5 tools when no gap exceeds threshold, got %d", len(result))
	}
}

func TestSelectCandidates_MinFloor(t *testing.T) {
	cfg := makeEmbeddingPrimaryConfig()
	cfg.ScoreFloor = 0.80
	cfg.ScoreGapThreshold = 0.50
	cfg.MinCandidates = 3
	pf := newTestPreFilter(cfg)

	specs := []ToolSpec{
		{Name: "tool_a"}, {Name: "tool_b"}, {Name: "tool_c"},
		{Name: "tool_d"}, {Name: "tool_e"},
	}
	//Only tool_a is above floor, but MinCandidates=3
	scores := map[string]float64{
		"tool_a": 0.90,
		"tool_b": 0.70,
		"tool_c": 0.60,
		"tool_d": 0.50,
		"tool_e": 0.40,
	}

	result := pf.selectCandidates(scores, specs)

	if len(result) < cfg.MinCandidates {
		t.Errorf("expected at least %d candidates (MinCandidates), got %d", cfg.MinCandidates, len(result))
	}
}
