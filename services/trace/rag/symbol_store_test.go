// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package rag

import (
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/weaviate/weaviate/entities/models"
)

func TestBuildSearchText_Basic(t *testing.T) {
	sym := &ast.Symbol{
		Name:    "FindHotspots",
		Kind:    ast.SymbolKindFunction,
		Package: "pkg/materials",
	}
	got := BuildSearchText(sym)
	want := "function FindHotspots in package pkg/materials"
	if got != want {
		t.Errorf("buildSearchText() = %q, want %q", got, want)
	}
}

func TestBuildSearchText_WithSignature(t *testing.T) {
	sym := &ast.Symbol{
		Name:      "FindHotspots",
		Kind:      ast.SymbolKindFunction,
		Package:   "pkg/materials",
		Signature: "func FindHotspots(ctx context.Context) ([]Hotspot, error)",
	}
	got := BuildSearchText(sym)
	if !strings.HasPrefix(got, "function FindHotspots in package pkg/materials: func FindHotspots") {
		t.Errorf("buildSearchText() = %q, missing signature", got)
	}
}

func TestBuildSearchText_WithReceiver(t *testing.T) {
	sym := &ast.Symbol{
		Name:      "ServeHTTP",
		Kind:      ast.SymbolKindMethod,
		Package:   "pkg/server",
		Receiver:  "Router",
		Signature: "func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request)",
	}
	got := BuildSearchText(sym)
	if !strings.Contains(got, "(Router) ServeHTTP") {
		t.Errorf("buildSearchText() = %q, want receiver in output", got)
	}
	if !strings.HasPrefix(got, "method (Router) ServeHTTP in package pkg/server") {
		t.Errorf("buildSearchText() = %q, wrong format", got)
	}
}

func TestBuildSearchText_WithDocComment(t *testing.T) {
	sym := &ast.Symbol{
		Name:       "FindHotspots",
		Kind:       ast.SymbolKindFunction,
		Package:    "pkg/materials",
		DocComment: "FindHotspots identifies functions with high PageRank scores. It analyzes the call graph.",
	}
	got := BuildSearchText(sym)
	if !strings.Contains(got, ". FindHotspots identifies functions with high PageRank scores.") {
		t.Errorf("buildSearchText() = %q, missing doc comment first sentence", got)
	}
	// Should NOT contain the second sentence.
	if strings.Contains(got, "It analyzes") {
		t.Errorf("buildSearchText() = %q, should only include first sentence", got)
	}
}

func TestBuildSearchText_WithReceiverAndDoc(t *testing.T) {
	sym := &ast.Symbol{
		Name:       "ServeHTTP",
		Kind:       ast.SymbolKindMethod,
		Package:    "pkg/server",
		Receiver:   "Router",
		Signature:  "func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request)",
		DocComment: "ServeHTTP dispatches incoming HTTP requests to registered route handlers.",
	}
	got := BuildSearchText(sym)
	if !strings.Contains(got, "(Router) ServeHTTP") {
		t.Errorf("buildSearchText() = %q, missing receiver", got)
	}
	if !strings.Contains(got, "dispatches incoming HTTP requests") {
		t.Errorf("buildSearchText() = %q, missing doc comment", got)
	}
}

func TestBuildSearchText_NoPackage(t *testing.T) {
	sym := &ast.Symbol{
		Name: "globalFunc",
		Kind: ast.SymbolKindFunction,
	}
	got := BuildSearchText(sym)
	if strings.Contains(got, "in package") {
		t.Errorf("buildSearchText() = %q, should not have package", got)
	}
}

func TestBuildSearchText_StructNoSignature(t *testing.T) {
	sym := &ast.Symbol{
		Name:    "Material",
		Kind:    ast.SymbolKindStruct,
		Package: "pkg/materials",
	}
	got := BuildSearchText(sym)
	want := "struct Material in package pkg/materials"
	if got != want {
		t.Errorf("buildSearchText() = %q, want %q", got, want)
	}
}

func TestTruncateDocComment_Empty(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"tab", "\t"},
		{"newline", "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDocComment(tt.input)
			if got != "" {
				t.Errorf("truncateDocComment(%q) = %q, want empty", tt.input, got)
			}
		})
	}
}

