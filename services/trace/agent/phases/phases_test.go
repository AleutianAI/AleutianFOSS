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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	agentcontext "github.com/AleutianAI/AleutianFOSS/services/trace/agent/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/events"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/safety"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// MockGraphProvider is a mock implementation of GraphProvider.
type MockGraphProvider struct {
	available bool
	graphID   string
	initError error
	initCalls int
}

func NewMockGraphProvider() *MockGraphProvider {
	return &MockGraphProvider{
		available: true,
		graphID:   "mock-graph-123",
	}
}

func (m *MockGraphProvider) Initialize(ctx context.Context, projectRoot string) (string, error) {
	m.initCalls++
	if m.initError != nil {
		return "", m.initError
	}
	return m.graphID, nil
}

func (m *MockGraphProvider) IsAvailable() bool {
	return m.available
}

// Helper to create test dependencies
func createTestDependencies() *Dependencies {
	session, _ := agent.NewSession("/test/project", nil)
	// GR-44 Rev 2: Disable router by default for tests that don't need it.
	// Tests requiring router must initialize a mock router explicitly.
	session.Config.ToolRouterEnabled = false
	return &Dependencies{
		Session:       session,
		Query:         "What does this function do?",
		EventEmitter:  events.NewEmitter(),
		GraphProvider: NewMockGraphProvider(),
	}
}

// TestInitPhase tests
func TestInitPhase_Name(t *testing.T) {
	phase := NewInitPhase()
	if phase.Name() != "init" {
		t.Errorf("Name() = %s, want init", phase.Name())
	}
}

func TestInitPhase_Execute_Success(t *testing.T) {
	phase := NewInitPhase()
	deps := createTestDependencies()

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StatePlan {
		t.Errorf("nextState = %s, want PLAN", nextState)
	}
	if deps.Session.GetGraphID() != "mock-graph-123" {
		t.Errorf("GraphID = %s, want mock-graph-123", deps.Session.GetGraphID())
	}
}

