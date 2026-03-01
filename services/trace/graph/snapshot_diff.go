// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"fmt"
	"sort"
)

// SnapshotDiff contains the differences between two graph snapshots.
type SnapshotDiff struct {
	// BaseSnapshotID is the ID of the base snapshot.
	BaseSnapshotID string `json:"base_snapshot_id"`

	// TargetSnapshotID is the ID of the target snapshot.
	TargetSnapshotID string `json:"target_snapshot_id"`

	// NodesAdded are node IDs present in target but not in base.
	NodesAdded []string `json:"nodes_added"`

	// NodesRemoved are node IDs present in base but not in target.
	NodesRemoved []string `json:"nodes_removed"`

	// NodesModified are nodes that changed between snapshots.
	NodesModified []NodeDiff `json:"nodes_modified"`

	// EdgesAdded is the count of edges in target but not in base.
	EdgesAdded int `json:"edges_added"`

	// EdgesRemoved is the count of edges in base but not in target.
	EdgesRemoved int `json:"edges_removed"`

	// Summary contains aggregate statistics about the diff.
	Summary DiffSummary `json:"summary"`
}

// NodeDiff describes how a single node changed between snapshots.
type NodeDiff struct {
	// NodeID is the unique node identifier.
	NodeID string `json:"node_id"`

	// SymbolName is the human-readable name of the symbol.
	SymbolName string `json:"symbol_name"`

	// ChangeType describes what changed: "signature_changed", "moved", "edges_changed".
	ChangeType string `json:"change_type"`
}

// DiffSummary contains aggregate statistics about a diff.
type DiffSummary struct {
	// TotalChanges is the total number of changes (added + removed + modified nodes + edge changes).
	TotalChanges int `json:"total_changes"`

	// FilesAffected is the number of distinct files with changed symbols.
	FilesAffected int `json:"files_affected"`

	// ChangeRatio is the fraction of nodes that changed (0.0 to 1.0).
	ChangeRatio float64 `json:"change_ratio"`
}

