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
	"strconv"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// JSONTool implements JSON file querying and validation.
//
// Thread Safety: JSONTool is safe for concurrent use.
type JSONTool struct {
	config *Config
}

// NewJSONTool creates a new JSON tool with the given configuration.
func NewJSONTool(config *Config) *JSONTool {
	return &JSONTool{config: config}
}

// Name returns the tool name.
func (t *JSONTool) Name() string {
	return "JSON"
}

// Category returns the tool category.
func (t *JSONTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *JSONTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "JSON",
		Description: "Query and validate JSON files. Supports jq-style path queries like .users[0].name",
		Parameters: map[string]tools.ParamDef{
			"file_path": {
				Type:        tools.ParamTypeString,
				Description: "Path to the JSON file. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"query": {
				Type:        tools.ParamTypeString,
				Description: "jq-style query path (e.g., '.users[0].name', '.config.timeout')",
				Required:    false,
			},
			"validate": {
				Type:        tools.ParamTypeBool,
				Description: "Only validate JSON syntax, don't query",
				Required:    false,
				Default:     false,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    78,
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Query a specific field",
				Parameters: map[string]any{
					"file_path": "/path/to/config.json",
					"query":     ".database.host",
				},
			},
			{
				Description: "Validate JSON file",
				Parameters: map[string]any{
					"file_path": "/path/to/data.json",
					"validate":  true,
				},
			},
			{
				Description: "Query array element",
				Parameters: map[string]any{
					"file_path": "/path/to/users.json",
					"query":     ".users[0].email",
				},
			},
		},
	}
}

// Execute queries or validates a JSON file.
func (t *JSONTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &JSONParams{}
	if filePath, ok := m["file_path"].(string); ok {
		p.FilePath = filePath
	}
	if query, ok := m["query"].(string); ok {
		p.Query = query
	}
	if validate, ok := m["validate"].(bool); ok {
		p.Validate = validate
	}

	// Resolve relative paths to absolute using working directory
	if p.FilePath != "" && !filepath.IsAbs(p.FilePath) {
		p.FilePath = filepath.Join(t.config.WorkingDir, p.FilePath)
	}

	// Validate params
	if err := p.ValidateParams(); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check path is allowed
	if !t.config.IsPathAllowed(p.FilePath) {
		return &tools.Result{
			Success:  false,
			Error:    "path is outside allowed directories",
			Duration: time.Since(start),
		}, nil
	}

	// Read file
	content, err := os.ReadFile(p.FilePath)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("reading file: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// Check file size
	if len(content) > MaxFileSizeBytes {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("file too large (%d bytes, max %d)", len(content), MaxFileSizeBytes),
			Duration: time.Since(start),
		}, nil
	}

	// Parse JSON
	var data any
	if err := json.Unmarshal(content, &data); err != nil {
		result := &JSONResult{
			Valid: false,
			Error: fmt.Sprintf("invalid JSON: %v", err),
			Path:  p.FilePath,
		}
		return &tools.Result{
			Success:    true, // Tool succeeded, but JSON is invalid
			Output:     result,
			OutputText: result.Error,
			Duration:   time.Since(start),
		}, nil
	}

	// If only validating, return success
	if p.Validate || p.Query == "" {
		result := &JSONResult{
			Valid: true,
			Value: data,
			Path:  p.FilePath,
		}

		outputText := "JSON is valid"
		if p.Query == "" && !p.Validate {
			// Return pretty-printed JSON
			prettyJSON, _ := json.MarshalIndent(data, "", "  ")
			outputText = string(prettyJSON)
		}

		return &tools.Result{
			Success:    true,
			Output:     result,
			OutputText: outputText,
			Duration:   time.Since(start),
		}, nil
	}

	// Execute query
	value, err := queryJSON(data, p.Query)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("query error: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	result := &JSONResult{
		Valid: true,
		Value: value,
		Path:  p.FilePath,
	}

	// Format output
	var outputText string
	switch v := value.(type) {
	case string:
		outputText = v
	case nil:
		outputText = "null"
	default:
		prettyJSON, _ := json.MarshalIndent(value, "", "  ")
		outputText = string(prettyJSON)
	}

	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: outputText,
		Duration:   time.Since(start),
	}, nil
}

// queryJSON executes a jq-style query on JSON data.
// Supports: .field, .field.nested, .array[0], .array[*]
func queryJSON(data any, query string) (any, error) {
	if query == "" || query == "." {
		return data, nil
	}

	// Remove leading dot if present
	if strings.HasPrefix(query, ".") {
		query = query[1:]
	}

	// Split query into parts
	parts := parseQueryParts(query)

	current := data
	for _, part := range parts {
		var err error
		current, err = applyQueryPart(current, part)
		if err != nil {
			return nil, err
		}
	}

	return current, nil
}

// queryPart represents a single part of a query path.
type queryPart struct {
	field string
	index int  // -1 means no index, -2 means all (*)
	isMap bool // true if accessing map field
}

// parseQueryParts splits a query into individual parts.
func parseQueryParts(query string) []queryPart {
	var parts []queryPart

	// Handle paths like "users[0].name" or "config.database.host"
	i := 0
	for i < len(query) {
		// Find the next separator (. or [)
		j := i
		for j < len(query) && query[j] != '.' && query[j] != '[' {
			j++
		}

		if j > i {
			// We have a field name
			parts = append(parts, queryPart{
				field: query[i:j],
				index: -1,
				isMap: true,
			})
		}

		if j >= len(query) {
			break
		}

		if query[j] == '.' {
			i = j + 1
			continue
		}

		if query[j] == '[' {
			// Find the closing bracket
			k := j + 1
			for k < len(query) && query[k] != ']' {
				k++
			}

			if k >= len(query) {
				// Malformed query, treat rest as field name
				parts = append(parts, queryPart{
					field: query[j:],
					index: -1,
					isMap: true,
				})
				break
			}

			indexStr := query[j+1 : k]
			var idx int
			if indexStr == "*" {
				idx = -2 // All elements
			} else {
				var err error
				idx, err = strconv.Atoi(indexStr)
				if err != nil {
					idx = -1
				}
			}

			parts = append(parts, queryPart{
				index: idx,
				isMap: false,
			})

			i = k + 1
			if i < len(query) && query[i] == '.' {
				i++
			}
			continue
		}

		i++
	}

	return parts
}

// applyQueryPart applies a single query part to the current data.
func applyQueryPart(data any, part queryPart) (any, error) {
	if part.isMap {
		// Access map field
		switch v := data.(type) {
		case map[string]any:
			if val, ok := v[part.field]; ok {
				return val, nil
			}
			return nil, fmt.Errorf("field '%s' not found", part.field)
		default:
			return nil, fmt.Errorf("cannot access field '%s' on non-object", part.field)
		}
	}

	// Access array index
	switch v := data.(type) {
	case []any:
		if part.index == -2 {
			// Return all elements
			return v, nil
		}
		if part.index < 0 || part.index >= len(v) {
			return nil, fmt.Errorf("index %d out of bounds (array length: %d)", part.index, len(v))
		}
		return v[part.index], nil
	default:
		return nil, fmt.Errorf("cannot index non-array with [%d]", part.index)
	}
}