func TestInitPhase_Execute_NilDependencies(t *testing.T) {
	phase := NewInitPhase()

	nextState, err := phase.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil dependencies")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestInitPhase_Execute_NilSession(t *testing.T) {
	phase := NewInitPhase()
	deps := createTestDependencies()
	deps.Session = nil

	nextState, err := phase.Execute(context.Background(), deps)
	if err == nil {
		t.Error("expected error for nil session")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestInitPhase_Execute_GraphUnavailable(t *testing.T) {
	phase := NewInitPhase()
	deps := createTestDependencies()
	deps.GraphProvider.(*MockGraphProvider).available = false

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextState != agent.StateDegraded {
		t.Errorf("nextState = %s, want DEGRADED", nextState)
	}
}

func TestInitPhase_Execute_GraphInitError(t *testing.T) {
	phase := NewInitPhase()
	deps := createTestDependencies()
	deps.GraphProvider.(*MockGraphProvider).initError = errors.New("init failed")

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextState != agent.StateDegraded {
		t.Errorf("nextState = %s, want DEGRADED", nextState)
	}
}

func TestInitPhase_Execute_NoGraphProvider(t *testing.T) {
	phase := NewInitPhase()
	deps := createTestDependencies()
	deps.GraphProvider = nil

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextState != agent.StateDegraded {
		t.Errorf("nextState = %s, want DEGRADED", nextState)
	}
}

// TestPlanPhase tests
func TestPlanPhase_Name(t *testing.T) {
	phase := NewPlanPhase()
	if phase.Name() != "plan" {
		t.Errorf("Name() = %s, want plan", phase.Name())
	}
}

func TestPlanPhase_Execute_AmbiguousQuery(t *testing.T) {
	phase := NewPlanPhase()
	deps := createTestDependencies()
	deps.Query = "help"                           // Too short, ambiguous
	deps.ContextManager = &agentcontext.Manager{} // Need context manager for validation

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateClarify {
		t.Errorf("nextState = %s, want CLARIFY", nextState)
	}
}

func TestPlanPhase_Execute_NilDependencies(t *testing.T) {
	phase := NewPlanPhase()

	nextState, err := phase.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil dependencies")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestPlanPhase_Execute_EmptyQuery(t *testing.T) {
	phase := NewPlanPhase()
	deps := createTestDependencies()
	deps.Query = ""

	nextState, err := phase.Execute(context.Background(), deps)
	if err == nil {
		t.Error("expected error for empty query")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestPlanPhase_IsQueryAmbiguous(t *testing.T) {
	phase := NewPlanPhase()

	tests := []struct {
		query    string
		expected bool
	}{
		{"help", true},                 // Too short
		{"something about code", true}, // Contains ambiguous phrase
		{"What does the calculateTotal function do?", false}, // Good query
		{"Explain the error handling in auth.go", false},     // Good query
		{"", true}, // Empty
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := phase.isQueryAmbiguous(tt.query)
			if result != tt.expected {
				t.Errorf("isQueryAmbiguous(%q) = %v, want %v", tt.query, result, tt.expected)
			}
		})
	}
}

// TestExecutePhase tests
func TestExecutePhase_Name(t *testing.T) {
	phase := NewExecutePhase()
	if phase.Name() != "execute" {
		t.Errorf("Name() = %s, want execute", phase.Name())
	}
}

func TestExecutePhase_Execute_NilDependencies(t *testing.T) {
	phase := NewExecutePhase()

	nextState, err := phase.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil dependencies")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestExecutePhase_Execute_NilContext(t *testing.T) {
	phase := NewExecutePhase()
	deps := createTestDependencies()
	deps.LLMClient = llm.NewMockClient()
	deps.ToolExecutor = tools.NewExecutor(tools.NewRegistry(), nil)

	nextState, err := phase.Execute(context.Background(), deps)
	if err == nil {
		t.Error("expected error for nil context")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestExecutePhase_Execute_Complete(t *testing.T) {
	phase := NewExecutePhase()
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)

	// Mock LLM that returns a final response (no tool calls)
	mockLLM := llm.NewMockClient()
	mockLLM.QueueFinalResponse("Here is the answer to your question.")
	deps.LLMClient = mockLLM

	// Need a context manager for updating context
	deps.ContextManager = &agentcontext.Manager{}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}
}

func TestExecutePhase_Execute_WithToolCalls(t *testing.T) {
	phase := NewExecutePhase(WithReflectionThreshold(100)) // High threshold to avoid reflection
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)
	deps.SafetyGate = safety.NewMockGate()

	// Register a mock tool
	mockTool := tools.NewMockTool("test_tool", tools.CategoryExploration)
	mockTool.ExecuteFunc = func(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
		return &tools.Result{
			Success:    true,
			OutputText: "Tool executed successfully",
		}, nil
	}
	deps.ToolRegistry.Register(mockTool)

	// Mock LLM that requests a tool call
	mockLLM := llm.NewMockClient()
	mockLLM.QueueToolCall("test_tool", map[string]any{"param": "value"})
	deps.LLMClient = mockLLM

	// Need a context manager
	deps.ContextManager = &agentcontext.Manager{}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Should continue execution after tool calls
	if nextState != agent.StateExecute {
		t.Errorf("nextState = %s, want EXECUTE", nextState)
	}
}

// TestReflectPhase tests
func TestReflectPhase_Name(t *testing.T) {
	phase := NewReflectPhase()
	if phase.Name() != "reflect" {
		t.Errorf("Name() = %s, want reflect", phase.Name())
	}
}

func TestReflectPhase_Execute_NilDependencies(t *testing.T) {
	phase := NewReflectPhase()

	nextState, err := phase.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil dependencies")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestReflectPhase_Execute_MaxStepsExceeded(t *testing.T) {
	phase := NewReflectPhase(WithMaxSteps(10))
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		TotalTokens: 1000,
	}

	// Simulate many steps completed
	for i := 0; i < 15; i++ {
		deps.Session.IncrementMetric(agent.MetricSteps, 1)
	}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}
}

func TestReflectPhase_Execute_MaxTokensExceeded(t *testing.T) {
	phase := NewReflectPhase(WithMaxTotalTokens(1000))
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		TotalTokens: 5000, // Exceeds limit
	}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}
}

func TestReflectPhase_Execute_Continue(t *testing.T) {
	phase := NewReflectPhase()
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		TotalTokens: 1000,
		ToolResults: []agent.ToolResult{
			{Success: true},
			{Success: true},
		},
	}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateExecute {
		t.Errorf("nextState = %s, want EXECUTE", nextState)
	}
}

