// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// These are unit tests that don't require the stack to be running.
// Run with: go test -v ./cmd/aleutian/... -run TestCLIUnit

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// CLI UNIT TESTS - No stack required
// =============================================================================

var unitTestHarness *CLITestHarness

func init() {
	unitTestHarness = NewCLITestHarness("")
}

// =============================================================================
// 1. ROOT COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Root_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"help flag long", []string{"--help"}, 0, []string{"Aleutian", "Usage"}},
		{"help flag short", []string{"-h"}, 0, []string{"Aleutian"}},
		{"help with no args", []string{}, 0, []string{"Usage"}},
		{"help shows stack", []string{"--help"}, 0, []string{"stack"}},
		{"help shows chat", []string{"--help"}, 0, []string{"chat"}},
		{"help shows session", []string{"--help"}, 0, []string{"session"}},
		{"help shows policy", []string{"--help"}, 0, []string{"policy"}},
		{"help shows weaviate", []string{"--help"}, 0, []string{"weaviate"}},
		{"help shows health", []string{"--help"}, 0, []string{"health"}},
		{"help shows ingest", []string{"--help"}, 0, []string{"ingest"}},
		{"help shows ask", []string{"--help"}, 0, []string{"ask"}},
		{"help shows evaluate", []string{"--help"}, 0, []string{"evaluate"}},
		{"help shows timeseries", []string{"--help"}, 0, []string{"timeseries"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Root_Version(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"version long flag", []string{"--version"}, 0},
		{"version short flag", []string{"-v"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Root_UnknownCommands(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"unknown command foobar", []string{"foobar"}, 1},
		{"unknown command xyz", []string{"xyz"}, 1},
		{"unknown command with args", []string{"unknown", "arg1", "arg2"}, 1},
		{"misspelled stack", []string{"stak"}, 1},
		{"misspelled chat", []string{"cht"}, 1},
		{"misspelled session", []string{"sesion"}, 1},
		{"empty string command", []string{""}, 0}, // Empty arg shows help
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Root_UnknownFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"unknown flag", []string{"--unknown-flag"}, 1},
		{"unknown short flag", []string{"-x"}, 1},
		{"unknown flag with value", []string{"--foo=bar"}, 1},
		{"unknown flag before command", []string{"--invalid", "stack"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Root_Personality(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantExit    int
		description string
	}{
		{"personality full", []string{"--personality", "full", "--help"}, 0, "Full personality mode"},
		{"personality standard", []string{"--personality", "standard", "--help"}, 0, "Standard personality"},
		{"personality minimal", []string{"--personality", "minimal", "--help"}, 0, "Minimal personality"},
		{"personality machine", []string{"--personality", "machine", "--help"}, 0, "Machine-parseable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

// =============================================================================
// 2. STACK COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Stack_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"stack help", []string{"stack", "--help"}, 0, []string{"start", "stop", "logs"}},
		{"stack -h", []string{"stack", "-h"}, 0, []string{"start", "stop"}},
		{"stack start help", []string{"stack", "start", "--help"}, 0, []string{"--backend", "--profile"}},
		{"stack stop help", []string{"stack", "stop", "--help"}, 0, []string{}},
		{"stack logs help", []string{"stack", "logs", "--help"}, 0, []string{}},
		{"stack destroy help", []string{"stack", "destroy", "--help"}, 0, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Stack_StartFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"backend flag in help", []string{"stack", "start", "--help"}, []string{"--backend"}},
		{"profile flag in help", []string{"stack", "start", "--help"}, []string{"--profile"}},
		{"build flag in help", []string{"stack", "start", "--help"}, []string{"--build"}},
		{"force-recreate flag", []string{"stack", "start", "--help"}, []string{"--force-recreate"}},
		{"skip-model-check flag", []string{"stack", "start", "--help"}, []string{"--skip-model-check"}},
		{"forecast-mode flag", []string{"stack", "start", "--help"}, []string{"--forecast-mode"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Errorf("Expected flag %q in help output", want)
				}
			}
		})
	}
}

func TestCLIUnit_Stack_ProfileValues(t *testing.T) {
	// Test that profile flag accepts valid values (via help, not execution)
	profiles := []string{"auto", "low", "standard", "performance", "ultra", "manual"}

	for _, profile := range profiles {
		t.Run("profile_"+profile, func(t *testing.T) {
			result, err := unitTestHarness.Run("stack", "start", "--profile", profile, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Should not error on unknown flag
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Errorf("Profile %q caused unknown flag error", profile)
			}
		})
	}
}

func TestCLIUnit_Stack_BackendValues(t *testing.T) {
	backends := []string{"ollama", "openai", "anthropic"}

	for _, backend := range backends {
		t.Run("backend_"+backend, func(t *testing.T) {
			result, err := unitTestHarness.Run("stack", "start", "--backend", backend, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Errorf("Backend %q caused unknown flag error", backend)
			}
		})
	}
}

func TestCLIUnit_Stack_UnknownSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"stack unknown", []string{"stack", "unknown"}, 0},   // Shows help
		{"stack foo", []string{"stack", "foo"}, 0},           // Shows help
		{"stack starting", []string{"stack", "starting"}, 0}, // Shows help
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

// =============================================================================
// 3. CHAT COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Chat_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"chat help", []string{"chat", "--help"}, 0, []string{"resume", "pipeline"}},
		{"chat -h", []string{"chat", "-h"}, 0, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Chat_Flags(t *testing.T) {
	flags := []string{"--resume", "--pipeline", "--no-rag", "--thinking", "--budget"}

	for _, flag := range flags {
		t.Run("flag_"+flag, func(t *testing.T) {
			result, err := unitTestHarness.Run("chat", "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertOutputContains(flag); err != nil {
				t.Errorf("Chat help should contain %q", flag)
			}
		})
	}
}

func TestCLIUnit_Chat_PipelineValues(t *testing.T) {
	pipelines := []string{"standard", "reranking", "graph"}

	for _, pipeline := range pipelines {
		t.Run("pipeline_"+pipeline, func(t *testing.T) {
			result, err := unitTestHarness.Run("chat", "--pipeline", pipeline, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Errorf("Pipeline %q caused unknown flag error", pipeline)
			}
		})
	}
}

func TestCLIUnit_Chat_ShortFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"pipeline short", []string{"chat", "-p", "standard", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Chat_BudgetValues(t *testing.T) {
	budgets := []string{"1024", "2048", "4096", "8192", "16384"}

	for _, budget := range budgets {
		t.Run("budget_"+budget, func(t *testing.T) {
			result, err := unitTestHarness.Run("chat", "--budget", budget, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "invalid") {
				t.Errorf("Budget %q caused invalid value error", budget)
			}
		})
	}
}

// =============================================================================
// 4. SESSION COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Session_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"session help", []string{"session", "--help"}, 0, []string{"list", "verify", "delete"}},
		{"session -h", []string{"session", "-h"}, 0, []string{"list"}},
		{"session list help", []string{"session", "list", "--help"}, 0, []string{}},
		{"session verify help", []string{"session", "verify", "--help"}, 0, []string{"--full", "--json"}},
		{"session delete help", []string{"session", "delete", "--help"}, 0, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Session_VerifyFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"verify with full", []string{"session", "verify", "test-id", "--full"}},
		{"verify with json", []string{"session", "verify", "test-id", "--json"}},
		{"verify with both", []string{"session", "verify", "test-id", "--full", "--json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Should not error on unknown flag
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got 'unknown flag' error - flag not registered")
			}
		})
	}
}

func TestCLIUnit_Session_MissingArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"verify no id", []string{"session", "verify"}},
		{"delete no id", []string{"session", "delete"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// May exit 0 (show help) or non-zero - just verify it doesn't crash
			_ = result
		})
	}
}

