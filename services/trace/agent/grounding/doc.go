// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package grounding provides anti-hallucination validation for LLM responses.
//
// This package implements an 8-layer defense system to detect and prevent
// hallucinations in LLM-generated responses about code. The layers are:
//
// PREVENTION LAYERS (before generation):
//   - Layer 1: Prompt Engineering - Citation requirements, grounding instructions
//   - Layer 2: Structured Output - Force JSON with evidence links
//
// DETECTION LAYERS (after generation):
//   - Layer 3: Citation Validation - Verify [file:line] references
//   - Layer 4: Tool Result Grounding - EvidenceIndex, claim extraction
//   - Layer 5: Language/Pattern Detection - Wrong-language detection
//
// VERIFICATION LAYERS (second pass):
//   - Layer 6: TMS Integration - Truth Maintenance System for belief tracking
//   - Layer 7: Multi-Sample Consistency - Generate multiple responses, find consensus
//   - Layer 8: Chain-of-Verification - Self-verification step
//
// The package follows the SafetyGate pattern from services/trace/agent/safety/
// for consistency with existing validation infrastructure.
//
// Thread Safety:
//
//	All types in this package are designed for concurrent use.
package grounding
