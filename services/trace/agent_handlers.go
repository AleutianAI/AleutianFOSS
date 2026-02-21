// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package code_buddy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/routing"
	"github.com/gin-gonic/gin"
)

// AgentHandlers contains the HTTP handlers for the Code Buddy agent.
//
// Thread Safety: AgentHandlers is safe for concurrent use.
type AgentHandlers struct {
	loop         agent.AgentLoop
	svc          *Service
	modelManager *llm.MultiModelManager
}

// NewAgentHandlers creates handlers for the Code Buddy agent.
//
// Description:
//
//	Creates HTTP handlers that wrap the AgentLoop interface.
//	The handlers provide REST endpoints for starting, continuing,
//	and aborting agent sessions.
//	Also initializes a shared MultiModelManager for tool routing.
//
// Inputs:
//
//	loop - The agent loop implementation. Must not be nil.
//	svc - The Code Buddy service for graph initialization. Must not be nil.
//
// Outputs:
//
//	*AgentHandlers - The configured handlers.
//
// Example:
//
//	loop := agent.NewDefaultAgentLoop()
//	svc := code_buddy.NewService(config)
//	handlers := code_buddy.NewAgentHandlers(loop, svc)
func NewAgentHandlers(loop agent.AgentLoop, svc *Service) *AgentHandlers {
	// Get Ollama endpoint from environment or use default
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	return &AgentHandlers{
		loop:         loop,
		svc:          svc,
		modelManager: llm.NewMultiModelManager(ollamaURL),
	}
}

// HandleAgentRun handles POST /v1/codebuddy/agent/run.
//
// Description:
//
//	Starts a new agent session with the given query. The session
//	initializes the code graph (if not already initialized), assembles
//	context, and executes the agent loop until completion or clarification.
//
// Request Body:
//
//	AgentRunRequest
//
// Response:
//
//	200 OK: AgentRunResponse (session completed or needs clarification)
//	400 Bad Request: Validation error
//	409 Conflict: Session already in progress
//	500 Internal Server Error: Processing error
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleAgentRun(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleAgentRun")

	var req AgentRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.Query == "" {
		logger.Warn("Empty query")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Query is required",
			Code:  "EMPTY_QUERY",
		})
		return
	}

	logger.Info("Starting agent session",
		"project_root", req.ProjectRoot,
		"query_len", len(req.Query))

	// Create session with optional config
	var sessionConfig *agent.SessionConfig
	if req.Config != nil {
		sessionConfig = req.Config
	}

	session, err := agent.NewSession(req.ProjectRoot, sessionConfig)
	if err != nil {
		logger.Error("Failed to create session", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
			Code:  "INVALID_CONFIG",
		})
		return
	}

	logger.Info("Session created",
		"session_id", session.ID,
		"state", session.State,
		"tool_router_enabled", session.Config.ToolRouterEnabled,
		"tool_router_model", session.Config.ToolRouterModel)

	// Initialize tool router if enabled
	if session.Config.ToolRouterEnabled {
		// PRE-FLIGHT CHECK: Verify router model is available
		logger.Info("=== PRE-FLIGHT CHECK: Verifying router model ===",
			"session_id", session.ID,
			"router_model", session.Config.ToolRouterModel,
			"ollama_endpoint", h.getOllamaEndpoint(),
			"has_model_manager", h.modelManager != nil)

		if preflightErr := h.verifyRouterModelAvailable(c.Request.Context(), session.Config.ToolRouterModel, logger); preflightErr != nil {
			logger.Error("⚠️  PRE-FLIGHT CHECK FAILED",
				"session_id", session.ID,
				"router_model", session.Config.ToolRouterModel,
				"error", preflightErr,
				"error_type", fmt.Sprintf("%T", preflightErr))
		} else {
			logger.Info("✓ PRE-FLIGHT CHECK PASSED",
				"session_id", session.ID,
				"router_model", session.Config.ToolRouterModel)
		}

		logger.Info("Tool router initialization starting",
			"session_id", session.ID,
			"model", session.Config.ToolRouterModel,
			"timeout", session.Config.ToolRouterTimeout,
			"confidence_threshold", session.Config.ToolRouterConfidence,
			"has_model_manager", h.modelManager != nil,
			"ollama_endpoint", h.getOllamaEndpoint())

		if err := h.initializeToolRouter(c.Request.Context(), session, logger); err != nil {
			// Log warning but don't fail - tool routing is optional
			logger.Warn("Failed to initialize tool router, continuing without it",
				"session_id", session.ID,
				"error", err,
				"model", session.Config.ToolRouterModel,
				"error_type", fmt.Sprintf("%T", err))

			// Also log if router is actually nil after failure
			if session.GetToolRouter() == nil {
				logger.Warn("Router is nil after initialization failure",
					"session_id", session.ID)
			}
		} else {
			// Verify router was actually set
			if session.GetToolRouter() != nil {
				logger.Info("Tool router initialization successful",
					"session_id", session.ID,
					"router_enabled", session.IsToolRouterEnabled())
			} else {
				logger.Error("Router initialization succeeded but router is nil",
					"session_id", session.ID)
			}
		}
	} else {
		logger.Info("Tool router disabled in config",
			"session_id", session.ID)
	}

	// Run the agent loop
	result, err := h.loop.Run(c.Request.Context(), session, req.Query)
	if err != nil {
		statusCode := http.StatusInternalServerError
		errCode := "AGENT_ERROR"

		if errors.Is(err, agent.ErrInvalidSession) {
			statusCode = http.StatusBadRequest
			errCode = "INVALID_SESSION"
		} else if errors.Is(err, agent.ErrEmptyQuery) {
			statusCode = http.StatusBadRequest
			errCode = "EMPTY_QUERY"
		} else if errors.Is(err, agent.ErrSessionInProgress) {
			statusCode = http.StatusConflict
			errCode = "SESSION_IN_PROGRESS"
		}

		logger.Error("Agent run failed", "error", err)
		c.JSON(statusCode, ErrorResponse{
			Error: err.Error(),
			Code:  errCode,
		})
		return
	}

	logger.Info("Agent session completed",
		"session_id", session.ID,
		"state", result.State,
		"steps_taken", result.StepsTaken)

	c.JSON(http.StatusOK, AgentRunResponse{
		SessionID:    session.ID,
		State:        string(result.State),
		StepsTaken:   result.StepsTaken,
		TokensUsed:   result.TokensUsed,
		Response:     result.Response,
		NeedsClarify: result.NeedsClarify,
		Error:        agentErrorToString(result.Error),
		DegradedMode: session.GetMetrics().DegradedMode,
	})
}

