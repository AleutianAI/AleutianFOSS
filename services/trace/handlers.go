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

	cbcontext "github.com/AleutianAI/AleutianFOSS/services/trace/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/memory"
	"github.com/AleutianAI/AleutianFOSS/services/trace/seeder"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// ServiceVersion is the Trace service version.
const ServiceVersion = "0.1.0"

// Handlers contains the HTTP handlers for Trace.
type Handlers struct {
	svc              *Service
	seeder           *seeder.Seeder
	weaviate         *weaviate.Client
	memoryStore      *memory.MemoryStore
	memoryRetriever  *memory.MemoryRetriever
	lifecycleManager *memory.LifecycleManager
	dataSpace        string
}

// NewHandlers creates handlers for the given service.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

// WithWeaviate sets the Weaviate client for library seeding and memory.
func (h *Handlers) WithWeaviate(client *weaviate.Client) *Handlers {
	h.weaviate = client
	if client != nil {
		s, err := seeder.NewSeeder(client, seeder.DefaultSeederConfig())
		if err != nil {
			slog.Error("Failed to create seeder", "error", err)
		} else {
			h.seeder = s
		}
	}
	return h
}

// WithMemory sets the data space and initializes memory components.
//
// Description:
//
//	Configures the handlers for memory operations. Requires Weaviate
//	to be configured first via WithWeaviate.
//
// Inputs:
//
//	dataSpace - Project isolation key for memory operations
//
// Outputs:
//
//	*Handlers - The handlers for method chaining
func (h *Handlers) WithMemory(dataSpace string) *Handlers {
	h.dataSpace = dataSpace
	if h.weaviate != nil {
		store, err := memory.NewMemoryStore(h.weaviate, dataSpace)
		if err != nil {
			slog.Error("Failed to create memory store", "error", err)
			return h
		}
		h.memoryStore = store

		retriever, err := memory.NewMemoryRetriever(h.weaviate, h.memoryStore, dataSpace)
		if err != nil {
			slog.Error("Failed to create memory retriever", "error", err)
			return h
		}
		h.memoryRetriever = retriever

		lifecycle, err := memory.NewLifecycleManager(h.weaviate, h.memoryStore, dataSpace)
		if err != nil {
			slog.Error("Failed to create lifecycle manager", "error", err)
			return h
		}
		h.lifecycleManager = lifecycle
	}
	return h
}

// HandleInit handles POST /v1/trace/init.
//
// Description:
//
//	Initializes a code graph for a project. Parses the project files,
//	builds the code graph and symbol index, and caches the result.
//
// Request Body:
//
//	InitRequest
//
// Response:
//
//	200 OK: InitResponse
//	400 Bad Request: Validation error
//	500 Internal Server Error: Processing error
func (h *Handlers) HandleInit(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleInit")

	var req InitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Initializing graph", "project_root", req.ProjectRoot)

	resp, err := h.svc.Init(c.Request.Context(), req.ProjectRoot, req.Languages, req.ExcludePatterns)
	if err != nil {
		statusCode := http.StatusInternalServerError
		errCode := "INIT_FAILED"

		if errors.Is(err, ErrRelativePath) {
			statusCode = http.StatusBadRequest
			errCode = "INVALID_PATH"
		} else if errors.Is(err, ErrPathTraversal) {
			statusCode = http.StatusBadRequest
			errCode = "PATH_TRAVERSAL"
		} else if errors.Is(err, ErrProjectTooLarge) {
			statusCode = http.StatusBadRequest
			errCode = "PROJECT_TOO_LARGE"
		} else if errors.Is(err, ErrInitInProgress) {
			statusCode = http.StatusConflict
			errCode = "INIT_IN_PROGRESS"
		} else if errors.Is(err, ErrInitTimeout) {
			statusCode = http.StatusGatewayTimeout
			errCode = "INIT_TIMEOUT"
		}

		logger.Error("Init failed", "error", err)
		c.JSON(statusCode, ErrorResponse{
			Error: err.Error(),
			Code:  errCode,
		})
		return
	}

	logger.Info("Graph initialized",
		"graph_id", resp.GraphID,
		"files_parsed", resp.FilesParsed,
		"symbols_extracted", resp.SymbolsExtracted,
		"parse_time_ms", resp.ParseTimeMs)

	c.JSON(http.StatusOK, resp)
}

