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
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	cbcontext "github.com/AleutianAI/AleutianFOSS/services/trace/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// InitRequest is the request body for POST /v1/trace/init.
type InitRequest struct {
	// ProjectRoot is the absolute path to the project root directory.
	// Required.
	ProjectRoot string `json:"project_root" binding:"required"`

	// Languages is the list of languages to parse. Default: ["go"].
	Languages []string `json:"languages"`

	// ExcludePatterns is a list of glob patterns to exclude. Default: ["vendor/*", "*_test.go"].
	ExcludePatterns []string `json:"exclude_patterns"`
}

// InitResponse is the response for POST /v1/trace/init.
type InitResponse struct {
	// GraphID is the unique identifier for this graph.
	GraphID string `json:"graph_id"`

	// IsRefresh indicates if this replaced an existing graph.
	IsRefresh bool `json:"is_refresh"`

	// PreviousID is the ID of the replaced graph (if IsRefresh is true).
	PreviousID string `json:"previous_id,omitempty"`

	// FilesParsed is the number of files successfully parsed.
	FilesParsed int `json:"files_parsed"`

	// SymbolsExtracted is the total number of symbols extracted.
	SymbolsExtracted int `json:"symbols_extracted"`

	// EdgesBuilt is the total number of edges created.
	EdgesBuilt int `json:"edges_built"`

	// ParseTimeMs is the total parse time in milliseconds.
	ParseTimeMs int64 `json:"parse_time_ms"`

	// Errors contains non-fatal errors encountered during parsing.
	Errors []string `json:"errors,omitempty"`
}

// ContextRequest is the request body for POST /v1/trace/context.
type ContextRequest struct {
	// GraphID is the graph to query. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// Query is the search query or task description. Required.
	Query string `json:"query" binding:"required"`

	// TokenBudget is the maximum tokens to use. Default: 8000.
	TokenBudget int `json:"token_budget"`

	// IncludeLibraryDocs enables library documentation lookup. Default: true.
	IncludeLibraryDocs *bool `json:"include_library_docs"`
}

// ContextResponse is the response for POST /v1/trace/context.
type ContextResponse struct {
	// Context is the formatted markdown context for LLM consumption.
	Context string `json:"context"`

	// TokensUsed is the estimated number of tokens used.
	TokensUsed int `json:"tokens_used"`

	// SymbolsIncluded lists the IDs of symbols included in context.
	SymbolsIncluded []string `json:"symbols_included"`

	// LibraryDocsIncluded lists the IDs of library docs included.
	LibraryDocsIncluded []string `json:"library_docs_included"`

	// Suggestions provides "also consider" hints.
	Suggestions []string `json:"suggestions"`
}

// CallersRequest is the query params for GET /v1/trace/callers.
type CallersRequest struct {
	// GraphID is the graph to query. Required.
	GraphID string `form:"graph_id" binding:"required"`

	// Function is the function name to find callers for. Required.
	Function string `form:"function" binding:"required"`

	// Limit is the maximum number of results. Default: 50.
	Limit int `form:"limit"`
}

// CallersResponse is the response for GET /v1/trace/callers.
type CallersResponse struct {
	// Function is the function name that was searched.
	Function string `json:"function"`

	// Callers is the list of symbols that call the function.
	Callers []*SymbolInfo `json:"callers"`
}

// ImplementationsRequest is the query params for GET /v1/trace/implementations.
type ImplementationsRequest struct {
	// GraphID is the graph to query. Required.
	GraphID string `form:"graph_id" binding:"required"`

	// Interface is the interface name to find implementations for. Required.
	Interface string `form:"interface" binding:"required"`

	// Limit is the maximum number of results. Default: 50.
	Limit int `form:"limit"`
}

// ImplementationsResponse is the response for GET /v1/trace/implementations.
type ImplementationsResponse struct {
	// Interface is the interface name that was searched.
	Interface string `json:"interface"`

	// Implementations is the list of types that implement the interface.
	Implementations []*SymbolInfo `json:"implementations"`
}

// SymbolRequest is the query params for GET /v1/trace/symbol/:id.
type SymbolRequest struct {
	// GraphID is the graph to query. Required.
	GraphID string `form:"graph_id" binding:"required"`
}

// SymbolResponse is the response for GET /v1/trace/symbol/:id.
type SymbolResponse struct {
	// Symbol is the detailed symbol information.
	Symbol *SymbolInfo `json:"symbol"`
}

