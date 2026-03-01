// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/gin-gonic/gin"
)

// HandleInspectNode handles GET /v1/trace/debug/graph/inspect.
//
// Description:
//
//	Looks up a symbol by name in the graph and returns its node with
//	incoming and outgoing edges. Used for QA debugging to verify graph
//	topology matches expected code relationships.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (optional, uses first cached if not specified)
//	name: Symbol name to look up (required)
//	kind: Filter results to a specific symbol kind (optional)
//	limit: Maximum edges per direction, default 50 (optional)
//
// Response:
//
//	200 OK: InspectNodeResponse
//	400 Bad Request: Missing required parameter
//	404 Not Found: No graphs cached or graph not found
//
// Thread Safety: This method is safe for concurrent use. Read-only access to graph.
func (h *Handlers) HandleInspectNode(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleInspectNode")

	name := c.Query("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "name parameter is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	kind := c.Query("kind")
	limit := 50
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	cached, graphID, err := h.resolveGraph(c)
	if err != nil {
		return // resolveGraph already wrote the error response
	}

	g := cached.Graph

	// Look up nodes by name
	nodes := g.GetNodesByName(name)

	// Filter by kind if specified
	if kind != "" {
		filtered := make([]*graph.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.Symbol != nil && n.Symbol.Kind.String() == kind {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	response := InspectNodeResponse{
		Matches:   make([]InspectNodeMatch, 0, len(nodes)),
		Truncated: false,
	}

	for _, node := range nodes {
		match := InspectNodeMatch{
			NodeID:   node.ID,
			Symbol:   SymbolInfoFromAST(node.Symbol),
			Outgoing: make([]InspectEdge, 0),
			Incoming: make([]InspectEdge, 0),
		}

		// Build outgoing edges
		for i, edge := range node.Outgoing {
			if i >= limit {
				response.Truncated = true
				break
			}
			peerNode, ok := g.GetNode(edge.ToID)
			if !ok {
				continue
			}
			match.Outgoing = append(match.Outgoing, InspectEdge{
				PeerID:   edge.ToID,
				PeerName: peerNode.Symbol.Name,
				PeerKind: peerNode.Symbol.Kind.String(),
				EdgeType: edge.Type.String(),
				Location: &edge.Location,
			})
		}

		// Build incoming edges
		for i, edge := range node.Incoming {
			if i >= limit {
				response.Truncated = true
				break
			}
			peerNode, ok := g.GetNode(edge.FromID)
			if !ok {
				continue
			}
			match.Incoming = append(match.Incoming, InspectEdge{
				PeerID:   edge.FromID,
				PeerName: peerNode.Symbol.Name,
				PeerKind: peerNode.Symbol.Kind.String(),
				EdgeType: edge.Type.String(),
				Location: &edge.Location,
			})
		}

		response.Matches = append(response.Matches, match)
	}

	logger.Info("inspect node",
		slog.String("graph_id", graphID),
		slog.String("name", name),
		slog.Int("matches", len(response.Matches)),
	)

	c.JSON(http.StatusOK, response)
}

// HandleExportGraph handles GET /v1/trace/debug/graph/export.
//
// Description:
//
//	Exports the full graph as a SerializableGraph JSON stream. Sets
//	Content-Disposition header for download. Uses streaming encoder
//	to avoid buffering the entire JSON in memory.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (optional, uses first cached if not specified)
//
// Response:
//
//	200 OK: SerializableGraph (JSON stream with Content-Disposition: attachment)
//	404 Not Found: No graphs cached or graph not found
//
// Thread Safety: This method is safe for concurrent use. Read-only access to graph.
func (h *Handlers) HandleExportGraph(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleExportGraph")

	cached, graphID, err := h.resolveGraph(c)
	if err != nil {
		return
	}

	sg := cached.Graph.ToSerializable()

	logger.Info("exporting graph",
		slog.String("graph_id", graphID),
		slog.Int("nodes", len(sg.Nodes)),
		slog.Int("edges", len(sg.Edges)),
	)

	c.Header("Content-Disposition", "attachment; filename=graph_"+graphID+".json")
	c.Header("Content-Type", "application/json")

	// Stream directly to response writer
	encoder := json.NewEncoder(c.Writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sg); err != nil {
		logger.Error("failed to encode graph", slog.Any("error", err))
		// Can't write error response since we already started writing the body
	}
}

// HandleSaveSnapshot handles POST /v1/trace/debug/graph/snapshot.
//
// Description:
//
//	Saves the current graph as a persistent snapshot in BadgerDB.
//
// Request Body:
//
//	SaveSnapshotRequest (graph_id optional, label optional)
//
// Response:
//
//	200 OK: SaveSnapshotResponse
//	404 Not Found: Graph not found
//	500 Internal Server Error: Snapshot save failed
//	503 Service Unavailable: Snapshot manager not configured
//
// Thread Safety: This method is safe for concurrent use.
func (h *Handlers) HandleSaveSnapshot(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSaveSnapshot")

	if h.svc.snapshotMgr == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "snapshot persistence not configured",
			Code:  "SNAPSHOTS_NOT_AVAILABLE",
		})
		return
	}

	var req SaveSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body — all fields are optional
		req = SaveSnapshotRequest{}
	}

	// Resolve graph using request's graph_id or first cached
	var cached *CachedGraph
	var resolveErr error

	if req.GraphID != "" {
		cached, resolveErr = h.svc.GetGraph(req.GraphID)
		if resolveErr != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "graph not found",
				Code:  "GRAPH_NOT_FOUND",
			})
			return
		}
	} else {
		cached = h.svc.getFirstGraph()
		if cached == nil {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "no graphs cached",
				Code:  "NO_GRAPHS",
			})
			return
		}
	}

	meta, err := h.svc.snapshotMgr.Save(c.Request.Context(), cached.Graph, req.Label)
	if err != nil {
		logger.Error("snapshot save failed", slog.Any("error", err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to save snapshot: " + err.Error(),
			Code:  "SNAPSHOT_SAVE_FAILED",
		})
		return
	}

	logger.Info("snapshot saved",
		slog.String("snapshot_id", meta.SnapshotID),
		slog.Int("node_count", meta.NodeCount),
	)

	c.JSON(http.StatusOK, SaveSnapshotResponse{
		SnapshotID:     meta.SnapshotID,
		GraphHash:      meta.GraphHash,
		NodeCount:      meta.NodeCount,
		EdgeCount:      meta.EdgeCount,
		CompressedSize: meta.CompressedSize,
	})
}

