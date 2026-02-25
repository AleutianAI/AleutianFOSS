// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package routing provides tool selection using a fast micro LLM.
//
// # Description
//
// The routing package implements a ToolRouter that uses a dedicated micro LLM
// (like granite4:micro-h) to quickly classify queries and select the appropriate
// tool before the main reasoning LLM processes the request.
//
// This separation of concerns allows:
//   - Fast tool selection (~50-100ms) vs full LLM reasoning (~10-30s)
//   - Better resource utilization (small model for routing, large for reasoning)
//   - Parallel model execution on GPUs with sufficient VRAM
//
// # Thread Safety
//
// All types in this package are designed for concurrent use.
package routing

import (
	"context"
	"time"
)

// =============================================================================
// Tool Router Interface
// =============================================================================

// ToolRouter selects the appropriate tool for a user query.
//
// # Description
//
// ToolRouter uses a fast micro LLM to classify queries into tool categories
// before the main reasoning LLM processes the request. This allows tool
// selection to complete in ~50-100ms instead of the full LLM inference time.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
//
// # Example
//
//	router := NewGranite4Router(ollamaEndpoint, "granite4:micro-h")
//	selection, err := router.SelectTool(ctx, "What functions call parseConfig?", tools, codeCtx)
//	if err != nil {
//	    // Fall back to letting main LLM choose
//	}
//	// Use selection.Tool to constrain main LLM
type ToolRouter interface {
	// SelectTool chooses the best tool for the given query and context.
	//
	// Inputs:
	//   - ctx: Context for cancellation/timeout. Must not be nil.
	//   - query: The user's question or request.
	//   - availableTools: Tools currently available for selection.
	//   - context: Optional code context (symbols, files loaded, etc.)
	//
	// Outputs:
	//   - *ToolSelection: The selected tool and optional parameter hints.
	//   - error: Non-nil if routing fails (caller should fall back to main LLM).
	SelectTool(ctx context.Context, query string, availableTools []ToolSpec, context *CodeContext) (*ToolSelection, error)

	// Model returns the model being used for routing.
	Model() string

	// Close releases any resources held by the router.
	Close() error
}

// =============================================================================
// Selection Result
// =============================================================================

// ToolSelection represents the router's decision.
//
// # Description
//
// Contains the selected tool name, confidence score, and optional hints
// for parameter values. The confidence score can be used to decide whether
// to trust the selection or fall back to the main LLM.
type ToolSelection struct {
	// Tool is the selected tool name (e.g., "find_symbol_usages").
	Tool string `json:"tool"`

	// Confidence is the router's confidence in this selection (0.0-1.0).
	// Use this to decide whether to fall back to the main LLM.
	// Recommended threshold: 0.7
	Confidence float64 `json:"confidence"`

	// ParamsHint contains optional parameter suggestions.
	// Keys are parameter names, values are suggested values.
	// The main LLM can use these as hints but isn't bound by them.
	ParamsHint map[string]string `json:"params_hint,omitempty"`

	// Reasoning is a brief explanation of why this tool was selected.
	// Used for debugging and tracing.
	Reasoning string `json:"reasoning,omitempty"`

	// Duration is how long the routing decision took.
	Duration time.Duration `json:"duration,omitempty"`

	// PrefilterMiss is true when the router selected a tool that was NOT in the
	// prefiltered candidate set. This means the prefilter incorrectly excluded
	// the correct tool. The EscalatingRouter uses this flag to trigger recovery.
	// CB-62 Rev 2.
	PrefilterMiss bool `json:"prefilter_miss,omitempty"`

	// RawModelPick is the tool name the router model originally returned,
	// before any findClosestTool correction. Empty when the model's pick
	// was in the candidate set. Used for observability and recovery.
	// CB-62 Rev 2.
	RawModelPick string `json:"raw_model_pick,omitempty"`
}

// IsConfident returns true if the selection confidence is above the threshold.
//
// # Inputs
//
//   - threshold: Minimum confidence to consider (e.g., 0.7).
//
// # Outputs
//
//   - bool: True if Confidence >= threshold.
func (s *ToolSelection) IsConfident(threshold float64) bool {
	return s.Confidence >= threshold
}

// =============================================================================
// Tool Specification
// =============================================================================

