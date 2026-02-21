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
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// WriteTool implements the Write file operation.
//
// Thread Safety: WriteTool is safe for concurrent use.
type WriteTool struct {
	config *Config
}

// NewWriteTool creates a new Write tool with the given configuration.
func NewWriteTool(config *Config) *WriteTool {
	return &WriteTool{config: config}
}

// Name returns the tool name.
func (t *WriteTool) Name() string {
	return "Write"
}

// Category returns the tool category.
func (t *WriteTool) Category() tools.ToolCategory {
	return tools.CategoryFile
}

// Definition returns the tool's parameter schema.
func (t *WriteTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "Write",
		Description: "Create a new file or overwrite an existing file. Uses atomic writes for safety. Requires approval before writing.",
		Parameters: map[string]tools.ParamDef{
			"file_path": {
				Type:        tools.ParamTypeString,
				Description: "Path for the file to write. Can be absolute or relative to the project root.",
				Required:    true,
			},
			"content": {
				Type:        tools.ParamTypeString,
				Description: "File content to write",
				Required:    true,
			},
		},
		Category:    tools.CategoryFile,
		Priority:    95,
		SideEffects: true, // Modifies filesystem
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Create a new Go file",
				Parameters: map[string]any{
					"file_path": "/path/to/new_file.go",
					"content":   "package main\n\nfunc main() {\n}\n",
				},
			},
		},
	}
}

// Execute writes content to a file using atomic write.
func (t *WriteTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Parse parameters
	m := params.ToMap()
	p := &WriteParams{}
	if filePath, ok := m["file_path"].(string); ok {
		p.FilePath = filePath
	}
	if content, ok := m["content"].(string); ok {
		p.Content = content
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
			Error:    "cannot write to sensitive path",
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

	// Check if file exists (for Created flag)
	_, err := os.Stat(p.FilePath)
	isNew := os.IsNotExist(err)

	// Create parent directories if needed
	dir := filepath.Dir(p.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("failed to create parent directories: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// On Windows, coordinate with LSP servers to release file handles
	// before atomic write (os.Rename) to avoid "Access is denied" errors.
	needsLSPCoordination := runtime.GOOS == "windows" && t.config.LSPReleaser != nil
	if needsLSPCoordination {
		if err := t.config.LSPReleaser.ReleaseFile(ctx, p.FilePath); err != nil {
			// Log but continue - best effort
			slog.Warn("failed to release file for LSP",
				slog.String("file", p.FilePath),
				slog.String("error", err.Error()),
			)
		}
	}

	// Perform atomic write
	writeErr := atomicWriteFile(p.FilePath, []byte(p.Content), 0644)

	// On Windows, reopen the file in LSP servers after write
	if needsLSPCoordination {
		langID := languageIDFromPath(p.FilePath)
		if reopenErr := t.config.LSPReleaser.ReopenFile(ctx, p.FilePath, p.Content, langID); reopenErr != nil {
			// Log but continue - best effort
			slog.Warn("failed to reopen file for LSP",
				slog.String("file", p.FilePath),
				slog.String("error", reopenErr.Error()),
			)
		}
	}

	if writeErr != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("failed to write file: %v", writeErr),
			Duration: time.Since(start),
		}, nil
	}

	// Synchronous graph refresh BEFORE returning to prevent event storm.
	// This ensures the graph is updated immediately, so subsequent queries
	// see fresh data instead of triggering a write->notify->parse->write loop.
	if t.config.GraphRefresher != nil {
		if err := t.config.GraphRefresher.RefreshFiles(ctx, []string{p.FilePath}); err != nil {
			// Log but don't fail the write - graph will eventually catch up via fsnotify
			slog.Warn("failed to refresh graph after write",
				slog.String("file", p.FilePath),
				slog.String("error", err.Error()),
			)
		}
	}

	result := &WriteResult{
		Success:      true,
		BytesWritten: int64(len(p.Content)),
		Path:         p.FilePath,
		Created:      isNew,
	}

	return &tools.Result{
		Success:       true,
		Output:        result,
		OutputText:    fmt.Sprintf("Wrote %d bytes to %s", result.BytesWritten, result.Path),
		Duration:      time.Since(start),
		ModifiedFiles: []string{p.FilePath},
	}, nil
}

// atomicWriteFile writes content to a file atomically using rename.
//
// This ensures that the file is either fully written or not modified at all,
// preventing partial writes on crashes or errors.
func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// Create temp file in same directory (ensures same filesystem for rename)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Write content
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing content: %w", err)
	}

	// Sync to disk
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing to disk: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Set permissions before rename
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	success = true
	return nil
}

// languageIDFromPath returns the LSP language identifier for a file path.
//
// Description:
//
//	Maps common file extensions to LSP language identifiers. Returns
//	"plaintext" for unknown extensions.
//
// Thread Safety:
//
//	Safe for concurrent use (pure function).
func languageIDFromPath(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".h", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".sh", ".bash":
		return "shellscript"
	case ".sql":
		return "sql"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss":
		return "scss"
	case ".less":
		return "less"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".xml":
		return "xml"
	case ".md":
		return "markdown"
	default:
		return "plaintext"
	}
}