// HandleAgentContinue handles POST /v1/codebuddy/agent/continue.
//
// Description:
//
//	Continues an existing agent session that is waiting for clarification.
//	The session must be in the CLARIFY state to accept continuation.
//
// Request Body:
//
//	AgentContinueRequest
//
// Response:
//
//	200 OK: AgentRunResponse
//	400 Bad Request: Session not in CLARIFY state
//	404 Not Found: Session not found
//	500 Internal Server Error: Processing error
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleAgentContinue(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleAgentContinue")

	var req AgentContinueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.SessionID == "" {
		logger.Warn("Missing session_id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "session_id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Continuing agent session",
		"session_id", req.SessionID,
		"clarification_len", len(req.Clarification))

	result, err := h.loop.Continue(c.Request.Context(), req.SessionID, req.Clarification)
	if err != nil {
		statusCode := http.StatusInternalServerError
		errCode := "AGENT_ERROR"

		if errors.Is(err, agent.ErrSessionNotFound) {
			statusCode = http.StatusNotFound
			errCode = "SESSION_NOT_FOUND"
		} else if errors.Is(err, agent.ErrNotInClarifyState) {
			statusCode = http.StatusBadRequest
			errCode = "NOT_IN_CLARIFY_STATE"
		} else if errors.Is(err, agent.ErrSessionInProgress) {
			statusCode = http.StatusConflict
			errCode = "SESSION_IN_PROGRESS"
		}

		logger.Error("Agent continue failed", "error", err)
		c.JSON(statusCode, ErrorResponse{
			Error: err.Error(),
			Code:  errCode,
		})
		return
	}

	// Get state info including degraded mode
	state, _ := h.loop.GetState(req.SessionID)
	degradedMode := false
	if state != nil {
		degradedMode = state.DegradedMode
	}

	logger.Info("Agent session continued",
		"session_id", req.SessionID,
		"state", result.State,
		"steps_taken", result.StepsTaken)

	c.JSON(http.StatusOK, AgentRunResponse{
		SessionID:    req.SessionID,
		State:        string(result.State),
		StepsTaken:   result.StepsTaken,
		TokensUsed:   result.TokensUsed,
		Response:     result.Response,
		NeedsClarify: result.NeedsClarify,
		Error:        agentErrorToString(result.Error),
		DegradedMode: degradedMode,
	})
}

// HandleAgentAbort handles POST /v1/codebuddy/agent/abort.
//
// Description:
//
//	Aborts an active agent session. The session will transition
//	to the ERROR state and any in-progress operations will be cancelled.
//
// Request Body:
//
//	AgentAbortRequest
//
// Response:
//
//	200 OK: Success message
//	404 Not Found: Session not found
//	500 Internal Server Error: Processing error
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleAgentAbort(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleAgentAbort")

	var req AgentAbortRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.SessionID == "" {
		logger.Warn("Missing session_id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "session_id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Aborting agent session", "session_id", req.SessionID)

	err := h.loop.Abort(c.Request.Context(), req.SessionID)
	if err != nil {
		statusCode := http.StatusInternalServerError
		errCode := "AGENT_ERROR"

		if errors.Is(err, agent.ErrSessionNotFound) {
			statusCode = http.StatusNotFound
			errCode = "SESSION_NOT_FOUND"
		}

		logger.Error("Agent abort failed", "error", err)
		c.JSON(statusCode, ErrorResponse{
			Error: err.Error(),
			Code:  errCode,
		})
		return
	}

	logger.Info("Agent session aborted", "session_id", req.SessionID)

	c.JSON(http.StatusOK, gin.H{
		"message":    "Session aborted successfully",
		"session_id": req.SessionID,
	})
}

// HandleAgentState handles GET /v1/codebuddy/agent/:id.
//
// Description:
//
//	Retrieves the current state of an agent session including
//	metrics, history, and any pending clarification prompts.
//
// Path Parameters:
//
//	id: Session ID (required)
//
// Response:
//
//	200 OK: AgentStateResponse
//	404 Not Found: Session not found
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleAgentState(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleAgentState")

	sessionID := c.Param("id")
	if sessionID == "" {
		logger.Warn("Missing session id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "session id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Getting agent session state", "session_id", sessionID)

	state, err := h.loop.GetState(sessionID)
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "SESSION_NOT_FOUND",
			})
			return
		}

		logger.Error("Get session state failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "GET_STATE_FAILED",
		})
		return
	}

	logger.Info("Got agent session state",
		"session_id", sessionID,
		"state", state.State)

	c.JSON(http.StatusOK, AgentStateResponse{
		SessionID:    state.ID,
		ProjectRoot:  state.ProjectRoot,
		GraphID:      state.GraphID,
		State:        string(state.State),
		StepCount:    state.StepCount,
		TokensUsed:   state.TokensUsed,
		CreatedAt:    state.CreatedAt / 1000,    // Convert millis to seconds
		LastActiveAt: state.LastActiveAt / 1000, // Convert millis to seconds
		DegradedMode: state.DegradedMode,
	})
}