// ToolSpec describes a tool for the router's system prompt.
//
// # Description
//
// Provides enough information about a tool for the router to make a
// selection decision. This is a simplified view of the full tool definition
// optimized for fast classification.
type ToolSpec struct {
	// Name is the tool's unique identifier (e.g., "find_symbol_usages").
	Name string `json:"name"`

	// Description explains what the tool does.
	Description string `json:"description"`

	// BestFor lists keywords that should trigger this tool.
	// CB-31e: Now populated from WhenToUse.Keywords.
	BestFor []string `json:"best_for,omitempty"`

	// UseWhen describes when to use this tool.
	// CB-31e: New field for structured routing guidance.
	UseWhen string `json:"use_when,omitempty"`

	// AvoidWhen describes when NOT to use this tool.
	// CB-31e: New field for structured routing guidance.
	AvoidWhen string `json:"avoid_when,omitempty"`

	// InsteadOf lists tools this should replace in specific scenarios.
	// CB-31e: New field for tool substitution patterns.
	InsteadOf []ToolSubstitution `json:"instead_of,omitempty"`

	// Params lists the parameter names (without full schema).
	// Used to generate parameter hints.
	Params []string `json:"params,omitempty"`

	// Category groups similar tools (e.g., "search", "read", "analyze").
	// Helps the router with coarse-grained classification.
	Category string `json:"category,omitempty"`
}

// ToolSubstitution describes when one tool should be used instead of another.
// CB-31e: Captures tool substitution patterns for the router.
type ToolSubstitution struct {
	// Tool is the name of the tool to replace.
	Tool string `json:"tool"`

	// When describes when substitution applies.
	When string `json:"when"`
}

// =============================================================================
// Code Context
// =============================================================================

// CodeContext provides optional context about the codebase.
//
// # Description
//
// Gives the router additional context to make better decisions.
// For example, knowing the primary language helps select language-specific tools.
//
// # History-Aware Routing
//
// This struct is designed to leverage Mamba2's O(n) linear complexity and
// 1M token context window. By including tool history with summaries, the
// router can make informed decisions about what information is still needed
// rather than suggesting the same tools repeatedly.
type CodeContext struct {
	// Language is the primary programming language (e.g., "go", "python").
	Language string `json:"language,omitempty"`

	// Files is the number of files currently loaded/indexed.
	Files int `json:"files,omitempty"`

	// Symbols is the number of symbols available for search.
	Symbols int `json:"symbols,omitempty"`

	// CurrentFile is the file currently being viewed/edited.
	CurrentFile string `json:"current_file,omitempty"`

	// RecentTools lists recently used tools (for context awareness).
	// DEPRECATED: Use ToolHistory instead for richer context.
	RecentTools []string `json:"recent_tools,omitempty"`

	// PreviousErrors contains tools that failed in this session.
	// The router should avoid suggesting these unless it can fix the issue.
	PreviousErrors []ToolError `json:"previous_errors,omitempty"`

	// ToolHistory contains the sequence of tools used with their results.
	// This enables history-aware routing where the router can see what
	// was already tried and what was learned from each tool.
	ToolHistory []ToolHistoryEntry `json:"tool_history,omitempty"`

	// Progress describes the current state of information gathering.
	// Example: "Found 3 entry points, read 2 files, identified main handler"
	Progress string `json:"progress,omitempty"`

	// StepNumber is the current execution step (1-indexed).
	StepNumber int `json:"step_number,omitempty"`
}

// ToolHistoryEntry captures a tool execution with its outcome.
//
// # Description
//
// Records what tool was called, what it found, and whether it succeeded.
// This gives the router context about what information is already available,
// enabling it to suggest the NEXT logical tool rather than repeating.
type ToolHistoryEntry struct {
	// Tool is the tool name that was called.
	Tool string `json:"tool"`

	// Summary is a brief description of what was found/returned.
	// Example: "Found 5 callers of parseConfig in pkg/config/"
	Summary string `json:"summary"`

	// Success indicates whether the tool call succeeded.
	Success bool `json:"success"`

	// StepNumber when this tool was called.
	StepNumber int `json:"step_number,omitempty"`
}

