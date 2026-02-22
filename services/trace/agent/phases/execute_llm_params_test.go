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
	"context"
	"fmt"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// =============================================================================
// convertMapToTypedParams Tests (IT-08b)
// =============================================================================

func TestConvertMapToTypedParams_FindDeadCode(t *testing.T) {
	params := map[string]any{
		"package":          "helpers",
		"include_exported": false,
		"limit":            float64(50), // JSON numbers are float64
		"exclude_tests":    true,
	}

	result, err := convertMapToTypedParams("find_dead_code", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "helpers" {
		t.Errorf("expected package=helpers, got %s", fdcp.Package)
	}
	if fdcp.IncludeExported != false {
		t.Error("expected include_exported=false")
	}
	if fdcp.Limit != 50 {
		t.Errorf("expected limit=50, got %d", fdcp.Limit)
	}
	if fdcp.ExcludeTests != true {
		t.Error("expected exclude_tests=true")
	}
}

func TestConvertMapToTypedParams_FindHotspots(t *testing.T) {
	params := map[string]any{
		"top":           float64(10),
		"kind":          "function",
		"package":       "auth",
		"exclude_tests": true,
		"sort_by":       "score",
	}

	result, err := convertMapToTypedParams("find_hotspots", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fhp, ok := result.(tools.FindHotspotsParams)
	if !ok {
		t.Fatalf("expected FindHotspotsParams, got %T", result)
	}
	if fhp.Top != 10 {
		t.Errorf("expected top=10, got %d", fhp.Top)
	}
	if fhp.Package != "auth" {
		t.Errorf("expected package=auth, got %s", fhp.Package)
	}
}

func TestConvertMapToTypedParams_FindCallers(t *testing.T) {
	params := map[string]any{
		"function_name": "parseConfig",
		"limit":         float64(20),
		"package_hint":  "config",
	}

	result, err := convertMapToTypedParams("find_callers", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindCallersParams)
	if !ok {
		t.Fatalf("expected FindCallersParams, got %T", result)
	}
	if fcp.FunctionName != "parseConfig" {
		t.Errorf("expected function_name=parseConfig, got %s", fcp.FunctionName)
	}
}

func TestConvertMapToTypedParams_EmptyParams(t *testing.T) {
	for _, toolName := range []string{"list_packages", "find_entry_points", "find_extractable_regions"} {
		result, err := convertMapToTypedParams(toolName, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", toolName, err)
		}
		ep, ok := result.(tools.EmptyParams)
		if !ok {
			t.Fatalf("expected EmptyParams for %s, got %T", toolName, result)
		}
		if ep.Tool != toolName {
			t.Errorf("expected tool=%s, got %s", toolName, ep.Tool)
		}
	}
}

func TestConvertMapToTypedParams_UnknownTool(t *testing.T) {
	params := map[string]any{"key": "value"}
	result, err := convertMapToTypedParams("unknown_tool", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mp, ok := result.(tools.MapParams)
	if !ok {
		t.Fatalf("expected MapParams, got %T", result)
	}
	if mp.Tool != "unknown_tool" {
		t.Errorf("expected tool=unknown_tool, got %s", mp.Tool)
	}
}

func TestConvertMapToTypedParams_DefaultValues(t *testing.T) {
	// Empty params should use defaults
	result, err := convertMapToTypedParams("find_dead_code", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Limit != 50 {
		t.Errorf("expected default limit=50, got %d", fdcp.Limit)
	}
	if fdcp.ExcludeTests != true {
		t.Error("expected default exclude_tests=true")
	}
}

// =============================================================================
// convertMapToTypedParams: Missing Fields Fix (IT-08b Code Review)
// =============================================================================

func TestConvertMapToTypedParams_FindCommunities_AllFields(t *testing.T) {
	params := map[string]any{
		"min_size":         float64(5),
		"resolution":       2.0,
		"top":              float64(15),
		"show_cross_edges": false,
		"package_filter":   "auth",
	}

	result, err := convertMapToTypedParams("find_communities", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindCommunitiesParams)
	if !ok {
		t.Fatalf("expected FindCommunitiesParams, got %T", result)
	}
	if fcp.MinSize != 5 {
		t.Errorf("expected min_size=5, got %d", fcp.MinSize)
	}
	if fcp.Resolution != 2.0 {
		t.Errorf("expected resolution=2.0, got %f", fcp.Resolution)
	}
	if fcp.Top != 15 {
		t.Errorf("expected top=15, got %d", fcp.Top)
	}
	if fcp.ShowCrossEdges != false {
		t.Error("expected show_cross_edges=false")
	}
	if fcp.PackageFilter != "auth" {
		t.Errorf("expected package_filter=auth, got %s", fcp.PackageFilter)
	}
}

func TestConvertMapToTypedParams_FindCommunities_Defaults(t *testing.T) {
	result, err := convertMapToTypedParams("find_communities", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindCommunitiesParams)
	if !ok {
		t.Fatalf("expected FindCommunitiesParams, got %T", result)
	}
	if fcp.MinSize != 3 {
		t.Errorf("expected default min_size=3, got %d", fcp.MinSize)
	}
	if fcp.ShowCrossEdges != true {
		t.Error("expected default show_cross_edges=true")
	}
	if fcp.Resolution != 1.0 {
		t.Errorf("expected default resolution=1.0, got %f", fcp.Resolution)
	}
}

func TestConvertMapToTypedParams_FindCycles_AllFields(t *testing.T) {
	params := map[string]any{
		"min_size":       float64(3),
		"limit":          float64(10),
		"package_filter": "hugolib",
		"sort_by":        "length_asc",
	}

	result, err := convertMapToTypedParams("find_cycles", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindCyclesParams)
	if !ok {
		t.Fatalf("expected FindCyclesParams, got %T", result)
	}
	if fcp.PackageFilter != "hugolib" {
		t.Errorf("expected package_filter=hugolib, got %s", fcp.PackageFilter)
	}
	if fcp.SortBy != "length_asc" {
		t.Errorf("expected sort_by=length_asc, got %s", fcp.SortBy)
	}
}

func TestConvertMapToTypedParams_FindCycles_Defaults(t *testing.T) {
	result, err := convertMapToTypedParams("find_cycles", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindCyclesParams)
	if !ok {
		t.Fatalf("expected FindCyclesParams, got %T", result)
	}
	if fcp.SortBy != "length_desc" {
		t.Errorf("expected default sort_by=length_desc, got %s", fcp.SortBy)
	}
}

func TestConvertMapToTypedParams_FindControlDependencies(t *testing.T) {
	params := map[string]any{
		"target": "handleRequest",
		"depth":  float64(8),
	}

	result, err := convertMapToTypedParams("find_control_dependencies", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindControlDependenciesParams)
	if !ok {
		t.Fatalf("expected FindControlDependenciesParams, got %T", result)
	}
	if fcp.Target != "handleRequest" {
		t.Errorf("expected target=handleRequest, got %s", fcp.Target)
	}
	if fcp.Depth != 8 {
		t.Errorf("expected depth=8, got %d", fcp.Depth)
	}
}

func TestConvertMapToTypedParams_FindControlDependencies_DefaultDepth(t *testing.T) {
	result, err := convertMapToTypedParams("find_control_dependencies", map[string]any{
		"target": "serve",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fcp, ok := result.(tools.FindControlDependenciesParams)
	if !ok {
		t.Fatalf("expected FindControlDependenciesParams, got %T", result)
	}
	if fcp.Depth != 5 {
		t.Errorf("expected default depth=5, got %d", fcp.Depth)
	}
}

func TestConvertMapToTypedParams_FindWeightedCriticality_AllFields(t *testing.T) {
	params := map[string]any{
		"top":           float64(30),
		"entry":         "main",
		"show_quadrant": false,
	}

	result, err := convertMapToTypedParams("find_weighted_criticality", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fwcp, ok := result.(tools.FindWeightedCriticalityParams)
	if !ok {
		t.Fatalf("expected FindWeightedCriticalityParams, got %T", result)
	}
	if fwcp.Top != 30 {
		t.Errorf("expected top=30, got %d", fwcp.Top)
	}
	if fwcp.Entry != "main" {
		t.Errorf("expected entry=main, got %s", fwcp.Entry)
	}
	if fwcp.ShowQuadrant != false {
		t.Error("expected show_quadrant=false")
	}
}

func TestConvertMapToTypedParams_FindWeightedCriticality_Defaults(t *testing.T) {
	result, err := convertMapToTypedParams("find_weighted_criticality", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fwcp, ok := result.(tools.FindWeightedCriticalityParams)
	if !ok {
		t.Fatalf("expected FindWeightedCriticalityParams, got %T", result)
	}
	if fwcp.ShowQuadrant != true {
		t.Error("expected default show_quadrant=true")
	}
}

func TestConvertMapToTypedParams_FindModuleAPI_AllFields(t *testing.T) {
	params := map[string]any{
		"community_id":       float64(3),
		"top":                float64(5),
		"min_community_size": float64(10),
	}

	result, err := convertMapToTypedParams("find_module_api", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fmap, ok := result.(tools.FindModuleAPIParams)
	if !ok {
		t.Fatalf("expected FindModuleAPIParams, got %T", result)
	}
	if fmap.MinCommunitySize != 10 {
		t.Errorf("expected min_community_size=10, got %d", fmap.MinCommunitySize)
	}
}

func TestConvertMapToTypedParams_FindModuleAPI_Defaults(t *testing.T) {
	result, err := convertMapToTypedParams("find_module_api", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fmap, ok := result.(tools.FindModuleAPIParams)
	if !ok {
		t.Fatalf("expected FindModuleAPIParams, got %T", result)
	}
	if fmap.MinCommunitySize != 3 {
		t.Errorf("expected default min_community_size=3, got %d", fmap.MinCommunitySize)
	}
}

func TestConvertMapToTypedParams_FindSymbol_WithPackage(t *testing.T) {
	params := map[string]any{
		"name":    "Engine",
		"kind":    "type",
		"package": "rendering",
	}

	result, err := convertMapToTypedParams("find_symbol", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fsp, ok := result.(tools.FindSymbolParams)
	if !ok {
		t.Fatalf("expected FindSymbolParams, got %T", result)
	}
	if fsp.Package != "rendering" {
		t.Errorf("expected package=rendering, got %s", fsp.Package)
	}
}

func TestConvertMapToTypedParams_FindDominators_WithShowTree(t *testing.T) {
	params := map[string]any{
		"target":    "handleRequest",
		"entry":     "main",
		"show_tree": true,
	}

	result, err := convertMapToTypedParams("find_dominators", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fdp, ok := result.(tools.FindDominatorsParams)
	if !ok {
		t.Fatalf("expected FindDominatorsParams, got %T", result)
	}
	if fdp.ShowTree != true {
		t.Error("expected show_tree=true")
	}
}

// =============================================================================
// buildParamSchemas Tests (IT-08b Code Review)
// =============================================================================

func TestBuildParamSchemas_DeterministicOrder(t *testing.T) {
	toolDef := &tools.ToolDefinition{
		Name: "test_tool",
		Parameters: map[string]tools.ParamDef{
			"zebra":  {Type: "string", Description: "Z param"},
			"alpha":  {Type: "integer", Description: "A param"},
			"middle": {Type: "boolean", Description: "M param"},
		},
	}

	// Run multiple times to verify deterministic order
	for i := 0; i < 10; i++ {
		schemas := buildParamSchemas(toolDef)
		if len(schemas) != 3 {
			t.Fatalf("expected 3 schemas, got %d", len(schemas))
		}
		if schemas[0].Name != "alpha" {
			t.Errorf("iteration %d: expected first schema=alpha, got %s", i, schemas[0].Name)
		}
		if schemas[1].Name != "middle" {
			t.Errorf("iteration %d: expected second schema=middle, got %s", i, schemas[1].Name)
		}
		if schemas[2].Name != "zebra" {
			t.Errorf("iteration %d: expected third schema=zebra, got %s", i, schemas[2].Name)
		}
	}
}

func TestBuildParamSchemas_NilToolDef(t *testing.T) {
	schemas := buildParamSchemas(nil)
	if schemas != nil {
		t.Errorf("expected nil for nil toolDef, got %v", schemas)
	}
}

func TestBuildParamSchemas_EmptyParams(t *testing.T) {
	toolDef := &tools.ToolDefinition{
		Name:       "test_tool",
		Parameters: map[string]tools.ParamDef{},
	}
	schemas := buildParamSchemas(toolDef)
	if schemas != nil {
		t.Errorf("expected nil for empty params, got %v", schemas)
	}
}

// =============================================================================
// enhanceParamsWithLLM Tests (IT-08b)
// =============================================================================

// mockParamExtractor implements agent.ParamExtractor for testing.
type mockParamExtractor struct {
	enabled     bool
	returnErr   error
	returnValue map[string]any
}

func (m *mockParamExtractor) ExtractParams(
	ctx context.Context,
	query string,
	toolName string,
	paramSchemas []agent.ParamExtractorSchema,
	regexHint map[string]any,
) (map[string]any, error) {
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	return m.returnValue, nil
}

func (m *mockParamExtractor) IsEnabled() bool {
	return m.enabled
}

func TestEnhanceParamsWithLLM_NilExtractor(t *testing.T) {
	phase := &ExecutePhase{}
	deps := &Dependencies{ParamExtractor: nil}
	regexResult := tools.FindDeadCodeParams{
		Package: "flask",
		Limit:   50,
	}

	result := phase.enhanceParamsWithLLM(
		context.Background(), deps, "find_dead_code",
		makeToolDefs("find_dead_code"), regexResult,
	)

	// Should return regex result unchanged
	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "flask" {
		t.Errorf("expected package=flask (unchanged), got %s", fdcp.Package)
	}
}

func TestEnhanceParamsWithLLM_ExtractorDisabled(t *testing.T) {
	phase := &ExecutePhase{}
	deps := &Dependencies{
		ParamExtractor: &mockParamExtractor{enabled: false},
	}
	regexResult := tools.FindDeadCodeParams{Package: "flask", Limit: 50}

	result := phase.enhanceParamsWithLLM(
		context.Background(), deps, "find_dead_code",
		makeToolDefs("find_dead_code"), regexResult,
	)

	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "flask" {
		t.Errorf("expected package=flask (unchanged), got %s", fdcp.Package)
	}
}

func TestEnhanceParamsWithLLM_LLMCorrects(t *testing.T) {
	phase := &ExecutePhase{}
	deps := &Dependencies{
		Query: "Find dead code in the Flask helpers module",
		ParamExtractor: &mockParamExtractor{
			enabled: true,
			returnValue: map[string]any{
				"package":          "helpers",
				"include_exported": false,
				"limit":            float64(50),
				"exclude_tests":    true,
			},
		},
	}
	regexResult := tools.FindDeadCodeParams{
		Package:      "flask", // Wrong - regex extracted project name
		Limit:        50,
		ExcludeTests: true,
	}

	// Need tool def with parameters for schema building
	defs := []tools.ToolDefinition{{
		Name: "find_dead_code",
		Parameters: map[string]tools.ParamDef{
			"package": {Type: "string", Description: "Package to scope to"},
		},
	}}

	result := phase.enhanceParamsWithLLM(
		context.Background(), deps, "find_dead_code", defs, regexResult,
	)

	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "helpers" {
		t.Errorf("expected LLM-corrected package=helpers, got %s", fdcp.Package)
	}
}

func TestEnhanceParamsWithLLM_LLMFails_FallsBack(t *testing.T) {
	phase := &ExecutePhase{}
	deps := &Dependencies{
		Query: "Find dead code in the Flask helpers module",
		ParamExtractor: &mockParamExtractor{
			enabled:   true,
			returnErr: fmt.Errorf("LLM timeout"),
		},
	}
	regexResult := tools.FindDeadCodeParams{
		Package: "flask",
		Limit:   50,
	}

	defs := []tools.ToolDefinition{{
		Name: "find_dead_code",
		Parameters: map[string]tools.ParamDef{
			"package": {Type: "string", Description: "Package to scope to"},
		},
	}}

	result := phase.enhanceParamsWithLLM(
		context.Background(), deps, "find_dead_code", defs, regexResult,
	)

	// Should fallback to regex result
	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "flask" {
		t.Errorf("expected package=flask (fallback), got %s", fdcp.Package)
	}
}

func TestEnhanceParamsWithLLM_NoToolDef(t *testing.T) {
	phase := &ExecutePhase{}
	deps := &Dependencies{
		ParamExtractor: &mockParamExtractor{enabled: true},
	}
	regexResult := tools.FindDeadCodeParams{Package: "flask", Limit: 50}

	// Empty tool defs - tool not found
	result := phase.enhanceParamsWithLLM(
		context.Background(), deps, "find_dead_code", nil, regexResult,
	)

	// Should return regex result unchanged
	fdcp, ok := result.(tools.FindDeadCodeParams)
	if !ok {
		t.Fatalf("expected FindDeadCodeParams, got %T", result)
	}
	if fdcp.Package != "flask" {
		t.Errorf("expected package=flask (unchanged), got %s", fdcp.Package)
	}
}

// =============================================================================
// Helper conversion tests
// =============================================================================

func TestGetStringParam(t *testing.T) {
	params := map[string]any{"key": "value"}

	if got := getStringParam(params, "key", "default"); got != "value" {
		t.Errorf("expected value, got %s", got)
	}
	if got := getStringParam(params, "missing", "default"); got != "default" {
		t.Errorf("expected default, got %s", got)
	}
}

func TestGetBoolParam(t *testing.T) {
	params := map[string]any{"key": true, "str": "true", "str_false": "false"}

	if got := getBoolParam(params, "key", false); got != true {
		t.Error("expected true")
	}
	if got := getBoolParam(params, "missing", true); got != true {
		t.Error("expected default true")
	}
	if got := getBoolParam(params, "str", false); got != true {
		t.Error("expected string 'true' to parse as true")
	}
}

func TestGetIntParam(t *testing.T) {
	params := map[string]any{
		"float": float64(42),
		"int":   10,
	}

	if got := getIntParam(params, "float", 0); got != 42 {
		t.Errorf("expected 42 from float64, got %d", got)
	}
	if got := getIntParam(params, "int", 0); got != 10 {
		t.Errorf("expected 10 from int, got %d", got)
	}
	if got := getIntParam(params, "missing", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
}

func TestGetFloat64Param(t *testing.T) {
	params := map[string]any{
		"float": 1.5,
		"int":   2,
	}

	if got := getFloat64Param(params, "float", 0); got != 1.5 {
		t.Errorf("expected 1.5, got %f", got)
	}
	if got := getFloat64Param(params, "int", 0); got != 2.0 {
		t.Errorf("expected 2.0 from int, got %f", got)
	}
	if got := getFloat64Param(params, "missing", 3.14); got != 3.14 {
		t.Errorf("expected default 3.14, got %f", got)
	}
}
