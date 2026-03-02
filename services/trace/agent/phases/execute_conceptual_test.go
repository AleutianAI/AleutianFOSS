// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package phases

import (
	"context"
	"fmt"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// IT-12: Conceptual Symbol Resolution Helper Tests
// =============================================================================

// TestTokenizeQueryKeywords_ConceptualQuery tests keyword extraction from
// conceptual queries that describe behavior rather than name functions.
func TestTokenizeQueryKeywords_ConceptualQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantAny  []string // at least these keywords should be present
		wantNone []string // these should NOT be present
	}{
		{
			name:     "material shader query",
			query:    "Show the call chain from assigning a material to a mesh through to shader compilation",
			wantAny:  []string{"material", "mesh", "shader", "compilation", "assign", "assigning"},
			wantNone: []string{"show", "the", "call", "chain", "from", "to"},
		},
		{
			name:     "rendering pipeline query",
			query:    "What is the rendering pipeline for scene objects?",
			wantAny:  []string{"render", "rendering", "pipeline", "scene", "objects"},
			wantNone: []string{"what", "the", "for"},
		},
		{
			name:    "simple function name query",
			query:   "What does render call?",
			wantAny: []string{"render"},
		},
		{
			name:    "strips punctuation",
			query:   "How does the binding subsystem handle validation?",
			wantAny: []string{"binding", "subsystem", "handle", "validation"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keywords := tokenizeQueryKeywords(tt.query)
			keywordSet := make(map[string]bool)
			for _, k := range keywords {
				keywordSet[k] = true
			}

			for _, want := range tt.wantAny {
				if !keywordSet[want] {
					t.Errorf("tokenizeQueryKeywords(%q) missing expected keyword %q (got: %v)",
						tt.query, want, keywords)
				}
			}

			for _, noWant := range tt.wantNone {
				if keywordSet[noWant] {
					t.Errorf("tokenizeQueryKeywords(%q) should not contain stop word %q (got: %v)",
						tt.query, noWant, keywords)
				}
			}
		})
	}
}

// TestTokenizeQueryKeywords_EmptyQuery returns empty slice.
func TestTokenizeQueryKeywords_EmptyQuery(t *testing.T) {
	keywords := tokenizeQueryKeywords("")
	if len(keywords) != 0 {
		t.Errorf("tokenizeQueryKeywords('') = %v, want empty", keywords)
	}
}

// TestSearchSymbolCandidates_FiltersNonCallable verifies that non-callable kinds
// (imports, variables, fields, constants, properties, interfaces, types,
// classes, structs) are filtered out, keeping only functions and methods.
func TestSearchSymbolCandidates_FiltersNonCallable(t *testing.T) {
	idx := index.NewSymbolIndex()
	// Add a method (should be kept)
	idx.Add(&ast.Symbol{
		ID: "mesh.ts:10:setMaterial", Name: "setMaterial",
		Kind: ast.SymbolKindMethod, FilePath: "mesh.ts",
		StartLine: 10, EndLine: 20, Language: "typescript",
	})
	// Add a function (should be kept)
	idx.Add(&ast.Symbol{
		ID: "utils.ts:1:getMesh", Name: "getMesh",
		Kind: ast.SymbolKindFunction, FilePath: "utils.ts",
		StartLine: 1, EndLine: 10, Language: "typescript",
	})
	// Non-callable kinds (all should be filtered out)
	idx.Add(&ast.Symbol{
		ID: "imports.ts:1:material_import", Name: "material",
		Kind: ast.SymbolKindImport, FilePath: "imports.ts",
		StartLine: 1, EndLine: 1, Language: "typescript",
	})
	idx.Add(&ast.Symbol{
		ID: "mesh.ts:5:meshType", Name: "Mesh",
		Kind: ast.SymbolKindClass, FilePath: "mesh.ts",
		StartLine: 5, EndLine: 100, Language: "typescript",
	})
	idx.Add(&ast.Symbol{
		ID: "vars.ts:3:materialVar", Name: "materialColor",
		Kind: ast.SymbolKindVariable, FilePath: "vars.ts",
		StartLine: 3, EndLine: 3, Language: "typescript",
	})
	idx.Add(&ast.Symbol{
		ID: "mesh.ts:1:MeshInterface", Name: "MeshInterface",
		Kind: ast.SymbolKindInterface, FilePath: "mesh.ts",
		StartLine: 1, EndLine: 10, Language: "typescript",
	})
	idx.Add(&ast.Symbol{
		ID: "mesh.ts:1:MeshType", Name: "MeshType",
		Kind: ast.SymbolKindType, FilePath: "mesh.ts",
		StartLine: 1, EndLine: 10, Language: "go",
	})
	idx.Add(&ast.Symbol{
		ID: "mesh.go:1:MeshStruct", Name: "MeshStruct",
		Kind: ast.SymbolKindStruct, FilePath: "mesh.go",
		StartLine: 1, EndLine: 10, Language: "go",
	})

	candidates := searchSymbolCandidates(context.Background(), idx, []string{"material", "mesh"}, 10)

	// Only setMaterial (method) and getMesh (function) should remain
	nonCallableKinds := map[string]bool{
		"import": true, "variable": true, "class": true,
		"interface": true, "type": true, "struct": true,
	}
	for _, c := range candidates {
		if nonCallableKinds[c.Kind] {
			t.Errorf("searchSymbolCandidates returned non-callable kind %q for %q", c.Kind, c.Name)
		}
	}

	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates (setMaterial, getMesh), got %d: %v", len(candidates), candidates)
	}
}

// TestSearchSymbolCandidates_NilIndex returns empty.
func TestSearchSymbolCandidates_NilIndex(t *testing.T) {
	candidates := searchSymbolCandidates(context.Background(), nil, []string{"material"}, 10)
	if len(candidates) != 0 {
		t.Errorf("searchSymbolCandidates(nil index) = %v, want empty", candidates)
	}
}

