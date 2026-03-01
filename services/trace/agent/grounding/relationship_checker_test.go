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
	"testing"
)

func TestRelationshipChecker_Name(t *testing.T) {
	checker := NewRelationshipChecker(nil)
	if checker.Name() != "relationship_checker" {
		t.Errorf("expected name 'relationship_checker', got %q", checker.Name())
	}
}

func TestRelationshipChecker_Disabled(t *testing.T) {
	config := &RelationshipCheckerConfig{Enabled: false}
	checker := NewRelationshipChecker(config)

	input := &CheckInput{
		Response: "ProcessData calls SaveToDatabase",
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when disabled, got %d", len(violations))
	}
}

func TestRelationshipChecker_NilInput(t *testing.T) {
	checker := NewRelationshipChecker(nil)
	violations := checker.Check(context.Background(), nil)
	if len(violations) != 0 {
		t.Errorf("expected no violations for nil input, got %d", len(violations))
	}
}

func TestRelationshipChecker_EmptyResponse(t *testing.T) {
	checker := NewRelationshipChecker(nil)
	input := &CheckInput{
		Response:      "",
		EvidenceIndex: NewEvidenceIndex(),
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for empty response, got %d", len(violations))
	}
}

func TestRelationshipChecker_NoEvidenceIndex(t *testing.T) {
	checker := NewRelationshipChecker(nil)
	input := &CheckInput{
		Response: "ProcessData calls SaveToDatabase",
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations without evidence index, got %d", len(violations))
	}
}

func TestRelationshipChecker_ImportExists(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["auth.go"] = []ImportInfo{
		{Path: "crypto/sha256", Alias: "sha256"},
		{Path: "github.com/jwt/jwt-go", Alias: "jwt"},
	}

	input := &CheckInput{
		Response:      "auth.go imports jwt for token handling.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for existing import, got %d", len(violations))
	}
}

func TestRelationshipChecker_ImportMissing(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["auth.go"] = []ImportInfo{
		{Path: "crypto/sha256", Alias: "sha256"},
		{Path: "github.com/jwt/jwt-go", Alias: "jwt"},
	}

	input := &CheckInput{
		Response:      "auth.go imports database for persistence.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing import, got %d", len(violations))
	}

	v := violations[0]
	if v.Type != ViolationRelationshipHallucination {
		t.Errorf("expected ViolationRelationshipHallucination, got %v", v.Type)
	}
	if v.Code != "IMPORT_NOT_FOUND" {
		t.Errorf("expected code IMPORT_NOT_FOUND, got %q", v.Code)
	}
}

func TestRelationshipChecker_ImportAliased(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["handler.go"] = []ImportInfo{
		{Path: "github.com/pkg/errors", Alias: "pkgerrors"},
	}

	input := &CheckInput{
		Response:      "handler imports pkgerrors for error handling.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for aliased import, got %d", len(violations))
	}
}

func TestRelationshipChecker_ImportByPath(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["service.go"] = []ImportInfo{
		{Path: "github.com/pkg/errors", Alias: "errors"},
	}

	input := &CheckInput{
		Response:      "service uses github.com/pkg/errors for error wrapping.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for import by path, got %d", len(violations))
	}
}

func TestRelationshipChecker_CallExists(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["ProcessData"] = []string{"ValidateInput", "Transform", "Save"}
	idx.Symbols["Save"] = true

	input := &CheckInput{
		Response:      "ProcessData calls Save to persist the data.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for existing call, got %d", len(violations))
	}
}

func TestRelationshipChecker_CallMissing(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["ProcessData"] = []string{"ValidateInput", "Transform"}
	idx.Symbols["SaveToDatabase"] = true

	input := &CheckInput{
		Response:      "ProcessData calls SaveToDatabase to persist the data.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing call, got %d", len(violations))
	}

	v := violations[0]
	if v.Type != ViolationRelationshipHallucination {
		t.Errorf("expected ViolationRelationshipHallucination, got %v", v.Type)
	}
	if v.Code != "CALL_NOT_FOUND" {
		t.Errorf("expected code CALL_NOT_FOUND, got %q", v.Code)
	}
}

func TestRelationshipChecker_CallerNotInEvidence(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["OtherFunction"] = []string{"Helper"}
	idx.Symbols["SaveToDatabase"] = true

	input := &CheckInput{
		Response:      "ProcessData calls SaveToDatabase to persist the data.",
		EvidenceIndex: idx,
	}

	// ProcessData is not in CallsWithin, so skip validation
	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when caller not in evidence, got %d", len(violations))
	}
}

func TestRelationshipChecker_CalleeNotInEvidence(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["ProcessData"] = []string{"ValidateInput", "Transform"}
	// SaveToDatabase is NOT in Symbols

	input := &CheckInput{
		Response:      "ProcessData calls SaveToDatabase to persist the data.",
		EvidenceIndex: idx,
	}

	// SaveToDatabase is not in Symbols, so skip validation
	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when callee not in evidence, got %d", len(violations))
	}
}

