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
	"sync"
	"time"
)

// RateLimiter implements a sliding window rate limiter per provider.
//
// Description:
//
//	Limits the number of requests per minute to each cloud provider using
//	a sliding window of timestamps. When the limit is exceeded, returns
//	the duration until the next request can be made.
//
//	Ollama is not rate-limited (local provider).
//
// Thread Safety: Safe for concurrent use via sync.Mutex.
type RateLimiter struct {
	mu      sync.Mutex
	limits  map[string]int
	windows map[string][]int64 // timestamps in Unix milliseconds
}

// NewRateLimiter creates a rate limiter with per-provider limits.
//
// Inputs:
//   - limitsPerMin: Per-provider rate limits (requests per minute).
//     Providers not in the map are not rate-limited.
//
// Outputs:
//   - *RateLimiter: Configured rate limiter.
func NewRateLimiter(limitsPerMin map[string]int) *RateLimiter {
	limits := make(map[string]int, len(limitsPerMin))
	for k, v := range limitsPerMin {
		limits[k] = v
	}
	return &RateLimiter{
		limits:  limits,
		windows: make(map[string][]int64),
	}
}

// Allow checks whether a request to the given provider is within the rate limit.
//
// Description:
//
//	If the request is allowed, records the timestamp. Ollama always passes.
//
// Inputs:
//   - provider: The provider name.
//
// Outputs:
//   - bool: True if the request is allowed.
//   - time.Duration: If rate-limited, how long to wait before retrying.
//     Zero if allowed.
func (r *RateLimiter) Allow(provider string) (bool, time.Duration) {
	// Ollama is never rate-limited
	if provider == "ollama" {
		return true, 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	limit, exists := r.limits[provider]
	if !exists || limit == 0 {
		return true, 0 // no limit configured
	}

	now := time.Now().UnixMilli()
	windowStart := now - 60_000 // 1 minute ago

	// Prune expired entries
	timestamps := r.windows[provider]
	pruned := make([]int64, 0, len(timestamps))
	for _, ts := range timestamps {
		if ts > windowStart {
			pruned = append(pruned, ts)
		}
	}

	if len(pruned) >= limit {
		// Rate limited — calculate retry-after
		oldestInWindow := pruned[0]
		retryAfter := time.Duration(oldestInWindow+60_000-now) * time.Millisecond
		r.windows[provider] = pruned
		return false, retryAfter
	}

	// Allowed — record this request
	pruned = append(pruned, now)
	r.windows[provider] = pruned
	return true, 0
}
