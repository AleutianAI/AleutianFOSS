// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package grounding

import (
	"context"
	"strings"
	"testing"
)

func TestPhantomPackageChecker_DetectsPhantomPackages(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The pkg/config package handles configuration and pkg/database stores data",
		KnownPackages: map[string]bool{
			"pkg/api_interaction": true,
			"pkg/calcs":           true,
			"cmd/orchestrator":    true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should detect two phantom packages
	if len(violations) != 2 {
		t.Errorf("Expected 2 violations, got %d", len(violations))
	}

	// Should identify pkg/config and pkg/database
	foundConfig, foundDatabase := false, false
	for _, v := range violations {
		if strings.Contains(v.Evidence, "pkg/config") {
			foundConfig = true
		}
		if strings.Contains(v.Evidence, "pkg/database") {
			foundDatabase = true
		}
	}
	if !foundConfig {
		t.Error("Should identify pkg/config as phantom")
	}
	if !foundDatabase {
		t.Error("Should identify pkg/database as phantom")
	}
}

func TestPhantomPackageChecker_ExemptsStdlib(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response:      "Uses fmt.Println and os.Getenv from stdlib",
		KnownPackages: map[string]bool{}, // Empty - no project packages
		ProjectLang:   "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should not flag stdlib even though not in KnownPackages
	if len(violations) != 0 {
		t.Errorf("Should exempt stdlib packages, got %d violations", len(violations))
	}
}

func TestPhantomPackageChecker_AcceptsKnownPackages(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The pkg/calcs package provides mathematical utilities",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should accept package that exists
	if len(violations) != 0 {
		t.Errorf("Should accept known package, got %d violations", len(violations))
	}
}

func TestPhantomPackageChecker_SkipsWhenNoKnownPackages(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response:      "The pkg/config package handles configuration",
		KnownPackages: nil, // No known packages data
		ProjectLang:   "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should skip validation when no KnownPackages available
	if len(violations) != 0 {
		t.Errorf("Should skip when no KnownPackages, got %d violations", len(violations))
	}
}

func TestPhantomPackageChecker_DetectsServicesPaths(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The services/trace package handles the agent and services/unknown does X",
		KnownPackages: map[string]bool{
			"services/trace":      true,
			"services/embeddings": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should detect services/unknown as phantom
	if len(violations) != 1 {
		t.Errorf("Expected 1 violation, got %d", len(violations))
	}

	if len(violations) > 0 && !strings.Contains(violations[0].Evidence, "services/unknown") {
		t.Errorf("Should identify services/unknown as phantom, got %s", violations[0].Evidence)
	}
}

func TestPhantomPackageChecker_AcceptsParentPackages(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The pkg directory contains utility packages",
		KnownPackages: map[string]bool{
			"pkg/calcs":           true,
			"pkg/api_interaction": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should accept "pkg" since it's a parent of known packages
	// Note: "pkg" alone is too short (< MinPackageLength 4) so would be skipped anyway
	if len(violations) != 0 {
		t.Errorf("Should accept parent package, got %d violations", len(violations))
	}
}

func TestPhantomPackageChecker_DetectsThePackagePattern(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "the config package handles configuration",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should detect "config" mentioned as a package (it matches looksLikePackage)
	if len(violations) != 1 {
		t.Errorf("Expected 1 violation for 'config' package reference, got %d", len(violations))
	}
}

func TestPhantomPackageChecker_DetectsInternalPaths(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The internal/utils package provides helpers",
		KnownPackages: map[string]bool{
			"internal/core": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should detect internal/utils as phantom
	if len(violations) != 1 {
		t.Errorf("Expected 1 violation, got %d", len(violations))
	}

	if len(violations) > 0 && !strings.Contains(violations[0].Evidence, "internal/utils") {
		t.Errorf("Should identify internal/utils as phantom, got %s", violations[0].Evidence)
	}
}

func TestPhantomPackageChecker_SuggestionIncludesAvailablePackages(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The pkg/config package handles configuration",
		KnownPackages: map[string]bool{
			"pkg/api_interaction": true,
			"pkg/calcs":           true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation, got %d", len(violations))
	}

	// Suggestion should include available packages
	if !strings.Contains(violations[0].Suggestion, "pkg/api_interaction") {
		t.Error("Suggestion should include pkg/api_interaction")
	}
	if !strings.Contains(violations[0].Suggestion, "pkg/calcs") {
		t.Error("Suggestion should include pkg/calcs")
	}
}

func TestPhantomPackageChecker_DisabledConfig(t *testing.T) {
	config := &PhantomPackageCheckerConfig{
		Enabled: false,
	}
	checker := NewPhantomPackageChecker(config)

	input := &CheckInput{
		Response: "The pkg/nonexistent package does stuff",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should return no violations when disabled
	if len(violations) != 0 {
		t.Errorf("Should return no violations when disabled, got %d", len(violations))
	}
}

func TestPhantomPackageChecker_Name(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)
	if checker.Name() != "phantom_package_checker" {
		t.Errorf("Expected name 'phantom_package_checker', got %s", checker.Name())
	}
}

func TestPhantomPackageChecker_ViolationType(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response: "The pkg/config package handles configuration",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation, got %d", len(violations))
	}

	if violations[0].Type != ViolationPhantomPackage {
		t.Errorf("Expected ViolationPhantomPackage, got %s", violations[0].Type)
	}
	if violations[0].Severity != SeverityCritical {
		t.Errorf("Expected SeverityCritical, got %s", violations[0].Severity)
	}
}

func TestDerivePackagesFromFiles(t *testing.T) {
	files := map[string]bool{
		"cmd/orchestrator/main.go":      true,
		"pkg/calcs/math.go":             true,
		"pkg/api_interaction/client.go": true,
	}

	packages := DerivePackagesFromFiles(files)

	expected := []string{
		"cmd/orchestrator",
		"cmd",
		"pkg/calcs",
		"pkg/api_interaction",
		"pkg",
	}

	for _, exp := range expected {
		if !packages[exp] {
			t.Errorf("Should derive package %q from files", exp)
		}
	}
}

func TestDerivePackagesFromFiles_NestedPaths(t *testing.T) {
	files := map[string]bool{
		"pkg/api/v1/handler.go": true,
	}

	packages := DerivePackagesFromFiles(files)

	// Should include all parent paths
	expected := []string{
		"pkg/api/v1",
		"pkg/api",
		"pkg",
	}

	for _, exp := range expected {
		if !packages[exp] {
			t.Errorf("Should derive parent package %q from nested file", exp)
		}
	}
}

func TestDerivePackagesFromFiles_EmptyInput(t *testing.T) {
	packages := DerivePackagesFromFiles(nil)
	if len(packages) != 0 {
		t.Errorf("Should return empty map for nil input, got %d packages", len(packages))
	}

	packages = DerivePackagesFromFiles(map[string]bool{})
	if len(packages) != 0 {
		t.Errorf("Should return empty map for empty input, got %d packages", len(packages))
	}
}

func TestDerivePackagesFromFiles_RootLevelFiles(t *testing.T) {
	files := map[string]bool{
		"main.go":   true,
		"README.md": true,
	}

	packages := DerivePackagesFromFiles(files)

	// Root level files shouldn't produce packages
	if len(packages) != 0 {
		t.Errorf("Should not derive packages from root-level files, got %d", len(packages))
	}
}

func TestPhantomPackageChecker_MaxPackagesToCheck(t *testing.T) {
	config := &PhantomPackageCheckerConfig{
		Enabled:            true,
		MaxPackagesToCheck: 1,
		MinPackageLength:   4,
		CheckGoPackages:    true,
	}
	checker := NewPhantomPackageChecker(config)

	input := &CheckInput{
		Response: "The pkg/config package and pkg/database package and pkg/logger package",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(context.Background(), input)

	// Should only check first package due to MaxPackagesToCheck=1
	if len(violations) > 1 {
		t.Errorf("Should limit to MaxPackagesToCheck, expected <=1, got %d", len(violations))
	}
}

func TestPhantomPackageChecker_PythonStdlib(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	input := &CheckInput{
		Response:      "Uses os.path and json.loads from stdlib",
		KnownPackages: map[string]bool{},
		ProjectLang:   "python",
	}

	violations := checker.Check(context.Background(), input)

	// Should not flag Python stdlib
	if len(violations) != 0 {
		t.Errorf("Should exempt Python stdlib packages, got %d violations", len(violations))
	}
}

func TestPhantomPackageChecker_ContextCancellation(t *testing.T) {
	checker := NewPhantomPackageChecker(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := &CheckInput{
		Response: "The pkg/config package handles configuration",
		KnownPackages: map[string]bool{
			"pkg/calcs": true,
		},
		ProjectLang: "go",
	}

	violations := checker.Check(ctx, input)

	// Should return early on context cancellation
	// (may have 0 or partial violations depending on timing)
	_ = violations // Just verify it doesn't panic
}
