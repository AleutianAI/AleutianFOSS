// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNewOperations(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	if ops.Manager() != mgr {
		t.Error("Manager() should return the provided manager")
	}
}

func TestOperations_languageFromPath(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	tests := []struct {
		path     string
		expected string
	}{
		{"/project/main.go", "go"},
		{"/project/app.py", "python"},
		{"/project/app.ts", "typescript"},
		{"/project/app.js", "javascript"},
		{"/project/unknown.xyz", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ops.languageFromPath(tc.path)
			if got != tc.expected {
				t.Errorf("languageFromPath(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

func TestPathToURI(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/project/main.go", "file:///project/main.go"},
		{"/Users/test/app.py", "file:///Users/test/app.py"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := pathToURI(tc.path)
			if got != tc.expected {
				t.Errorf("pathToURI(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		uri      string
		expected string
	}{
		{"file:///project/main.go", "/project/main.go"},
		{"file:///Users/test/app.py", "/Users/test/app.py"},
	}

	for _, tc := range tests {
		t.Run(tc.uri, func(t *testing.T) {
			got := URIToPath(tc.uri)
			if got != tc.expected {
				t.Errorf("URIToPath(%q) = %q, want %q", tc.uri, got, tc.expected)
			}
		})
	}
}

func TestParseLocationResponse(t *testing.T) {
	t.Run("null response", func(t *testing.T) {
		locs, err := parseLocationResponse(json.RawMessage("null"))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if locs != nil {
			t.Errorf("expected nil, got %v", locs)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		locs, err := parseLocationResponse(json.RawMessage(""))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if locs != nil {
			t.Errorf("expected nil, got %v", locs)
		}
	})

	t.Run("single location", func(t *testing.T) {
		data := `{"uri":"file:///test.go","range":{"start":{"line":10,"character":0},"end":{"line":10,"character":5}}}`
		locs, err := parseLocationResponse(json.RawMessage(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(locs) != 1 {
			t.Fatalf("expected 1 location, got %d", len(locs))
		}
		if locs[0].URI != "file:///test.go" {
			t.Errorf("URI = %q, want file:///test.go", locs[0].URI)
		}
	})

	t.Run("array of locations", func(t *testing.T) {
		data := `[{"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":5}}},{"uri":"file:///b.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":5}}}]`
		locs, err := parseLocationResponse(json.RawMessage(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(locs) != 2 {
			t.Fatalf("expected 2 locations, got %d", len(locs))
		}
	})

	t.Run("array of location links", func(t *testing.T) {
		data := `[{"targetUri":"file:///test.go","targetRange":{"start":{"line":10,"character":0},"end":{"line":15,"character":0}},"targetSelectionRange":{"start":{"line":10,"character":5},"end":{"line":10,"character":15}}}]`
		locs, err := parseLocationResponse(json.RawMessage(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(locs) != 1 {
			t.Fatalf("expected 1 location, got %d", len(locs))
		}
		if locs[0].URI != "file:///test.go" {
			t.Errorf("URI = %q, want file:///test.go", locs[0].URI)
		}
	})
}

func TestOperations_Definition_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	_, err := ops.Definition(nil, "/test.go", 1, 0) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_Definition_UnsupportedLanguage(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	ctx := context.Background()
	_, err := ops.Definition(ctx, "/test.unknown", 1, 0)
	if err == nil {
		t.Error("expected error for unsupported language")
	}
}

func TestOperations_References_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	_, err := ops.References(nil, "/test.go", 1, 0, true) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_Hover_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	_, err := ops.Hover(nil, "/test.go", 1, 0) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_Rename_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	_, err := ops.Rename(nil, "/test.go", 1, 0, "newName") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_Rename_RequiresNewName(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	ctx := context.Background()
	_, err := ops.Rename(ctx, "/test.go", 1, 0, "")
	if err == nil {
		t.Error("expected error for empty newName")
	}
}

func TestOperations_WorkspaceSymbol_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	_, err := ops.WorkspaceSymbol(nil, "go", "test") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_OpenDocument_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	err := ops.OpenDocument(nil, "/test.go", "package main") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_CloseDocument_RequiresContext(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	err := ops.CloseDocument(nil, "/test.go") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestOperations_IsAvailable(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	// Unknown extension should not be available
	if ops.IsAvailable("/test.unknown") {
		t.Error("unknown extension should not be available")
	}

	// Go extension check (may or may not be available depending on system)
	// Just check it doesn't panic
	_ = ops.IsAvailable("/test.go")
}

func TestOperations_URIConversion(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	// Test round-trip
	path := "/project/main.go"
	uri := ops.PathToURI(path)
	back := ops.URIToPath(uri)

	if back != path {
		t.Errorf("round-trip failed: %q -> %q -> %q", path, uri, back)
	}
}

// Integration tests - only run if gopls is installed
const testGoFile = `package main

func main() {
	helper()
}

func helper() string {
	return "hello"
}
`

func TestOperations_Definition_Integration(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	// Setup test file
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(testGoFile), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	mgr := NewManager(dir, DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Open the file first
	if err := ops.OpenDocument(ctx, goFile, testGoFile); err != nil {
		t.Fatalf("OpenDocument: %v", err)
	}

	// Wait for gopls to process
	time.Sleep(500 * time.Millisecond)

	// Find definition of helper() call (line 4, col 1)
	locs, err := ops.Definition(ctx, goFile, 4, 1)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}

	if len(locs) == 0 {
		t.Fatal("no locations returned")
	}

	// Should point to helper function definition (line 7)
	if locs[0].Range.Start.Line != 6 { // 0-indexed
		t.Errorf("line = %d, want 6 (helper function definition)", locs[0].Range.Start.Line)
	}
}

func TestOperations_Hover_Integration(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(testGoFile), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	mgr := NewManager(dir, DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Open the file first
	if err := ops.OpenDocument(ctx, goFile, testGoFile); err != nil {
		t.Fatalf("OpenDocument: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Hover over helper function (line 7, col 5)
	info, err := ops.Hover(ctx, goFile, 7, 5)
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}

	if info == nil {
		t.Fatal("no hover info returned")
	}

	if info.Content == "" {
		t.Error("hover content should not be empty")
	}
}

func TestOperations_References_Integration(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(testGoFile), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	mgr := NewManager(dir, DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())

	ops := NewOperations(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Open the file first
	if err := ops.OpenDocument(ctx, goFile, testGoFile); err != nil {
		t.Fatalf("OpenDocument: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Find references to helper function (line 7, col 5)
	refs, err := ops.References(ctx, goFile, 7, 5, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}

	// Should find at least the definition and the call
	if len(refs) < 2 {
		t.Errorf("expected at least 2 references, got %d", len(refs))
	}
}

func TestHoverInfo_Fields(t *testing.T) {
	info := HoverInfo{
		Content: "```go\nfunc helper() string\n```",
		Kind:    "markdown",
		Range: &Range{
			Start: Position{Line: 6, Character: 5},
			End:   Position{Line: 6, Character: 11},
		},
	}

	if info.Content == "" {
		t.Error("Content should not be empty")
	}
	if info.Kind != "markdown" {
		t.Errorf("Kind = %q, want markdown", info.Kind)
	}
	if info.Range == nil {
		t.Error("Range should not be nil")
	}
}

// =============================================================================
// WORKSPACE EDIT HELPER TESTS
// =============================================================================

func TestOperations_SummarizeWorkspaceEdit(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())
	ops := NewOperations(mgr)

	t.Run("nil edit", func(t *testing.T) {
		summary := ops.SummarizeWorkspaceEdit(nil)
		if summary.FileCount != 0 {
			t.Errorf("FileCount = %d, want 0", summary.FileCount)
		}
		if summary.TotalEdits != 0 {
			t.Errorf("TotalEdits = %d, want 0", summary.TotalEdits)
		}
	})

	t.Run("empty edit", func(t *testing.T) {
		edit := &WorkspaceEdit{}
		summary := ops.SummarizeWorkspaceEdit(edit)
		if summary.FileCount != 0 {
			t.Errorf("FileCount = %d, want 0", summary.FileCount)
		}
	})

	t.Run("edit with changes", func(t *testing.T) {
		edit := &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				"file:///project/main.go": {
					{Range: Range{}, NewText: "foo"},
					{Range: Range{}, NewText: "bar"},
				},
				"file:///project/util.go": {
					{Range: Range{}, NewText: "baz"},
				},
			},
		}
		summary := ops.SummarizeWorkspaceEdit(edit)
		if summary.FileCount != 2 {
			t.Errorf("FileCount = %d, want 2", summary.FileCount)
		}
		if summary.TotalEdits != 3 {
			t.Errorf("TotalEdits = %d, want 3", summary.TotalEdits)
		}
		if summary.Files["/project/main.go"] != 2 {
			t.Errorf("main.go edits = %d, want 2", summary.Files["/project/main.go"])
		}
	})
}

func TestOperations_ValidateWorkspaceEdit(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())
	ops := NewOperations(mgr)

	t.Run("nil edit", func(t *testing.T) {
		err := ops.ValidateWorkspaceEdit(nil)
		if err == nil {
			t.Error("expected error for nil edit")
		}
	})

	t.Run("empty edit", func(t *testing.T) {
		edit := &WorkspaceEdit{}
		err := ops.ValidateWorkspaceEdit(edit)
		if err == nil {
			t.Error("expected error for empty edit")
		}
	})

	t.Run("invalid URI scheme", func(t *testing.T) {
		edit := &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				"https://example.com/file.go": {
					{Range: Range{}, NewText: "foo"},
				},
			},
		}
		err := ops.ValidateWorkspaceEdit(edit)
		if err == nil {
			t.Error("expected error for invalid URI scheme")
		}
	})

	t.Run("negative position", func(t *testing.T) {
		edit := &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				"file:///project/main.go": {
					{Range: Range{Start: Position{Line: -1, Character: 0}}, NewText: "foo"},
				},
			},
		}
		err := ops.ValidateWorkspaceEdit(edit)
		if err == nil {
			t.Error("expected error for negative position")
		}
	})

	t.Run("valid edit", func(t *testing.T) {
		edit := &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				"file:///project/main.go": {
					{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 5}}, NewText: "foo"},
				},
			},
		}
		err := ops.ValidateWorkspaceEdit(edit)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestOperations_PathToURI(t *testing.T) {
	mgr := NewManager("/tmp/test", DefaultManagerConfig())
	defer mgr.ShutdownAll(context.Background())
	ops := NewOperations(mgr)

	t.Run("simple path", func(t *testing.T) {
		uri := ops.PathToURI("/project/main.go")
		if uri != "file:///project/main.go" {
			t.Errorf("PathToURI = %q, want file:///project/main.go", uri)
		}
	})

	t.Run("path with spaces", func(t *testing.T) {
		uri := ops.PathToURI("/project/my file.go")
		// URL encoding should handle spaces
		if uri == "" {
			t.Error("PathToURI should not return empty string")
		}
		// Convert back and verify
		path := ops.URIToPath(uri)
		if path != "/project/my file.go" {
			t.Errorf("roundtrip failed: got %q, want /project/my file.go", path)
		}
	})
}

func TestOperations_IsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"server crashed", ErrServerCrashed, true},
		{"server not running", ErrServerNotRunning, true},
		{"unsupported language", ErrUnsupportedLanguage, false},
		{"request timeout", ErrRequestTimeout, false},
		{"LSP server error", &LSPError{Code: -32000, Message: "test"}, true},
		{"LSP method not found", &LSPError{Code: -32601, Message: "not found"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableError(tc.err)
			if result != tc.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tc.err, result, tc.expected)
			}
		})
	}
}