func TestRelationshipChecker_BidirectionalCallClaim(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["Handler"] = []string{"Process", "Respond"}
	idx.Symbols["Process"] = true

	// Reverse claim: "B is called by A"
	input := &CheckInput{
		Response:      "Process is called by Handler.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations for reverse call claim, got %d", len(violations))
	}
}

func TestRelationshipChecker_BidirectionalCallClaimMissing(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.CallsWithin["Handler"] = []string{"Process", "Respond"}
	idx.Symbols["Missing"] = true

	// Reverse claim: "B is called by A" but B is not called
	input := &CheckInput{
		Response:      "Missing is called by Handler.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing reverse call, got %d", len(violations))
	}
}

func TestRelationshipChecker_SubjectNotInEvidence(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["known.go"] = []ImportInfo{
		{Path: "fmt", Alias: "fmt"},
	}

	// unknown_file is not in imports keys
	input := &CheckInput{
		Response:      "unknown_file imports database",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when subject not in evidence, got %d", len(violations))
	}
}

func TestRelationshipChecker_NoCallsData(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	// No CallsWithin data

	input := &CheckInput{
		Response:      "ProcessData calls SaveToDatabase",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when no call data, got %d", len(violations))
	}
}

func TestRelationshipChecker_NoImportsData(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	// No Imports data

	input := &CheckInput{
		Response:      "auth imports database",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 0 {
		t.Errorf("expected no violations when no import data, got %d", len(violations))
	}
}

func TestRelationshipChecker_ContextCancellation(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["service.go"] = []ImportInfo{{Path: "fmt", Alias: "fmt"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := &CheckInput{
		Response:      "service.go imports missing1. service.go imports missing2.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(ctx, input)
	// Should return early due to cancellation
	if len(violations) > 2 {
		t.Errorf("unexpected number of violations after cancellation: %d", len(violations))
	}
}

func TestRelationshipChecker_MaxRelationshipsLimit(t *testing.T) {
	config := &RelationshipCheckerConfig{
		Enabled:                 true,
		ValidateImports:         true,
		ValidateCalls:           true,
		MaxRelationshipsToCheck: 2,
	}
	checker := NewRelationshipChecker(config)

	idx := NewEvidenceIndex()
	idx.Imports["service.go"] = []ImportInfo{{Path: "fmt", Alias: "fmt"}}

	// Response with many import claims
	input := &CheckInput{
		Response:      "service.go imports missing1. service.go imports missing2. service.go imports missing3. service.go imports missing4.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	// Should only check first 2 claims
	if len(violations) > 2 {
		t.Errorf("expected at most 2 violations with limit, got %d", len(violations))
	}
}

func TestRelationshipChecker_MultipleViolations(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["auth.go"] = []ImportInfo{
		{Path: "fmt", Alias: "fmt"},
	}
	idx.CallsWithin["Handler"] = []string{"Process"}
	idx.Symbols["Missing"] = true

	input := &CheckInput{
		Response:      "auth imports database. Handler calls Missing.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	if len(violations) != 2 {
		t.Errorf("expected 2 violations, got %d", len(violations))
	}
}

func TestRelationshipChecker_DedupesSameClaim(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["auth.go"] = []ImportInfo{{Path: "fmt", Alias: "fmt"}}

	// Same claim twice should only produce one violation
	input := &CheckInput{
		Response:      "auth imports database. Also, auth imports database.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	// Deduplication should prevent duplicate violations
	if len(violations) > 1 {
		t.Errorf("expected at most 1 violation due to dedup, got %d", len(violations))
	}
}

func TestRelationshipChecker_ExtractRelationshipClaims(t *testing.T) {
	checker := NewRelationshipChecker(nil)

	tests := []struct {
		name     string
		response string
		count    int
	}{
		{"call forward", "ProcessData calls SaveToDatabase", 1},
		{"call reverse", "SaveToDatabase is called by ProcessData", 1},
		{"call invokes", "Handler invokes Process", 1},
		{"import forward", "auth imports jwt", 1},
		{"import reverse", "jwt is imported by auth", 1},
		{"import uses", "service uses database", 1},
		{"multiple", "A calls B. C imports D.", 2},
		{"no claims", "This is just a description.", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := checker.extractRelationshipClaims(tt.response)
			if len(claims) != tt.count {
				t.Errorf("expected %d claims, got %d", tt.count, len(claims))
			}
		})
	}
}

func TestRelationshipChecker_Priority(t *testing.T) {
	if ViolationTypeToPriority(ViolationRelationshipHallucination) != PriorityRelationshipHallucination {
		t.Error("ViolationRelationshipHallucination should map to PriorityRelationshipHallucination")
	}

	if PriorityRelationshipHallucination != 2 {
		t.Errorf("expected PriorityRelationshipHallucination to be 2, got %d", PriorityRelationshipHallucination)
	}
}

func TestRelationshipChecker_Integration(t *testing.T) {
	// Integration test with realistic input - uses camelCase to verify lowercase support
	checker := NewRelationshipChecker(nil)

	idx := NewEvidenceIndex()
	idx.Imports["services/trace/agent/grounding/grounder.go"] = []ImportInfo{
		{Path: "context", Alias: "context"},
		{Path: "fmt", Alias: "fmt"},
		{Path: "strings", Alias: "strings"},
	}
	idx.CallsWithin["buildEvidenceIndex"] = []string{"normalizePath", "extractSymbols", "extractSymbolDetails"}
	idx.Symbols["extractSymbols"] = true
	idx.Symbols["extractSymbolDetails"] = true
	idx.Symbols["InvalidFunction"] = true

	input := &CheckInput{
		Response: `grounder.go imports context and fmt for its operations.
buildEvidenceIndex calls extractSymbols to parse code.
buildEvidenceIndex calls InvalidFunction for error handling.`,
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)

	// Should find violation for InvalidFunction call (not in buildEvidenceIndex's calls)
	found := false
	for _, v := range violations {
		if v.Type == ViolationRelationshipHallucination && v.Code == "CALL_NOT_FOUND" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected to find a CALL_NOT_FOUND violation for InvalidFunction")
	}
}

func TestRelationshipChecker_ValidatesOnlyEnabled(t *testing.T) {
	// Only imports enabled
	config := &RelationshipCheckerConfig{
		Enabled:         true,
		ValidateImports: true,
		ValidateCalls:   false,
	}
	checker := NewRelationshipChecker(config)

	idx := NewEvidenceIndex()
	idx.Imports["service.go"] = []ImportInfo{{Path: "fmt", Alias: "fmt"}}
	idx.CallsWithin["Func"] = []string{"Helper"}
	idx.Symbols["Missing"] = true

	input := &CheckInput{
		Response:      "service.go imports missing. Func calls Missing.",
		EvidenceIndex: idx,
	}

	violations := checker.Check(context.Background(), input)
	// Should only have import violation, not call violation
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].Code != "IMPORT_NOT_FOUND" {
		t.Errorf("expected IMPORT_NOT_FOUND, got %q", violations[0].Code)
	}
}

// Test extractImports helper function
func TestExtractGoImports(t *testing.T) {
	content := `package main

import (
	"context"
	"fmt"
	pkgerrors "github.com/pkg/errors"
)

import "strings"

func main() {}
`
	imports := extractGoImports(content)

	if len(imports) != 4 {
		t.Fatalf("expected 4 imports, got %d", len(imports))
	}

	// Check specific imports
	foundContext := false
	foundPkgErrors := false
	foundStrings := false
	for _, imp := range imports {
		if imp.Path == "context" && imp.Alias == "context" {
			foundContext = true
		}
		if imp.Path == "github.com/pkg/errors" && imp.Alias == "pkgerrors" {
			foundPkgErrors = true
		}
		if imp.Path == "strings" && imp.Alias == "strings" {
			foundStrings = true
		}
	}

	if !foundContext {
		t.Error("expected to find context import")
	}
	if !foundPkgErrors {
		t.Error("expected to find pkgerrors import")
	}
	if !foundStrings {
		t.Error("expected to find strings import")
	}
}

func TestExtractPythonImports(t *testing.T) {
	content := `import os
import sys as system
from flask import Flask, request
from mymodule import helper

def main():
    pass
`
	imports := extractPythonImports(content)

	if len(imports) != 4 {
		t.Fatalf("expected 4 imports, got %d", len(imports))
	}

	// Check specific imports
	foundOs := false
	foundSys := false
	foundFlask := false
	for _, imp := range imports {
		if imp.Path == "os" {
			foundOs = true
		}
		if imp.Path == "sys" && imp.Alias == "system" {
			foundSys = true
		}
		if imp.Path == "flask" {
			foundFlask = true
		}
	}

	if !foundOs {
		t.Error("expected to find os import")
	}
	if !foundSys {
		t.Error("expected to find sys import with alias")
	}
	if !foundFlask {
		t.Error("expected to find flask import")
	}
}

func TestExtractGoFunctionCalls(t *testing.T) {
	content := `package main

func ProcessData(input string) error {
	validated := ValidateInput(input)
	result := Transform(validated)
	return Save(result)
}

func ValidateInput(s string) string {
	return strings.TrimSpace(s)
}
`
	calls := extractGoFunctionCalls(content)

	processCalls, ok := calls["ProcessData"]
	if !ok {
		t.Fatal("expected ProcessData in call graph")
	}

	// Should find ValidateInput, Transform, Save
	found := make(map[string]bool)
	for _, call := range processCalls {
		found[call] = true
	}

	if !found["ValidateInput"] {
		t.Error("expected ProcessData to call ValidateInput")
	}
	if !found["Transform"] {
		t.Error("expected ProcessData to call Transform")
	}
	if !found["Save"] {
		t.Error("expected ProcessData to call Save")
	}
}
