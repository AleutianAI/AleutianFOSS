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
	"fmt"
	"strings"
)

// ConsentPolicy enforces per-provider user consent (P-4).
//
// Description:
//
//	Controls whether data may be sent to each cloud provider based on
//	explicit user consent. Supports a "local only" mode that blocks all
//	cloud providers regardless of individual consent.
//
// Thread Safety: Safe for concurrent use (read-only after construction).
type ConsentPolicy struct {
	localOnly       bool
	providerConsent map[string]bool
}

// NewConsentPolicy creates a consent policy from configuration.
//
// Inputs:
//   - localOnly: When true, blocks all cloud providers.
//   - providerConsent: Per-provider consent flags (e.g., {"anthropic": true}).
//
// Outputs:
//   - *ConsentPolicy: Configured consent policy.
func NewConsentPolicy(localOnly bool, providerConsent map[string]bool) *ConsentPolicy {
	consent := make(map[string]bool, len(providerConsent))
	for k, v := range providerConsent {
		consent[k] = v
	}
	return &ConsentPolicy{
		localOnly:       localOnly,
		providerConsent: consent,
	}
}

// HasConsent checks whether user consent has been given for a provider.
//
// Description:
//
//	Ollama always has consent (local provider, no data leaves the environment).
//	If localOnly mode is active, all cloud providers are blocked.
//	Otherwise, checks the per-provider consent map.
//
// Inputs:
//   - provider: The provider name.
//
// Outputs:
//   - bool: True if consent has been given.
//   - string: Reason if no consent (empty if consented).
func (c *ConsentPolicy) HasConsent(provider string) (bool, string) {
	// Ollama is always consented — local provider
	if provider == "ollama" {
		return true, ""
	}

	if c.localOnly {
		return false, fmt.Sprintf("local-only mode is active — all cloud providers blocked (set TRACE_LOCAL_ONLY=false to allow)")
	}

	if !c.providerConsent[provider] {
		envKey := "TRACE_CONSENT_" + strings.ToUpper(provider)
		return false, fmt.Sprintf("provider %q requires user consent — set %s=true", provider, envKey)
	}

	return true, ""
}
