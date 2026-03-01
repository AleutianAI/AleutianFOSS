// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system Attribution.

package graph_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

// pandasRoot is the root of the pandas test codebase.
const pandasRoot = "/Users/jin/projects/crs_test_codebases/python/pandas"

// TestDiagPandasSeriesTypeRefEdges is the IT-06d Bug 10 diagnostic.
//
// It builds a graph from pandas/core/frame.py + pandas/core/series.py and
// verifies that the Series node has incoming EdgeTypeReferences edges from
// functions in frame.py that annotate `-> Series` or `: Series`.
//
// This test isolates Hypothesis A/B (annotation extraction and disambiguation)
// from Hypothesis D (query truncation due to insertion order). With only two
// files and no asv_bench code, benchmark EdgeTypeCalls edges cannot fill the
// fetch window — if this test fails, the annotation extraction itself is broken.
func TestDiagPandasSeriesTypeRefEdges(t *testing.T) {
	if _, err := os.Stat(pandasRoot); os.IsNotExist(err) {
		t.Skip("pandas source not available")
	}

	parser := ast.NewPythonParser()

	seriesContent, err := os.ReadFile(pandasRoot + "/pandas/core/series.py")
	if err != nil {
		t.Fatalf("read series.py: %v", err)
	}
	frameContent, err := os.ReadFile(pandasRoot + "/pandas/core/frame.py")
	if err != nil {
		t.Fatalf("read frame.py: %v", err)
	}

	seriesResult, err := parser.Parse(context.Background(), seriesContent, "pandas/core/series.py")
	if err != nil {
		t.Fatalf("parse series.py: %v", err)
	}
	frameResult, err := parser.Parse(context.Background(), frameContent, "pandas/core/frame.py")
	if err != nil {
		t.Fatalf("parse frame.py: %v", err)
	}

	// Report how many symbols carry TypeReferences mentioning "Series".
	seriesRefCount := 0
	for _, sym := range frameResult.Symbols {
		for _, ref := range sym.TypeReferences {
			if ref.Name == "Series" {
				seriesRefCount++
				t.Logf("  frame.py symbol %q has TypeReference to Series at line %d",
					sym.Name, ref.Location.StartLine)
			}
		}
	}
	t.Logf("frame.py: %d symbols with TypeReference → Series", seriesRefCount)

	if seriesRefCount == 0 {
		t.Error("FAIL Hypothesis A: no symbols in frame.py carry TypeReference to 'Series'" +
			" — extractTypeRefsFromAnnotation is not extracting the annotations")
	}

	// Build the two-file graph.
	b := graph.NewBuilder()
	result, err := b.Build(context.Background(), []*ast.ParseResult{seriesResult, frameResult})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Logf("Graph: nodes=%d edges=%d",
		result.Stats.NodesCreated, result.Stats.EdgesCreated)

	// Find the canonical Series node (class kind, defined in series.py).
	seriesNodes := result.Graph.GetNodesByName("Series")
	t.Logf("Nodes named 'Series': %d", len(seriesNodes))
	for _, n := range seriesNodes {
		t.Logf("  node: id=%s kind=%s file=%s incoming=%d",
			n.ID, n.Symbol.Kind, n.Symbol.FilePath, len(n.Incoming))
	}

	var canonicalSeries *graph.Node
	for _, n := range seriesNodes {
		if n.Symbol.Kind == ast.SymbolKindClass &&
			n.Symbol.FilePath == "pandas/core/series.py" {
			canonicalSeries = n
			break
		}
	}
	if canonicalSeries == nil {
		t.Fatal("FAIL: could not find Series class node in pandas/core/series.py")
	}

	// Count edge types on the canonical Series node.
	var callCount, refCount, implCount, otherCount int
	for _, e := range canonicalSeries.Incoming {
		switch e.Type {
		case graph.EdgeTypeCalls:
			callCount++
		case graph.EdgeTypeReferences:
			refCount++
			fmt.Printf("  EdgeTypeReferences from=%s loc=%s:%d\n",
				e.FromID, e.Location.FilePath, e.Location.StartLine)
		case graph.EdgeTypeImplements:
			implCount++
		default:
			otherCount++
		}
	}
	t.Logf("Series node incoming edges: calls=%d refs=%d implements=%d other=%d",
		callCount, refCount, implCount, otherCount)

	// Hypothesis B check: if refCount == 0 but seriesRefCount > 0, the edges were created
	// but resolveSymbolByName resolved to a different Series node.
	if seriesRefCount > 0 && refCount == 0 {
		// Check if any OTHER Series node has the refs (disambiguation failure).
		for _, n := range seriesNodes {
			if n == canonicalSeries {
				continue
			}
			var nr int
			for _, e := range n.Incoming {
				if e.Type == graph.EdgeTypeReferences {
					nr++
				}
			}
			if nr > 0 {
				t.Errorf("FAIL Hypothesis B: EdgeTypeReferences went to wrong Series node"+
					" (id=%s file=%s kind=%s) instead of series.py class",
					n.ID, n.Symbol.FilePath, n.Symbol.Kind)
			}
		}
	}

	if refCount == 0 {
		t.Error("FAIL: canonical Series class node has no incoming EdgeTypeReferences" +
			" from frame.py — Bug 10 is not yet fixed")
	} else {
		t.Logf("PASS: Series has %d incoming EdgeTypeReferences from frame.py", refCount)
	}
}

