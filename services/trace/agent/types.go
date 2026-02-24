// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package agent provides a state-machine-driven agent orchestration system.
//
// The agent loop coordinates exploration tools, safety checks, and LLM
// interactions to answer user queries about codebases. It implements a
// finite state machine with phases: IDLE, INIT, PLAN, EXECUTE, REFLECT,
// CLARIFY, DEGRADED, COMPLETE, and ERROR.
//
// Thread Safety:
//
//	All exported types in this package are designed for concurrent use.
//	Sessions are protected by internal synchronization.
package agent

import (
	"context"
	"time"
)

// AgentState represents a state in the agent loop state machine.
//
// Valid state transitions are enforced by the state machine. Invalid
// transitions return ErrInvalidTransition.
type AgentState string

const (
	// StateIdle is the initial state before any query is received.
	StateIdle AgentState = "IDLE"

	// StateInit initializes the Trace graph for the project.
	StateInit AgentState = "INIT"

	// StatePlan assembles initial context and classifies the query.
	StatePlan AgentState = "PLAN"

	// StateExecute is the main tool-use loop.
	StateExecute AgentState = "EXECUTE"

	// StateReflect enables self-correction after hitting step limits.
	StateReflect AgentState = "REFLECT"

	// StateClarify requests additional input from the user.
	StateClarify AgentState = "CLARIFY"

	// StateDegraded operates with limited tools when Trace unavailable.
	StateDegraded AgentState = "DEGRADED"

	// StateComplete indicates successful completion.
	StateComplete AgentState = "COMPLETE"

	// StateError indicates an unrecoverable error occurred.
	StateError AgentState = "ERROR"
)

// ModelType identifies the LLM model for format-aware processing.
//
// Description:
//
//	Used by control flow hardening layers to apply model-specific logic:
//	- Parser: Different models output tool calls in different formats
//	- Sanitizer: Some formats are native to certain models and shouldn't be stripped
//
// Thread Safety: Safe for concurrent use (immutable string type).
type ModelType string

const (
	// ModelClaude represents Anthropic Claude models.
	// Native format: <function_calls>/<invoke> - should not be stripped by sanitizer.
	ModelClaude ModelType = "claude"

	// ModelGLM4 represents GLM-4 models.
	// Uses <execute><command> format for tool calls.
	ModelGLM4 ModelType = "glm4"

	// ModelGPT4 represents OpenAI GPT-4 models.
	// Uses JSON function_call format.
	ModelGPT4 ModelType = "gpt4"

	// ModelGeneric represents unknown/generic models.
	// Default: strip all non-standard formats.
	ModelGeneric ModelType = "generic"
)

// String returns the string representation of the model type.
//
// Outputs:
//
//	string - The model type as a string (e.g., "claude", "gpt4")
func (m ModelType) String() string {
	return string(m)
}

// IsAnthropicModel returns true if this is an Anthropic model.
//
// Description:
//
//	Anthropic models use native function_calls format that should be preserved
//	by the sanitizer rather than stripped as leaked markup.
//
// Outputs:
//
//	bool - True if model is Claude
func (m ModelType) IsAnthropicModel() bool {
	return m == ModelClaude
}

// String returns the string representation of the state.
//
// Outputs:
//
//	string - The state as a string (e.g., "IDLE", "EXECUTE")
func (s AgentState) String() string {
	return string(s)
}

// IsTerminal returns true if the state is a terminal state (COMPLETE or ERROR).
//
// Outputs:
//
//	bool - True if state is COMPLETE or ERROR
func (s AgentState) IsTerminal() bool {
	return s == StateComplete || s == StateError
}

// IsActive returns true if the state allows continued execution.
//
// Outputs:
//
//	bool - True if state is INIT, PLAN, EXECUTE, REFLECT, or DEGRADED
func (s AgentState) IsActive() bool {
	switch s {
	case StateInit, StatePlan, StateExecute, StateReflect, StateDegraded:
		return true
	default:
		return false
	}
}

// AllStates returns all valid agent states.
//
// Outputs:
//
//	[]AgentState - Slice containing all 9 valid states
func AllStates() []AgentState {
	return []AgentState{
		StateIdle,
		StateInit,
		StatePlan,
		StateExecute,
		StateReflect,
		StateClarify,
		StateDegraded,
		StateComplete,
		StateError,
	}
}