// TestSearchSymbolCandidates_EmptyKeywords returns empty.
func TestSearchSymbolCandidates_EmptyKeywords(t *testing.T) {
	idx := index.NewSymbolIndex()
	candidates := searchSymbolCandidates(context.Background(), idx, []string{}, 10)
	if len(candidates) != 0 {
		t.Errorf("searchSymbolCandidates(empty keywords) = %v, want empty", candidates)
	}
}

// TestSearchSymbolCandidates_Deduplication verifies that symbols found by
// multiple keywords are only returned once.
func TestSearchSymbolCandidates_Deduplication(t *testing.T) {
	idx := index.NewSymbolIndex()
	idx.Add(&ast.Symbol{
		ID: "mesh.ts:10:setMaterial", Name: "setMaterial",
		Kind: ast.SymbolKindMethod, FilePath: "mesh.ts",
		StartLine: 10, EndLine: 20, Language: "typescript",
	})

	// Search with keywords that would both match "setMaterial"
	candidates := searchSymbolCandidates(context.Background(), idx, []string{"setMaterial", "material"}, 10)

	nameCount := make(map[string]int)
	for _, c := range candidates {
		nameCount[c.Name]++
	}

	for name, count := range nameCount {
		if count > 1 {
			t.Errorf("searchSymbolCandidates returned duplicate candidate %q (%d times)", name, count)
		}
	}
}

// mockConceptualExtractor is a minimal ParamExtractor for testing resolveConceptualName.
type mockConceptualExtractor struct {
	enabled     bool
	resolveFunc func(ctx context.Context, query string, candidates []agent.SymbolCandidate) (string, error)
}

func (m *mockConceptualExtractor) IsEnabled() bool { return m.enabled }
func (m *mockConceptualExtractor) ExtractParams(_ context.Context, _ string, _ string,
	_ []agent.ParamExtractorSchema, _ map[string]any) (map[string]any, error) {
	return nil, nil
}
func (m *mockConceptualExtractor) ResolveConceptualSymbol(ctx context.Context, query string,
	candidates []agent.SymbolCandidate, tier0Count, tier1Count int, sourceContext string) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, query, candidates)
	}
	return "", nil
}

// TestResolveConceptualName_CallableAwareExit verifies IT-12 Rev 4: when a name
// exists in the index but ONLY as non-callable kinds (struct, type, interface),
// resolveConceptualName should NOT exit early and should continue to LLM resolution.
func TestResolveConceptualName_CallableAwareExit(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	// "Site" exists as a struct only (no callable matches)
	siteStruct := &ast.Symbol{
		ID:        "hugolib/site.go:91:Site",
		Name:      "Site",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "hugolib/site.go",
		StartLine: 91,
		EndLine:   200,
		Package:   "hugolib",
		Exported:  true,
		Language:  "go",
	}
	// "newHugoSites" exists as a function (better for call chains)
	newHugoSites := &ast.Symbol{
		ID:        "hugolib/hugo_sites.go:20:newHugoSites",
		Name:      "newHugoSites",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "hugolib/hugo_sites.go",
		StartLine: 20,
		EndLine:   50,
		Package:   "hugolib",
		Exported:  false,
		Language:  "go",
	}
	for _, sym := range []*ast.Symbol{siteStruct, newHugoSites} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	t.Run("name_only_non_callable_continues_to_LLM", func(t *testing.T) {
		// When "Site" is only a struct, resolveConceptualName should NOT return
		// "Site" early. It should proceed to LLM resolution which picks "newHugoSites".
		extractor := &mockConceptualExtractor{
			enabled: true,
			resolveFunc: func(_ context.Context, _ string, candidates []agent.SymbolCandidate) (string, error) {
				// The LLM picks the best function from candidates
				return "newHugoSites", nil
			},
		}
		result := resolveConceptualName(ctx, "Site", "call chain from site initialization", idx, extractor, nil, conceptualResolutionOpts{})
		if result.Resolved == "Site" {
			t.Errorf("IT-12 Rev 4: resolveConceptualName should NOT return 'Site' when only non-callable matches exist, got %q", result.Resolved)
		}
		if result.Resolved != "newHugoSites" {
			t.Errorf("expected 'newHugoSites' from LLM resolution, got %q", result.Resolved)
		}
	})

	t.Run("name_with_callable_exits_early", func(t *testing.T) {
		// Add a function named "Site" — now the name has callable matches
		siteFunc := &ast.Symbol{
			ID:        "hugolib/site.go:536:Site",
			Name:      "Site",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "hugolib/site.go",
			StartLine: 536,
			EndLine:   540,
			Package:   "hugolib",
			Exported:  true,
			Language:  "go",
		}
		if err := idx.Add(siteFunc); err != nil {
			t.Fatalf("Failed to add %s: %v", siteFunc.ID, err)
		}

		extractor := &mockConceptualExtractor{
			enabled: true,
			resolveFunc: func(_ context.Context, _ string, _ []agent.SymbolCandidate) (string, error) {
				t.Error("IT-12 Rev 4: ResolveConceptualSymbol should NOT be called when callable matches exist")
				return "should_not_be_used", nil
			},
		}
		result := resolveConceptualName(ctx, "Site", "call chain from site initialization", idx, extractor, nil, conceptualResolutionOpts{})
		if result.Resolved != "Site" {
			t.Errorf("expected 'Site' (early exit because callable match exists), got %q", result.Resolved)
		}
	})
}

