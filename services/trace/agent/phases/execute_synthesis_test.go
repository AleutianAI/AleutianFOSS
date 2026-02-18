// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

import (
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

// CR-20-1: Unit tests for getSingleFormattedResult.
// This function gates whether tool output is passed through verbatim (preventing
// hallucination) or sent to the LLM for synthesis. Zero test coverage before this.

func TestGetSingleFormattedResult_GraphResultPassThrough(t *testing.T) {
	// Single successful result with "## GRAPH RESULT" header should pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "## GRAPH RESULT: Symbol 'Application' not found\n\nNo interface or class named 'Application' exists.\n",
		},
	}

	output, ok := getSingleFormattedResult(results)
	if !ok {
		t.Fatal("expected pass-through for GRAPH RESULT output")
	}
	if output != results[0].Output {
		t.Errorf("expected verbatim output, got %q", output)
	}
}

func TestGetSingleFormattedResult_FoundPrefixPassThrough(t *testing.T) {
	// Single successful result starting with "Found " should pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "Found 5 implementations/subclasses of 'Iterator':\n\n  • Table (struct) in table/iterator.go:165\n",
		},
	}

	output, ok := getSingleFormattedResult(results)
	if !ok {
		t.Fatal("expected pass-through for 'Found' prefix output")
	}
	if output != results[0].Output {
		t.Errorf("expected verbatim output, got %q", output)
	}
}

func TestGetSingleFormattedResult_ExhaustiveFooterPassThrough(t *testing.T) {
	// Single successful result with exhaustive footer should pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "Interface: iface.go:1:Reader\n  • MyReader (struct) in impl.go:1\n\n---\nThe graph has been fully indexed — these results are exhaustive.\n",
		},
	}

	output, ok := getSingleFormattedResult(results)
	if !ok {
		t.Fatal("expected pass-through for exhaustive footer output")
	}
	if output != results[0].Output {
		t.Errorf("expected verbatim output, got %q", output)
	}
}

func TestGetSingleFormattedResult_PlainTextNoPassThrough(t *testing.T) {
	// Single successful result with plain text (no graph markers) should NOT pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "The function doSomething is defined at line 42 and calls helper at line 50.",
		},
	}

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through for plain text output")
	}
}

func TestGetSingleFormattedResult_MultipleResults(t *testing.T) {
	// Multiple successful results should NOT pass through (multi-result synthesis needed).
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "## GRAPH RESULT: Found 3 implementations\n",
		},
		{
			Success: true,
			Output:  "## GRAPH RESULT: Found 2 callers\n",
		},
	}

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through for multiple results")
	}
}

func TestGetSingleFormattedResult_SuccessPlusError(t *testing.T) {
	// One success + one error should NOT pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "## GRAPH RESULT: Symbol 'Foo' not found\n",
		},
		{
			Success: false,
			Error:   "timeout querying graph",
		},
	}

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through when errors present")
	}
}

func TestGetSingleFormattedResult_EmptyResults(t *testing.T) {
	// No results should NOT pass through.
	var results []agent.ToolResult

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through for empty results")
	}
}

func TestGetSingleFormattedResult_SuccessEmptyOutput(t *testing.T) {
	// CR-20-6: A success with empty output alongside a success with real output
	// should NOT pass through — the empty success indicates another tool ran but
	// produced nothing, which should go through the full synthesis path.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "## GRAPH RESULT: Symbol 'Foo' not found\n",
		},
		{
			Success: true,
			Output:  "",
		},
	}

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through when empty-output success exists")
	}
}

func TestGetSingleFormattedResult_SingleEmptyOutputOnly(t *testing.T) {
	// A single success with empty output should NOT pass through.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "",
		},
	}

	_, ok := getSingleFormattedResult(results)
	if ok {
		t.Fatal("expected no pass-through for single empty-output success")
	}
}

// Phase 20: Test that forceLLMSynthesis pass-through logic correctly identifies
// authoritative graph results that should bypass LLM synthesis entirely.
// This prevents the Express 7150 hallucination class where the LLM fabricates
// details when the tool already returned "Symbol not found".
func TestGetSingleFormattedResult_NotFoundPassThrough(t *testing.T) {
	// A "Symbol not found" graph result should pass through verbatim.
	// This is the exact pattern that caused Express 7150 hallucination:
	// the tool correctly returned "not found" but forceLLMSynthesis sent it
	// to the LLM, which fabricated an answer about mixin patterns.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "## GRAPH RESULT: Symbol 'Application' not found\n\nNo interface or class named 'Application' exists in the codebase graph.\n",
		},
	}

	output, ok := getSingleFormattedResult(results)
	if !ok {
		t.Fatal("expected pass-through for 'not found' graph result — this would cause LLM hallucination")
	}
	if output != results[0].Output {
		t.Errorf("expected verbatim output, got %q", output)
	}
}

func TestGetSingleFormattedResult_FoundImplementationsPassThrough(t *testing.T) {
	// A successful find_implementations result should pass through verbatim.
	results := []agent.ToolResult{
		{
			Success: true,
			Output:  "Found 3 implementations/subclasses of 'Page':\n\n  • nopPage (type) in hugolib/page.go:42\n  • pageState (struct) in hugolib/page.go:100\n  • testPage (struct) in hugolib/page_test.go:15\n\n---\nThe graph has been fully indexed — these results are exhaustive.\n",
		},
	}

	output, ok := getSingleFormattedResult(results)
	if !ok {
		t.Fatal("expected pass-through for find_implementations result")
	}
	if output != results[0].Output {
		t.Errorf("expected verbatim output, got %q", output)
	}
}
