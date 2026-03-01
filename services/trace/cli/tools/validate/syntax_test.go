// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package validate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

func TestSyntaxTool_Name(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	if got := tool.Name(); got != "validate_syntax" {
		t.Errorf("Name() = %v, want validate_syntax", got)
	}
}

func TestSyntaxTool_Category(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	if got := tool.Category(); got != tools.CategoryReasoning {
		t.Errorf("Category() = %v, want %v", got, tools.CategoryReasoning)
	}
}

func TestSyntaxTool_Definition(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	def := tool.Definition()

	if def.Name != "validate_syntax" {
		t.Errorf("Definition().Name = %v, want validate_syntax", def.Name)
	}

	if def.SideEffects {
		t.Error("Definition().SideEffects should be false")
	}

	// Check required parameters exist
	if _, ok := def.Parameters["file_path"]; !ok {
		t.Error("Definition() missing file_path parameter")
	}
	if _, ok := def.Parameters["content"]; !ok {
		t.Error("Definition() missing content parameter")
	}
	if _, ok := def.Parameters["language"]; !ok {
		t.Error("Definition() missing language parameter")
	}

	// Check WhenToUse is populated
	if len(def.WhenToUse.Keywords) == 0 {
		t.Error("Definition().WhenToUse.Keywords should not be empty")
	}
}

func TestSyntaxTool_Execute_ValidGo(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	validGo := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  validGo,
		"language": "go",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Errorf("Execute() Success = false, Error = %s", result.Error)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}

	if output.Language != "go" {
		t.Errorf("Execute() output.Language = %v, want go", output.Language)
	}
}

func TestSyntaxTool_Execute_InvalidGo(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	invalidGo := `package main

func main() {
	// Missing closing brace
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  invalidGo,
		"language": "go",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Errorf("Execute() Success = false, this should still succeed with validation errors")
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if output.Valid {
		t.Error("Execute() output.Valid = true, want false for invalid syntax")
	}

	if len(output.Errors) == 0 {
		t.Error("Execute() should have syntax errors")
	}
}

func TestSyntaxTool_Execute_ValidPython(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	validPython := `def hello(name: str) -> str:
    """Greet someone."""
    return f"Hello, {name}!"

class Greeter:
    def __init__(self, prefix: str):
        self.prefix = prefix

    def greet(self, name: str) -> str:
        return f"{self.prefix} {name}"
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  validPython,
		"language": "python",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}
}

func TestSyntaxTool_Execute_InvalidPython(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	invalidPython := `def hello(name:
    return "Hello"
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  invalidPython,
		"language": "python",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if output.Valid {
		t.Error("Execute() output.Valid = true, want false for invalid syntax")
	}
}

func TestSyntaxTool_Execute_ValidTypeScript(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	validTS := `interface User {
    id: number;
    name: string;
}

function greet(user: User): string {
    return "Hello, " + user.name;
}

const users: User[] = [];
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  validTS,
		"language": "typescript",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}
}