// HandleListSnapshots handles GET /v1/trace/debug/graph/snapshots.
//
// Description:
//
//	Lists saved graph snapshots, optionally filtered by project root.
//
// Query Parameters:
//
//	project_root: Optional filter by project root path
//	limit: Maximum results, default 100
//
// Response:
//
//	200 OK: ListSnapshotsResponse
//	503 Service Unavailable: Snapshot manager not configured
//
// Thread Safety: This method is safe for concurrent use.
func (h *Handlers) HandleListSnapshots(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleListSnapshots")

	if h.svc.snapshotMgr == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "snapshot persistence not configured",
			Code:  "SNAPSHOTS_NOT_AVAILABLE",
		})
		return
	}

	projectRoot := c.Query("project_root")
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// Convert project_root to project_hash if provided
	projectHash := ""
	if projectRoot != "" {
		projectHash = graph.ProjectHash(projectRoot)
	}

	snapshots, err := h.svc.snapshotMgr.List(c.Request.Context(), projectHash, limit)
	if err != nil {
		logger.Error("failed to list snapshots", slog.Any("error", err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to list snapshots: " + err.Error(),
			Code:  "SNAPSHOT_LIST_FAILED",
		})
		return
	}

	logger.Info("listing snapshots", slog.Int("count", len(snapshots)))

	c.JSON(http.StatusOK, ListSnapshotsResponse{
		Snapshots: snapshots,
	})
}

// HandleLoadSnapshot handles GET /v1/trace/debug/graph/snapshot/:id.
//
// Description:
//
//	Loads a specific snapshot and returns its metadata and basic graph
//	statistics (node count, edge count, hash). The graph is reconstructed
//	to compute these values but is not cached in memory.
//
// Path Parameters:
//
//	id: Snapshot ID (required)
//
// Response:
//
//	200 OK: LoadSnapshotResponse
//	404 Not Found: Snapshot not found
//	503 Service Unavailable: Snapshot manager not configured
//
// Thread Safety: This method is safe for concurrent use.
func (h *Handlers) HandleLoadSnapshot(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleLoadSnapshot")

	if h.svc.snapshotMgr == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "snapshot persistence not configured",
			Code:  "SNAPSHOTS_NOT_AVAILABLE",
		})
		return
	}

	snapshotID := c.Param("id")
	if snapshotID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "snapshot id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	g, meta, err := h.svc.snapshotMgr.Load(c.Request.Context(), snapshotID)
	if err != nil {
		logger.Warn("snapshot not found", slog.String("snapshot_id", snapshotID), slog.Any("error", err))
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "snapshot not found: " + err.Error(),
			Code:  "SNAPSHOT_NOT_FOUND",
		})
		return
	}

	logger.Info("snapshot loaded",
		slog.String("snapshot_id", snapshotID),
		slog.Int("node_count", g.NodeCount()),
	)

	c.JSON(http.StatusOK, LoadSnapshotResponse{
		Metadata:  meta,
		NodeCount: g.NodeCount(),
		EdgeCount: g.EdgeCount(),
		GraphHash: g.Hash(),
	})
}