func TestExpandConceptSynonyms(t *testing.T) {
	t.Run("expands initialization to verb forms", func(t *testing.T) {
		result := expandConceptSynonyms([]string{"site", "initialization"})
		// Should contain original keywords plus synonyms
		has := make(map[string]bool)
		for _, kw := range result {
			has[kw] = true
		}
		if !has["site"] {
			t.Error("missing original keyword 'site'")
		}
		if !has["initialization"] {
			t.Error("missing original keyword 'initialization'")
		}
		for _, expected := range []string{"init", "new", "build", "setup", "create"} {
			if !has[expected] {
				t.Errorf("missing synonym %q for 'initialization'", expected)
			}
		}
	})

	t.Run("no duplicates in output", func(t *testing.T) {
		result := expandConceptSynonyms([]string{"build", "creation"})
		seen := make(map[string]int)
		for _, kw := range result {
			seen[kw]++
			if seen[kw] > 1 {
				t.Errorf("duplicate keyword %q in result", kw)
			}
		}
		// "build" appears in input and in "creation" synonyms — should only appear once
		if seen["build"] != 1 {
			t.Errorf("expected 'build' exactly once, got %d", seen["build"])
		}
	})

	t.Run("no expansion for unknown words", func(t *testing.T) {
		result := expandConceptSynonyms([]string{"menu", "frobnicator"})
		if len(result) != 2 {
			t.Errorf("expected 2 keywords (no expansion), got %d: %v", len(result), result)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		result := expandConceptSynonyms(nil)
		if len(result) != 0 {
			t.Errorf("expected empty result, got %v", result)
		}
	})
}

// TestExtractDomainNouns verifies that extractDomainNouns correctly identifies
// tokens that are NOT concept synonym keys (i.e., domain-specific nouns).
func TestExtractDomainNouns(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   []string
	}{
		{
			name:   "menu assembly - menu is domain noun",
			tokens: []string{"menu", "assembly"},
			want:   []string{"menu"},
		},
		{
			name:   "site initialization - site is domain noun",
			tokens: []string{"site", "initialization"},
			want:   []string{"site"},
		},
		{
			name:   "axis rendering - axis is domain noun",
			tokens: []string{"axis", "rendering"},
			want:   []string{"axis"},
		},
		{
			name:   "error handling - error is domain noun",
			tokens: []string{"error", "handling"},
			want:   []string{"error"},
		},
		{
			name:   "initialization only - all concept keys",
			tokens: []string{"initialization"},
			want:   nil,
		},
		{
			name:   "shader compilation - shader is domain noun",
			tokens: []string{"shader", "compilation"},
			want:   []string{"shader"},
		},
		{
			name:   "page rendering - render stripped from rendering is concept root",
			tokens: []string{"page", "render"},
			want:   []string{"page"},
		},
		{
			name:   "page rendering full tokens - both forms filtered",
			tokens: []string{"page", "rendering", "render"},
			want:   []string{"page"},
		},
		{
			name:   "handl stripped from handling is concept root",
			tokens: []string{"error", "handl"},
			want:   []string{"error"},
		},
		{
			name:   "empty input returns empty",
			tokens: nil,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomainNouns(tt.tokens)
			if len(got) != len(tt.want) {
				t.Errorf("extractDomainNouns(%v) = %v, want %v", tt.tokens, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractDomainNouns(%v)[%d] = %q, want %q", tt.tokens, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestExtractConceptValues verifies that concept synonym values are extracted
// for concept keys found in name tokens (including -ing reconstituted forms).
func TestExtractConceptValues(t *testing.T) {
	t.Run("rendering token produces render in values", func(t *testing.T) {
		values := extractConceptValues([]string{"page", "rendering"})
		has := make(map[string]bool)
		for _, v := range values {
			has[v] = true
		}
		if !has["render"] {
			t.Errorf("expected 'render' in concept values, got %v", values)
		}
	})

	t.Run("render token reconstitutes to rendering key", func(t *testing.T) {
		values := extractConceptValues([]string{"page", "render"})
		has := make(map[string]bool)
		for _, v := range values {
			has[v] = true
		}
		if !has["render"] {
			t.Errorf("expected 'render' in concept values via rendering key, got %v", values)
		}
	})

	t.Run("assembly token produces assemble in values", func(t *testing.T) {
		values := extractConceptValues([]string{"menu", "assembly"})
		has := make(map[string]bool)
		for _, v := range values {
			has[v] = true
		}
		if !has["assemble"] {
			t.Errorf("expected 'assemble' in concept values, got %v", values)
		}
	})

	t.Run("no concept tokens returns empty", func(t *testing.T) {
		values := extractConceptValues([]string{"page", "menu"})
		if len(values) != 0 {
			t.Errorf("expected empty concept values for non-concept tokens, got %v", values)
		}
	})

	t.Run("no duplicates", func(t *testing.T) {
		// Both "rendering" and "render" map to the same key's values
		values := extractConceptValues([]string{"rendering", "render"})
		seen := make(map[string]int)
		for _, v := range values {
			seen[v]++
			if seen[v] > 1 {
				t.Errorf("duplicate value %q in concept values", v)
			}
		}
	})
}

// TestCandidateTier verifies three-tier assignment based on domain noun and concept value matching.
func TestCandidateTier(t *testing.T) {
	tests := []struct {
		name          string
		candidate     agent.SymbolCandidate
		domainNouns   []string
		conceptValues []string
		wantTier      int
	}{
		{
			name:          "renderPages with page+render → tier 0 (domain+concept)",
			candidate:     agent.SymbolCandidate{Name: "renderPages"},
			domainNouns:   []string{"page"},
			conceptValues: []string{"render", "draw", "paint"},
			wantTier:      0,
		},
		{
			name:          "assembleMenus with menu+assemble → tier 0 (domain+concept)",
			candidate:     agent.SymbolCandidate{Name: "assembleMenus"},
			domainNouns:   []string{"menu"},
			conceptValues: []string{"assemble", "build", "compose"},
			wantTier:      0,
		},
		{
			name:          "Page with page but no concept match → tier 1 (domain only)",
			candidate:     agent.SymbolCandidate{Name: "Page"},
			domainNouns:   []string{"page"},
			conceptValues: []string{"render", "draw", "paint"},
			wantTier:      1,
		},
		{
			name:          "menuEntries with menu but no concept match → tier 1 (domain only)",
			candidate:     agent.SymbolCandidate{Name: "menuEntries"},
			domainNouns:   []string{"menu"},
			conceptValues: []string{"assemble", "build"},
			wantTier:      1,
		},
		{
			name:          "Build with concept match only → tier 1 (concept synonym)",
			candidate:     agent.SymbolCandidate{Name: "Build"},
			domainNouns:   []string{"menu"},
			conceptValues: []string{"assemble", "build"},
			wantTier:      1,
		},
		{
			name:          "Render with concept match only → tier 1 (concept synonym)",
			candidate:     agent.SymbolCandidate{Name: "Render"},
			domainNouns:   []string{"page"},
			conceptValues: []string{"render", "draw"},
			wantTier:      1,
		},
		{
			name:          "validate for data validation → tier 1 (concept synonym match)",
			candidate:     agent.SymbolCandidate{Name: "validate"},
			domainNouns:   []string{"data"},
			conceptValues: []string{"validate", "check", "verify"},
			wantTier:      1,
		},
		{
			name:          "ValidateStruct for data validation → tier 1 (concept substring)",
			candidate:     agent.SymbolCandidate{Name: "ValidateStruct"},
			domainNouns:   []string{"data"},
			conceptValues: []string{"validate", "check", "verify"},
			wantTier:      1,
		},
		{
			name:          "Bind has no domain or concept match → tier 2",
			candidate:     agent.SymbolCandidate{Name: "Bind"},
			domainNouns:   []string{"data"},
			conceptValues: []string{"validate", "check", "verify"},
			wantTier:      2,
		},
		{
			name:          "empty domain nouns → tier 2 (no regression)",
			candidate:     agent.SymbolCandidate{Name: "assembleMenus"},
			domainNouns:   nil,
			conceptValues: []string{"assemble"},
			wantTier:      2,
		},
		{
			name:          "short noun log does not boost catalogBuilder",
			candidate:     agent.SymbolCandidate{Name: "catalogBuilder"},
			domainNouns:   []string{"log"},
			conceptValues: []string{"build"},
			wantTier:      1, // "build" concept matches "catalogBuilder" via substring
		},
		{
			name:          "short noun api does not boost apiHandler",
			candidate:     agent.SymbolCandidate{Name: "apiHandler"},
			domainNouns:   []string{"api"},
			conceptValues: []string{"handle"},
			wantTier:      1, // "handle" concept matches "apiHandler" via substring
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := candidateTier(tt.candidate, tt.domainNouns, tt.conceptValues)
			if got != tt.wantTier {
				t.Errorf("candidateTier(%q, domainNouns=%v, conceptValues=%v) = %d, want %d",
					tt.candidate.Name, tt.domainNouns, tt.conceptValues, got, tt.wantTier)
			}
		})
	}
}

// TestResolveConceptualName_DomainNounBoosting verifies the end-to-end behavior
// of domain noun boosting in resolveConceptualName. assembleMenus (8 edges, contains
// "menu") must rank above Build (55 edges, synonym-only match) for "menu assembly".
func TestResolveConceptualName_DomainNounBoosting(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	// assembleMenus: a method with 8 edges — contains "menu" domain noun
	assembleMenus := &ast.Symbol{
		ID:        "hugolib/menu.go:50:assembleMenus",
		Name:      "assembleMenus",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "hugolib/menu.go",
		StartLine: 50,
		EndLine:   100,
		Package:   "hugolib",
		Exported:  false,
		Language:  "go",
	}
	// Build: a function with 55 edges — matches via "build" synonym of "assembly"
	build := &ast.Symbol{
		ID:        "hugolib/hugo_sites.go:100:Build",
		Name:      "Build",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "hugolib/hugo_sites.go",
		StartLine: 100,
		EndLine:   200,
		Package:   "hugolib",
		Exported:  true,
		Language:  "go",
	}
	for _, sym := range []*ast.Symbol{assembleMenus, build} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	t.Run("domain_noun_beats_high_edge_count", func(t *testing.T) {
		// The mock extractor returns the FIRST candidate's name,
		// simulating positional bias in small LLMs.
		extractor := &mockConceptualExtractor{
			enabled: true,
			resolveFunc: func(_ context.Context, _ string, candidates []agent.SymbolCandidate) (string, error) {
				if len(candidates) == 0 {
					return "", nil
				}
				// Return the first candidate (simulates positional bias)
				return candidates[0].Name, nil
			},
		}
		result := resolveConceptualName(ctx, "menu assembly", "Show the call chain from site initialization to menu assembly", idx, extractor, nil, conceptualResolutionOpts{})
		if result.Resolved != "assembleMenus" {
			t.Errorf("IT-12 Rev 5: domain noun boosting should rank assembleMenus above Build for 'menu assembly', got %q", result.Resolved)
		}
	})

	t.Run("no_domain_noun_uses_edge_count", func(t *testing.T) {
		// "initialization" is all concept keys → no domain nouns → pure edge count sort.
		// Inject graph analytics so Build (55 edges) ranks above Init (15 edges).
		idx2 := index.NewSymbolIndex()
		buildSym2 := &ast.Symbol{
			ID:        "hugolib/hugo_sites.go:100:Build",
			Name:      "Build",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "hugolib/hugo_sites.go",
			StartLine: 100,
			EndLine:   200,
			Package:   "hugolib",
			Exported:  true,
			Language:  "go",
		}
		initSym := &ast.Symbol{
			ID:        "hugolib/hugo_sites.go:10:Init",
			Name:      "Init",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "hugolib/hugo_sites.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "hugolib",
			Exported:  true,
			Language:  "go",
		}
		for _, sym := range []*ast.Symbol{buildSym2, initSym} {
			if err := idx2.Add(sym); err != nil {
				t.Fatalf("Failed to add %s: %v", sym.ID, err)
			}
		}

		// Build a graph with edge counts: Build gets 55 edges, Init gets 15.
		g := graph.NewGraph("/project")
		// Add all nodes first (need dummy targets for edges).
		for _, sym := range []*ast.Symbol{buildSym2, initSym} {
			if _, err := g.AddNode(sym); err != nil {
				t.Fatalf("AddNode %s: %v", sym.ID, err)
			}
		}
		// Add dummy target nodes for edges.
		dummySymbols := make([]*ast.Symbol, 55)
		for i := range dummySymbols {
			dummySymbols[i] = &ast.Symbol{
				ID:        fmt.Sprintf("hugolib/dummy.go:%d:dummy%d", i+1, i),
				Name:      fmt.Sprintf("dummy%d", i),
				Kind:      ast.SymbolKindFunction,
				FilePath:  "hugolib/dummy.go",
				StartLine: i + 1,
				EndLine:   i + 2,
				Package:   "hugolib",
				Language:  "go",
			}
			if _, err := g.AddNode(dummySymbols[i]); err != nil {
				t.Fatalf("AddNode dummy%d: %v", i, err)
			}
		}
		// Build: 47 outgoing + 8 incoming = 55 total
		for i := 0; i < 47; i++ {
			if err := g.AddEdge(buildSym2.ID, dummySymbols[i].ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
				t.Fatalf("AddEdge out %d: %v", i, err)
			}
		}
		for i := 47; i < 55; i++ {
			if err := g.AddEdge(dummySymbols[i].ID, buildSym2.ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
				t.Fatalf("AddEdge in %d: %v", i, err)
			}
		}
		// Init: 12 outgoing + 3 incoming = 15 total
		for i := 0; i < 12; i++ {
			if err := g.AddEdge(initSym.ID, dummySymbols[i].ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
				t.Fatalf("AddEdge init out %d: %v", i, err)
			}
		}
		for i := 12; i < 15; i++ {
			if err := g.AddEdge(dummySymbols[i].ID, initSym.ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
				t.Fatalf("AddEdge init in %d: %v", i, err)
			}
		}
		g.Freeze()
		hg, err := graph.WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph: %v", err)
		}
		ga := graph.NewGraphAnalytics(hg)

		extractor := &mockConceptualExtractor{
			enabled: true,
			resolveFunc: func(_ context.Context, _ string, candidates []agent.SymbolCandidate) (string, error) {
				if len(candidates) == 0 {
					return "", nil
				}
				// Return first candidate (simulates positional bias)
				return candidates[0].Name, nil
			},
		}
		// "initialization" → all concept keys → domainNouns = [] → all tier 1 → pure edge count
		// Build (55 edges) should be sorted above Init (15 edges)
		result := resolveConceptualName(ctx, "initialization", "call chain from initialization", idx2, extractor, nil, conceptualResolutionOpts{Analytics: ga})
		if result.Resolved != "Build" {
			t.Errorf("IT-12 Rev 5: with no domain nouns, edge-count sort should pick Build (55 edges) over Init (15 edges), got %q", result.Resolved)
		}
	})
}

// TestResolveConceptualName_DotNotationPreserved verifies IT-R2d: when a
// dot-notation name like "Scene.constructor" is passed, resolveConceptualName
// must return the ORIGINAL dot-notation name (not strip it to "constructor").
// The tool-side ResolveFunctionCandidates handles dot-notation correctly via
// resolveTypeDotMethod(Type, Method), which uses Receiver filtering. Stripping
// the type prefix loses disambiguation context.
func TestResolveConceptualName_DotNotationPreserved(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	// Add multiple "constructor" symbols with different Receivers
	sceneConstructor := &ast.Symbol{
		ID:        "scene.ts:2006:constructor",
		Name:      "constructor",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "scene.ts",
		StartLine: 2006,
		EndLine:   2100,
		Receiver:  "Scene",
		Language:  "typescript",
	}
	nodeConstructor := &ast.Symbol{
		ID:        "node.ts:384:constructor",
		Name:      "constructor",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "node.ts",
		StartLine: 384,
		EndLine:   420,
		Receiver:  "Node",
		Language:  "typescript",
	}
	for _, sym := range []*ast.Symbol{sceneConstructor, nodeConstructor} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	extractor := &mockConceptualExtractor{enabled: true}

	t.Run("dot_notation_preserved_not_stripped", func(t *testing.T) {
		// "Scene.constructor" should return "Scene.constructor" (preserving the
		// type prefix), NOT "constructor" (which would lose disambiguation).
		result := resolveConceptualName(ctx, "Scene.constructor",
			"Find path from Scene.constructor to render", idx, extractor, nil, conceptualResolutionOpts{})
		if result.Resolved != "Scene.constructor" {
			t.Errorf("IT-R2d: resolveConceptualName should preserve dot-notation 'Scene.constructor', got %q", result.Resolved)
		}
	})

	t.Run("bare_method_still_returns_bare", func(t *testing.T) {
		// A bare "constructor" (no dot) with callable matches should return as-is.
		result := resolveConceptualName(ctx, "constructor",
			"Find path from constructor to render", idx, extractor, nil, conceptualResolutionOpts{})
		if result.Resolved != "constructor" {
			t.Errorf("bare name 'constructor' should return unchanged, got %q", result.Resolved)
		}
	})
}

// TestPruneCandidatesByTier verifies D3 pre-LLM pruning rules.
func TestPruneCandidatesByTier(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	t.Run("keeps_all_tier0", func(t *testing.T) {
		// 5 tier0 candidates — all should be kept
		candidates := make([]agent.SymbolCandidate, 0, 25)
		for i := 0; i < 5; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("updateScene%d", i), Kind: "function",
				FilePath: "scene.go", Line: i * 10, OutEdges: 10,
			})
		}
		// 15 tier2 candidates
		for i := 0; i < 15; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("unrelated%d", i), Kind: "function",
				FilePath: "other.go", Line: i * 10,
			})
		}
		result := pruneCandidatesByTier(candidates, domainNouns, conceptValues)
		// Should have all 5 tier0 + capped tier1 (0) + capped tier2
		tier0Count := 0
		for _, c := range result {
			if candidateTier(c, domainNouns, conceptValues) == 0 {
				tier0Count++
			}
		}
		if tier0Count != 5 {
			t.Errorf("expected all 5 tier0 to be kept, got %d", tier0Count)
		}
	})

	t.Run("caps_tier1_based_on_tier0_count", func(t *testing.T) {
		// 1 tier0, 15 tier1 → tier1 capped at 10
		candidates := []agent.SymbolCandidate{
			{Name: "updateScene", Kind: "function", FilePath: "s.go", Line: 1, OutEdges: 10},
		}
		for i := 0; i < 15; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("scene%d", i), Kind: "function",
				FilePath: "s.go", Line: i * 10, OutEdges: 5,
			})
		}
		result := pruneCandidatesByTier(candidates, domainNouns, conceptValues)
		// 1 tier0 + 10 tier1 = 11
		if len(result) != 11 {
			t.Errorf("expected 11 candidates (1 tier0 + 10 tier1), got %d", len(result))
		}
	})

	t.Run("tier2_dropped_when_enough_higher_tiers", func(t *testing.T) {
		// 5 tier0, 5 tier1, 10 tier2 → tier2 dropped (tier0+tier1 >= 8)
		candidates := make([]agent.SymbolCandidate, 0)
		for i := 0; i < 5; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("updateScene%d", i), Kind: "function",
				FilePath: "s.go", Line: i, OutEdges: 10,
			})
		}
		for i := 0; i < 5; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("sceneOnly%d", i), Kind: "function",
				FilePath: "s.go", Line: 100 + i, OutEdges: 5,
			})
		}
		for i := 0; i < 10; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("unrelated%d", i), Kind: "function",
				FilePath: "s.go", Line: 200 + i,
			})
		}
		result := pruneCandidatesByTier(candidates, domainNouns, conceptValues)
		for _, c := range result {
			if candidateTier(c, domainNouns, conceptValues) == 2 {
				t.Error("tier2 candidates should be dropped when tier0+tier1 >= 8")
				break
			}
		}
	})

	t.Run("hard_cap_20", func(t *testing.T) {
		// 25 tier0 → hard cap at 20
		candidates := make([]agent.SymbolCandidate, 0, 25)
		for i := 0; i < 25; i++ {
			candidates = append(candidates, agent.SymbolCandidate{
				Name: fmt.Sprintf("updateScene%d", i), Kind: "function",
				FilePath: "s.go", Line: i, OutEdges: 10,
			})
		}
		result := pruneCandidatesByTier(candidates, domainNouns, conceptValues)
		if len(result) > 20 {
			t.Errorf("hard cap should limit to 20, got %d", len(result))
		}
	})
}