// HistoryEntry records a single step in the agent's execution history.
type HistoryEntry struct {
	// Step is the 0-indexed step number.
	Step int `json:"step"`

	// Type describes what happened (plan, tool_call, reflection, etc.).
	Type string `json:"type"`

	// State is the agent state at this step.
	State AgentState `json:"state"`

	// Query is the user's query (for plan entries).
	Query string `json:"query,omitempty"`

	// ToolName is the tool that was invoked (for tool_call entries).
	ToolName string `json:"tool_name,omitempty"`

	// Input contains the tool input or prompt.
	Input string `json:"input,omitempty"`

	// Output contains the tool result or LLM response.
	Output string `json:"output,omitempty"`

	// TokensUsed is the tokens consumed in this step.
	TokensUsed int `json:"tokens_used,omitempty"`

	// DurationMs is how long this step took in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// Timestamp is when this step occurred (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`

	// Error contains any error message from this step.
	Error string `json:"error,omitempty"`

	// ClarificationPrompt is the prompt shown when entering CLARIFY state.
	ClarificationPrompt string `json:"clarification_prompt,omitempty"`
}

// SessionMetrics tracks metrics for a session.
type SessionMetrics struct {
	// TotalSteps is the number of steps executed.
	TotalSteps int `json:"total_steps"`

	// TotalTokens is the total tokens consumed.
	TotalTokens int `json:"total_tokens"`

	// TotalDurationMs is the total execution time in milliseconds.
	TotalDurationMs int64 `json:"total_duration_ms"`

	// ToolCalls is the number of tool invocations.
	ToolCalls int `json:"tool_calls"`

	// ToolErrors is the number of failed tool calls.
	ToolErrors int `json:"tool_errors"`

	// LLMCalls is the number of LLM API calls.
	LLMCalls int `json:"llm_calls"`

	// CacheHits is the number of cache hits.
	CacheHits int `json:"cache_hits"`

	// DegradedMode indicates if running in degraded mode.
	DegradedMode bool `json:"degraded_mode"`

	// GroundingRetries is the number of grounding validation retries.
	GroundingRetries int `json:"grounding_retries"`

	// ToolForcingRetries is the number of tool forcing retries.
	ToolForcingRetries int `json:"tool_forcing_retries"`

	// GraphStats contains Trace graph statistics.
	GraphStats *GraphStats `json:"graph_stats,omitempty"`
}

// GraphStats contains statistics about the Trace graph.
type GraphStats struct {
	// FilesParsed is the number of files in the graph.
	FilesParsed int `json:"files_parsed"`

	// SymbolsExtracted is the number of symbols extracted.
	SymbolsExtracted int `json:"symbols_extracted"`

	// EdgesBuilt is the number of edges in the graph.
	EdgesBuilt int `json:"edges_built"`

	// ParseTimeMs is how long graph construction took.
	ParseTimeMs int64 `json:"parse_time_ms"`
}

// RunResult contains the outcome of a Run or Continue call.
type RunResult struct {
	// State is the current agent state after execution.
	State AgentState `json:"state"`

	// Response is the final response text (for COMPLETE state).
	Response string `json:"response,omitempty"`

	// NeedsClarify contains clarification details (for CLARIFY state).
	NeedsClarify *ClarifyRequest `json:"needs_clarify,omitempty"`

	// ToolsUsed lists all tool invocations made.
	ToolsUsed []ToolInvocation `json:"tools_used"`

	// TokensUsed is the total tokens consumed.
	TokensUsed int `json:"tokens_used"`

	// StepsTaken is the number of steps executed.
	StepsTaken int `json:"steps_taken"`

	// SafetyChecks contains any safety analysis results.
	SafetyChecks []SafetyResult `json:"safety_checks,omitempty"`

	// Error contains error details (for ERROR state).
	Error *AgentError `json:"error,omitempty"`

	// ReasoningSummary provides high-level metrics about the reasoning process.
	// Populated when CRS is enabled for the session.
	ReasoningSummary *ReasoningSummary `json:"reasoning_summary,omitempty"`
}