// HandleDeleteSnapshot handles DELETE /v1/trace/debug/graph/snapshot/:id.
//
// Description:
//
//	Deletes a specific snapshot from BadgerDB.
//
// Path Parameters:
//
//	id: Snapshot ID (required)
//
// Response:
//
//	200 OK: {"deleted": true}
//	404 Not Found: Snapshot not found
//	503 Service Unavailable: Snapshot manager not configured
//
// Thread Safety: This method is safe for concurrent use.
func (h *Handlers) HandleDeleteSnapshot(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDeleteSnapshot")

	if h.svc.snapshotMgr == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "snapshot persistence not configured",
			Code:  "SNAPSHOTS_NOT_AVAILABLE",
		})
		return
	}

	snapshotID := c.Param("id")
	if snapshotID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "snapshot id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	if err := h.svc.snapshotMgr.Delete(c.Request.Context(), snapshotID); err != nil {
		logger.Warn("snapshot delete failed", slog.String("snapshot_id", snapshotID), slog.Any("error", err))
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "snapshot not found or delete failed: " + err.Error(),
			Code:  "SNAPSHOT_NOT_FOUND",
		})
		return
	}

	logger.Info("snapshot deleted", slog.String("snapshot_id", snapshotID))

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// HandleDiffSnapshots handles GET /v1/trace/debug/graph/snapshot/diff.
//
// Description:
//
//	Compares two snapshots and returns the differences.
//
// Query Parameters:
//
//	base: Base snapshot ID (required)
//	target: Target snapshot ID (required)
//
// Response:
//
//	200 OK: SnapshotDiffResponse
//	400 Bad Request: Missing required parameters
//	404 Not Found: Snapshot not found
//	503 Service Unavailable: Snapshot manager not configured
//
// Thread Safety: This method is safe for concurrent use.
func (h *Handlers) HandleDiffSnapshots(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDiffSnapshots")

	if h.svc.snapshotMgr == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "snapshot persistence not configured",
			Code:  "SNAPSHOTS_NOT_AVAILABLE",
		})
		return
	}

	baseID := c.Query("base")
	targetID := c.Query("target")

	if baseID == "" || targetID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "both 'base' and 'target' parameters are required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	// Load both snapshots
	baseGraph, _, err := h.svc.snapshotMgr.Load(c.Request.Context(), baseID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "base snapshot not found: " + err.Error(),
			Code:  "SNAPSHOT_NOT_FOUND",
		})
		return
	}

	targetGraph, _, err := h.svc.snapshotMgr.Load(c.Request.Context(), targetID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "target snapshot not found: " + err.Error(),
			Code:  "SNAPSHOT_NOT_FOUND",
		})
		return
	}

	diff, err := graph.DiffSnapshots(baseGraph, targetGraph, baseID, targetID)
	if err != nil {
		logger.Error("diff failed", slog.Any("error", err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "diff computation failed: " + err.Error(),
			Code:  "DIFF_FAILED",
		})
		return
	}

	logger.Info("snapshot diff computed",
		slog.String("base", baseID),
		slog.String("target", targetID),
		slog.Int("total_changes", diff.Summary.TotalChanges),
	)

	c.JSON(http.StatusOK, SnapshotDiffResponse{Diff: diff})
}

// resolveGraph resolves a CachedGraph from query params (graph_id or project_root),
// falling back to the first cached graph. Writes error response on failure.
//
// Returns the cached graph, its graph ID, and nil error on success.
// On failure, writes the error response and returns a non-nil error.
func (h *Handlers) resolveGraph(c *gin.Context) (*CachedGraph, string, error) {
	graphID := c.Query("graph_id")

	if graphID == "" {
		projectRoot := c.Query("project_root")
		if projectRoot != "" {
			graphID = h.svc.generateGraphID(projectRoot)
		}
	}

	if graphID != "" {
		cached, err := h.svc.GetGraph(graphID)
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "graph not found",
				Code:  "GRAPH_NOT_FOUND",
			})
			return nil, "", err
		}
		return cached, graphID, nil
	}

	cached := h.svc.getFirstGraph()
	if cached == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "no graphs cached",
			Code:  "NO_GRAPHS",
		})
		return nil, "", ErrGraphNotInitialized
	}

	graphID = h.svc.generateGraphID(cached.ProjectRoot)
	return cached, graphID, nil
}
