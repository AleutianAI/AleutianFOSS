// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// newTestProxy creates a ProxyServer pointing at mock servers.
func newTestProxy(traceURL, ollamaURL string) *ProxyServer {
	return NewProxyServer(ProxyConfig{
		ListenAddr:  ":0",
		TraceURL:    traceURL,
		OllamaURL:   ollamaURL,
		ProjectRoot: "/test/project",
		Timeout:     30 * time.Second,
	})
}

// chatRequest builds a JSON chat completion request body.
func chatRequest(messages []ChatMessage, stream bool) string {
	req := ChatCompletionRequest{
		Model:    "glm4:latest",
		Messages: messages,
		Stream:   stream,
	}
	b, _ := json.Marshal(req)
	return string(b)
}

func TestBasicChatCompletion(t *testing.T) {
	t.Run("agent returns COMPLETE, verify OpenAI response format", func(t *testing.T) {
		// Mock trace server agent/run endpoint.
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/trace/agent/run" {
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.Error(w, "not found", http.StatusNotFound)
				return
			}

			var req agentRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("failed to decode request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			if req.ProjectRoot != "/test/project" {
				t.Errorf("expected project_root=/test/project, got %s", req.ProjectRoot)
			}
			if req.Query != "What functions call parseConfig?" {
				t.Errorf("unexpected query: %s", req.Query)
			}
			// CB-62: Verify model is forwarded as config.main_model
			if req.Config == nil || req.Config.MainModel != "glm4:latest" {
				mainModel := ""
				if req.Config != nil {
					mainModel = req.Config.MainModel
				}
				t.Errorf("expected config.main_model=glm4:latest, got %s", mainModel)
			}

			resp := agentRunResponse{
				SessionID:  "sess-001",
				State:      "COMPLETE",
				StepsTaken: 3,
				TokensUsed: 500,
				Response:   "parseConfig is called by main() and LoadSettings().",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "What functions call parseConfig?"},
		}, false)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp ChatCompletionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Object != "chat.completion" {
			t.Errorf("expected object=chat.completion, got %s", resp.Object)
		}
		if len(resp.Choices) != 1 {
			t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
		}
		if resp.Choices[0].Message.Role != "assistant" {
			t.Errorf("expected role=assistant, got %s", resp.Choices[0].Message.Role)
		}
		if !strings.Contains(resp.Choices[0].Message.Content, "parseConfig") {
			t.Errorf("expected response to mention parseConfig, got: %s", resp.Choices[0].Message.Content)
		}
		if resp.Choices[0].FinishReason != "stop" {
			t.Errorf("expected finish_reason=stop, got %s", resp.Choices[0].FinishReason)
		}
		if resp.Model != "glm4:latest" {
			t.Errorf("expected model=glm4:latest, got %s", resp.Model)
		}
		if resp.Usage == nil || resp.Usage.TotalTokens != 500 {
			t.Errorf("expected total_tokens=500, got %v", resp.Usage)
		}
	})
}

func TestSessionContinuity(t *testing.T) {
	t.Run("second request with same conversation uses /continue", func(t *testing.T) {
		callCount := 0
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/v1/trace/agent/run":
				if callCount != 1 {
					t.Errorf("expected /run to be called first, but it was call #%d", callCount)
				}
				json.NewEncoder(w).Encode(agentRunResponse{
					SessionID:  "sess-002",
					State:      "COMPLETE",
					StepsTaken: 2,
					Response:   "Found 3 callers.",
				})

			case "/v1/trace/agent/continue":
				if callCount != 2 {
					t.Errorf("expected /continue to be called second, but it was call #%d", callCount)
				}
				var req map[string]string
				json.NewDecoder(r.Body).Decode(&req)
				if req["session_id"] != "sess-002" {
					t.Errorf("expected session_id=sess-002, got %s", req["session_id"])
				}
				if req["clarification"] != "Show me the call chain" {
					t.Errorf("unexpected clarification: %s", req["clarification"])
				}
				json.NewEncoder(w).Encode(agentRunResponse{
					SessionID:  "sess-002",
					State:      "COMPLETE",
					StepsTaken: 4,
					Response:   "Call chain: main() → LoadSettings() → parseConfig().",
				})

			default:
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.Error(w, "not found", http.StatusNotFound)
			}
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		// First request: creates session.
		body1 := chatRequest([]ChatMessage{
			{Role: "user", Content: "What functions call parseConfig?"},
		}, false)
		req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body1))
		req1.Header.Set("Content-Type", "application/json")
		rec1 := httptest.NewRecorder()
		mux.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusOK {
			t.Fatalf("first request: expected 200, got %d", rec1.Code)
		}

		// Second request: same first message → should use /continue.
		body2 := chatRequest([]ChatMessage{
			{Role: "user", Content: "What functions call parseConfig?"},
			{Role: "assistant", Content: "Found 3 callers."},
			{Role: "user", Content: "Show me the call chain"},
		}, false)
		req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body2))
		req2.Header.Set("Content-Type", "application/json")
		rec2 := httptest.NewRecorder()
		mux.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Fatalf("second request: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
		}

		var resp2 ChatCompletionResponse
		json.NewDecoder(rec2.Body).Decode(&resp2)
		if !strings.Contains(resp2.Choices[0].Message.Content, "Call chain") {
			t.Errorf("expected call chain in response, got: %s", resp2.Choices[0].Message.Content)
		}

		if callCount != 2 {
			t.Errorf("expected 2 trace server calls, got %d", callCount)
		}
	})
}

