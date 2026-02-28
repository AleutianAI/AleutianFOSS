// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestDiffSnapshots_IdenticalGraphs(t *testing.T) {
	g := buildSnapshotTestGraph()

	diff, err := DiffSnapshots(g, g, "snap1", "snap2")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesAdded) != 0 {
		t.Errorf("nodes added = %d, want 0", len(diff.NodesAdded))
	}
	if len(diff.NodesRemoved) != 0 {
		t.Errorf("nodes removed = %d, want 0", len(diff.NodesRemoved))
	}
	if len(diff.NodesModified) != 0 {
		t.Errorf("nodes modified = %d, want 0", len(diff.NodesModified))
	}
	if diff.EdgesAdded != 0 {
		t.Errorf("edges added = %d, want 0", diff.EdgesAdded)
	}
	if diff.EdgesRemoved != 0 {
		t.Errorf("edges removed = %d, want 0", diff.EdgesRemoved)
	}
	if diff.Summary.TotalChanges != 0 {
		t.Errorf("total changes = %d, want 0", diff.Summary.TotalChanges)
	}
	if diff.Summary.ChangeRatio != 0 {
		t.Errorf("change ratio = %f, want 0", diff.Summary.ChangeRatio)
	}
}

func TestDiffSnapshots_NodeAdded(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	base.AddNode(symA)
	base.Freeze()

	target := NewGraph("/test")
	symA2 := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	target.AddNode(symA2)
	target.AddNode(symB)
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesAdded) != 1 {
		t.Fatalf("nodes added = %d, want 1", len(diff.NodesAdded))
	}
	if diff.NodesAdded[0] != "file.go:10:funcB" {
		t.Errorf("added node = %q, want %q", diff.NodesAdded[0], "file.go:10:funcB")
	}
	if len(diff.NodesRemoved) != 0 {
		t.Errorf("nodes removed = %d, want 0", len(diff.NodesRemoved))
	}
	if diff.Summary.FilesAffected != 1 {
		t.Errorf("files affected = %d, want 1", diff.Summary.FilesAffected)
	}
}

func TestDiffSnapshots_NodeRemoved(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	base.AddNode(symA)
	base.AddNode(symB)
	base.Freeze()

	target := NewGraph("/test")
	symA2 := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	target.AddNode(symA2)
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesRemoved) != 1 {
		t.Fatalf("nodes removed = %d, want 1", len(diff.NodesRemoved))
	}
	if diff.NodesRemoved[0] != "file.go:10:funcB" {
		t.Errorf("removed node = %q, want %q", diff.NodesRemoved[0], "file.go:10:funcB")
	}
}

func TestDiffSnapshots_NodeModified_SignatureChanged(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symA.Signature = "func() error"
	base.AddNode(symA)
	base.Freeze()

	target := NewGraph("/test")
	symA2 := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symA2.Signature = "func(ctx context.Context) error"
	target.AddNode(symA2)
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesModified) != 1 {
		t.Fatalf("nodes modified = %d, want 1", len(diff.NodesModified))
	}
	if diff.NodesModified[0].ChangeType != "signature_changed" {
		t.Errorf("change type = %q, want %q", diff.NodesModified[0].ChangeType, "signature_changed")
	}
}

func TestDiffSnapshots_NodeModified_Moved(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("old.go:1:funcA", "funcA", ast.SymbolKindFunction, "old.go")
	base.AddNode(symA)
	base.Freeze()

	target := NewGraph("/test")
	// Same ID but different file path — note: in practice IDs contain file path,
	// so moved symbols appear as add+remove. This tests the case where IDs match
	// but FilePath differs.
	symA2 := makeSymbol("old.go:1:funcA", "funcA", ast.SymbolKindFunction, "new.go")
	target.AddNode(symA2)
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesModified) != 1 {
		t.Fatalf("nodes modified = %d, want 1", len(diff.NodesModified))
	}
	if diff.NodesModified[0].ChangeType != "moved" {
		t.Errorf("change type = %q, want %q", diff.NodesModified[0].ChangeType, "moved")
	}
}