// ReasoningSummary provides high-level metrics about reasoning progress.
//
// Description:
//
//	Computed from CRS state to give a quick overview of reasoning
//	progress without requiring full index inspection. Included in
//	RunResult when CRS is enabled for the session.
type ReasoningSummary struct {
	// NodesExplored is the total number of nodes in the proof index.
	NodesExplored int `json:"nodes_explored"`

	// NodesProven is the count of nodes with PROVEN status.
	NodesProven int `json:"nodes_proven"`

	// NodesDisproven is the count of nodes with DISPROVEN status.
	NodesDisproven int `json:"nodes_disproven"`

	// NodesUnknown is the count of nodes with UNKNOWN or EXPANDED status.
	NodesUnknown int `json:"nodes_unknown"`

	// ConstraintsApplied is the number of active constraints.
	ConstraintsApplied int `json:"constraints_applied"`

	// ExplorationDepth is the number of history entries (proxy for depth).
	ExplorationDepth int `json:"exploration_depth"`

	// ConfidenceScore is the ratio of proven nodes to explored nodes.
	// Value is between 0.0 and 1.0. Use with caution - this is a coverage
	// metric, not a statistical confidence interval.
	ConfidenceScore float64 `json:"confidence_score"`
}

// ClarifyRequest contains details when agent needs clarification.
type ClarifyRequest struct {
	// Question is what the agent needs to know.
	Question string `json:"question"`

	// Options are suggested clarification options.
	Options []string `json:"options,omitempty"`

	// Context explains why clarification is needed.
	Context string `json:"context,omitempty"`
}

// ToolParameters represents validated parameters for a tool invocation.
//
// This struct provides type-safe parameter storage instead of map[string]any.
// The RawJSON field allows flexible parameter passing while maintaining
// type safety at the API boundary.
type ToolParameters struct {
	// RawJSON contains the raw JSON parameters for flexible tool inputs.
	// Use encoding/json to unmarshal into tool-specific parameter structs.
	RawJSON []byte `json:"raw_json,omitempty"`

	// StringParams contains string-typed parameters.
	StringParams map[string]string `json:"string_params,omitempty"`

	// IntParams contains integer-typed parameters.
	IntParams map[string]int `json:"int_params,omitempty"`

	// BoolParams contains boolean-typed parameters.
	BoolParams map[string]bool `json:"bool_params,omitempty"`
}

// GetString returns a string parameter by key.
//
// Inputs:
//
//	key - The parameter name
//
// Outputs:
//
//	string - The parameter value
//	bool - True if the parameter exists
func (p *ToolParameters) GetString(key string) (string, bool) {
	if p.StringParams == nil {
		return "", false
	}
	v, ok := p.StringParams[key]
	return v, ok
}

// GetInt returns an integer parameter by key.
//
// Inputs:
//
//	key - The parameter name
//
// Outputs:
//
//	int - The parameter value
//	bool - True if the parameter exists
func (p *ToolParameters) GetInt(key string) (int, bool) {
	if p.IntParams == nil {
		return 0, false
	}
	v, ok := p.IntParams[key]
	return v, ok
}

// GetBool returns a boolean parameter by key.
//
// Inputs:
//
//	key - The parameter name
//
// Outputs:
//
//	bool - The parameter value
//	bool - True if the parameter exists
func (p *ToolParameters) GetBool(key string) (bool, bool) {
	if p.BoolParams == nil {
		return false, false
	}
	v, ok := p.BoolParams[key]
	return v, ok
}

// ToolInvocation represents a single tool call.
type ToolInvocation struct {
	// ID is a unique identifier for this invocation.
	ID string `json:"id"`

	// Tool is the tool name.
	Tool string `json:"tool"`

	// Parameters are the tool input parameters.
	Parameters *ToolParameters `json:"parameters"`

	// Reason explains why the agent chose this tool.
	Reason string `json:"reason,omitempty"`

	// StepNumber is the step when this was invoked.
	StepNumber int `json:"step_number"`

	// Result contains the tool execution result.
	Result *ToolResult `json:"result,omitempty"`
}

