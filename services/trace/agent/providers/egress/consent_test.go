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
	"strings"
	"testing"
)

func TestConsentPolicy_OllamaAlwaysConsented(t *testing.T) {
	// Even in local-only mode, Ollama should have consent
	policy := NewConsentPolicy(true, nil)

	consented, reason := policy.HasConsent("ollama")
	if !consented {
		t.Errorf("Ollama should always have consent, got reason: %s", reason)
	}
}

func TestConsentPolicy_LocalOnlyBlocksCloud(t *testing.T) {
	policy := NewConsentPolicy(true, map[string]bool{"anthropic": true})

	consented, reason := policy.HasConsent("anthropic")
	if consented {
		t.Error("anthropic should be blocked in local-only mode even with consent")
	}
	if !strings.Contains(reason, "local-only mode") {
		t.Errorf("reason should mention local-only mode, got: %s", reason)
	}
}

func TestConsentPolicy_NoConsent(t *testing.T) {
	policy := NewConsentPolicy(false, map[string]bool{})

	consented, reason := policy.HasConsent("anthropic")
	if consented {
		t.Error("anthropic should require consent")
	}
	if !strings.Contains(reason, "TRACE_CONSENT_ANTHROPIC") {
		t.Errorf("reason should mention env var, got: %s", reason)
	}
}

func TestConsentPolicy_WithConsent(t *testing.T) {
	policy := NewConsentPolicy(false, map[string]bool{
		"anthropic": true,
		"openai":    false,
	})

	consented, _ := policy.HasConsent("anthropic")
	if !consented {
		t.Error("anthropic should be consented")
	}

	consented, _ = policy.HasConsent("openai")
	if consented {
		t.Error("openai should not be consented (explicitly false)")
	}

	consented, _ = policy.HasConsent("gemini")
	if consented {
		t.Error("gemini should not be consented (not in map)")
	}
}

func TestConsentPolicy_DefensiveCopy(t *testing.T) {
	consent := map[string]bool{"anthropic": true}
	policy := NewConsentPolicy(false, consent)

	// Mutate the original map
	consent["anthropic"] = false

	// Policy should still use the original value
	consented, _ := policy.HasConsent("anthropic")
	if !consented {
		t.Error("policy should use a defensive copy of the consent map")
	}
}
