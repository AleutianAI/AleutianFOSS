// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

// =============================================================================
// read_file Tool — CB-63 Tier 1
// =============================================================================

var readFileTracer = otel.Tracer("tools.read_file")

// ReadFileParams contains the validated input parameters.
type ReadFileParams struct {
	// Path is the file path relative to the project root.
	Path string

	// StartLine is the first line to read (1-indexed, inclusive). Default: 1.
	StartLine int

	// EndLine is the last line to read (1-indexed, inclusive). Default: 200.
	EndLine int

	// Truncated is true when the requested range exceeded 500 lines and was clamped.
	Truncated bool
}

// ToolName returns the tool name for TypedParams interface.
func (p ReadFileParams) ToolName() string { return "read_file" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p ReadFileParams) ToMap() map[string]any {
	m := map[string]any{
		"path": p.Path,
	}
	if p.StartLine > 0 {
		m["start_line"] = p.StartLine
	}
	if p.EndLine > 0 {
		m["end_line"] = p.EndLine
	}
	return m
}

// ReadFileOutput contains the structured result.
type ReadFileOutput struct {
	// Path is the file path.
	Path string `json:"path"`

	// Language is the detected language.
	Language string `json:"language"`

	// TotalLines is the total number of lines in the file.
	TotalLines int `json:"total_lines"`

	// StartLine is the first line returned (1-indexed).
	StartLine int `json:"start_line"`

	// EndLine is the last line returned (1-indexed).
	EndLine int `json:"end_line"`

	// Source is the file content.
	Source string `json:"source"`

	// Truncated indicates if the response was limited by max lines (500).
	Truncated bool `json:"truncated"`
}

// readFileTool reads a file or line range from the project.
//
// Description:
//
//	Reads a file or specific line range from the project filesystem.
//	Validates paths against the project root to prevent traversal attacks.
//	Default: first 200 lines. Max: 500 lines per request.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type readFileTool struct {
	graph  *graph.Graph
	logger *slog.Logger
}

// NewReadFileTool creates the read_file tool.
//
// Description:
//
//	Creates a tool that reads files or line ranges from the project.
//	Validates all paths against the project root for security.
//
// Inputs:
//
//   - g: The code graph (used for ProjectRoot). Must not be nil.
//
// Outputs:
//
//   - Tool: The read_file tool implementation.
//
// Limitations:
//
//   - Maximum 500 lines per request
//   - Path must be under the project root
//
// Assumptions:
//
//   - Project files are accessible at graph.ProjectRoot
func NewReadFileTool(g *graph.Graph) Tool {
	return &readFileTool{
		graph:  g,
		logger: slog.Default(),
	}
}

func (t *readFileTool) Name() string {
	return "read_file"
}

func (t *readFileTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *readFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "read_file",
		Description: "Read a file or line range from the project. " +
			"Supports path-based access for viewing source code. " +
			"Use when the user pastes a file path from graph results or wants to see specific lines. " +
			"Default: first 200 lines. Max: 500 lines per request.",
		Parameters: map[string]ParamDef{
			"path": {
				Type:        ParamTypeString,
				Description: "File path relative to project root (e.g., 'src/main.go', 'pandas/io/parsers/readers.py')",
				Required:    true,
			},
			"start_line": {
				Type:        ParamTypeInt,
				Description: "First line to read (1-indexed, default: 1)",
				Required:    false,
				Default:     1,
			},
			"end_line": {
				Type:        ParamTypeInt,
				Description: "Last line to read (1-indexed, default: 200)",
				Required:    false,
				Default:     200,
			},
		},
		Category:    CategoryExploration,
		Priority:    93,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"show file", "read file", "view file",
				"show lines", "lines of", "file contents",
				"show me the file", "read lines",
			},
			UseWhen: "User wants to see the contents of a specific file or line range. " +
				"Use when the user provides a file path, often from graph query results. " +
				"Questions like 'show me file X', 'show lines 100-200 of X'.",
			AvoidWhen: "User asks about a specific named symbol — use read_symbol instead. " +
				"User asks about what symbols are in a file — use list_symbols_in_file.",
		},
	}
}