// TestDiagPandasSeriesQueryPriority verifies that FindReferencesByID returns
// EdgeTypeReferences locations before EdgeTypeCalls locations (IT-06d Hypothesis D fix).
//
// It builds a minimal graph where one file annotates Series as a type and another
// calls its constructor, then confirms the type annotation location appears first
// in the query results.
func TestDiagPandasSeriesQueryPriority(t *testing.T) {
	if _, err := os.Stat(pandasRoot); os.IsNotExist(err) {
		t.Skip("pandas source not available")
	}

	// Build a tiny synthetic graph with both edge types to verify ordering.
	parser := ast.NewPythonParser()

	// File A: defines the Series class.
	defContent := []byte(`
class Series:
    def __init__(self, data=None):
        self.data = data
`)

	// File B: calls the constructor (EdgeTypeCalls on Series).
	callerContent := []byte(`
from .series import Series

def make_series():
    return Series([1, 2, 3])
`)

	// File C: annotates a return type -> Series (EdgeTypeReferences on Series).
	annotatorContent := []byte(`
from .series import Series

def wrap(x: int) -> Series:
    return Series([x])
`)

	defResult, err := parser.Parse(context.Background(), defContent, "pandas/core/series.py")
	if err != nil {
		t.Fatalf("parse def: %v", err)
	}
	callerResult, err := parser.Parse(context.Background(), callerContent, "pandas/core/caller.py")
	if err != nil {
		t.Fatalf("parse caller: %v", err)
	}
	annotatorResult, err := parser.Parse(context.Background(), annotatorContent, "pandas/core/annotator.py")
	if err != nil {
		t.Fatalf("parse annotator: %v", err)
	}

	b := graph.NewBuilder()
	// Process in order: def, caller, annotator — so call edges are inserted before type ref edges.
	result, err := b.Build(context.Background(), []*ast.ParseResult{defResult, callerResult, annotatorResult})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	seriesNodes := result.Graph.GetNodesByName("Series")
	var canonicalSeries *graph.Node
	for _, n := range seriesNodes {
		if n.Symbol.Kind == ast.SymbolKindClass {
			canonicalSeries = n
			break
		}
	}
	if canonicalSeries == nil {
		t.Fatal("Series class node not found")
	}

	t.Logf("Series incoming edges: %d total", len(canonicalSeries.Incoming))
	for _, e := range canonicalSeries.Incoming {
		t.Logf("  type=%s from=%s loc=%s:%d", e.Type, e.FromID, e.Location.FilePath, e.Location.StartLine)
	}

	// Query with a small limit to confirm priority ordering.
	locs, err := result.Graph.FindReferencesByID(context.Background(), canonicalSeries.ID, graph.WithLimit(1))
	if err != nil {
		t.Fatalf("FindReferencesByID: %v", err)
	}

	if len(locs) == 0 {
		t.Fatal("FindReferencesByID returned no locations")
	}

	// With limit=1 and two-pass ordering, the first result must come from annotator.py
	// (EdgeTypeReferences), not caller.py (EdgeTypeCalls).
	if locs[0].FilePath != "pandas/core/annotator.py" {
		t.Errorf("FAIL: priority ordering broken — first result is %s:%d (expected annotator.py)",
			locs[0].FilePath, locs[0].StartLine)
	} else {
		t.Logf("PASS: EdgeTypeReferences location returned first: %s:%d",
			locs[0].FilePath, locs[0].StartLine)
	}
}
