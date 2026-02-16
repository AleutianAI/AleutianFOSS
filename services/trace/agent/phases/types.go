// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package phases implements the individual phases of the agent state machine.
//
// Each phase handles a specific stage of agent execution:
//   - INIT: Initialize the session, load graph
//   - PLAN: Assemble initial context, prepare for execution
//   - EXECUTE: Main tool execution loop
//   - REFLECT: Evaluate progress and decide next steps
//   - CLARIFY: Request user input for ambiguous situations
//
// Thread Safety:
//
//	Phase implementations must be safe for concurrent use.
package phases

import (
	"context"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	agentcontext "github.com/AleutianAI/AleutianFOSS/services/trace/agent/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/events"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/grounding"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/integration"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/safety"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// Phase defines the interface for agent phases.
//
// Each phase represents a distinct stage in the agent's execution lifecycle.
// Phases are responsible for:
//   - Performing their designated tasks
//   - Determining the next state to transition to
//   - Emitting relevant events
//   - Handling errors appropriately
type Phase interface {
	// Name returns the phase name for logging and debugging.
	//
	// Outputs:
	//   string - The human-readable phase name.
	Name() string

	// Execute runs the phase logic.
	//
	// Description:
	//   Performs the phase-specific operations and returns the next state
	//   the agent should transition to.
	//
	// Inputs:
	//   ctx - Context for cancellation and timeout.
	//   deps - Dependencies required by the phase.
	//
	// Outputs:
	//   agent.AgentState - The next state to transition to.
	//   error - Non-nil if an unrecoverable error occurred.
	//
	// Thread Safety: Must be safe for concurrent use.
	Execute(ctx context.Context, deps *Dependencies) (agent.AgentState, error)
}

// Type aliases for dependency interfaces.
// These allow the agent package to reference types without importing sub-packages.
type (
	// LLMClient is the interface for LLM completion.
	LLMClient = llm.Client

	// ContextManager is the interface for context management.
	ContextManager = agentcontext.Manager

	// ToolRegistry is the interface for tool registration.
	ToolRegistry = tools.Registry

	// ToolExecutor is the interface for tool execution.
	ToolExecutor = tools.Executor

	// SafetyGate is the interface for safety checks.
	SafetyGate = safety.Gate

	// EventEmitter is the interface for event emission.
	EventEmitter = events.Emitter

	// ResponseGrounder is the interface for grounding validation.
	ResponseGrounder = grounding.Grounder
)

// Dependencies contains all dependencies needed by phases.
//
// This struct provides phases with access to the session state,
// external services, and configuration without coupling phases
// to specific implementations.
type Dependencies struct {
	// Session is the current agent session.
	Session *agent.Session

	// Query is the user's original query.
	Query string

	// Context is the assembled context (nil until PLAN phase completes).
	Context *agent.AssembledContext

	// ContextManager handles context assembly and updates.
	ContextManager *ContextManager

	// LLMClient sends requests to the language model.
	LLMClient LLMClient

	// ToolRegistry provides access to available tools.
	ToolRegistry *ToolRegistry

	// ToolExecutor executes tool invocations.
	ToolExecutor *ToolExecutor

	// SafetyGate validates proposed changes.
	SafetyGate SafetyGate

	// EventEmitter broadcasts agent events.
	EventEmitter *EventEmitter

	// GraphProvider initializes and provides the code graph.
	GraphProvider GraphProvider

	// ResponseGrounder validates LLM responses against project reality.
	// Optional - if nil, grounding validation is skipped.
	ResponseGrounder ResponseGrounder

	// AnchoredSynthesisBuilder builds tool-anchored synthesis prompts.
	// Optional - if nil, basic synthesis prompt is used.
	AnchoredSynthesisBuilder grounding.AnchoredSynthesisBuilder

	// DirtyTracker tracks files modified by tools for graph refresh.
	// Optional - if nil, graph freshness tracking is disabled.
	DirtyTracker *graph.DirtyTracker

	// GraphRefresher handles incremental graph updates.
	// Optional - if nil, graph refresh is disabled.
	GraphRefresher *graph.Refresher

	// Coordinator orchestrates MCTS activities in response to agent events.
	// Optional - if nil, MCTS activity coordination is disabled.
	// Use HandleEvent to emit events and trigger appropriate activities.
	Coordinator *integration.Coordinator

	// CRS is the Code Reasoning State for MCTS integration.
	// Optional - if nil, CRS-based reasoning is disabled.
	// Use for session restore, checkpoint management, and proof tracking.
	CRS crs.CRS

	// PersistenceManager handles CRS checkpoint storage.
	// Optional - if nil, CRS persistence is disabled.
	// GR-33/GR-36: Required for session restore and checkpoint save.
	PersistenceManager *crs.PersistenceManager

	// BadgerJournal stores CRS deltas for replay.
	// Optional - if nil, delta journaling is disabled.
	// GR-33/GR-36: Required for session restore to replay deltas.
	BadgerJournal *crs.BadgerJournal

	// GraphAnalytics provides graph analysis operations (dominators, communities, etc).
	// Required for symbol resolution in parameter extraction (CB-31d).
	// Optional - if nil, parameter extraction falls back to raw symbol names.
	GraphAnalytics *graph.GraphAnalytics

	// SymbolIndex provides fast symbol lookup by ID, name, or fuzzy search.
	// Required for symbol resolution in parameter extraction (CB-31d).
	// Optional - if nil, parameter extraction falls back to raw symbol names.
	SymbolIndex *index.SymbolIndex
}

// GraphProvider initializes and provides access to the code graph.
type GraphProvider interface {
	// Initialize sets up the code graph for a project.
	//
	// Inputs:
	//   ctx - Context for cancellation.
	//   projectRoot - Path to the project root.
	//
	// Outputs:
	//   string - The graph ID.
	//   error - Non-nil if initialization fails.
	Initialize(ctx context.Context, projectRoot string) (string, error)

	// IsAvailable checks if the graph service is available.
	//
	// Outputs:
	//   bool - True if the service is available.
	IsAvailable() bool
}

// PhaseResult contains the result of phase execution.
type PhaseResult struct {
	// NextState is the state to transition to.
	NextState agent.AgentState

	// UpdatedContext is the context after phase execution.
	UpdatedContext *agent.AssembledContext

	// Response is any response generated by the phase.
	Response string

	// ToolCalls are tool invocations requested by the LLM.
	ToolCalls []agent.ToolInvocation

	// ClarificationNeeded indicates if user input is required.
	ClarificationNeeded bool

	// ClarificationPrompt is the prompt to show the user.
	ClarificationPrompt string

	// Error contains any error that occurred.
	Error error
}

// ReflectionDecision represents the outcome of reflection.
type ReflectionDecision string

const (
	// DecisionContinue indicates execution should continue.
	DecisionContinue ReflectionDecision = "continue"

	// DecisionComplete indicates the task is done.
	DecisionComplete ReflectionDecision = "complete"

	// DecisionClarify indicates user input is needed.
	DecisionClarify ReflectionDecision = "clarify"
)

// ReflectionInput contains data for the reflection phase.
type ReflectionInput struct {
	// StepsCompleted is the number of steps executed so far.
	StepsCompleted int

	// TokensUsed is the total tokens consumed.
	TokensUsed int

	// ToolsInvoked is the number of tool invocations.
	ToolsInvoked int

	// LastResponse is the most recent LLM response.
	LastResponse string

	// RecentResults are the recent tool results.
	RecentResults []agent.ToolResult
}

// ReflectionOutput contains the reflection decision.
type ReflectionOutput struct {
	// Decision is what to do next.
	Decision ReflectionDecision

	// Reason explains the decision.
	Reason string

	// ClarificationPrompt is set if Decision is DecisionClarify.
	ClarificationPrompt string
}
