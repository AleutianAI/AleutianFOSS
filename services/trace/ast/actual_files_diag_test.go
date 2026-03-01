package ast_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// addSymbolsToIndexRecursive mirrors service.go:564-577 behavior exactly.
func addSymbolsToIndexRecursive(t *testing.T, idx *index.SymbolIndex, symbols []*ast.Symbol, logErrors bool) {
	t.Helper()
	for _, sym := range symbols {
		if sym == nil {
			continue
		}
		err := idx.Add(sym)
		if err != nil && logErrors {
			t.Logf("  idx.Add(%s [%s]) FAILED: %v", sym.Name, sym.Kind, err)
		}
		if len(sym.Children) > 0 {
			addSymbolsToIndexRecursive(t, idx, sym.Children, logErrors)
		}
	}
}

func TestActualTransformNode(t *testing.T) {
	content, err := os.ReadFile("/Users/jin/projects/Babylon.js/packages/dev/core/src/Meshes/transformNode.ts")
	if err != nil {
		t.Skipf("BabylonJS not available: %v", err)
	}

	parser := ast.NewTypeScriptParser()
	start := time.Now()
	result, err := parser.Parse(context.Background(), content, "packages/dev/core/src/Meshes/transformNode.ts")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Parse() FAILED for actual transformNode.ts: %v", err)
	}
	t.Logf("Parsed transformNode.ts in %v — %d symbols, %d errors", elapsed, len(result.Symbols), len(result.Errors))
	for _, e := range result.Errors {
		t.Logf("  Parse error: %s", e)
	}

	// Find TransformNode
	var tn *ast.Symbol
	for _, sym := range result.Symbols {
		t.Logf("Top-level symbol: %s (%s) at line %d", sym.Name, sym.Kind, sym.StartLine)
		if sym.Name == "TransformNode" {
			tn = sym
		}
	}

	if tn == nil {
		t.Fatal("TransformNode NOT FOUND in parsed symbols — THIS IS THE ROOT CAUSE")
	}

	t.Logf("TransformNode: Kind=%s StartLine=%d EndLine=%d Children=%d", tn.Kind, tn.StartLine, tn.EndLine, len(tn.Children))

	// Validate
	if err := tn.Validate(); err != nil {
		t.Fatalf("TransformNode.Validate() FAILED: %v", err)
	}
	t.Logf("TransformNode.Validate() PASSED")

	// Validate entire result
	if err := result.Validate(); err != nil {
		t.Fatalf("ParseResult.Validate() FAILED — THIS WOULD DROP THE ENTIRE FILE: %v", err)
	}
	t.Logf("ParseResult.Validate() PASSED")

	// === PHASE 2: Simulate index pipeline (mirrors service.go:274-279) ===
	t.Log("\n=== PHASE 2: Simulating index.Add() pipeline ===")
	idx := index.NewSymbolIndex()
	addSymbolsToIndexRecursive(t, idx, result.Symbols, true)

	// Check if TransformNode is in the index
	matches := idx.GetByName("TransformNode")
	if len(matches) == 0 {
		t.Fatal("TransformNode NOT IN INDEX after addSymbolsToIndexRecursive — THIS IS THE INDEX BUG")
	}
	t.Logf("TransformNode found in index: %d matches", len(matches))
	for _, m := range matches {
		t.Logf("  Match: %s (%s) at %s:%d", m.Name, m.Kind, m.FilePath, m.StartLine)
	}

	stats := idx.Stats()
	t.Logf("Index stats: %d total symbols, %d files", stats.TotalSymbols, stats.FileCount)
}