// HandleGetReasoningTrace handles GET /v1/codebuddy/agent/:id/reasoning.
//
// Description:
//
//	Retrieves the step-by-step reasoning trace for a session.
//	The trace shows what actions were taken, what was found,
//	and how the CRS was updated during reasoning.
//
// Path Parameters:
//
//	id: Session ID (required)
//
// Response:
//
//	200 OK: ReasoningTraceResponse
//	404 Not Found: Session not found
//	204 No Content: Session exists but trace recording not enabled
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleGetReasoningTrace(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetReasoningTrace")

	sessionID := c.Param("id")
	if sessionID == "" {
		logger.Warn("Missing session id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "session id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Getting reasoning trace", "session_id", sessionID)

	session, err := h.loop.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "SESSION_NOT_FOUND",
			})
			return
		}

		logger.Error("Get session failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "GET_SESSION_FAILED",
		})
		return
	}

	trace := session.GetReasoningTrace()
	if trace == nil {
		// Trace recording not enabled for this session
		c.Status(http.StatusNoContent)
		return
	}

	// Convert to API response
	response := convertReasoningTrace(trace, session.GetReasoningSummary())

	logger.Info("Got reasoning trace",
		"session_id", sessionID,
		"total_steps", response.TotalSteps)

	c.JSON(http.StatusOK, response)
}

// HandleGetCRSExport handles GET /v1/codebuddy/agent/:id/crs.
//
// Description:
//
//	Retrieves the full CRS (Code Reasoning State) export for a session.
//	This includes all six indexes and summary metrics for debugging
//	and analysis of the reasoning process.
//
// Path Parameters:
//
//	id: Session ID (required)
//
// Response:
//
//	200 OK: CRSExportResponse
//	404 Not Found: Session not found
//	204 No Content: Session exists but CRS not enabled
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleGetCRSExport(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetCRSExport")

	sessionID := c.Param("id")
	if sessionID == "" {
		logger.Warn("Missing session id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "session id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Getting CRS export", "session_id", sessionID)

	session, err := h.loop.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, agent.ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "SESSION_NOT_FOUND",
			})
			return
		}

		logger.Error("Get session failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "GET_SESSION_FAILED",
		})
		return
	}

	export := session.GetCRSExport()
	if export == nil {
		// CRS not enabled for this session
		c.Status(http.StatusNoContent)
		return
	}

	// Convert to API response
	response := convertCRSExport(export)

	logger.Info("Got CRS export",
		"session_id", sessionID,
		"generation", response.Generation)

	c.JSON(http.StatusOK, response)
}