func TestReflectPhase_LooksStuck(t *testing.T) {
	phase := NewReflectPhase()

	tests := []struct {
		name     string
		results  []agent.ToolResult
		expected bool
	}{
		{
			name:     "all success",
			results:  []agent.ToolResult{{Success: true}, {Success: true}, {Success: true}},
			expected: false,
		},
		{
			name:     "all failures",
			results:  []agent.ToolResult{{Success: false}, {Success: false}, {Success: false}},
			expected: true,
		},
		{
			name:     "mostly failures",
			results:  []agent.ToolResult{{Success: false}, {Success: false}, {Success: true}, {Success: false}},
			expected: true,
		},
		{
			name:     "too few results",
			results:  []agent.ToolResult{{Success: false}, {Success: false}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ReflectionInput{RecentResults: tt.results}
			result := phase.looksStuck(input)
			if result != tt.expected {
				t.Errorf("looksStuck() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestReflectPhase_LooksComplete_NotFoundAnswer(t *testing.T) {
	phase := NewReflectPhase()

	tests := []struct {
		name         string
		lastResponse string
		results      []agent.ToolResult
		want         bool
	}{
		{
			name:         "symbol not found is complete",
			lastResponse: "I searched the codebase but the symbol 'Foo' was not found.",
			results:      []agent.ToolResult{{Success: false}},
			want:         true,
		},
		{
			name:         "function does not exist is complete",
			lastResponse: "After analyzing the code, the function 'Bar' does not exist.",
			results:      []agent.ToolResult{{Success: false}},
			want:         true,
		},
		{
			name:         "unable to find is complete",
			lastResponse: "I looked through all files but was unable to find 'Baz'.",
			results:      []agent.ToolResult{{Success: false}},
			want:         true,
		},
		{
			name:         "no matches is complete",
			lastResponse: "Queried the codebase but found no matches for the requested symbol.",
			results:      []agent.ToolResult{{Success: false}},
			want:         true,
		},
		{
			name:         "checked and not found is complete",
			lastResponse: "I checked all available symbols but the function 'test' was not found.",
			results:      []agent.ToolResult{{Success: false}},
			want:         true,
		},
		{
			name:         "not found without search context is incomplete",
			lastResponse: "The symbol was not found.",
			results:      []agent.ToolResult{{Success: false}},
			want:         false,
		},
		{
			name:         "error message is not complete",
			lastResponse: "Error: failed to execute query.",
			results:      []agent.ToolResult{{Success: false}},
			want:         false,
		},
		{
			name:         "does not exist without search context is incomplete",
			lastResponse: "The function does not exist.",
			results:      []agent.ToolResult{{Success: false}},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ReflectionInput{
				LastResponse:  tt.lastResponse,
				RecentResults: tt.results,
			}

			got := phase.looksComplete(input)
			if got != tt.want {
				t.Errorf("looksComplete() = %v, want %v for response: %s",
					got, tt.want, tt.lastResponse)
			}
		})
	}
}

func TestReflectPhase_LooksStuck_NotFoundAnswer(t *testing.T) {
	phase := NewReflectPhase()

	tests := []struct {
		name         string
		lastResponse string
		results      []agent.ToolResult
		want         bool
	}{
		{
			name:         "not found answer is not stuck",
			lastResponse: "The symbol was not found in the codebase.",
			results:      []agent.ToolResult{{Success: false}, {Success: false}, {Success: false}},
			want:         false, // Should NOT be stuck
		},
		{
			name:         "does not exist answer is not stuck",
			lastResponse: "The function does not exist.",
			results:      []agent.ToolResult{{Success: false}, {Success: false}, {Success: false}},
			want:         false, // Should NOT be stuck
		},
		{
			name:         "multiple failures without not found IS stuck",
			lastResponse: "Error executing query.",
			results:      []agent.ToolResult{{Success: false}, {Success: false}, {Success: false}},
			want:         true, // Should be stuck
		},
		{
			name:         "empty response with failures IS stuck",
			lastResponse: "",
			results:      []agent.ToolResult{{Success: false}, {Success: false}, {Success: false}},
			want:         true, // Should be stuck
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ReflectionInput{
				LastResponse:  tt.lastResponse,
				RecentResults: tt.results,
			}

			got := phase.looksStuck(input)
			if got != tt.want {
				t.Errorf("looksStuck() = %v, want %v for response: %s",
					got, tt.want, tt.lastResponse)
			}
		})
	}
}

func TestReflectPhase_LooksComplete_EmptyResponseLoop(t *testing.T) {
	phase := NewReflectPhase()

	tests := []struct {
		name         string
		lastResponse string
		results      []agent.ToolResult
		want         bool
	}{
		{
			name:         "empty response with mostly failed results is complete",
			lastResponse: "",
			results: []agent.ToolResult{
				{Success: false, Output: ""},
				{Success: false, Output: "error"},
				{Success: false, Output: ""},
			},
			want: true, // Empty loop detected
		},
		{
			name:         "empty response with all tiny outputs is complete",
			lastResponse: "",
			results: []agent.ToolResult{
				{Success: true, Output: "No results"}, // < 100 chars
				{Success: true, Output: "Not found"},  // < 100 chars
				{Success: false, Output: ""},
			},
			want: true, // Empty loop detected
		},
		{
			name:         "empty response with substantial output is NOT complete",
			lastResponse: "",
			results: []agent.ToolResult{
				{Success: true, Output: strings.Repeat("Valid output with substantial content that indicates real progress. ", 5)}, // > 100 chars
				{Success: true, Output: "small"},
				{Success: false, Output: ""},
			},
			want: false, // Has real progress
		},
		{
			name:         "empty response with only 2 results is NOT complete",
			lastResponse: "",
			results: []agent.ToolResult{
				{Success: false, Output: ""},
				{Success: false, Output: ""},
			},
			want: false, // Need >= 3 results
		},
		{
			name:         "non-empty response doesn't trigger empty loop detection",
			lastResponse: "Some response text",
			results: []agent.ToolResult{
				{Success: false, Output: ""},
				{Success: false, Output: ""},
				{Success: false, Output: ""},
			},
			want: false, // LastResponse is not empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ReflectionInput{
				LastResponse:  tt.lastResponse,
				RecentResults: tt.results,
			}

			got := phase.looksComplete(input)
			if got != tt.want {
				t.Errorf("looksComplete() = %v, want %v for empty_response=%v, results=%d",
					got, tt.want, tt.lastResponse == "", len(tt.results))
			}
		})
	}
}

// TestClarifyPhase tests
func TestClarifyPhase_Name(t *testing.T) {
	phase := NewClarifyPhase()
	if phase.Name() != "clarify" {
		t.Errorf("Name() = %s, want clarify", phase.Name())
	}
}

func TestClarifyPhase_Execute_AwaitingClarification(t *testing.T) {
	phase := NewClarifyPhase()
	deps := createTestDependencies()

	nextState, err := phase.Execute(context.Background(), deps)
	if !errors.Is(err, agent.ErrAwaitingClarification) {
		t.Errorf("expected ErrAwaitingClarification, got %v", err)
	}
	if nextState != agent.StateClarify {
		t.Errorf("nextState = %s, want CLARIFY", nextState)
	}
}

func TestClarifyPhase_Execute_WithClarification(t *testing.T) {
	phase := NewClarifyPhase()
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{}
	deps.ContextManager = &agentcontext.Manager{}

	phase.SetClarificationInput("I want to understand the login function")

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StatePlan {
		t.Errorf("nextState = %s, want PLAN", nextState)
	}
}

func TestClarifyPhase_GetClarificationPrompt(t *testing.T) {
	phase := NewClarifyPhase(WithDefaultPrompt("Custom prompt"))
	deps := createTestDependencies()

	prompt := phase.GetClarificationPrompt(deps)
	if prompt != "Custom prompt" {
		t.Errorf("GetClarificationPrompt() = %s, want Custom prompt", prompt)
	}
}

// Test utilities
func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"Hello World", "world", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "foo", false},
		{"", "foo", false},
		{"Hello", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+":"+tt.substr, func(t *testing.T) {
			result := containsIgnoreCase(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("containsIgnoreCase(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

// Test that execute phase requires context manager for completion flow
func TestExecutePhase_RequiresContextManager(t *testing.T) {
	// The execute phase needs a context manager to add messages
	// This test documents that requirement
	phase := NewExecutePhase()
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
		TotalTokens:         100,
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)

	mockLLM := llm.NewMockClient()
	mockLLM.QueueFinalResponse("Done")
	deps.LLMClient = mockLLM

	// With a nil context manager, we need to provide a mock
	// In a real scenario, the context manager would be required
	deps.ContextManager = &agentcontext.Manager{}

	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}
}

// Test phase options
func TestExecutePhaseOptions(t *testing.T) {
	phase := NewExecutePhase(
		WithMaxTokens(8192),
		WithReflectionThreshold(20),
		WithSafetyCheck(false),
	)

	if phase.maxTokens != 8192 {
		t.Errorf("maxTokens = %d, want 8192", phase.maxTokens)
	}
	if phase.reflectionThreshold != 20 {
		t.Errorf("reflectionThreshold = %d, want 20", phase.reflectionThreshold)
	}
	if phase.requireSafetyCheck {
		t.Error("requireSafetyCheck should be false")
	}
}

func TestReflectPhaseOptions(t *testing.T) {
	phase := NewReflectPhase(
		WithMaxSteps(100),
		WithMaxTotalTokens(200000),
	)

	if phase.maxSteps != 100 {
		t.Errorf("maxSteps = %d, want 100", phase.maxSteps)
	}
	if phase.maxTokens != 200000 {
		t.Errorf("maxTokens = %d, want 200000", phase.maxTokens)
	}
}

func TestPlanPhaseOptions(t *testing.T) {
	phase := NewPlanPhase(WithInitialBudget(10000))

	if phase.initialBudget != 10000 {
		t.Errorf("initialBudget = %d, want 10000", phase.initialBudget)
	}
}

func TestClarifyPhaseOptions(t *testing.T) {
	phase := NewClarifyPhase(WithDefaultPrompt("Test prompt"))

	if phase.defaultPrompt != "Test prompt" {
		t.Errorf("defaultPrompt = %s, want Test prompt", phase.defaultPrompt)
	}
}

// Integration test: Full workflow simulation
func TestFullWorkflowSimulation(t *testing.T) {
	// This test simulates a simple workflow through phases

	// 1. Init phase
	initPhase := NewInitPhase()
	deps := createTestDependencies()

	state, err := initPhase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if state != agent.StatePlan {
		t.Fatalf("Expected PLAN after init, got %s", state)
	}

	// Simulate that we're now at a state where execute would work
	deps.Context = &agent.AssembledContext{
		SystemPrompt: "You are a helpful assistant.",
		ConversationHistory: []agent.Message{
			{Role: "user", Content: deps.Query},
		},
		TotalTokens: 100,
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)
	deps.ContextManager = &agentcontext.Manager{} // Required for completion flow

	// 2. Execute phase with completion
	execPhase := NewExecutePhase()
	mockLLM := llm.NewMockClient()
	mockLLM.QueueFinalResponse("The function calculates the total price.")
	deps.LLMClient = mockLLM

	state, err = execPhase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if state != agent.StateComplete {
		t.Fatalf("Expected COMPLETE after execute, got %s", state)
	}
}

// Test event emission
func TestPhaseEmitsEvents(t *testing.T) {
	initPhase := NewInitPhase()
	deps := createTestDependencies()

	var receivedEvents []events.Event
	deps.EventEmitter.Subscribe(func(e *events.Event) {
		receivedEvents = append(receivedEvents, *e)
	})

	_, _ = initPhase.Execute(context.Background(), deps)

	// Should have emitted session start and state transition events
	if len(receivedEvents) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(receivedEvents))
	}

	// Check for session start event
	hasSessionStart := false
	for _, e := range receivedEvents {
		if e.Type == events.TypeSessionStart {
			hasSessionStart = true
			break
		}
	}
	if !hasSessionStart {
		t.Error("Expected session_start event")
	}
}