func TestClarifyState(t *testing.T) {
	t.Run("agent returns CLARIFY, question appears as assistant content", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID:  "sess-003",
				State:      "CLARIFY",
				StepsTaken: 1,
				NeedsClarify: &clarifyDetail{
					Question: "Which parseConfig do you mean? There are 2: config/parser.go and cmd/main.go",
					Options:  []string{"config/parser.go", "cmd/main.go"},
				},
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Show callers of parseConfig"},
		}, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp ChatCompletionResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		content := resp.Choices[0].Message.Content
		if !strings.Contains(content, "Which parseConfig") {
			t.Errorf("expected clarification question in content, got: %s", content)
		}
	})
}

func TestErrorState(t *testing.T) {
	t.Run("agent returns ERROR, verify HTTP 500 with error message", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID: "sess-004",
				State:     "ERROR",
				Error:     "graph not initialized for project",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Find callers"},
		}, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}

		var resp ChatCompletionResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		if !strings.Contains(resp.Choices[0].Message.Content, "graph not initialized") {
			t.Errorf("expected error message in content, got: %s", resp.Choices[0].Message.Content)
		}
	})
}

func TestModelsEndpoint(t *testing.T) {
	t.Run("mock Ollama /api/tags, verify OpenAI /v1/models format", func(t *testing.T) {
		ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/tags" {
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaTagsResponse{
				Models: []OllamaModel{
					{Name: "glm4:latest", ModifiedAt: "2025-01-01T00:00:00Z"},
					{Name: "qwen2.5:latest", ModifiedAt: "2025-01-02T00:00:00Z"},
				},
			})
		}))
		defer ollamaServer.Close()

		proxy := newTestProxy("http://unused", ollamaServer.URL)
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp ModelListResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Object != "list" {
			t.Errorf("expected object=list, got %s", resp.Object)
		}
		if len(resp.Data) != 2 {
			t.Fatalf("expected 2 models, got %d", len(resp.Data))
		}
		if resp.Data[0].ID != "glm4:latest" {
			t.Errorf("expected first model=glm4:latest, got %s", resp.Data[0].ID)
		}
		if resp.Data[0].Object != "model" {
			t.Errorf("expected object=model, got %s", resp.Data[0].Object)
		}
		if resp.Data[0].OwnedBy != "ollama" {
			t.Errorf("expected owned_by=ollama, got %s", resp.Data[0].OwnedBy)
		}
	})
}