// HandleDebugCRS handles GET /v1/codebuddy/agent/debug/crs.
//
// Description:
//
//	Returns CRS debug information for all sessions or a specific session.
//	Used by integration tests to verify CRS state and circuit breaker behavior.
//	GR-Phase1 Issue 5: Debug endpoint for CRS observability.
//
// Query Parameters:
//
//	session_id: Optional session ID for detailed view
//
// Response:
//
//	200 OK: DebugCRSResponse (summary or detail depending on session_id)
//	404 Not Found: Session not found (when session_id provided)
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleDebugCRS(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDebugCRS")

	sessionID := c.Query("session_id")

	if sessionID != "" {
		// Detail mode: return specific session's CRS state
		logger.Info("Getting CRS debug detail", "session_id", sessionID)

		session, err := h.loop.GetSession(sessionID)
		if err != nil {
			if errors.Is(err, agent.ErrSessionNotFound) {
				c.JSON(http.StatusNotFound, ErrorResponse{
					Error: err.Error(),
					Code:  "SESSION_NOT_FOUND",
				})
				return
			}
			logger.Error("Get session failed", "error", err)
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error: err.Error(),
				Code:  "GET_SESSION_FAILED",
			})
			return
		}

		// Build detailed response
		detail := h.buildCRSSessionDetail(session)

		c.JSON(http.StatusOK, DebugCRSResponse{
			ActiveSessions:        1,
			TotalTraceSteps:       len(detail.TraceSteps),
			CircuitBreakersActive: boolToInt(detail.CircuitBreaker != nil && detail.CircuitBreaker.Active),
			Session:               detail,
		})
		return
	}

	// Summary mode: return all sessions
	logger.Info("Getting CRS debug summary")

	summaries := h.loop.ListSessions()

	// Build response
	totalSteps := 0
	cbActive := 0
	sessionSummaries := make([]DebugCRSSessionSummary, 0, len(summaries))

	for _, s := range summaries {
		totalSteps += s.TraceStepCount
		if s.CircuitBreakerActive {
			cbActive++
		}
		sessionSummaries = append(sessionSummaries, DebugCRSSessionSummary{
			ID:                   s.ID,
			State:                s.State.String(),
			TraceSteps:           s.TraceStepCount,
			CircuitBreakerActive: s.CircuitBreakerActive,
			CreatedAtMilli:       s.CreatedAt,
		})
	}

	logger.Info("Got CRS debug summary",
		"session_count", len(summaries),
		"total_steps", totalSteps,
		"cb_active", cbActive)

	c.JSON(http.StatusOK, DebugCRSResponse{
		ActiveSessions:        len(summaries),
		TotalTraceSteps:       totalSteps,
		CircuitBreakersActive: cbActive,
		Sessions:              sessionSummaries,
	})
}

// buildCRSSessionDetail builds detailed CRS info for a session.
func (h *AgentHandlers) buildCRSSessionDetail(session *agent.Session) *DebugCRSSessionDetail {
	traceSteps := session.GetTraceSteps()
	debugSteps := make([]DebugCRSTraceStep, 0, len(traceSteps))

	for _, step := range traceSteps {
		debugSteps = append(debugSteps, DebugCRSTraceStep{
			Action:     step.Action,
			Tool:       step.Tool,
			Target:     step.Target,
			DurationMs: step.Duration.Milliseconds(),
			Error:      step.Error,
			Metadata:   step.Metadata,
		})
	}

	// Build tool counts from trace steps
	toolCounts := make(map[string]int)
	for _, step := range traceSteps {
		if step.Action == "tool_call" && step.Tool != "" {
			toolCounts[step.Tool]++
		}
	}

	detail := &DebugCRSSessionDetail{
		SessionID:  session.ID,
		State:      session.State.String(),
		TraceSteps: debugSteps,
		ToolCounts: toolCounts,
		Metrics: DebugCRSMetrics{
			ToolCalls:  session.GetMetric(agent.MetricToolCalls),
			TokensUsed: session.GetMetric(agent.MetricTokens),
			Steps:      session.GetMetric(agent.MetricSteps),
			// RouterCalls not tracked as separate metric yet
		},
	}

	// Add circuit breaker info if active
	if session.IsCircuitBreakerActive() {
		detail.CircuitBreaker = &DebugCRSCircuitBreaker{
			Active:    true,
			Threshold: crs.DefaultCircuitBreakerThreshold,
		}
	}

	return detail
}