// TestPruneCandidatesByTier_SparseTiers verifies tier2 kept when higher tiers are sparse.
func TestPruneCandidatesByTier_SparseTiers(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	// 2 tier0, 3 tier1 (total 5 < 8), 10 tier2 → tier2 kept (max 5)
	candidates := []agent.SymbolCandidate{
		{Name: "updateScene1", Kind: "function", FilePath: "s.go", Line: 1, OutEdges: 10},
		{Name: "updateScene2", Kind: "function", FilePath: "s.go", Line: 2, OutEdges: 8},
		{Name: "sceneA", Kind: "function", FilePath: "s.go", Line: 10, OutEdges: 5},
		{Name: "sceneB", Kind: "function", FilePath: "s.go", Line: 11, OutEdges: 4},
		{Name: "sceneC", Kind: "function", FilePath: "s.go", Line: 12, OutEdges: 3},
	}
	for i := 0; i < 10; i++ {
		candidates = append(candidates, agent.SymbolCandidate{
			Name: fmt.Sprintf("other%d", i), Kind: "function",
			FilePath: "s.go", Line: 100 + i,
		})
	}
	result := pruneCandidatesByTier(candidates, domainNouns, conceptValues)
	tier2Count := 0
	for _, c := range result {
		if candidateTier(c, domainNouns, conceptValues) == 2 {
			tier2Count++
		}
	}
	if tier2Count == 0 {
		t.Error("tier2 should be kept when tier0+tier1 < 8")
	}
	if tier2Count > 5 {
		t.Errorf("tier2 should be capped at 5, got %d", tier2Count)
	}
	// Total: 2 tier0 + 3 tier1 + 5 tier2 = 10
	if len(result) != 10 {
		t.Errorf("expected 10 candidates, got %d", len(result))
	}
}