func TestModelsEndpoint_FiltersInfrastructureModels(t *testing.T) {
	t.Run("infrastructure models hidden from dropdown", func(t *testing.T) {
		ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaTagsResponse{
				Models: []OllamaModel{
					{Name: "gpt-oss:20b"},
					{Name: "granite4:micro-h"},        // infra: tool router
					{Name: "ministral-3:3b"},          // infra: param extractor
					{Name: "nomic-embed-text-v2-moe"}, // infra: embedding
					{Name: "qwen3:14b"},
				},
			})
		}))
		defer ollamaServer.Close()

		proxy := newTestProxy("http://unused", ollamaServer.URL)
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp ModelListResponse
		json.NewDecoder(rec.Body).Decode(&resp)

		// Should only have gpt-oss:20b and qwen3:14b (3 infra models filtered)
		if len(resp.Data) != 2 {
			names := make([]string, len(resp.Data))
			for i, m := range resp.Data {
				names[i] = m.ID
			}
			t.Fatalf("expected 2 models after filtering, got %d: %v", len(resp.Data), names)
		}
		if resp.Data[0].ID != "gpt-oss:20b" {
			t.Errorf("expected first model=gpt-oss:20b, got %s", resp.Data[0].ID)
		}
		if resp.Data[1].ID != "qwen3:14b" {
			t.Errorf("expected second model=qwen3:14b, got %s", resp.Data[1].ID)
		}
	})
}

func TestIsInfrastructureModel(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"granite4:micro-h", true},
		{"ministral-3:3b", true},
		{"nomic-embed-text-v2-moe", true},
		{"nomic-embed-text-v2-moe:latest", true}, // Ollama may append :latest
		{"gpt-oss:20b", false},
		{"qwen3:14b", false},
		{"granite4:latest", false}, // different tag, not infra
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInfrastructureModel(tt.name); got != tt.want {
				t.Errorf("isInfrastructureModel(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHealthEndpoint(t *testing.T) {
	t.Run("all services up", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer traceServer.Close()

		ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaTagsResponse{Models: []OllamaModel{}})
		}))
		defer ollamaServer.Close()

		proxy := newTestProxy(traceServer.URL, ollamaServer.URL)
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var resp HealthResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Status != "healthy" {
			t.Errorf("expected status=healthy, got %s", resp.Status)
		}
		if resp.Proxy != "up" {
			t.Errorf("expected proxy=up, got %s", resp.Proxy)
		}
		if resp.TraceServer != "up" {
			t.Errorf("expected trace_server=up, got %s", resp.TraceServer)
		}
		if resp.Ollama != "up" {
			t.Errorf("expected ollama=up, got %s", resp.Ollama)
		}
	})

	t.Run("trace server down", func(t *testing.T) {
		ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaTagsResponse{Models: []OllamaModel{}})
		}))
		defer ollamaServer.Close()

		proxy := newTestProxy("http://localhost:1", ollamaServer.URL)
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var resp HealthResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Status != "degraded" {
			t.Errorf("expected status=degraded, got %s", resp.Status)
		}
		if resp.TraceServer != "down" {
			t.Errorf("expected trace_server=down, got %s", resp.TraceServer)
		}
		if resp.Ollama != "up" {
			t.Errorf("expected ollama=up, got %s", resp.Ollama)
		}
	})

	t.Run("ollama down", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://localhost:1")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var resp HealthResponse
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Status != "degraded" {
			t.Errorf("expected status=degraded, got %s", resp.Status)
		}
		if resp.TraceServer != "up" {
			t.Errorf("expected trace_server=up, got %s", resp.TraceServer)
		}
		if resp.Ollama != "down" {
			t.Errorf("expected ollama=down, got %s", resp.Ollama)
		}
	})
}

func TestProjectInit(t *testing.T) {
	t.Run("verify init forwards to trace server", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/trace/init" {
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}

			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["project_root"] != "/my/project" {
				t.Errorf("expected project_root=/my/project, got %s", body["project_root"])
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"graph_id": "graph-123",
				"status":   "initialized",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		reqBody := `{"project_root": "/my/project"}`
		req := httptest.NewRequest(http.MethodPost, "/init", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]string
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp["graph_id"] != "graph-123" {
			t.Errorf("expected graph_id=graph-123, got %s", resp["graph_id"])
		}
	})
}

