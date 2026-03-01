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
	"testing"
)

func TestDataSensitivity_String(t *testing.T) {
	tests := []struct {
		sensitivity DataSensitivity
		want        string
	}{
		{SensitivityPublic, "public"},
		{SensitivityConfidential, "confidential"},
		{SensitivityPII, "pii"},
		{SensitivityPHI, "phi"},
		{SensitivitySecret, "secret"},
		{DataSensitivity(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.sensitivity.String()
			if got != tt.want {
				t.Errorf("DataSensitivity(%d).String() = %q, want %q", int(tt.sensitivity), got, tt.want)
			}
		})
	}
}

func TestDataSensitivity_AllowsExternalSend(t *testing.T) {
	tests := []struct {
		sensitivity DataSensitivity
		want        bool
	}{
		{SensitivityPublic, true},
		{SensitivityConfidential, true},
		{SensitivityPII, false},
		{SensitivityPHI, false},
		{SensitivitySecret, false},
		{DataSensitivity(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.sensitivity.String(), func(t *testing.T) {
			got := tt.sensitivity.AllowsExternalSend()
			if got != tt.want {
				t.Errorf("AllowsExternalSend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSensitivity(t *testing.T) {
	tests := []struct {
		input string
		want  DataSensitivity
	}{
		{"public", SensitivityPublic},
		{"Confidential", SensitivityConfidential},
		{"PII", SensitivityPII},
		{"PHI", SensitivityPHI},
		{"Secret", SensitivitySecret},
		{"unknown_value", SensitivitySecret},
		{"", SensitivitySecret},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseSensitivity(tt.input)
			if got != tt.want {
				t.Errorf("ParseSensitivity(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewEgressDecision(t *testing.T) {
	d := NewEgressDecision("req-1", "sess-1", "anthropic", "claude-sonnet-4-20250514")

	if d.RequestID != "req-1" {
		t.Errorf("RequestID = %q, want %q", d.RequestID, "req-1")
	}
	if d.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", d.SessionID, "sess-1")
	}
	if d.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", d.Provider, "anthropic")
	}
	if d.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want %q", d.Model, "claude-sonnet-4-20250514")
	}
	if d.Timestamp == 0 {
		t.Error("Timestamp should be set to current time, got 0")
	}
	if d.Allowed {
		t.Error("Allowed should default to false")
	}
}

func TestSentinelErrors(t *testing.T) {
	// Verify all sentinel errors have distinct messages.
	errs := []error{
		ErrProviderDisabled,
		ErrProviderDenied,
		ErrNoConsent,
		ErrSensitiveData,
		ErrTokenBudgetExhausted,
		ErrCostLimitReached,
		ErrRateLimited,
		ErrSecretNotFound,
	}

	seen := make(map[string]bool, len(errs))
	for _, e := range errs {
		msg := e.Error()
		if msg == "" {
			t.Errorf("sentinel error has empty message: %v", e)
		}
		if seen[msg] {
			t.Errorf("duplicate sentinel error message: %q", msg)
		}
		seen[msg] = true
	}
}
