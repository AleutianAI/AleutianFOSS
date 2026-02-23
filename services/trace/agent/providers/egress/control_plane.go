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
	"sync"
	"time"
)

// ProviderControlPlane implements the global and per-provider kill switch (P-1).
//
// Description:
//
//	Controls whether egress to cloud providers is allowed. Supports both a
//	global kill switch (disable all cloud egress) and per-provider switches.
//	Ollama is always considered enabled because it is a local provider with
//	no data egress.
//
// Thread Safety: Safe for concurrent use via sync.RWMutex.
type ProviderControlPlane struct {
	mu              sync.RWMutex
	globalEnabled   bool
	providerEnabled map[string]bool
	disabledAt      int64 // Unix milliseconds UTC when global switch was turned off
}

// NewProviderControlPlane creates a new control plane from configuration.
//
// Inputs:
//   - enabled: Initial state of the global kill switch.
//
// Outputs:
//   - *ProviderControlPlane: Configured control plane.
func NewProviderControlPlane(enabled bool) *ProviderControlPlane {
	cp := &ProviderControlPlane{
		globalEnabled:   enabled,
		providerEnabled: make(map[string]bool),
	}
	if !enabled {
		cp.disabledAt = time.Now().UnixMilli()
	}
	return cp
}

// IsEnabled checks whether a provider is allowed to receive egress traffic.
//
// Description:
//
//	Ollama always returns true (local provider, no data leaves the environment).
//	For cloud providers, both the global switch and per-provider switch must be
//	enabled. If a per-provider override has not been set, the provider is
//	considered enabled by default.
//
// Inputs:
//   - provider: The provider name (e.g., "anthropic", "ollama").
//
// Outputs:
//   - bool: True if egress is allowed for this provider.
//   - string: Reason if disabled (empty string if enabled).
func (c *ProviderControlPlane) IsEnabled(provider string) (bool, string) {
	// Ollama is always enabled — local provider, no egress
	if provider == "ollama" {
		return true, ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.globalEnabled {
		return false, fmt.Sprintf("global egress kill switch activated at %s",
			time.UnixMilli(c.disabledAt).UTC().Format(time.RFC3339))
	}

	// Check per-provider override (default: enabled)
	if enabled, exists := c.providerEnabled[provider]; exists && !enabled {
		return false, fmt.Sprintf("provider %q disabled by per-provider kill switch", provider)
	}

	return true, ""
}

// SetGlobalEnabled sets the global kill switch state.
//
// Inputs:
//   - enabled: New global state. When set to false, records the disable timestamp.
//
// Thread Safety: Safe for concurrent use.
func (c *ProviderControlPlane) SetGlobalEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.globalEnabled = enabled
	if !enabled {
		c.disabledAt = time.Now().UnixMilli()
	}
}

// SetProviderEnabled sets the per-provider kill switch state.
//
// Inputs:
//   - provider: The provider name.
//   - enabled: New state for this provider.
//
// Thread Safety: Safe for concurrent use.
func (c *ProviderControlPlane) SetProviderEnabled(provider string, enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.providerEnabled[provider] = enabled
}
