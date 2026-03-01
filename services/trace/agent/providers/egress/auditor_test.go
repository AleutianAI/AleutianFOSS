// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestEgressAuditor_LogBefore(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, true)
	ctx := context.Background()

	decision := NewEgressDecision("req-1", "sess-1", "anthropic", "claude-sonnet-4-20250514")
	decision.Sensitivity = SensitivityPublic
	decision.ContentHash = "abc123"
	decision.EstimatedTokens = 5000
	decision.EstimatedCostCents = 1.5

	auditor.LogBefore(ctx, decision)

	output := buf.String()
	if !strings.Contains(output, "egress_before") {
		t.Error("output should contain 'egress_before'")
	}
	if !strings.Contains(output, "req-1") {
		t.Error("output should contain request_id")
	}
	if !strings.Contains(output, "abc123") {
		t.Error("output should contain content_hash when hashContent=true")
	}
}

func TestEgressAuditor_LogBefore_NoHash(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, false)
	ctx := context.Background()

	decision := NewEgressDecision("req-1", "sess-1", "anthropic", "claude-sonnet-4-20250514")
	decision.ContentHash = "abc123"

	auditor.LogBefore(ctx, decision)

	output := buf.String()
	if strings.Contains(output, "abc123") {
		t.Error("output should NOT contain content_hash when hashContent=false")
	}
}

func TestEgressAuditor_LogAfter_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, false)
	ctx := context.Background()

	auditor.LogAfter(ctx, "req-1", "anthropic", "claude-sonnet-4-20250514", 100, 200, 500, 0.5, nil)

	output := buf.String()
	if !strings.Contains(output, "egress_after") {
		t.Error("output should contain 'egress_after'")
	}
	if !strings.Contains(output, "success") {
		t.Error("output should contain status 'success'")
	}
}

func TestEgressAuditor_LogAfter_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, false)
	ctx := context.Background()

	auditor.LogAfter(ctx, "req-1", "openai", "gpt-4o", 100, 0, 200, 0, errors.New("timeout"))

	output := buf.String()
	if !strings.Contains(output, "error") {
		t.Error("output should contain status 'error'")
	}
	if !strings.Contains(output, "timeout") {
		t.Error("output should contain the error message")
	}
}

func TestEgressAuditor_LogBlocked(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, false)
	ctx := context.Background()

	decision := NewEgressDecision("req-1", "sess-1", "gemini", "gemini-1.5-pro")
	decision.BlockedBy = "consent"
	decision.BlockReason = "no consent for gemini"

	auditor.LogBlocked(ctx, decision)

	output := buf.String()
	if !strings.Contains(output, "egress_blocked") {
		t.Error("output should contain 'egress_blocked'")
	}
	if !strings.Contains(output, "consent") {
		t.Error("output should contain blocked_by")
	}
}

func TestEgressAuditor_Disabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, false, false)
	ctx := context.Background()

	decision := NewEgressDecision("req-1", "sess-1", "anthropic", "claude-sonnet-4-20250514")
	auditor.LogBefore(ctx, decision)
	auditor.LogAfter(ctx, "req-1", "anthropic", "claude-sonnet-4-20250514", 100, 200, 500, 0.5, nil)
	auditor.LogBlocked(ctx, decision)

	if buf.Len() != 0 {
		t.Errorf("disabled auditor should produce no output, got: %s", buf.String())
	}
}

func TestEgressAuditor_NilDecision(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditor := NewEgressAuditor(logger, true, false)
	ctx := context.Background()

	// Should not panic
	auditor.LogBefore(ctx, nil)
	auditor.LogBlocked(ctx, nil)

	if buf.Len() != 0 {
		t.Error("nil decision should produce no output")
	}
}

func TestHashContent(t *testing.T) {
	t.Run("empty returns empty", func(t *testing.T) {
		if h := HashContent(nil); h != "" {
			t.Errorf("nil should return empty, got %q", h)
		}
		if h := HashContent([]byte{}); h != "" {
			t.Errorf("empty bytes should return empty, got %q", h)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		data := []byte("hello world")
		h1 := HashContent(data)
		h2 := HashContent(data)
		if h1 != h2 {
			t.Errorf("hashes should be deterministic: %q != %q", h1, h2)
		}
		if len(h1) != 64 { // SHA256 hex = 64 chars
			t.Errorf("SHA256 hex should be 64 chars, got %d", len(h1))
		}
	})

	t.Run("different inputs different hashes", func(t *testing.T) {
		h1 := HashContent([]byte("hello"))
		h2 := HashContent([]byte("world"))
		if h1 == h2 {
			t.Error("different inputs should produce different hashes")
		}
	})
}