// Execute runs the read_file tool.
func (t *readFileTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_file").
			WithTool("read_file").
			WithDuration(time.Since(start)).
			WithError(err.Error()).
			Build()
		return &Result{
			Success:   false,
			Error:     err.Error(),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Start span
	ctx, span := readFileTracer.Start(ctx, "readFileTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "read_file"),
			attribute.String("path", p.Path),
			attribute.Int("start_line", p.StartLine),
			attribute.Int("end_line", p.EndLine),
		),
	)
	defer span.End()

	// Security: resolve and validate path
	absPath := filepath.Join(t.graph.ProjectRoot, p.Path)
	resolved, err := filepath.Abs(absPath)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_file").
			WithTarget(p.Path).
			WithTool("read_file").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("resolving path: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("invalid path '%s': %v", p.Path, err),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	projectRoot, err := filepath.Abs(t.graph.ProjectRoot)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("resolving project root: %w", err)
	}

	if !strings.HasPrefix(resolved, projectRoot+string(filepath.Separator)) && resolved != projectRoot {
		errMsg := fmt.Sprintf("path traversal rejected: '%s' resolves outside project root", p.Path)
		span.SetAttributes(attribute.Bool("path_traversal_blocked", true))
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_file").
			WithTarget(p.Path).
			WithTool("read_file").
			WithDuration(time.Since(start)).
			WithError(errMsg).
			Build()
		return &Result{
			Success:   false,
			Error:     errMsg,
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Read the file
	f, err := os.Open(resolved)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_file").
			WithTarget(p.Path).
			WithTool("read_file").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("opening file: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("cannot read '%s': %v", p.Path, err),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}
	defer f.Close()

	// Count total lines and collect requested range
	var lines []string
	totalLines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		totalLines++
		if totalLines >= p.StartLine && totalLines <= p.EndLine {
			lines = append(lines, scanner.Text())
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		span.RecordError(scanErr)
		return nil, fmt.Errorf("reading %s: %w", p.Path, scanErr)
	}

	// Adjust endLine to actual
	actualEnd := p.EndLine
	if actualEnd > totalLines {
		actualEnd = totalLines
	}

	// Detect language from extension
	language := detectLanguage(p.Path)

	truncated := p.Truncated

	output := ReadFileOutput{
		Path:       p.Path,
		Language:   language,
		TotalLines: totalLines,
		StartLine:  p.StartLine,
		EndLine:    actualEnd,
		Source:     strings.Join(lines, "\n"),
		Truncated:  truncated,
	}

	// Format text
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## File: `%s` (lines %d–%d of %d)\n\n",
		p.Path, p.StartLine, actualEnd, totalLines))
	sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", language, output.Source))
	if truncated {
		sb.WriteString(fmt.Sprintf("\n⚠ Showing %d lines (max 500 per request). Use start_line/end_line to paginate.\n", len(lines)))
	}

	duration := time.Since(start)

	span.SetAttributes(
		attribute.Int("total_lines", totalLines),
		attribute.Int("lines_returned", len(lines)),
		attribute.Bool("truncated", truncated),
	)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_read_file").
		WithTarget(p.Path).
		WithTool("read_file").
		WithDuration(duration).
		WithMetadata("total_lines", fmt.Sprintf("%d", totalLines)).
		WithMetadata("lines_returned", fmt.Sprintf("%d", len(lines))).
		WithMetadata("truncated", fmt.Sprintf("%t", truncated)).
		Build()

	return &Result{
		Success:     true,
		Output:      output,
		OutputText:  sb.String(),
		TokensUsed:  estimateTokens(sb.String()),
		TraceStep:   &toolStep,
		Duration:    duration,
		ResultCount: len(lines),
	}, nil
}

// parseParams validates and extracts typed parameters.
func (t *readFileTool) parseParams(params map[string]any) (ReadFileParams, error) {
	p := ReadFileParams{
		StartLine: 1,
		EndLine:   200,
	}

	if pathRaw, ok := params["path"]; ok {
		if path, ok := parseStringParam(pathRaw); ok && path != "" {
			p.Path = path
		}
	}
	if p.Path == "" {
		return p, fmt.Errorf("'path' parameter is required")
	}

	// Reject obvious traversal attempts
	if strings.Contains(p.Path, "..") {
		return p, fmt.Errorf("path must not contain '..': %s", p.Path)
	}

	if startRaw, ok := params["start_line"]; ok {
		if start, ok := parseIntParam(startRaw); ok && start > 0 {
			p.StartLine = start
		}
	}

	if endRaw, ok := params["end_line"]; ok {
		if end, ok := parseIntParam(endRaw); ok && end > 0 {
			p.EndLine = end
		}
	}

	if p.EndLine < p.StartLine {
		return p, fmt.Errorf("end_line (%d) must be >= start_line (%d)", p.EndLine, p.StartLine)
	}

	// Enforce max 500 lines — set truncated flag before clamping
	if p.EndLine-p.StartLine+1 > 500 {
		p.Truncated = true
		p.EndLine = p.StartLine + 499
	}

	return p, nil
}

// detectLanguage infers the programming language from a file extension.
func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".sh", ".bash":
		return "bash"
	default:
		return ""
	}
}