// TestValidateTierSelection_Override verifies D3 post-LLM override logic.
func TestValidateTierSelection_Override(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	candidates := []agent.SymbolCandidate{
		{Name: "updateSceneBounds", Kind: "function", FilePath: "s.go", Line: 1, OutEdges: 10, InEdges: 5},
		{Name: "createGraph", Kind: "function", FilePath: "g.go", Line: 1, OutEdges: 20, InEdges: 15},
	}
	validated, overridden := validateTierSelection("createGraph", candidates, domainNouns, conceptValues)
	if !overridden {
		t.Error("expected override: LLM picked tier1 but tier0 with >= 3 edges exists")
	}
	if validated != "updateSceneBounds" {
		t.Errorf("expected override to 'updateSceneBounds', got %q", validated)
	}
}

// TestValidateTierSelection_Accept verifies tier0 LLM pick is accepted.
func TestValidateTierSelection_Accept(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	candidates := []agent.SymbolCandidate{
		{Name: "updateSceneBounds", Kind: "function", FilePath: "s.go", Line: 1, OutEdges: 10},
		{Name: "createGraph", Kind: "function", FilePath: "g.go", Line: 1, OutEdges: 20},
	}
	validated, overridden := validateTierSelection("updateSceneBounds", candidates, domainNouns, conceptValues)
	if overridden {
		t.Error("should NOT override when LLM picked tier0")
	}
	if validated != "updateSceneBounds" {
		t.Errorf("expected 'updateSceneBounds', got %q", validated)
	}
}

