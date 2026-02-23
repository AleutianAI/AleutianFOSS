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

func TestRateLimiter_OllamaNotLimited(t *testing.T) {
	rl := NewRateLimiter(map[string]int{"ollama": 1})

	// Even with a limit of 1, ollama should never be rate limited
	for i := 0; i < 100; i++ {
		ok, _ := rl.Allow("ollama")
		if !ok {
			t.Fatal("ollama should never be rate limited")
		}
	}
}

func TestRateLimiter_NoLimitConfigured(t *testing.T) {
	rl := NewRateLimiter(map[string]int{})

	ok, _ := rl.Allow("anthropic")
	if !ok {
		t.Error("provider with no limit should always be allowed")
	}
}

func TestRateLimiter_WithinLimit(t *testing.T) {
	rl := NewRateLimiter(map[string]int{"anthropic": 5})

	for i := 0; i < 5; i++ {
		ok, _ := rl.Allow("anthropic")
		if !ok {
			t.Errorf("request %d should be within limit", i+1)
		}
	}
}

func TestRateLimiter_ExceedsLimit(t *testing.T) {
	rl := NewRateLimiter(map[string]int{"openai": 3})

	for i := 0; i < 3; i++ {
		ok, _ := rl.Allow("openai")
		if !ok {
			t.Errorf("request %d should be within limit", i+1)
		}
	}

	ok, retryAfter := rl.Allow("openai")
	if ok {
		t.Error("request should be rate limited")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter should be positive, got %v", retryAfter)
	}
}

func TestRateLimiter_IndependentProviders(t *testing.T) {
	rl := NewRateLimiter(map[string]int{
		"anthropic": 2,
		"openai":    2,
	})

	// Exhaust anthropic
	rl.Allow("anthropic")
	rl.Allow("anthropic")
	ok, _ := rl.Allow("anthropic")
	if ok {
		t.Error("anthropic should be rate limited")
	}

	// openai should still be available
	ok, _ = rl.Allow("openai")
	if !ok {
		t.Error("openai should not be rate limited by anthropic's limit")
	}
}

func TestRateLimiter_DefensiveCopy(t *testing.T) {
	limits := map[string]int{"anthropic": 5}
	rl := NewRateLimiter(limits)

	// Mutate the original map
	limits["anthropic"] = 0

	// Rate limiter should use the original value
	for i := 0; i < 5; i++ {
		ok, _ := rl.Allow("anthropic")
		if !ok {
			t.Errorf("request %d should be allowed (defensive copy should prevent mutation)", i+1)
		}
	}
}