// HandleContext handles POST /v1/trace/context.
//
// Description:
//
//	Assembles relevant context for an LLM prompt using the code graph.
//
// Request Body:
//
//	ContextRequest
//
// Response:
//
//	200 OK: ContextResponse
//	400 Bad Request: Validation error or graph not initialized
//	500 Internal Server Error: Processing error
func (h *Handlers) HandleContext(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleContext")

	var req ContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	// Apply defaults
	budget := req.TokenBudget
	if budget <= 0 {
		budget = cbcontext.DefaultTokenBudget
	}

	logger.Info("Assembling context",
		"graph_id", req.GraphID,
		"query_len", len(req.Query),
		"budget", budget)

	resp, err := h.svc.GetContext(c.Request.Context(), req.GraphID, req.Query, budget)
	if err != nil {
		statusCode := http.StatusInternalServerError
		errCode := "CONTEXT_FAILED"

		if errors.Is(err, ErrGraphNotInitialized) {
			statusCode = http.StatusBadRequest
			errCode = "GRAPH_NOT_INITIALIZED"
		} else if errors.Is(err, ErrGraphExpired) {
			statusCode = http.StatusBadRequest
			errCode = "GRAPH_EXPIRED"
		} else if errors.Is(err, cbcontext.ErrEmptyQuery) {
			statusCode = http.StatusBadRequest
			errCode = "EMPTY_QUERY"
		} else if errors.Is(err, cbcontext.ErrQueryTooLong) {
			statusCode = http.StatusBadRequest
			errCode = "QUERY_TOO_LONG"
		}

		logger.Error("Context assembly failed", "error", err)
		c.JSON(statusCode, ErrorResponse{
			Error: err.Error(),
			Code:  errCode,
		})
		return
	}

	logger.Info("Context assembled",
		"tokens_used", resp.TokensUsed,
		"symbols_included", len(resp.SymbolsIncluded))

	c.JSON(http.StatusOK, resp)
}

// HandleSymbol handles GET /v1/trace/symbol/:id.
//
// Description:
//
//	Retrieves detailed information about a symbol by its ID.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//
// Path Parameters:
//
//	id: Symbol ID (required)
//
// Response:
//
//	200 OK: SymbolResponse
//	400 Bad Request: Graph not initialized
//	404 Not Found: Symbol not found
func (h *Handlers) HandleSymbol(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSymbol")

	graphID := c.Query("graph_id")
	if graphID == "" {
		logger.Warn("Missing graph_id parameter")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "graph_id parameter is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	symbolID := c.Param("id")
	if symbolID == "" {
		logger.Warn("Missing symbol id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "symbol id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	logger.Info("Getting symbol", "graph_id", graphID, "symbol_id", symbolID)

	sym, err := h.svc.GetSymbol(c.Request.Context(), graphID, symbolID)
	if err != nil {
		if errors.Is(err, ErrGraphNotInitialized) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "GRAPH_NOT_INITIALIZED",
			})
			return
		}

		logger.Warn("Symbol not found", "error", err)
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: err.Error(),
			Code:  "SYMBOL_NOT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, SymbolResponse{Symbol: sym})
}

// HandleCallers handles GET /v1/trace/callers.
//
// Description:
//
//	Finds all functions that call the given function.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//	function: Name of the function to find callers for (required)
//	limit: Maximum number of results (optional, default 50)
//
// Response:
//
//	200 OK: CallersResponse (may be empty array)
//	400 Bad Request: Missing parameters or graph not initialized
func (h *Handlers) HandleCallers(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleCallers")

	var req CallersRequest
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

	logger.Info("Finding callers", "graph_id", req.GraphID, "function", req.Function)

	callers, err := h.svc.FindCallers(c.Request.Context(), req.GraphID, req.Function, req.Limit)
	if err != nil {
		if errors.Is(err, ErrGraphNotInitialized) || errors.Is(err, ErrGraphExpired) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "GRAPH_NOT_INITIALIZED",
			})
			return
		}

		logger.Error("Find callers failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "QUERY_FAILED",
		})
		return
	}

	logger.Info("Found callers", "count", len(callers))

	c.JSON(http.StatusOK, CallersResponse{
		Function: req.Function,
		Callers:  callers,
	})
}

