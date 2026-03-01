// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// DiffTool implements the Diff file comparison operation.
//
// Thread Safety: DiffTool is safe for concurrent use.
type DiffTool struct {
	config *Config
}

// NewDiffTool creates a new Diff tool with the given configuration.
func NewDiffTool(config *Config) *DiffTool {
	return &DiffTool{config: config}
}

// Name returns the tool name.
func (t *DiffTool) Name() string {
	return "Diff"
}

// Category returns the tool category.
func (t *DiffTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *DiffTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Diff",
		Description: "Compare two files and show differences in unified diff format.",
		Parameters: map[string]tools.ParamDef{
			"file_a": {
				Type:        tools.ParamTypeString,
				Description: "Path to the first file. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"file_b": {
				Type:        tools.ParamTypeString,
				Description: "Path to the second file. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"context_lines": {
				Type:        tools.ParamTypeInt,
				Description: "Lines of context around changes (default: 3, max: 10)",
				Required:    false,
				Default:     3,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    80,
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Compare two files",
				Parameters: map[string]any{
					"file_a": "/path/to/original.go",
					"file_b": "/path/to/modified.go",
				},
			},
		},
	}
}

// Execute compares two files and returns the diff.
func (t *DiffTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &DiffParams{}
	if fileA, ok := m["file_a"].(string); ok {
		p.FileA = fileA
	}
	if fileB, ok := m["file_b"].(string); ok {
		p.FileB = fileB
	}
	if contextLines, ok := getIntParam(m, "context_lines"); ok {
		p.ContextLines = contextLines
	}

	// Set defaults
	if p.ContextLines == 0 {
		p.ContextLines = 3
	}

	// Resolve relative paths to absolute using working directory
	if p.FileA != "" && !filepath.IsAbs(p.FileA) {
		p.FileA = filepath.Join(t.config.WorkingDir, p.FileA)
	}
	if p.FileB != "" && !filepath.IsAbs(p.FileB) {
		p.FileB = filepath.Join(t.config.WorkingDir, p.FileB)
	}

	// Validate
	if err := p.Validate(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check paths are allowed
	if !t.config.IsPathAllowed(p.FileA) {
		return &tools.Result{
			Success:  false,
			Error:    "file_a is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}
	if !t.config.IsPathAllowed(p.FileB) {
		return &tools.Result{
			Success:  false,
			Error:    "file_b is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Read both files
	contentA, err := os.ReadFile(p.FileA)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("reading file_a: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	contentB, err := os.ReadFile(p.FileB)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("reading file_b: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// Check file sizes
	if len(contentA) > MaxFileSizeBytes {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("file_a too large (%d bytes, max %d)", len(contentA), MaxFileSizeBytes),
			Duration: time.Since(start),
		}, nil
	}
	if len(contentB) > MaxFileSizeBytes {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("file_b too large (%d bytes, max %d)", len(contentB), MaxFileSizeBytes),
			Duration: time.Since(start),
		}, nil
	}

	// Generate diff
	linesA := strings.Split(string(contentA), "\n")
	linesB := strings.Split(string(contentB), "\n")

	// Check if identical
	if string(contentA) == string(contentB) {
		result := &DiffResult{
			Diff:           "",
			LinesAdded:     0,
			LinesRemoved:   0,
			FilesIdentical: true,
		}
		return &tools.Result{
			Success:    true,
			Output:     result,
			OutputText: "Files are identical",
			Duration:   time.Since(start),
		}, nil
	}

	// Generate unified diff with context
	diff, added, removed := generateUnifiedDiffWithStats(p.FileA, p.FileB, linesA, linesB, p.ContextLines)

	result := &DiffResult{
		Diff:           diff,
		LinesAdded:     added,
		LinesRemoved:   removed,
		FilesIdentical: false,
	}

	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: diff,
		Duration:   time.Since(start),
	}, nil
}

// generateUnifiedDiffWithStats creates a unified diff and counts changes.
func generateUnifiedDiffWithStats(fileA, fileB string, linesA, linesB []string, contextLines int) (string, int, int) {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- %s\n", fileA))
	diff.WriteString(fmt.Sprintf("+++ %s\n", fileB))

	// Use Myers diff algorithm (simplified version)
	hunks := computeDiffHunks(linesA, linesB, contextLines)

	totalAdded := 0
	totalRemoved := 0

	for _, hunk := range hunks {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			hunk.oldStart+1, hunk.oldCount,
			hunk.newStart+1, hunk.newCount))

		for _, line := range hunk.lines {
			diff.WriteString(line)
			diff.WriteString("\n")

			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				totalAdded++
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				totalRemoved++
			}
		}
	}

	return diff.String(), totalAdded, totalRemoved
}

