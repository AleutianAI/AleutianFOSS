// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/events"
)

// InitPhase handles session initialization.
//
// This phase is responsible for:
//   - Initializing the code graph for the project
//   - Setting up the session for execution
//   - Handling degraded mode when services are unavailable
//
// Thread Safety: InitPhase is safe for concurrent use.
type InitPhase struct{}

// NewInitPhase creates a new initialization phase.
//
// Outputs:
//
//	*InitPhase - The configured phase.
func NewInitPhase() *InitPhase {
	return &InitPhase{}
}

// Name implements Phase.
//
// Outputs:
//
//	string - "init"
func (p *InitPhase) Name() string {
	return "init"
}

// Execute implements Phase.
//
// Description:
//
//	Initializes the code graph for the project. If the graph service
//	is unavailable, transitions to DEGRADED mode instead of failing.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	deps - Phase dependencies including the graph provider.
//
// Outputs:
//
//	agent.AgentState - PLAN on success, DEGRADED if service unavailable, ERROR on failure.
//	error - Non-nil only for unrecoverable errors.
//
// Thread Safety: This method is safe for concurrent use.
func (p *InitPhase) Execute(ctx context.Context, deps *Dependencies) (agent.AgentState, error) {
	if deps == nil {
		return agent.StateError, fmt.Errorf("dependencies are nil")
	}

	if deps.Session == nil {
		return agent.StateError, fmt.Errorf("session is nil")
	}

	// Emit session start event
	p.emitSessionStart(deps)

	// Check if graph provider is available
	if deps.GraphProvider == nil {
		return p.handleDegradedMode(deps, "graph provider not configured")
	}

	if !deps.GraphProvider.IsAvailable() {
		return p.handleDegradedMode(deps, "graph service unavailable")
	}

	// Initialize the graph
	graphID, err := p.initializeGraph(ctx, deps)
	if err != nil {
		return p.handleGraphInitError(deps, err)
	}

	// Update session with graph ID
	deps.Session.SetGraphID(graphID)

	// Emit state transition event
	p.emitStateTransition(deps, agent.StateInit, agent.StatePlan, "graph initialized")

	return agent.StatePlan, nil
}

// initializeGraph initializes the code graph for the project.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//
// Outputs:
//
//	string - The graph ID.
//	error - Non-nil if initialization fails.
func (p *InitPhase) initializeGraph(ctx context.Context, deps *Dependencies) (string, error) {
	projectRoot := deps.Session.GetProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("project root is not set")
	}

	graphID, err := deps.GraphProvider.Initialize(ctx, projectRoot)
	if err != nil {
		return "", fmt.Errorf("initialize graph for %s: %w", projectRoot, err)
	}

	return graphID, nil
}

// handleDegradedMode transitions to degraded mode.
//
// Inputs:
//
//	deps - Phase dependencies.
//	reason - The reason for entering degraded mode.
//
// Outputs:
//
//	agent.AgentState - DEGRADED.
//	error - Always nil (degraded mode is not an error).
func (p *InitPhase) handleDegradedMode(deps *Dependencies, reason string) (agent.AgentState, error) {
	slog.Warn("Entering degraded mode",
		slog.String("session_id", deps.Session.ID),
		slog.String("reason", reason),
	)
	p.emitStateTransition(deps, agent.StateInit, agent.StateDegraded, reason)

	return agent.StateDegraded, nil
}

// handleGraphInitError handles graph initialization errors.
//
// Inputs:
//
//	deps - Phase dependencies.
//	err - The initialization error.
//
// Outputs:
//
//	agent.AgentState - DEGRADED or ERROR depending on error type.
//	error - The original error if it's unrecoverable.
func (p *InitPhase) handleGraphInitError(deps *Dependencies, err error) (agent.AgentState, error) {
	// For most graph init errors, enter degraded mode rather than failing
	// The agent can still function with limited capabilities
	slog.Warn("Graph initialization failed, entering degraded mode",
		slog.String("session_id", deps.Session.ID),
		slog.String("error", err.Error()),
	)
	p.emitError(deps, err, true)
	p.emitStateTransition(deps, agent.StateInit, agent.StateDegraded, "graph initialization failed")

	return agent.StateDegraded, nil
}

// emitSessionStart emits a session start event.
//
// Inputs:
//
//	deps - Phase dependencies.
func (p *InitPhase) emitSessionStart(deps *Dependencies) {
	if deps.EventEmitter == nil {
		return
	}

	deps.EventEmitter.Emit(events.TypeSessionStart, &events.SessionStartData{
		Query:       deps.Query,
		ProjectRoot: deps.Session.GetProjectRoot(),
	})
}

// emitStateTransition emits a state transition event.
//
// Inputs:
//
//	deps - Phase dependencies.
//	from - The previous state.
//	to - The new state.
//	reason - The reason for the transition.
func (p *InitPhase) emitStateTransition(deps *Dependencies, from, to agent.AgentState, reason string) {
	if deps.EventEmitter == nil {
		return
	}

	deps.EventEmitter.Emit(events.TypeStateTransition, &events.StateTransitionData{
		FromState: from,
		ToState:   to,
		Reason:    reason,
	})
}

// emitError emits an error event.
//
// Inputs:
//
//	deps - Phase dependencies.
//	err - The error that occurred.
//	recoverable - Whether the error is recoverable.
func (p *InitPhase) emitError(deps *Dependencies, err error, recoverable bool) {
	if deps.EventEmitter == nil {
		return
	}

	deps.EventEmitter.Emit(events.TypeError, &events.ErrorData{
		Error:       err.Error(),
		Recoverable: recoverable,
	})
}