// SymbolInfo is a simplified symbol representation for API responses.
type SymbolInfo struct {
	// ID is the unique symbol identifier.
	ID string `json:"id"`

	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind (function, struct, interface, etc.).
	Kind string `json:"kind"`

	// FilePath is the relative path to the file.
	FilePath string `json:"file_path"`

	// StartLine is the 1-indexed line where the symbol starts.
	StartLine int `json:"start_line"`

	// EndLine is the 1-indexed line where the symbol ends.
	EndLine int `json:"end_line"`

	// Signature is the type signature.
	Signature string `json:"signature,omitempty"`

	// DocComment is the documentation comment.
	DocComment string `json:"doc_comment,omitempty"`

	// Package is the package name.
	Package string `json:"package,omitempty"`

	// Exported indicates if the symbol is publicly visible.
	Exported bool `json:"exported"`
}

// SeedRequest is the request body for POST /v1/trace/seed.
type SeedRequest struct {
	// ProjectRoot is the absolute path to the project root. Required.
	ProjectRoot string `json:"project_root" binding:"required"`

	// DataSpace is the Weaviate data space for isolation. Required.
	DataSpace string `json:"data_space" binding:"required"`
}

// SeedResponse is the response for POST /v1/trace/seed.
type SeedResponse struct {
	// DependenciesFound is the number of dependencies discovered.
	DependenciesFound int `json:"dependencies_found"`

	// DocsIndexed is the number of documentation entries indexed.
	DocsIndexed int `json:"docs_indexed"`

	// Errors contains non-fatal errors encountered during seeding.
	Errors []string `json:"errors,omitempty"`
}

// HealthResponse is the response for GET /v1/trace/health.
type HealthResponse struct {
	// Status is "healthy" or "degraded".
	Status string `json:"status"`

	// Version is the service version.
	Version string `json:"version"`
}

// ReadyResponse is the response for GET /v1/trace/ready.
type ReadyResponse struct {
	// Ready is true if the service is ready to accept requests.
	Ready bool `json:"ready"`

	// GraphCount is the number of cached graphs.
	GraphCount int `json:"graph_count"`

	// WeaviateOK is true if Weaviate connection is healthy.
	WeaviateOK bool `json:"weaviate_ok"`
}

// ErrorResponse is the standard error response format.
type ErrorResponse struct {
	// Error is the error message.
	Error string `json:"error"`

	// Code is the error code (optional).
	Code string `json:"code,omitempty"`

	// Details provides additional error context (optional).
	Details string `json:"details,omitempty"`
}

// CachedGraph holds a graph and its associated data.
type CachedGraph struct {
	// Graph is the code graph.
	Graph *graph.Graph

	// Index is the symbol index.
	Index *index.SymbolIndex

	// Assembler is the context assembler.
	Assembler *cbcontext.Assembler

	// Adapter is the CRS-enabled graph adapter with query caching.
	// Created lazily on first use. Provides cache statistics via QueryCacheStats().
	Adapter *graph.CRSGraphAdapter

	// BuiltAtMilli is when the graph was built.
	BuiltAtMilli int64

	// ProjectRoot is the project root path.
	ProjectRoot string

	// ExpiresAtMilli is when the graph expires (0 = never).
	ExpiresAtMilli int64
}

// SymbolInfoFromAST converts an ast.Symbol to SymbolInfo.
func SymbolInfoFromAST(s *ast.Symbol) *SymbolInfo {
	if s == nil {
		return nil
	}
	return &SymbolInfo{
		ID:         s.ID,
		Name:       s.Name,
		Kind:       s.Kind.String(),
		FilePath:   s.FilePath,
		StartLine:  s.StartLine,
		EndLine:    s.EndLine,
		Signature:  s.Signature,
		DocComment: s.DocComment,
		Package:    s.Package,
		Exported:   s.Exported,
	}
}

// =============================================================================
// Agent API Types (CB-11 Agent Loop)
// =============================================================================

// AgentRunRequest is the request body for POST /v1/trace/agent/run.
type AgentRunRequest struct {
	// ProjectRoot is the absolute path to the project root directory.
	// Required.
	ProjectRoot string `json:"project_root" binding:"required"`

	// Query is the user's question or task description. Required.
	Query string `json:"query" binding:"required"`

	// Config is optional session configuration overrides.
	Config *agent.SessionConfig `json:"config,omitempty"`
}

// AgentRunResponse is the response for POST /v1/trace/agent/run.
type AgentRunResponse struct {
	// SessionID is the unique identifier for this session.
	SessionID string `json:"session_id"`

	// State is the current session state (IDLE, INIT, PLAN, EXECUTE, etc.).
	State string `json:"state"`

	// StepsTaken is the number of agent steps completed.
	StepsTaken int `json:"steps_taken"`

	// TokensUsed is the total tokens consumed.
	TokensUsed int `json:"tokens_used"`

	// Response is the agent's final response (if complete).
	Response string `json:"response,omitempty"`

	// NeedsClarify contains clarification details if state is CLARIFY.
	NeedsClarify *agent.ClarifyRequest `json:"needs_clarify,omitempty"`

	// Error is the error message if state is ERROR.
	Error string `json:"error,omitempty"`

	// DegradedMode indicates if the session is running with limited capabilities.
	DegradedMode bool `json:"degraded_mode"`
}

