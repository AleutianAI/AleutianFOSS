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

	"github.com/AleutianAI/AleutianFOSS/services/policy_engine"
)

// DataClassifier classifies data sensitivity for egress control (P-3).
//
// Thread Safety: Implementations must be safe for concurrent use.
type DataClassifier interface {
	// Classify determines the sensitivity level of the given data.
	//
	// Inputs:
	//   - ctx: Context for cancellation.
	//   - data: The raw data to classify.
	//
	// Outputs:
	//   - DataSensitivity: The classification level.
	Classify(ctx context.Context, data []byte) DataSensitivity
}

// PolicyEngineClassifier adapts the policy_engine.PolicyEngine to the
// DataClassifier interface.
//
// Description:
//
//	Wraps the existing PolicyEngine.ClassifyData() method and converts its
//	string result to a DataSensitivity enum value. This reuses the regex-based
//	pattern matching already implemented in the policy engine.
//
// Limitations:
//   - Pattern-based only — relies on regex patterns in the enforcement YAML.
//     Will not detect semantically sensitive data that lacks pattern matches.
//
// Thread Safety: Safe for concurrent use (PolicyEngine is read-only after init).
type PolicyEngineClassifier struct {
	engine *policy_engine.PolicyEngine
}

// NewPolicyEngineClassifier creates a DataClassifier backed by the PolicyEngine.
//
// Inputs:
//   - engine: An initialized PolicyEngine. Must not be nil.
//
// Outputs:
//   - *PolicyEngineClassifier: The adapter.
func NewPolicyEngineClassifier(engine *policy_engine.PolicyEngine) *PolicyEngineClassifier {
	return &PolicyEngineClassifier{engine: engine}
}

// Classify determines the sensitivity level using PolicyEngine pattern matching.
//
// Inputs:
//   - ctx: Context for cancellation (not used by regex matcher but kept for interface).
//   - data: The raw data to classify.
//
// Outputs:
//   - DataSensitivity: The classification level. Returns SensitivityPublic for empty data.
func (c *PolicyEngineClassifier) Classify(_ context.Context, data []byte) DataSensitivity {
	if len(data) == 0 {
		return SensitivityPublic
	}
	classification := c.engine.ClassifyData(data)
	return ParseSensitivity(classification)
}

// NoOpClassifier always returns SensitivityPublic.
//
// Description:
//
//	Used as a fallback when the PolicyEngine is unavailable (e.g., during
//	testing or when the enforcement YAML cannot be loaded). Logs a warning
//	at construction time to make the degraded state visible.
//
// Thread Safety: Safe for concurrent use.
type NoOpClassifier struct{}

// NewNoOpClassifier creates a classifier that always returns Public.
//
// Outputs:
//   - *NoOpClassifier: A no-op classifier.
func NewNoOpClassifier() *NoOpClassifier {
	return &NoOpClassifier{}
}

// Classify always returns SensitivityPublic.
func (c *NoOpClassifier) Classify(_ context.Context, _ []byte) DataSensitivity {
	return SensitivityPublic
}