// ToolError captures a failed tool attempt for router feedback.
//
// # Description
//
// When a tool fails (e.g., missing required parameter), the error is recorded
// and fed back to the router. This helps the router avoid suggesting the same
// tool repeatedly when it cannot be used successfully.
type ToolError struct {
	// Tool is the tool name that failed.
	Tool string `json:"tool"`

	// Error is the error message from the failure.
	Error string `json:"error"`

	// Timestamp when the error occurred.
	Timestamp string `json:"timestamp,omitempty"`
}

// =============================================================================
// Router Configuration
// =============================================================================

// RouterConfig configures the tool router behavior.
//
// # Description
//
// Controls the router's model selection, timeout, and fallback behavior.
type RouterConfig struct {
	// Model is the Ollama model to use for routing (e.g., "granite4:micro-h").
	Model string `json:"model"`

	// OllamaEndpoint is the Ollama server URL (e.g., "http://localhost:11434").
	OllamaEndpoint string `json:"ollama_endpoint"`

	// Timeout is the maximum time for a routing decision.
	// Recommended: 500ms. If exceeded, returns error for fallback.
	Timeout time.Duration `json:"timeout"`

	// Temperature controls randomness. Lower = more deterministic.
	// Recommended: 0.1 for routing (we want consistency).
	Temperature float64 `json:"temperature"`

	// ConfidenceThreshold is the minimum confidence for a selection.
	// Below this, the router returns an error for fallback to main LLM.
	ConfidenceThreshold float64 `json:"confidence_threshold"`

	// KeepAlive controls how long the router model stays in VRAM.
	// "-1" = infinite (recommended when using with main LLM).
	KeepAlive string `json:"keep_alive"`

	// MaxTokens limits the router's response length.
	// Should be small since we only need JSON output.
	// Recommended: 256.
	MaxTokens int `json:"max_tokens"`

	// NumCtx sets the context window size for the router model.
	// Router only needs to see current query, available tools, and recent history.
	// Recommended: 16384 (16K tokens) to minimize VRAM usage and allow main agent larger context.
	// Note: On systems with limited VRAM (32GB), keeping router context low allows main agent 64K+ context.
	NumCtx int `json:"num_ctx"`

	// EscalationModel is the Ollama model used for escalation when the primary
	// router's confidence is below EscalationThreshold. Empty string disables
	// escalation. Example: "granite3.3:8b".
	EscalationModel string `json:"escalation_model,omitempty"`

	// EscalationThreshold is the minimum confidence from the primary router
	// below which escalation to the larger model is triggered. Default: 0.7.
	EscalationThreshold float64 `json:"escalation_threshold,omitempty"`

	// EscalationTimeout is the maximum time for an escalation routing decision.
	// Default: 3s.
	EscalationTimeout time.Duration `json:"escalation_timeout,omitempty"`
}

// DefaultRouterConfig returns sensible defaults for the router.
//
// # Outputs
//
//   - RouterConfig: Default configuration.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		Model:               "granite4:micro-h",
		OllamaEndpoint:      "http://localhost:11434",
		Timeout:             500 * time.Millisecond,
		Temperature:         0.1,
		ConfidenceThreshold: 0.7,
		KeepAlive:           "24h",
		MaxTokens:           256,
		NumCtx:              16384, // 16K context window (router doesn't need huge context)
	}
}

// =============================================================================
// Error Types
// =============================================================================

// RouterError represents an error from the tool router.
type RouterError struct {
	// Code is a machine-readable error code.
	Code string `json:"code"`

	// Message is a human-readable error message.
	Message string `json:"message"`

	// Retryable indicates if the error might resolve on retry.
	Retryable bool `json:"retryable"`
}

// Error implements the error interface.
func (e *RouterError) Error() string {
	return e.Code + ": " + e.Message
}

// Common router error codes.
const (
	ErrCodeTimeout          = "ROUTER_TIMEOUT"
	ErrCodeLowConfidence    = "ROUTER_LOW_CONFIDENCE"
	ErrCodeParseError       = "ROUTER_PARSE_ERROR"
	ErrCodeModelUnavailable = "ROUTER_MODEL_UNAVAILABLE"
	ErrCodeNoTools          = "ROUTER_NO_TOOLS"
	ErrCodeEmptyResponse    = "ROUTER_EMPTY_RESPONSE" // GR-Phase1: Model returned empty response
)

// NewRouterError creates a new RouterError.
func NewRouterError(code, message string, retryable bool) *RouterError {
	return &RouterError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
}
