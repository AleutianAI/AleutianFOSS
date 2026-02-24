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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSendRAGRequest preserves your existing RAG logic test
func TestSendRAGRequest(t *testing.T) {
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rag" {
			t.Errorf("Expected path /v1/rag, got %s", r.URL.Path)
		}
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody["query"] != "Test Question" {
			t.Errorf("Expected query 'Test Question', got %v", reqBody["query"])
		}
		resp := map[string]interface{}{
			"answer":     "This is a mock answer",
			"session_id": "mock-session-123",
			"sources": []map[string]interface{}{
				{"source": "doc1.txt", "score": 0.95},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockOrchestrator.Close()

	os.Setenv("ALEUTIAN_ORCHESTRATOR_URL", mockOrchestrator.URL)
	defer os.Unsetenv("ALEUTIAN_ORCHESTRATOR_URL")

	response, err := sendRAGRequest("Test Question", "session-1", "standard")

	if err != nil {
		t.Fatalf("sendRAGRequest returned error: %v", err)
	}
	if response.Answer != "This is a mock answer" {
		t.Errorf("Expected answer 'This is a mock answer', got '%s'", response.Answer)
	}
	if len(response.Sources) != 1 {
		t.Errorf("Expected 1 source, got %d", len(response.Sources))
	}
}

// TestClientSideToolsSecurity verifies our new Path rules
func TestClientSideToolsSecurity(t *testing.T) {
	// Create a dummy file in the current temp dir for valid read tests
	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "secret.txt")
	os.WriteFile(secretFile, []byte("super secret"), 0644)

	tests := []struct {
		name      string
		tool      string
		input     string
		wantError bool
	}{
		{"Read Local File", "read_file", "go.mod", false},                   // Valid relative path
		{"Read /tmp File", "read_file", "/tmp/somefile.log", false},         // Valid absolute exception
		{"Read /tmp Traversal", "read_file", "/tmp/../../etc/passwd", true}, // Malicious /tmp
		{"Read Absolute Path", "read_file", "/etc/passwd", true},            // Blocked absolute
		{"Read Parent Dir", "read_file", "../secret.txt", true},             // Blocked relative traversal
		{"List Valid Dir", "list_files", ".", false},
		{"List Root Dir", "list_files", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result string
			// NOTE: We test the security logic wrapper, not the OS call success
			// So if it returns "Error: Access Denied", that's what we check.
			if tt.tool == "read_file" {
				result = readFileSafe(tt.input, "")
			} else {
				result = listFilesSafe(tt.input, "")
			}

			if tt.wantError {
				if !strings.Contains(result, "Access Denied") {
					t.Errorf("Expected 'Access Denied' for input '%s', got: '%s'", tt.input, result)
				}
			} else {
				if strings.Contains(result, "Access Denied") {
					t.Errorf("Expected allowed access for input '%s', got Blocked: '%s'", tt.input, result)
				}
			}
		})
	}
}
