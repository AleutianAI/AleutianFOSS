// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package validation

import (
	"strings"
	"testing"
)

func TestInputValidator_ValidateFilePath(t *testing.T) {
	v := NewInputValidator(nil)

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errType string
	}{
		// Valid paths
		{
			name:    "simple file",
			path:    "main.go",
			wantErr: false,
		},
		{
			name:    "nested path",
			path:    "pkg/auth/handler.go",
			wantErr: false,
		},
		{
			name:    "absolute path",
			path:    "/home/user/project/main.go",
			wantErr: false,
		},
		{
			name:    "path with spaces",
			path:    "path with spaces/file.go",
			wantErr: false,
		},

		// Path traversal - direct
		{
			name:    "direct traversal ..",
			path:    "..",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "traversal at start",
			path:    "../etc/passwd",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "traversal in middle",
			path:    "pkg/../../../etc/passwd",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "traversal at end",
			path:    "pkg/file/..",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "windows traversal",
			path:    "pkg\\..\\..\\etc\\passwd",
			wantErr: true,
			errType: "traversal",
		},

		// Path traversal - encoded
		{
			name:    "url encoded ..",
			path:    "%2e%2e",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "double url encoded ..",
			path:    "%252e%252e",
			wantErr: true,
			errType: "traversal",
		},
		{
			name:    "encoded traversal with slash",
			path:    "..%2f..%2fetc%2fpasswd",
			wantErr: true,
			errType: "traversal",
		},

		// Null bytes
		{
			name:    "null byte in path",
			path:    "file\x00.go",
			wantErr: true,
			errType: "null byte",
		},
		{
			name:    "null byte at end",
			path:    "file.go\x00",
			wantErr: true,
			errType: "null byte",
		},

		// Empty and special
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
			errType: "empty",
		},
		{
			name:    "home directory",
			path:    "~/secrets.txt",
			wantErr: true,
			errType: "~",
		},
		{
			name:    "home directory with path",
			path:    "~user/secrets.txt",
			wantErr: true,
			errType: "~",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateFilePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFilePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errType != "" {
				if !strings.Contains(err.Error(), tt.errType) {
					t.Errorf("ValidateFilePath() error = %v, want error containing %q", err, tt.errType)
				}
			}
		})
	}
}

func TestInputValidator_ValidateFilePath_LengthLimit(t *testing.T) {
	// Test with custom length limit
	opts := &InputValidatorOptions{
		MaxPathLen:    100,
		MaxPatchLen:   1 << 20,
		MaxPatternLen: 1000,
	}
	v := NewInputValidator(opts)

	// Path under limit should pass
	shortPath := strings.Repeat("a", 99)
	if err := v.ValidateFilePath(shortPath); err != nil {
		t.Errorf("ValidateFilePath() with short path = %v, want nil", err)
	}

	// Path over limit should fail
	longPath := strings.Repeat("a", 101)
	if err := v.ValidateFilePath(longPath); err == nil {
		t.Error("ValidateFilePath() with long path = nil, want error")
	}
}

