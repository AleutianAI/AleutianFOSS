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
	"sort"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// TreeTool implements the Tree directory visualization operation.
//
// Thread Safety: TreeTool is safe for concurrent use.
type TreeTool struct {
	config *Config
}

// NewTreeTool creates a new Tree tool with the given configuration.
func NewTreeTool(config *Config) *TreeTool {
	return &TreeTool{config: config}
}

// Name returns the tool name.
func (t *TreeTool) Name() string {
	return "Tree"
}

// Category returns the tool category.
func (t *TreeTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *TreeTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Tree",
		Description: "Display directory structure as an ASCII tree. Useful for understanding project layout.",
		Parameters: map[string]tools.ParamDef{
			"path": {
				Type:        tools.ParamTypeString,
				Description: "Directory to visualize. Defaults to working directory.",
				Required:    false,
			},
			"depth": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Maximum depth to traverse (default: 3, max: %d)", MaxTreeDepth),
				Required:    false,
				Default:     3,
			},
			"show_hidden": {
				Type:        tools.ParamTypeBool,
				Description: "Include hidden files and directories (dotfiles)",
				Required:    false,
				Default:     false,
			},
			"dirs_only": {
				Type:        tools.ParamTypeBool,
				Description: "Show only directories, not files",
				Required:    false,
				Default:     false,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    75,
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Show project structure",
				Parameters: map[string]any{
					"path":  "/path/to/project",
					"depth": 2,
				},
			},
			{
				Description: "Show directories only",
				Parameters: map[string]any{
					"dirs_only": true,
				},
			},
		},
	}
}

// Execute generates a tree visualization of a directory.
func (t *TreeTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &TreeParams{}
	if path, ok := m["path"].(string); ok {
		p.Path = path
	}
	if depth, ok := getIntParam(m, "depth"); ok {
		p.Depth = depth
	}
	if showHidden, ok := m["show_hidden"].(bool); ok {
		p.ShowHidden = showHidden
	}
	if dirsOnly, ok := m["dirs_only"].(bool); ok {
		p.DirsOnly = dirsOnly
	}

	// Set defaults
	if p.Path == "" {
		p.Path = t.config.WorkingDir
	}
	if p.Depth == 0 {
		p.Depth = 3
	}

	// Validate
	if err := p.Validate(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check path is allowed
	if !t.config.IsPathAllowed(p.Path) {
		return &tools.Result{
			Success:  false,
			Error:    "path is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Check path exists and is directory
	info, err := os.Stat(p.Path)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("accessing path: %v", err),
			Duration: time.Since(start),
		}, nil
	}
	if !info.IsDir() {
		return &tools.Result{
			Success:  false,
			Error:    "path is not a directory",
			Duration: time.Since(start),
		}, nil
	}

	// Build tree
	var output strings.Builder
	stats := &treeStats{}
	truncated := false

	// Write root directory name
	output.WriteString(filepath.Base(p.Path))
	output.WriteString("\n")

	// Build the tree recursively
	truncated = t.buildTree(ctx, &output, p.Path, "", p.Depth, p.ShowHidden, p.DirsOnly, stats)

	result := &TreeResult{
		Tree:       output.String(),
		TotalDirs:  stats.dirs,
		TotalFiles: stats.files,
		Truncated:  truncated,
	}

	// Add summary line
	summary := fmt.Sprintf("\n%d directories", stats.dirs)
	if !p.DirsOnly {
		summary += fmt.Sprintf(", %d files", stats.files)
	}
	if truncated {
		summary += " (truncated at depth " + fmt.Sprint(p.Depth) + ")"
	}

	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: output.String() + summary,
		Duration:   time.Since(start),
	}, nil
}

type treeStats struct {
	dirs  int
	files int
}

// buildTree recursively builds the tree output.
func (t *TreeTool) buildTree(ctx context.Context, output *strings.Builder, path, prefix string, depth int, showHidden, dirsOnly bool, stats *treeStats) bool {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return true
	default:
	}

	if depth <= 0 {
		return true // Truncated
	}

	// Read directory entries
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}

	// Filter and sort entries
	var filtered []os.DirEntry
	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files unless requested
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}

		// Skip excluded directories
		if entry.IsDir() {
			skip := false
			for _, ex := range DefaultExclusions {
				if name == ex {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}

		// Skip files if dirs_only
		if dirsOnly && !entry.IsDir() {
			continue
		}

		filtered = append(filtered, entry)
	}

	// Sort: directories first, then alphabetically
	sort.Slice(filtered, func(i, j int) bool {
		iDir := filtered[i].IsDir()
		jDir := filtered[j].IsDir()
		if iDir != jDir {
			return iDir // Directories first
		}
		return filtered[i].Name() < filtered[j].Name()
	})

	truncated := false
	for i, entry := range filtered {
		isLast := i == len(filtered)-1

		// Determine the connector
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		// Determine the new prefix for children
		childPrefix := prefix + "│   "
		if isLast {
			childPrefix = prefix + "    "
		}

		// Write the entry
		output.WriteString(prefix)
		output.WriteString(connector)
		output.WriteString(entry.Name())

		if entry.IsDir() {
			output.WriteString("/")
			stats.dirs++
		} else {
			stats.files++
		}
		output.WriteString("\n")

		// Recurse into directories
		if entry.IsDir() {
			childPath := filepath.Join(path, entry.Name())
			if t.buildTree(ctx, output, childPath, childPrefix, depth-1, showHidden, dirsOnly, stats) {
				truncated = true
			}
		}
	}

	return truncated
}
