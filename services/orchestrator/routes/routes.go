// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routes

import (
	"net/http"

	"github.com/AleutianAI/AleutianFOSS/pkg/extensions"
	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/handlers"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/middleware"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/services"
	"github.com/AleutianAI/AleutianFOSS/services/policy_engine"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// SetupRoutes configures all HTTP routes for the orchestrator service.
//
// # Description
//
// Registers all API endpoints with the Gin router. Some endpoints require
// a Weaviate client for vector database operations; these are only registered
// when client is not nil.
//
// # Inputs
//
//   - router: Gin engine instance
//   - client: Weaviate client (may be nil if vector DB not available)
//   - globalLLMClient: LLM client for chat operations
//   - policyEngine: Policy engine for sensitive data scanning
//   - opts: Extension options for auth, audit, and filtering (Enterprise features)
//
// # Endpoints
//
//   - GET /health: Health check
//   - POST /v1/chat/direct: Direct LLM chat (always available)
//   - POST /v1/chat/rag: Conversational RAG (requires Weaviate)
//   - And more (see route registration below)
//
// # Enterprise Extensions
//
// The opts parameter enables enterprise features:
//   - AuthProvider: Applied as middleware for authentication
//   - AuthzProvider: Used in handlers for authorization checks
//   - AuditLogger: Used in handlers for compliance logging
//   - MessageFilter: Used in handlers for PII detection/redaction
func SetupRoutes(router *gin.Engine, client *weaviate.Client, globalLLMClient llm.LLMClient,
	policyEngine *policy_engine.PolicyEngine, opts extensions.ServiceOptions) {

	router.GET("/health", handlers.HealthCheck)
	router.GET("/metrics", gin.WrapH(promhttp.Handler())) // Prometheus metrics
	router.StaticFS("/ui", http.Dir("/app/ui"))

	// Add a friendly redirect from /chat to the actual HTML file
	router.GET("/chat", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/ui/chat.html")
	})

	// Create ChatHandler with optional RAG service
	// RAG service is only available when vector DB (Weaviate) is configured
	var chatRAGService *services.ChatRAGService
	if client != nil {
		chatRAGService = services.NewChatRAGService(client, globalLLMClient, policyEngine)
	}
	chatHandler := handlers.NewChatHandler(globalLLMClient, policyEngine, chatRAGService, opts)

	// Create StreamingChatHandler for SSE streaming endpoints
	// Pass Weaviate client for session history loading on resume
	streamingHandler := handlers.NewStreamingChatHandler(globalLLMClient, policyEngine, chatRAGService, client, opts)

	// API version 1 group - protected by auth middleware
	// NopAuthProvider (default) allows all requests as "local-user"
	// Enterprise implementations validate tokens against identity providers
	v1 := router.Group("/v1")
	v1.Use(middleware.AuthMiddleware(opts.AuthProvider))
	{
		// Chat endpoints using the new ChatHandler interface
		v1.POST("/chat/direct", chatHandler.HandleDirectChat)
		v1.POST("/chat/direct/stream", streamingHandler.HandleDirectChatStream)

		v1.POST("/timeseries/forecast", handlers.HandleTimeSeriesForecast())
		v1.POST("/data/fetch", handlers.HandleDataFetch())
		v1.POST("/trading/signal", handlers.HandleTradingSignal())
		v1.POST("/models/pull", handlers.HandleModelPull())
		v1.POST("/agent/step", handlers.HandleAgentStep(policyEngine))
		v1.POST("/embed", handlers.HandleEmbed())

		// Vector DB-dependent routes (requires Weaviate client)
		if client != nil {
			v1.GET("/chat/ws", handlers.HandleChatWebSocket(client, globalLLMClient, policyEngine))
			v1.POST("/chat/rag", chatHandler.HandleChatRAG)                                // Conversational RAG (default for CLI)
			v1.POST("/chat/rag/stream", streamingHandler.HandleChatRAGStream)              // Streaming RAG
			v1.POST("/chat/rag/verified/stream", streamingHandler.HandleVerifiedRAGStream) // Verified pipeline streaming
			v1.POST("/documents", handlers.CreateDocument(client))
			v1.GET("/documents", handlers.ListDocuments(client))
			v1.GET("/document/versions", handlers.GetDocumentVersions(client)) // Document version history
			v1.DELETE("/document", handlers.DeleteBySource(client))
			v1.POST("/rag", handlers.HandleRAGRequest(client, globalLLMClient)) // Single-shot RAG (aleutian ask)

			// Session administration routes
			sessions := v1.Group("/sessions")
			{
				sessions.GET("", handlers.ListSessions(client))
				sessions.GET("/:sessionId", handlers.GetSession(client))
				sessions.GET("/:sessionId/history", handlers.GetSessionHistory(client))
				sessions.GET("/:sessionId/documents", handlers.GetSessionDocuments(client))
				sessions.DELETE("/:sessionId", handlers.DeleteSessions(client))
				sessions.POST("/:sessionId/verify", handlers.VerifySession(client))
			}

			// Weaviate administration routes
			weaviateAdmin := v1.Group("/weaviate")
			{
				weaviateAdmin.POST("/backups", handlers.HandleBackup(client))
				weaviateAdmin.GET("/summary", handlers.GetSummary(client))
				weaviateAdmin.DELETE("/data", handlers.DeleteAll(client))
			}

			// Dataspace routes
			dataspace := v1.Group("/dataspace")
			{
				dataspace.GET("/:name/stats", handlers.GetDataspaceStats(client))
			}
		}
	}
}