// boolToInt converts bool to int (1 for true, 0 for false).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// HandleDebugHistory handles GET /v1/codebuddy/agent/debug/history.
//
// Description:
//
//	Returns query history across all sessions or for a specific session.
//	Used by integration tests to verify reasoning history and query flow.
//	GR-Phase1 Issue 5b: Debug endpoint for query history.
//
// Query Parameters:
//
//	session_id: Optional session ID for filtering to one session
//	limit: Optional max entries to return (default 100)
//
// Response:
//
//	200 OK: DebugHistoryResponse
//	404 Not Found: Session not found (when session_id provided)
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) HandleDebugHistory(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDebugHistory")

	sessionID := c.Query("session_id")
	limitStr := c.Query("limit")

	// Parse limit with default
	limit := 100
	if limitStr != "" {
		if parsed, err := parseIntParam(limitStr, 1, 1000); err == nil {
			limit = parsed
		}
	}

	if sessionID != "" {
		// Single session mode
		logger.Info("Getting history for specific session",
			slog.String("session_id", sessionID),
			slog.Int("limit", limit))

		session, err := h.loop.GetSession(sessionID)
		if err != nil {
			if errors.Is(err, agent.ErrSessionNotFound) {
				c.JSON(http.StatusNotFound, ErrorResponse{
					Error: err.Error(),
					Code:  "SESSION_NOT_FOUND",
				})
				return
			}
			logger.Error("Get session failed", slog.String("error", err.Error()))
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error: err.Error(),
				Code:  "GET_SESSION_FAILED",
			})
			return
		}

		entries := h.buildHistoryEntriesForSession(session, limit)

		c.JSON(http.StatusOK, DebugHistoryResponse{
			Count:    len(entries),
			Sessions: []string{sessionID},
			History:  entries,
			Limit:    limit,
		})
		return
	}

	// All sessions mode
	logger.Info("Getting history for all sessions", slog.Int("limit", limit))

	summaries := h.loop.ListSessions()
	allEntries := make([]DebugHistoryEntry, 0)
	sessionIDs := make([]string, 0, len(summaries))

	for _, summary := range summaries {
		sessionIDs = append(sessionIDs, summary.ID)

		session, err := h.loop.GetSession(summary.ID)
		if err != nil {
			logger.Warn("Failed to get session for history",
				slog.String("session_id", summary.ID),
				slog.String("error", err.Error()))
			continue
		}

		entries := h.buildHistoryEntriesForSession(session, limit-len(allEntries))
		allEntries = append(allEntries, entries...)

		if len(allEntries) >= limit {
			break
		}
	}

	// Trim to limit
	if len(allEntries) > limit {
		allEntries = allEntries[:limit]
	}

	logger.Info("Got history for all sessions",
		slog.Int("session_count", len(sessionIDs)),
		slog.Int("entry_count", len(allEntries)))

	c.JSON(http.StatusOK, DebugHistoryResponse{
		Count:    len(allEntries),
		Sessions: sessionIDs,
		History:  allEntries,
		Limit:    limit,
	})
}

// buildHistoryEntriesForSession extracts query history entries from a session.
func (h *AgentHandlers) buildHistoryEntriesForSession(session *agent.Session, maxEntries int) []DebugHistoryEntry {
	if maxEntries <= 0 {
		return nil
	}

	history := session.GetHistory()
	entries := make([]DebugHistoryEntry, 0)

	// Extract query from plan entries
	var currentQuery string
	var queryTimestamp int64
	var toolsUsed []string

	for _, histEntry := range history {
		if histEntry.Type == "plan" && histEntry.Query != "" {
			// Found a new query
			if currentQuery != "" {
				// Save previous query entry
				entry := DebugHistoryEntry{
					SessionID:  session.ID,
					Timestamp:  queryTimestamp,
					Query:      currentQuery,
					State:      string(session.State),
					ToolsUsed:  toolsUsed,
					DurationMs: histEntry.Timestamp - queryTimestamp,
				}
				entries = append(entries, entry)
				if len(entries) >= maxEntries {
					return entries
				}
			}
			currentQuery = histEntry.Query
			queryTimestamp = histEntry.Timestamp
			toolsUsed = make([]string, 0)
		} else if histEntry.Type == "tool_call" && histEntry.ToolName != "" {
			// Record tool usage for current query
			toolsUsed = append(toolsUsed, histEntry.ToolName)
		}
	}

	// Add final query if exists
	if currentQuery != "" {
		durationMs := int64(0)
		if queryTimestamp > 0 {
			durationMs = time.Now().UnixMilli() - queryTimestamp
		}
		entry := DebugHistoryEntry{
			SessionID:  session.ID,
			Timestamp:  queryTimestamp,
			Query:      currentQuery,
			State:      string(session.State),
			ToolsUsed:  toolsUsed,
			DurationMs: durationMs,
		}
		entries = append(entries, entry)
	}

	return entries
}

