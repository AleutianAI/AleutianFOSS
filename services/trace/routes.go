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
	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers all Code Buddy routes with the router.
//
// Description:
//
//	Registers all /v1/codebuddy/* endpoints with the given Gin router group.
//	The router group should already have any required middleware applied.
//
// Inputs:
//
//	rg - Gin router group (typically /v1)
//	handlers - The handlers instance
//
// Core Endpoints:
//
//	POST /v1/codebuddy/init - Initialize a code graph
//	POST /v1/codebuddy/context - Assemble context for LLM prompt
//	GET  /v1/codebuddy/symbol/:id - Get symbol by ID
//	GET  /v1/codebuddy/callers - Find function callers
//	GET  /v1/codebuddy/implementations - Find interface implementations
//	POST /v1/codebuddy/seed - Seed library documentation
//
// Memory Endpoints:
//
//	GET  /v1/codebuddy/memories - List memories
//	POST /v1/codebuddy/memories - Store a new memory
//	POST /v1/codebuddy/memories/retrieve - Semantic memory retrieval
//	DELETE /v1/codebuddy/memories/:id - Delete a memory
//	POST /v1/codebuddy/memories/:id/validate - Validate a memory
//	POST /v1/codebuddy/memories/:id/contradict - Contradict a memory
//
// Agentic Tool Endpoints (24 tools):
//
//	GET  /v1/codebuddy/tools - Discover available tools
//
//	POST /v1/codebuddy/explore/entry_points - Find entry points
//	POST /v1/codebuddy/explore/data_flow - Trace data flow
//	POST /v1/codebuddy/explore/error_flow - Trace error flow
//	POST /v1/codebuddy/explore/config_usage - Find config usages
//	POST /v1/codebuddy/explore/similar_code - Find similar code
//	POST /v1/codebuddy/explore/minimal_context - Build minimal context
//	POST /v1/codebuddy/explore/summarize_file - Summarize a file
//	POST /v1/codebuddy/explore/summarize_package - Summarize a package
//	POST /v1/codebuddy/explore/change_impact - Analyze change impact
//
//	POST /v1/codebuddy/reason/breaking_changes - Check breaking changes
//	POST /v1/codebuddy/reason/simulate_change - Simulate a change
//	POST /v1/codebuddy/reason/validate_change - Validate code syntax
//	POST /v1/codebuddy/reason/test_coverage - Find test coverage
//	POST /v1/codebuddy/reason/side_effects - Detect side effects
//	POST /v1/codebuddy/reason/suggest_refactor - Suggest refactoring
//
//	POST /v1/codebuddy/coordinate/plan_changes - Plan multi-file changes
//	POST /v1/codebuddy/coordinate/validate_plan - Validate a change plan
//	POST /v1/codebuddy/coordinate/preview_changes - Preview changes as diffs
//
//	POST /v1/codebuddy/patterns/detect - Detect design patterns
//	POST /v1/codebuddy/patterns/code_smells - Find code smells
//	POST /v1/codebuddy/patterns/duplication - Find duplicate code
//	POST /v1/codebuddy/patterns/circular_deps - Find circular dependencies
//	POST /v1/codebuddy/patterns/conventions - Extract conventions
//	POST /v1/codebuddy/patterns/dead_code - Find dead code
//
// Health Endpoints:
//
//	GET  /v1/codebuddy/health - Health check
//	GET  /v1/codebuddy/ready - Readiness check
//
// Example:
//
//	service := trace.NewService(trace.DefaultServiceConfig())
//	handlers := trace.NewHandlers(service)
//
//	v1 := router.Group("/v1")
//	trace.RegisterRoutes(v1, handlers)
func RegisterRoutes(rg *gin.RouterGroup, handlers *Handlers) {
	codebuddy := rg.Group("/codebuddy")
	{
		// Graph lifecycle
		codebuddy.POST("/init", handlers.HandleInit)

		// Context assembly
		codebuddy.POST("/context", handlers.HandleContext)

		// Symbol queries
		codebuddy.GET("/symbol/:id", handlers.HandleSymbol)
		codebuddy.GET("/callers", handlers.HandleCallers)
		codebuddy.GET("/implementations", handlers.HandleImplementations)

		// Library documentation seeding
		codebuddy.POST("/seed", handlers.HandleSeed)

		// Memory management
		codebuddy.GET("/memories", handlers.HandleListMemories)
		codebuddy.POST("/memories", handlers.HandleStoreMemory)
		codebuddy.POST("/memories/retrieve", handlers.HandleRetrieveMemories)
		codebuddy.DELETE("/memories/:id", handlers.HandleDeleteMemory)
		codebuddy.POST("/memories/:id/validate", handlers.HandleValidateMemory)
		codebuddy.POST("/memories/:id/contradict", handlers.HandleContradictMemory)

		// Health checks
		codebuddy.GET("/health", handlers.HandleHealth)
		codebuddy.GET("/ready", handlers.HandleReady)

		// =================================================================
		// DEBUG ENDPOINTS (GR-43)
		// =================================================================

		debug := codebuddy.Group("/debug")
		{
			debug.GET("/graph/stats", handlers.HandleGetGraphStats)
			debug.GET("/cache", handlers.HandleGetCacheStats)
		}

		// =================================================================
		// AGENTIC TOOL ENDPOINTS (CB-22b)
		// =================================================================

		// Tool discovery
		codebuddy.GET("/tools", handlers.HandleGetTools)

		// Exploration tools (9 endpoints)
		explore := codebuddy.Group("/explore")
		{
			explore.POST("/entry_points", handlers.HandleFindEntryPoints)
			explore.POST("/data_flow", handlers.HandleTraceDataFlow)
			explore.POST("/error_flow", handlers.HandleTraceErrorFlow)
			explore.POST("/config_usage", handlers.HandleFindConfigUsage)
			explore.POST("/similar_code", handlers.HandleFindSimilarCode)
			explore.POST("/minimal_context", handlers.HandleBuildMinimalContext)
			explore.POST("/summarize_file", handlers.HandleSummarizeFile)
			explore.POST("/summarize_package", handlers.HandleSummarizePackage)
			explore.POST("/change_impact", handlers.HandleAnalyzeChangeImpact)
		}

		// Reasoning tools (6 endpoints)
		reason := codebuddy.Group("/reason")
		{
			reason.POST("/breaking_changes", handlers.HandleCheckBreakingChanges)
			reason.POST("/simulate_change", handlers.HandleSimulateChange)
			reason.POST("/validate_change", handlers.HandleValidateChange)
			reason.POST("/test_coverage", handlers.HandleFindTestCoverage)
			reason.POST("/side_effects", handlers.HandleDetectSideEffects)
			reason.POST("/suggest_refactor", handlers.HandleSuggestRefactor)
		}

		// Coordination tools (3 endpoints)
		coordinate := codebuddy.Group("/coordinate")
		{
			coordinate.POST("/plan_changes", handlers.HandlePlanMultiFileChange)
			coordinate.POST("/validate_plan", handlers.HandleValidatePlan)
			coordinate.POST("/preview_changes", handlers.HandlePreviewChanges)
		}

		// Pattern tools (6 endpoints)
		patterns := codebuddy.Group("/patterns")
		{
			patterns.POST("/detect", handlers.HandleDetectPatterns)
			patterns.POST("/code_smells", handlers.HandleFindCodeSmells)
			patterns.POST("/duplication", handlers.HandleFindDuplication)
			patterns.POST("/circular_deps", handlers.HandleFindCircularDeps)
			patterns.POST("/conventions", handlers.HandleExtractConventions)
			patterns.POST("/dead_code", handlers.HandleFindDeadCode)
		}
	}
}

