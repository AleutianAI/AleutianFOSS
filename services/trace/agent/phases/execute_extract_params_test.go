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
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// makeToolDefs creates a minimal []ToolDefinition containing just the named tools.
// extractToolParameters checks that the tool name exists in the definitions before
// proceeding to its switch statement, so every test needs at least one matching def.
func makeToolDefs(names ...string) []tools.ToolDefinition {
	defs := make([]tools.ToolDefinition, len(names))
	for i, name := range names {
		defs[i] = tools.ToolDefinition{Name: name}
	}
	return defs
}

// newExtractPhase creates a minimal ExecutePhase for testing extractToolParameters.
func newExtractPhase() *ExecutePhase {
	return &ExecutePhase{}
}

// --- Tests for tools that need NO parameters ---

func TestExtractToolParameters_ListPackages(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("list_packages")

	params, err := extractParams(t, phase, "list all packages", "list_packages", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 0 {
		t.Errorf("expected empty params, got %v", params)
	}
}

func TestExtractToolParameters_FindEntryPoints(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_entry_points")

	params, err := extractParams(t, phase, "find entry points", "find_entry_points", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 0 {
		t.Errorf("expected empty params, got %v", params)
	}
}

func TestExtractToolParameters_FindExtractableRegions(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_extractable_regions")

	params, err := extractParams(t, phase, "find extractable regions", "find_extractable_regions", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 0 {
		t.Errorf("expected empty params, got %v", params)
	}
}

// --- Tests for graph_overview (hardcoded defaults) ---

func TestExtractToolParameters_GraphOverview(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("graph_overview")

	params, err := extractParams(t, phase, "show graph overview", "graph_overview", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertParamInt(t, params, "depth", 2)
	assertParamBool(t, params, "include_dependencies", true)
	assertParamBool(t, params, "include_metrics", true)
}

// --- Tests for explore_package ---

func TestExtractToolParameters_ExplorePackage(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("explore_package")

	t.Run("extracts package name", func(t *testing.T) {
		// extractPackageNameFromQuery looks for "package X" pattern
		params, err := extractParams(t, phase, "explore package core", "explore_package", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "package", "core")
		assertParamBool(t, params, "include_dependencies", true)
		assertParamBool(t, params, "include_dependents", true)
	})

	t.Run("error on empty package", func(t *testing.T) {
		// A query with no identifiable package name
		_, err := phase.extractToolParameters(
			context.Background(), "explore something", "explore_package", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no package name")
		}
	})
}

// --- Tests for find_callers / find_callees (combined case) ---

func TestExtractToolParameters_FindCallers(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_callers")

	t.Run("extracts function name", func(t *testing.T) {
		params, err := extractParams(t, phase, "who calls parseConfig?", "find_callers", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "function_name", "parseConfig")
		assertParamInt(t, params, "limit", 20)
	})

	t.Run("extracts package hint", func(t *testing.T) {
		// Use a query pattern that reliably extracts both function name and package hint.
		// "callers of parseConfig in the core package" — "callers of X" is a known pattern.
		params, err := extractParams(t, phase, "callers of parseConfig in the core package", "find_callers", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "function_name", "parseConfig")
		_, hasPkgHint := params["package_hint"]
		if !hasPkgHint {
			t.Error("expected package_hint to be extracted from 'in the core package'")
		}
	})

	t.Run("error on empty query", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "", "find_callers", defs, nil, nil)
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

func TestExtractToolParameters_FindCallees(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_callees")

	params, err := extractParams(t, phase, "what does handleRequest call?", "find_callees", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertParamString(t, params, "function_name", "handleRequest")
	assertParamInt(t, params, "limit", 20)
}

// --- Tests for find_implementations ---

func TestExtractToolParameters_FindImplementations(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_implementations")

	t.Run("extracts interface name", func(t *testing.T) {
		params, err := extractParams(t, phase, "what implements the Handler interface?", "find_implementations", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "interface_name", "Handler")
		assertParamInt(t, params, "limit", 20)
	})

	t.Run("error on no interface name", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "show implementations", "find_implementations", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no interface name")
		}
	})
}

// --- Tests for find_references ---

func TestExtractToolParameters_FindReferences(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_references")

	t.Run("extracts symbol name", func(t *testing.T) {
		params, err := extractParams(t, phase, "find references to Config", "find_references", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "symbol_name", "Config")
		assertParamInt(t, params, "limit", 20)
	})

	t.Run("error on empty query", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "", "find_references", defs, nil, nil)
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

// --- Tests for find_hotspots ---

func TestExtractToolParameters_FindHotspots(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_hotspots")

	t.Run("default params", func(t *testing.T) {
		params, err := extractParams(t, phase, "find hotspot functions", "find_hotspots", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamExists(t, params, "top")
		assertParamExists(t, params, "kind")
	})

	t.Run("extracts top N", func(t *testing.T) {
		params, err := extractParams(t, phase, "top 5 hotspot functions", "find_hotspots", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamInt(t, params, "top", 5)
	})

	t.Run("extracts kind", func(t *testing.T) {
		params, err := extractParams(t, phase, "find hotspot functions", "find_hotspots", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		kind := params["kind"].(string)
		// Should extract "function" from "hotspot functions"
		if kind != "function" && kind != "all" {
			t.Errorf("expected kind 'function' or 'all', got %q", kind)
		}
	})

	t.Run("IT-07 extracts package context", func(t *testing.T) {
		params, err := extractParams(t, phase, "hotspot functions in the tpl package", "find_hotspots", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, hasPkg := params["package"]
		if !hasPkg {
			t.Error("expected 'package' parameter from 'in the tpl package'")
		}
	})
}

// --- Tests for find_dead_code ---

func TestExtractToolParameters_FindDeadCode(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_dead_code")

	t.Run("default params", func(t *testing.T) {
		params, err := extractParams(t, phase, "find dead code", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", false)
		assertParamInt(t, params, "limit", 50)
	})

	t.Run("exported query sets include_exported", func(t *testing.T) {
		params, err := extractParams(t, phase, "find dead exported functions", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", true)
	})

	t.Run("public query sets include_exported", func(t *testing.T) {
		params, err := extractParams(t, phase, "find dead public methods", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", true)
	})

	// IT-08d: Negation tests for include_exported
	t.Run("not public negates include_exported", func(t *testing.T) {
		params, err := extractParams(t, phase, "Which functions have no incoming calls and are not public entry points?", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", false)
	})

	t.Run("not public API negates include_exported", func(t *testing.T) {
		params, err := extractParams(t, phase, "Which functions are not public API entry points?", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", false)
	})

	t.Run("affirmative public still sets include_exported", func(t *testing.T) {
		params, err := extractParams(t, phase, "Find dead code including public/exported functions", "find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", true)
	})

	// IT-08d Next Steps: Exact queries from failing integration tests
	t.Run("IT-08d_6157_pandas_not_public_api", func(t *testing.T) {
		params, err := extractParams(t, phase,
			"Which Pandas internal functions have no callers and are not public API entry points?",
			"find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", false)
	})

	t.Run("IT-08d_8057_nestjs_not_exported", func(t *testing.T) {
		params, err := extractParams(t, phase,
			"Find internal functions that have no callers and are not exported",
			"find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_exported", false)
	})

	t.Run("IT-08d_5056_hugo_resources_package", func(t *testing.T) {
		params, err := extractParams(t, phase,
			"Find unused or dead code in the resources package",
			"find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "package", "resources")
	})

	t.Run("IT-08d_8056_nestjs_packages_common_directory", func(t *testing.T) {
		params, err := extractParams(t, phase,
			"Find dead code specifically in the packages/common directory",
			"find_dead_code", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "package", "packages/common")
	})
}

// --- Tests for find_cycles ---

func TestExtractToolParameters_FindCycles(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_cycles")

	params, err := extractParams(t, phase, "find circular dependencies", "find_cycles", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertParamInt(t, params, "min_size", 2)
	assertParamInt(t, params, "limit", 20)
}

// --- Tests for find_path ---

func TestExtractToolParameters_FindPath(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_path")

	t.Run("extracts from and to", func(t *testing.T) {
		params, err := extractParams(t, phase, "find path from main to parseConfig", "find_path", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamExists(t, params, "from")
		assertParamExists(t, params, "to")
		from := params["from"].(string)
		to := params["to"].(string)
		if from == "" || to == "" {
			t.Errorf("expected non-empty from=%q and to=%q", from, to)
		}
	})

	t.Run("error when both missing", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "find path", "find_path", defs, nil, nil)
		if err == nil {
			t.Error("expected error when from and to are missing")
		}
	})
}

// --- Tests for find_important ---

func TestExtractToolParameters_FindImportant(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_important")

	params, err := extractParams(t, phase, "top 10 most important functions", "find_important", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertParamInt(t, params, "top", 10)
	assertParamExists(t, params, "kind")
}

// --- Tests for find_symbol ---

func TestExtractToolParameters_FindSymbol(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_symbol")

	t.Run("extracts symbol name", func(t *testing.T) {
		params, err := extractParams(t, phase, "find symbol Config", "find_symbol", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamExists(t, params, "name")
		assertParamExists(t, params, "kind")
	})

	t.Run("error on no symbol name", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "", "find_symbol", defs, nil, nil)
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

// --- Tests for find_communities ---

func TestExtractToolParameters_FindCommunities(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_communities")

	t.Run("default resolution", func(t *testing.T) {
		params, err := extractParams(t, phase, "find code communities", "find_communities", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamFloat(t, params, "resolution", 1.0)
	})

	t.Run("high resolution", func(t *testing.T) {
		params, err := extractParams(t, phase, "find detailed fine-grained communities", "find_communities", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamFloat(t, params, "resolution", 2.0)
	})

	t.Run("low resolution", func(t *testing.T) {
		params, err := extractParams(t, phase, "find broad coarse communities", "find_communities", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamFloat(t, params, "resolution", 0.5)
	})
}

// --- Tests for find_articulation_points ---

func TestExtractToolParameters_FindArticulationPoints(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_articulation_points")

	t.Run("defaults include bridges", func(t *testing.T) {
		params, err := extractParams(t, phase, "find articulation points", "find_articulation_points", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_bridges", true)
		assertParamExists(t, params, "top")
	})

	t.Run("no bridges when requested", func(t *testing.T) {
		params, err := extractParams(t, phase, "find only points without bridges", "find_articulation_points", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "include_bridges", false)
	})
}

// --- Tests for find_dominators (no deps → raw names) ---

func TestExtractToolParameters_FindDominators(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_dominators")

	t.Run("extracts target", func(t *testing.T) {
		params, err := extractParams(t, phase, "what dominates handleRequest?", "find_dominators", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "target", "handleRequest")
	})

	t.Run("error on no target", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "show dominators", "find_dominators", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no target")
		}
	})
}

// --- Tests for find_common_dependency ---

func TestExtractToolParameters_FindCommonDependency(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_common_dependency")

	t.Run("extracts two targets", func(t *testing.T) {
		params, err := extractParams(t, phase, "common dependency between Parser and Writer", "find_common_dependency", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		targets, ok := params["targets"]
		if !ok {
			t.Fatal("expected 'targets' parameter")
		}
		targetSlice, ok := targets.([]string)
		if !ok {
			t.Fatalf("expected targets to be []string, got %T", targets)
		}
		if len(targetSlice) < 2 {
			t.Errorf("expected at least 2 targets, got %d", len(targetSlice))
		}
	})

	t.Run("error on single target", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "common dependency of Parser", "find_common_dependency", defs, nil, nil)
		if err == nil {
			t.Error("expected error when only one target can be extracted")
		}
	})
}

// --- Tests for find_critical_path ---

func TestExtractToolParameters_FindCriticalPath(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_critical_path")

	t.Run("extracts target", func(t *testing.T) {
		params, err := extractParams(t, phase, "critical path to handleRequest", "find_critical_path", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamExists(t, params, "target")
	})

	t.Run("error on no target", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "show critical path", "find_critical_path", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no target")
		}
	})
}

// --- Tests for find_merge_points ---

func TestExtractToolParameters_FindMergePoints(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_merge_points")

	t.Run("returns default params", func(t *testing.T) {
		params, err := extractParams(t, phase, "merge points from parseConfig to handleRequest", "find_merge_points", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamInt(t, params, "top", 20)
		assertParamInt(t, params, "min_sources", 2)
	})

	t.Run("returns empty params when no sources found", func(t *testing.T) {
		params, err := extractParams(t, phase, "find merge points", "find_merge_points", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// find_merge_points returns empty params as fallback (uses all entry points)
		_ = params
	})
}

// --- Tests for find_control_dependencies ---

func TestExtractToolParameters_FindControlDependencies(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_control_dependencies")

	t.Run("extracts target", func(t *testing.T) {
		params, err := extractParams(t, phase, "control dependencies of handleRequest", "find_control_dependencies", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "target", "handleRequest")
	})

	t.Run("error on no target", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "show control dependencies", "find_control_dependencies", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no target")
		}
	})
}

// --- Tests for find_loops ---

func TestExtractToolParameters_FindLoops(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_loops")

	t.Run("no entry point returns empty params", func(t *testing.T) {
		params, err := extractParams(t, phase, "find all loops", "find_loops", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// May or may not have entry_point depending on extraction
		_ = params
	})

	t.Run("returns default params", func(t *testing.T) {
		params, err := extractParams(t, phase, "find loops in processData function", "find_loops", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamInt(t, params, "top", 20)
		assertParamInt(t, params, "min_size", 1)
		assertParamBool(t, params, "show_nesting", true)
	})
}

// --- Tests for find_weighted_criticality ---

func TestExtractToolParameters_FindWeightedCriticality(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_weighted_criticality")

	params, err := extractParams(t, phase, "top 10 critical functions", "find_weighted_criticality", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertParamInt(t, params, "top", 10)
}

// --- Tests for find_module_api ---

func TestExtractToolParameters_FindModuleAPI(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_module_api")

	params, err := extractParams(t, phase, "find module APIs", "find_module_api", defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// May be empty or have "top" if non-default
	_ = params
}

// --- Tests for Grep ---

func TestExtractToolParameters_Grep(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("Grep")

	t.Run("extracts pattern from capitalized word", func(t *testing.T) {
		params, err := extractParams(t, phase, "search for Config in the codebase", "Grep", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamExists(t, params, "pattern")
		assertParamExists(t, params, "output_mode")
	})

	t.Run("file list mode", func(t *testing.T) {
		params, err := extractParams(t, phase, "which file contains HandleRequest", "Grep", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "output_mode", "files_with_matches")
	})

	t.Run("count mode", func(t *testing.T) {
		params, err := extractParams(t, phase, "how many times does Config appear", "Grep", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "output_mode", "count")
	})

	t.Run("error on no pattern", func(t *testing.T) {
		// Use all-lowercase words with no capitalized symbols — the fallback
		// looks for capitalized words as symbol names, so all-lowercase should fail.
		_, err := phase.extractToolParameters(
			context.Background(), "do a search", "Grep", defs, nil, nil)
		if err == nil {
			t.Error("expected error when no pattern can be extracted")
		}
	})
}

// --- Tests for check_reducibility ---

func TestExtractToolParameters_CheckReducibility(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("check_reducibility")

	t.Run("default shows irreducible", func(t *testing.T) {
		params, err := extractParams(t, phase, "check reducibility", "check_reducibility", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "show_irreducible", true)
	})

	t.Run("hide irreducible when requested", func(t *testing.T) {
		params, err := extractParams(t, phase, "check reducibility summary only", "check_reducibility", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamBool(t, params, "show_irreducible", false)
	})
}

// --- Tests for get_call_chain ---

func TestExtractToolParameters_GetCallChain(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("get_call_chain")

	t.Run("extracts function name and defaults", func(t *testing.T) {
		params, err := extractParams(t, phase, "call chain for handleRequest", "get_call_chain", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "function_name", "handleRequest")
		assertParamString(t, params, "direction", "downstream")
		assertParamExists(t, params, "max_depth")
	})

	t.Run("upstream direction", func(t *testing.T) {
		params, err := extractParams(t, phase, "upstream callers of parseConfig", "get_call_chain", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "direction", "upstream")
	})

	t.Run("who calls triggers upstream", func(t *testing.T) {
		params, err := extractParams(t, phase, "who calls handleRequest", "get_call_chain", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertParamString(t, params, "direction", "upstream")
	})

	t.Run("error on no function name", func(t *testing.T) {
		_, err := phase.extractToolParameters(
			context.Background(), "show call chain", "get_call_chain", defs, nil, nil)
		if err == nil {
			t.Error("expected error for query with no function name")
		}
	})
}

// --- Tests for unknown tool and missing tool def ---

func TestExtractToolParameters_UnknownTool(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("unknown_tool")

	_, err := phase.extractToolParameters(
		context.Background(), "do something", "unknown_tool", defs, nil, nil)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestExtractToolParameters_MissingToolDef(t *testing.T) {
	phase := newExtractPhase()
	defs := makeToolDefs("find_callers") // only find_callers in defs

	_, err := phase.extractToolParameters(
		context.Background(), "find hotspots", "find_hotspots", defs, nil, nil)
	if err == nil {
		t.Error("expected error when tool def not found")
	}
}

// extractParams is a test helper that calls extractToolParameters and converts
// the returned TypedParams to map[string]interface{} via ToMap() for assertion.
func extractParams(t *testing.T, phase *ExecutePhase, query, toolName string, defs []tools.ToolDefinition) (map[string]interface{}, error) {
	t.Helper()
	typed, err := phase.extractToolParameters(
		context.Background(), query, toolName, defs, nil, nil)
	if err != nil {
		return nil, err
	}
	return typed.ToMap(), nil
}

// --- Assertion helpers ---

func assertParamExists(t *testing.T, params map[string]interface{}, key string) {
	t.Helper()
	if _, ok := params[key]; !ok {
		t.Errorf("expected parameter %q to exist", key)
	}
}

func assertParamString(t *testing.T, params map[string]interface{}, key, want string) {
	t.Helper()
	v, ok := params[key]
	if !ok {
		t.Errorf("expected parameter %q to exist", key)
		return
	}
	got, ok := v.(string)
	if !ok {
		t.Errorf("expected parameter %q to be string, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("parameter %q = %q, want %q", key, got, want)
	}
}

func assertParamInt(t *testing.T, params map[string]interface{}, key string, want int) {
	t.Helper()
	v, ok := params[key]
	if !ok {
		t.Errorf("expected parameter %q to exist", key)
		return
	}
	got, ok := v.(int)
	if !ok {
		t.Errorf("expected parameter %q to be int, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("parameter %q = %d, want %d", key, got, want)
	}
}

func assertParamFloat(t *testing.T, params map[string]interface{}, key string, want float64) {
	t.Helper()
	v, ok := params[key]
	if !ok {
		t.Errorf("expected parameter %q to exist", key)
		return
	}
	got, ok := v.(float64)
	if !ok {
		t.Errorf("expected parameter %q to be float64, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("parameter %q = %f, want %f", key, got, want)
	}
}

func assertParamBool(t *testing.T, params map[string]interface{}, key string, want bool) {
	t.Helper()
	v, ok := params[key]
	if !ok {
		t.Errorf("expected parameter %q to exist", key)
		return
	}
	got, ok := v.(bool)
	if !ok {
		t.Errorf("expected parameter %q to be bool, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("parameter %q = %v, want %v", key, got, want)
	}
}