// parseIntParam parses an integer parameter with bounds checking.
func parseIntParam(s string, min, max int) (int, error) {
	var val int
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return 0, err
	}
	if val < min {
		val = min
	}
	if val > max {
		val = max
	}
	return val, nil
}

// initializeToolRouter sets up the micro-LLM tool router for a session.
//
// Description:
//
//	Creates a Granite4Router with the session's configuration and wraps it
//	with a RouterAdapter to implement the agent.ToolRouter interface.
//	Blocks until the router model is warmed to ensure tool selection works.
//
// Inputs:
//
//	ctx - Context for the request (used for logging, not warmup).
//	session - The session to configure.
//	logger - Logger for diagnostics.
//
// Outputs:
//
//	error - Non-nil if router creation or warmup fails.
//
// Thread Safety: This method is safe for concurrent use.
func (h *AgentHandlers) initializeToolRouter(ctx context.Context, session *agent.Session, logger *slog.Logger) error {
	logger.Info("initializeToolRouter: Entering function",
		"session_id", session.ID,
		"has_model_manager", h.modelManager != nil)

	// Build router config from session config
	routerConfig := routing.RouterConfig{
		Model:               session.Config.ToolRouterModel,
		OllamaEndpoint:      h.getOllamaEndpoint(),
		Timeout:             session.Config.ToolRouterTimeout,
		ConfidenceThreshold: session.Config.ToolRouterConfidence,
		Temperature:         0.1, // Low temperature for consistent routing
		MaxTokens:           256,
		KeepAlive:           "24h", // Keep model loaded (24 hours)
		NumCtx:              16384, // 16K context window (router doesn't need huge context)
	}

	logger.Info("initializeToolRouter: Config built",
		"model", routerConfig.Model,
		"endpoint", routerConfig.OllamaEndpoint,
		"timeout", routerConfig.Timeout)

	// Check prerequisites
	if h.modelManager == nil {
		logger.Error("initializeToolRouter: modelManager is nil",
			"session_id", session.ID)
		routing.RecordRouterInit(session.Config.ToolRouterModel, false, "model_manager_nil")
		return fmt.Errorf("modelManager is nil, cannot initialize router")
	}

	// Create the Granite4 router
	logger.Info("initializeToolRouter: Creating Granite4Router",
		"session_id", session.ID)

	router, err := routing.NewGranite4Router(h.modelManager, routerConfig)
	if err != nil {
		logger.Error("initializeToolRouter: NewGranite4Router failed",
			"session_id", session.ID,
			"error", err,
			"error_type", fmt.Sprintf("%T", err))
		routing.RecordRouterInit(session.Config.ToolRouterModel, false, "router_creation_failed")
		return fmt.Errorf("failed to create router: %w", err)
	}

	logger.Info("initializeToolRouter: Router created successfully",
		"session_id", session.ID)

	// Wrap with adapter to implement agent.ToolRouter interface
	adapter := routing.NewRouterAdapter(router)

	// Set router on session
	session.SetToolRouter(adapter)

	logger.Info("initializeToolRouter: Router set on session",
		"session_id", session.ID,
		"router_enabled_check", session.IsToolRouterEnabled())

	// Warm the router model SYNCHRONOUSLY with background context.
	// We use context.Background() with timeout instead of request context
	// because the request context may be cancelled when HTTP response is sent,
	// but we need the model to stay loaded for subsequent tool routing calls.
	warmupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger.Info("initializeToolRouter: Starting model warmup",
		"model", routerConfig.Model,
		"timeout", "60s")

	warmupStart := time.Now()
	if warmErr := router.WarmRouter(warmupCtx); warmErr != nil {
		warmupDuration := time.Since(warmupStart)
		logger.Error("initializeToolRouter: Model warmup failed",
			"session_id", session.ID,
			"error", warmErr,
			"model", routerConfig.Model,
			"error_type", fmt.Sprintf("%T", warmErr))
		routing.RecordRouterInit(session.Config.ToolRouterModel, false, "warmup_failed")

		// C-1: Record warmup failure as TraceStep in CRS
		session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "model_warmup",
			Target:    routerConfig.Model,
			Tool:      "router",
			Duration:  warmupDuration,
			Error:     warmErr.Error(),
			Metadata: map[string]string{
				"success":  "false",
				"endpoint": routerConfig.OllamaEndpoint,
			},
		})

		return fmt.Errorf("failed to warm router: %w", warmErr)
	}

	warmupDuration := time.Since(warmupStart)

	// IT-06c I-14: Re-warm the main agent model after loading the router model.
	// Loading granite4:micro-h into VRAM may evict the main model (gpt-oss:20b)
	// on resource-constrained systems. Re-warming ensures the first llm_call
	// doesn't pay a ~14s cold-load penalty.
	mainModel := os.Getenv("OLLAMA_MODEL")
	if mainModel != "" && mainModel != routerConfig.Model && h.modelManager != nil {
		mainWarmStart := time.Now()
		if warmErr := h.modelManager.WarmModel(warmupCtx, mainModel, "24h", 65536); warmErr != nil {
			logger.Warn("initializeToolRouter: Main model re-warm failed (non-fatal)",
				"session_id", session.ID,
				"model", mainModel,
				"error", warmErr)
		} else {
			logger.Info("initializeToolRouter: Main model re-warmed",
				"session_id", session.ID,
				"model", mainModel,
				"duration", time.Since(mainWarmStart))
		}
	}

	logger.Info("initializeToolRouter: Complete - Router fully initialized",
		"session_id", session.ID,
		"model", routerConfig.Model,
		"router_enabled", session.IsToolRouterEnabled())

	// Record successful initialization
	routing.RecordRouterInit(session.Config.ToolRouterModel, true, "")

	// C-1: Record warmup success as TraceStep in CRS
	session.RecordTraceStep(crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "model_warmup",
		Target:    routerConfig.Model,
		Tool:      "router",
		Duration:  warmupDuration,
		Metadata: map[string]string{
			"success":     "true",
			"endpoint":    routerConfig.OllamaEndpoint,
			"duration_ms": fmt.Sprintf("%d", warmupDuration.Milliseconds()),
		},
	})

	return nil
}