// ToolResult contains the outcome of a tool execution.
//
// Note: Duration is serialized as nanoseconds (int64) in JSON per Go's
// default time.Duration marshaling. Use DurationMs() for milliseconds.
type ToolResult struct {
	// InvocationID links back to the invocation.
	InvocationID string `json:"invocation_id"`

	// Success indicates if the tool succeeded.
	Success bool `json:"success"`

	// Output is the tool's output.
	Output string `json:"output"`

	// Error contains any error message.
	Error string `json:"error,omitempty"`

	// Duration is how long execution took (serialized as nanoseconds).
	Duration time.Duration `json:"duration"`

	// TokensUsed is the estimated tokens in the output.
	TokensUsed int `json:"tokens_used"`

	// Cached indicates if result came from cache.
	Cached bool `json:"cached"`

	// Truncated indicates if output was truncated.
	Truncated bool `json:"truncated"`
}

// DurationMs returns the duration in milliseconds.
//
// Outputs:
//
//	int64 - Duration in milliseconds
func (r *ToolResult) DurationMs() int64 {
	return r.Duration.Milliseconds()
}

// SafetyResult contains security analysis findings.
type SafetyResult struct {
	// Passed indicates if all safety checks passed.
	Passed bool `json:"passed"`

	// Issues contains security issues found.
	Issues []SecurityIssue `json:"issues"`

	// Blocked indicates if the operation was blocked.
	Blocked bool `json:"blocked"`

	// BlockReason explains why the operation was blocked.
	BlockReason string `json:"block_reason,omitempty"`
}

// SecurityIssue represents a single security finding.
type SecurityIssue struct {
	// ID is a unique identifier for this issue type.
	ID string `json:"id"`

	// Title is a short description.
	Title string `json:"title"`

	// Description provides details.
	Description string `json:"description"`

	// Severity is the issue severity (critical, high, medium, low).
	Severity string `json:"severity"`

	// Category is the issue category (injection, secrets, etc.).
	Category string `json:"category"`

	// FilePath is where the issue was found.
	FilePath string `json:"file_path,omitempty"`

	// Line is the line number.
	Line int `json:"line,omitempty"`

	// Code is the problematic code snippet.
	Code string `json:"code,omitempty"`
}

// AgentError contains error information for the ERROR state.
//
// AgentError implements the error interface, allowing it to be used
// as a standard Go error while providing structured error information.
type AgentError struct {
	// Code is a machine-readable error code.
	Code string `json:"code"`

	// Message is a human-readable error message.
	Message string `json:"message"`

	// Details contains additional error context.
	Details string `json:"details,omitempty"`

	// Recoverable indicates if the error might be resolved by retry.
	Recoverable bool `json:"recoverable"`

	// Step is the step where the error occurred.
	Step int `json:"step,omitempty"`
}

// Error implements the error interface.
//
// Outputs:
//
//	string - The error message, optionally with code prefix
func (e *AgentError) Error() string {
	if e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return e.Message
}

// Unwrap returns nil as AgentError does not wrap another error.
//
// Outputs:
//
//	error - Always nil
func (e *AgentError) Unwrap() error {
	return nil
}

// QueryIntent represents the classified intent of a user query.
type QueryIntent struct {
	// Type is the query type (explore, modify, explain, debug, refactor).
	Type string `json:"type"`

	// Scope is the query scope (file, function, package, project).
	Scope string `json:"scope"`

	// Targets are specific symbols/files mentioned.
	Targets []string `json:"targets"`

	// Confidence is how certain we are about the classification (0.0-1.0).
	Confidence float64 `json:"confidence"`
}

// ValidQueryTypes contains all valid query intent types.
var ValidQueryTypes = []string{"explore", "modify", "explain", "debug", "refactor"}

// ValidQueryScopes contains all valid query scopes.
var ValidQueryScopes = []string{"file", "function", "package", "project"}

// Validate checks that the QueryIntent has valid values.
//
// Outputs:
//
//	error - Non-nil if any field is invalid
func (q *QueryIntent) Validate() error {
	if q.Confidence < 0.0 || q.Confidence > 1.0 {
		return ErrInvalidSession // Reusing error for validation failures
	}

	// Type validation (empty is allowed for unknown intents)
	if q.Type != "" {
		valid := false
		for _, t := range ValidQueryTypes {
			if q.Type == t {
				valid = true
				break
			}
		}
		if !valid {
			return ErrInvalidSession
		}
	}

	// Scope validation (empty is allowed for unscoped queries)
	if q.Scope != "" {
		valid := false
		for _, s := range ValidQueryScopes {
			if q.Scope == s {
				valid = true
				break
			}
		}
		if !valid {
			return ErrInvalidSession
		}
	}

	return nil
}