// AgentContinueRequest is the request body for POST /v1/trace/agent/continue.
type AgentContinueRequest struct {
	// SessionID is the session to continue. Required.
	SessionID string `json:"session_id" binding:"required"`

	// Clarification is the user's response to the clarification request. Required.
	Clarification string `json:"clarification" binding:"required"`
}

// AgentAbortRequest is the request body for POST /v1/trace/agent/abort.
type AgentAbortRequest struct {
	// SessionID is the session to abort. Required.
	SessionID string `json:"session_id" binding:"required"`
}

// AgentStateResponse is the response for GET /v1/trace/agent/:id.
type AgentStateResponse struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"session_id"`

	// ProjectRoot is the project root path.
	ProjectRoot string `json:"project_root"`

	// GraphID is the code graph ID (if initialized).
	GraphID string `json:"graph_id,omitempty"`

	// State is the current session state.
	State string `json:"state"`

	// StepCount is the number of steps completed.
	StepCount int `json:"step_count"`

	// TokensUsed is the total tokens consumed.
	TokensUsed int `json:"tokens_used"`

	// CreatedAt is the Unix timestamp of session creation.
	CreatedAt int64 `json:"created_at"`

	// LastActiveAt is the Unix timestamp of last activity.
	LastActiveAt int64 `json:"last_active_at"`

	// DegradedMode indicates if running with limited capabilities.
	DegradedMode bool `json:"degraded_mode"`
}

// =============================================================================
// CRS Export API Types (CB-29-2)
// =============================================================================

// ReasoningTraceResponse is the response for GET /v1/trace/agent/:id/reasoning.
type ReasoningTraceResponse struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"session_id"`

	// TotalSteps is the number of reasoning steps recorded.
	TotalSteps int `json:"total_steps"`

	// Duration is the total time from first to last step.
	Duration string `json:"total_duration"`

	// StartTime is when the first step occurred (RFC3339).
	StartTime string `json:"start_time,omitempty"`

	// EndTime is when the last step occurred (RFC3339).
	EndTime string `json:"end_time,omitempty"`

	// Trace contains all recorded reasoning steps.
	Trace []ReasoningStep `json:"trace"`

	// Summary provides high-level reasoning metrics.
	Summary *ReasoningSummaryResponse `json:"summary,omitempty"`
}