// getOllamaEndpoint returns the Ollama endpoint from environment or default.
func (h *AgentHandlers) getOllamaEndpoint() string {
	endpoint := os.Getenv("OLLAMA_URL")
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	return endpoint
}

// verifyRouterModelAvailable performs a pre-flight check to ensure the router model
// is available and responding on Ollama before attempting to initialize the router.
//
// # Description
//
// Sends a test request to the router model to verify it's loaded and accessible.
// This helps diagnose issues where router initialization fails silently due to
// model unavailability.
//
// # Inputs
//
//	ctx - Context for the request.
//	routerModel - The router model name to test (e.g., "granite4:micro-h").
//	logger - Logger for diagnostic output.
//
// # Outputs
//
//	error - Non-nil if model is not available or fails to respond.
func (h *AgentHandlers) verifyRouterModelAvailable(ctx context.Context, routerModel string, logger *slog.Logger) error {
	logger.Info("verifyRouterModelAvailable: Testing router model connectivity",
		"model", routerModel,
		"endpoint", h.getOllamaEndpoint())

	if h.modelManager == nil {
		logger.Error("verifyRouterModelAvailable: modelManager is nil")
		return fmt.Errorf("modelManager is nil")
	}

	// Create test messages
	messages := []datatypes.Message{
		{Role: "user", Content: "test"},
	}

	// Create minimal params with short timeout
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	temp := float32(0.1)
	maxTokens := 10
	numCtx := 16384
	params := llm.GenerationParams{
		Temperature:   &temp,
		MaxTokens:     &maxTokens,
		NumCtx:        &numCtx,
		ModelOverride: routerModel,
	}

	logger.Info("verifyRouterModelAvailable: Sending test request",
		"model", routerModel,
		"timeout", "10s")

	response, err := h.modelManager.Chat(testCtx, routerModel, messages, params)
	if err != nil {
		logger.Error("verifyRouterModelAvailable: Test request failed",
			"model", routerModel,
			"error", err,
			"error_type", fmt.Sprintf("%T", err))
		return fmt.Errorf("router model test failed: %w", err)
	}

	if response == "" {
		logger.Warn("verifyRouterModelAvailable: Model returned empty response",
			"model", routerModel)
		return fmt.Errorf("router model returned empty response")
	}

	logger.Info("verifyRouterModelAvailable: Router model is available and responding",
		"model", routerModel,
		"response_length", len(response))

	return nil
}

// agentErrorToString converts an AgentError to a string, returning empty string for nil.
//
// Inputs:
//
//	err - The agent error to convert.
//
// Outputs:
//
//	string - The error message or empty string.
func agentErrorToString(err *agent.AgentError) string {
	if err == nil {
		return ""
	}
	return err.Message
}

