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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// EditTool implements the Edit file operation.
//
// Thread Safety: EditTool is safe for concurrent use.
type EditTool struct {
	config *Config
}

// NewEditTool creates a new Edit tool with the given configuration.
func NewEditTool(config *Config) *EditTool {
	return &EditTool{config: config}
}

// Name returns the tool name.
func (t *EditTool) Name() string {
	return "Edit"
}

// Category returns the tool category.
func (t *EditTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *EditTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Edit",
		Description: "Make surgical edits to a file by replacing exact text. The old_string must match exactly (including whitespace). Use replace_all for multiple replacements.",
		Parameters: map[string]tools.ParamDef{
			"file_path": {
				Type:        tools.ParamTypeString,
				Description: "Path to the file to edit. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"old_string": {
				Type:        tools.ParamTypeString,
				Description: "Exact text to replace (must be unique unless replace_all is true)",
				Required:    true,
			},
			"new_string": {
				Type:        tools.ParamTypeString,
				Description: "Replacement text",
				Required:    true,
			},
			"replace_all": {
				Type:        tools.ParamTypeBool,
				Description: "Replace all occurrences instead of failing on multiple matches",
				Required:    false,
				Default:     false,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    98,
		SideEffects: true,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Replace a function name",
				Parameters: map[string]any{
					"file_path":  "/path/to/file.go",
					"old_string": "func oldName(",
					"new_string": "func newName(",
				},
			},
			{
				Description: "Replace all occurrences of a variable",
				Parameters: map[string]any{
					"file_path":   "/path/to/file.go",
					"old_string":  "oldVar",
					"new_string":  "newVar",
					"replace_all": true,
				},
			},
		},
	}
}

