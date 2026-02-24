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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockAgentLoop implements agent.AgentLoop for testing.
type MockAgentLoop struct {
	runFunc          func(ctx context.Context, session *agent.Session, query string) (*agent.RunResult, error)
	continueFunc     func(ctx context.Context, sessionID string, clarification string) (*agent.RunResult, error)
	abortFunc        func(ctx context.Context, sessionID string) error
	getStateFunc     func(sessionID string) (*agent.SessionState, error)
	getSessionFunc   func(sessionID string) (*agent.Session, error)
	closeSessionFunc func(sessionID string) error
	listSessionsFunc func() []*agent.SessionSummary
}

func (m *MockAgentLoop) Run(ctx context.Context, session *agent.Session, query string) (*agent.RunResult, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, session, query)
	}
	return &agent.RunResult{
		State:      agent.StateComplete,
		StepsTaken: 1,
		Response:   "Mock response",
	}, nil
}

func (m *MockAgentLoop) Continue(ctx context.Context, sessionID string, clarification string) (*agent.RunResult, error) {
	if m.continueFunc != nil {
		return m.continueFunc(ctx, sessionID, clarification)
	}
	return &agent.RunResult{
		State:      agent.StateComplete,
		StepsTaken: 2,
	}, nil
}

func (m *MockAgentLoop) Abort(ctx context.Context, sessionID string) error {
	if m.abortFunc != nil {
		return m.abortFunc(ctx, sessionID)
	}
	return nil
}

func (m *MockAgentLoop) GetState(sessionID string) (*agent.SessionState, error) {
	if m.getStateFunc != nil {
		return m.getStateFunc(sessionID)
	}
	return &agent.SessionState{
		ID:           sessionID,
		ProjectRoot:  "/test/project",
		State:        agent.StateComplete,
		CreatedAt:    time.Now().UnixMilli(),
		LastActiveAt: time.Now().UnixMilli(),
	}, nil
}

func (m *MockAgentLoop) GetSession(sessionID string) (*agent.Session, error) {
	if m.getSessionFunc != nil {
		return m.getSessionFunc(sessionID)
	}
	session, _ := agent.NewSession("/test/project", nil)
	return session, nil
}

func (m *MockAgentLoop) CloseSession(sessionID string) error {
	if m.closeSessionFunc != nil {
		return m.closeSessionFunc(sessionID)
	}
	return nil
}

func (m *MockAgentLoop) ListSessions() []*agent.SessionSummary {
	if m.listSessionsFunc != nil {
		return m.listSessionsFunc()
	}
	return []*agent.SessionSummary{}
}

func setupAgentTestRouter(handlers *AgentHandlers) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/v1")
	RegisterAgentRoutes(v1, handlers)
	return r
}