func TestInputValidator_ValidateDiffPatch(t *testing.T) {
	v := NewInputValidator(nil)

	validPatch := `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main

 func main() {
+    fmt.Println("hello")
 }
`

	tests := []struct {
		name    string
		patch   string
		wantErr bool
	}{
		{
			name:    "valid git diff",
			patch:   validPatch,
			wantErr: false,
		},
		{
			name: "valid unified diff",
			patch: `--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main
+import "fmt"
`,
			wantErr: false,
		},
		{
			name:    "empty patch",
			patch:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			patch:   "this is not a diff",
			wantErr: true,
		},
		{
			name:    "null byte in patch",
			patch:   "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\x00\n+new\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateDiffPatch(tt.patch)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDiffPatch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInputValidator_ValidateDiffPatch_SizeLimit(t *testing.T) {
	// Test with custom size limit
	opts := &InputValidatorOptions{
		MaxPathLen:    4096,
		MaxPatchLen:   1000, // 1KB limit
		MaxPatternLen: 1000,
	}
	v := NewInputValidator(opts)

	// Generate a valid patch header followed by content to exceed limit
	header := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n"
	bigContent := "+" + strings.Repeat("a", 2000)
	bigPatch := header + bigContent

	if err := v.ValidateDiffPatch(bigPatch); err == nil {
		t.Error("ValidateDiffPatch() with oversized patch = nil, want error")
	}
}

func TestInputValidator_ValidateSymbolID(t *testing.T) {
	v := NewInputValidator(nil)

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "valid simple ID",
			id:      "main.go:10:main",
			wantErr: false,
		},
		{
			name:    "valid nested ID",
			id:      "pkg/auth/handler.go:42:HandleLogin",
			wantErr: false,
		},
		{
			name:    "valid method ID",
			id:      "pkg/service.go:100:UserService.Create",
			wantErr: false,
		},
		{
			name:    "empty ID",
			id:      "",
			wantErr: true,
		},
		{
			name:    "ID with shell pipe",
			id:      "main.go:10:main|rm -rf",
			wantErr: true,
		},
		{
			name:    "ID with semicolon",
			id:      "main.go:10:main;ls",
			wantErr: true,
		},
		{
			name:    "ID with backtick",
			id:      "main.go:10:`whoami`",
			wantErr: true,
		},
		{
			name:    "ID with dollar sign",
			id:      "main.go:10:$USER",
			wantErr: true,
		},
		{
			name:    "ID with quotes",
			id:      `main.go:10:"test"`,
			wantErr: true,
		},
		{
			name:    "ID with null byte",
			id:      "main.go:10:main\x00",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateSymbolID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSymbolID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInputValidator_ValidateRegexPattern(t *testing.T) {
	v := NewInputValidator(nil)

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		// Valid patterns
		{
			name:    "simple pattern",
			pattern: "hello",
			wantErr: false,
		},
		{
			name:    "case insensitive",
			pattern: "(?i)login",
			wantErr: false,
		},
		{
			name:    "word boundary",
			pattern: `\bfunc\b`,
			wantErr: false,
		},
		{
			name:    "character class",
			pattern: "[a-zA-Z_][a-zA-Z0-9_]*",
			wantErr: false,
		},

		// Invalid patterns
		{
			name:    "empty pattern",
			pattern: "",
			wantErr: true,
		},
		{
			name:    "invalid regex syntax",
			pattern: "[unclosed",
			wantErr: true,
		},
		{
			name:    "invalid group",
			pattern: "(unclosed",
			wantErr: true,
		},

		// ReDoS-prone patterns
		{
			name:    "nested quantifiers",
			pattern: "(a+)+",
			wantErr: true,
		},
		{
			name:    "nested star quantifiers",
			pattern: "(a*)*",
			wantErr: true,
		},
		{
			name:    "dangerous dot star",
			pattern: "(.*)*",
			wantErr: true,
		},
		{
			name:    "adjacent wildcards",
			pattern: ".*.*",
			wantErr: true,
		},
		{
			name:    "adjacent plus wildcards",
			pattern: ".+.+",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateRegexPattern(tt.pattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRegexPattern() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInputValidator_ValidateRegexPattern_LengthLimit(t *testing.T) {
	opts := &InputValidatorOptions{
		MaxPathLen:    4096,
		MaxPatchLen:   1 << 20,
		MaxPatternLen: 50, // Short limit for testing
	}
	v := NewInputValidator(opts)

	// Pattern under limit should pass (if valid)
	shortPattern := "hello"
	if err := v.ValidateRegexPattern(shortPattern); err != nil {
		t.Errorf("ValidateRegexPattern() with short pattern = %v, want nil", err)
	}

	// Pattern over limit should fail
	longPattern := strings.Repeat("a", 51)
	if err := v.ValidateRegexPattern(longPattern); err == nil {
		t.Error("ValidateRegexPattern() with long pattern = nil, want error")
	}
}

func TestInputValidator_ValidateGitArgs(t *testing.T) {
	v := NewInputValidator(nil)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid log args",
			args:    []string{"log", "--oneline", "-n", "10"},
			wantErr: false,
		},
		{
			name:    "valid show args",
			args:    []string{"show", "HEAD", "--format=%H"},
			wantErr: false,
		},
		{
			name:    "valid file path",
			args:    []string{"log", "--", "src/main.go"},
			wantErr: false,
		},
		{
			name:    "empty arg",
			args:    []string{"log", "", "file.go"},
			wantErr: true,
		},
		{
			name:    "pipe injection",
			args:    []string{"log", "|", "rm -rf /"},
			wantErr: true,
		},
		{
			name:    "semicolon injection",
			args:    []string{"log", ";", "echo pwned"},
			wantErr: true,
		},
		{
			name:    "backtick injection",
			args:    []string{"log", "`whoami`"},
			wantErr: true,
		},
		{
			name:    "dollar sign injection",
			args:    []string{"log", "$HOME"},
			wantErr: true,
		},
		{
			name:    "null byte injection",
			args:    []string{"log", "file\x00.go"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateGitArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGitArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "path",
		Value:   "test.go",
		Reason:  "contains traversal",
		Details: "found '..'",
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "path") {
		t.Errorf("Error() should contain field name, got %s", errStr)
	}
	if !strings.Contains(errStr, "contains traversal") {
		t.Errorf("Error() should contain reason, got %s", errStr)
	}
}

func TestIsPathTraversal(t *testing.T) {
	v := NewInputValidator(nil)

	err := v.ValidateFilePath("../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}

	if !IsPathTraversal(err) {
		t.Errorf("IsPathTraversal() = false, want true for %v", err)
	}

	// Non-traversal error
	err = v.ValidateFilePath("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}

	if IsPathTraversal(err) {
		t.Errorf("IsPathTraversal() = true, want false for empty path error")
	}
}

func TestIsNullByte(t *testing.T) {
	v := NewInputValidator(nil)

	err := v.ValidateFilePath("file\x00.go")
	if err == nil {
		t.Fatal("expected error for null byte")
	}

	if !IsNullByte(err) {
		t.Errorf("IsNullByte() = false, want true for %v", err)
	}
}

func TestIsSizeLimit(t *testing.T) {
	opts := &InputValidatorOptions{
		MaxPathLen:    10,
		MaxPatchLen:   1 << 20,
		MaxPatternLen: 1000,
	}
	v := NewInputValidator(opts)

	err := v.ValidateFilePath(strings.Repeat("a", 20))
	if err == nil {
		t.Fatal("expected error for size limit")
	}

	if !IsSizeLimit(err) {
		t.Errorf("IsSizeLimit() = false, want true for %v", err)
	}
}

func TestDefaultInputValidatorOptions(t *testing.T) {
	opts := DefaultInputValidatorOptions()

	if opts.MaxPathLen != 4096 {
		t.Errorf("MaxPathLen = %d, want 4096", opts.MaxPathLen)
	}
	if opts.MaxPatchLen != 1<<20 {
		t.Errorf("MaxPatchLen = %d, want %d", opts.MaxPatchLen, 1<<20)
	}
	if opts.MaxPatternLen != 1000 {
		t.Errorf("MaxPatternLen = %d, want 1000", opts.MaxPatternLen)
	}
}

func TestNewInputValidator_NilOptions(t *testing.T) {
	v := NewInputValidator(nil)
	if v == nil {
		t.Fatal("NewInputValidator(nil) returned nil")
	}

	// Should use defaults
	if v.maxPathLen != 4096 {
		t.Errorf("maxPathLen = %d, want 4096", v.maxPathLen)
	}
}

// Benchmark tests
func BenchmarkValidateFilePath(b *testing.B) {
	v := NewInputValidator(nil)
	path := "services/trace/analysis/blast_radius.go"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ValidateFilePath(path)
	}
}

func BenchmarkValidateSymbolID(b *testing.B) {
	v := NewInputValidator(nil)
	id := "services/trace/analysis/blast_radius.go:42:Analyze"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ValidateSymbolID(id)
	}
}

func BenchmarkValidateRegexPattern(b *testing.B) {
	v := NewInputValidator(nil)
	pattern := "(?i)authenticate"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ValidateRegexPattern(pattern)
	}
}
