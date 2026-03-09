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

// execute_learning.go contains CRS/CDCL learning functions extracted from
// execute.go as part of CB-30c Phase 2 decomposition.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/integration"
	"github.com/google/uuid"
)

// -----------------------------------------------------------------------------
// CRS-04: Learning Activity Integration
// -----------------------------------------------------------------------------

// learnFromFailure triggers CDCL learning from a failure event.
//
// Description:
//
//	When a failure occurs (tool error, cycle, circuit breaker), this method
//	creates a learned clause that prevents the same failure pattern. The
//	clause is stored in CRS and checked before future decisions.
//
//	IMPORTANT: Only learns from hard signals. Soft signals (LLM feedback)
//	are NOT used for clause learning per Rule #2.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies containing session and CRS.
//	failure - The failure event to learn from.
//
// Outputs:
//
//	None. Errors are logged but do not interrupt execution.
//
// Thread Safety: Safe for concurrent use.
func (p *ExecutePhase) learnFromFailure(ctx context.Context, deps *Dependencies, failure crs.FailureEvent) {
	if deps.Session == nil {
		return
	}

	crsInstance := deps.Session.GetCRS()
	if crsInstance == nil {
		return
	}

	// Validate failure event
	if err := failure.Validate(); err != nil {
		slog.Warn("CRS-04: Invalid failure event",
			slog.String("error", err.Error()),
		)
		return
	}

	// Only learn from hard signals (Rule #2)
	if !failure.Source.IsHard() {
		slog.Debug("CRS-04: Skipping learning from soft signal",
			slog.String("failure_type", string(failure.FailureType)),
		)
		return
	}

	// Get step history for CDCL analysis
	steps := crsInstance.GetStepHistory(deps.Session.ID)
	failure.DecisionPath = steps

	// Generate learned clause from failure
	clause := p.generateClauseFromFailure(deps.Session.ID, failure)
	if clause == nil {
		return
	}

	// Add clause to CRS
	if err := crsInstance.AddClause(ctx, clause); err != nil {
		slog.Warn("CRS-04: Failed to add learned clause",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)
		return
	}

	slog.Info("CRS-04: Learned clause from failure",
		slog.String("session_id", deps.Session.ID),
		slog.String("failure_type", string(failure.FailureType)),
		slog.String("clause_id", clause.ID),
		slog.String("clause", clause.String()),
	)

	// Record metric
	integration.RecordClauseLearned(string(failure.FailureType))
}

// generateClauseFromFailure creates a CDCL clause from a failure event.
//
// Description:
//
//	Analyzes the failure and decision path to generate a clause that
//	prevents the same failure pattern. Different failure types produce
//	different clause structures:
//
//	- Cycle: (¬tool:A ∨ ¬prev_tool:B) - Block repeating A after B
//	- Circuit breaker: (¬tool:X ∨ ¬outcome:success) - Block repeated success tool
//	- Tool error: (¬tool:X ∨ ¬error:category) - Block tool when error occurs
//
// Inputs:
//
//	sessionID - The session ID for clause attribution.
//	failure - The failure event to analyze.
//
// Outputs:
//
//	*crs.Clause - The learned clause, or nil if no clause could be generated.
func (p *ExecutePhase) generateClauseFromFailure(sessionID string, failure crs.FailureEvent) *crs.Clause {
	var literals []crs.Literal

	switch failure.FailureType {
	case crs.FailureTypeCycleDetected:
		// Cycle: Block the tool that was repeated
		if failure.Tool != "" {
			literals = append(literals, crs.Literal{
				Variable: "tool:" + failure.Tool,
				Negated:  true,
			})
			// If we have history, add prev_tool to make clause more specific
			if len(failure.DecisionPath) > 1 {
				prevTool := failure.DecisionPath[len(failure.DecisionPath)-2].Tool
				if prevTool != "" {
					literals = append(literals, crs.Literal{
						Variable: "prev_tool:" + prevTool,
						Negated:  true,
					})
				}
			}
		}

	case crs.FailureTypeCircuitBreaker:
		// Circuit breaker: Block the tool that was called too many times
		if failure.Tool != "" {
			literals = append(literals, crs.Literal{
				Variable: "tool:" + failure.Tool,
				Negated:  true,
			})
			// Add outcome:success to make clause more specific
			// (only block repeating successful calls)
			literals = append(literals, crs.Literal{
				Variable: "outcome:success",
				Negated:  true,
			})
		}

	case crs.FailureTypeToolError:
		// Tool error: Block the tool with the specific error category
		if failure.Tool != "" && failure.ErrorCategory != "" {
			literals = append(literals, crs.Literal{
				Variable: "tool:" + failure.Tool,
				Negated:  true,
			})
			literals = append(literals, crs.Literal{
				Variable: "error:" + string(failure.ErrorCategory),
				Negated:  true,
			})
		}

	case crs.FailureTypeSafety:
		// Safety: Block the tool that triggered safety violation
		if failure.Tool != "" {
			literals = append(literals, crs.Literal{
				Variable: "tool:" + failure.Tool,
				Negated:  true,
			})
		}

	case crs.FailureTypeResolutionDemotion:
		// D3: Resolution demotion — block the demoted symbol within session.
		// The ErrorMessage contains the demoted symbol name in format
		// "LLM picked tierN [symbolName], overrode to tier0".
		if failure.ErrorMessage != "" {
			literals = append(literals, crs.Literal{
				Variable: "resolved_symbol:" + failure.ErrorMessage,
				Negated:  true,
			})
		}

	default:
		// Unknown failure type - cannot generate clause
		return nil
	}

	if len(literals) == 0 {
		return nil
	}

	// CR-3: Use UUID for collision-resistant clause IDs
	clauseID := fmt.Sprintf("clause_%s_%s_%s",
		string(failure.FailureType),
		failure.Tool,
		uuid.New().String()[:8], // Short UUID suffix for readability
	)

	return &crs.Clause{
		ID:          clauseID,
		Literals:    literals,
		Source:      crs.SignalSourceHard,
		FailureType: failure.FailureType,
		SessionID:   sessionID,
	}
}

// checkDecisionAllowed checks if a proposed tool selection violates learned clauses.
//
// Description:
//
//	Before making a tool selection decision, this method checks if the
//	proposed tool violates any learned clauses. If a violation is found,
//	the decision should be blocked and an alternative tool selected.
//
// Inputs:
//
//	ctx - Context for tracing. Must not be nil.
//	deps - Phase dependencies containing session and CRS.
//	tool - The proposed tool to check.
//
// Outputs:
//
//	bool - True if the decision is allowed.
//	string - Reason if the decision is blocked.
//
// Thread Safety: Safe for concurrent use.
func (p *ExecutePhase) checkDecisionAllowed(ctx context.Context, deps *Dependencies, tool string) (bool, string) {
	if deps.Session == nil {
		return true, ""
	}

	crsInstance := deps.Session.GetCRS()
	if crsInstance == nil {
		return true, ""
	}

	allowed, reason := crsInstance.CheckDecisionAllowed(deps.Session.ID, tool)
	if !allowed {
		slog.InfoContext(ctx, "CRS-04: Decision blocked by learned clause",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", tool),
			slog.String("reason", reason),
		)
		integration.RecordDecisionBlocked(tool)
	}

	return allowed, reason
}