// DiffSnapshots computes the differences between two graphs.
//
// Description:
//
//	Compares two graphs (typically loaded from snapshots) and produces a
//	SnapshotDiff describing what changed. Comparison is by node ID —
//	symbols with the same ID in both graphs are compared for modifications.
//	Moved symbols (same name, different file) show as remove + add.
//
// Inputs:
//
//	base - The base graph for comparison. Must not be nil.
//	target - The target graph for comparison. Must not be nil.
//	baseSnapshotID - ID of the base snapshot (for labeling).
//	targetSnapshotID - ID of the target snapshot (for labeling).
//
// Outputs:
//
//	*SnapshotDiff - The computed differences.
//	error - Non-nil if either graph is nil.
//
// Complexity:
//
//	O(V + E) where V is max(baseNodes, targetNodes) and E is max(baseEdges, targetEdges).
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func DiffSnapshots(base, target *Graph, baseSnapshotID, targetSnapshotID string) (*SnapshotDiff, error) {
	if base == nil {
		return nil, fmt.Errorf("base graph must not be nil")
	}
	if target == nil {
		return nil, fmt.Errorf("target graph must not be nil")
	}

	diff := &SnapshotDiff{
		BaseSnapshotID:   baseSnapshotID,
		TargetSnapshotID: targetSnapshotID,
		NodesAdded:       []string{},
		NodesRemoved:     []string{},
		NodesModified:    []NodeDiff{},
	}

	// Use frozen graph node maps directly (read-only, no copy needed)
	affectedFiles := make(map[string]bool)

	// Find added and modified nodes
	for id, tNode := range target.nodes {
		bNode, exists := base.nodes[id]
		if !exists {
			diff.NodesAdded = append(diff.NodesAdded, id)
			if tNode.Symbol != nil {
				affectedFiles[tNode.Symbol.FilePath] = true
			}
			continue
		}

		// Check for modifications
		if nodeChanged(bNode, tNode) {
			changeType := classifyChange(bNode, tNode)
			name := ""
			if tNode.Symbol != nil {
				name = tNode.Symbol.Name
				affectedFiles[tNode.Symbol.FilePath] = true
			}
			if bNode.Symbol != nil {
				affectedFiles[bNode.Symbol.FilePath] = true
			}
			diff.NodesModified = append(diff.NodesModified, NodeDiff{
				NodeID:     id,
				SymbolName: name,
				ChangeType: changeType,
			})
		}
	}

	// Find removed nodes
	for id, bNode := range base.nodes {
		if _, exists := target.nodes[id]; !exists {
			diff.NodesRemoved = append(diff.NodesRemoved, id)
			if bNode.Symbol != nil {
				affectedFiles[bNode.Symbol.FilePath] = true
			}
		}
	}

	// Sort for deterministic output
	sort.Strings(diff.NodesAdded)
	sort.Strings(diff.NodesRemoved)
	sort.Slice(diff.NodesModified, func(i, j int) bool {
		return diff.NodesModified[i].NodeID < diff.NodesModified[j].NodeID
	})

	// Compare edges
	baseEdgeSet := buildEdgeSet(base.edges)
	targetEdgeSet := buildEdgeSet(target.edges)

	for key := range targetEdgeSet {
		if _, exists := baseEdgeSet[key]; !exists {
			diff.EdgesAdded++
		}
	}
	for key := range baseEdgeSet {
		if _, exists := targetEdgeSet[key]; !exists {
			diff.EdgesRemoved++
		}
	}

	// Compute summary
	totalNodes := len(base.nodes)
	if len(target.nodes) > totalNodes {
		totalNodes = len(target.nodes)
	}

	changeRatio := 0.0
	if totalNodes > 0 {
		changedNodes := len(diff.NodesAdded) + len(diff.NodesRemoved) + len(diff.NodesModified)
		changeRatio = float64(changedNodes) / float64(totalNodes)
	}

	diff.Summary = DiffSummary{
		TotalChanges:  len(diff.NodesAdded) + len(diff.NodesRemoved) + len(diff.NodesModified) + diff.EdgesAdded + diff.EdgesRemoved,
		FilesAffected: len(affectedFiles),
		ChangeRatio:   changeRatio,
	}

	return diff, nil
}

// nodeChanged returns true if two nodes with the same ID have different content.
func nodeChanged(base, target *Node) bool {
	if base.Symbol == nil || target.Symbol == nil {
		return base.Symbol != target.Symbol
	}

	// Check signature change
	if base.Symbol.Signature != target.Symbol.Signature {
		return true
	}

	// Check file move
	if base.Symbol.FilePath != target.Symbol.FilePath {
		return true
	}

	// Check line move (significant restructuring)
	if base.Symbol.StartLine != target.Symbol.StartLine {
		return true
	}

	// Check edge count change
	if len(base.Outgoing) != len(target.Outgoing) || len(base.Incoming) != len(target.Incoming) {
		return true
	}

	return false
}

// classifyChange determines the type of change between two nodes.
func classifyChange(base, target *Node) string {
	if base.Symbol == nil || target.Symbol == nil {
		return "signature_changed"
	}

	if base.Symbol.FilePath != target.Symbol.FilePath {
		return "moved"
	}

	if base.Symbol.Signature != target.Symbol.Signature {
		return "signature_changed"
	}

	if len(base.Outgoing) != len(target.Outgoing) || len(base.Incoming) != len(target.Incoming) {
		return "edges_changed"
	}

	return "signature_changed"
}

// buildEdgeSet creates a set of edge keys for comparison.
// Key format: "fromID|toID|typeCode"
func buildEdgeSet(edges []*Edge) map[string]bool {
	set := make(map[string]bool, len(edges))
	for _, e := range edges {
		key := fmt.Sprintf("%s|%s|%d", e.FromID, e.ToID, e.Type)
		set[key] = true
	}
	return set
}
