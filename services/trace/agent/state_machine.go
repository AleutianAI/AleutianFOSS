// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package agent

import (
	"fmt"
	"sort"
	"sync"
)

// StateMachine manages valid state transitions for the agent loop.
//
// The state machine enforces the following transition graph:
//
//	IDLE → INIT                  : User query received
//	INIT → PLAN                  : Graph initialized successfully
//	INIT → DEGRADED              : Trace unavailable
//	INIT → ERROR                 : Unrecoverable init failure
//	PLAN → EXECUTE               : Initial context assembled
//	PLAN → CLARIFY               : Query ambiguous, need user input
//	PLAN → ERROR                 : Planning failed
//	CLARIFY → PLAN               : User provided clarification
//	CLARIFY → ERROR              : Clarification failed
//	EXECUTE → EXECUTE            : Tool call completed, continue
//	EXECUTE → REFLECT            : Hit step limit or confidence threshold
//	EXECUTE → COMPLETE           : Direct answer, no reflection needed
//	EXECUTE → CLARIFY            : Unable to proceed without user input
//	EXECUTE → ERROR              : Execution failed
//	REFLECT → EXECUTE            : Self-correction, try different approach
//	REFLECT → COMPLETE           : Answer satisfactory
//	REFLECT → CLARIFY            : Need user input
//	REFLECT → ERROR              : Reflection failed
//	COMPLETE → PLAN              : Multi-turn follow-up question
//	DEGRADED → PLAN              : Limited planning without graph
//	DEGRADED → ERROR             : Degraded mode failed
//	* → ERROR                    : Any state can transition to ERROR
//
// Thread Safety:
//
//	StateMachine is safe for concurrent use.
type StateMachine struct {
	mu sync.RWMutex

	// transitions maps (from, to) pairs that are valid.
	transitions map[AgentState]map[AgentState]bool
}

// NewStateMachine creates a new state machine with all valid transitions.
//
// Outputs:
//
//	*StateMachine - Configured state machine
func NewStateMachine() *StateMachine {
	sm := &StateMachine{
		transitions: make(map[AgentState]map[AgentState]bool),
	}

	// Initialize all states with empty transition maps
	for _, state := range AllStates() {
		sm.transitions[state] = make(map[AgentState]bool)
	}

	// Define valid transitions
	sm.addTransition(StateIdle, StateInit)

	sm.addTransition(StateInit, StatePlan)
	sm.addTransition(StateInit, StateDegraded)
	sm.addTransition(StateInit, StateError)

	sm.addTransition(StatePlan, StateExecute)
	sm.addTransition(StatePlan, StateClarify)
	sm.addTransition(StatePlan, StateError)

	sm.addTransition(StateClarify, StatePlan)
	sm.addTransition(StateClarify, StateError)

	sm.addTransition(StateExecute, StateExecute)
	sm.addTransition(StateExecute, StateReflect)
	sm.addTransition(StateExecute, StateComplete)
	sm.addTransition(StateExecute, StateClarify) // cb_30a: Allow execute to request clarification
	sm.addTransition(StateExecute, StateError)

	sm.addTransition(StateReflect, StateExecute)
	sm.addTransition(StateReflect, StateComplete)
	sm.addTransition(StateReflect, StateClarify)
	sm.addTransition(StateReflect, StateError)

	// Multi-turn conversations: allow follow-up questions after completion
	sm.addTransition(StateComplete, StatePlan)

	sm.addTransition(StateDegraded, StatePlan)
	sm.addTransition(StateDegraded, StateError)

	return sm
}

// addTransition registers a valid transition.
func (sm *StateMachine) addTransition(from, to AgentState) {
	sm.transitions[from][to] = true
}

// CanTransition checks if a transition from one state to another is valid.
//
// Inputs:
//
//	from - Current state
//	to - Target state
//
// Outputs:
//
//	bool - True if the transition is valid
//
// Thread Safety: This method is safe for concurrent use.
func (sm *StateMachine) CanTransition(from, to AgentState) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if toMap, ok := sm.transitions[from]; ok {
		return toMap[to]
	}
	return false
}