func TestTruncateDocComment_SingleSentence(t *testing.T) {
	got := truncateDocComment("FindHotspots identifies functions with high PageRank scores.")
	if got != "FindHotspots identifies functions with high PageRank scores." {
		t.Errorf("truncateDocComment() = %q", got)
	}
}

func TestTruncateDocComment_MultiSentence(t *testing.T) {
	input := "FindHotspots identifies functions with high PageRank scores. It analyzes the call graph to find them."
	got := truncateDocComment(input)
	want := "FindHotspots identifies functions with high PageRank scores."
	if got != want {
		t.Errorf("truncateDocComment() = %q, want %q", got, want)
	}
}

func TestTruncateDocComment_StripCommentPrefix(t *testing.T) {
	got := truncateDocComment("// FindHotspots identifies functions.")
	want := "FindHotspots identifies functions."
	if got != want {
		t.Errorf("truncateDocComment() = %q, want %q", got, want)
	}
}

func TestTruncateDocComment_StripPythonPrefix(t *testing.T) {
	got := truncateDocComment("# FindHotspots identifies functions.")
	want := "FindHotspots identifies functions."
	if got != want {
		t.Errorf("truncateDocComment() = %q, want %q", got, want)
	}
}

func TestTruncateDocComment_LongNoSentence(t *testing.T) {
	// Build a string > 200 chars with no period-space
	long := strings.Repeat("word ", 50) // 250 chars
	got := truncateDocComment(long)
	if len(got) > 200 {
		t.Errorf("truncateDocComment() len=%d, want <= 200", len(got))
	}
	// Should truncate at a word boundary
	if strings.HasSuffix(got, " ") {
		t.Errorf("truncateDocComment() = %q, should not end with space", got)
	}
}

func TestTruncateDocComment_ExactlyAtBoundary(t *testing.T) {
	// 100 chars exactly, no sentence boundary
	input := strings.Repeat("a", 100)
	got := truncateDocComment(input)
	if got != input {
		t.Errorf("truncateDocComment() truncated 100-char string unnecessarily")
	}
}

// =============================================================================
// CRS-26h: CodeSymbol Schema Tests
// =============================================================================

func TestGetCodeSymbolSchema_VectorizerNone(t *testing.T) {
	schema := GetCodeSymbolSchema()
	if schema.Vectorizer != "none" {
		t.Errorf("GetCodeSymbolSchema().Vectorizer = %q, want %q", schema.Vectorizer, "none")
	}
}

func TestGetCodeSymbolSchema_NoModuleConfig(t *testing.T) {
	schema := GetCodeSymbolSchema()
	if schema.ModuleConfig != nil {
		t.Errorf("GetCodeSymbolSchema().ModuleConfig = %v, want nil (no text2vec-ollama)", schema.ModuleConfig)
	}
}

func TestGetCodeSymbolSchema_PropertiesNoModuleConfig(t *testing.T) {
	schema := GetCodeSymbolSchema()
	for _, prop := range schema.Properties {
		if prop.ModuleConfig != nil {
			t.Errorf("Property %q has ModuleConfig %v, want nil", prop.Name, prop.ModuleConfig)
		}
	}
}

func TestGetCodeSymbolSchema_SearchTextSearchable(t *testing.T) {
	schema := GetCodeSymbolSchema()
	var found bool
	for _, prop := range schema.Properties {
		if prop.Name == "searchText" {
			found = true
			if prop.IndexSearchable == nil || !*prop.IndexSearchable {
				t.Errorf("searchText.IndexSearchable should be true")
			}
			break
		}
	}
	if !found {
		t.Errorf("searchText property not found in schema")
	}
}

func TestGetCodeSymbolSchema_FilterableProperties(t *testing.T) {
	schema := GetCodeSymbolSchema()
	filterableExpected := []string{"symbolId", "name", "kind", "packagePath", "filePath", "exported", "dataSpace", "graphHash"}
	propMap := make(map[string]*models.Property)
	for _, p := range schema.Properties {
		propMap[p.Name] = p
	}
	for _, name := range filterableExpected {
		prop, ok := propMap[name]
		if !ok {
			t.Errorf("Expected filterable property %q not found", name)
			continue
		}
		if prop.IndexFilterable == nil || !*prop.IndexFilterable {
			t.Errorf("Property %q should be filterable", name)
		}
	}
}