func TestInitWithPathTranslation(t *testing.T) {
	t.Run("init translates project_root and preserves extra fields", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/trace/init" {
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.Error(w, "not found", http.StatusNotFound)
				return
			}

			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

			if body["project_root"] != "/projects/AleutianFOSS" {
				t.Errorf("expected translated project_root=/projects/AleutianFOSS, got %s", body["project_root"])
			}
			// Verify extra fields are preserved through decode-re-encode.
			langs, ok := body["languages"]
			if !ok {
				t.Error("expected languages field to be preserved")
			} else {
				langSlice := langs.([]interface{})
				if len(langSlice) != 2 || langSlice[0] != "go" || langSlice[1] != "python" {
					t.Errorf("expected languages=[go,python], got %v", langs)
				}
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "initialized"})
		}))
		defer traceServer.Close()

		proxy := NewProxyServer(ProxyConfig{
			ListenAddr:      ":0",
			TraceURL:        traceServer.URL,
			Timeout:         30 * time.Second,
			HostPrefix:      "/Users/jin/GolandProjects",
			ContainerPrefix: "/projects",
		})
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		reqBody := `{"project_root": "/Users/jin/GolandProjects/AleutianFOSS", "languages": ["go", "python"]}`
		req := httptest.NewRequest(http.MethodPost, "/init", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestStreaming(t *testing.T) {
	t.Run("stream=true returns SSE format with buffered response", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID:  "sess-005",
				State:      "COMPLETE",
				StepsTaken: 2,
				Response:   "parseConfig has 3 callers.",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Find callers of parseConfig"},
		}, true)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType != "text/event-stream" {
			t.Errorf("expected Content-Type=text/event-stream, got %s", contentType)
		}

		responseBody := rec.Body.String()
		if !strings.Contains(responseBody, "[DONE]") {
			t.Error("expected [DONE] marker in SSE response")
		}
		if !strings.Contains(responseBody, "parseConfig has 3 callers") {
			t.Errorf("expected response content in SSE data, got: %s", responseBody)
		}

		// Verify the SSE data chunk is valid JSON.
		lines := strings.Split(responseBody, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
				jsonData := strings.TrimPrefix(line, "data: ")
				var chunk ChatCompletionChunk
				if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
					t.Errorf("SSE chunk is not valid JSON: %v", err)
				}
				if chunk.Object != "chat.completion.chunk" {
					t.Errorf("expected object=chat.completion.chunk, got %s", chunk.Object)
				}
			}
		}
	})

	t.Run("stream=true sends heartbeats during slow agent call", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate a slow agent call (3 seconds → at least 1 heartbeat).
			time.Sleep(3 * time.Second)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID:  "sess-heartbeat",
				State:      "COMPLETE",
				StepsTaken: 5,
				Response:   "Done after slow processing.",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Slow query for heartbeat test"},
		}, true)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		responseBody := rec.Body.String()

		// Verify heartbeat comments were sent.
		heartbeatCount := strings.Count(responseBody, ": processing")
		if heartbeatCount == 0 {
			t.Errorf("expected at least 1 heartbeat comment, got 0. Body:\n%s", responseBody)
		}

		// Verify the final response is still present.
		if !strings.Contains(responseBody, "Done after slow processing") {
			t.Errorf("expected response content after heartbeats, got:\n%s", responseBody)
		}
		if !strings.Contains(responseBody, "[DONE]") {
			t.Error("expected [DONE] marker after heartbeats")
		}
	})

	t.Run("stream=true with agent error emits error as SSE data", func(t *testing.T) {
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID: "sess-stream-err",
				State:     "ERROR",
				Error:     "graph not initialized",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Query that errors"},
		}, true)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		// SSE headers already sent — status is 200 even for errors.
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 (SSE), got %d", rec.Code)
		}

		responseBody := rec.Body.String()
		if !strings.Contains(responseBody, "graph not initialized") {
			t.Errorf("expected error message in SSE data, got:\n%s", responseBody)
		}
		if !strings.Contains(responseBody, "[DONE]") {
			t.Error("expected [DONE] after error")
		}
	})
}

func TestProjectRootHeader(t *testing.T) {
	t.Run("X-Project-Root header overrides default", func(t *testing.T) {
		var receivedRoot string
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			receivedRoot = req["project_root"]

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agentRunResponse{
				SessionID: "sess-006",
				State:     "COMPLETE",
				Response:  "Done.",
			})
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Show callers"},
		}, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Project-Root", "/override/project")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if receivedRoot != "/override/project" {
			t.Errorf("expected project_root=/override/project, got %s", receivedRoot)
		}
	})
}

