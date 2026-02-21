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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// ReadTool implements the Read file operation.
//
// Thread Safety: ReadTool is safe for concurrent use.
type ReadTool struct {
	config *Config
}

// NewReadTool creates a new Read tool with the given configuration.
func NewReadTool(config *Config) *ReadTool {
	return &ReadTool{config: config}
}

// Name returns the tool name.
func (t *ReadTool) Name() string {
	return "Read"
}

// Category returns the tool category.
func (t *ReadTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *ReadTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Read",
		Description: "Read file contents with line numbers. Supports text files, detects binary files. Use offset and limit for large files.",
		Parameters: map[string]tools.ParamDef{
			"file_path": {
				Type:        tools.ParamTypeString,
				Description: "Path to the file to read. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"offset": {
				Type:        tools.ParamTypeInt,
				Description: "Line number to start reading from (1-indexed). Defaults to 1.",
				Required:    false,
				Default:     0,
			},
			"limit": {
				Type:        tools.ParamTypeInt,
				Description: fmt.Sprintf("Maximum lines to read. Defaults to %d, max %d.", DefaultReadLimit, MaxReadLimit),
				Required:    false,
				Default:     DefaultReadLimit,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    100, // High priority - most common operation
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Read file using relative path",
				Parameters: map[string]any{
					"file_path": "main/main.go",
				},
			},
			{
				Description: "Read specific line range",
				Parameters: map[string]any{
					"file_path": "pkg/handlers/handler.go",
					"offset":    100,
					"limit":     50,
				},
			},
		},
	}
}

// Execute reads a file and returns its contents with line numbers.
func (t *ReadTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &ReadParams{}
	if filePath, ok := m["file_path"].(string); ok {
		p.FilePath = filePath
	}
	if offset, ok := getIntParam(m, "offset"); ok {
		p.Offset = offset
	}
	if limit, ok := getIntParam(m, "limit"); ok {
		p.Limit = limit
	}

	// Set defaults
	if p.Limit == 0 {
		p.Limit = DefaultReadLimit
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

	// Resolve and validate path
	realPath, err := ResolveAndValidatePath(p.FilePath, t.config)
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Check if sensitive path
	if IsSensitivePath(realPath) {
		return &tools.Result{
			Success:  false,
			Error:    "cannot read sensitive file",
			Duration: time.Since(start),
		}, nil
	}

	// Get file info
	stat, err := os.Stat(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &tools.Result{
				Success:  false,
				Error:    fmt.Sprintf("file not found: %s", p.FilePath),
				Duration: time.Since(start),
			}, nil
		}
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("cannot access file: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// Check if directory
	if stat.IsDir() {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("path is a directory, not a file: %s", p.FilePath),
			Duration: time.Since(start),
		}, nil
	}

	// Check file size
	if stat.Size() > MaxFileSizeBytes {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("file too large (%d bytes, max %d bytes)", stat.Size(), MaxFileSizeBytes),
			Duration: time.Since(start),
		}, nil
	}

	// Detect file type and handle accordingly
	fileType := detectFileType(realPath)

	var result *ReadResult
	switch fileType {
	case "binary":
		return &tools.Result{
			Success:  false,
			Error:    "binary file detected; cannot display as text",
			Duration: time.Since(start),
		}, nil
	default:
		result, err = t.readTextFile(ctx, realPath, p)
	}

	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	result.FileType = fileType

	// Mark file as read for edit tracking
	t.config.MarkFileRead(realPath)

	return &tools.Result{
		Success:    true,
		Output:     result,
		OutputText: result.Content,
		Duration:   time.Since(start),
		TokensUsed: estimateTokens(result.Content),
	}, nil
}

// readTextFile reads a text file with line numbers.
func (t *ReadTool) readTextFile(ctx context.Context, path string, p *ReadParams) (*ReadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	var lines []string
	var totalLines int
	var bytesRead int64

	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxLineLengthChars*4) // Allow for multi-byte characters

	lineNum := 0
	startLine := p.Offset
	if startLine < 1 {
		startLine = 1
	}
	endLine := startLine + p.Limit - 1

	for scanner.Scan() {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		lineNum++
		totalLines = lineNum
		line := scanner.Text()
		bytesRead += int64(len(line)) + 1 // +1 for newline

		// Skip lines before offset
		if lineNum < startLine {
			continue
		}

		// Stop after limit
		if lineNum > endLine {
			continue // Keep counting total lines
		}

		// Truncate long lines
		if len(line) > MaxLineLengthChars {
			line = line[:MaxLineLengthChars] + "... [truncated]"
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	// Format with line numbers (cat -n format)
	var formatted strings.Builder
	for i, line := range lines {
		actualLineNum := startLine + i
		// Right-align line numbers in 6-character field, followed by tab and arrow
		formatted.WriteString(fmt.Sprintf("%6d→%s\n", actualLineNum, line))
	}

	truncated := totalLines > endLine

	return &ReadResult{
		Content:     formatted.String(),
		TotalLines:  totalLines,
		LinesRead:   len(lines),
		Truncated:   truncated,
		TruncatedAt: endLine,
		BytesRead:   bytesRead,
	}, nil
}

// detectFileType detects the type of a file based on content and extension.
func detectFileType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	// Known text extensions
	textExts := map[string]string{
		".go":           "go",
		".py":           "python",
		".js":           "javascript",
		".ts":           "typescript",
		".jsx":          "javascript",
		".tsx":          "typescript",
		".java":         "java",
		".c":            "c",
		".cpp":          "cpp",
		".h":            "c",
		".hpp":          "cpp",
		".rs":           "rust",
		".rb":           "ruby",
		".php":          "php",
		".swift":        "swift",
		".kt":           "kotlin",
		".scala":        "scala",
		".cs":           "csharp",
		".md":           "markdown",
		".txt":          "text",
		".json":         "json",
		".yaml":         "yaml",
		".yml":          "yaml",
		".xml":          "xml",
		".html":         "html",
		".css":          "css",
		".sql":          "sql",
		".sh":           "shell",
		".bash":         "shell",
		".zsh":          "shell",
		".fish":         "shell",
		".ps1":          "powershell",
		".bat":          "batch",
		".cmd":          "batch",
		".toml":         "toml",
		".ini":          "ini",
		".cfg":          "config",
		".conf":         "config",
		".env":          "env",
		".gitignore":    "gitignore",
		".dockerignore": "dockerignore",
		".editorconfig": "editorconfig",
	}

	if fileType, ok := textExts[ext]; ok {
		return fileType
	}

	// Check content for binary
	file, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer file.Close()

	// Read first 512 bytes to detect content type
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "unknown"
	}
	buf = buf[:n]

	// Check for null bytes (binary indicator)
	for _, b := range buf {
		if b == 0 {
			return "binary"
		}
	}

	// Use http.DetectContentType as fallback
	contentType := http.DetectContentType(buf)
	if strings.HasPrefix(contentType, "text/") {
		return "text"
	}

	// Check if valid UTF-8
	if utf8.Valid(buf) {
		return "text"
	}

	return "binary"
}

// getIntParam extracts an int parameter from the params map.
// Handles both int and float64 (JSON unmarshaling produces float64).
func getIntParam(params map[string]any, key string) (int, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}

	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	default:
		return 0, false
	}
}

// estimateTokens estimates the token count for a string.
// Approximation: ~4 characters per token for code.
func estimateTokens(s string) int {
	return len(s) / 4
}
