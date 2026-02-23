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
	"errors"
	"testing"
	"time"
)

func TestEnvBackend_GetSecret_Found(t *testing.T) {
	t.Setenv("TEST_SECRET_KEY", "my-secret-value")

	backend := NewEnvBackend(0) // no caching
	ctx := context.Background()

	value, err := backend.GetSecret(ctx, "TEST_SECRET_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "my-secret-value" {
		t.Errorf("got %q, want %q", value, "my-secret-value")
	}
}

func TestEnvBackend_GetSecret_NotFound(t *testing.T) {
	t.Setenv("TEST_MISSING_KEY", "")

	backend := NewEnvBackend(0)
	ctx := context.Background()

	_, err := backend.GetSecret(ctx, "TEST_MISSING_KEY")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("error should wrap ErrSecretNotFound, got: %v", err)
	}
}

func TestEnvBackend_GetSecret_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	backend := NewEnvBackend(0)

	_, err := backend.GetSecret(ctx, "ANY_KEY")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEnvBackend_Caching(t *testing.T) {
	t.Setenv("TEST_CACHED_KEY", "value1")

	backend := NewEnvBackend(1 * time.Hour) // long TTL
	ctx := context.Background()

	// First read — should cache
	value, err := backend.GetSecret(ctx, "TEST_CACHED_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "value1" {
		t.Errorf("got %q, want %q", value, "value1")
	}

	// Change env var — cached value should persist
	t.Setenv("TEST_CACHED_KEY", "value2")

	value, err = backend.GetSecret(ctx, "TEST_CACHED_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "value1" {
		t.Errorf("cached value should be 'value1', got %q", value)
	}
}

func TestEnvBackend_NoCaching(t *testing.T) {
	t.Setenv("TEST_NOCACHE_KEY", "value1")

	backend := NewEnvBackend(0) // no caching
	ctx := context.Background()

	value, _ := backend.GetSecret(ctx, "TEST_NOCACHE_KEY")
	if value != "value1" {
		t.Errorf("got %q, want %q", value, "value1")
	}

	t.Setenv("TEST_NOCACHE_KEY", "value2")

	value, _ = backend.GetSecret(ctx, "TEST_NOCACHE_KEY")
	if value != "value2" {
		t.Errorf("without caching should read new value, got %q", value)
	}
}

func TestSecretManager_GetSecret(t *testing.T) {
	t.Setenv("TEST_SM_KEY", "managed-secret")

	sm := NewSecretManager(0)
	ctx := context.Background()

	value, err := sm.GetSecret(ctx, "TEST_SM_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "managed-secret" {
		t.Errorf("got %q, want %q", value, "managed-secret")
	}
}

func TestSecretBackendInterface(t *testing.T) {
	var _ SecretBackend = (*EnvBackend)(nil)
}
