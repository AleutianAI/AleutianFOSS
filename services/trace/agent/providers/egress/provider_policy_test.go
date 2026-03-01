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

func TestProviderPolicy_OllamaAlwaysAllowed(t *testing.T) {
	// Even with deny list containing ollama, it should pass
	policy := NewProviderPolicy(nil, map[string]bool{"ollama": true})

	allowed, reason := policy.IsAllowed("ollama")
	if !allowed {
		t.Errorf("Ollama should always be allowed, got reason: %s", reason)
	}
}

func TestProviderPolicy_NoRestrictions(t *testing.T) {
	policy := NewProviderPolicy(nil, nil)

	for _, p := range []string{"anthropic", "openai", "gemini"} {
		allowed, reason := policy.IsAllowed(p)
		if !allowed {
			t.Errorf("%s should be allowed with no restrictions, got reason: %s", p, reason)
		}
	}
}

func TestProviderPolicy_AllowlistOnly(t *testing.T) {
	policy := NewProviderPolicy(map[string]bool{"anthropic": true}, nil)

	allowed, _ := policy.IsAllowed("anthropic")
	if !allowed {
		t.Error("anthropic should be allowed (in allowlist)")
	}

	allowed, reason := policy.IsAllowed("openai")
	if allowed {
		t.Error("openai should not be allowed (not in allowlist)")
	}
	if !strings.Contains(reason, "not in the allowlist") {
		t.Errorf("reason should mention allowlist, got: %s", reason)
	}
}

func TestProviderPolicy_DenylistOnly(t *testing.T) {
	policy := NewProviderPolicy(nil, map[string]bool{"gemini": true})

	allowed, reason := policy.IsAllowed("gemini")
	if allowed {
		t.Error("gemini should be denied (in denylist)")
	}
	if !strings.Contains(reason, "denylist") {
		t.Errorf("reason should mention denylist, got: %s", reason)
	}

	allowed, _ = policy.IsAllowed("anthropic")
	if !allowed {
		t.Error("anthropic should be allowed (not in denylist)")
	}
}

func TestProviderPolicy_DenylistOverridesAllowlist(t *testing.T) {
	// Provider is in both allowlist and denylist — denylist wins
	policy := NewProviderPolicy(
		map[string]bool{"anthropic": true, "openai": true},
		map[string]bool{"openai": true},
	)

	allowed, _ := policy.IsAllowed("anthropic")
	if !allowed {
		t.Error("anthropic should be allowed (in allowlist, not in denylist)")
	}

	allowed, reason := policy.IsAllowed("openai")
	if allowed {
		t.Error("openai should be denied (denylist overrides allowlist)")
	}
	if !strings.Contains(reason, "denylist") {
		t.Errorf("reason should mention denylist, got: %s", reason)
	}
}