// Test context cancellation
func TestPhasesRespectContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	time.Sleep(5 * time.Millisecond) // Ensure timeout

	initPhase := NewInitPhase()
	deps := createTestDependencies()

	// The graph provider should detect cancelled context
	deps.GraphProvider.(*MockGraphProvider).initError = ctx.Err()

	state, _ := initPhase.Execute(ctx, deps)
	// Should handle gracefully (degraded mode)
	if state != agent.StateDegraded {
		t.Errorf("Expected DEGRADED on context cancellation, got %s", state)
	}
}

// Test fallback response generation when synthesis fails
func TestReflectPhase_FallbackResponse(t *testing.T) {
	phase := NewReflectPhase(
		WithMaxSteps(1), // Low threshold to trigger completion immediately
	)
	deps := createTestDependencies()

	// Set up context with tool results but no LLM client (triggers fallback)
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{
			{Role: "user", Content: "What are the security concerns?"},
		},
		ToolResults: []agent.ToolResult{
			{InvocationID: "call_1", Success: true, Output: "Found 3 files in /src/auth: login.go, token.go, session.go"},
			{InvocationID: "call_2", Success: true, Output: "Function validateToken handles JWT verification"},
			{InvocationID: "call_3", Success: false, Output: "", Error: "file not found"},
		},
	}
	deps.LLMClient = nil // No LLM client - forces fallback

	// Update session metrics to trigger completion
	deps.Session.IncrementMetric(agent.MetricSteps, 10)

	state, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if state != agent.StateComplete {
		t.Errorf("Expected COMPLETE, got %s", state)
	}

	// Check that a fallback response was generated
	ctx := deps.Session.GetCurrentContext()
	if ctx == nil {
		t.Fatal("Expected context to be set")
	}

	// Find assistant response
	var assistantMsg *agent.Message
	for i := len(ctx.ConversationHistory) - 1; i >= 0; i-- {
		if ctx.ConversationHistory[i].Role == "assistant" {
			assistantMsg = &ctx.ConversationHistory[i]
			break
		}
	}

	if assistantMsg == nil {
		t.Fatal("Expected assistant response in conversation history")
	}

	// Check fallback response contains expected elements
	if !contains(assistantMsg.Content, "exploration") {
		t.Errorf("Fallback response should mention exploration, got: %s", assistantMsg.Content)
	}
	if !contains(assistantMsg.Content, "3 tool calls") {
		t.Errorf("Fallback response should mention tool calls count, got: %s", assistantMsg.Content)
	}
	if !contains(assistantMsg.Content, "2 successful results") {
		t.Errorf("Fallback response should mention successful results, got: %s", assistantMsg.Content)
	}
}