// HandleImplementations handles GET /v1/trace/implementations.
//
// Description:
//
//	Finds all types that implement the given interface.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (required)
//	interface: Name of the interface to find implementations for (required)
//	limit: Maximum number of results (optional, default 50)
//
// Response:
//
//	200 OK: ImplementationsResponse (may be empty array)
//	400 Bad Request: Missing parameters or graph not initialized
func (h *Handlers) HandleImplementations(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleImplementations")

	var req ImplementationsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("Invalid query parameters", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid query parameters: graph_id and interface are required",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 50
	}

	logger.Info("Finding implementations", "graph_id", req.GraphID, "interface", req.Interface)

	implementations, err := h.svc.FindImplementations(c.Request.Context(), req.GraphID, req.Interface, req.Limit)
	if err != nil {
		if errors.Is(err, ErrGraphNotInitialized) || errors.Is(err, ErrGraphExpired) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "GRAPH_NOT_INITIALIZED",
			})
			return
		}

		logger.Error("Find implementations failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "QUERY_FAILED",
		})
		return
	}

	logger.Info("Found implementations", "count", len(implementations))

	c.JSON(http.StatusOK, ImplementationsResponse{
		Interface:       req.Interface,
		Implementations: implementations,
	})
}

// HandleHealth handles GET /v1/trace/health.
//
// Description:
//
//	Returns the health status of the service. Always returns 200 if running.
//
// Response:
//
//	200 OK: HealthResponse
func (h *Handlers) HandleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{
		Status:  "healthy",
		Version: ServiceVersion,
	})
}

// HandleReady handles GET /v1/trace/ready.
//
// Description:
//
//	Returns the readiness status of the service including dependency checks.
//	Returns 503 Service Unavailable if model warmup has not completed.
//
// Response:
//
//	200 OK: ReadyResponse (Ready=true) - Service is fully ready
//	503 Service Unavailable: ReadyResponse (Ready=false) - Warmup in progress
func (h *Handlers) HandleReady(c *gin.Context) {
	// Check warmup status - return 503 if still warming up
	warmupComplete := IsWarmupComplete()

	resp := ReadyResponse{
		Ready:      warmupComplete,
		GraphCount: h.svc.GraphCount(),
		WeaviateOK: h.weaviate != nil,
	}

	if !warmupComplete {
		c.Header("Retry-After", "30")
		c.JSON(http.StatusServiceUnavailable, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleGetGraphStats handles GET /v1/trace/debug/graph/stats.
//
// Description:
//
//	Returns statistics about a cached graph including node/edge counts
//	broken down by type and kind. Used for debugging and integration tests.
//	GR-43: Added to validate EdgeTypeImplements edges are being created.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (optional, uses first cached if not specified)
//	project_root: Project root to look up graph (alternative to graph_id)
//
// Response:
//
//	200 OK: GraphStatsResponse
//	404 Not Found: No graphs cached or graph not found
//
// Thread Safety: This method is safe for concurrent use. Read-only access to graph.
func (h *Handlers) HandleGetGraphStats(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetGraphStats")

	graphID := c.Query("graph_id")

	// If no graph_id, try to get from project_root
	if graphID == "" {
		projectRoot := c.Query("project_root")
		if projectRoot != "" {
			// Generate graph ID from project root (same algorithm as service.go)
			graphID = h.svc.generateGraphID(projectRoot)
		}
	}

	var cached *CachedGraph
	var err error

	if graphID != "" {
		// Try to get specific graph
		cached, err = h.svc.GetGraph(graphID)
		if err != nil {
			logger.Warn("Graph not found", "graph_id", graphID, "error", err)
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "graph not found",
				Code:  "GRAPH_NOT_FOUND",
			})
			return
		}
	} else {
		// Get the first cached graph (for convenience)
		cached = h.svc.getFirstGraph()
		if cached == nil {
			logger.Info("No graphs cached")
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "no graphs cached",
				Code:  "NO_GRAPHS",
			})
			return
		}
		graphID = h.svc.generateGraphID(cached.ProjectRoot)
	}

	// Get stats from the graph
	stats := cached.Graph.Stats()

	// Convert EdgesByType map to string keys for JSON
	edgesByType := make(map[string]int)
	for edgeType, count := range stats.EdgesByType {
		edgesByType[edgeType.String()] = count
	}

	// Convert NodesByKind map to string keys for JSON
	nodesByKind := make(map[string]int)
	for kind, count := range stats.NodesByKind {
		nodesByKind[kind.String()] = count
	}

	logger.Info("Returning graph stats",
		"graph_id", graphID,
		"node_count", stats.NodeCount,
		"edge_count", stats.EdgeCount,
		"implements_edges", edgesByType["implements"])

	c.JSON(http.StatusOK, GraphStatsResponse{
		GraphID:      graphID,
		ProjectRoot:  cached.ProjectRoot,
		State:        stats.State.String(),
		NodeCount:    stats.NodeCount,
		EdgeCount:    stats.EdgeCount,
		MaxNodes:     stats.MaxNodes,
		MaxEdges:     stats.MaxEdges,
		BuiltAtMilli: stats.BuiltAtMilli,
		EdgesByType:  edgesByType,
		NodesByKind:  nodesByKind,
	})
}