// TestValidateTierSelection_Tier2OverriddenByTier1 verifies that when the LLM picks a tier2
// candidate and a tier1 concept-match candidate exists, the validator overrides.
// This is the test 5251 pattern: LLM picks "Bind" (tier2) over "validate" (tier1 concept match).
func TestValidateTierSelection_Tier2OverriddenByTier1(t *testing.T) {
	domainNouns := []string{"data"}
	conceptValues := []string{"validate", "check", "verify"}

	candidates := []agent.SymbolCandidate{
		{Name: "chooseData", Kind: "function", FilePath: "d.go", Line: 1, OutEdges: 1, InEdges: 7},
		{Name: "validate", Kind: "function", FilePath: "v.go", Line: 1, OutEdges: 1, InEdges: 27},
		{Name: "Bind", Kind: "method", FilePath: "c.go", Line: 1, OutEdges: 0, InEdges: 88},
	}
	validated, overridden := validateTierSelection("Bind", candidates, domainNouns, conceptValues)
	if !overridden {
		t.Error("expected override: LLM picked tier2 (Bind) but tier1 with sufficient edges exists (chooseData)")
	}
	// chooseData is first tier1 candidate in list order
	if validated != "chooseData" {
		t.Errorf("expected override to 'chooseData' (first tier1), got %q", validated)
	}
}