func TestAgentHandlers_HandleAgentRun_Success(t *testing.T) {
	mockLoop := &MockAgentLoop{
		runFunc: func(ctx context.Context, session *agent.Session, query string) (*agent.RunResult, error) {
			return &agent.RunResult{
				State:      agent.StateComplete,
				StepsTaken: 3,
				TokensUsed: 1000,
				Response:   "The function calculates the sum of two numbers.",
			}, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentRunRequest{
		ProjectRoot: "/test/project",
		Query:       "What does the add function do?",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/run", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp AgentRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.State != "COMPLETE" {
		t.Errorf("State = %s, want COMPLETE", resp.State)
	}
	if resp.StepsTaken != 3 {
		t.Errorf("StepsTaken = %d, want 3", resp.StepsTaken)
	}
	if resp.Response == "" {
		t.Error("expected non-empty response")
	}
}

func TestAgentHandlers_HandleAgentRun_EmptyQuery(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentRunRequest{
		ProjectRoot: "/test/project",
		Query:       "",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/run", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAgentHandlers_HandleAgentRun_NeedsClarify(t *testing.T) {
	mockLoop := &MockAgentLoop{
		runFunc: func(ctx context.Context, session *agent.Session, query string) (*agent.RunResult, error) {
			return &agent.RunResult{
				State:      agent.StateClarify,
				StepsTaken: 2,
				NeedsClarify: &agent.ClarifyRequest{
					Question: "Which add function are you referring to?",
					Options:  []string{"math.Add", "util.Add"},
				},
			}, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentRunRequest{
		ProjectRoot: "/test/project",
		Query:       "What does the add function do?",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/run", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp AgentRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.State != "CLARIFY" {
		t.Errorf("State = %s, want CLARIFY", resp.State)
	}
	if resp.NeedsClarify == nil {
		t.Error("expected NeedsClarify to be set")
	}
}

func TestAgentHandlers_HandleAgentContinue_Success(t *testing.T) {
	mockLoop := &MockAgentLoop{
		continueFunc: func(ctx context.Context, sessionID string, clarification string) (*agent.RunResult, error) {
			return &agent.RunResult{
				State:      agent.StateComplete,
				StepsTaken: 4,
				Response:   "The math.Add function adds two integers.",
			}, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentContinueRequest{
		SessionID:     "test-session-id",
		Clarification: "math.Add",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/continue", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp AgentRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.State != "COMPLETE" {
		t.Errorf("State = %s, want COMPLETE", resp.State)
	}
}

func TestAgentHandlers_HandleAgentContinue_NotFound(t *testing.T) {
	mockLoop := &MockAgentLoop{
		continueFunc: func(ctx context.Context, sessionID string, clarification string) (*agent.RunResult, error) {
			return nil, agent.ErrSessionNotFound
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentContinueRequest{
		SessionID:     "nonexistent-id",
		Clarification: "test",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/continue", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAgentHandlers_HandleAgentAbort_Success(t *testing.T) {
	abortCalled := false
	mockLoop := &MockAgentLoop{
		abortFunc: func(ctx context.Context, sessionID string) error {
			abortCalled = true
			return nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentAbortRequest{
		SessionID: "test-session-id",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/abort", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	if !abortCalled {
		t.Error("expected abort to be called")
	}
}

func TestAgentHandlers_HandleAgentAbort_NotFound(t *testing.T) {
	mockLoop := &MockAgentLoop{
		abortFunc: func(ctx context.Context, sessionID string) error {
			return agent.ErrSessionNotFound
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentAbortRequest{
		SessionID: "nonexistent-id",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/abort", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAgentHandlers_HandleAgentState_Success(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getStateFunc: func(sessionID string) (*agent.SessionState, error) {
			return &agent.SessionState{
				ID:           sessionID,
				ProjectRoot:  "/test/project",
				State:        agent.StateExecute,
				StepCount:    5,
				TokensUsed:   2000,
				CreatedAt:    time.Now().Add(-5 * time.Minute).UnixMilli(),
				LastActiveAt: time.Now().UnixMilli(),
			}, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/test-session-id", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp AgentStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.SessionID != "test-session-id" {
		t.Errorf("SessionID = %s, want test-session-id", resp.SessionID)
	}
	if resp.State != "EXECUTE" {
		t.Errorf("State = %s, want EXECUTE", resp.State)
	}
	if resp.StepCount != 5 {
		t.Errorf("StepCount = %d, want 5", resp.StepCount)
	}
}

func TestAgentHandlers_HandleAgentState_NotFound(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getStateFunc: func(sessionID string) (*agent.SessionState, error) {
			return nil, agent.ErrSessionNotFound
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/nonexistent-id", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAgentHandlers_HandleAgentRun_InvalidJSON(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("POST", "/v1/trace/agent/run", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAgentHandlers_HandleAgentRun_SessionInProgress(t *testing.T) {
	mockLoop := &MockAgentLoop{
		runFunc: func(ctx context.Context, session *agent.Session, query string) (*agent.RunResult, error) {
			return nil, agent.ErrSessionInProgress
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	body := AgentRunRequest{
		ProjectRoot: "/test/project",
		Query:       "test query",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/trace/agent/run", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestAgentErrorToString(t *testing.T) {
	tests := []struct {
		name string
		err  *agent.AgentError
		want string
	}{
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
		{
			name: "with message",
			err: &agent.AgentError{
				Code:    "TEST_ERROR",
				Message: "Test error message",
			},
			want: "Test error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentErrorToString(tt.err)
			if got != tt.want {
				t.Errorf("agentErrorToString() = %s, want %s", got, tt.want)
			}
		})
	}
}

// =============================================================================
// CRS Export Endpoint Tests (CB-29-2)
// =============================================================================

func TestAgentHandlers_HandleGetReasoningTrace_NotFound(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getSessionFunc: func(sessionID string) (*agent.Session, error) {
			return nil, agent.ErrSessionNotFound
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/nonexistent-id/reasoning", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAgentHandlers_HandleGetReasoningTrace_EmptyTrace(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getSessionFunc: func(sessionID string) (*agent.Session, error) {
			// Return session with trace recorder (now always initialized)
			session, _ := agent.NewSession("/test/project", nil)
			return session, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/test-session/reasoning", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Should return 200 OK with empty trace (trace recorder is now always enabled)
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify the response contains an empty trace
	var response ReasoningTraceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.TotalSteps != 0 {
		t.Errorf("TotalSteps = %d, want 0", response.TotalSteps)
	}
}

func TestAgentHandlers_HandleGetCRSExport_NotFound(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getSessionFunc: func(sessionID string) (*agent.Session, error) {
			return nil, agent.ErrSessionNotFound
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/nonexistent-id/crs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAgentHandlers_HandleGetCRSExport_NoCRS(t *testing.T) {
	mockLoop := &MockAgentLoop{
		getSessionFunc: func(sessionID string) (*agent.Session, error) {
			// Return session without CRS
			session, _ := agent.NewSession("/test/project", nil)
			return session, nil
		},
	}

	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	req := httptest.NewRequest("GET", "/v1/trace/agent/test-session/crs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Should return 204 No Content when CRS not enabled
	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// =============================================================================
// CB-60b: Functional Options Tests
// =============================================================================

func TestNewAgentHandlers_WithOptions_SameMMM(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	mgr := llm.NewMultiModelManager("http://localhost:11434")
	factory := providers.NewProviderFactory(mgr)
	rc := &providers.RoleConfig{
		Main:   providers.ProviderConfig{Provider: providers.ProviderOllama, Model: "test"},
		Router: providers.ProviderConfig{Provider: providers.ProviderOllama, Model: "router"},
	}

	handlers := NewAgentHandlers(mockLoop, nil,
		WithProviderFactory(factory),
		WithModelManager(mgr),
		WithRoleConfig(rc),
	)

	// Verify the same manager pointer is used (no double MMM)
	if handlers.modelManager != mgr {
		t.Error("expected injected modelManager to be used (pointer equality)")
	}
	if handlers.providerFactory != factory {
		t.Error("expected injected providerFactory to be used (pointer equality)")
	}
	if handlers.roleConfig != rc {
		t.Error("expected injected roleConfig to be used (pointer equality)")
	}
}

func TestNewAgentHandlers_WithOptions_NilMMM_NoPanic(t *testing.T) {
	// All-cloud configuration: no Ollama MMM needed, should not panic.
	// When nil MMM is passed via option, backward compat creates a fallback.
	mockLoop := &MockAgentLoop{}
	factory := providers.NewProviderFactory(nil)

	handlers := NewAgentHandlers(mockLoop, nil,
		WithProviderFactory(factory),
	)

	// Factory was injected, so it should be the same pointer
	if handlers.providerFactory != factory {
		t.Error("expected injected factory to be used")
	}
	// modelManager should have been created as fallback (backward compat)
	if handlers.modelManager == nil {
		t.Error("expected fallback modelManager to be created")
	}
}

func TestNewAgentHandlers_NoOptions_CreatesOwnMMM(t *testing.T) {
	mockLoop := &MockAgentLoop{}

	handlers := NewAgentHandlers(mockLoop, nil)

	if handlers.modelManager == nil {
		t.Error("expected modelManager to be created when no options provided")
	}
	if handlers.providerFactory == nil {
		t.Error("expected providerFactory to be created when no options provided")
	}
	if handlers.roleConfig != nil {
		t.Error("expected roleConfig to be nil when not injected")
	}
}

func TestNewAgentHandlers_WithRoleConfig_StoredCorrectly(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	rc := &providers.RoleConfig{
		Main:           providers.ProviderConfig{Provider: providers.ProviderAnthropic, Model: "claude-sonnet"},
		Router:         providers.ProviderConfig{Provider: providers.ProviderOllama, Model: "granite4:micro-h"},
		ParamExtractor: providers.ProviderConfig{Provider: providers.ProviderOllama, Model: "ministral-3:3b"},
	}

	handlers := NewAgentHandlers(mockLoop, nil, WithRoleConfig(rc))

	if handlers.roleConfig == nil {
		t.Fatal("expected roleConfig to be set")
	}
	if handlers.roleConfig.Main.Provider != providers.ProviderAnthropic {
		t.Errorf("Main.Provider = %q, want %q", handlers.roleConfig.Main.Provider, providers.ProviderAnthropic)
	}
	if handlers.roleConfig.Router.Model != "granite4:micro-h" {
		t.Errorf("Router.Model = %q, want %q", handlers.roleConfig.Router.Model, "granite4:micro-h")
	}
}

func TestAgentHandlers_HandleGetReasoningTrace_MissingSessionID(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	// Test with empty session ID - Gin routes to handler with empty :id param
	req := httptest.NewRequest("GET", "/v1/trace/agent//reasoning", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Empty session ID gets caught by handler validation, returns 400
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAgentHandlers_HandleGetCRSExport_MissingSessionID(t *testing.T) {
	mockLoop := &MockAgentLoop{}
	handlers := NewAgentHandlers(mockLoop, nil)
	r := setupAgentTestRouter(handlers)

	// Test with empty session ID - Gin routes to handler with empty :id param
	req := httptest.NewRequest("GET", "/v1/trace/agent//crs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Empty session ID gets caught by handler validation, returns 400
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
