package graph_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

// TestDiagFlaskRequestRefs tests end-to-end that 'request' from globals.py gets
// incoming reference edges when app.py imports it via "from .globals import request".
func TestDiagFlaskRequestRefs(t *testing.T) {
	flaskRoot := "/Users/jin/projects/crs_test_codebases/python/flask"
	if _, err := os.Stat(flaskRoot); os.IsNotExist(err) {
		t.Skip("flask source not available")
	}

	// Parse the two key files
	parser := ast.NewPythonParser()

	globalsContent, err := os.ReadFile(flaskRoot + "/src/flask/globals.py")
	if err != nil {
		t.Fatalf("read globals.py: %v", err)
	}
	appContent, err := os.ReadFile(flaskRoot + "/src/flask/app.py")
	if err != nil {
		t.Fatalf("read app.py: %v", err)
	}

	globalsResult, err := parser.Parse(context.Background(), globalsContent, "src/flask/globals.py")
	if err != nil {
		t.Fatalf("parse globals.py: %v", err)
	}
	appResult, err := parser.Parse(context.Background(), appContent, "src/flask/app.py")
	if err != nil {
		t.Fatalf("parse app.py: %v", err)
	}

	t.Logf("globals.py: %d symbols, %d imports", len(globalsResult.Symbols), len(globalsResult.Imports))
	t.Logf("app.py: %d symbols, %d imports", len(appResult.Symbols), len(appResult.Imports))

	// Log relevant symbols from globals.py
	for _, sym := range globalsResult.Symbols {
		if sym.Name == "request" {
			t.Logf("  globals.py 'request' symbol: id=%s kind=%s exported=%v", sym.ID, sym.Kind, sym.Exported)
		}
	}

	// Log the request import from app.py
	for _, imp := range appResult.Imports {
		for _, n := range imp.Names {
			if n == "request" {
				t.Logf("  app.py import: path=%q name=%q relative=%v", imp.Path, n, imp.IsRelative)
			}
		}
	}

	// Build the graph
	b := graph.NewBuilder()
	result, err := b.Build(context.Background(), []*ast.ParseResult{globalsResult, appResult})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Logf("Graph built: nodes=%d edges=%d namedImportEdges=%d",
		result.Stats.NodesCreated, result.Stats.EdgesCreated, result.Stats.NamedImportEdgesResolved)

	// Find the request node
	requestNodes := result.Graph.GetNodesByName("request")
	t.Logf("Nodes named 'request': %d", len(requestNodes))
	for _, n := range requestNodes {
		t.Logf("  node: id=%s kind=%s file=%s incoming=%d outgoing=%d",
			n.ID, n.Symbol.Kind, n.Symbol.FilePath, len(n.Incoming), len(n.Outgoing))
		for _, e := range n.Incoming {
			fmt.Printf("    incoming edge: from=%s type=%s\n", e.FromID, e.Type)
		}
	}

	// Check if any request node has incoming references
	found := false
	for _, n := range requestNodes {
		if n.Symbol.Kind == ast.SymbolKindVariable {
			for _, e := range n.Incoming {
				if e.Type == graph.EdgeTypeReferences {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("FAIL: 'request' variable has no incoming EdgeTypeReferences - GR-62 fix not working")
	} else {
		t.Log("PASS: 'request' has incoming EdgeTypeReferences edges")
	}
}

// TestDiagImportLocation checks if the import Location is populated
func TestDiagImportLocation(t *testing.T) {
	content, err := os.ReadFile("/Users/jin/projects/crs_test_codebases/python/flask/src/flask/app.py")
	if err != nil {
		t.Skip("flask source not available")
	}

	parser := ast.NewPythonParser()
	result, err := parser.Parse(context.Background(), content, "src/flask/app.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	for _, imp := range result.Imports {
		for _, n := range imp.Names {
			if n == "request" {
				t.Logf("Import location: file=%q line=%d", imp.Location.FilePath, imp.Location.StartLine)
			}
		}
	}
}