// convertReasoningTrace converts crs.ReasoningTrace to ReasoningTraceResponse.
//
// Inputs:
//
//	trace - The CRS reasoning trace.
//	summary - The reasoning summary (optional).
//
// Outputs:
//
//	*ReasoningTraceResponse - The API response.
func convertReasoningTrace(trace *crs.ReasoningTrace, summary *agent.ReasoningSummary) *ReasoningTraceResponse {
	if trace == nil {
		return nil
	}

	response := &ReasoningTraceResponse{
		SessionID:  trace.SessionID,
		TotalSteps: trace.TotalSteps,
		Duration:   trace.Duration,
		Trace:      make([]ReasoningStep, 0, len(trace.Trace)),
	}

	if trace.StartTime != 0 {
		response.StartTime = time.UnixMilli(trace.StartTime).UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if trace.EndTime != 0 {
		response.EndTime = time.UnixMilli(trace.EndTime).UTC().Format("2006-01-02T15:04:05Z07:00")
	}

	for _, step := range trace.Trace {
		respStep := ReasoningStep{
			Step:         step.Step,
			Action:       step.Action,
			Target:       step.Target,
			Tool:         step.Tool,
			DurationMs:   step.Duration.Milliseconds(),
			SymbolsFound: step.SymbolsFound,
			Error:        step.Error,
			Metadata:     step.Metadata,
		}

		if step.Timestamp != 0 {
			respStep.Timestamp = time.UnixMilli(step.Timestamp).UTC().Format("2006-01-02T15:04:05Z07:00")
		}

		for _, update := range step.ProofUpdates {
			respStep.ProofUpdates = append(respStep.ProofUpdates, ProofUpdateResponse{
				NodeID: update.NodeID,
				Status: update.Status, // Status string kept for backwards compat
				Reason: update.Reason,
				Source: update.Source.String(), // Convert SignalSource to string
			})
		}

		response.Trace = append(response.Trace, respStep)
	}

	if summary != nil {
		response.Summary = &ReasoningSummaryResponse{
			NodesExplored:      summary.NodesExplored,
			NodesProven:        summary.NodesProven,
			NodesDisproven:     summary.NodesDisproven,
			NodesUnknown:       summary.NodesUnknown,
			ConstraintsApplied: summary.ConstraintsApplied,
			ExplorationDepth:   summary.ExplorationDepth,
			ConfidenceScore:    summary.ConfidenceScore,
		}
	}

	return response
}

// convertCRSExport converts crs.CRSExport to CRSExportResponse.
//
// Inputs:
//
//	export - The CRS export.
//
// Outputs:
//
//	*CRSExportResponse - The API response.
func convertCRSExport(export *crs.CRSExport) *CRSExportResponse {
	if export == nil {
		return nil
	}

	response := &CRSExportResponse{
		SessionID:  export.SessionID,
		Generation: export.Generation,
		Indexes:    CRSIndexesResponse{},
		Summary: ReasoningSummaryResponse{
			NodesExplored:      export.Summary.NodesExplored,
			NodesProven:        export.Summary.NodesProven,
			NodesDisproven:     export.Summary.NodesDisproven,
			NodesUnknown:       export.Summary.NodesUnknown,
			ConstraintsApplied: export.Summary.ConstraintsApplied,
			ExplorationDepth:   export.Summary.ExplorationDepth,
			ConfidenceScore:    export.Summary.ConfidenceScore,
		},
	}

	if export.Timestamp != 0 {
		response.Timestamp = time.UnixMilli(export.Timestamp).UTC().Format("2006-01-02T15:04:05Z07:00")
	}

	// Convert proof index entries
	for _, entry := range export.Indexes.Proof.Entries {
		response.Indexes.Proof = append(response.Indexes.Proof, ProofEntryResponse{
			NodeID: entry.NodeID,
			Status: entry.Status,
			// Evidence is derived from proof/disproof numbers
			Evidence: nil,
		})
	}

	// Convert constraint index entries
	for _, entry := range export.Indexes.Constraint.Constraints {
		response.Indexes.Constraints = append(response.Indexes.Constraints, ConstraintEntryResponse{
			ID:       entry.ID,
			Type:     entry.Type,
			Nodes:    entry.Nodes,
			Strength: 1.0, // Not stored in source, default to 1.0
		})
	}

	// Set aggregate counts for performance-deferred indexes
	response.Indexes.SimilarityCount = export.Indexes.Similarity.PairCount
	response.Indexes.DependencyCount = export.Indexes.Dependency.EdgeCount
	response.Indexes.StreamingCardinality = export.Indexes.Streaming.Cardinality
	response.Indexes.StreamingBytes = export.Indexes.Streaming.ApproximateBytes

	// Convert history index recent entries
	for _, entry := range export.Indexes.History.RecentEntries {
		histEntry := HistoryEntryResponse{
			NodeID:     entry.NodeID,
			VisitCount: 1, // Each entry represents one visit
		}
		if entry.Timestamp != 0 {
			histEntry.LastVisitedAt = time.UnixMilli(entry.Timestamp).UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		response.Indexes.History = append(response.Indexes.History, histEntry)
	}

	return response
}