// TestValidateTierSelection_Tier2AcceptedNoTier1 verifies that when only tier2
// candidates exist, the LLM pick is accepted.
func TestValidateTierSelection_Tier2AcceptedNoTier1(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	// Neither candidate matches domain or concept for "scene"/"update"
	candidates := []agent.SymbolCandidate{
		{Name: "buildProject", Kind: "function", FilePath: "p.go", Line: 1, OutEdges: 30},
		{Name: "renderAll", Kind: "function", FilePath: "r.go", Line: 1, OutEdges: 20},
	}
	validated, overridden := validateTierSelection("buildProject", candidates, domainNouns, conceptValues)
	if overridden {
		t.Error("should NOT override when no tier0 or tier1 candidates exist")
	}
	if validated != "buildProject" {
		t.Errorf("expected 'buildProject', got %q", validated)
	}
}

// TestValidateTierSelection_TrivialTier0 verifies LLM pick accepted when tier0 has < 3 edges.
func TestValidateTierSelection_TrivialTier0(t *testing.T) {
	domainNouns := []string{"scene"}
	conceptValues := []string{"update"}

	candidates := []agent.SymbolCandidate{
		{Name: "updateSceneTag", Kind: "function", FilePath: "s.go", Line: 1, OutEdges: 1, InEdges: 0},
		{Name: "createGraph", Kind: "function", FilePath: "g.go", Line: 1, OutEdges: 20, InEdges: 15},
	}
	validated, overridden := validateTierSelection("createGraph", candidates, domainNouns, conceptValues)
	if overridden {
		t.Error("should NOT override when tier0 has < 3 OutEdges (trivial getter)")
	}
	if validated != "createGraph" {
		t.Errorf("expected 'createGraph', got %q", validated)
	}
}

// =============================================================================
// D3c: New Tests
// =============================================================================

// TestConceptExactnessScore verifies the scoring function for concept synonym matching.
func TestConceptExactnessScore(t *testing.T) {
	conceptValues := []string{"validate", "check", "verify"}

	tests := []struct {
		name      string
		candidate agent.SymbolCandidate
		wantScore int
	}{
		{
			name:      "exact match returns 3",
			candidate: agent.SymbolCandidate{Name: "validate"},
			wantScore: 3,
		},
		{
			name:      "exact match case insensitive",
			candidate: agent.SymbolCandidate{Name: "Validate"},
			wantScore: 3,
		},
		{
			name:      "prefix match returns 2",
			candidate: agent.SymbolCandidate{Name: "validateHeader"},
			wantScore: 2,
		},
		{
			name:      "contains match returns 1",
			candidate: agent.SymbolCandidate{Name: "revalidateForm"},
			wantScore: 1,
		},
		{
			name:      "no match returns 0",
			candidate: agent.SymbolCandidate{Name: "Bind"},
			wantScore: 0,
		},
		{
			name:      "short concept values skipped",
			candidate: agent.SymbolCandidate{Name: "abc"},
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conceptExactnessScore(tt.candidate, conceptValues)
			if got != tt.wantScore {
				t.Errorf("conceptExactnessScore(%q, %v) = %d, want %d",
					tt.candidate.Name, conceptValues, got, tt.wantScore)
			}
		})
	}

	t.Run("short concept values skipped entirely", func(t *testing.T) {
		shortValues := []string{"is", "do", "go"}
		got := conceptExactnessScore(agent.SymbolCandidate{Name: "is"}, shortValues)
		if got != 0 {
			t.Errorf("short concept values should be skipped, got %d", got)
		}
	})
}