func TestSessionTTLCleanup(t *testing.T) {
	t.Run("expired sessions get new agent runs", func(t *testing.T) {
		runCount := 0
		traceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/trace/agent/run" {
				runCount++
				json.NewEncoder(w).Encode(agentRunResponse{
					SessionID: fmt.Sprintf("sess-%d", runCount),
					State:     "COMPLETE",
					Response:  "Done.",
				})
			} else {
				t.Errorf("unexpected path after TTL expiry: %s", r.URL.Path)
				http.Error(w, "unexpected", http.StatusInternalServerError)
			}
		}))
		defer traceServer.Close()

		proxy := newTestProxy(traceServer.URL, "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		// First request creates session.
		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "First message for TTL test"},
		}, false)
		req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req1.Header.Set("Content-Type", "application/json")
		rec1 := httptest.NewRecorder()
		mux.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusOK {
			t.Fatalf("first request: expected 200, got %d", rec1.Code)
		}

		// Manually expire the session by modifying lastUsed.
		threadKey := computeThreadKey([]ChatMessage{
			{Role: "user", Content: "First message for TTL test"},
		})
		if entry, ok := proxy.sessions.Load(threadKey); ok {
			se := entry.(*sessionEntry)
			se.lastUsed = time.Now().Add(-2 * time.Hour) // Expire it.
		}

		// Second request: same thread key but expired → should call /run again.
		req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		rec2 := httptest.NewRecorder()
		mux.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Fatalf("second request: expected 200, got %d", rec2.Code)
		}

		if runCount != 2 {
			t.Errorf("expected 2 /run calls (expired session), got %d", runCount)
		}
	})
}

func TestNoUserMessage(t *testing.T) {
	t.Run("request with no user message returns 400", func(t *testing.T) {
		proxy := newTestProxy("http://unused", "http://unused")
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "system", Content: "You are helpful."},
		}, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})
}

func TestNoProjectRoot(t *testing.T) {
	t.Run("no project root configured returns 400", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr: ":0",
			TraceURL:   "http://unused",
			OllamaURL:  "http://unused",
			Timeout:    30 * time.Second,
			// No ProjectRoot set.
		})
		mux := http.NewServeMux()
		proxy.RegisterRoutes(mux)

		body := chatRequest([]ChatMessage{
			{Role: "user", Content: "Hello"},
		}, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestTranslatePath(t *testing.T) {
	t.Run("no config passthrough", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr: ":0",
			Timeout:    30 * time.Second,
		})
		got := proxy.translatePath("/Users/jin/GolandProjects/AleutianFOSS")
		if got != "/Users/jin/GolandProjects/AleutianFOSS" {
			t.Errorf("expected passthrough, got %s", got)
		}
	})

	t.Run("basic translation", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr:      ":0",
			Timeout:         30 * time.Second,
			HostPrefix:      "/Users/jin/GolandProjects",
			ContainerPrefix: "/projects",
		})
		got := proxy.translatePath("/Users/jin/GolandProjects/AleutianFOSS")
		expected := "/projects/AleutianFOSS"
		if got != expected {
			t.Errorf("expected %s, got %s", expected, got)
		}
	})

	t.Run("non-matching prefix passthrough", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr:      ":0",
			Timeout:         30 * time.Second,
			HostPrefix:      "/Users/jin/GolandProjects",
			ContainerPrefix: "/projects",
		})
		got := proxy.translatePath("/other/path/to/project")
		if got != "/other/path/to/project" {
			t.Errorf("expected passthrough for non-matching prefix, got %s", got)
		}
	})

	t.Run("exact prefix match", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr:      ":0",
			Timeout:         30 * time.Second,
			HostPrefix:      "/Users/jin/GolandProjects",
			ContainerPrefix: "/projects",
		})
		got := proxy.translatePath("/Users/jin/GolandProjects")
		expected := "/projects"
		if got != expected {
			t.Errorf("expected %s, got %s", expected, got)
		}
	})

	t.Run("overlapping prefix not on directory boundary passthrough", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr:      ":0",
			Timeout:         30 * time.Second,
			HostPrefix:      "/Users/jin/GolandProjects",
			ContainerPrefix: "/projects",
		})
		got := proxy.translatePath("/Users/jin/GolandProjectsEvil/malicious")
		if got != "/Users/jin/GolandProjectsEvil/malicious" {
			t.Errorf("expected passthrough for non-boundary prefix overlap, got %s", got)
		}
	})

	t.Run("only host set no container passthrough", func(t *testing.T) {
		proxy := NewProxyServer(ProxyConfig{
			ListenAddr: ":0",
			Timeout:    30 * time.Second,
			HostPrefix: "/Users/jin/GolandProjects",
		})
		got := proxy.translatePath("/Users/jin/GolandProjects/AleutianFOSS")
		if got != "/Users/jin/GolandProjects/AleutianFOSS" {
			t.Errorf("expected passthrough when ContainerPrefix empty, got %s", got)
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("extractLastUserMessage", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "First question"},
			{Role: "assistant", Content: "First answer"},
			{Role: "user", Content: "Second question"},
		}
		got := extractLastUserMessage(messages)
		if got != "Second question" {
			t.Errorf("expected 'Second question', got '%s'", got)
		}
	})

	t.Run("extractLastUserMessage empty", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "system", Content: "You are helpful."},
		}
		got := extractLastUserMessage(messages)
		if got != "" {
			t.Errorf("expected empty, got '%s'", got)
		}
	})

	t.Run("computeThreadKey stable", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "user", Content: "Hello world"},
			{Role: "assistant", Content: "Hi!"},
			{Role: "user", Content: "Follow up"},
		}
		key1 := computeThreadKey(messages)
		key2 := computeThreadKey(messages)
		if key1 != key2 {
			t.Errorf("thread keys should be stable, got %s and %s", key1, key2)
		}
		if key1 == "no-user-msg" {
			t.Error("expected a hash, got no-user-msg")
		}
	})

	t.Run("computeThreadKey no user messages", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "system", Content: "System prompt"},
		}
		key := computeThreadKey(messages)
		if key != "no-user-msg" {
			t.Errorf("expected 'no-user-msg', got '%s'", key)
		}
	})
}

