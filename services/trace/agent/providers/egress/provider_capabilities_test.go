// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"os"
	"testing"
)

func TestDefaultCapabilities_Claude(t *testing.T) {
	caps := DefaultCapabilities("anthropic", "claude-sonnet-4-20250514")

	if caps.MaxContextTokens != 200000 {
		t.Errorf("expected MaxContextTokens=200000, got %d", caps.MaxContextTokens)
	}
	if !caps.CanReceiveFileSystemInfo {
		t.Error("expected CanReceiveFileSystemInfo=true")
	}
	if caps.MaxToolResultTokens != 4000 {
		t.Errorf("expected MaxToolResultTokens=4000, got %d", caps.MaxToolResultTokens)
	}
	if caps.HistoryWindow != 20 {
		t.Errorf("expected HistoryWindow=20, got %d", caps.HistoryWindow)
	}
}

func TestDefaultCapabilities_GPT4o(t *testing.T) {
	caps := DefaultCapabilities("openai", "gpt-4o")

	if caps.MaxContextTokens != 128000 {
		t.Errorf("expected MaxContextTokens=128000, got %d", caps.MaxContextTokens)
	}
}

func TestDefaultCapabilities_GPT4oMini(t *testing.T) {
	caps := DefaultCapabilities("openai", "gpt-4o-mini")

	if caps.MaxContextTokens != 128000 {
		t.Errorf("expected MaxContextTokens=128000 for gpt-4o-mini, got %d", caps.MaxContextTokens)
	}
}

func TestDefaultCapabilities_OpenAI_O1(t *testing.T) {
	caps := DefaultCapabilities("openai", "o1-preview")

	if caps.MaxContextTokens != 200000 {
		t.Errorf("expected MaxContextTokens=200000 for o1, got %d", caps.MaxContextTokens)
	}
}

func TestDefaultCapabilities_Gemini(t *testing.T) {
	caps := DefaultCapabilities("gemini", "gemini-2.0-flash")

	if caps.MaxContextTokens != 2000000 {
		t.Errorf("expected MaxContextTokens=2000000, got %d", caps.MaxContextTokens)
	}
}

func TestDefaultCapabilities_Ollama(t *testing.T) {
	caps := DefaultCapabilities("ollama", "llama3")

	if caps.MaxContextTokens != 8192 {
		t.Errorf("expected MaxContextTokens=8192, got %d", caps.MaxContextTokens)
	}
}

func TestDefaultCapabilities_Unknown(t *testing.T) {
	caps := DefaultCapabilities("unknown-provider", "some-model")

	if caps.MaxContextTokens != 128000 {
		t.Errorf("expected MaxContextTokens=128000 for unknown provider, got %d", caps.MaxContextTokens)
	}
}

func TestLoadCapabilitiesFromEnv(t *testing.T) {
	// Set env vars for anthropic
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_ANTHROPIC_MAX_CONTEXT_TOKENS", "150000")
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_ANTHROPIC_CAN_RECEIVE_FS_INFO", "false")
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_ANTHROPIC_MAX_TOOL_RESULT_TOKENS", "2000")
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_ANTHROPIC_HISTORY_WINDOW", "10")

	caps := LoadCapabilitiesFromEnv("anthropic", "claude-sonnet-4-20250514")

	if caps.MaxContextTokens != 150000 {
		t.Errorf("expected MaxContextTokens=150000, got %d", caps.MaxContextTokens)
	}
	if caps.CanReceiveFileSystemInfo {
		t.Error("expected CanReceiveFileSystemInfo=false after env override")
	}
	if caps.MaxToolResultTokens != 2000 {
		t.Errorf("expected MaxToolResultTokens=2000, got %d", caps.MaxToolResultTokens)
	}
	if caps.HistoryWindow != 10 {
		t.Errorf("expected HistoryWindow=10, got %d", caps.HistoryWindow)
	}
}

func TestLoadCapabilitiesFromEnv_InvalidValues(t *testing.T) {
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_OPENAI_MAX_CONTEXT_TOKENS", "not-a-number")

	caps := LoadCapabilitiesFromEnv("openai", "gpt-4o")

	// Should keep defaults when env vars are invalid
	if caps.MaxContextTokens != 128000 {
		t.Errorf("expected default MaxContextTokens=128000 with invalid env, got %d", caps.MaxContextTokens)
	}
}

func TestLoadCapabilitiesFromEnv_NoEnvVars(t *testing.T) {
	// Clear any potentially set env vars
	os.Unsetenv("TRACE_PROVIDER_CAPABILITIES_GEMINI_MAX_CONTEXT_TOKENS")

	caps := LoadCapabilitiesFromEnv("gemini", "gemini-2.0-flash")

	// Should return defaults
	if caps.MaxContextTokens != 2000000 {
		t.Errorf("expected default MaxContextTokens=2000000, got %d", caps.MaxContextTokens)
	}
}

func TestLoadCapabilitiesFromEnv_ZeroMaxToolResultTokens(t *testing.T) {
	t.Setenv("TRACE_PROVIDER_CAPABILITIES_ANTHROPIC_MAX_TOOL_RESULT_TOKENS", "0")

	caps := LoadCapabilitiesFromEnv("anthropic", "claude-sonnet-4-20250514")

	// 0 means unlimited, should be accepted
	if caps.MaxToolResultTokens != 0 {
		t.Errorf("expected MaxToolResultTokens=0 (unlimited), got %d", caps.MaxToolResultTokens)
	}
}