// ReasoningStep represents one step in the reasoning process.
type ReasoningStep struct {
	// Step is the 1-indexed step number.
	Step int `json:"step"`

	// Timestamp is when this step occurred (RFC3339).
	Timestamp string `json:"timestamp"`

	// Action describes what was done (e.g., "explore", "analyze", "trace_flow").
	Action string `json:"action"`

	// Target is the file or symbol being operated on.
	Target string `json:"target"`

	// Tool is the tool that triggered this action (optional).
	Tool string `json:"tool,omitempty"`

	// DurationMs is how long this step took in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// SymbolsFound lists symbols discovered in this step.
	SymbolsFound []string `json:"symbols_found,omitempty"`

	// ProofUpdates lists proof status changes.
	ProofUpdates []ProofUpdateResponse `json:"proof_updates,omitempty"`

	// Error contains any error that occurred.
	Error string `json:"error,omitempty"`

	// Metadata contains additional step context.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ProofUpdateResponse represents a proof status change in API responses.
type ProofUpdateResponse struct {
	// NodeID is the node whose proof status changed.
	NodeID string `json:"node_id"`

	// Status is the new status: "proven", "disproven", "expanded", "unknown".
	Status string `json:"status"`

	// Reason explains why the status changed.
	Reason string `json:"reason,omitempty"`

	// Source indicates signal source: "hard", "soft".
	Source string `json:"source,omitempty"`
}

// ReasoningSummaryResponse provides high-level reasoning metrics.
type ReasoningSummaryResponse struct {
	// NodesExplored is the total code nodes examined.
	NodesExplored int `json:"nodes_explored"`

	// NodesProven is nodes with verified behavior (tests pass, types check).
	NodesProven int `json:"nodes_proven"`

	// NodesDisproven is nodes with verified issues (tests fail, type errors).
	NodesDisproven int `json:"nodes_disproven"`

	// NodesUnknown is nodes without conclusive evidence.
	NodesUnknown int `json:"nodes_unknown"`

	// ConstraintsApplied is the number of constraints used in reasoning.
	ConstraintsApplied int `json:"constraints_applied"`

	// ExplorationDepth is the maximum depth reached in call graph traversal.
	ExplorationDepth int `json:"exploration_depth"`

	// ConfidenceScore is overall confidence in the reasoning (0.0-1.0).
	ConfidenceScore float64 `json:"confidence_score"`
}

// CRSExportResponse is the response for GET /v1/trace/agent/:id/crs.
type CRSExportResponse struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"session_id"`

	// Generation is the CRS snapshot generation number.
	Generation int64 `json:"generation"`

	// Timestamp is when this snapshot was taken (RFC3339).
	Timestamp string `json:"timestamp"`

	// Indexes contains exports of all six CRS indexes.
	Indexes CRSIndexesResponse `json:"indexes"`

	// Summary provides high-level reasoning metrics.
	Summary ReasoningSummaryResponse `json:"summary"`
}

// CRSIndexesResponse contains exports of all six CRS indexes.
//
// Note: Some indexes only provide aggregate counts for performance reasons.
// Full data export for similarity and dependency indexes is deferred.
type CRSIndexesResponse struct {
	// Proof contains proof status entries.
	Proof []ProofEntryResponse `json:"proof"`

	// Constraints contains constraint entries.
	Constraints []ConstraintEntryResponse `json:"constraints"`

	// SimilarityCount is the number of similarity pairs stored.
	// Full similarity matrix export is deferred for performance.
	SimilarityCount int `json:"similarity_count"`

	// DependencyCount is the number of dependency edges.
	// Full dependency graph export is deferred for performance.
	DependencyCount int `json:"dependency_count"`

	// History contains recent exploration history entries.
	History []HistoryEntryResponse `json:"history"`

	// StreamingCardinality is the estimated unique item count in streaming index.
	StreamingCardinality uint64 `json:"streaming_cardinality"`

	// StreamingBytes is the approximate memory usage of streaming index.
	StreamingBytes int `json:"streaming_bytes"`
}

// ProofEntryResponse represents a proof entry in API responses.
type ProofEntryResponse struct {
	// NodeID is the node identifier.
	NodeID string `json:"node_id"`

	// Status is the proof status: "unknown", "proven", "disproven", "expanded".
	Status string `json:"status"`

	// Evidence lists reasons for this status.
	Evidence []string `json:"evidence,omitempty"`
}

// ConstraintEntryResponse represents a constraint in API responses.
type ConstraintEntryResponse struct {
	// ID is the constraint identifier.
	ID string `json:"id"`

	// Type is the constraint type.
	Type string `json:"type"`

	// Nodes are the affected nodes.
	Nodes []string `json:"nodes"`

	// Strength is the constraint weight (0.0-1.0).
	Strength float64 `json:"strength"`
}

// HistoryEntryResponse represents an exploration history entry.
type HistoryEntryResponse struct {
	// NodeID is the explored node.
	NodeID string `json:"node_id"`

	// VisitCount is how many times this node was visited.
	VisitCount int `json:"visit_count"`

	// LastVisitedAt is when it was last visited (RFC3339).
	LastVisitedAt string `json:"last_visited_at,omitempty"`
}

// =============================================================================
// AGENTIC TOOL TYPES (CB-20/21/22/23 Tool Endpoints)
// =============================================================================
// Request and response types for the agentic reasoning layer tools.
// These wrap the internal package types for HTTP API consumption.

// ToolsResponse is the response for GET /v1/trace/tools.
type ToolsResponse struct {
	Tools []ToolDefinition `json:"tools"`
}

// --- Exploration Tool Types ---

// FindEntryPointsRequest is the request for POST /v1/trace/explore/entry_points.
type FindEntryPointsRequest struct {
	GraphID      string `json:"graph_id" binding:"required"`
	Type         string `json:"type"`
	Package      string `json:"package"`
	Limit        int    `json:"limit"`
	IncludeTests bool   `json:"include_tests"`
}

// TraceDataFlowRequest is the request for POST /v1/trace/explore/data_flow.
type TraceDataFlowRequest struct {
	GraphID     string `json:"graph_id" binding:"required"`
	SourceID    string `json:"source_id" binding:"required"`
	MaxHops     int    `json:"max_hops"`
	IncludeCode bool   `json:"include_code"`
}

// TraceErrorFlowRequest is the request for POST /v1/trace/explore/error_flow.
type TraceErrorFlowRequest struct {
	GraphID string `json:"graph_id" binding:"required"`
	Scope   string `json:"scope" binding:"required"`
	MaxHops int    `json:"max_hops"`
}

// FindConfigUsageRequest is the request for POST /v1/trace/explore/config_usage.
type FindConfigUsageRequest struct {
	GraphID         string `json:"graph_id" binding:"required"`
	ConfigKey       string `json:"config_key" binding:"required"`
	IncludeDefaults bool   `json:"include_defaults"`
}

// FindSimilarCodeRequest is the request for POST /v1/trace/explore/similar_code.
type FindSimilarCodeRequest struct {
	GraphID       string  `json:"graph_id" binding:"required"`
	SymbolID      string  `json:"symbol_id" binding:"required"`
	MinSimilarity float64 `json:"min_similarity"`
	Limit         int     `json:"limit"`
}

// BuildMinimalContextRequest is the request for POST /v1/trace/explore/minimal_context.
type BuildMinimalContextRequest struct {
	GraphID        string `json:"graph_id" binding:"required"`
	SymbolID       string `json:"symbol_id" binding:"required"`
	TokenBudget    int    `json:"token_budget"`
	IncludeCallees bool   `json:"include_callees"`
}

// SummarizeFileRequest is the request for POST /v1/trace/explore/summarize_file.
type SummarizeFileRequest struct {
	GraphID  string `json:"graph_id" binding:"required"`
	FilePath string `json:"file_path" binding:"required"`
}

// SummarizePackageRequest is the request for POST /v1/trace/explore/summarize_package.
type SummarizePackageRequest struct {
	GraphID string `json:"graph_id" binding:"required"`
	Package string `json:"package" binding:"required"`
}

// AnalyzeChangeImpactRequest is the request for POST /v1/trace/explore/change_impact.
type AnalyzeChangeImpactRequest struct {
	GraphID    string `json:"graph_id" binding:"required"`
	SymbolID   string `json:"symbol_id" binding:"required"`
	ChangeType string `json:"change_type"`
}

// --- Reasoning Tool Types ---

// CheckBreakingChangesRequest is the request for POST /v1/trace/reason/breaking_changes.
type CheckBreakingChangesRequest struct {
	GraphID           string `json:"graph_id" binding:"required"`
	SymbolID          string `json:"symbol_id" binding:"required"`
	ProposedSignature string `json:"proposed_signature" binding:"required"`
}

// SimulateChangeRequest is the request for POST /v1/trace/reason/simulate_change.
type SimulateChangeRequest struct {
	GraphID       string                 `json:"graph_id" binding:"required"`
	SymbolID      string                 `json:"symbol_id" binding:"required"`
	ChangeType    string                 `json:"change_type" binding:"required"`
	ChangeDetails map[string]interface{} `json:"change_details" binding:"required"`
}

// ValidateChangeRequest is the request for POST /v1/trace/reason/validate_change.
type ValidateChangeRequest struct {
	Code     string `json:"code" binding:"required"`
	Language string `json:"language" binding:"required"`
}

// FindTestCoverageRequest is the request for POST /v1/trace/reason/test_coverage.
type FindTestCoverageRequest struct {
	GraphID         string `json:"graph_id" binding:"required"`
	SymbolID        string `json:"symbol_id" binding:"required"`
	IncludeIndirect bool   `json:"include_indirect"`
}

// DetectSideEffectsRequest is the request for POST /v1/trace/reason/side_effects.
type DetectSideEffectsRequest struct {
	GraphID    string `json:"graph_id" binding:"required"`
	SymbolID   string `json:"symbol_id" binding:"required"`
	Transitive bool   `json:"transitive"`
}

// SuggestRefactorRequest is the request for POST /v1/trace/reason/suggest_refactor.
type SuggestRefactorRequest struct {
	GraphID  string `json:"graph_id" binding:"required"`
	SymbolID string `json:"symbol_id" binding:"required"`
}

// --- Coordination Tool Types ---

// PlanMultiFileChangeRequest is the request for POST /v1/trace/coordinate/plan_changes.
type PlanMultiFileChangeRequest struct {
	GraphID      string `json:"graph_id" binding:"required"`
	TargetID     string `json:"target_id" binding:"required"`
	ChangeType   string `json:"change_type" binding:"required"`
	NewSignature string `json:"new_signature"`
	NewName      string `json:"new_name"`
	Description  string `json:"description"`
	IncludeTests bool   `json:"include_tests"`
}

// ValidatePlanRequest is the request for POST /v1/trace/coordinate/validate_plan.
type ValidatePlanRequest struct {
	PlanID string `json:"plan_id" binding:"required"`
}

// PreviewChangesRequest is the request for POST /v1/trace/coordinate/preview_changes.
type PreviewChangesRequest struct {
	PlanID       string `json:"plan_id" binding:"required"`
	ContextLines int    `json:"context_lines"`
}

// --- Pattern Tool Types ---

// DetectPatternsRequest is the request for POST /v1/trace/patterns/detect.
type DetectPatternsRequest struct {
	GraphID       string   `json:"graph_id" binding:"required"`
	Scope         string   `json:"scope"`
	Patterns      []string `json:"patterns"`
	MinConfidence float64  `json:"min_confidence"`
}

// FindCodeSmellsRequest is the request for POST /v1/trace/patterns/code_smells.
type FindCodeSmellsRequest struct {
	GraphID      string `json:"graph_id" binding:"required"`
	Scope        string `json:"scope"`
	MinSeverity  string `json:"min_severity"`
	IncludeTests bool   `json:"include_tests"`
}

// FindDuplicationRequest is the request for POST /v1/trace/patterns/duplication.
type FindDuplicationRequest struct {
	GraphID       string  `json:"graph_id" binding:"required"`
	Scope         string  `json:"scope"`
	MinSimilarity float64 `json:"min_similarity"`
	Type          string  `json:"type"`
	IncludeTests  bool    `json:"include_tests"`
}

// FindCircularDepsRequest is the request for POST /v1/trace/patterns/circular_deps.
type FindCircularDepsRequest struct {
	GraphID string `json:"graph_id" binding:"required"`
	Scope   string `json:"scope"`
	Level   string `json:"level"`
}

// ExtractConventionsRequest is the request for POST /v1/trace/patterns/conventions.
type ExtractConventionsRequest struct {
	GraphID      string   `json:"graph_id" binding:"required"`
	Scope        string   `json:"scope"`
	Types        []string `json:"types"`
	IncludeTests bool     `json:"include_tests"`
}

// FindDeadCodeRequest is the request for POST /v1/trace/patterns/dead_code.
type FindDeadCodeRequest struct {
	GraphID         string `json:"graph_id" binding:"required"`
	Scope           string `json:"scope"`
	IncludeExported bool   `json:"include_exported"`
}

// --- Common Response Wrapper ---

// AgenticResponse wraps all agentic tool responses with latency tracking.
type AgenticResponse struct {
	// Result contains the actual response data.
	Result interface{} `json:"result"`

	// LatencyMs is the time taken to process the request in milliseconds.
	LatencyMs int64 `json:"latency_ms"`

	// Warnings contains non-fatal warnings if any.
	Warnings []string `json:"warnings,omitempty"`

	// Limitations documents what couldn't be analyzed.
	Limitations []string `json:"limitations,omitempty"`
}

// =============================================================================
// LSP INTEGRATION TYPES (CB-24)
// =============================================================================

// LSPDefinitionRequest is the request for POST /v1/trace/lsp/definition.
type LSPDefinitionRequest struct {
	// GraphID is the graph to use for project context. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// FilePath is the absolute path to the file. Required.
	FilePath string `json:"file_path" binding:"required"`

	// Line is the 1-indexed line number. Required.
	Line int `json:"line" binding:"required,min=1"`

	// Column is the 0-indexed column number. Required.
	Column int `json:"column" binding:"required,min=0"`
}

// LSPReferencesRequest is the request for POST /v1/trace/lsp/references.
type LSPReferencesRequest struct {
	// GraphID is the graph to use for project context. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// FilePath is the absolute path to the file. Required.
	FilePath string `json:"file_path" binding:"required"`

	// Line is the 1-indexed line number. Required.
	Line int `json:"line" binding:"required,min=1"`

	// Column is the 0-indexed column number. Required.
	Column int `json:"column" binding:"required,min=0"`

	// IncludeDeclaration includes the declaration in results.
	IncludeDeclaration bool `json:"include_declaration"`
}

// LSPHoverRequest is the request for POST /v1/trace/lsp/hover.
type LSPHoverRequest struct {
	// GraphID is the graph to use for project context. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// FilePath is the absolute path to the file. Required.
	FilePath string `json:"file_path" binding:"required"`

	// Line is the 1-indexed line number. Required.
	Line int `json:"line" binding:"required,min=1"`

	// Column is the 0-indexed column number. Required.
	Column int `json:"column" binding:"required,min=0"`
}

// LSPRenameRequest is the request for POST /v1/trace/lsp/rename.
type LSPRenameRequest struct {
	// GraphID is the graph to use for project context. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// FilePath is the absolute path to the file. Required.
	FilePath string `json:"file_path" binding:"required"`

	// Line is the 1-indexed line number. Required.
	Line int `json:"line" binding:"required,min=1"`

	// Column is the 0-indexed column number. Required.
	Column int `json:"column" binding:"required,min=0"`

	// NewName is the new name for the symbol. Required.
	NewName string `json:"new_name" binding:"required"`
}

// LSPWorkspaceSymbolRequest is the request for POST /v1/trace/lsp/symbols.
type LSPWorkspaceSymbolRequest struct {
	// GraphID is the graph to use for project context. Required.
	GraphID string `json:"graph_id" binding:"required"`

	// Query is the symbol search query. Required.
	Query string `json:"query" binding:"required"`

	// Language is the language to search (defaults to project primary language).
	Language string `json:"language"`
}

// LSPLocation represents a location in a document.
type LSPLocation struct {
	// FilePath is the absolute path to the file.
	FilePath string `json:"file_path"`

	// StartLine is the 1-indexed start line.
	StartLine int `json:"start_line"`

	// StartColumn is the 0-indexed start column.
	StartColumn int `json:"start_column"`

	// EndLine is the 1-indexed end line.
	EndLine int `json:"end_line"`

	// EndColumn is the 0-indexed end column.
	EndColumn int `json:"end_column"`
}

// LSPDefinitionResponse is the response for POST /v1/trace/lsp/definition.
type LSPDefinitionResponse struct {
	// Locations contains the definition location(s).
	Locations []LSPLocation `json:"locations"`

	// LatencyMs is the request latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
}

// LSPReferencesResponse is the response for POST /v1/trace/lsp/references.
type LSPReferencesResponse struct {
	// Locations contains the reference location(s).
	Locations []LSPLocation `json:"locations"`

	// LatencyMs is the request latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
}

// LSPHoverResponse is the response for POST /v1/trace/lsp/hover.
type LSPHoverResponse struct {
	// Content is the hover content (documentation, type info).
	Content string `json:"content"`

	// Kind is the content format ("plaintext" or "markdown").
	Kind string `json:"kind"`

	// Range is the range this hover applies to (optional).
	Range *LSPLocation `json:"range,omitempty"`

	// LatencyMs is the request latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
}

// LSPRenameResponse is the response for POST /v1/trace/lsp/rename.
type LSPRenameResponse struct {
	// Edits is a map from file path to list of text edits.
	Edits map[string][]LSPTextEdit `json:"edits"`

	// FileCount is the number of files affected.
	FileCount int `json:"file_count"`

	// EditCount is the total number of edits.
	EditCount int `json:"edit_count"`

	// LatencyMs is the request latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
}

// LSPTextEdit represents a text edit.
type LSPTextEdit struct {
	// Range is the range to replace.
	Range LSPLocation `json:"range"`

	// NewText is the replacement text.
	NewText string `json:"new_text"`
}

// LSPSymbolInfo represents information about a workspace symbol.
type LSPSymbolInfo struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind (function, class, etc.).
	Kind string `json:"kind"`

	// Location is where the symbol is defined.
	Location LSPLocation `json:"location"`

	// ContainerName is the name of the containing symbol.
	ContainerName string `json:"container_name,omitempty"`
}

// LSPWorkspaceSymbolResponse is the response for POST /v1/trace/lsp/symbols.
type LSPWorkspaceSymbolResponse struct {
	// Symbols contains the matching symbols.
	Symbols []LSPSymbolInfo `json:"symbols"`

	// LatencyMs is the request latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
}

// LSPStatusResponse is the response for GET /v1/trace/lsp/status.
type LSPStatusResponse struct {
	// Available indicates if LSP is available for the project.
	Available bool `json:"available"`

	// RunningServers lists languages with running servers.
	RunningServers []string `json:"running_servers"`

	// SupportedLanguages lists all supported languages.
	SupportedLanguages []string `json:"supported_languages"`
}

// =============================================================================
// DEBUG ENDPOINT TYPES (GR-43)
// =============================================================================

// GraphStatsResponse is the response for GET /v1/trace/debug/graph/stats.
//
// Description:
//
//	Returns detailed statistics about a cached graph including node/edge counts
//	broken down by type and kind. Used for debugging and integration tests.
type GraphStatsResponse struct {
	// GraphID is the unique identifier for this graph.
	GraphID string `json:"graph_id"`

	// ProjectRoot is the absolute path to the project root.
	ProjectRoot string `json:"project_root"`

	// State is the graph state ("building" or "readonly").
	State string `json:"state"`

	// NodeCount is the total number of nodes in the graph.
	NodeCount int `json:"node_count"`

	// EdgeCount is the total number of edges in the graph.
	EdgeCount int `json:"edge_count"`

	// MaxNodes is the configured maximum node capacity.
	MaxNodes int `json:"max_nodes"`

	// MaxEdges is the configured maximum edge capacity.
	MaxEdges int `json:"max_edges"`

	// BuiltAtMilli is the Unix timestamp in milliseconds when graph was frozen.
	BuiltAtMilli int64 `json:"built_at_milli"`

	// EdgesByType maps edge type name to count.
	EdgesByType map[string]int `json:"edges_by_type"`

	// NodesByKind maps symbol kind name to count.
	NodesByKind map[string]int `json:"nodes_by_kind"`
}

// =============================================================================
// CRS DEBUG ENDPOINT TYPES (GR-Phase1 Issue 5)
// =============================================================================

// DebugCRSResponse is the response for GET /v1/trace/debug/crs.
//
// Description:
//
//	Returns CRS (Code Reasoning State) debug information. When session_id is
//	provided, returns detailed state for that session. Otherwise returns a
//	summary of all active sessions.
type DebugCRSResponse struct {
	// ActiveSessions is the count of sessions with CRS state.
	ActiveSessions int `json:"active_sessions"`

	// TotalTraceSteps is the total trace steps across all sessions.
	TotalTraceSteps int `json:"total_trace_steps"`

	// CircuitBreakersActive is the count of sessions with active circuit breakers.
	CircuitBreakersActive int `json:"circuit_breakers_active"`

	// Sessions is a summary of each session (only in summary mode).
	Sessions []DebugCRSSessionSummary `json:"sessions,omitempty"`

	// Session is the detailed session state (only when session_id provided).
	Session *DebugCRSSessionDetail `json:"session,omitempty"`
}

// DebugCRSSessionSummary is a brief summary of a session's CRS state.
type DebugCRSSessionSummary struct {
	// ID is the session identifier.
	ID string `json:"id"`

	// State is the current agent state (e.g., "EXECUTE", "COMPLETE").
	State string `json:"state"`

	// TraceSteps is the number of trace steps recorded.
	TraceSteps int `json:"trace_steps"`

	// CircuitBreakerActive indicates if circuit breaker has fired.
	CircuitBreakerActive bool `json:"circuit_breaker_active"`

	// CreatedAtMilli is the session creation time (Unix milliseconds).
	CreatedAtMilli int64 `json:"created_at_milli"`
}

// DebugCRSSessionDetail is the detailed CRS state for a single session.
type DebugCRSSessionDetail struct {
	// SessionID is the session identifier.
	SessionID string `json:"session_id"`

	// State is the current agent state.
	State string `json:"state"`

	// TraceSteps is the list of recorded trace steps.
	TraceSteps []DebugCRSTraceStep `json:"trace_steps"`

	// CircuitBreaker contains circuit breaker state.
	CircuitBreaker *DebugCRSCircuitBreaker `json:"circuit_breaker,omitempty"`

	// Metrics contains session metrics.
	Metrics DebugCRSMetrics `json:"metrics"`

	// ToolCounts maps tool name to call count.
	ToolCounts map[string]int `json:"tool_counts"`
}

// DebugCRSTraceStep is a trace step for debug output.
type DebugCRSTraceStep struct {
	// Action is the step action (e.g., "tool_call", "semantic_correction").
	Action string `json:"action"`

	// Tool is the tool name if applicable.
	Tool string `json:"tool,omitempty"`

	// Target is the target of the action.
	Target string `json:"target,omitempty"`

	// DurationMs is the duration in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// Error is the error message if the step failed.
	Error string `json:"error,omitempty"`

	// Metadata contains additional step metadata.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DebugCRSCircuitBreaker contains circuit breaker state.
type DebugCRSCircuitBreaker struct {
	// Active indicates if the circuit breaker has fired.
	Active bool `json:"active"`

	// Tool is the tool that triggered the circuit breaker.
	Tool string `json:"tool,omitempty"`

	// Count is the call count when circuit breaker fired.
	Count int `json:"count,omitempty"`

	// Threshold is the configured threshold.
	Threshold int `json:"threshold"`
}

// DebugCRSMetrics contains session metrics for debug output.
type DebugCRSMetrics struct {
	// ToolCalls is the total number of tool calls.
	ToolCalls int `json:"tool_calls"`

	// TokensUsed is the estimated token count.
	TokensUsed int `json:"tokens_used"`

	// Steps is the number of agent steps.
	Steps int `json:"steps"`
}

// =============================================================================
// DEBUG HISTORY ENDPOINT TYPES (GR-Phase1 Issue 5b)
// =============================================================================

// DebugHistoryResponse is the response for GET /v1/trace/agent/debug/history.
//
// Description:
//
//	Returns query history across all sessions or for a specific session.
//	Used by integration tests to verify reasoning history and query flow.
type DebugHistoryResponse struct {
	// Count is the total number of history entries returned.
	Count int `json:"count"`

	// Sessions is the list of unique session IDs with history.
	Sessions []string `json:"sessions"`

	// History is the list of query history entries.
	History []DebugHistoryEntry `json:"history"`

	// Limit is the maximum entries that can be returned.
	Limit int `json:"limit"`
}

// DebugHistoryEntry represents a single query in the history.
type DebugHistoryEntry struct {
	// SessionID is the session that processed this query.
	SessionID string `json:"session_id"`

	// Timestamp is when the query was received (Unix milliseconds).
	Timestamp int64 `json:"timestamp"`

	// Query is the user's question or task.
	Query string `json:"query"`

	// State is the final state of the query (e.g., "COMPLETE", "ERROR").
	State string `json:"state"`

	// ToolsUsed is the list of tools invoked for this query.
	ToolsUsed []string `json:"tools_used"`

	// DurationMs is how long the query took to process.
	DurationMs int64 `json:"duration_ms"`
}
