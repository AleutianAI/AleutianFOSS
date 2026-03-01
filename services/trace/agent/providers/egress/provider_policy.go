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

import "fmt"

// ProviderPolicy enforces allowlist/denylist rules for cloud providers (P-2).
//
// Description:
//
//	Determines whether a provider is permitted based on explicit allowlist
//	and denylist configuration. The denylist takes precedence over the
//	allowlist (defense in depth). Ollama always passes because it is a
//	local provider with no data egress.
//
// Thread Safety: Safe for concurrent use (read-only after construction).
type ProviderPolicy struct {
	allowlist map[string]bool
	denylist  map[string]bool
}

// NewProviderPolicy creates a new provider policy from allowlist/denylist sets.
//
// Inputs:
//   - allowlist: Providers explicitly allowed. Empty means all allowed.
//   - denylist: Providers explicitly denied. Takes precedence over allowlist.
//
// Outputs:
//   - *ProviderPolicy: Configured policy.
func NewProviderPolicy(allowlist, denylist map[string]bool) *ProviderPolicy {
	return &ProviderPolicy{
		allowlist: allowlist,
		denylist:  denylist,
	}
}

// IsAllowed checks whether a provider passes the allowlist/denylist policy.
//
// Description:
//
//	Resolution order:
//	  1. Ollama always passes (local provider).
//	  2. If the provider is in the denylist, it is denied.
//	  3. If the allowlist is non-empty and the provider is not in it, it is denied.
//	  4. Otherwise, allowed.
//
// Inputs:
//   - provider: The provider name.
//
// Outputs:
//   - bool: True if the provider is allowed.
//   - string: Reason if denied (empty if allowed).
func (p *ProviderPolicy) IsAllowed(provider string) (bool, string) {
	// Ollama is always allowed — local provider
	if provider == "ollama" {
		return true, ""
	}

	// Denylist takes precedence (defense in depth)
	if p.denylist[provider] {
		return false, fmt.Sprintf("provider %q is in the denylist", provider)
	}

	// If allowlist is set, provider must be in it
	if len(p.allowlist) > 0 && !p.allowlist[provider] {
		return false, fmt.Sprintf("provider %q is not in the allowlist", provider)
	}

	return true, ""
}
