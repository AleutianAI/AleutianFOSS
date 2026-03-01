// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/policy_engine"
)

// TestSeverity_AtLeast tests severity comparison.
func TestSeverity_AtLeast(t *testing.T) {
	tests := []struct {
		severity  Severity
		threshold Severity
		want      bool
	}{
		{SeverityCritical, SeverityHigh, true},
		{SeverityHigh, SeverityHigh, true},
		{SeverityMedium, SeverityHigh, false},
		{SeverityLow, SeverityMedium, false},
		{SeverityCritical, SeverityLow, true},
		{SeverityInfo, SeverityInfo, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity)+"_atleast_"+string(tt.threshold), func(t *testing.T) {
			if got := tt.severity.AtLeast(tt.threshold); got != tt.want {
				t.Errorf("Severity(%s).AtLeast(%s) = %v, want %v",
					tt.severity, tt.threshold, got, tt.want)
			}
		})
	}
}

// TestParseSeverity tests severity parsing.
func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  Severity
	}{
		{"critical", SeverityCritical},
		{"CRITICAL", SeverityCritical},
		{"high", SeverityHigh},
		{"HIGH", SeverityHigh},
		{"medium", SeverityMedium},
		{"low", SeverityLow},
		{"info", SeverityInfo},
		{"unknown", SeverityLow}, // Default
		{"", SeverityLow},        // Default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseSeverity(tt.input); got != tt.want {
				t.Errorf("ParseSeverity(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

// TestCollectFiles tests file collection with patterns.
func TestCollectFiles(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create test files
	testFiles := []string{
		"main.go",
		"util.go",
		"main_test.go",
		"README.md",
		"subdir/helper.go",
		"vendor/lib.go",
	}

	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	t.Run("all_files_recursive", func(t *testing.T) {
		files, err := collectFiles(tmpDir, true, nil, nil)
		if err != nil {
			t.Fatalf("collectFiles failed: %v", err)
		}
		if len(files) != len(testFiles) {
			t.Errorf("Got %d files, want %d", len(files), len(testFiles))
		}
	})

	t.Run("non_recursive", func(t *testing.T) {
		files, err := collectFiles(tmpDir, false, nil, nil)
		if err != nil {
			t.Fatalf("collectFiles failed: %v", err)
		}
		// Should only get root level files
		if len(files) > 4 {
			t.Errorf("Got %d files, want <= 4 (root level only)", len(files))
		}
	})

	t.Run("include_go_only", func(t *testing.T) {
		files, err := collectFiles(tmpDir, true, []string{"*.go"}, nil)
		if err != nil {
			t.Fatalf("collectFiles failed: %v", err)
		}
		// Should only get .go files
		for _, f := range files {
			if filepath.Ext(f) != ".go" {
				t.Errorf("Expected .go file, got %s", f)
			}
		}
	})

	t.Run("exclude_vendor", func(t *testing.T) {
		files, err := collectFiles(tmpDir, true, nil, []string{"vendor"})
		if err != nil {
			t.Fatalf("collectFiles failed: %v", err)
		}
		// Should not include vendor files
		for _, f := range files {
			if filepath.Base(filepath.Dir(f)) == "vendor" {
				t.Errorf("Vendor file should be excluded: %s", f)
			}
		}
	})

	t.Run("exclude_test_files", func(t *testing.T) {
		files, err := collectFiles(tmpDir, true, nil, []string{"*_test.go"})
		if err != nil {
			t.Fatalf("collectFiles failed: %v", err)
		}
		// Should not include test files
		for _, f := range files {
			if filepath.Base(f) == "main_test.go" {
				t.Errorf("Test file should be excluded: %s", f)
			}
		}
	})
}

// TestMatchesPatterns tests pattern matching, including all supported ** glob forms.
func TestMatchesPatterns(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		// Simple basename patterns
		{"main.go", []string{"*.go"}, true},
		{"main.go", []string{"*.py"}, false},
		{"path/to/file.txt", []string{"*.txt"}, true},
		{"path/to/file.txt", []string{"*.go", "*.py"}, false},
		{"file.go", []string{}, false},

		// **/suffix — match any depth
		{"vendor/lib.go", []string{"**/lib.go"}, true},
		{"vendor/lib/util.go", []string{"**/util.go"}, true},
		{"util.go", []string{"**/util.go"}, true},
		{"main.go", []string{"**/util.go"}, false},

		// prefix/** — match any path under prefix
		{"src/auth/auth.go", []string{"src/**"}, true},
		{"src/auth/internal/token.go", []string{"src/**"}, true},
		{"vendor/lib/lib.go", []string{"vendor/**"}, true},
		{"main.go", []string{"vendor/**"}, false},
		{"node_modules/pkg/index.js", []string{"node_modules/**"}, true},

		// **/*.ext — match any path with extension
		{"auth/auth_test.go", []string{"**/*_test.go"}, true},
		{"main_test.go", []string{"**/*_test.go"}, true},
		{"auth/auth.go", []string{"**/*_test.go"}, false},
		{"auth/auth.go", []string{"**/*.go"}, true},
		{"auth/auth.go", []string{"**/*.py"}, false},

		// Multiple patterns — match if any matches
		{"main.go", []string{"*.py", "*.go"}, true},
		{"vendor/pkg.go", []string{"vendor/**", "node_modules/**"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.path+"_"+strings.Join(tt.patterns, ","), func(t *testing.T) {
			if got := matchesPatterns(tt.path, tt.patterns); got != tt.want {
				t.Errorf("matchesPatterns(%q, %v) = %v, want %v",
					tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestIsBinaryFile tests binary file detection.
func TestIsBinaryFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create text file
	textPath := filepath.Join(tmpDir, "text.txt")
	if err := os.WriteFile(textPath, []byte("Hello, world!\n"), 0644); err != nil {
		t.Fatalf("Failed to write text file: %v", err)
	}

	// Create binary file (with null bytes)
	binaryPath := filepath.Join(tmpDir, "binary.bin")
	if err := os.WriteFile(binaryPath, []byte("Hello\x00World"), 0644); err != nil {
		t.Fatalf("Failed to write binary file: %v", err)
	}

	t.Run("text_file", func(t *testing.T) {
		if isBinaryFile(textPath) {
			t.Error("Text file detected as binary")
		}
	})

	t.Run("binary_file", func(t *testing.T) {
		if !isBinaryFile(binaryPath) {
			t.Error("Binary file not detected")
		}
	})

	t.Run("binary_extension", func(t *testing.T) {
		exePath := filepath.Join(tmpDir, "test.exe")
		if err := os.WriteFile(exePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to write exe file: %v", err)
		}
		if !isBinaryFile(exePath) {
			t.Error("Exe file not detected as binary")
		}
	})
}

// TestScanSingleFile tests single file scanning.
func TestScanSingleFile(t *testing.T) {
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		t.Fatalf("Failed to create policy engine: %v", err)
	}

	tmpDir := t.TempDir()

	t.Run("file_with_secret", func(t *testing.T) {
		// Create file with potential secret
		path := filepath.Join(tmpDir, "secret.go")
		content := `package main
const AWS_SECRET_ACCESS_KEY = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		violations, skipped, warning := scanSingleFile(path, engine, DefaultMaxFileSize)
		if skipped {
			t.Error("File should not be skipped")
		}
		if warning != "" {
			t.Errorf("Unexpected warning: %s", warning)
		}
		// Note: Whether we find violations depends on the actual policy patterns
		t.Logf("Found %d violations", len(violations))
	})

	t.Run("clean_file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "clean.go")
		content := `package main

func main() {
    fmt.Println("Hello, world!")
}
`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		violations, skipped, _ := scanSingleFile(path, engine, DefaultMaxFileSize)
		if skipped {
			t.Error("File should not be skipped")
		}
		if len(violations) > 0 {
			t.Errorf("Expected no violations, got %d", len(violations))
		}
	})

	t.Run("large_file_skipped", func(t *testing.T) {
		path := filepath.Join(tmpDir, "large.go")
		// Create content larger than 100 bytes
		content := make([]byte, 200)
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		_, skipped, _ := scanSingleFile(path, engine, 100)
		if !skipped {
			t.Error("Large file should be skipped")
		}
	})
}

// TestScanFilesParallel tests parallel file scanning.
func TestScanFilesParallel(t *testing.T) {
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		t.Fatalf("Failed to create policy engine: %v", err)
	}

	tmpDir := t.TempDir()

	// Create multiple test files
	for i := 0; i < 10; i++ {
		path := filepath.Join(tmpDir, "file"+string(rune('0'+i))+".go")
		content := "package main\nfunc main() {}\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	files, _ := collectFiles(tmpDir, true, nil, nil)

	ctx := context.Background()
	violations, scanned, skipped, warnings := scanFilesParallel(ctx, files, engine, 4, DefaultMaxFileSize)

	if scanned != len(files) {
		t.Errorf("Scanned %d files, want %d", scanned, len(files))
	}
	if skipped != 0 {
		t.Errorf("Skipped %d files, want 0", skipped)
	}
	if len(warnings) != 0 {
		t.Errorf("Got %d warnings, want 0", len(warnings))
	}
	t.Logf("Found %d violations in %d files", len(violations), scanned)
}

// TestConfidenceToSeverity tests confidence to severity mapping.
func TestConfidenceToSeverity(t *testing.T) {
	tests := []struct {
		classification string
		confidence     policy_engine.ConfidenceLevel
		want           Severity
	}{
		{"Secret", policy_engine.High, SeverityCritical},
		{"Secret", policy_engine.Medium, SeverityHigh},
		{"Password", policy_engine.High, SeverityCritical},
		{"PII", policy_engine.High, SeverityHigh},
		{"PII", policy_engine.Medium, SeverityMedium},
		{"Other", policy_engine.High, SeverityHigh},
		{"Other", policy_engine.Medium, SeverityMedium},
		{"Other", policy_engine.Low, SeverityLow},
	}

	for _, tt := range tests {
		t.Run(tt.classification+"_"+string(tt.confidence), func(t *testing.T) {
			if got := confidenceToSeverity(tt.classification, tt.confidence); got != tt.want {
				t.Errorf("confidenceToSeverity(%s, %s) = %s, want %s",
					tt.classification, tt.confidence, got, tt.want)
			}
		})
	}
}

// TestCheckResult_New tests result creation.
func TestCheckResult_New(t *testing.T) {
	result := NewCheckResult()

	if result.Violations == nil {
		t.Error("Violations should be initialized")
	}
	if result.ViolationCounts == nil {
		t.Error("ViolationCounts should be initialized")
	}
	if result.Warnings == nil {
		t.Error("Warnings should be initialized")
	}
}