// Test context truncation for synthesis
func TestReflectPhase_ContextTruncation(t *testing.T) {
	phase := NewReflectPhase()

	// Create a very large context
	deps := createTestDependencies()
	largeOutput := make([]byte, 50000)
	for i := range largeOutput {
		largeOutput[i] = 'x'
	}

	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{
			{Role: "user", Content: "Original query"},
		},
		ToolResults: []agent.ToolResult{
			{InvocationID: "call_1", Success: true, Output: string(largeOutput)},
			{InvocationID: "call_2", Success: true, Output: string(largeOutput)},
		},
	}

	// Test that prepareSynthesisContext truncates correctly
	reduced := phase.prepareSynthesisContext(deps)

	// Estimate reduced size
	totalSize := 0
	for _, msg := range reduced.ConversationHistory {
		totalSize += len(msg.Content)
	}
	for _, result := range reduced.ToolResults {
		totalSize += len(result.Output)
	}

	// Should be much smaller than original (2 x 50000 = 100000 bytes)
	if totalSize >= 100000 {
		t.Errorf("Context was not truncated: size = %d", totalSize)
	}

	// Each tool result should be truncated to ~2000 chars + truncation message
	for _, result := range reduced.ToolResults {
		if len(result.Output) > 2500 {
			t.Errorf("Tool result not truncated: len = %d", len(result.Output))
		}
	}
}

// helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// Coordinator Event Tests (CR-8)
// =============================================================================

func TestExecutePhase_WithNilCoordinator_NoError(t *testing.T) {
	// When Coordinator is nil, emitCoordinatorEvent should return early without error
	phase := NewExecutePhase(WithReflectionThreshold(100))
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)
	deps.ContextManager = &agentcontext.Manager{}

	// Explicitly set Coordinator to nil (it's already nil by default)
	deps.Coordinator = nil

	// Mock LLM that completes without tool calls
	mockLLM := llm.NewMockClient()
	mockLLM.QueueFinalResponse("Done")
	deps.LLMClient = mockLLM

	// Execute should complete without error despite nil Coordinator
	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed with nil Coordinator: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}
}

func TestExecutePhase_WithNilSession_CoordinatorEventSkipped(t *testing.T) {
	// When Session is nil but Coordinator is set, emitCoordinatorEvent should not panic
	phase := NewExecutePhase()
	deps := &Dependencies{
		Session: nil, // Explicitly nil
		Query:   "test",
	}

	// This should return error due to nil session, not panic from emitCoordinatorEvent
	nextState, err := phase.Execute(context.Background(), deps)
	if err == nil {
		t.Error("Expected error for nil session")
	}
	if nextState != agent.StateError {
		t.Errorf("nextState = %s, want ERROR", nextState)
	}
}

