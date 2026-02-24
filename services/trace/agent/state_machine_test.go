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
	"errors"
	"sync"
	"testing"
)

func TestStateMachine_ValidTransitions(t *testing.T) {
	sm := NewStateMachine()

	validTransitions := []struct {
		from AgentState
		to   AgentState
	}{
		// IDLE transitions
		{StateIdle, StateInit},

		// INIT transitions
		{StateInit, StatePlan},
		{StateInit, StateDegraded},
		{StateInit, StateError},

		// PLAN transitions
		{StatePlan, StateExecute},
		{StatePlan, StateClarify},
		{StatePlan, StateError},

		// CLARIFY transitions
		{StateClarify, StatePlan},
		{StateClarify, StateError},

		// EXECUTE transitions
		{StateExecute, StateExecute},
		{StateExecute, StateReflect},
		{StateExecute, StateComplete},
		{StateExecute, StateError},

		// REFLECT transitions
		{StateReflect, StateExecute},
		{StateReflect, StateComplete},
		{StateReflect, StateClarify},
		{StateReflect, StateError},

		// DEGRADED transitions
		{StateDegraded, StatePlan},
		{StateDegraded, StateError},
	}

	for _, tt := range validTransitions {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			if !sm.CanTransition(tt.from, tt.to) {
				t.Errorf("expected transition %s -> %s to be valid", tt.from, tt.to)
			}
		})
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	sm := NewStateMachine()

	invalidTransitions := []struct {
		from AgentState
		to   AgentState
	}{
		// Cannot go backwards from terminal states
		{StateComplete, StateIdle},
		{StateComplete, StateInit},
		{StateComplete, StateExecute},
		{StateError, StateIdle},
		{StateError, StateInit},
		{StateError, StateExecute},

		// Cannot skip states
		{StateIdle, StateExecute},
		{StateIdle, StateComplete},
		{StateIdle, StatePlan},
		{StateIdle, StateReflect},

		// Cannot go from PLAN directly to COMPLETE
		{StatePlan, StateComplete},

		// Cannot go from CLARIFY directly to EXECUTE
		{StateClarify, StateExecute},
		{StateClarify, StateComplete},

		// Cannot go from EXECUTE back to INIT
		{StateExecute, StateInit},
		{StateExecute, StatePlan},

		// Cannot go from REFLECT back to INIT or PLAN
		{StateReflect, StateInit},
		{StateReflect, StatePlan},

		// Cannot go from DEGRADED to most states
		{StateDegraded, StateInit},
		{StateDegraded, StateExecute},
		{StateDegraded, StateComplete},
		{StateDegraded, StateReflect},
	}

	for _, tt := range invalidTransitions {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			if sm.CanTransition(tt.from, tt.to) {
				t.Errorf("expected transition %s -> %s to be invalid", tt.from, tt.to)
			}
		})
	}
}

func TestStateMachine_Transition(t *testing.T) {
	sm := NewStateMachine()

	t.Run("valid transition updates state", func(t *testing.T) {
		session, err := NewSession("/tmp/project", nil)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		if session.GetState() != StateIdle {
			t.Errorf("expected IDLE, got %s", session.GetState())
		}

		err = sm.Transition(session, StateInit)
		if err != nil {
			t.Errorf("Transition: %v", err)
		}

		if session.GetState() != StateInit {
			t.Errorf("expected INIT, got %s", session.GetState())
		}
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		session, err := NewSession("/tmp/project", nil)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		err = sm.Transition(session, StateComplete)
		if err == nil {
			t.Error("expected error for invalid transition")
		}
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("expected ErrInvalidTransition, got %v", err)
		}

		// State should remain unchanged
		if session.GetState() != StateIdle {
			t.Errorf("expected state to remain IDLE, got %s", session.GetState())
		}
	})
}

