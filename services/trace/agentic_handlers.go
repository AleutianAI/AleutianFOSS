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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/analysis"
	"github.com/AleutianAI/AleutianFOSS/services/trace/coordinate"
	"github.com/AleutianAI/AleutianFOSS/services/trace/explore"
	"github.com/AleutianAI/AleutianFOSS/services/trace/patterns"
	"github.com/AleutianAI/AleutianFOSS/services/trace/reason"
	"github.com/gin-gonic/gin"
)

// =============================================================================
// TOOL DISCOVERY
// =============================================================================

// HandleGetTools returns all available tool definitions for agent discovery.
func (h *Handlers) HandleGetTools(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetTools")
	logger.Info("Fetching tool definitions")

	registry := NewToolRegistry()
	c.JSON(http.StatusOK, ToolsResponse{Tools: registry.GetTools()})
}

// =============================================================================
// EXPLORATION HANDLERS
// =============================================================================

// HandleFindEntryPoints finds code entry points (main, handlers, commands, tests).
func (h *Handlers) HandleFindEntryPoints(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindEntryPoints")

	var req FindEntryPointsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	// Apply defaults
	if req.Limit <= 0 {
		req.Limit = 100
	}
	entryType := explore.EntryPointType(req.Type)
	if req.Type == "" {
		entryType = explore.EntryPointAll
	}

	finder := explore.NewEntryPointFinder(cached.Graph, cached.Index)
	result, err := finder.FindEntryPoints(c.Request.Context(), explore.EntryPointOptions{
		Type:         entryType,
		Package:      req.Package,
		Limit:        req.Limit,
		IncludeTests: req.IncludeTests,
	})
	if err != nil {
		logger.Error("Failed to find entry points", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find entry points",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found entry points", "count", len(result.EntryPoints))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleTraceDataFlow traces data flow from a source through the codebase.
func (h *Handlers) HandleTraceDataFlow(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleTraceDataFlow")

	var req TraceDataFlowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	// Apply defaults
	maxHops := req.MaxHops
	if maxHops <= 0 {
		maxHops = 5
	}

	tracer := explore.NewDataFlowTracer(cached.Graph, cached.Index)
	opts := []explore.ExploreOption{
		explore.WithMaxHops(maxHops),
		explore.WithIncludeCode(req.IncludeCode),
	}
	result, err := tracer.TraceDataFlow(c.Request.Context(), req.SourceID, opts...)
	if err != nil {
		logger.Error("Failed to trace data flow", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to trace data flow",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Traced data flow", "sources", len(result.Sources), "sinks", len(result.Sinks))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:      result,
		LatencyMs:   time.Since(start).Milliseconds(),
		Limitations: result.Limitations,
	})
}

// HandleTraceErrorFlow traces error propagation through the codebase.
func (h *Handlers) HandleTraceErrorFlow(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleTraceErrorFlow")

	var req TraceErrorFlowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	maxHops := req.MaxHops
	if maxHops <= 0 {
		maxHops = 5
	}

	tracer := explore.NewErrorFlowTracer(cached.Graph, cached.Index)
	result, err := tracer.TraceErrorFlow(c.Request.Context(), req.Scope, explore.WithMaxHops(maxHops))
	if err != nil {
		logger.Error("Failed to trace error flow", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to trace error flow",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Traced error flow", "origins", len(result.Origins), "escapes", len(result.Escapes))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindConfigUsage finds usages of configuration values.
func (h *Handlers) HandleFindConfigUsage(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindConfigUsage")

	var req FindConfigUsageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	finder := explore.NewConfigFinder(cached.Graph, cached.Index)
	result, err := finder.FindConfigUsage(c.Request.Context(), req.ConfigKey)
	if err != nil {
		logger.Error("Failed to find config usage", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find config usage",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found config usage", "key", req.ConfigKey, "uses", len(result.UsedIn))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindSimilarCode finds structurally similar code.
func (h *Handlers) HandleFindSimilarCode(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindSimilarCode")

	var req FindSimilarCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	// Apply defaults
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	engine := explore.NewSimilarityEngine(cached.Graph, cached.Index)

	// Build the similarity index before searching
	if err := engine.Build(c.Request.Context()); err != nil {
		logger.Error("Failed to build similarity index", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to build similarity index",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	result, err := engine.FindSimilarCode(c.Request.Context(), req.SymbolID, explore.WithMaxNodes(limit))
	if err != nil {
		logger.Error("Failed to find similar code", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find similar code",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found similar code", "matches", len(result.Results))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleBuildMinimalContext builds token-efficient context for a symbol.
func (h *Handlers) HandleBuildMinimalContext(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleBuildMinimalContext")

	var req BuildMinimalContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	tokenBudget := req.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = 4000
	}

	builder := explore.NewMinimalContextBuilder(cached.Graph, cached.Index)
	result, err := builder.BuildMinimalContext(c.Request.Context(), req.SymbolID, explore.WithTokenBudget(tokenBudget))
	if err != nil {
		logger.Error("Failed to build minimal context", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to build minimal context",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Built minimal context", "tokens", result.TotalTokens)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleSummarizeFile generates a structured summary of a file.
func (h *Handlers) HandleSummarizeFile(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSummarizeFile")

	var req SummarizeFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	summarizer := explore.NewFileSummarizer(cached.Graph, cached.Index)
	result, err := summarizer.SummarizeFile(c.Request.Context(), req.FilePath)
	if err != nil {
		logger.Error("Failed to summarize file", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to summarize file",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Summarized file", "file", req.FilePath)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleSummarizePackage generates a public API summary of a package.
func (h *Handlers) HandleSummarizePackage(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSummarizePackage")

	var req SummarizePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	summarizer := explore.NewPackageAPISummarizer(cached.Graph, cached.Index)
	result, err := summarizer.FindPackageAPI(c.Request.Context(), req.Package)
	if err != nil {
		logger.Error("Failed to summarize package", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to summarize package",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Summarized package", "package", req.Package)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleAnalyzeChangeImpact analyzes the blast radius of a change.
func (h *Handlers) HandleAnalyzeChangeImpact(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleAnalyzeChangeImpact")

	var req AnalyzeChangeImpactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	analyzer := analysis.NewBlastRadiusAnalyzer(cached.Graph, cached.Index, nil)
	result, err := analyzer.Analyze(c.Request.Context(), req.SymbolID, nil)
	if err != nil {
		logger.Error("Failed to analyze change impact", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to analyze change impact",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Analyzed change impact", "symbol", req.SymbolID)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// =============================================================================
// REASONING HANDLERS
// =============================================================================

// HandleCheckBreakingChanges checks if a proposed change would break callers.
func (h *Handlers) HandleCheckBreakingChanges(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleCheckBreakingChanges")

	var req CheckBreakingChangesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	analyzer := reason.NewBreakingChangeAnalyzer(cached.Graph, cached.Index)
	result, err := analyzer.AnalyzeBreaking(c.Request.Context(), req.SymbolID, req.ProposedSignature)
	if err != nil {
		logger.Error("Failed to check breaking changes", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to check breaking changes",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Checked breaking changes", "is_breaking", result.IsBreaking, "callers_affected", result.CallersAffected)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:      result,
		LatencyMs:   time.Since(start).Milliseconds(),
		Limitations: result.Limitations,
	})
}

// HandleSimulateChange simulates a change and identifies update locations.
func (h *Handlers) HandleSimulateChange(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSimulateChange")

	var req SimulateChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	// Extract new signature from change details
	newSignature := ""
	if sig, ok := req.ChangeDetails["new_signature"].(string); ok {
		newSignature = sig
	} else if req.ChangeType == "rename" {
		// For rename operations, construct signature by replacing the name
		if newName, ok := req.ChangeDetails["new_name"].(string); ok {
			symbol, found := cached.Index.GetByID(req.SymbolID)
			if found && symbol != nil && symbol.Signature != "" {
				// Replace the symbol name in the signature
				newSignature = strings.Replace(symbol.Signature, symbol.Name, newName, 1)
			} else {
				// If no signature found, just use the new name as the "signature"
				newSignature = newName
			}
		}
	}

	// Validate we have a new signature to simulate
	if newSignature == "" {
		logger.Warn("No new signature or valid change details provided", "symbol_id", req.SymbolID, "change_type", req.ChangeType)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "new_signature or valid change_details required",
			Code:    "INVALID_REQUEST",
			Details: "For rename operations, provide change_details.new_name. For other changes, provide change_details.new_signature",
		})
		return
	}

	simulator := reason.NewChangeSimulator(cached.Graph, cached.Index)
	result, err := simulator.SimulateChange(c.Request.Context(), req.SymbolID, newSignature)
	if err != nil {
		logger.Error("Failed to simulate change", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to simulate change",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Simulated change", "updates_needed", len(result.CallersToUpdate))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleValidateChange validates proposed code syntactically.
func (h *Handlers) HandleValidateChange(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleValidateChange")

	var req ValidateChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	validator := reason.NewChangeValidator(nil) // nil index for validation-only
	result, err := validator.ValidateChange(c.Request.Context(), req.Code, req.Language)
	if err != nil {
		logger.Error("Failed to validate change", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to validate change",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	// Compute overall validity from component checks
	isValid := result.SyntaxValid && result.TypesValid && result.ImportsValid

	logger.Info("Validated change", "valid", isValid, "syntax", result.SyntaxValid, "types", result.TypesValid, "imports", result.ImportsValid)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindTestCoverage finds tests that cover a symbol.
func (h *Handlers) HandleFindTestCoverage(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindTestCoverage")

	var req FindTestCoverageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	finder := reason.NewTestCoverageFinder(cached.Graph, cached.Index)
	result, err := finder.FindTestCoverage(c.Request.Context(), req.SymbolID)
	if err != nil {
		logger.Error("Failed to find test coverage", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find test coverage",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found test coverage", "direct", len(result.DirectTests), "indirect", len(result.IndirectTests))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleDetectSideEffects detects side effects of a function.
func (h *Handlers) HandleDetectSideEffects(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDetectSideEffects")

	var req DetectSideEffectsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	analyzer := reason.NewSideEffectAnalyzer(cached.Graph, cached.Index)
	result, err := analyzer.FindSideEffects(c.Request.Context(), req.SymbolID)
	if err != nil {
		logger.Error("Failed to detect side effects", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to detect side effects",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Detected side effects", "count", len(result.SideEffects))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleSuggestRefactor suggests refactoring improvements.
func (h *Handlers) HandleSuggestRefactor(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSuggestRefactor")

	var req SuggestRefactorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	suggester := reason.NewRefactorSuggester(cached.Graph, cached.Index)
	result, err := suggester.SuggestRefactor(c.Request.Context(), req.SymbolID)
	if err != nil {
		logger.Error("Failed to suggest refactoring", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to suggest refactoring",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Suggested refactoring", "suggestions", len(result.Suggestions))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// =============================================================================
// COORDINATION HANDLERS
// =============================================================================

// HandlePlanMultiFileChange generates a coordinated change plan.
func (h *Handlers) HandlePlanMultiFileChange(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandlePlanMultiFileChange")

	var req PlanMultiFileChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	breakingAnalyzer := reason.NewBreakingChangeAnalyzer(cached.Graph, cached.Index)
	blastAnalyzer := analysis.NewBlastRadiusAnalyzer(cached.Graph, cached.Index, nil)
	validator := reason.NewChangeValidator(cached.Index)

	coordinator := coordinate.NewMultiFileChangeCoordinator(
		cached.Graph, cached.Index,
		breakingAnalyzer, blastAnalyzer, validator,
	)

	changeSet := coordinate.ChangeSet{
		PrimaryChange: coordinate.ChangeRequest{
			TargetID:     req.TargetID,
			ChangeType:   coordinate.ChangeType(req.ChangeType),
			NewSignature: req.NewSignature,
			NewName:      req.NewName,
		},
		Description: req.Description,
	}

	opts := coordinate.DefaultPlanOptions()
	opts.IncludeTests = req.IncludeTests

	result, err := coordinator.PlanChanges(c.Request.Context(), changeSet, &opts)
	if err != nil {
		logger.Error("Failed to plan multi-file change", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to plan multi-file change",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	// Set the graph ID so the plan can be validated/previewed later
	result.GraphID = req.GraphID

	// Store plan for later validation/preview
	h.svc.StorePlan(result)

	logger.Info("Planned multi-file change", "files", result.TotalFiles, "changes", result.TotalChanges)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:      result,
		LatencyMs:   time.Since(start).Milliseconds(),
		Warnings:    result.Warnings,
		Limitations: result.Limitations,
	})
}

// HandleValidatePlan validates a change plan.
func (h *Handlers) HandleValidatePlan(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleValidatePlan")

	var req ValidatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	planData, err := h.svc.GetPlan(req.PlanID)
	if err != nil {
		logger.Warn("Plan not found", "plan_id", req.PlanID)
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "Plan not found",
			Code:  "PLAN_NOT_FOUND",
		})
		return
	}

	plan, ok := planData.(*coordinate.ChangePlan)
	if !ok {
		logger.Error("Invalid plan type")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Invalid plan data",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	cached, err := h.svc.GetGraphForPlan(planData)
	if err != nil {
		logger.Warn("Graph for plan not found", "plan_id", req.PlanID)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "The graph for this plan is no longer available",
		})
		return
	}

	breakingAnalyzer := reason.NewBreakingChangeAnalyzer(cached.Graph, cached.Index)
	blastAnalyzer := analysis.NewBlastRadiusAnalyzer(cached.Graph, cached.Index, nil)
	validator := reason.NewChangeValidator(cached.Index)

	coordinator := coordinate.NewMultiFileChangeCoordinator(
		cached.Graph, cached.Index,
		breakingAnalyzer, blastAnalyzer, validator,
	)

	result, err := coordinator.ValidatePlan(c.Request.Context(), plan)
	if err != nil {
		logger.Error("Failed to validate plan", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to validate plan",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Validated plan", "valid", result.Valid)
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
		Warnings:  result.Warnings,
	})
}

// HandlePreviewChanges generates unified diffs for a plan.
func (h *Handlers) HandlePreviewChanges(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandlePreviewChanges")

	var req PreviewChangesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	planData, err := h.svc.GetPlan(req.PlanID)
	if err != nil {
		logger.Warn("Plan not found", "plan_id", req.PlanID)
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "Plan not found",
			Code:  "PLAN_NOT_FOUND",
		})
		return
	}

	plan, ok := planData.(*coordinate.ChangePlan)
	if !ok {
		logger.Error("Invalid plan type")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Invalid plan data",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	cached, err := h.svc.GetGraphForPlan(planData)
	if err != nil {
		logger.Warn("Graph for plan not found", "plan_id", req.PlanID)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "The graph for this plan is no longer available",
		})
		return
	}

	breakingAnalyzer := reason.NewBreakingChangeAnalyzer(cached.Graph, cached.Index)
	blastAnalyzer := analysis.NewBlastRadiusAnalyzer(cached.Graph, cached.Index, nil)
	validator := reason.NewChangeValidator(cached.Index)

	coordinator := coordinate.NewMultiFileChangeCoordinator(
		cached.Graph, cached.Index,
		breakingAnalyzer, blastAnalyzer, validator,
	)

	result, err := coordinator.PreviewChanges(c.Request.Context(), plan)
	if err != nil {
		logger.Error("Failed to preview changes", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to preview changes",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Previewed changes", "files", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// =============================================================================
// PATTERN HANDLERS
// =============================================================================

// HandleDetectPatterns detects design patterns in the codebase.
func (h *Handlers) HandleDetectPatterns(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDetectPatterns")

	var req DetectPatternsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	minConfidence := req.MinConfidence
	if minConfidence <= 0 {
		minConfidence = 0.6
	}

	detector := patterns.NewPatternDetector(cached.Graph, cached.Index)

	// Convert string patterns to PatternType
	var patternTypes []patterns.PatternType
	for _, p := range req.Patterns {
		patternTypes = append(patternTypes, patterns.PatternType(p))
	}

	opts := &patterns.DetectionOptions{
		Patterns:            patternTypes,
		MinConfidence:       minConfidence,
		IncludeNonIdiomatic: true,
	}

	result, err := detector.DetectPatterns(c.Request.Context(), req.Scope, opts)
	if err != nil {
		logger.Error("Failed to detect patterns", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to detect patterns",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Detected patterns", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindCodeSmells finds code quality issues.
func (h *Handlers) HandleFindCodeSmells(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindCodeSmells")

	var req FindCodeSmellsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	minSeverity := patterns.Severity(req.MinSeverity)
	if req.MinSeverity == "" {
		minSeverity = patterns.SeverityWarning
	}

	finder := patterns.NewSmellFinder(cached.Graph, cached.Index, cached.ProjectRoot)
	opts := &patterns.SmellOptions{
		Thresholds:   patterns.DefaultSmellThresholds(),
		MinSeverity:  minSeverity,
		IncludeTests: req.IncludeTests,
	}

	result, err := finder.FindCodeSmells(c.Request.Context(), req.Scope, opts)
	if err != nil {
		logger.Error("Failed to find code smells", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find code smells",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found code smells", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindDuplication finds duplicate code.
func (h *Handlers) HandleFindDuplication(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindDuplication")

	var req FindDuplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	minSimilarity := req.MinSimilarity
	if minSimilarity <= 0 {
		minSimilarity = 0.8
	}

	finder := patterns.NewDuplicationFinder(cached.Graph, cached.Index, cached.ProjectRoot)

	opts := &patterns.DuplicationOptions{
		SimilarityThreshold: minSimilarity,
		IncludeTests:        req.IncludeTests,
	}

	if _, err := finder.BuildIndex(c.Request.Context(), opts); err != nil {
		logger.Error("Failed to build duplication index", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to build duplication index",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	result, err := finder.FindDuplication(c.Request.Context(), req.Scope, opts)
	if err != nil {
		logger.Error("Failed to find duplication", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find duplication",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found duplication", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindCircularDeps finds circular dependencies.
func (h *Handlers) HandleFindCircularDeps(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindCircularDeps")

	var req FindCircularDepsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	depType := patterns.CircularDepType(req.Level)
	if req.Level == "" {
		depType = patterns.CircularDepPackage
	}

	// Use empty module path - circular dep finder will use all packages
	finder := patterns.NewCircularDepFinder(cached.Graph, "")
	result, err := finder.FindCircularDeps(c.Request.Context(), req.Scope, depType)
	if err != nil {
		logger.Error("Failed to find circular dependencies", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find circular dependencies",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found circular dependencies", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleExtractConventions extracts coding conventions.
func (h *Handlers) HandleExtractConventions(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleExtractConventions")

	var req ExtractConventionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	extractor := patterns.NewConventionExtractor(cached.Index, cached.ProjectRoot)

	opts := &patterns.ConventionOptions{
		IncludeTests: req.IncludeTests,
	}

	result, err := extractor.ExtractConventions(c.Request.Context(), req.Scope, opts)
	if err != nil {
		logger.Error("Failed to extract conventions", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to extract conventions",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Extracted conventions", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// HandleFindDeadCode finds unreferenced code.
func (h *Handlers) HandleFindDeadCode(c *gin.Context) {
	start := time.Now()
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleFindDeadCode")

	var req FindDeadCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	cached, err := h.svc.GetGraph(req.GraphID)
	if err != nil {
		logger.Warn("Graph not found", "graph_id", req.GraphID, "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Code:    "GRAPH_NOT_FOUND",
			Details: "Ensure /init was called first",
		})
		return
	}

	finder := patterns.NewDeadCodeFinder(cached.Graph, cached.Index, cached.ProjectRoot)
	opts := &patterns.DeadCodeOptions{
		IncludeExported: req.IncludeExported,
	}

	result, err := finder.FindDeadCode(c.Request.Context(), req.Scope, opts)
	if err != nil {
		logger.Error("Failed to find dead code", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to find dead code",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	logger.Info("Found dead code", "count", len(result))
	c.JSON(http.StatusOK, AgenticResponse{
		Result:    result,
		LatencyMs: time.Since(start).Milliseconds(),
	})
}