func TestExecutePhase_ToolFailed_WithNilCoordinator_NoError(t *testing.T) {
	// Tool failure should be handled even when Coordinator is nil
	phase := NewExecutePhase(WithReflectionThreshold(100))
	deps := createTestDependencies()
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
	}
	deps.ToolRegistry = tools.NewRegistry()
	deps.SafetyGate = safety.NewMockGate()
	deps.Coordinator = nil

	// Register a tool that fails
	failingTool := tools.NewMockTool("failing_tool", tools.CategoryExploration)
	failingTool.ExecuteFunc = func(ctx context.Context, params tools.TypedParams) (*tools.Result, error) {
		return &tools.Result{
			Success: false,
			Error:   "tool failed deliberately",
		}, nil
	}
	deps.ToolRegistry.Register(failingTool)
	deps.ToolExecutor = tools.NewExecutor(deps.ToolRegistry, nil)

	// Mock LLM that requests the failing tool
	mockLLM := llm.NewMockClient()
	mockLLM.QueueToolCall("failing_tool", map[string]any{"param": "value"})
	deps.LLMClient = mockLLM
	deps.ContextManager = &agentcontext.Manager{}

	// Execute should handle tool failure without Coordinator error
	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Should continue execution after tool failure
	if nextState != agent.StateExecute {
		t.Errorf("nextState = %s, want EXECUTE", nextState)
	}
}

// CB-31: Test empty response fallback when LLM returns empty and synthesis fails
func TestExecutePhase_EmptyResponseFallback(t *testing.T) {
	phase := NewExecutePhase(WithReflectionThreshold(100))
	deps := createTestDependencies()
	deps.Query = "What does the parseConfig function do?"
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{
			{Role: "user", Content: deps.Query},
		},
		// Empty ToolResults - synthesis will fail
		ToolResults: []agent.ToolResult{},
	}
	deps.Coordinator = nil

	// Mock LLM that returns EMPTY response
	mockLLM := llm.NewMockClient()
	mockLLM.QueueFinalResponse("") // Empty response triggers the bug path
	deps.LLMClient = mockLLM

	// Execute - should complete with fallback message instead of empty
	nextState, err := phase.Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE", nextState)
	}

	// Check that fallback response was generated
	ctx := deps.Session.GetCurrentContext()
	if ctx == nil {
		t.Fatal("Expected context to be set")
	}

	// Find last assistant response
	var assistantMsg *agent.Message
	for i := len(ctx.ConversationHistory) - 1; i >= 0; i-- {
		if ctx.ConversationHistory[i].Role == "assistant" {
			assistantMsg = &ctx.ConversationHistory[i]
			break
		}
	}

	if assistantMsg == nil {
		t.Fatal("Expected assistant response in conversation history")
	}

	// CB-31: Verify we get a fallback message, NOT empty
	if assistantMsg.Content == "" {
		t.Error("CB-31 Bug: Got empty response when fallback should have been provided")
	}

	// Check fallback contains expected elements
	if !contains(assistantMsg.Content, "unable") {
		t.Errorf("Fallback should mention being unable to respond, got: %s", truncateForTest(assistantMsg.Content, 200))
	}
	if !contains(assistantMsg.Content, "Suggestions") {
		t.Errorf("Fallback should contain suggestions, got: %s", truncateForTest(assistantMsg.Content, 200))
	}
}

// CB-31: Test buildEmptyResponseFallback directly with tool history
func TestExecutePhase_BuildEmptyResponseFallback(t *testing.T) {
	phase := NewExecutePhase()
	deps := createTestDependencies()
	deps.Query = "Find the implementation of processRequest"
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
		ToolResults:         []agent.ToolResult{},
	}

	t.Run("no_tools_executed", func(t *testing.T) {
		fallback := phase.buildEmptyResponseFallback(deps)

		if fallback == "" {
			t.Error("Expected non-empty fallback message")
		}
		if !contains(fallback, "unable") {
			t.Errorf("Fallback should mention being unable, got: %s", truncateForTest(fallback, 200))
		}
		if !contains(fallback, "Suggestions") {
			t.Errorf("Fallback should contain suggestions, got: %s", truncateForTest(fallback, 200))
		}
		// Should mention no codebase exploration was completed
		if !contains(fallback, "No codebase exploration") {
			t.Errorf("Fallback should mention no exploration when no tools ran, got: %s", truncateForTest(fallback, 200))
		}
	})

	t.Run("with_tools_executed", func(t *testing.T) {
		// Add some trace steps to simulate tool execution
		deps.Session.RecordTraceStep(crs.TraceStep{
			Action: "tool_call",
			Tool:   "Grep",
		})
		deps.Session.RecordTraceStep(crs.TraceStep{
			Action: "tool_call",
			Tool:   "Read",
		})

		fallback := phase.buildEmptyResponseFallback(deps)

		if fallback == "" {
			t.Error("Expected non-empty fallback message")
		}
		// Should mention tool execution summary
		if !contains(fallback, "tool call") {
			t.Errorf("Fallback should mention tool calls when tools were executed, got: %s", truncateForTest(fallback, 300))
		}
		if !contains(fallback, "Exploration Summary") {
			t.Errorf("Fallback should have exploration summary, got: %s", truncateForTest(fallback, 300))
		}
	})

	t.Run("includes_query", func(t *testing.T) {
		fallback := phase.buildEmptyResponseFallback(deps)

		// Should include the original query for reference
		if !contains(fallback, "Original Query") {
			t.Errorf("Fallback should include original query, got: %s", truncateForTest(fallback, 300))
		}
	})
}

