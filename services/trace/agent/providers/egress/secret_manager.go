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
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// SecretBackend is the interface for retrieving secrets (P-10).
//
// Thread Safety: Implementations must be safe for concurrent use.
type SecretBackend interface {
	// GetSecret retrieves a secret by key.
	//
	// Inputs:
	//   - ctx: Context for cancellation.
	//   - key: The secret key name.
	//
	// Outputs:
	//   - string: The secret value.
	//   - error: Non-nil if the secret cannot be retrieved (including ErrSecretNotFound).
	GetSecret(ctx context.Context, key string) (string, error)
}

// EnvBackend reads secrets from environment variables with TTL-based caching.
//
// Description:
//
//	Reads secrets from os.Getenv and caches them for the configured TTL.
//	This avoids repeated syscalls while allowing secret rotation by clearing
//	the cache after the TTL expires.
//
// Thread Safety: Safe for concurrent use via sync.RWMutex.
type EnvBackend struct {
	mu    sync.RWMutex
	cache map[string]cachedSecret
	ttl   time.Duration
}

type cachedSecret struct {
	value     string
	fetchedAt int64 // Unix milliseconds UTC
}

// NewEnvBackend creates a secret backend that reads from environment variables.
//
// Inputs:
//   - ttl: How long to cache secrets before re-reading from the environment.
//     Use 0 for no caching (re-read every time).
//
// Outputs:
//   - *EnvBackend: Configured backend.
func NewEnvBackend(ttl time.Duration) *EnvBackend {
	return &EnvBackend{
		cache: make(map[string]cachedSecret),
		ttl:   ttl,
	}
}

// GetSecret retrieves a secret from the environment, using the cache if fresh.
//
// Inputs:
//   - ctx: Context for cancellation (environment reads are fast, but respected).
//   - key: The environment variable name.
//
// Outputs:
//   - string: The secret value.
//   - error: ErrSecretNotFound if the environment variable is not set or empty.
func (e *EnvBackend) GetSecret(ctx context.Context, key string) (string, error) {
	if ctx.Err() != nil {
		return "", fmt.Errorf("retrieving secret %q: %w", key, ctx.Err())
	}

	now := time.Now().UnixMilli()

	// Check cache first
	if e.ttl > 0 {
		e.mu.RLock()
		if cached, ok := e.cache[key]; ok {
			age := time.Duration(now-cached.fetchedAt) * time.Millisecond
			if age < e.ttl {
				e.mu.RUnlock()
				if cached.value == "" {
					return "", fmt.Errorf("secret %q: %w", key, ErrSecretNotFound)
				}
				return cached.value, nil
			}
		}
		e.mu.RUnlock()
	}

	// Read from environment
	value := os.Getenv(key)

	// Update cache
	if e.ttl > 0 {
		e.mu.Lock()
		e.cache[key] = cachedSecret{value: value, fetchedAt: now}
		e.mu.Unlock()
	}

	if value == "" {
		return "", fmt.Errorf("secret %q: %w", key, ErrSecretNotFound)
	}

	return value, nil
}

// SecretManager selects the appropriate secret backend based on configuration.
//
// Description:
//
//	Currently supports only the "env" backend (environment variables).
//	The backend is selected by the TRACE_SECRET_BACKEND environment variable.
//	Defaults to "env" when not set.
//
// Thread Safety: Safe for concurrent use (delegates to thread-safe backend).
type SecretManager struct {
	backend SecretBackend
}

// NewSecretManager creates a secret manager with the appropriate backend.
//
// Inputs:
//   - cacheTTL: Cache TTL for the environment backend.
//
// Outputs:
//   - *SecretManager: Configured secret manager.
func NewSecretManager(cacheTTL time.Duration) *SecretManager {
	return &SecretManager{
		backend: NewEnvBackend(cacheTTL),
	}
}

// GetSecret retrieves a secret from the configured backend.
//
// Inputs:
//   - ctx: Context for cancellation.
//   - key: The secret key name.
//
// Outputs:
//   - string: The secret value.
//   - error: Non-nil if the secret cannot be retrieved.
func (s *SecretManager) GetSecret(ctx context.Context, key string) (string, error) {
	return s.backend.GetSecret(ctx, key)
}