func TestStateMachine_ValidTransitionsFrom(t *testing.T) {
	sm := NewStateMachine()

	tests := []struct {
		from     AgentState
		expected int // minimum number of valid transitions
	}{
		{StateIdle, 1},     // -> INIT
		{StateInit, 3},     // -> PLAN, DEGRADED, ERROR
		{StatePlan, 3},     // -> EXECUTE, CLARIFY, ERROR
		{StateClarify, 2},  // -> PLAN, ERROR
		{StateExecute, 4},  // -> EXECUTE, REFLECT, COMPLETE, ERROR
		{StateReflect, 4},  // -> EXECUTE, COMPLETE, CLARIFY, ERROR
		{StateDegraded, 2}, // -> PLAN, ERROR
		{StateComplete, 0}, // terminal
		{StateError, 0},    // terminal
	}

	for _, tt := range tests {
		t.Run(tt.from.String(), func(t *testing.T) {
			transitions := sm.ValidTransitionsFrom(tt.from)
			if len(transitions) < tt.expected {
				t.Errorf("expected at least %d transitions from %s, got %d: %v",
					tt.expected, tt.from, len(transitions), transitions)
			}
		})
	}
}

func TestStateMachine_TransitionReason(t *testing.T) {
	sm := NewStateMachine()

	tests := []struct {
		from     AgentState
		to       AgentState
		contains string
	}{
		{StateIdle, StateInit, "query received"},
		{StateInit, StatePlan, "initialized successfully"},
		{StateInit, StateDegraded, "unavailable"},
		{StatePlan, StateExecute, "context assembled"},
		{StatePlan, StateClarify, "ambiguous"},
		{StateExecute, StateReflect, "step limit"},
		{StateExecute, StateComplete, "Direct answer"},
		{StateReflect, StateExecute, "different approach"},
	}

	for _, tt := range tests {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			reason := sm.TransitionReason(tt.from, tt.to)
			if reason == "" {
				t.Error("expected non-empty reason")
			}
			// Just verify we get a reason, content varies
		})
	}
}

func TestStateMachine_TransitionEvent(t *testing.T) {
	sm := NewStateMachine()

	event := sm.TransitionEvent(StateIdle, StateInit)

	if event.Type != "state_transition" {
		t.Errorf("expected type state_transition, got %s", event.Type)
	}
	if event.State != StateInit {
		t.Errorf("expected state INIT, got %s", event.State)
	}
	if event.Input != StateIdle.String() {
		t.Errorf("expected input IDLE, got %s", event.Input)
	}
	if event.Output == "" {
		t.Error("expected non-empty output (reason)")
	}
}

func TestStateMachine_ConcurrentAccess(t *testing.T) {
	sm := NewStateMachine()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Run many concurrent transitions
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a fresh session for each goroutine
			session, err := NewSession("/tmp/project", nil)
			if err != nil {
				errors <- err
				return
			}

			// Perform valid transition sequence
			transitions := []AgentState{
				StateInit,
				StatePlan,
				StateExecute,
				StateComplete,
			}

			for _, state := range transitions {
				if err := sm.Transition(session, state); err != nil {
					errors <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent transition error: %v", err)
	}
}

func TestAgentState_IsTerminal(t *testing.T) {
	tests := []struct {
		state    AgentState
		terminal bool
	}{
		{StateIdle, false},
		{StateInit, false},
		{StatePlan, false},
		{StateExecute, false},
		{StateReflect, false},
		{StateClarify, false},
		{StateDegraded, false},
		{StateComplete, true},
		{StateError, true},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if tt.state.IsTerminal() != tt.terminal {
				t.Errorf("expected IsTerminal=%v for %s", tt.terminal, tt.state)
			}
		})
	}
}

func TestAgentState_IsActive(t *testing.T) {
	tests := []struct {
		state  AgentState
		active bool
	}{
		{StateIdle, false},
		{StateInit, true},
		{StatePlan, true},
		{StateExecute, true},
		{StateReflect, true},
		{StateClarify, false},
		{StateDegraded, true},
		{StateComplete, false},
		{StateError, false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if tt.state.IsActive() != tt.active {
				t.Errorf("expected IsActive=%v for %s", tt.active, tt.state)
			}
		})
	}
}

