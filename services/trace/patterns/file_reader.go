// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// FileReader provides cached file reading for pattern analysis tools.
//
// # Description
//
// FileReader caches file contents (split into lines) to avoid repeated I/O
// when multiple tools analyze the same files. This replaces the three
// duplicate readSymbolCode implementations in smells.go, duplication.go,
// and conventions.go.
//
// # Thread Safety
//
// This type is safe for concurrent use via sync.RWMutex.
type FileReader struct {
	mu          sync.RWMutex
	cache       map[string][]string // path → lines
	projectRoot string
}

// NewFileReader creates a new cached file reader.
//
// # Inputs
//
//   - projectRoot: Base directory for resolving relative file paths.
//
// # Outputs
//
//   - *FileReader: Configured reader with empty cache.
func NewFileReader(projectRoot string) *FileReader {
	return &FileReader{
		cache:       make(map[string][]string),
		projectRoot: projectRoot,
	}
}

// ReadLines reads file contents as lines, using cache on subsequent calls.
//
// # Inputs
//
//   - filePath: Relative file path (joined with projectRoot).
//
// # Outputs
//
//   - []string: File lines.
//   - error: Non-nil on I/O failure.
func (r *FileReader) ReadLines(filePath string) ([]string, error) {
	r.mu.RLock()
	if lines, ok := r.cache[filePath]; ok {
		r.mu.RUnlock()
		return lines, nil
	}
	r.mu.RUnlock()

	fullPath := filepath.Join(r.projectRoot, filePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", fullPath, err)
	}

	lines := strings.Split(string(content), "\n")

	r.mu.Lock()
	r.cache[filePath] = lines
	r.mu.Unlock()

	return lines, nil
}

// ReadSymbolCode reads the source code for a symbol, using cache.
//
// # Description
//
// Replaces the 3 duplicate readSymbolCode implementations that existed
// in SmellFinder, DuplicationFinder, and ConventionExtractor.
//
// # Inputs
//
//   - sym: The symbol to read code for.
//
// # Outputs
//
//   - string: Source code of the symbol.
//   - error: Non-nil if symbol is nil, lines are out of bounds, or I/O fails.
func (r *FileReader) ReadSymbolCode(sym *ast.Symbol) (string, error) {
	if sym == nil {
		return "", fmt.Errorf("nil symbol")
	}

	lines, err := r.ReadLines(sym.FilePath)
	if err != nil {
		return "", err
	}

	if sym.StartLine < 1 || sym.EndLine > len(lines) {
		return "", fmt.Errorf("symbol lines out of bounds: %d-%d in %d-line file %s",
			sym.StartLine, sym.EndLine, len(lines), sym.FilePath)
	}

	symbolLines := lines[sym.StartLine-1 : sym.EndLine]
	return strings.Join(symbolLines, "\n"), nil
}

// CacheSize returns the number of cached files.
func (r *FileReader) CacheSize() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cache)
}