// TestAutoPickExactConceptMatch verifies the auto-pick logic for exact concept synonyms.
func TestAutoPickExactConceptMatch(t *testing.T) {
	conceptValues := []string{"validate", "check", "verify"}
	domainNouns := []string{"data"}

	t.Run("single exact match auto-picked", func(t *testing.T) {
		candidates := []agent.SymbolCandidate{
			{Name: "validate", Kind: "function", FilePath: "v.go", Line: 1},
			{Name: "validateHeader", Kind: "method", FilePath: "c.go", Line: 10},
			{Name: "checkInput", Kind: "function", FilePath: "i.go", Line: 20},
		}
		picked, ok := autoPickExactConceptMatch(candidates, conceptValues, nil)
		if !ok {
			t.Error("expected auto-pick when single exact match exists")
		}
		if picked.Name != "validate" {
			t.Errorf("expected 'validate', got %q", picked.Name)
		}
	})

	t.Run("multiple exact matches no pick", func(t *testing.T) {
		candidates := []agent.SymbolCandidate{
			{Name: "validate", Kind: "function", FilePath: "v.go", Line: 1},
			{Name: "check", Kind: "function", FilePath: "c.go", Line: 10},
		}
		_, ok := autoPickExactConceptMatch(candidates, conceptValues, nil)
		if ok {
			t.Error("should NOT auto-pick when multiple exact matches exist")
		}
	})

	t.Run("no exact matches no pick", func(t *testing.T) {
		candidates := []agent.SymbolCandidate{
			{Name: "validateHeader", Kind: "method", FilePath: "c.go", Line: 10},
			{Name: "checkInput", Kind: "function", FilePath: "i.go", Line: 20},
		}
		_, ok := autoPickExactConceptMatch(candidates, conceptValues, nil)
		if ok {
			t.Error("should NOT auto-pick when no exact matches exist")
		}
	})

	t.Run("empty candidates no pick", func(t *testing.T) {
		_, ok := autoPickExactConceptMatch(nil, conceptValues, nil)
		if ok {
			t.Error("should NOT auto-pick with empty candidates")
		}
	})

	t.Run("tier0 exact preferred over tier1 exact", func(t *testing.T) {
		// "validate" is tier1 (concept-only), but if there's a tier0 candidate
		// that also has exactness=3, it should be preferred.
		// Here Build is tier1 (concept "build" exact match) and assembleMenus is tier0.
		// Neither tier0 has exactness=3, so no auto-pick.
		menuConcepts := []string{"assemble", "build", "compose"}
		menuDomainNouns := []string{"menu"}
		candidates := []agent.SymbolCandidate{
			{Name: "assembleMenus", Kind: "method", FilePath: "m.go", Line: 1},
			{Name: "Build", Kind: "function", FilePath: "b.go", Line: 10},
		}
		_, ok := autoPickExactConceptMatch(candidates, menuConcepts, menuDomainNouns)
		if ok {
			t.Error("should NOT auto-pick: tier0 has no exact match, tier1 'Build' should not be picked over tier0")
		}
	})

	t.Run("duplicate same-name exact matches auto-picked", func(t *testing.T) {
		// D3c Rev 2: Two "validate" functions at different locations should
		// still auto-pick because they're the same logical symbol.
		// This matches the real Gin test 5251 scenario.
		candidates := []agent.SymbolCandidate{
			{Name: "validate", Kind: "function", FilePath: "v.go", Line: 1, InEdges: 27},
			{Name: "validate", Kind: "function", FilePath: "v2.go", Line: 10, InEdges: 0},
			{Name: "validateHeader", Kind: "method", FilePath: "c.go", Line: 20},
		}
		picked, ok := autoPickExactConceptMatch(candidates, conceptValues, nil)
		if !ok {
			t.Error("expected auto-pick when all exact matches share the same name")
		}
		if picked.Name != "validate" {
			t.Errorf("expected 'validate', got %q", picked.Name)
		}
		// Should pick the first one (higher edge count due to sort order)
		if picked.InEdges != 27 {
			t.Errorf("expected first candidate (27 in-edges), got %d", picked.InEdges)
		}
	})

	t.Run("tier0 blocks tier1 exact match", func(t *testing.T) {
		// validateData is tier0 (contains "data" domain noun + "validate" concept).
		// validate is tier1 (concept synonym only, no "data" in name).
		// Auto-pick should NOT fire because tier0's only candidate "validateData"
		// has exactness=2 (prefix), not exactness=3 (exact).
		candidates := []agent.SymbolCandidate{
			{Name: "validateData", Kind: "function", FilePath: "d.go", Line: 1}, // tier0
			{Name: "validate", Kind: "function", FilePath: "v.go", Line: 10},    // tier1
		}
		_, ok := autoPickExactConceptMatch(candidates, conceptValues, domainNouns)
		if ok {
			t.Error("should NOT auto-pick: tier0 'validateData' is prefix (exactness=2), not exact")
		}
	})
}

// TestSearchSymbolCandidates_PopulatesReceiver verifies D3c: Receiver field populated.
func TestSearchSymbolCandidates_PopulatesReceiver(t *testing.T) {
	idx := index.NewSymbolIndex()
	idx.Add(&ast.Symbol{
		ID: "context.go:100:validate", Name: "validate",
		Kind: ast.SymbolKindMethod, FilePath: "context.go",
		StartLine: 100, EndLine: 120, Language: "go",
		Receiver: "Context",
	})
	idx.Add(&ast.Symbol{
		ID: "utils.go:10:validate", Name: "validate",
		Kind: ast.SymbolKindFunction, FilePath: "utils.go",
		StartLine: 10, EndLine: 20, Language: "go",
	})

	candidates := searchSymbolCandidates(context.Background(), idx, []string{"validate"}, 10)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	foundReceiver := false
	foundNoReceiver := false
	for _, c := range candidates {
		if c.Receiver == "Context" {
			foundReceiver = true
		}
		if c.Receiver == "" && c.Kind == "function" {
			foundNoReceiver = true
		}
	}
	if !foundReceiver {
		t.Error("D3c: expected Receiver='Context' on method candidate")
	}
	if !foundNoReceiver {
		t.Error("D3c: expected empty Receiver on function candidate")
	}
}