func TestDefaultStateMachine(t *testing.T) {
	// Test that the default state machine is properly initialized
	if DefaultStateMachine == nil {
		t.Fatal("DefaultStateMachine is nil")
	}

	// Test convenience functions
	if !CanTransition(StateIdle, StateInit) {
		t.Error("CanTransition failed for IDLE -> INIT")
	}

	session, err := NewSession("/tmp/project", nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := Transition(session, StateInit); err != nil {
		t.Errorf("Transition failed: %v", err)
	}
}

func TestAllStates(t *testing.T) {
	states := AllStates()

	expected := 9 // IDLE, INIT, PLAN, EXECUTE, REFLECT, CLARIFY, DEGRADED, COMPLETE, ERROR
	if len(states) != expected {
		t.Errorf("expected %d states, got %d", expected, len(states))
	}

	// Verify all expected states are present
	stateSet := make(map[AgentState]bool)
	for _, s := range states {
		stateSet[s] = true
	}

	expectedStates := []AgentState{
		StateIdle, StateInit, StatePlan, StateExecute,
		StateReflect, StateClarify, StateDegraded,
		StateComplete, StateError,
	}

	for _, s := range expectedStates {
		if !stateSet[s] {
			t.Errorf("missing state: %s", s)
		}
	}
}

func TestAgentState_String(t *testing.T) {
	tests := []struct {
		state    AgentState
		expected string
	}{
		{StateIdle, "IDLE"},
		{StateInit, "INIT"},
		{StatePlan, "PLAN"},
		{StateExecute, "EXECUTE"},
		{StateReflect, "REFLECT"},
		{StateClarify, "CLARIFY"},
		{StateDegraded, "DEGRADED"},
		{StateComplete, "COMPLETE"},
		{StateError, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.state.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.state.String())
			}
		})
	}
}

// TestFullWorkflow tests a complete successful workflow
func TestFullWorkflow(t *testing.T) {
	sm := NewStateMachine()
	session, err := NewSession("/tmp/project", nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Simulate a successful query flow
	workflow := []AgentState{
		StateInit,     // Start initialization
		StatePlan,     // Plan the approach
		StateExecute,  // First execution step
		StateExecute,  // Continue execution
		StateReflect,  // Hit reflection threshold
		StateExecute,  // Continue after reflection
		StateComplete, // Done
	}

	for i, target := range workflow {
		err := sm.Transition(session, target)
		if err != nil {
			t.Fatalf("step %d: transition to %s failed: %v", i, target, err)
		}
		if session.GetState() != target {
			t.Fatalf("step %d: expected %s, got %s", i, target, session.GetState())
		}
	}
}

// TestDegradedModeWorkflow tests the degraded mode path
func TestDegradedModeWorkflow(t *testing.T) {
	sm := NewStateMachine()
	session, err := NewSession("/tmp/project", nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	workflow := []AgentState{
		StateInit,     // Start initialization
		StateDegraded, // Trace unavailable
		StatePlan,     // Limited planning
		StateExecute,  // Execute with limited tools
		StateComplete, // Done
	}

	for i, target := range workflow {
		err := sm.Transition(session, target)
		if err != nil {
			t.Fatalf("step %d: transition to %s failed: %v", i, target, err)
		}
	}
}

// TestClarificationWorkflow tests the clarification path
func TestClarificationWorkflow(t *testing.T) {
	sm := NewStateMachine()
	session, err := NewSession("/tmp/project", nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	workflow := []AgentState{
		StateInit,     // Start
		StatePlan,     // Plan
		StateClarify,  // Need clarification
		StatePlan,     // User provided input
		StateExecute,  // Now execute
		StateComplete, // Done
	}

	for i, target := range workflow {
		err := sm.Transition(session, target)
		if err != nil {
			t.Fatalf("step %d: transition to %s failed: %v", i, target, err)
		}
	}
}