func TestActualMultiIndex(t *testing.T) {
	content, err := os.ReadFile("/Users/jin/projects/pandas/pandas/core/indexes/multi.py")
	if err != nil {
		t.Skipf("Pandas not available: %v", err)
	}

	parser := ast.NewPythonParser()
	start := time.Now()
	result, err := parser.Parse(context.Background(), content, "pandas/core/indexes/multi.py")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Parse() FAILED for actual multi.py: %v", err)
	}
	t.Logf("Parsed multi.py in %v — %d symbols, %d errors", elapsed, len(result.Symbols), len(result.Errors))
	for _, e := range result.Errors {
		t.Logf("  Parse error: %s", e)
	}

	// Find MultiIndex
	var mi *ast.Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == ast.SymbolKindClass {
			t.Logf("Class: %s at line %d (%d children)", sym.Name, sym.StartLine, len(sym.Children))
		}
		if sym.Name == "MultiIndex" {
			mi = sym
		}
	}

	if mi == nil {
		t.Fatal("MultiIndex NOT FOUND in parsed symbols — THIS IS THE ROOT CAUSE")
	}

	t.Logf("MultiIndex: Kind=%s StartLine=%d EndLine=%d Children=%d", mi.Kind, mi.StartLine, mi.EndLine, len(mi.Children))

	// Validate
	if err := mi.Validate(); err != nil {
		t.Fatalf("MultiIndex.Validate() FAILED: %v", err)
	}
	t.Logf("MultiIndex.Validate() PASSED")

	// Validate entire result
	if err := result.Validate(); err != nil {
		t.Fatalf("ParseResult.Validate() FAILED — THIS WOULD DROP THE ENTIRE FILE: %v", err)
	}
	t.Logf("ParseResult.Validate() PASSED")

	// === PHASE 2: Simulate index pipeline (mirrors service.go:274-279) ===
	t.Log("\n=== PHASE 2: Simulating index.Add() pipeline ===")
	idx := index.NewSymbolIndex()
	addSymbolsToIndexRecursive(t, idx, result.Symbols, true)

	// Check if MultiIndex is in the index
	matches := idx.GetByName("MultiIndex")
	if len(matches) == 0 {
		t.Fatal("MultiIndex NOT IN INDEX after addSymbolsToIndexRecursive — THIS IS THE INDEX BUG")
	}
	t.Logf("MultiIndex found in index: %d matches", len(matches))
	for _, m := range matches {
		t.Logf("  Match: %s (%s) at %s:%d", m.Name, m.Kind, m.FilePath, m.StartLine)
	}

	stats := idx.Stats()
	t.Logf("Index stats: %d total symbols, %d files", stats.TotalSymbols, stats.FileCount)
}

// TestActualFullProjectBabylonJS simulates the full Init() pipeline locally
// to verify TransformNode makes it into the index after walking the entire project.
func TestActualFullProjectBabylonJS(t *testing.T) {
	projectRoot := "/Users/jin/projects/Babylon.js/packages/dev/core/src"
	if _, err := os.Stat(projectRoot); err != nil {
		t.Skipf("BabylonJS not available: %v", err)
	}

	parser := ast.NewTypeScriptParser()
	idx := index.NewSymbolIndex()
	var fileCount, symbolCount, errorCount int

	// Walk and parse like service.go:parseProjectToResults
	start := time.Now()
	err := walkAndParse(t, projectRoot, parser, idx, &fileCount, &symbolCount, &errorCount)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	t.Logf("Parsed %d files in %v — %d symbols indexed, %d errors", fileCount, elapsed, symbolCount, errorCount)

	// Check target symbols
	targets := []string{"TransformNode", "Scene", "Engine", "AbstractMesh", "Node", "Mesh"}
	for _, name := range targets {
		matches := idx.GetByName(name)
		if len(matches) == 0 {
			t.Errorf("MISSING from index: %s", name)
		} else {
			t.Logf("FOUND in index: %s — %d matches", name, len(matches))
			for _, m := range matches {
				t.Logf("  %s (%s) at %s:%d [%d children]", m.Name, m.Kind, m.FilePath, m.StartLine, len(m.Children))
			}
		}
	}
}