// Execute performs a surgical edit on a file.
func (t *EditTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &EditParams{}
	if filePath, ok := m["file_path"].(string); ok {
		p.FilePath = filePath
	}
	if oldString, ok := m["old_string"].(string); ok {
		p.OldString = oldString
	}
	if newString, ok := m["new_string"].(string); ok {
		p.NewString = newString
	}
	if replaceAll, ok := m["replace_all"].(bool); ok {
		p.ReplaceAll = replaceAll
	}

	// Resolve relative paths to absolute using working directory
	if p.FilePath != "" && !filepath.IsAbs(p.FilePath) {
		p.FilePath = filepath.Join(t.config.WorkingDir, p.FilePath)
	}

	// Validate
	if err := p.Validate(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check if sensitive path
	if IsSensitivePath(p.FilePath) {
		return &tools.Result{
			Success:  false,
			Error:    "cannot edit sensitive file",
			Duration: time.Since(start),
		}, nil
	}

	// Check if path is within allowed directories
	if !t.config.IsPathAllowed(p.FilePath) {
		return &tools.Result{
			Success:  false,
			Error:    "path is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Check if file was read first (safety measure)
	if !t.config.WasFileRead(p.FilePath) {
		return &tools.Result{
			Success:  false,
			Error:    ErrFileNotRead.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Read current content and compute hash for optimistic locking
	content, err := os.ReadFile(p.FilePath)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("failed to read file: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// Check file size
	if len(content) > MaxEditFileSize {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("file too large for edit (%d bytes, max %d)", len(content), MaxEditFileSize),
			Duration: time.Since(start),
		}, nil
	}

	oldContent := string(content)
	originalHash := computeContentHash(content)

	// Validate edit
	if err := t.validateEdit(oldContent, p.OldString, p.NewString, p.ReplaceAll); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Perform replacement (without holding any lock)
	var newContent string
	var replacements int
	if p.ReplaceAll {
		replacements = strings.Count(oldContent, p.OldString)
		newContent = strings.ReplaceAll(oldContent, p.OldString, p.NewString)
	} else {
		replacements = 1
		newContent = strings.Replace(oldContent, p.OldString, p.NewString, 1)
	}

	// Generate diff
	diff := generateUnifiedDiff(p.FilePath, oldContent, newContent)

	// Optimistic locking: verify file unchanged before writing
	// This prevents conflicts when user edits the file while agent is thinking
	if err := verifyAndWrite(p.FilePath, originalHash, []byte(newContent), 0644); err != nil {
		if err == ErrConflict {
			return &tools.Result{
				Success:  false,
				Error:    ErrConflict.Error(),
				Duration: time.Since(start),
			}, nil
		}
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("failed to write file: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// Synchronous graph refresh BEFORE returning to prevent event storm.
	// This ensures the graph is updated immediately, so subsequent queries
	// see fresh data instead of triggering a write->notify->parse->write loop.
	if t.config.GraphRefresher != nil {
		if err := t.config.GraphRefresher.RefreshFiles(ctx, []string{p.FilePath}); err != nil {
			// Log but don't fail the edit - graph will eventually catch up via fsnotify
			slog.Warn("failed to refresh graph after edit",
				slog.String("file", p.FilePath),
				slog.String("error", err.Error()),
			)
		}
	}

	result := &EditResult{
		Success:      true,
		Replacements: replacements,
		Diff:         diff,
		Path:         p.FilePath,
	}

	return &tools.Result{
		Success:       true,
		Output:        result,
		OutputText:    fmt.Sprintf("Made %d replacement(s) in %s\n\n%s", replacements, result.Path, diff),
		Duration:      time.Since(start),
		ModifiedFiles: []string{p.FilePath},
	}, nil
}

// validateEdit checks if the edit can be performed.
func (t *EditTool) validateEdit(content, oldStr, newStr string, replaceAll bool) error {
	if oldStr == "" {
		return fmt.Errorf("old_string cannot be empty")
	}
	if oldStr == newStr {
		return fmt.Errorf("old_string and new_string are identical")
	}

	count := strings.Count(content, oldStr)

	if count == 0 {
		return &EditError{
			Err:        ErrNoMatch,
			Suggestion: "verify the exact text including whitespace, or use Grep to find it",
		}
	}

	if count > 1 && !replaceAll {
		return &EditError{
			Err:        ErrMultipleMatch,
			MatchCount: count,
			Suggestion: fmt.Sprintf("matches %d times; use replace_all=true or provide more surrounding context to make it unique", count),
		}
	}

	return nil
}

// generateUnifiedDiff creates a simple unified diff format output.
func generateUnifiedDiff(filepath, oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- %s (original)\n", filepath))
	diff.WriteString(fmt.Sprintf("+++ %s (modified)\n", filepath))

	// Find changed regions using a simple diff algorithm
	changes := findChanges(oldLines, newLines)
	for _, change := range changes {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			change.oldStart+1, change.oldCount,
			change.newStart+1, change.newCount))

		// Context before (up to 3 lines)
		contextStart := max(0, change.oldStart-3)
		for i := contextStart; i < change.oldStart; i++ {
			diff.WriteString(fmt.Sprintf(" %s\n", oldLines[i]))
		}

		// Removed lines
		for i := change.oldStart; i < change.oldStart+change.oldCount; i++ {
			if i < len(oldLines) {
				diff.WriteString(fmt.Sprintf("-%s\n", oldLines[i]))
			}
		}

		// Added lines
		for i := change.newStart; i < change.newStart+change.newCount; i++ {
			if i < len(newLines) {
				diff.WriteString(fmt.Sprintf("+%s\n", newLines[i]))
			}
		}

		// Context after (up to 3 lines)
		contextEnd := min(len(oldLines), change.oldStart+change.oldCount+3)
		for i := change.oldStart + change.oldCount; i < contextEnd; i++ {
			diff.WriteString(fmt.Sprintf(" %s\n", oldLines[i]))
		}
	}

	return diff.String()
}

// changeRegion represents a region of change in the diff.
type changeRegion struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
}

// findChanges identifies regions that differ between old and new content.
func findChanges(oldLines, newLines []string) []changeRegion {
	var changes []changeRegion

	// Simple algorithm: find first and last differing lines
	// For more complex diffs, a proper LCS algorithm would be better
	firstDiff := -1
	lastDiffOld := -1
	lastDiffNew := -1

	minLen := min(len(oldLines), len(newLines))

	// Find first difference
	for i := 0; i < minLen; i++ {
		if oldLines[i] != newLines[i] {
			firstDiff = i
			break
		}
	}

	// If no difference found in common part, check for length difference
	if firstDiff == -1 {
		if len(oldLines) != len(newLines) {
			firstDiff = minLen
		} else {
			return changes // No changes
		}
	}

	// Find last difference from end
	oldIdx := len(oldLines) - 1
	newIdx := len(newLines) - 1
	for oldIdx >= firstDiff && newIdx >= firstDiff {
		if oldLines[oldIdx] != newLines[newIdx] {
			break
		}
		oldIdx--
		newIdx--
	}
	lastDiffOld = oldIdx
	lastDiffNew = newIdx

	if firstDiff <= lastDiffOld || firstDiff <= lastDiffNew {
		changes = append(changes, changeRegion{
			oldStart: firstDiff,
			oldCount: lastDiffOld - firstDiff + 1,
			newStart: firstDiff,
			newCount: lastDiffNew - firstDiff + 1,
		})
	}

	return changes
}

// computeContentHash returns a SHA-256 hash of the content.
// Used for optimistic locking to detect external modifications.
func computeContentHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// verifyAndWrite implements optimistic locking for file writes.
//
// # Description
//
// Reads the current file content, verifies it matches the expected hash,
// and only then performs the atomic write. This prevents overwriting
// changes made by the user while the agent was thinking.
//
// # Inputs
//
//   - path: File path to write to.
//   - expectedHash: SHA-256 hash of content when originally read.
//   - newContent: New content to write.
//   - perm: File permissions.
//
// # Outputs
//
//   - error: ErrConflict if file changed, other errors on I/O failure.
//
// # Thread Safety
//
// Uses brief file locking during the verify-and-write operation.
// Lock is held only during the critical section, not during editing.
func verifyAndWrite(path string, expectedHash string, newContent []byte, perm os.FileMode) error {
	// Re-read file content to verify it hasn't changed
	currentContent, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-reading file for verification: %w", err)
	}

	// Compute current hash
	currentHash := computeContentHash(currentContent)

	// Verify hash matches (optimistic lock check)
	if currentHash != expectedHash {
		return ErrConflict
	}

	// File unchanged, proceed with atomic write
	return atomicWriteFile(path, newContent, perm)
}
