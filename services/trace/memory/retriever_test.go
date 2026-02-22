// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package memory

import (
	"testing"
	"time"
)

func TestDefaultRetrieverConfig(t *testing.T) {
	config := DefaultRetrieverConfig()

	if config.MaxResults != 10 {
		t.Errorf("expected max results 10, got %d", config.MaxResults)
	}

	if config.ConfidenceWeight != 0.3 {
		t.Errorf("expected confidence weight 0.3, got %f", config.ConfidenceWeight)
	}

	if config.RecencyWeight != 0.2 {
		t.Errorf("expected recency weight 0.2, got %f", config.RecencyWeight)
	}

	if config.RelevanceWeight != 0.5 {
		t.Errorf("expected relevance weight 0.5, got %f", config.RelevanceWeight)
	}

	if config.RecencyDecayDays != 30 {
		t.Errorf("expected recency decay days 30, got %f", config.RecencyDecayDays)
	}

	if !config.AutoMarkUsed {
		t.Error("expected auto mark used to be true")
	}

	// Check weights sum to 1.0
	totalWeight := config.ConfidenceWeight + config.RecencyWeight + config.RelevanceWeight
	if totalWeight != 1.0 {
		t.Errorf("expected weights to sum to 1.0, got %f", totalWeight)
	}
}

func TestMatchScope(t *testing.T) {
	tests := []struct {
		name        string
		memoryScope string
		targetPath  string
		expected    bool
	}{
		{
			name:        "wildcard matches everything",
			memoryScope: "*",
			targetPath:  "services/trace/handlers.go",
			expected:    true,
		},
		{
			name:        "exact match",
			memoryScope: "services/trace/handlers.go",
			targetPath:  "services/trace/handlers.go",
			expected:    true,
		},
		{
			name:        "prefix wildcard matches",
			memoryScope: "services/trace/*",
			targetPath:  "services/trace/handlers.go",
			expected:    true,
		},
		{
			name:        "prefix wildcard doesn't match different path",
			memoryScope: "services/rag_engine/*",
			targetPath:  "services/trace/handlers.go",
			expected:    false,
		},
		{
			name:        "exact mismatch",
			memoryScope: "services/trace/types.go",
			targetPath:  "services/trace/handlers.go",
			expected:    false,
		},
		{
			name:        "empty scope doesn't match",
			memoryScope: "",
			targetPath:  "services/trace/handlers.go",
			expected:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matchScope(tc.memoryScope, tc.targetPath)
			if result != tc.expected {
				t.Errorf("matchScope(%q, %q) = %v, expected %v",
					tc.memoryScope, tc.targetPath, result, tc.expected)
			}
		})
	}
}

func TestCommonPrefix(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name:     "empty paths",
			paths:    []string{},
			expected: "",
		},
		{
			name:     "single path",
			paths:    []string{"services/trace/handlers.go"},
			expected: "services/trace/handlers.go",
		},
		{
			name:     "same directory",
			paths:    []string{"services/trace/handlers.go", "services/trace/types.go"},
			expected: "services/trace",
		},
		{
			name:     "different directories",
			paths:    []string{"services/trace/handlers.go", "services/rag_engine/agent.py"},
			expected: "services",
		},
		{
			name:     "no common prefix",
			paths:    []string{"services/trace/handlers.go", "pkg/utils/helpers.go"},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := commonPrefix(tc.paths)
			if result != tc.expected {
				t.Errorf("commonPrefix(%v) = %q, expected %q", tc.paths, result, tc.expected)
			}
		})
	}
}

func TestCalculateScore(t *testing.T) {
	retriever := &MemoryRetriever{
		config: RetrieverConfig{
			ConfidenceWeight: 0.3,
			RecencyWeight:    0.2,
			RelevanceWeight:  0.5,
			RecencyDecayDays: 30,
		},
	}

	now := time.Now().UnixMilli()

	t.Run("high confidence recent memory scores high", func(t *testing.T) {
		memory := CodeMemory{
			Confidence: 1.0,
			LastUsed:   now,
			Scope:      "services/*",
		}

		score := retriever.calculateScore(memory, 1.0, now, "services/trace")
		// Should be > 1.0 due to scope boost
		if score <= 1.0 {
			t.Errorf("expected high score > 1.0, got %f", score)
		}
	})

	t.Run("low confidence old memory scores low", func(t *testing.T) {
		memory := CodeMemory{
			Confidence: 0.1,
			LastUsed:   time.Now().Add(-365 * 24 * time.Hour).UnixMilli(), // 1 year ago
			Scope:      "pkg/*",
		}

		score := retriever.calculateScore(memory, 0.1, now, "services/trace")
		if score >= 0.2 {
			t.Errorf("expected low score < 0.2, got %f", score)
		}
	})

	t.Run("scope match provides boost", func(t *testing.T) {
		memory := CodeMemory{
			Confidence: 0.5,
			LastUsed:   now,
			Scope:      "services/*",
		}

		scoreWithMatch := retriever.calculateScore(memory, 0.5, now, "services/trace")
		scoreWithoutMatch := retriever.calculateScore(memory, 0.5, now, "pkg/utils")

		if scoreWithMatch <= scoreWithoutMatch {
			t.Errorf("expected scope match to boost score: with=%f, without=%f",
				scoreWithMatch, scoreWithoutMatch)
		}
	})

	t.Run("recency affects score", func(t *testing.T) {
		recentMemory := CodeMemory{
			Confidence: 0.5,
			LastUsed:   now,
			Scope:      "*",
		}

		oldMemory := CodeMemory{
			Confidence: 0.5,
			LastUsed:   time.Now().Add(-60 * 24 * time.Hour).UnixMilli(), // 60 days ago
			Scope:      "*",
		}

		recentScore := retriever.calculateScore(recentMemory, 0.5, now, "")
		oldScore := retriever.calculateScore(oldMemory, 0.5, now, "")

		if recentScore <= oldScore {
			t.Errorf("expected recent memory to score higher: recent=%f, old=%f",
				recentScore, oldScore)
		}
	})
}

func TestRetrieveOptions(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		opts := RetrieveOptions{
			Query: "test query",
		}

		if opts.Limit != 0 {
			t.Errorf("expected default limit 0, got %d", opts.Limit)
		}

		if opts.IncludeArchived != false {
			t.Error("expected IncludeArchived to default to false")
		}

		if opts.MinConfidence != 0 {
			t.Errorf("expected default min confidence 0, got %f", opts.MinConfidence)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		opts := RetrieveOptions{
			Query:           "test query",
			Scope:           "services/*",
			Limit:           5,
			IncludeArchived: true,
			MinConfidence:   0.7,
		}

		if opts.Limit != 5 {
			t.Errorf("expected limit 5, got %d", opts.Limit)
		}

		if !opts.IncludeArchived {
			t.Error("expected IncludeArchived to be true")
		}

		if opts.MinConfidence != 0.7 {
			t.Errorf("expected min confidence 0.7, got %f", opts.MinConfidence)
		}
	})
}
