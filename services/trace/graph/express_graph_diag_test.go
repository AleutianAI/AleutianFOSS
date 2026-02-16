package graph_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

func TestDiag_ExpressCrossFileReceiverResolution(t *testing.T) {
	// Enable debug logging to see resolveCallTarget decisions
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	parseResults := []*ast.ParseResult{
		{
			FilePath: "lib/application.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        "lib/application.js:165:Application.handle",
					Name:      "handle",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "lib/application.js",
					StartLine: 165,
					EndLine:   180,
					Language:  "javascript",
					Receiver:  "Application",
					Calls: []ast.CallSite{
						{Target: "handle", Receiver: "router", IsMethod: true},
					},
				},
			},
		},
		{
			FilePath: "lib/router/index.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        "lib/router/index.js:136:Router.handle",
					Name:      "handle",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "lib/router/index.js",
					StartLine: 136,
					EndLine:   200,
					Language:  "javascript",
					Receiver:  "Router",
					Calls:     []ast.CallSite{},
				},
			},
		},
	}

	builder := graph.NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := result.Graph
	stats := g.Stats()
	t.Logf("Graph: nodes=%d edges=%d", stats.NodeCount, stats.EdgeCount)
	t.Logf("Build stats: nodes_created=%d edges_created=%d call_resolved=%d call_unresolved=%d",
		result.Stats.NodesCreated, result.Stats.EdgesCreated, result.Stats.CallEdgesResolved, result.Stats.CallEdgesUnresolved)
	if len(result.FileErrors) > 0 {
		for _, fe := range result.FileErrors {
			t.Logf("File error: %s: %v", fe.FilePath, fe.Err)
		}
	}
	if result.Stats.NodesCreated == 0 {
		t.Log("WARNING: NodesCreated is 0 — symbols may not be indexed")
	}

	edges := g.Edges()
	t.Logf("All edges (%d):", len(edges))
	for _, e := range edges {
		t.Logf("  %s → %s (type=%v)", e.FromID, e.ToID, e.Type)
	}

	callers, err := g.FindCallersByID(context.Background(), "lib/router/index.js:136:Router.handle")
	if err != nil {
		t.Fatalf("FindCallersByID failed: %v", err)
	}

	t.Logf("Callers of Router.handle: %d", len(callers.Symbols))
	if len(callers.Symbols) == 0 {
		t.Error("WANT: Application.handle → Router.handle edge, GOT: 0 callers")
	} else {
		for _, sym := range callers.Symbols {
			t.Logf("  Caller: %s (%s)", sym.Name, sym.ID)
		}
	}
}