// Transition attempts to transition a session from one state to another.
//
// Description:
//
//	Validates the transition and updates the session state if valid.
//	Returns an error if the transition is not allowed.
//
// Inputs:
//
//	session - The session to transition
//	to - Target state
//
// Outputs:
//
//	error - ErrInvalidTransition if transition not allowed
//
// Thread Safety: This method is safe for concurrent use.
func (sm *StateMachine) Transition(session *Session, to AgentState) error {
	from := session.GetState()

	if !sm.CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}

	session.SetState(to)
	return nil
}

// ValidTransitionsFrom returns all valid transitions from a given state.
//
// Description:
//
//	Returns a sorted slice of all valid target states for deterministic
//	iteration order. States are sorted alphabetically by string value.
//
// Inputs:
//
//	from - The source state
//
// Outputs:
//
//	[]AgentState - All valid target states, sorted alphabetically
//
// Thread Safety: This method is safe for concurrent use.
func (sm *StateMachine) ValidTransitionsFrom(from AgentState) []AgentState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []AgentState
	if toMap, ok := sm.transitions[from]; ok {
		for state, valid := range toMap {
			if valid {
				result = append(result, state)
			}
		}
	}

	// Sort for deterministic ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].String() < result[j].String()
	})

	return result
}

// TransitionReason provides a human-readable description of a transition.
//
// Inputs:
//
//	from - Source state
//	to - Target state
//
// Outputs:
//
//	string - Description of why this transition occurs
func (sm *StateMachine) TransitionReason(from, to AgentState) string {
	key := from.String() + "->" + to.String()

	reasons := map[string]string{
		"IDLE->INIT":        "User query received",
		"INIT->PLAN":        "Graph initialized successfully",
		"INIT->DEGRADED":    "Trace unavailable, entering degraded mode",
		"INIT->ERROR":       "Initialization failed unrecoverably",
		"PLAN->EXECUTE":     "Initial context assembled",
		"PLAN->CLARIFY":     "Query ambiguous, need user input",
		"PLAN->ERROR":       "Planning failed",
		"CLARIFY->PLAN":     "User provided clarification",
		"CLARIFY->ERROR":    "Clarification timeout or failure",
		"EXECUTE->EXECUTE":  "Tool call completed, continue",
		"EXECUTE->REFLECT":  "Hit step limit or confidence threshold",
		"EXECUTE->COMPLETE": "Direct answer, no reflection needed",
		"EXECUTE->CLARIFY":  "Unable to proceed without user input",
		"EXECUTE->ERROR":    "Execution failed unrecoverably",
		"REFLECT->EXECUTE":  "Self-correction, try different approach",
		"REFLECT->COMPLETE": "Answer satisfactory",
		"REFLECT->CLARIFY":  "Need user input after reflection",
		"REFLECT->ERROR":    "Reflection failed",
		"COMPLETE->PLAN":    "Multi-turn follow-up question",
		"DEGRADED->PLAN":    "Limited planning without graph",
		"DEGRADED->ERROR":   "Degraded mode failed",
	}

	if reason, ok := reasons[key]; ok {
		return reason
	}
	return "Unknown transition"
}

// TransitionEvent creates a history entry for a state transition.
//
// Inputs:
//
//	from - Source state
//	to - Target state
//
// Outputs:
//
//	HistoryEntry - Entry suitable for session history
func (sm *StateMachine) TransitionEvent(from, to AgentState) HistoryEntry {
	return HistoryEntry{
		Type:   "state_transition",
		State:  to,
		Input:  from.String(),
		Output: sm.TransitionReason(from, to),
	}
}

// DefaultStateMachine is the shared state machine instance.
var DefaultStateMachine = NewStateMachine()

// Transition is a convenience function using the default state machine.
func Transition(session *Session, to AgentState) error {
	return DefaultStateMachine.Transition(session, to)
}

// CanTransition is a convenience function using the default state machine.
func CanTransition(from, to AgentState) bool {
	return DefaultStateMachine.CanTransition(from, to)
}
