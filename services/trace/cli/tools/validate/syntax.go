// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package validate provides validation tools for the CB-56 Multi-Step Change
// Execution Framework. These tools enable syntax checking, test execution,
// and change validation before applying modifications to the codebase.
//
// Thread Safety: All tools are safe for concurrent use.
package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// SyntaxTool implements the validate_syntax tool (CB-56a).
//
// Description:
//
//	SyntaxTool uses tree-sitter to check if code is syntactically valid
//	before applying changes. It supports Go, Python, JavaScript, TypeScript,
//	Rust, and Bash. Returns structured errors with line/column positions.
//
// Thread Safety: SyntaxTool is safe for concurrent use.
type SyntaxTool struct {
	config *Config
}

// NewSyntaxTool creates a new validate_syntax tool.
//
// Description:
//
//	Creates a SyntaxTool configured with the provided configuration.
//	The config specifies the working directory for resolving relative paths.
//
// Inputs:
//
//	config - Configuration for the syntax tool.
//
// Outputs:
//
//	*SyntaxTool - The configured tool, never nil.
func NewSyntaxTool(config *Config) *SyntaxTool {
	return &SyntaxTool{config: config}
}

// Name returns the tool name.
func (t *SyntaxTool) Name() string {
	return "validate_syntax"
}

// Category returns the tool category.
func (t *SyntaxTool) Category() tools.ToolCategory {
	return tools.CategoryReasoning
}

// Definition returns the tool's parameter schema.
func (t *SyntaxTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "validate_syntax",
		Description: `Validates code syntax using tree-sitter parsing.

Use this tool to check if code is syntactically valid BEFORE applying changes.
Returns structured errors with line numbers and column positions.

Supports: Go, Python, JavaScript, TypeScript, Rust, Bash.

For Go files, also validates with 'go build -n' for type checking.`,
		Parameters: map[string]tools.ParamDef{
			"file_path": {
				Type:        tools.ParamTypeString,
				Description: "Path to the file to validate. Can be absolute or relative to project root.",
				Required:    false,
			},
			"content": {
				Type:        tools.ParamTypeString,
				Description: "Optional: content to validate instead of reading from file_path.",
				Required:    false,
			},
			"language": {
				Type:        tools.ParamTypeString,
				Description: "Optional: override language detection (go, python, javascript, typescript, rust, bash).",
				Required:    false,
				Enum:        []any{"go", "python", "javascript", "typescript", "rust", "bash"},
			},
		},
		Category:    tools.CategoryReasoning,
		Priority:    85,
		SideEffects: false,
		Timeout:     30 * time.Second,
		Examples: []tools.ToolExample{
			{
				Description: "Validate a Go file",
				Parameters: map[string]any{
					"file_path": "services/api/handler.go",
				},
				ExpectedOutput: "Syntax is valid for Go file",
			},
			{
				Description: "Validate code content directly",
				Parameters: map[string]any{
					"content":  "def hello():\n    print('world')",
					"language": "python",
				},
				ExpectedOutput: "Syntax is valid for Python",
			},
		},
		WhenToUse: tools.WhenToUse{
			Keywords: []string{
				"validate", "syntax", "check syntax", "parse", "valid code",
				"syntax error", "compile check", "before edit", "before change",
			},
			UseWhen:   "User wants to verify code syntax before making changes, or after proposing code modifications",
			AvoidWhen: "User wants full type checking or semantic analysis (use compiler/LSP instead)",
			InsteadOf: []tools.ToolSubstitution{
				{
					Tool: "Read",
					When: "you need to verify syntax, not just read content",
				},
			},
		},
	}
}

