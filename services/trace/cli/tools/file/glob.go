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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// GlobTool implements the Glob file search operation.
//
// Thread Safety: GlobTool is safe for concurrent use.
type GlobTool struct {
	config *Config
}

// NewGlobTool creates a new Glob tool with the given configuration.
func NewGlobTool(config *Config) *GlobTool {
	return &GlobTool{config: config}
}

// Name returns the tool name.
func (t *GlobTool) Name() string {
	return "Glob"
}

// Category returns the tool category.
func (t *GlobTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *GlobTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Glob",
		Description: "Find files matching a glob pattern. Supports ** for recursive matching. Returns files sorted by modification time (most recent first).",
		Parameters: map[string]tools.ParamDef{
			"pattern": {
				Type:        tools.ParamTypeString,
				Description: "Glob pattern to match (e.g., '**/*.go', 'src/**/*.ts', '*.json')",
				Required:    true,
			},
			"path": {
				Type:        tools.ParamTypeString,
				Description: "Directory to search in. Defaults to working directory.",
				Required:    false,
			},
			"limit": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Maximum results to return. Defaults to %d, max %d.", DefaultGlobLimit, MaxGlobLimit),
				Required:    false,
				Default:     DefaultGlobLimit,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    90,
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Find all Go files",
				Parameters: map[string]any{
					"pattern": "**/*.go",
				},
			},
			{
				Description: "Find TypeScript files in src",
				Parameters: map[string]any{
					"pattern": "**/*.ts",
					"path":    "/project/src",
				},
			},
		},
	}
}

// Execute finds files matching the glob pattern.
func (t *GlobTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &GlobParams{}
	if pattern, ok := m["pattern"].(string); ok {
		p.Pattern = pattern
	}
	if path, ok := m["path"].(string); ok {
		p.Path = path
	}
	if limit, ok := getIntParam(m, "limit"); ok {
		p.Limit = limit
	}

	// Set defaults
	if p.Limit == 0 {
		p.Limit = DefaultGlobLimit
	}
	if p.Path == "" {
		p.Path = t.config.WorkingDir
	} else if !filepath.IsAbs(p.Path) {
		// GR-38: Normalize relative paths to absolute by joining with working directory
		p.Path = filepath.Join(t.config.WorkingDir, p.Path)
	}

	// Validate
	if err := p.Validate(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check if path is within allowed directories
	if !t.config.IsPathAllowed(p.Path) {
		return &tools.Result{
			Success:  false,
			Error:    "path is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Perform glob search
	files, totalCount, err := t.globFiles(ctx, p.Path, p.Pattern, p.Limit)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	result := &GlobResult{
		Files:      files,
		Count:      len(files),
		Truncated:  totalCount > p.Limit,
		SearchPath: p.Path,
	}

	// Format output as list of paths
	var output strings.Builder
	for _, f := range files {
		output.WriteString(f.Path)
		output.WriteString("\n")
	}

	jsonOutput, _ := json.Marshal(result)
	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: output.String(),
		Duration:   time.Since(start),
		TokensUsed: estimateTokens(string(jsonOutput)),
	}, nil
}

// globFiles performs the actual glob search.
func (t *GlobTool) globFiles(ctx context.Context, root, pattern string, limit int) ([]FileInfo, int, error) {
	var files []FileInfo
	totalCount := 0

	// Build exclusion set
	exclusions := make(map[string]bool)
	for _, ex := range DefaultExclusions {
		exclusions[ex] = true
	}

	// Handle ** pattern (recursive)
	isRecursive := strings.Contains(pattern, "**")

	// Split pattern into directory prefix and file pattern
	var dirPrefix string
	var filePattern string
	if isRecursive {
		// Find the part before **
		parts := strings.SplitN(pattern, "**", 2)
		if len(parts) > 0 && parts[0] != "" {
			dirPrefix = strings.TrimSuffix(parts[0], "/")
		}
		if len(parts) > 1 {
			filePattern = strings.TrimPrefix(parts[1], "/")
		}
	} else {
		// Non-recursive: pattern is the file pattern
		filePattern = pattern
	}

	// Walk the directory
	searchRoot := root
	if dirPrefix != "" {
		searchRoot = filepath.Join(root, dirPrefix)
	}

	err := filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			// Skip permission errors
			return nil
		}

		// Skip excluded directories
		if d.IsDir() {
			if exclusions[d.Name()] {
				return filepath.SkipDir
			}
			if !isRecursive && path != searchRoot {
				return filepath.SkipDir
			}
			return nil
		}

		// Match file against pattern
		var matched bool
		if filePattern == "" {
			matched = true
		} else {
			// Handle {a,b} alternatives by trying each
			patterns := expandBraces(filePattern)
			for _, p := range patterns {
				if m, _ := filepath.Match(p, d.Name()); m {
					matched = true
					break
				}
			}
		}

		if !matched {
			return nil
		}

		totalCount++

		// Get file info
		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		files = append(files, FileInfo{
			Path:    path,
			RelPath: relPath,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixMilli(),
		})

		return nil
	})

	if err != nil && err != context.Canceled {
		return nil, 0, fmt.Errorf("walking directory: %w", err)
	}

	// Sort by modification time (most recent first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})

	// Apply limit
	if len(files) > limit {
		files = files[:limit]
	}

	return files, totalCount, nil
}

// expandBraces expands {a,b} patterns into multiple patterns.
// For example, "*.{go,ts}" becomes ["*.go", "*.ts"]
func expandBraces(pattern string) []string {
	start := strings.Index(pattern, "{")
	if start == -1 {
		return []string{pattern}
	}

	end := strings.Index(pattern[start:], "}")
	if end == -1 {
		return []string{pattern}
	}
	end += start

	prefix := pattern[:start]
	suffix := pattern[end+1:]
	alternatives := strings.Split(pattern[start+1:end], ",")

	var results []string
	for _, alt := range alternatives {
		expanded := expandBraces(prefix + alt + suffix)
		results = append(results, expanded...)
	}

	return results
}