// HandleGetCacheStats handles GET /v1/trace/debug/cache.
//
// Description:
//
//	Returns query cache statistics from the CRSGraphAdapter.
//	GR-10: Added to expose LRU cache hit/miss stats for monitoring.
//
// Query Parameters:
//
//	graph_id: ID of the graph to query (optional, uses first cached if not specified)
//
// Response:
//
//	200 OK: QueryCacheStats (callers/callees/paths cache stats)
//	404 Not Found: No graphs cached or graph not found
//	503 Service Unavailable: Graph has no adapter (cache not available)
//
// Thread Safety: This method is safe for concurrent use. Read-only access.
func (h *Handlers) HandleGetCacheStats(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleGetCacheStats")

	graphID := c.Query("graph_id")

	var cached *CachedGraph
	var err error

	if graphID != "" {
		cached, err = h.svc.GetGraph(graphID)
		if err != nil {
			logger.Warn("Graph not found", "graph_id", graphID, "error", err)
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "graph not found",
				Code:  "GRAPH_NOT_FOUND",
			})
			return
		}
	} else {
		cached = h.svc.getFirstGraph()
		if cached == nil {
			logger.Info("No graphs cached")
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "no graphs cached",
				Code:  "NO_GRAPHS",
			})
			return
		}
	}

	if cached.Adapter == nil {
		logger.Warn("Graph has no adapter for cache stats")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "cache not available for this graph",
			Code:  "CACHE_NOT_AVAILABLE",
		})
		return
	}

	stats := cached.Adapter.QueryCacheStats()

	logger.Info("Returning cache stats",
		slog.Int64("total_hits", stats.TotalHits),
		slog.Int64("total_misses", stats.TotalMisses),
		slog.Float64("hit_rate", stats.HitRate),
	)

	c.JSON(http.StatusOK, stats)
}

// HandleSeed handles POST /v1/trace/seed.
//
// Description:
//
//	Seeds library documentation from project dependencies into Weaviate.
//	Parses go.mod, locates cached dependencies, extracts documentation,
//	and indexes into Weaviate for context assembly.
//
// Request Body:
//
//	SeedRequest
//
// Response:
//
//	200 OK: SeedResponse
//	400 Bad Request: Validation error
//	503 Service Unavailable: Weaviate not configured
func (h *Handlers) HandleSeed(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleSeed")

	if h.seeder == nil {
		logger.Warn("Seed requested but Weaviate not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Library seeding requires Weaviate",
			Code:  "WEAVIATE_NOT_CONFIGURED",
		})
		return
	}

	var req SeedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	logger.Info("Starting library seeding",
		"project_root", req.ProjectRoot,
		"data_space", req.DataSpace)

	result, err := h.seeder.Seed(c.Request.Context(), req.ProjectRoot, req.DataSpace)
	if err != nil {
		logger.Error("Seeding failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "SEED_FAILED",
		})
		return
	}

	logger.Info("Seeding complete",
		"dependencies_found", result.DependenciesFound,
		"docs_indexed", result.DocsIndexed)

	c.JSON(http.StatusOK, SeedResponse{
		DependenciesFound: result.DependenciesFound,
		DocsIndexed:       result.DocsIndexed,
		Errors:            result.Errors,
	})
}