func TestSyntaxTool_Execute_ValidRust(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	validRust := `fn main() {
    let message = "Hello, World!";
    println!("{}", message);
}

struct Point {
    x: i32,
    y: i32,
}

impl Point {
    fn new(x: i32, y: i32) -> Self {
        Point { x, y }
    }
}
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  validRust,
		"language": "rust",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}
}

func TestSyntaxTool_Execute_ValidBash(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	validBash := `#!/bin/bash

function greet() {
    local name="$1"
    echo "Hello, $name!"
}

if [ -n "$1" ]; then
    greet "$1"
else
    greet "World"
fi
`

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  validBash,
		"language": "bash",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}
}

func TestSyntaxTool_Execute_FromFile(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.go")

	validGo := `package main

func main() {}
`
	if err := os.WriteFile(tmpFile, []byte(validGo), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	config := NewConfig(tmpDir)
	tool := NewSyntaxTool(config)
	ctx := context.Background()

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"file_path": tmpFile,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Errorf("Execute() Success = false, Error = %s", result.Error)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}

	// Language should be auto-detected
	if output.Language != "go" {
		t.Errorf("Execute() output.Language = %v, want go (auto-detected)", output.Language)
	}
}

func TestSyntaxTool_Execute_RelativePath(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}
	tmpFile := filepath.Join(subDir, "test.py")

	validPython := `def hello():
    return "Hello"
`
	if err := os.WriteFile(tmpFile, []byte(validPython), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	config := NewConfig(tmpDir)
	tool := NewSyntaxTool(config)
	ctx := context.Background()

	// Use relative path
	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"file_path": "src/test.py",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Errorf("Execute() Success = false, Error = %s", result.Error)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	if !output.Valid {
		t.Errorf("Execute() output.Valid = false, errors = %+v", output.Errors)
	}
}

func TestSyntaxTool_Execute_MissingInput(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Success {
		t.Error("Execute() Success = true, want false for missing input")
	}

	if result.Error == "" {
		t.Error("Execute() Error should not be empty")
	}
}

func TestSyntaxTool_Execute_UnsupportedLanguage(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  "some code",
		"language": "cobol",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Success {
		t.Error("Execute() Success = true, want false for unsupported language")
	}
}

func TestSyntaxTool_Execute_FileNotFound(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"file_path": "/nonexistent/path/to/file.go",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Success {
		t.Error("Execute() Success = true, want false for missing file")
	}
}

func TestSyntaxTool_Execute_ContentTakesPrecedence(t *testing.T) {
	// Create a temporary file with INVALID content
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.go")

	invalidGo := `package main { invalid }`
	if err := os.WriteFile(tmpFile, []byte(invalidGo), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	config := NewConfig(tmpDir)
	tool := NewSyntaxTool(config)
	ctx := context.Background()

	// Provide both file_path and content - content should win
	validGo := `package main

func main() {}
`
	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"file_path": tmpFile,
		"content":   validGo,
		"language":  "go",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(*SyntaxOutput)
	if !ok {
		t.Fatalf("Execute() Output is not *SyntaxOutput")
	}

	// Should be valid because content (valid) takes precedence over file (invalid)
	if !output.Valid {
		t.Error("Execute() output.Valid = false, content should take precedence")
	}
}

func TestSyntaxTool_Execute_ContextCancellation(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  "package main\n\nfunc main() {}",
		"language": "go",
	}})

	// Should not return an error, but parsing may fail
	if err != nil {
		t.Logf("Execute() error = %v (expected due to cancellation)", err)
	}

	// Result may or may not succeed depending on timing
	_ = result
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filePath string
		want     string
	}{
		{"main.go", "go"},
		{"handler.py", "python"},
		{"types.pyi", "python"},
		{"app.js", "javascript"},
		{"component.jsx", "javascript"},
		{"module.mjs", "javascript"},
		{"types.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"lib.rs", "rust"},
		{"script.sh", "bash"},
		{"build.bash", "bash"},
		{"unknown.xyz", ""},
		{"", ""},
		{"/path/to/file.go", "go"},
	}

	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			got := detectLanguage(tt.filePath)
			if got != tt.want {
				t.Errorf("detectLanguage(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestSyntaxOutput_Metadata(t *testing.T) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	result, err := tool.Execute(ctx, tools.MapParams{Params: map[string]any{
		"content":  "package main\n\nfunc main() {}",
		"language": "go",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check metadata
	if result.Metadata == nil {
		t.Fatal("Execute() Metadata is nil")
	}

	if lang, ok := result.Metadata["language"].(string); !ok || lang != "go" {
		t.Errorf("Execute() Metadata[language] = %v, want go", result.Metadata["language"])
	}

	if valid, ok := result.Metadata["valid"].(bool); !ok || !valid {
		t.Errorf("Execute() Metadata[valid] = %v, want true", result.Metadata["valid"])
	}

	if _, ok := result.Metadata["parse_time"].(int64); !ok {
		t.Errorf("Execute() Metadata[parse_time] missing or wrong type")
	}
}

// Benchmark tests
func BenchmarkSyntaxTool_Execute_SmallGo(b *testing.B) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	content := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, tools.MapParams{Params: map[string]any{
			"content":  content,
			"language": "go",
		}})
	}
}

func BenchmarkSyntaxTool_Execute_LargeGo(b *testing.B) {
	tool := NewSyntaxTool(DefaultConfig())
	ctx := context.Background()

	// Generate a larger Go file
	var content string
	content = "package main\n\nimport \"fmt\"\n\n"
	for i := 0; i < 100; i++ {
		content += "func function" + string(rune('A'+i%26)) + "(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n\n"
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, tools.MapParams{Params: map[string]any{
			"content":  content,
			"language": "go",
		}})
	}
}
