// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import "fmt"

// StreamDelta represents a delta event for SSE streaming.
//
// Description:
//
//	Provides a JSON-serializable summary of a CRS delta for Server-Sent Events.
//	Contains the delta type, sequence number, timestamp, and a human-readable
//	summary suitable for real-time observability dashboards.
//
// Thread Safety: Immutable after creation.
type StreamDelta struct {
	// Type is the delta type name (e.g., "proof", "constraint", "similarity").
	Type string `json:"type"`

	// SeqNum is the NATS JetStream message sequence number.
	SeqNum uint64 `json:"seq"`

	// Timestamp is when the delta was created (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`

	// Summary is a human-readable description of the delta.
	Summary string `json:"summary"`
}

// DeltaToStreamDelta converts a Delta to a StreamDelta for SSE.
//
// Description:
//
//	Extracts the type and generates a human-readable summary from a Delta.
//	Used by the SSE endpoint to stream live CRS decisions.
//
// Inputs:
//
//   - delta: The CRS delta to summarize. Must not be nil.
//   - seqNum: The NATS message sequence number.
//   - timestampMs: The delta timestamp (Unix milliseconds UTC).
//
// Outputs:
//
//   - StreamDelta: SSE-ready summary of the delta.
//
// Thread Safety: Safe for concurrent use (read-only on delta).
func DeltaToStreamDelta(delta Delta, seqNum uint64, timestampMs int64) StreamDelta {
	sd := StreamDelta{
		Type:      delta.Type().String(),
		SeqNum:    seqNum,
		Timestamp: timestampMs,
	}

	switch d := delta.(type) {
	case *ProofDelta:
		count := len(d.Updates)
		sd.Summary = fmt.Sprintf("Updated proof numbers for %d symbol(s)", count)
	case *ConstraintDelta:
		added := len(d.Add)
		removed := len(d.Remove)
		updated := len(d.Update)
		sd.Summary = fmt.Sprintf("Constraints: +%d -%d ~%d", added, removed, updated)
	case *SimilarityDelta:
		count := len(d.Updates)
		sd.Summary = fmt.Sprintf("Updated %d similarity score(s)", count)
	case *DependencyDelta:
		added := len(d.AddEdges)
		removed := len(d.RemoveEdges)
		sd.Summary = fmt.Sprintf("Dependencies: +%d -%d edge(s)", added, removed)
	case *HistoryDelta:
		sd.Summary = "Added history entry"
	case *StreamingDelta:
		sd.Summary = "Updated streaming statistics"
	case *CompositeDelta:
		count := len(d.Deltas)
		sd.Summary = fmt.Sprintf("Composite delta with %d sub-delta(s)", count)
	default:
		sd.Summary = fmt.Sprintf("Delta type: %s", delta.Type().String())
	}

	return sd
}