// TestActualFullProjectPandas simulates the full Init() pipeline locally
// to verify MultiIndex makes it into the index after walking the entire project.
func TestActualFullProjectPandas(t *testing.T) {
	projectRoot := "/Users/jin/projects/pandas/pandas"
	if _, err := os.Stat(projectRoot); err != nil {
		t.Skipf("Pandas not available: %v", err)
	}

	parser := ast.NewPythonParser()
	idx := index.NewSymbolIndex()
	var fileCount, symbolCount, errorCount int

	start := time.Now()
	err := walkAndParse(t, projectRoot, parser, idx, &fileCount, &symbolCount, &errorCount)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	t.Logf("Parsed %d files in %v — %d symbols indexed, %d errors", fileCount, elapsed, symbolCount, errorCount)

	// Check target symbols
	targets := []string{"MultiIndex", "DataFrame", "Series", "Index"}
	for _, name := range targets {
		matches := idx.GetByName(name)
		if len(matches) == 0 {
			t.Errorf("MISSING from index: %s", name)
		} else {
			t.Logf("FOUND in index: %s — %d matches", name, len(matches))
			for _, m := range matches {
				t.Logf("  %s (%s) at %s:%d [%d children]", m.Name, m.Kind, m.FilePath, m.StartLine, len(m.Children))
			}
		}
	}
}

// walkAndParse walks a project directory, parses each file, and adds symbols to the index.
// This mirrors the exact pipeline in service.go: parseProjectToResults + addSymbolsToIndexRecursive.
func walkAndParse(t *testing.T, root string, parser ast.Parser, idx *index.SymbolIndex, fileCount, symbolCount, errorCount *int) error {
	t.Helper()

	extensions := parser.Extensions()
	extSet := make(map[string]bool)
	for _, ext := range extensions {
		extSet[ext] = true
	}

	return walkDir(root, func(path string) error {
		// Get extension
		ext := ""
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] == '.' {
				ext = path[i:]
				break
			}
		}
		if !extSet[ext] {
			return nil
		}

		// Skip test files and node_modules
		for i := 0; i < len(path); i++ {
			if i+12 <= len(path) && path[i:i+12] == "node_modules" {
				return nil
			}
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip unreadable files
		}

		// Get relative path
		relPath := path[len(root)+1:]

		result, err := parser.Parse(context.Background(), content, relPath)
		if err != nil {
			*errorCount++
			t.Logf("  Parse error: %s: %v", relPath, err)
			return nil
		}

		*fileCount++

		// Add symbols to index (mirrors service.go addSymbolsToIndexRecursive)
		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			if err := idx.Add(sym); err != nil {
				*errorCount++
				// Log ALL failures
				t.Logf("  idx.Add FAILED: %s (%s) at %s:%d — %v", sym.Name, sym.Kind, sym.FilePath, sym.StartLine, err)
			} else {
				*symbolCount++
			}
			// Recursively add children
			addChildrenToIndex(t, idx, sym.Children, symbolCount, errorCount)
		}

		return nil
	})
}

func addChildrenToIndex(t *testing.T, idx *index.SymbolIndex, children []*ast.Symbol, symbolCount, errorCount *int) {
	t.Helper()
	for _, child := range children {
		if child == nil {
			continue
		}
		if err := idx.Add(child); err != nil {
			*errorCount++
			// Only log non-duplicate errors (duplicates expected for methods with same name)
			if err.Error() != "" {
				t.Logf("  idx.Add child FAILED: %s (%s) at %s:%d — %v", child.Name, child.Kind, child.FilePath, child.StartLine, err)
			}
		} else {
			*symbolCount++
		}
		if len(child.Children) > 0 {
			addChildrenToIndex(t, idx, child.Children, symbolCount, errorCount)
		}
	}
}

// walkDir walks a directory recursively, calling fn for each regular file.
func walkDir(root string, fn func(path string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := root + "/" + entry.Name()
		if entry.IsDir() {
			// Skip common excluded directories
			name := entry.Name()
			if name == "node_modules" || name == "vendor" || name == ".git" || name == "__pycache__" {
				continue
			}
			if err := walkDir(path, fn); err != nil {
				return err
			}
		} else {
			if err := fn(path); err != nil {
				return err
			}
		}
	}
	return nil
}