// getOrCreateRequestID gets or creates a request ID.
func getOrCreateRequestID(c *gin.Context) string {
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Header("X-Request-ID", requestID)
	return requestID
}

// HandleListMemories handles GET /v1/trace/memories.
//
// Description:
//
//	Lists memories for the configured data space with optional filtering.
//
// Query Parameters:
//
//	limit: Maximum number of results (optional, default 10)
//	offset: Number of results to skip for pagination (optional)
//	memory_type: Filter by memory type (optional)
//	include_archived: Include archived memories (optional, default false)
//	min_confidence: Minimum confidence threshold (optional)
//
// Response:
//
//	200 OK: MemoriesResponse
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleListMemories(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleListMemories")

	if h.memoryStore == nil {
		logger.Warn("Memory list requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	var req memory.ListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("Invalid query parameters", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid query parameters",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	memories, err := h.memoryStore.List(
		c.Request.Context(),
		req.Limit,
		req.Offset,
		req.MemoryType,
		req.IncludeArchived,
		req.MinConfidence,
	)
	if err != nil {
		logger.Error("List memories failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "LIST_FAILED",
		})
		return
	}

	logger.Info("Listed memories", "count", len(memories))

	c.JSON(http.StatusOK, memory.MemoriesResponse{
		Memories: memories,
		Total:    len(memories),
	})
}

// HandleStoreMemory handles POST /v1/trace/memories.
//
// Description:
//
//	Stores a new memory in Weaviate.
//
// Request Body:
//
//	StoreRequest
//
// Response:
//
//	201 Created: MemoryResponse
//	400 Bad Request: Validation error
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleStoreMemory(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleStoreMemory")

	if h.memoryStore == nil {
		logger.Warn("Memory store requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	var req memory.StoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	// Set default source if not provided
	source := memory.MemorySource(req.Source)
	if source == "" {
		source = memory.SourceManual
	}

	// Set default confidence if not provided
	confidence := req.Confidence
	if confidence == 0 {
		confidence = 0.5
	}

	mem := memory.CodeMemory{
		Content:    req.Content,
		MemoryType: req.MemoryType,
		Scope:      req.Scope,
		Confidence: confidence,
		Source:     source,
	}

	stored, err := h.memoryStore.Store(c.Request.Context(), mem)
	if err != nil {
		if errors.Is(err, memory.ErrEmptyContent) ||
			errors.Is(err, memory.ErrEmptyScope) ||
			errors.Is(err, memory.ErrInvalidMemoryType) ||
			errors.Is(err, memory.ErrInvalidMemorySource) ||
			errors.Is(err, memory.ErrInvalidConfidence) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
				Code:  "VALIDATION_FAILED",
			})
			return
		}

		logger.Error("Store memory failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "STORE_FAILED",
		})
		return
	}

	logger.Info("Stored memory",
		"memory_id", stored.MemoryID,
		"type", stored.MemoryType,
		"scope", stored.Scope)

	c.JSON(http.StatusCreated, memory.MemoryResponse{
		Memory: *stored,
	})
}

// HandleRetrieveMemories handles POST /v1/trace/memories/retrieve.
//
// Description:
//
//	Performs semantic retrieval of memories relevant to a query.
//
// Request Body:
//
//	RetrieveRequest
//
// Response:
//
//	200 OK: RetrieveResponse
//	400 Bad Request: Validation error
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleRetrieveMemories(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleRetrieveMemories")

	if h.memoryRetriever == nil {
		logger.Warn("Memory retrieve requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	var req memory.RetrieveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("Invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid request body",
			Code:  "INVALID_REQUEST",
		})
		return
	}

	opts := memory.RetrieveOptions{
		Query:           req.Query,
		Scope:           req.Scope,
		Limit:           req.Limit,
		IncludeArchived: req.IncludeArchived,
		MinConfidence:   req.MinConfidence,
	}

	results, err := h.memoryRetriever.Retrieve(c.Request.Context(), opts)
	if err != nil {
		logger.Error("Retrieve memories failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "RETRIEVE_FAILED",
		})
		return
	}

	logger.Info("Retrieved memories",
		"query", req.Query,
		"count", len(results))

	c.JSON(http.StatusOK, memory.RetrieveResponse{
		Results: results,
	})
}