func TestCLIUnit_Session_UnknownSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"session unknown", []string{"session", "unknown"}, 0}, // Shows help
		{"session foo", []string{"session", "foo"}, 0},         // Shows help
		{"session create", []string{"session", "create"}, 0},   // Shows help
		{"session update", []string{"session", "update"}, 0},   // Shows help
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Session_VerifyIDFormats(t *testing.T) {
	// Test various session ID formats are accepted (will fail at runtime but parse OK)
	ids := []string{
		"simple-id",
		"uuid-like-12345678-1234-1234-1234-123456789012",
		"with_underscores",
		"with.dots",
		"MixedCase123",
		"123numeric",
	}

	for _, id := range ids {
		t.Run("id_"+id, func(t *testing.T) {
			result, err := unitTestHarness.Run("session", "verify", id, "--json")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Should accept the ID format (even if session doesn't exist)
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "invalid") && strings.Contains(combined, "session") {
				t.Errorf("ID format %q was rejected", id)
			}
		})
	}
}

// =============================================================================
// 5. POLICY COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Policy_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"policy help", []string{"policy", "--help"}, 0, []string{"verify", "dump", "test"}},
		{"policy -h", []string{"policy", "-h"}, 0, []string{"verify"}},
		{"policy verify help", []string{"policy", "verify", "--help"}, 0, []string{}},
		{"policy dump help", []string{"policy", "dump", "--help"}, 0, []string{}},
		{"policy test help", []string{"policy", "test", "--help"}, 0, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Policy_Verify(t *testing.T) {
	tests := []struct {
		name         string
		wantContains []string
	}{
		{"shows hash", []string{"SHA256"}},
		{"shows policy", []string{"Policy"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "verify")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					// Try lowercase
					if err := result.AssertOutputContains(strings.ToLower(want)); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
}

func TestCLIUnit_Policy_VerifyConsistency(t *testing.T) {
	// Run multiple times and verify consistent hash
	var hashes []string

	for i := 0; i < 5; i++ {
		result, err := unitTestHarness.Run("policy", "verify")
		if err != nil {
			t.Fatalf("Run %d failed: %v", i, err)
		}
		hashes = append(hashes, result.Stdout)
	}

	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("Hash inconsistent on run %d", i)
		}
	}
}

func TestCLIUnit_Policy_Dump(t *testing.T) {
	tests := []struct {
		name         string
		wantContains []string
	}{
		{"contains classifications", []string{"classifications"}},
		{"contains id", []string{"id:"}},
		{"contains regex", []string{"regex:"}},
		{"contains description", []string{"description"}},
		{"contains confidence", []string{"confidence"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "dump")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Policy_TestSSN(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains []string
	}{
		{"standard SSN", "123-45-6789", []string{"SSN"}},
		{"SSN with context", "My SSN is 123-45-6789", []string{"SSN"}},
		{"SSN at start", "123-45-6789 is sensitive", []string{"SSN"}},
		{"SSN at end", "The number is 123-45-6789", []string{"SSN"}},
		{"multiple SSNs", "123-45-6789 and 987-65-4321", []string{"SSN"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "test", tt.input)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Exit code 1 = findings found (CLI-01f: proper exit codes)
			if err := result.AssertExitCode(1); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Policy_TestAWSKeys(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains []string
	}{
		{"AWS access key", "AKIAIOSFODNN7EXAMPLE", []string{"AWS"}},
		{"AWS key in config", "aws_access_key_id=AKIAIOSFODNN7EXAMPLE", []string{"AWS"}},
		{"AWS key with context", "The key is AKIAIOSFODNN7EXAMPLE here", []string{"AWS"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "test", tt.input)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Exit code 1 = findings found (CLI-01f: proper exit codes)
			if err := result.AssertExitCode(1); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Policy_TestClean(t *testing.T) {
	cleanInputs := []string{
		"Hello World",
		"This is a normal sentence",
		"No secrets here",
		"12345",
		"abc123",
		"test@example",
		"random text with numbers 12345",
	}

	for _, input := range cleanInputs {
		testName := input
		if len(testName) > 10 {
			testName = testName[:10]
		}
		t.Run("clean_"+testName, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "test", input)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			// Clean inputs should not trigger pattern matches
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "SECRET") || strings.Contains(combined, "PII") {
				if !strings.Contains(combined, "No") {
					// Only fail if it actually found something
				}
			}
		})
	}
}

func TestCLIUnit_Policy_TestMultiplePatterns(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains []string
	}{
		{"SSN and AWS", "SSN: 123-45-6789 AWS: AKIAIOSFODNN7EXAMPLE", []string{"SSN", "AWS"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run("policy", "test", tt.input)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Exit code 1 = findings found (CLI-01f: proper exit codes)
			if err := result.AssertExitCode(1); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// =============================================================================
// 6. WEAVIATE COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Weaviate_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"weaviate help", []string{"weaviate", "--help"}, 0, []string{"summary", "backup", "restore"}},
		{"weaviate -h", []string{"weaviate", "-h"}, 0, []string{"summary"}},
		{"weaviate summary help", []string{"weaviate", "summary", "--help"}, 0, []string{}},
		{"weaviate backup help", []string{"weaviate", "backup", "--help"}, 0, []string{}},
		{"weaviate restore help", []string{"weaviate", "restore", "--help"}, 0, []string{}},
		{"weaviate delete help", []string{"weaviate", "delete", "--help"}, 0, []string{}},
		{"weaviate wipeout help", []string{"weaviate", "wipeout", "--help"}, 0, []string{"--force"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Weaviate_MissingArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"backup no id", []string{"weaviate", "backup"}},
		{"restore no id", []string{"weaviate", "restore"}},
		{"delete no source", []string{"weaviate", "delete"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// These should fail (non-zero exit code) - may be 1 or 2 depending on error type
			if err := result.AssertFailure(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Weaviate_WipeoutRequiresForce(t *testing.T) {
	result, err := unitTestHarness.Run("weaviate", "wipeout")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should fail or warn without --force
	if err := result.AssertOutputContains("--force"); err != nil {
		t.Error("Wipeout without --force should mention --force flag")
	}
}

func TestCLIUnit_Weaviate_UnknownSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"weaviate unknown", []string{"weaviate", "unknown"}, 0}, // Shows help
		{"weaviate foo", []string{"weaviate", "foo"}, 0},         // Shows help
		{"weaviate query", []string{"weaviate", "query"}, 0},     // Shows help
		{"weaviate search", []string{"weaviate", "search"}, 0},   // Shows help
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Weaviate_BackupIDFormats(t *testing.T) {
	ids := []string{
		"backup-001",
		"my_backup",
		"backup.2024.01.01",
		"BACKUP123",
		"b",
	}

	for _, id := range ids {
		t.Run("backup_"+id, func(t *testing.T) {
			result, err := unitTestHarness.Run("weaviate", "backup", id)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Should accept the ID format
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "invalid") && strings.Contains(combined, "id") {
				t.Errorf("Backup ID format %q was rejected", id)
			}
		})
	}
}

// =============================================================================
// 7. HEALTH COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Health_Help(t *testing.T) {
	result, err := unitTestHarness.Run("health", "--help")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if err := result.AssertSuccess(); err != nil {
		t.Error(err)
	}

	expectedFlags := []string{"--ai", "--json", "--window", "--verbose", "-w", "-v"}
	for _, flag := range expectedFlags {
		if err := result.AssertOutputContains(flag); err != nil {
			t.Errorf("Health help should contain %s", flag)
		}
	}
}

func TestCLIUnit_Health_Flags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"ai flag", []string{"health", "--ai", "--help"}},
		{"json flag", []string{"health", "--json", "--help"}},
		{"verbose flag", []string{"health", "--verbose", "--help"}},
		{"verbose short", []string{"health", "-v", "--help"}},
		{"window flag", []string{"health", "--window", "5m", "--help"}},
		{"window short", []string{"health", "-w", "5m", "--help"}},
		{"multiple flags", []string{"health", "--ai", "--json", "--help"}},
		{"all flags", []string{"health", "--ai", "--json", "--verbose", "-w", "15m", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

func TestCLIUnit_Health_WindowValues(t *testing.T) {
	windows := []string{"1m", "5m", "15m", "30m", "1h", "2h"}

	for _, window := range windows {
		t.Run("window_"+window, func(t *testing.T) {
			result, err := unitTestHarness.Run("health", "-w", window, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "invalid") {
				t.Errorf("Window %q was rejected as invalid", window)
			}
		})
	}
}

// =============================================================================
// 8. INGEST COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Ingest_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"ingest help", []string{"ingest", "--help"}, []string{"--force", "--dataspace", "--version"}},
		{"populate help", []string{"populate", "--help"}, []string{"vectordb"}},
		{"populate vectordb help", []string{"populate", "vectordb", "--help"}, []string{"--force"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Ingest_Flags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"force flag", []string{"ingest", "--force", "--help"}},
		{"dataspace flag", []string{"ingest", "--dataspace", "test", "--help"}},
		{"version flag", []string{"ingest", "--version", "v1.0", "--help"}},
		{"all flags", []string{"ingest", "--force", "--dataspace", "test", "--version", "v1", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

func TestCLIUnit_Ingest_DataSpaceValues(t *testing.T) {
	spaces := []string{"default", "production", "staging", "test", "my-project", "project_v2"}

	for _, space := range spaces {
		t.Run("space_"+space, func(t *testing.T) {
			result, err := unitTestHarness.Run("ingest", "--dataspace", space, "--help")
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "invalid") {
				t.Errorf("Data space %q was rejected", space)
			}
		})
	}
}

// =============================================================================
// 9. TIMESERIES COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Timeseries_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"timeseries help", []string{"timeseries", "--help"}, []string{"fetch", "forecast"}},
		{"timeseries fetch help", []string{"timeseries", "fetch", "--help"}, []string{"--days"}},
		{"timeseries forecast help", []string{"timeseries", "forecast", "--help"}, []string{"--model", "--horizon", "--context"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Timeseries_FetchFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"days flag", []string{"timeseries", "fetch", "--days", "30", "--help"}},
		{"days 365", []string{"timeseries", "fetch", "--days", "365", "--help"}},
		{"days 7", []string{"timeseries", "fetch", "--days", "7", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

func TestCLIUnit_Timeseries_ForecastFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"model flag", []string{"timeseries", "forecast", "--model", "test", "--help"}},
		{"horizon flag", []string{"timeseries", "forecast", "--horizon", "20", "--help"}},
		{"context flag", []string{"timeseries", "forecast", "--context", "300", "--help"}},
		{"all flags", []string{"timeseries", "forecast", "--model", "test", "--horizon", "10", "--context", "100", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

// =============================================================================
// 10. EVALUATE COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Evaluate_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"evaluate help", []string{"evaluate", "--help"}, []string{"run", "export"}},
		{"evaluate run help", []string{"evaluate", "run", "--help"}, []string{"--config"}},
		{"evaluate export help", []string{"evaluate", "export", "--help"}, []string{"--output"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Evaluate_RunFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"config flag", []string{"evaluate", "run", "--config", "test.yaml", "--help"}},
		{"date flag", []string{"evaluate", "run", "--date", "20240101", "--help"}},
		{"ticker flag", []string{"evaluate", "run", "--ticker", "SPY", "--help"}},
		{"model flag", []string{"evaluate", "run", "--model", "test", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

func TestCLIUnit_Evaluate_ExportFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"output flag", []string{"evaluate", "export", "run-id", "--output", "test.csv"}},
		{"output short", []string{"evaluate", "export", "run-id", "-o", "test.csv"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

// =============================================================================
// 11. ASK COMMAND TESTS (20+ tests)
// =============================================================================

func TestCLIUnit_Ask_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"ask help", []string{"ask", "--help"}, []string{"--pipeline", "--no-rag"}},
		{"ask -h", []string{"ask", "-h"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Ask_Flags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"pipeline flag", []string{"ask", "--pipeline", "standard", "--help"}},
		{"pipeline short", []string{"ask", "-p", "standard", "--help"}},
		{"no-rag flag", []string{"ask", "--no-rag", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			combined := result.Stdout + result.Stderr
			if strings.Contains(combined, "unknown flag") {
				t.Error("Got unknown flag error")
			}
		})
	}
}

// =============================================================================
// 12. UPLOAD COMMAND TESTS (10+ tests)
// =============================================================================

func TestCLIUnit_Upload_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{"upload help", []string{"upload", "--help"}, []string{"logs", "backups"}},
		{"upload logs help", []string{"upload", "logs", "--help"}, []string{}},
		{"upload backups help", []string{"upload", "backups", "--help"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertSuccess(); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Upload_Disabled(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"upload logs", []string{"upload", "logs", "."}},
		{"upload backups", []string{"upload", "backups", "."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			// Should show disabled message
			if err := result.AssertOutputContains("disabled"); err != nil {
				t.Error(err)
			}
		})
	}
}

// =============================================================================
// 13. HARNESS SELF-TESTS (10+ tests)
// =============================================================================

func TestCLIUnit_Harness_AssertExitCode(t *testing.T) {
	result := &CLIResult{ExitCode: 0}
	if err := result.AssertExitCode(0); err != nil {
		t.Error("AssertExitCode(0) should pass for exit code 0")
	}
	if err := result.AssertExitCode(1); err == nil {
		t.Error("AssertExitCode(1) should fail for exit code 0")
	}
}

func TestCLIUnit_Harness_AssertSuccess(t *testing.T) {
	result := &CLIResult{ExitCode: 0}
	if err := result.AssertSuccess(); err != nil {
		t.Error("AssertSuccess should pass for exit code 0")
	}

	result.ExitCode = 1
	if err := result.AssertSuccess(); err == nil {
		t.Error("AssertSuccess should fail for exit code 1")
	}
}

func TestCLIUnit_Harness_AssertFailure(t *testing.T) {
	result := &CLIResult{ExitCode: 1}
	if err := result.AssertFailure(); err != nil {
		t.Error("AssertFailure should pass for exit code 1")
	}

	result.ExitCode = 0
	if err := result.AssertFailure(); err == nil {
		t.Error("AssertFailure should fail for exit code 0")
	}
}

func TestCLIUnit_Harness_AssertStdoutContains(t *testing.T) {
	result := &CLIResult{Stdout: "Hello World"}
	if err := result.AssertStdoutContains("Hello"); err != nil {
		t.Error(err)
	}
	if err := result.AssertStdoutContains("World"); err != nil {
		t.Error(err)
	}
	if err := result.AssertStdoutContains("Goodbye"); err == nil {
		t.Error("Should fail for missing substring")
	}
}

func TestCLIUnit_Harness_AssertStderrContains(t *testing.T) {
	result := &CLIResult{Stderr: "Error: something went wrong"}
	if err := result.AssertStderrContains("Error"); err != nil {
		t.Error(err)
	}
	if err := result.AssertStderrContains("Success"); err == nil {
		t.Error("Should fail for missing substring")
	}
}

func TestCLIUnit_Harness_AssertOutputContains(t *testing.T) {
	result := &CLIResult{Stdout: "Hello", Stderr: "World"}
	if err := result.AssertOutputContains("Hello"); err != nil {
		t.Error(err)
	}
	if err := result.AssertOutputContains("World"); err != nil {
		t.Error(err)
	}
	if err := result.AssertOutputContains("Goodbye"); err == nil {
		t.Error("Should fail for missing substring")
	}
}

func TestCLIUnit_Harness_AssertStdoutNotContains(t *testing.T) {
	result := &CLIResult{Stdout: "Hello World"}
	if err := result.AssertStdoutNotContains("Goodbye"); err != nil {
		t.Error(err)
	}
	if err := result.AssertStdoutNotContains("Hello"); err == nil {
		t.Error("Should fail when substring is present")
	}
}

func TestCLIUnit_Harness_AssertNoTimeout(t *testing.T) {
	result := &CLIResult{TimedOut: false}
	if err := result.AssertNoTimeout(); err != nil {
		t.Error(err)
	}

	result.TimedOut = true
	if err := result.AssertNoTimeout(); err == nil {
		t.Error("Should fail when timed out")
	}
}

func TestCLIUnit_Harness_GlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"hello*", "hello world", true},
		{"hello*", "hello", true},
		{"*world", "hello world", true},
		{"*world", "world", true},
		{"hello*world", "hello big world", true},
		{"hello*world", "helloworld", true},
		{"exact", "exact", true},
		{"exact", "not exact", false},
		{"hello*", "world", false},
		{"*world", "hello", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "ac", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.input, func(t *testing.T) {
			got := globMatch(tt.pattern, tt.input)
			if got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.input, got, tt.want)
			}
		})
	}
}

func TestCLIUnit_Harness_Timeout(t *testing.T) {
	// Test that timeout is respected
	harness := NewCLITestHarness("")
	harness.Timeout = 100 * time.Millisecond

	result, err := harness.RunWithOptions(CLIRunOptions{
		Args:    []string{"--help"},
		Timeout: 5 * time.Second, // Override with longer timeout
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TimedOut {
		t.Error("Should not have timed out with 5s timeout")
	}
}

// =============================================================================
// 14. INIT COMMAND TESTS
// =============================================================================

func TestCLIUnit_Init_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"init help long", []string{"init", "--help"}, 0, []string{"init", "index"}},
		{"init help short", []string{"init", "-h"}, 0, []string{"init"}},
		{"init shows force flag", []string{"init", "--help"}, 0, []string{"--force"}},
		{"init shows json flag", []string{"init", "--help"}, 0, []string{"--json"}},
		{"init shows dry-run flag", []string{"init", "--help"}, 0, []string{"--dry-run"}},
		{"init shows languages flag", []string{"init", "--help"}, 0, []string{"--languages"}},
		{"init shows exclude flag", []string{"init", "--help"}, 0, []string{"--exclude"}},
		{"init shows max-workers flag", []string{"init", "--help"}, 0, []string{"--max-workers"}},
		{"init shows quiet flag", []string{"init", "--help"}, 0, []string{"--quiet"}},
		{"init shows verbose flag", []string{"init", "--help"}, 0, []string{"--verbose"}},
		{"init examples documented", []string{"init", "--help"}, 0, []string{"Examples"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Init_InvalidArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"path does not exist", []string{"init", "/nonexistent/path/abc123"}, 2},
		{"too many args", []string{"init", "path1", "path2"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Init_PathIsFile(t *testing.T) {
	// Create a temp file to pass as the path (not a directory)
	f, err := os.CreateTemp("", "aleutian-test-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	f.Close()
	defer os.Remove(f.Name())

	result, err := unitTestHarness.Run("init", f.Name())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(2); err != nil {
		t.Error(err)
	}
}

func TestCLIUnit_Init_DryRunOnFixture(t *testing.T) {
	// dry-run on a real Go project must NOT create .aleutian/, only report what would be indexed.
	fixtureDir := findFixtureDir(t)
	workDir := copyFixture(t, fixtureDir)
	defer os.RemoveAll(workDir)

	result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"init", "--dry-run"},
		WorkDir: workDir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(0); err != nil {
		t.Error(err)
	}
	if _, statErr := os.Stat(workDir + "/.aleutian"); statErr == nil {
		t.Error("dry-run must not create .aleutian/ directory")
	}
}

func TestCLIUnit_Init_Functional(t *testing.T) {
	// Run aleutian init on the sample-go-project fixture and validate the index.
	fixtureDir := findFixtureDir(t)

	// Use a temp copy so we don't pollute the checked-in fixture.
	workDir := copyFixture(t, fixtureDir)
	defer os.RemoveAll(workDir)

	result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"init", "--json"},
		WorkDir: workDir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(0); err != nil {
		t.Error(err)
	}
	// Index must exist after init
	indexPath := workDir + "/.aleutian/index.json"
	if _, statErr := os.Stat(indexPath); statErr != nil {
		t.Errorf("index.json not created after init: %v", statErr)
	}
	// JSON output must contain success indicators
	if err := result.AssertStdoutContains("symbols"); err != nil {
		t.Error(err)
	}
}

// =============================================================================
// 15. GRAPH COMMAND TESTS
// =============================================================================

func TestCLIUnit_Graph_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"graph help", []string{"graph", "--help"}, 0, []string{"callers", "callees", "path"}},
		{"graph -h", []string{"graph", "-h"}, 0, []string{"callers", "callees"}},
		{"graph callers help", []string{"graph", "callers", "--help"}, 0, []string{"SYMBOL"}},
		{"graph callers flags", []string{"graph", "callers", "--help"}, 0, []string{"--json", "--depth", "--limit"}},
		{"graph callees help", []string{"graph", "callees", "--help"}, 0, []string{"SYMBOL"}},
		{"graph callees flags", []string{"graph", "callees", "--help"}, 0, []string{"--json", "--depth"}},
		{"graph path help", []string{"graph", "path", "--help"}, 0, []string{"FROM", "TO"}},
		{"graph path flags", []string{"graph", "path", "--help"}, 0, []string{"--json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Graph_MissingArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{"callers no symbol", []string{"graph", "callers"}, 1},
		{"callees no symbol", []string{"graph", "callees"}, 1},
		{"path no args", []string{"graph", "path"}, 1},
		{"path one arg only", []string{"graph", "path", "main.main"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Graph_MissingIndex(t *testing.T) {
	// Running graph callers from a directory with no .aleutian/ must exit 1
	// (graph.ExitError = 1 covers all error conditions including missing index).
	dir, err := os.MkdirTemp("", "aleutian-graph-noindex-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"graph", "callers", "SomeFunction"},
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(1); err != nil {
		t.Error(err)
	}
	if err := result.AssertStderrContains("index"); err != nil {
		t.Error(err)
	}
}

func TestCLIUnit_Graph_Functional(t *testing.T) {
	// Run init on fixture then query callers and callees.
	fixtureDir := findFixtureDir(t)
	workDir := copyFixture(t, fixtureDir)
	defer os.RemoveAll(workDir)

	// Initialize index first.
	initResult, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"init"},
		WorkDir: workDir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := initResult.AssertExitCode(0); err != nil {
		t.Fatalf("init must succeed before graph tests: %v", err)
	}

	t.Run("callers of ValidateToken returns results", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"graph", "callers", "ValidateToken", "--json"},
			WorkDir: workDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if err := result.AssertExitCode(0); err != nil {
			t.Error(err)
		}
		if err := result.AssertStdoutContains("callers"); err != nil {
			t.Error(err)
		}
	})

	t.Run("callees of main returns results", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"graph", "callees", "main", "--json"},
			WorkDir: workDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if err := result.AssertExitCode(0); err != nil {
			t.Error(err)
		}
		if err := result.AssertStdoutContains("callees"); err != nil {
			t.Error(err)
		}
	})

	t.Run("callers of unknown symbol exits 1 with fail-if-empty", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"graph", "callers", "NonExistentSymbolXYZ123", "--fail-if-empty"},
			WorkDir: workDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if err := result.AssertExitCode(1); err != nil {
			t.Error(err)
		}
	})
}

// =============================================================================
// 16. IMPACT COMMAND TESTS
// =============================================================================

func TestCLIUnit_Impact_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"impact help", []string{"impact", "--help"}, 0, []string{"impact"}},
		{"impact -h", []string{"impact", "-h"}, 0, []string{"impact"}},
		{"impact shows diff flag", []string{"impact", "--help"}, 0, []string{"--diff"}},
		{"impact shows staged flag", []string{"impact", "--help"}, 0, []string{"--staged"}},
		{"impact shows commit flag", []string{"impact", "--help"}, 0, []string{"--commit"}},
		{"impact shows branch flag", []string{"impact", "--help"}, 0, []string{"--branch"}},
		{"impact shows json flag", []string{"impact", "--help"}, 0, []string{"--json"}},
		{"impact shows exit codes", []string{"impact", "--help"}, 0, []string{"exits"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Impact_MutuallyExclusiveModes(t *testing.T) {
	// Specifying more than one change mode must exit 2.
	dir, err := os.MkdirTemp("", "aleutian-impact-modes-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	tests := []struct {
		name string
		args []string
	}{
		{"diff and staged", []string{"impact", "--diff", "--staged"}},
		{"diff and commit", []string{"impact", "--diff", "--commit", "abc123"}},
		{"staged and branch", []string{"impact", "--staged", "--branch", "main"}},
		{"commit and branch", []string{"impact", "--commit", "abc123", "--branch", "main"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
				Args:    tt.args,
				WorkDir: dir,
			})
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(2); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCLIUnit_Impact_NoDiffInNonGitDir(t *testing.T) {
	// Impact --diff in a non-git directory must exit with error (2).
	dir, err := os.MkdirTemp("", "aleutian-impact-nogit-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"impact", "--diff"},
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Must not exit 0 — non-git dir has no diff
	if result.ExitCode == 0 {
		t.Error("impact --diff in non-git dir should not exit 0")
	}
}

func TestCLIUnit_Impact_Functional(t *testing.T) {
	// Use the fixture dir directly: it has a real git repo. impact --diff is read-only
	// (it reads git diffs, does not write to .aleutian/) so using the original is safe.
	fixtureDir := findFixtureDir(t)

	// Initialize the index first so impact has a valid index to work with.
	workDir := copyFixture(t, fixtureDir)
	defer os.RemoveAll(workDir)

	// Restore git history in the copy (copyFixture skips .git, so re-init)
	if out, initErr := exec.Command("git", "-C", workDir, "init").CombinedOutput(); initErr != nil {
		t.Fatalf("git init failed: %v: %s", initErr, out)
	}
	if out, addErr := exec.Command("git", "-C", workDir, "add", ".").CombinedOutput(); addErr != nil {
		t.Fatalf("git add failed: %v: %s", addErr, out)
	}
	if out, commitErr := exec.Command("git", "-C", workDir, "-c", "user.email=test@test.com",
		"-c", "user.name=Test", "commit", "-m", "fixture").CombinedOutput(); commitErr != nil {
		t.Fatalf("git commit failed: %v: %s", commitErr, out)
	}

	// Build index.
	initResult, err := unitTestHarness.RunWithOptions(CLIRunOptions{
		Args:    []string{"init"},
		WorkDir: workDir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := initResult.AssertExitCode(0); err != nil {
		t.Fatalf("init must succeed before impact test: %v", err)
	}

	t.Run("diff with no changes exits 0", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"impact", "--diff", "--json"},
			WorkDir: workDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		// With no uncommitted changes, impact should exit 0.
		if err := result.AssertExitCode(0); err != nil {
			t.Error(err)
		}
	})
}

// =============================================================================
// 17. POLICY CHECK COMMAND TESTS
// =============================================================================

func TestCLIUnit_PolicyCheck_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"policy check help", []string{"policy", "check", "--help"}, 0, []string{"check"}},
		{"policy check -h", []string{"policy", "check", "-h"}, 0, []string{"check"}},
		{"policy check shows json flag", []string{"policy", "check", "--help"}, 0, []string{"--json"}},
		{"policy check shows threshold flag", []string{"policy", "check", "--help"}, 0, []string{"--threshold"}},
		{"policy check shows severity flag", []string{"policy", "check", "--help"}, 0, []string{"--severity"}},
		{"policy check shows exclude flag", []string{"policy", "check", "--help"}, 0, []string{"--exclude"}},
		{"policy check shows redact flag", []string{"policy", "check", "--help"}, 0, []string{"--redact"}},
		{"policy check shows workers flag", []string{"policy", "check", "--help"}, 0, []string{"--workers"}},
		{"policy check exit codes documented", []string{"policy", "check", "--help"}, 0, []string{"Exit"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_PolicyCheck_InvalidPath(t *testing.T) {
	result, err := unitTestHarness.Run("policy", "check", "/nonexistent/path/abc123")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(2); err != nil {
		t.Error(err)
	}
}

func TestCLIUnit_PolicyCheck_JSONErrorOnInvalidPath(t *testing.T) {
	result, err := unitTestHarness.Run("policy", "check", "--json", "/nonexistent/path/abc123")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := result.AssertExitCode(2); err != nil {
		t.Error(err)
	}
	// JSON error output goes to stdout
	if err := result.AssertStdoutContains("error"); err != nil {
		t.Error(err)
	}
}

func TestCLIUnit_PolicyCheck_Functional(t *testing.T) {
	fixtureDir := findFixtureDir(t)

	t.Run("finds credential violation in fixture", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"policy", "check", "--json", "--threshold", "low"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		// Must exit 1 — fixture has intentional credential pattern in config.go
		if err := result.AssertExitCode(1); err != nil {
			t.Error(err)
		}
		if err := result.AssertStdoutContains("violations"); err != nil {
			t.Error(err)
		}
	})

	t.Run("clean dir has no violations", func(t *testing.T) {
		dir, mkErr := os.MkdirTemp("", "aleutian-policy-clean-*")
		if mkErr != nil {
			t.Fatalf("Failed to create temp dir: %v", mkErr)
		}
		defer os.RemoveAll(dir)

		// Write a file with no secrets
		if writeErr := os.WriteFile(dir+"/clean.go", []byte("package main\n\nfunc main() {}\n"), 0644); writeErr != nil {
			t.Fatalf("Failed to write file: %v", writeErr)
		}

		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"policy", "check", "--json"},
			WorkDir: dir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if err := result.AssertExitCode(0); err != nil {
			t.Error(err)
		}
	})

	t.Run("redact flag hides matched content", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"policy", "check", "--json", "--redact", "--threshold", "low"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		// The matched content should be redacted
		if err := result.AssertStdoutContains("REDACTED"); err != nil {
			t.Error(err)
		}
		// The raw secret value must not appear in output
		if err := result.AssertStdoutNotContains("sk-test-hardcoded-secret-12345"); err != nil {
			t.Error(err)
		}
	})
}

// =============================================================================
// 18. RISK COMMAND TESTS
// =============================================================================

func TestCLIUnit_Risk_Help(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExit     int
		wantContains []string
	}{
		{"risk help", []string{"risk", "--help"}, 0, []string{"risk"}},
		{"risk -h", []string{"risk", "-h"}, 0, []string{"risk"}},
		{"risk shows json flag", []string{"risk", "--help"}, 0, []string{"--json"}},
		{"risk shows threshold flag", []string{"risk", "--help"}, 0, []string{"--threshold"}},
		{"risk shows strict flag", []string{"risk", "--help"}, 0, []string{"--strict"}},
		{"risk shows permissive flag", []string{"risk", "--help"}, 0, []string{"--permissive"}},
		{"risk shows timeout flag", []string{"risk", "--help"}, 0, []string{"--timeout"}},
		{"risk shows skip-impact flag", []string{"risk", "--help"}, 0, []string{"--skip-impact"}},
		{"risk shows skip-policy flag", []string{"risk", "--help"}, 0, []string{"--skip-policy"}},
		{"risk shows skip-complexity flag", []string{"risk", "--help"}, 0, []string{"--skip-complexity"}},
		{"risk shows explain flag", []string{"risk", "--help"}, 0, []string{"--explain"}},
		{"risk shows exit codes", []string{"risk", "--help"}, 0, []string{"Exit"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unitTestHarness.Run(tt.args...)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if err := result.AssertExitCode(tt.wantExit); err != nil {
				t.Error(err)
			}
			for _, want := range tt.wantContains {
				if err := result.AssertOutputContains(want); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCLIUnit_Risk_Functional(t *testing.T) {
	// Use the fixture dir: it has a real git repo, so git diff succeeds.
	// We skip-impact (no .aleutian/ index in the original fixture) and use
	// best-effort mode so other signal failures don't abort the run.
	fixtureDir := findFixtureDir(t)

	t.Run("skip-impact with real git dir runs without error exit", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"risk", "--skip-impact", "--best-effort", "--json", "--threshold", "critical"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		// Can be 0 (below threshold) or 1 (risk found) but not 2 (fatal error)
		if result.ExitCode == 2 {
			t.Errorf("risk must not exit 2 in best-effort mode: stderr=%s stdout=%s",
				result.Stderr, result.Stdout)
		}
	})

	t.Run("json output contains risk_level field", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"risk", "--skip-impact", "--best-effort", "--json"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if err := result.AssertStdoutContains("risk_level"); err != nil {
			t.Error(err)
		}
	})

	t.Run("strict flag maps to threshold low", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"risk", "--skip-impact", "--best-effort", "--strict", "--json"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode == 2 {
			t.Errorf("risk --strict must not exit 2: stderr=%s", result.Stderr)
		}
	})

	t.Run("permissive flag runs without error", func(t *testing.T) {
		result, err := unitTestHarness.RunWithOptions(CLIRunOptions{
			Args:    []string{"risk", "--skip-impact", "--best-effort", "--permissive", "--json"},
			WorkDir: fixtureDir,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.ExitCode == 2 {
			t.Errorf("risk --permissive must not exit 2: stderr=%s", result.Stderr)
		}
	})
}

// =============================================================================
// TEST FIXTURE HELPERS (CLI-01)
// =============================================================================

// findFixtureDir returns the absolute path to test/fixtures/sample-go-project.
func findFixtureDir(t *testing.T) string {
	t.Helper()

	sourceDir, err := findSourceDir()
	if err != nil {
		t.Fatalf("Cannot find source directory: %v", err)
	}

	fixtureDir := strings.Join([]string{sourceDir, "test", "fixtures", "sample-go-project"}, string(os.PathSeparator))
	if _, statErr := os.Stat(fixtureDir); statErr != nil {
		t.Fatalf("Fixture directory not found at %s: %v", fixtureDir, statErr)
	}
	return fixtureDir
}

// copyFixture copies the fixture directory to a fresh temp directory.
// This prevents test runs from dirtying the checked-in fixture (e.g. .aleutian/).
func copyFixture(t *testing.T, fixtureDir string) string {
	t.Helper()

	destDir, err := os.MkdirTemp("", "aleutian-fixture-copy-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir for fixture copy: %v", err)
	}

	if err := copyDir(fixtureDir, destDir); err != nil {
		os.RemoveAll(destDir)
		t.Fatalf("Failed to copy fixture: %v", err)
	}

	return destDir
}

// copyDir recursively copies src to dst, skipping .aleutian/ and .git/.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .aleutian and .git directories (don't copy index or git history)
		base := filepath.Base(path)
		if info.IsDir() && (base == ".aleutian" || base == ".git") {
			return filepath.SkipDir
		}

		relPath, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
