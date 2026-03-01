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
	"context"
	"fmt"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/phases"
)

// PhaseAdapter adapts a phases.Phase to agent.PhaseExecutor.
//
// Description:
//
//	PhaseAdapter wraps a phases.Phase to implement the agent.PhaseExecutor
//	interface. It handles the conversion of the untyped deps parameter
//	to *phases.Dependencies.
//
// Thread Safety: PhaseAdapter is safe for concurrent use (delegates to Phase).
type PhaseAdapter struct {
	phase phases.Phase
}

// NewPhaseAdapter creates a new phase adapter.
//
// Inputs:
//
//	phase - The phase to wrap.
//
// Outputs:
//
//	*PhaseAdapter - The adapter.
func NewPhaseAdapter(phase phases.Phase) *PhaseAdapter {
	return &PhaseAdapter{phase: phase}
}

// Execute implements agent.PhaseExecutor.
//
// Description:
//
//	Executes the wrapped phase. Converts the deps parameter to
//	*phases.Dependencies. Returns an error if deps is nil or
//	wrong type.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	deps - Dependencies (must be *phases.Dependencies).
//
// Outputs:
//
//	agent.AgentState - The next state.
//	error - Non-nil if execution failed or deps is invalid.
//
// Thread Safety: This method is safe for concurrent use.
func (a *PhaseAdapter) Execute(ctx context.Context, deps any) (agent.AgentState, error) {
	if deps == nil {
		return agent.StateError, fmt.Errorf("phase dependencies are nil")
	}

	phaseDeps, ok := deps.(*phases.Dependencies)
	if !ok {
		return agent.StateError, fmt.Errorf("invalid phase dependencies type: expected *phases.Dependencies, got %T", deps)
	}

	return a.phase.Execute(ctx, phaseDeps)
}

// Name implements agent.PhaseExecutor.
//
// Outputs:
//
//	string - The phase name.
func (a *PhaseAdapter) Name() string {
	return a.phase.Name()
}

// Ensure PhaseAdapter implements agent.PhaseExecutor.
var _ agent.PhaseExecutor = (*PhaseAdapter)(nil)
