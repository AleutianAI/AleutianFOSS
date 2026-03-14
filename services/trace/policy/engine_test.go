// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package policy

import (
	"testing"
)

func TestPolicyEngine(t *testing.T) {
	// Initialize the engine once (it's fast!)
	engine, err := NewPolicyEngine()
	if err != nil {
		t.Fatalf("Failed to initialize engine: %v", err)
	}

	// Define test cases (Table-Driven)
	tests := []struct {
		name            string
		input           string
		shouldFind      bool
		expectedClass   string
		expectedPattern string
	}{
		{
			name:          "Safe String",
			input:         "This is a perfectly safe string about the weather.",
			shouldFind:    false,
			expectedClass: "",
		},
		{
			name:            "AWS Access Key (Secret)",
			input:           "My aws key is AKIA1234567890123456 for the prod account.",
			shouldFind:      true,
			expectedClass:   "secret",
			expectedPattern: "AWS_ACCESS_KEY_ID",
		},
		{
			name:            "Email Address (PII)",
			input:           "Please contact jdoe@example.com for support.",
			shouldFind:      true,
			expectedClass:   "pii",
			expectedPattern: "EMAIL_ADDRESS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Test ScanFileContent (Detailed Audit)
			findings := engine.ScanFileContent(tc.input)

			if tc.shouldFind {
				if len(findings) == 0 {
					t.Errorf("Expected to find '%s' but got 0 findings.", tc.expectedPattern)
					return
				}

				// Verify the first finding matches expectations
				first := findings[0]
				if first.ClassificationName != tc.expectedClass {
					t.Errorf("Expected classification '%s', got '%s'", tc.expectedClass, first.ClassificationName)
				}
				if first.PatternId != tc.expectedPattern {
					t.Errorf("Expected pattern ID '%s', got '%s'", tc.expectedPattern, first.PatternId)
				}

				// 2. Test ClassifyData (Fast Check)
				// This verifies that ClassifyData agrees with ScanFileContent
				fastClass := engine.ClassifyData([]byte(tc.input))
				if fastClass != tc.expectedClass {
					t.Errorf("ClassifyData mismatch. Expected '%s', got '%s'", tc.expectedClass, fastClass)
				}

			} else {
				if len(findings) > 0 {
					t.Errorf("Expected 0 findings, got %d. First match: %s", len(findings), findings[0].PatternId)
				}

				// Verify ClassifyData returns "public" for safe strings
				fastClass := engine.ClassifyData([]byte(tc.input))
				if fastClass != "public" {
					t.Errorf("Expected 'public' for safe string, got '%s'", fastClass)
				}
			}
		})
	}
}

func TestEngineInitializationProperties(t *testing.T) {
	engine, err := NewPolicyEngine()
	if err != nil {
		t.Fatalf("Failed to init: %v", err)
	}

	// verify sorting: Priority 100 (Secret) should be before Priority 50 (PII)
	if len(engine.Classifiers) < 2 {
		t.Fatal("Not enough classifiers loaded to test sorting.")
	}

	first := engine.Classifiers[0]
	last := engine.Classifiers[len(engine.Classifiers)-1]

	if first.Priority < last.Priority {
		t.Errorf("Classifiers are not sorted by priority! First: %d, Last: %d", first.Priority, last.Priority)
	}

	if first.Name != "secret" {
		t.Logf("Warning: 'secret' is not the first classifier. The highest priority is currently: %s", first.Name)
	}
}

func TestPolicyEngine_Concurrency(t *testing.T) {
	engine, _ := NewPolicyEngine()
	input := "My fake key is AKIA1234567890123456"

	// Simulate 100 concurrent file scans
	t.Run("ParallelScanning", func(t *testing.T) {
		t.Parallel()
		for i := 0; i < 100; i++ {
			t.Run("Worker", func(t *testing.T) {
				t.Parallel()
				findings := engine.ScanFileContent(input)
				if len(findings) == 0 {
					t.Error("Concurrent scan failed to find secret")
				}
			})
		}
	})
}

func BenchmarkScanSafeString(b *testing.B) {
	engine, _ := NewPolicyEngine()
	input := "This is a standard log line or sentence with no secrets in it whatsoever."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.ScanFileContent(input)
	}
}

func BenchmarkScanSecretString(b *testing.B) {
	engine, _ := NewPolicyEngine()
	input := "My fake key is AKIA1234567890123456 which should be detected."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.ScanFileContent(input)
	}
}