func TestCleanupExpiredSessions(t *testing.T) {
	t.Run("expired sessions are removed", func(t *testing.T) {
		proxy := newTestProxy("http://unused", "http://unused")

		// Add a fresh session and an expired one.
		proxy.sessions.Store("fresh", &sessionEntry{
			sessionID: "sess-fresh",
			lastUsed:  time.Now(),
		})
		proxy.sessions.Store("expired", &sessionEntry{
			sessionID: "sess-expired",
			lastUsed:  time.Now().Add(-2 * time.Hour),
		})

		proxy.CleanupExpiredSessions()

		if _, ok := proxy.sessions.Load("fresh"); !ok {
			t.Error("fresh session should not have been removed")
		}
		if _, ok := proxy.sessions.Load("expired"); ok {
			t.Error("expired session should have been removed")
		}
	})
}

func TestDetectProjectLanguages(t *testing.T) {
	t.Run("go project", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "main.go"))
		touch(t, filepath.Join(dir, "handler.go"))

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "go" {
			t.Errorf("expected [go], got %v", langs)
		}
	})

	t.Run("python project", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "app.py"))
		touch(t, filepath.Join(dir, "utils.py"))

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "python" {
			t.Errorf("expected [python], got %v", langs)
		}
	})

	t.Run("typescript project", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "index.ts"))
		touch(t, filepath.Join(dir, "component.tsx"))

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "typescript" {
			t.Errorf("expected [typescript], got %v", langs)
		}
	})

	t.Run("javascript project", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "index.js"))
		touch(t, filepath.Join(dir, "utils.mjs"))

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "javascript" {
			t.Errorf("expected [javascript], got %v", langs)
		}
	})

	t.Run("mixed go and python", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "main.go"))
		touch(t, filepath.Join(dir, "script.py"))

		langs := detectProjectLanguages(dir)
		sort.Strings(langs)
		if len(langs) != 2 || langs[0] != "go" || langs[1] != "python" {
			t.Errorf("expected [go python], got %v", langs)
		}
	})

	t.Run("empty directory returns go fallback", func(t *testing.T) {
		dir := t.TempDir()

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "go" {
			t.Errorf("expected [go] fallback, got %v", langs)
		}
	})

	t.Run("skips node_modules", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "main.go"))
		nmDir := filepath.Join(dir, "node_modules", "dep")
		if err := os.MkdirAll(nmDir, 0o755); err != nil {
			t.Fatal(err)
		}
		touch(t, filepath.Join(nmDir, "index.js"))

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "go" {
			t.Errorf("expected [go] (node_modules skipped), got %v", langs)
		}
	})

	t.Run("skips vendor and venv", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "app.py"))
		for _, skip := range []string{"vendor", ".venv"} {
			d := filepath.Join(dir, skip)
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			touch(t, filepath.Join(d, "lib.go"))
		}

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "python" {
			t.Errorf("expected [python] (vendor/.venv skipped), got %v", langs)
		}
	})

	t.Run("respects max depth", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "main.go"))
		deep := filepath.Join(dir, "a", "b", "c", "d", "e", "deep.py")
		touch(t, deep)

		langs := detectProjectLanguages(dir)
		if len(langs) != 1 || langs[0] != "go" {
			t.Errorf("expected [go] (deep .py skipped), got %v", langs)
		}
	})

	t.Run("case insensitive extensions", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "Main.GO"))
		touch(t, filepath.Join(dir, "App.PY"))

		langs := detectProjectLanguages(dir)
		sort.Strings(langs)
		if len(langs) != 2 || langs[0] != "go" || langs[1] != "python" {
			t.Errorf("expected [go python], got %v", langs)
		}
	})

	t.Run("nonexistent directory returns go fallback", func(t *testing.T) {
		langs := detectProjectLanguages("/nonexistent/path/abc123")
		if len(langs) != 1 || langs[0] != "go" {
			t.Errorf("expected [go] fallback, got %v", langs)
		}
	})
}