// ProposedChange represents a code change the agent wants to make.
type ProposedChange struct {
	// FilePath is the file to modify.
	FilePath string `json:"file_path"`

	// ChangeType is create, modify, or delete.
	ChangeType string `json:"change_type"`

	// OldContent is the original content (for modify).
	OldContent string `json:"old_content,omitempty"`

	// NewContent is the new content.
	NewContent string `json:"new_content"`

	// Reason explains why this change is needed.
	Reason string `json:"reason"`
}

// SessionState contains the externally visible state of a session.
type SessionState struct {
	// ID is the session identifier.
	ID string `json:"id"`

	// ProjectRoot is the project being analyzed.
	ProjectRoot string `json:"project_root"`

	// GraphID is the Trace graph ID.
	GraphID string `json:"graph_id,omitempty"`

	// State is the current agent state.
	State AgentState `json:"state"`

	// StepCount is the number of steps executed.
	StepCount int `json:"step_count"`

	// TokensUsed is the total tokens consumed.
	TokensUsed int `json:"tokens_used"`

	// CreatedAt is when the session started (Unix milliseconds UTC).
	CreatedAt int64 `json:"created_at"`

	// LastActiveAt is when the session was last active (Unix milliseconds UTC).
	LastActiveAt int64 `json:"last_active_at"`

	// DegradedMode indicates if running with limited tools.
	DegradedMode bool `json:"degraded_mode"`
}

// SessionSummary is a brief summary of a session for listing/debug endpoints.
//
// Description:
//
//	Contains minimal session information for debug endpoints that need to
//	list all sessions without exposing full session internals.
type SessionSummary struct {
	// ID is the session identifier.
	ID string `json:"id"`

	// State is the current agent state.
	State AgentState `json:"state"`

	// TraceStepCount is the number of CRS trace steps recorded.
	TraceStepCount int `json:"trace_step_count"`

	// CircuitBreakerActive indicates if the circuit breaker has fired.
	CircuitBreakerActive bool `json:"circuit_breaker_active"`

	// CreatedAt is when the session started (Unix milliseconds UTC).
	CreatedAt int64 `json:"created_at"`

	// ToolCalls is the total number of tool calls made.
	ToolCalls int `json:"tool_calls"`
}

// =============================================================================
// Param Extractor Interface (IT-08b)
// =============================================================================

// ParamExtractor uses a fast LLM to extract tool parameters from queries.
//
// # Description
//
// Corrects regex-based parameter extraction errors using semantic understanding.
// When enabled, the regex result is passed as a hint and the LLM can confirm
// or correct it. Falls back to regex result on any error.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type ParamExtractor interface {
	// ExtractParams extracts corrected parameters for a tool.
	//
	// Inputs:
	//   - ctx: Context for cancellation/timeout.
	//   - query: The user's natural language query.
	//   - toolName: The name of the selected tool.
	//   - paramSchemas: Parameter definitions for the tool.
	//   - regexHint: The regex extractor's output (used as a hint).
	//
	// Outputs:
	//   - map[string]any: Corrected parameter values.
	//   - error: Non-nil if extraction fails (caller should use regexHint).
	ExtractParams(ctx context.Context, query string, toolName string,
		paramSchemas []ParamExtractorSchema, regexHint map[string]any) (map[string]any, error)

	// IsEnabled returns true if the extractor is enabled.
	IsEnabled() bool
}

// ParamExtractorSchema describes a tool parameter for the LLM prompt.
type ParamExtractorSchema struct {
	// Name is the parameter name.
	Name string

	// Type is the parameter type (string, integer, boolean).
	Type string

	// Required indicates if the parameter is required.
	Required bool

	// Default is the default value as a string.
	Default string

	// Description explains what the parameter is for.
	Description string
}

// =============================================================================
// Tool Router Interface
// =============================================================================