// Helper for test truncation
func truncateForTest(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TestExecutePhase_SynthesisMode_SkipsToolForcing verifies the IT-11 fix:
// when handleCompletion is called from forceLLMSynthesis (ToolChoice=none),
// tool forcing must NOT trigger, even for analytical queries. Before the fix,
// this caused a redundant second EXECUTE loop wasting ~9-18s per test.
func TestExecutePhase_SynthesisMode_SkipsToolForcing(t *testing.T) {
	phase := NewExecutePhase()
	deps := createTestDependencies()
	// Use an analytical query that would normally trigger tool forcing.
	deps.Query = "Find natural code communities and module boundaries in this codebase"
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
		ToolResults: []agent.ToolResult{
			{
				Success: true,
				Output:  "Detected 20 communities (modularity: 0.45):\n\nCommunity 1 (3776 symbols)...",
			},
		},
	}
	deps.ContextManager = &agentcontext.Manager{}

	// Simulate a successful synthesis response.
	response := &llm.Response{
		Content:      "The community detection found 20 distinct groups...",
		OutputTokens: 500,
	}

	// Build a request with ToolChoice=none (what forceLLMSynthesis sets).
	request := &llm.Request{
		ToolChoice: llm.ToolChoiceNone(),
	}

	nextState, err := phase.handleCompletion(
		context.Background(), deps, response, request, time.Now(), 1,
	)
	if err != nil {
		t.Fatalf("handleCompletion failed: %v", err)
	}

	// Must complete, NOT return to EXECUTE for a redundant loop.
	if nextState != agent.StateComplete {
		t.Errorf("nextState = %s, want COMPLETE (synthesis mode should skip tool forcing)", nextState)
	}

	// Verify no forcing retry was counted.
	forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)
	if forcingRetries != 0 {
		t.Errorf("MetricToolForcingRetries = %d, want 0 (no forcing should occur in synthesis mode)", forcingRetries)
	}
}

// TestExecutePhase_NonSynthesisMode_StillForcesTools verifies that the IT-11 fix
// does NOT break normal tool forcing: when ToolChoice is NOT none (i.e., a normal
// LLM response without tool calls for an analytical query), tool forcing should
// still trigger as before.
func TestExecutePhase_NonSynthesisMode_StillForcesTools(t *testing.T) {
	phase := NewExecutePhase()
	deps := createTestDependencies()
	// Analytical query that should trigger tool forcing.
	deps.Query = "Find natural code communities and module boundaries in this codebase"
	deps.Context = &agent.AssembledContext{
		ConversationHistory: []agent.Message{},
	}
	deps.ContextManager = &agentcontext.Manager{}

	// Register a tool so the forcing hint has something to suggest.
	deps.ToolRegistry = tools.NewRegistry()

	// LLM returned text without tool calls (normal response, not synthesis).
	response := &llm.Response{
		Content:      "I think the code has several modules...",
		OutputTokens: 100,
	}

	// Normal request — ToolChoice is nil or auto, NOT none.
	request := &llm.Request{}

	nextState, err := phase.handleCompletion(
		context.Background(), deps, response, request, time.Now(), 1,
	)
	if err != nil {
		t.Fatalf("handleCompletion failed: %v", err)
	}

	// Should return EXECUTE to force tool usage for this analytical query.
	if nextState != agent.StateExecute {
		t.Errorf("nextState = %s, want EXECUTE (tool forcing should trigger for analytical query without synthesis mode)", nextState)
	}

	// Verify forcing retry was counted.
	forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)
	if forcingRetries != 1 {
		t.Errorf("MetricToolForcingRetries = %d, want 1", forcingRetries)
	}
}