func TestDiffSnapshots_EdgeChanges(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	base.AddNode(symA)
	base.AddNode(symB)
	base.AddEdge(symA.ID, symB.ID, EdgeTypeCalls, makeLocation("file.go", 5))
	base.Freeze()

	target := NewGraph("/test")
	symA2 := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB2 := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	target.AddNode(symA2)
	target.AddNode(symB2)
	// Reverse the edge direction
	target.AddEdge(symB2.ID, symA2.ID, EdgeTypeCalls, makeLocation("file.go", 15))
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if diff.EdgesAdded != 1 {
		t.Errorf("edges added = %d, want 1", diff.EdgesAdded)
	}
	if diff.EdgesRemoved != 1 {
		t.Errorf("edges removed = %d, want 1", diff.EdgesRemoved)
	}
}

func TestDiffSnapshots_EmptyGraphs(t *testing.T) {
	base := NewGraph("/test")
	base.Freeze()

	target := NewGraph("/test")
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if diff.Summary.TotalChanges != 0 {
		t.Errorf("total changes = %d, want 0", diff.Summary.TotalChanges)
	}
	if diff.Summary.ChangeRatio != 0 {
		t.Errorf("change ratio = %f, want 0.0", diff.Summary.ChangeRatio)
	}
}

func TestDiffSnapshots_NilBase(t *testing.T) {
	target := NewGraph("/test")
	target.Freeze()

	_, err := DiffSnapshots(nil, target, "base", "target")
	if err == nil {
		t.Error("expected error for nil base")
	}
}

func TestDiffSnapshots_NilTarget(t *testing.T) {
	base := NewGraph("/test")
	base.Freeze()

	_, err := DiffSnapshots(base, nil, "base", "target")
	if err == nil {
		t.Error("expected error for nil target")
	}
}

func TestDiffSnapshots_ChangeRatio(t *testing.T) {
	base := NewGraph("/test")
	for i := 0; i < 10; i++ {
		sym := makeSymbol("file.go:"+string(rune('0'+i))+":f"+string(rune('0'+i)),
			"f"+string(rune('0'+i)), ast.SymbolKindFunction, "file.go")
		base.AddNode(sym)
	}
	base.Freeze()

	// Target has 5 of the same + 5 new = 5 removed + 5 added
	target := NewGraph("/test")
	for i := 0; i < 5; i++ {
		sym := makeSymbol("file.go:"+string(rune('0'+i))+":f"+string(rune('0'+i)),
			"f"+string(rune('0'+i)), ast.SymbolKindFunction, "file.go")
		target.AddNode(sym)
	}
	for i := 0; i < 5; i++ {
		sym := makeSymbol("new.go:"+string(rune('0'+i))+":g"+string(rune('0'+i)),
			"g"+string(rune('0'+i)), ast.SymbolKindFunction, "new.go")
		target.AddNode(sym)
	}
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	if len(diff.NodesAdded) != 5 {
		t.Errorf("nodes added = %d, want 5", len(diff.NodesAdded))
	}
	if len(diff.NodesRemoved) != 5 {
		t.Errorf("nodes removed = %d, want 5", len(diff.NodesRemoved))
	}
	if diff.Summary.ChangeRatio != 1.0 {
		t.Errorf("change ratio = %f, want 1.0", diff.Summary.ChangeRatio)
	}
}

func TestDiffSnapshots_DeterministicOutput(t *testing.T) {
	base := NewGraph("/test")
	symA := makeSymbol("b.go:1:b", "b", ast.SymbolKindFunction, "b.go")
	symB := makeSymbol("a.go:1:a", "a", ast.SymbolKindFunction, "a.go")
	base.AddNode(symA)
	base.AddNode(symB)
	base.Freeze()

	target := NewGraph("/test")
	target.Freeze()

	diff, err := DiffSnapshots(base, target, "base", "target")
	if err != nil {
		t.Fatalf("DiffSnapshots: %v", err)
	}

	// Removed nodes should be sorted
	if len(diff.NodesRemoved) != 2 {
		t.Fatalf("nodes removed = %d, want 2", len(diff.NodesRemoved))
	}
	if diff.NodesRemoved[0] >= diff.NodesRemoved[1] {
		t.Errorf("removed nodes not sorted: %q >= %q", diff.NodesRemoved[0], diff.NodesRemoved[1])
	}
}
