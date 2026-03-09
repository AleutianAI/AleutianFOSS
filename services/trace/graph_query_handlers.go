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
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/gin-gonic/gin"
)

// isGraphStateError returns true if the error indicates the graph is not
// initialized, expired, nil, or not frozen — all client-recoverable states.
func isGraphStateError(err error) bool {
	return errors.Is(err, ErrGraphNotInitialized) ||
		errors.Is(err, ErrGraphExpired) ||
		errors.Is(err, graph.ErrNilGraph) ||
		errors.Is(err, graph.ErrGraphNotFrozen)
}

// isSymbolNotFoundError returns true if the error indicates a symbol name
// could not be resolved — a client input error, not a server failure.
func isSymbolNotFoundError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "function not found") ||
		strings.Contains(msg, "node not found")
}

// HandleFindCallees handles GET /v1/trace/callees.
//
// Description:
//
//	Finds all functions that the given function calls.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//	function: Name of the function to find callees for (required)
//	limit: Maximum number of results (optional, default 50)
//
// Response:
//
//	200 OK: CalleesResponse (may be empty array)
//	400 Bad Request: Missing parameters or graph not initialized
func (h *Handlers) HandleFindCallees(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindCallees")

	var req CalleesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("Invalid query parameters", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid query parameters: graph_id and function are required",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 50
	}

	logger.Info("Finding callees", "graph_id", req.GraphID, "function", req.Function)

	callees, err := h.svc.FindCallees(c.Request.Context(), req.GraphID, req.Function, req.Limit)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find callees failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "QUERY_FAILED",
		})
		return
	}

	logger.Info("Found callees", "count", len(callees))

	c.JSON(http.StatusOK, CalleesResponse{
		Function: req.Function,
		Callees:  callees,
	})
}

// HandleGetCallChain handles GET /v1/trace/call-chain.
//
// Description:
//
//	Finds the shortest call chain between two functions.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//	from: Source function name (required)
//	to: Target function name (required)
//
// Response:
//
//	200 OK: CallChainResponse
//	400 Bad Request: Missing parameters, graph not initialized, or function not found
func (h *Handlers) HandleGetCallChain(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetCallChain")

	var req CallChainRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("Invalid query parameters", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid query parameters: graph_id, from, and to are required",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Getting call chain", "graph_id", req.GraphID, "from", req.From, "to", req.To)

	path, length, err := h.svc.GetCallChain(c.Request.Context(), req.GraphID, req.From, req.To)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		if isSymbolNotFoundError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "SYMBOL_NOT_FOUND",
			})
			return
		}

		logger.Error("Get call chain failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "QUERY_FAILED",
		})
		return
	}

	logger.Info("Found call chain", "length", length)

	c.JSON(http.StatusOK, CallChainResponse{
		From:   req.From,
		To:     req.To,
		Path:   path,
		Length: length,
	})
}

// HandleFindReferences handles GET /v1/trace/references.
//
// Description:
//
//	Finds all locations that reference the given symbol.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//	symbol: Name of the symbol to find references for (required)
//	limit: Maximum number of results (optional, default 50)
//
// Response:
//
//	200 OK: ReferencesResponse (may be empty array)
//	400 Bad Request: Missing parameters or graph not initialized
func (h *Handlers) HandleFindReferences(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindReferences")

	var req ReferencesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("Invalid query parameters", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid query parameters: graph_id and symbol are required",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 50
	}

	logger.Info("Finding references", "graph_id", req.GraphID, "symbol", req.Symbol)

	refs, err := h.svc.FindReferences(c.Request.Context(), req.GraphID, req.Symbol, req.Limit)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find references failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "QUERY_FAILED",
		})
		return
	}

	logger.Info("Found references", "count", len(refs))

	c.JSON(http.StatusOK, ReferencesResponse{
		Symbol:     req.Symbol,
		References: refs,
	})
}

// HandleFindHotspots handles POST /v1/trace/analytics/hotspots.
//
// Description:
//
//	Finds the most-connected nodes in the graph.
//
// Request Body:
//
//	graph_id: ID of the graph to query (required)
//	limit: Maximum number of results (optional, default 10)
//
// Response:
//
//	200 OK: AgenticResponse wrapping hotspot results
//	400 Bad Request: Invalid body or graph not initialized
func (h *Handlers) HandleFindHotspots(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindHotspots")

	var req FindHotspotsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Finding hotspots", "graph_id", req.GraphID, "limit", req.Limit)

	result, err := h.svc.FindHotspots(c.Request.Context(), req.GraphID, req.Limit)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find hotspots failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindCycles handles POST /v1/trace/analytics/cycles.
//
// Description:
//
//	Finds cyclic dependencies in the graph.
//
// Request Body:
//
//	graph_id: ID of the graph to query (required)
//
// Response:
//
//	200 OK: AgenticResponse wrapping cycle results
//	400 Bad Request: Invalid body or graph not initialized
func (h *Handlers) HandleFindCycles(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindCycles")

	var req FindCyclesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Finding cycles", "graph_id", req.GraphID)

	result, err := h.svc.FindCycles(c.Request.Context(), req.GraphID)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find cycles failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindImportant handles POST /v1/trace/analytics/important.
//
// Description:
//
//	Finds the most important nodes by PageRank.
//
// Request Body:
//
//	graph_id: ID of the graph to query (required)
//	limit: Maximum number of results (optional, default 10)
//
// Response:
//
//	200 OK: AgenticResponse wrapping PageRank results
//	400 Bad Request: Invalid body or graph not initialized
func (h *Handlers) HandleFindImportant(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindImportant")

	var req FindImportantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Finding important nodes", "graph_id", req.GraphID, "limit", req.Limit)

	result, err := h.svc.FindImportant(c.Request.Context(), req.GraphID, req.Limit)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find important failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindCommunities handles POST /v1/trace/analytics/communities.
//
// Description:
//
//	Detects code communities in the graph using the Leiden algorithm.
//
// Request Body:
//
//	graph_id: ID of the graph to query (required)
//
// Response:
//
//	200 OK: AgenticResponse wrapping community results
//	400 Bad Request: Invalid body or graph not initialized
func (h *Handlers) HandleFindCommunities(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindCommunities")

	var req FindCommunitiesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Finding communities", "graph_id", req.GraphID)

	result, err := h.svc.FindCommunities(c.Request.Context(), req.GraphID)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		logger.Error("Find communities failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindPath handles POST /v1/trace/analytics/path.
//
// Description:
//
//	Finds the shortest path between two functions.
//
// Request Body:
//
//	graph_id: ID of the graph to query (required)
//	from: Source function name (required)
//	to: Target function name (required)
//
// Response:
//
//	200 OK: AgenticResponse wrapping path result
//	400 Bad Request: Invalid body, graph not initialized, or function not found
func (h *Handlers) HandleFindPath(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindPath")

	var req FindPathRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Finding path", "graph_id", req.GraphID, "from", req.From, "to", req.To)

	result, err := h.svc.FindPath(c.Request.Context(), req.GraphID, req.From, req.To)
	if err != nil {
		if isGraphStateError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   err.Error(),
				Code:    "GRAPH_NOT_INITIALIZED",
				Details: "Ensure /init was called first",
			})
			return
		}

		if isSymbolNotFoundError(err) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "SYMBOL_NOT_FOUND",
			})
			return
		}

		logger.Error("Find path failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}