func TestIsUIMetaRequest(t *testing.T) {
	t.Run("title generation", func(t *testing.T) {
		query := `### Task:
Generate a concise, 3-5 word title with an emoji summarizing the chat history.
### Chat History:
<chat_history>
USER: Tell me about read_csv
ASSISTANT: read_csv calls 6 functions...
</chat_history>`
		if !isUIMetaRequest(query) {
			t.Error("expected title generation to be detected as UI meta-request")
		}
	})

	t.Run("follow-up suggestions", func(t *testing.T) {
		query := `### Task:
Suggest 3-5 relevant follow-up questions or prompts that the user might naturally ask next.
### Chat History:
<chat_history>
USER: What calls parseConfig?
</chat_history>`
		if !isUIMetaRequest(query) {
			t.Error("expected follow-up suggestions to be detected as UI meta-request")
		}
	})

	t.Run("real code question", func(t *testing.T) {
		query := "What functions call read_csv in this project?"
		if isUIMetaRequest(query) {
			t.Error("real code question should not be detected as UI meta-request")
		}
	})

	t.Run("long but real question", func(t *testing.T) {
		query := "I have a function that processes data from a CSV file using pandas read_csv with parameters like encoding, sep, dtype, and na_values. The function seems to hang when the file is larger than 2GB. Can you find all callers of this function and check if any of them also handle large files?"
		if isUIMetaRequest(query) {
			t.Error("long real question should not be detected as UI meta-request")
		}
	})

	t.Run("single pattern not enough", func(t *testing.T) {
		// A query that mentions "title" in a code context should not trigger.
		query := `How is the "title" field used in the page component?`
		if isUIMetaRequest(query) {
			t.Error("single pattern match should not trigger meta-request detection")
		}
	})
}

func TestForwardToOllama(t *testing.T) {
	t.Run("forwards meta-request to ollama", func(t *testing.T) {
		ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/chat" {
				t.Errorf("expected /api/chat, got %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ollamaChatResponse{
				Message: ollamaChatMessage{
					Role:    "assistant",
					Content: `{ "title": "📊 CSV Analysis Functions" }`,
				},
			})
		}))
		defer ollamaSrv.Close()

		// Trace server should NOT be called.
		traceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("trace server should not be called for meta-requests")
		}))
		defer traceSrv.Close()

		proxy := newTestProxy(traceSrv.URL, ollamaSrv.URL)
		proxy.config.ProjectRoot = "/projects"

		body := `{"model":"gemma3n:latest","messages":[{"role":"user","content":"Tell me about read_csv"},{"role":"assistant","content":"read_csv calls..."},{"role":"user","content":"### Task:\nGenerate a concise, 3-5 word title summarizing the chat history.\n### Chat History:\n<chat_history>\nUSER: Tell me about read_csv\n</chat_history>"}]}`

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		proxy.handleChatCompletions(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp ChatCompletionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if len(resp.Choices) == 0 {
			t.Fatal("expected at least one choice")
		}
		if !strings.Contains(resp.Choices[0].Message.Content, "CSV") {
			t.Errorf("expected ollama response content, got: %s", resp.Choices[0].Message.Content)
		}
	})
}

// touch creates an empty file, creating parent directories as needed.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