// HandleDeleteMemory handles DELETE /v1/trace/memories/:id.
//
// Description:
//
//	Permanently deletes a memory by its ID.
//
// Path Parameters:
//
//	id: Memory ID (required)
//
// Response:
//
//	204 No Content: Successfully deleted
//	404 Not Found: Memory not found
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleDeleteMemory(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleDeleteMemory")

	if h.memoryStore == nil {
		logger.Warn("Memory delete requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	memoryID := c.Param("id")
	if memoryID == "" {
		logger.Warn("Missing memory id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "memory id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	err := h.memoryStore.Delete(c.Request.Context(), memoryID)
	if err != nil {
		if errors.Is(err, memory.ErrMemoryNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "MEMORY_NOT_FOUND",
			})
			return
		}

		logger.Error("Delete memory failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "DELETE_FAILED",
		})
		return
	}

	logger.Info("Deleted memory", "memory_id", memoryID)

	c.Status(http.StatusNoContent)
}

// HandleValidateMemory handles POST /v1/trace/memories/:id/validate.
//
// Description:
//
//	Validates a memory, boosting its confidence score.
//
// Path Parameters:
//
//	id: Memory ID (required)
//
// Response:
//
//	200 OK: MemoryResponse
//	404 Not Found: Memory not found
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleValidateMemory(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleValidateMemory")

	if h.lifecycleManager == nil {
		logger.Warn("Memory validate requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	memoryID := c.Param("id")
	if memoryID == "" {
		logger.Warn("Missing memory id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "memory id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	err := h.lifecycleManager.ValidateMemory(c.Request.Context(), memoryID)
	if err != nil {
		if errors.Is(err, memory.ErrMemoryNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "MEMORY_NOT_FOUND",
			})
			return
		}

		logger.Error("Validate memory failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "VALIDATE_FAILED",
		})
		return
	}

	// Fetch updated memory
	mem, err := h.memoryStore.Get(c.Request.Context(), memoryID)
	if err != nil {
		logger.Error("Get memory after validate failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "GET_FAILED",
		})
		return
	}

	logger.Info("Validated memory", "memory_id", memoryID, "confidence", mem.Confidence)

	c.JSON(http.StatusOK, memory.MemoryResponse{
		Memory: *mem,
	})
}

// HandleContradictMemory handles POST /v1/trace/memories/:id/contradict.
//
// Description:
//
//	Marks a memory as contradicted, reducing its confidence or deleting it.
//
// Path Parameters:
//
//	id: Memory ID (required)
//
// Request Body:
//
//	{ "reason": "Why this memory is contradicted" }
//
// Response:
//
//	200 OK: Success message
//	404 Not Found: Memory not found
//	503 Service Unavailable: Memory system not configured
func (h *Handlers) HandleContradictMemory(c *gin.Context) {
	requestID := getOrCreateRequestID(c)
	logger := slog.With("request_id", requestID, "handler", "HandleContradictMemory")

	if h.lifecycleManager == nil {
		logger.Warn("Memory contradict requested but memory system not configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: "Memory system requires Weaviate and data space configuration",
			Code:  "MEMORY_NOT_CONFIGURED",
		})
		return
	}

	memoryID := c.Param("id")
	if memoryID == "" {
		logger.Warn("Missing memory id")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "memory id is required",
			Code:  "MISSING_PARAMETER",
		})
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// Reason is optional
		req.Reason = "no reason provided"
	}

	err := h.lifecycleManager.ContradictMemory(c.Request.Context(), memoryID, req.Reason)
	if err != nil {
		if errors.Is(err, memory.ErrMemoryNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: err.Error(),
				Code:  "MEMORY_NOT_FOUND",
			})
			return
		}

		logger.Error("Contradict memory failed", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: err.Error(),
			Code:  "CONTRADICT_FAILED",
		})
		return
	}

	logger.Info("Contradicted memory", "memory_id", memoryID, "reason", req.Reason)

	c.JSON(http.StatusOK, gin.H{
		"message": "Memory contradicted successfully",
	})
}
