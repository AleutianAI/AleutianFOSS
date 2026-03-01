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

func TestProviderControlPlane_OllamaAlwaysEnabled(t *testing.T) {
	cp := NewProviderControlPlane(false) // global disabled

	enabled, reason := cp.IsEnabled("ollama")
	if !enabled {
		t.Errorf("Ollama should always be enabled, got reason: %s", reason)
	}
}

func TestProviderControlPlane_GlobalDisabled(t *testing.T) {
	cp := NewProviderControlPlane(false)

	enabled, reason := cp.IsEnabled("anthropic")
	if enabled {
		t.Error("anthropic should be disabled when global switch is off")
	}
	if !strings.Contains(reason, "global egress kill switch") {
		t.Errorf("reason should mention global kill switch, got: %s", reason)
	}
}

func TestProviderControlPlane_GlobalEnabled(t *testing.T) {
	cp := NewProviderControlPlane(true)

	enabled, reason := cp.IsEnabled("anthropic")
	if !enabled {
		t.Errorf("anthropic should be enabled, got reason: %s", reason)
	}
	if reason != "" {
		t.Errorf("reason should be empty when enabled, got: %s", reason)
	}
}

func TestProviderControlPlane_PerProviderDisabled(t *testing.T) {
	cp := NewProviderControlPlane(true)
	cp.SetProviderEnabled("openai", false)

	enabled, reason := cp.IsEnabled("openai")
	if enabled {
		t.Error("openai should be disabled by per-provider switch")
	}
	if !strings.Contains(reason, "per-provider kill switch") {
		t.Errorf("reason should mention per-provider switch, got: %s", reason)
	}

	// Other providers should still be enabled
	enabled, _ = cp.IsEnabled("anthropic")
	if !enabled {
		t.Error("anthropic should still be enabled")
	}
}

func TestProviderControlPlane_SetGlobalEnabled(t *testing.T) {
	cp := NewProviderControlPlane(true)

	enabled, _ := cp.IsEnabled("anthropic")
	if !enabled {
		t.Error("anthropic should be enabled initially")
	}

	cp.SetGlobalEnabled(false)

	enabled, _ = cp.IsEnabled("anthropic")
	if enabled {
		t.Error("anthropic should be disabled after global disable")
	}

	cp.SetGlobalEnabled(true)

	enabled, _ = cp.IsEnabled("anthropic")
	if !enabled {
		t.Error("anthropic should be re-enabled after global re-enable")
	}
}

func TestProviderControlPlane_PerProviderReEnabled(t *testing.T) {
	cp := NewProviderControlPlane(true)
	cp.SetProviderEnabled("gemini", false)

	enabled, _ := cp.IsEnabled("gemini")
	if enabled {
		t.Error("gemini should be disabled")
	}

	cp.SetProviderEnabled("gemini", true)

	enabled, _ = cp.IsEnabled("gemini")
	if !enabled {
		t.Error("gemini should be re-enabled")
	}
}