// ToolRouter selects the appropriate tool for a user query using a fast micro LLM.
//
// # Description
//
// ToolRouter uses a small, fast model (like granite4:micro-h) to pre-select
// tools before the main reasoning LLM processes the request. This allows tool
// selection to complete in ~50-100ms instead of the full LLM inference time.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type ToolRouter interface {
	// SelectTool chooses the best tool for the given query and context.
	//
	// Inputs:
	//   - ctx: Context for cancellation/timeout.
	//   - query: The user's question or request.
	//   - availableTools: Tools currently available for selection.
	//   - codeContext: Optional context about the codebase.
	//
	// Outputs:
	//   - *ToolRouterSelection: The selected tool with confidence.
	//   - error: Non-nil if routing fails (caller should fall back to main LLM).
	SelectTool(ctx context.Context, query string, availableTools []ToolRouterSpec, codeContext *ToolRouterCodeContext) (*ToolRouterSelection, error)

	// Model returns the model being used for routing.
	Model() string

	// Close releases any resources held by the router.
	Close() error
}

// ToolRouterSelection represents the router's decision.
type ToolRouterSelection struct {
	// Tool is the selected tool name.
	Tool string `json:"tool"`

	// Confidence is the router's confidence (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// ParamsHint contains optional parameter suggestions.
	ParamsHint map[string]string `json:"params_hint,omitempty"`

	// Reasoning explains why this tool was selected.
	Reasoning string `json:"reasoning,omitempty"`

	// Duration is how long the routing decision took.
	Duration time.Duration `json:"duration,omitempty"`
}

// ToolRouterSpec describes a tool for the router.
//
// CB-31e: Extended with UseWhen, AvoidWhen, and InsteadOf fields
// to provide structured guidance for tool selection.
type ToolRouterSpec struct {
	// Name is the tool name.
	Name string `json:"name"`

	// Description explains what the tool does.
	Description string `json:"description"`

	// BestFor contains keywords that should trigger this tool.
	// CB-31e: Now populated from WhenToUse.Keywords.
	BestFor []string `json:"best_for,omitempty"`

	// UseWhen describes when to use this tool.
	UseWhen string `json:"use_when,omitempty"`

	// AvoidWhen describes when NOT to use this tool.
	AvoidWhen string `json:"avoid_when,omitempty"`

	// InsteadOf lists tools this should replace.
	InsteadOf []ToolRouterSubstitution `json:"instead_of,omitempty"`

	// Params lists parameter names.
	Params []string `json:"params,omitempty"`

	// Category is the tool category.
	Category string `json:"category,omitempty"`
}

// ToolRouterSubstitution describes a tool substitution for the router.
type ToolRouterSubstitution struct {
	// Tool is the name of the tool to replace.
	Tool string `json:"tool"`

	// When describes when substitution applies.
	When string `json:"when"`
}

// ToolRouterCodeContext provides context about the codebase.
//
// # History-Aware Routing
//
// This struct is designed to leverage Mamba2's O(n) linear complexity and
// 1M token context window. By including tool history with summaries, the
// router can make informed decisions about what information is still needed
// rather than suggesting the same tools repeatedly.
type ToolRouterCodeContext struct {
	Language       string            `json:"language,omitempty"`
	Files          int               `json:"files,omitempty"`
	Symbols        int               `json:"symbols,omitempty"`
	CurrentFile    string            `json:"current_file,omitempty"`
	RecentTools    []string          `json:"recent_tools,omitempty"`
	PreviousErrors []ToolRouterError `json:"previous_errors,omitempty"`

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

// ToolRouterError captures a failed tool attempt for router feedback.
type ToolRouterError struct {
	Tool      string `json:"tool"`
	Error     string `json:"error"`
	Timestamp string `json:"timestamp,omitempty"`
}

// =============================================================================
// Model Manager Interface
// =============================================================================

// ModelManager coordinates multiple LLM models to prevent thrashing.
//
// # Description
//
// When using multiple models (e.g., tool router + main reasoner), ModelManager
// uses keep_alive to keep both models loaded in VRAM, preventing expensive
// model reload cycles.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type ModelManager interface {
	// WarmModel pre-loads a model into VRAM with the specified keep_alive.
	WarmModel(ctx context.Context, model string, keepAlive string) error

	// GetLoadedModels returns currently tracked models.
	GetLoadedModels() []ManagedModelInfo
}

// ManagedModelInfo contains information about a managed model.
type ManagedModelInfo struct {
	Name      string        `json:"name"`
	IsLoaded  bool          `json:"is_loaded"`
	KeepAlive string        `json:"keep_alive"`
	LastUsed  int64         `json:"last_used"` // Unix milliseconds UTC
	LoadTime  time.Duration `json:"load_time"`
}
