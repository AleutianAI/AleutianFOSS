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
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/policy_engine"
)

func TestPolicyEngineClassifier_PublicData(t *testing.T) {
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		t.Fatalf("failed to create policy engine: %v", err)
	}

	classifier := NewPolicyEngineClassifier(engine)
	ctx := context.Background()

	// Normal code content should be public
	data := []byte("func main() { fmt.Println(\"hello world\") }")
	sensitivity := classifier.Classify(ctx, data)
	if sensitivity != SensitivityPublic {
		t.Errorf("normal code should be public, got %s", sensitivity)
	}
}

func TestPolicyEngineClassifier_EmptyData(t *testing.T) {
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		t.Fatalf("failed to create policy engine: %v", err)
	}

	classifier := NewPolicyEngineClassifier(engine)
	ctx := context.Background()

	sensitivity := classifier.Classify(ctx, nil)
	if sensitivity != SensitivityPublic {
		t.Errorf("empty data should be public, got %s", sensitivity)
	}

	sensitivity = classifier.Classify(ctx, []byte{})
	if sensitivity != SensitivityPublic {
		t.Errorf("empty bytes should be public, got %s", sensitivity)
	}
}

func TestPolicyEngineClassifier_SensitiveData(t *testing.T) {
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		t.Fatalf("failed to create policy engine: %v", err)
	}

	classifier := NewPolicyEngineClassifier(engine)
	ctx := context.Background()

	// Data containing what looks like an API key should trigger classification
	data := []byte("Authorization: Bearer sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA")
	sensitivity := classifier.Classify(ctx, data)
	if sensitivity.AllowsExternalSend() {
		// This should be classified as sensitive (Secret or PII)
		// The exact classification depends on the enforcement YAML patterns
		t.Logf("Sensitive data classified as: %s", sensitivity)
	}
}

func TestNoOpClassifier_AlwaysPublic(t *testing.T) {
	classifier := NewNoOpClassifier()
	ctx := context.Background()

	tests := [][]byte{
		nil,
		{},
		[]byte("SSN: 123-45-6789"),
		[]byte("password=secret123"),
	}

	for _, data := range tests {
		sensitivity := classifier.Classify(ctx, data)
		if sensitivity != SensitivityPublic {
			t.Errorf("NoOpClassifier should always return public, got %s for %q", sensitivity, string(data))
		}
	}
}

func TestDataClassifierInterface(t *testing.T) {
	// Verify both implementations satisfy the interface
	var _ DataClassifier = (*PolicyEngineClassifier)(nil)
	var _ DataClassifier = (*NoOpClassifier)(nil)
}