// RegisterAgentRoutes registers the Code Buddy agent routes with the router.
//
// Description:
//
//	Registers all /v1/codebuddy/agent/* endpoints with the given Gin router group.
//	These endpoints provide the agent loop functionality for AI-driven code
//	assistance with multi-step reasoning, tool execution, and clarification.
//
// Inputs:
//
//	rg - Gin router group (typically /v1)
//	handlers - The agent handlers instance
//
// Endpoints:
//
//	POST /v1/codebuddy/agent/run - Start a new agent session
//	POST /v1/codebuddy/agent/continue - Continue from CLARIFY state
//	POST /v1/codebuddy/agent/abort - Abort an active session
//	GET  /v1/codebuddy/agent/:id - Get session state
//	GET  /v1/codebuddy/agent/:id/reasoning - Get reasoning trace
//	GET  /v1/codebuddy/agent/:id/crs - Get CRS state export
//
// Example:
//
//	loop := agent.NewDefaultAgentLoop()
//	service := trace.NewService(config)
//	agentHandlers := trace.NewAgentHandlers(loop, service)
//
//	v1 := router.Group("/v1")
//	trace.RegisterAgentRoutes(v1, agentHandlers)
func RegisterAgentRoutes(rg *gin.RouterGroup, handlers *AgentHandlers) {
	RegisterAgentRoutesWithMiddleware(rg, handlers, nil)
}

// RegisterAgentRoutesWithMiddleware registers agent routes with optional middleware.
//
// Description:
//
//	Same as RegisterAgentRoutes but allows applying middleware (e.g., warmup guard)
//	to all agent endpoints. If middleware is nil, no additional middleware is applied.
//
// Inputs:
//
//	rg - The router group to register routes under.
//	handlers - The agent handlers.
//	middleware - Optional middleware to apply to all agent routes. Can be nil.
//
// Thread Safety: This function is safe for concurrent use.
func RegisterAgentRoutesWithMiddleware(rg *gin.RouterGroup, handlers *AgentHandlers, middleware gin.HandlerFunc) {
	var agent *gin.RouterGroup
	if middleware != nil {
		agent = rg.Group("/codebuddy/agent", middleware)
	} else {
		agent = rg.Group("/codebuddy/agent")
	}
	{
		// Session lifecycle
		agent.POST("/run", handlers.HandleAgentRun)
		agent.POST("/continue", handlers.HandleAgentContinue)
		agent.POST("/abort", handlers.HandleAgentAbort)

		// Session state
		agent.GET("/:id", handlers.HandleAgentState)

		// CRS Export API (CB-29-2)
		agent.GET("/:id/reasoning", handlers.HandleGetReasoningTrace)
		agent.GET("/:id/crs", handlers.HandleGetCRSExport)

		// Debug endpoints (GR-Phase1 Issue 5, Issue 5b)
		debug := agent.Group("/debug")
		{
			debug.GET("/crs", handlers.HandleDebugCRS)
			debug.GET("/history", handlers.HandleDebugHistory)
		}
	}
}