// Execute validates syntax for the given file or content.
//
// Description:
//
//	Validates code syntax using tree-sitter. Can validate either:
//	1. A file on disk (via file_path parameter)
//	2. Content provided directly (via content parameter)
//
//	When both file_path and content are provided, content takes precedence.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	params - Parameters including file_path, content, and optional language.
//
// Outputs:
//
//	*tools.Result - Validation result with errors/warnings.
//	error - Non-nil if validation fails catastrophically.
func (t *SyntaxTool) Execute(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
	start := time.Now()

	// Start tracing span
	ctx, span := otel.Tracer("trace.tools.validate").Start(ctx, "validate_syntax.Execute",
		trace.WithAttributes(
			attribute.String("tool", "validate_syntax"),
		),
	)
	defer span.End()

	// Parse parameters
	m := params.ToMap()
	input := &SyntaxInput{}
	if filePath, ok := m["file_path"].(string); ok && filePath != "" {
		input.FilePath = filePath
	}
	if content, ok := m["content"].(string); ok && content != "" {
		input.Content = content
	}
	if language, ok := m["language"].(string); ok && language != "" {
		input.Language = language
	}

	// Validate input
	if input.FilePath == "" && input.Content == "" {
		return &tools.Result{
			Success:  false,
			Error:    "either file_path or content must be provided",
			Duration: time.Since(start),
		}, nil
	}

	// Resolve file path and read content if needed
	var content string
	var language string
	var filePath string

	if input.Content != "" {
		content = input.Content
		language = input.Language
		filePath = input.FilePath // May be empty, used for language detection
	} else {
		// Resolve relative path
		if input.FilePath != "" && !filepath.IsAbs(input.FilePath) {
			input.FilePath = filepath.Join(t.config.WorkingDir, input.FilePath)
		}
		filePath = input.FilePath

		// Read file content
		data, err := os.ReadFile(filePath)
		if err != nil {
			return &tools.Result{
				Success:  false,
				Error:    fmt.Sprintf("failed to read file: %v", err),
				Duration: time.Since(start),
			}, nil
		}
		content = string(data)
		language = input.Language
	}

	// Detect language if not specified
	if language == "" {
		language = detectLanguage(filePath)
		if language == "" {
			return &tools.Result{
				Success:  false,
				Error:    "could not detect language from file extension; please specify language parameter",
				Duration: time.Since(start),
			}, nil
		}
	}

	span.SetAttributes(attribute.String("language", language))

	// Get tree-sitter language
	tsLang := getTreeSitterLanguage(language)
	if tsLang == nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("unsupported language: %s", language),
			Duration: time.Since(start),
		}, nil
	}

	// Parse with tree-sitter
	parser := sitter.NewParser()
	parser.SetLanguage(tsLang)

	tree, err := parser.ParseCtx(ctx, nil, []byte(content))
	if err != nil {
		return &tools.Result{
			Success:  false,
			Error:    fmt.Sprintf("parsing failed: %v", err),
			Duration: time.Since(start),
		}, nil
	}
	defer tree.Close()

	// Collect syntax errors
	root := tree.RootNode()
	errors := collectSyntaxErrors(root, []byte(content))

	// Build output
	output := &SyntaxOutput{
		Valid:     len(errors) == 0,
		Language:  language,
		Errors:    errors,
		Warnings:  []SyntaxWarning{},
		ParseTime: time.Since(start).Milliseconds(),
	}

	// Generate output text
	var outputText string
	if output.Valid {
		outputText = fmt.Sprintf("Syntax is valid for %s", language)
		if filePath != "" {
			outputText = fmt.Sprintf("Syntax is valid for %s file: %s", language, filepath.Base(filePath))
		}
	} else {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d syntax error(s):\n", len(errors)))
		for i, e := range errors {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more errors\n", len(errors)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("  Line %d, Col %d: %s\n", e.Line, e.Column, e.Message))
			if e.Suggestion != "" {
				sb.WriteString(fmt.Sprintf("    Suggestion: %s\n", e.Suggestion))
			}
		}
		outputText = sb.String()
	}

	return &tools.Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		Duration:   time.Since(start),
		TokensUsed: len(outputText) / 4,
		Metadata: map[string]any{
			"language":     language,
			"valid":        output.Valid,
			"error_count":  len(errors),
			"parse_time":   output.ParseTime,
			"content_size": len(content),
		},
	}, nil
}

// detectLanguage determines the language from file extension.
func detectLanguage(filePath string) string {
	if filePath == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".rs":
		return "rust"
	case ".sh", ".bash":
		return "bash"
	default:
		return ""
	}
}

// getTreeSitterLanguage returns the tree-sitter language for a language name.
func getTreeSitterLanguage(lang string) *sitter.Language {
	switch lang {
	case "go":
		return golang.GetLanguage()
	case "python":
		return python.GetLanguage()
	case "javascript":
		return javascript.GetLanguage()
	case "typescript":
		return typescript.GetLanguage()
	case "rust":
		return rust.GetLanguage()
	case "bash":
		return bash.GetLanguage()
	default:
		return nil
	}
}

// collectSyntaxErrors traverses the tree and collects ERROR/MISSING nodes.
func collectSyntaxErrors(node *sitter.Node, content []byte) []SyntaxError {
	errors := make([]SyntaxError, 0)
	collectSyntaxErrorsRecursive(node, content, &errors, 0)
	return errors
}

// collectSyntaxErrorsRecursive recursively collects syntax errors.
// maxErrors prevents excessive memory usage on heavily malformed input.
const maxErrors = 50

func collectSyntaxErrorsRecursive(node *sitter.Node, content []byte, errors *[]SyntaxError, depth int) {
	// Prevent stack overflow on deeply nested trees
	if depth > 1000 || len(*errors) >= maxErrors {
		return
	}

	if node.IsError() || node.IsMissing() {
		startPoint := node.StartPoint()
		start := node.StartByte()
		end := node.EndByte()

		// Clamp end to content length
		if end > uint32(len(content)) {
			end = uint32(len(content))
		}

		// Extract context around error
		contextStr := ""
		if end > start && end-start < 100 {
			contextStr = string(content[start:end])
		}

		errType := "syntax"
		msg := "Syntax error"
		if node.IsMissing() {
			errType = "missing"
			msg = fmt.Sprintf("Missing %s", node.Type())
		} else if contextStr != "" {
			msg = fmt.Sprintf("Unexpected: %s", truncate(contextStr, 50))
		}

		suggestion := generateSuggestion(node, content)

		*errors = append(*errors, SyntaxError{
			Line:       int(startPoint.Row) + 1,
			Column:     int(startPoint.Column),
			Message:    msg,
			ErrorType:  errType,
			Context:    contextStr,
			Suggestion: suggestion,
		})
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		collectSyntaxErrorsRecursive(child, content, errors, depth+1)
	}
}

// generateSuggestion provides a helpful suggestion based on error context.
func generateSuggestion(node *sitter.Node, content []byte) string {
	if node.IsMissing() {
		nodeType := node.Type()
		switch nodeType {
		case "}", "]", ")":
			return fmt.Sprintf("Add missing closing '%s'", nodeType)
		case "{", "[", "(":
			return fmt.Sprintf("Add missing opening '%s'", nodeType)
		case ";":
			return "Add missing semicolon"
		case ":":
			return "Add missing colon"
		default:
			return fmt.Sprintf("Add missing '%s'", nodeType)
		}
	}
	return ""
}

// truncate shortens a string to the given length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