// diffHunk represents a contiguous region of changes.
type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []string
}

// computeDiffHunks computes diff hunks using a simple LCS-based algorithm.
func computeDiffHunks(linesA, linesB []string, contextLines int) []diffHunk {
	// Compute edit script using LCS
	edits := computeEditScript(linesA, linesB)

	if len(edits) == 0 {
		return nil
	}

	// Group edits into hunks with context
	var hunks []diffHunk
	var currentHunk *diffHunk

	for i, edit := range edits {
		// Start new hunk if needed
		if currentHunk == nil {
			startA := max(0, edit.oldLine-contextLines)
			startB := max(0, edit.newLine-contextLines)

			currentHunk = &diffHunk{
				oldStart: startA,
				newStart: startB,
			}

			// Add leading context
			for j := startA; j < edit.oldLine; j++ {
				currentHunk.lines = append(currentHunk.lines, " "+linesA[j])
			}
		}

		// Add the edit
		switch edit.op {
		case editDelete:
			currentHunk.lines = append(currentHunk.lines, "-"+linesA[edit.oldLine])
		case editInsert:
			currentHunk.lines = append(currentHunk.lines, "+"+linesB[edit.newLine])
		case editEqual:
			currentHunk.lines = append(currentHunk.lines, " "+linesA[edit.oldLine])
		}

		// Check if we should close the hunk
		closeHunk := false
		if i == len(edits)-1 {
			closeHunk = true
		} else {
			nextEdit := edits[i+1]
			// If gap to next edit is larger than 2*context, close hunk
			gap := 0
			if edit.op != editInsert && nextEdit.op != editInsert {
				gap = nextEdit.oldLine - edit.oldLine - 1
			}
			if gap > contextLines*2 {
				closeHunk = true
			}
		}

		if closeHunk && currentHunk != nil {
			// Add trailing context
			trailingStart := edit.oldLine + 1
			if edit.op == editInsert {
				trailingStart = edit.oldLine
			}
			trailingEnd := min(len(linesA), trailingStart+contextLines)
			for j := trailingStart; j < trailingEnd; j++ {
				currentHunk.lines = append(currentHunk.lines, " "+linesA[j])
			}

			// Calculate counts
			currentHunk.oldCount = 0
			currentHunk.newCount = 0
			for _, line := range currentHunk.lines {
				if strings.HasPrefix(line, "-") {
					currentHunk.oldCount++
				} else if strings.HasPrefix(line, "+") {
					currentHunk.newCount++
				} else {
					currentHunk.oldCount++
					currentHunk.newCount++
				}
			}

			hunks = append(hunks, *currentHunk)
			currentHunk = nil
		}
	}

	return hunks
}

type editOp int

const (
	editEqual editOp = iota
	editInsert
	editDelete
)

type edit struct {
	op      editOp
	oldLine int
	newLine int
}

// computeEditScript computes the sequence of edits to transform linesA to linesB.
func computeEditScript(linesA, linesB []string) []edit {
	m, n := len(linesA), len(linesB)

	// Build LCS matrix
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if linesA[i-1] == linesB[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else {
				lcs[i][j] = max(lcs[i-1][j], lcs[i][j-1])
			}
		}
	}

	// Backtrack to find edit script
	var edits []edit
	i, j := m, n

	for i > 0 || j > 0 {
		if i > 0 && j > 0 && linesA[i-1] == linesB[j-1] {
			// Lines are equal - could add as context if needed
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			// Insert from B
			edits = append([]edit{{op: editInsert, oldLine: i, newLine: j - 1}}, edits...)
			j--
		} else if i > 0 {
			// Delete from A
			edits = append([]edit{{op: editDelete, oldLine: i - 1, newLine: j}}, edits...)
			i--
		}
	}

	// Add equal lines around changes for context
	return addContextToEdits(edits, linesA, linesB)
}

// addContextToEdits adds context lines around the edit operations.
func addContextToEdits(edits []edit, linesA, linesB []string) []edit {
	if len(edits) == 0 {
		return edits
	}

	var result []edit
	prevOldLine := -1

	for _, e := range edits {
		// Add equal lines between this edit and the previous
		if e.op == editDelete || e.op == editEqual {
			for i := prevOldLine + 1; i < e.oldLine; i++ {
				result = append(result, edit{op: editEqual, oldLine: i, newLine: -1})
			}
			prevOldLine = e.oldLine
		}
		result = append(result, e)
	}

	return result
}
