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
	"strconv"
	"strings"
)

// ProviderCapabilities describes the data handling capabilities and limits
// for a specific LLM provider. Used by the DataMinimizer to decide what
// content to include, truncate, or strip before sending requests externally.
//
// Thread Safety: ProviderCapabilities is a value type. Safe to copy and share.
type ProviderCapabilities struct {
	// MaxContextTokens is the maximum context window size for the provider/model.
	// Claude: 200000, GPT-4o: 128000, Gemini 2.0: 2000000, Ollama: model-specific.
	MaxContextTokens int

	// CanReceiveFileSystemInfo controls whether absolute file paths are sent.
	// When false, absolute paths are replaced with relative paths (./...).
	// Default: true.
	CanReceiveFileSystemInfo bool

	// MaxToolResultTokens caps the size of individual tool result content.
	// Results exceeding this limit are truncated with a notice.
	// 0 means unlimited. Default: 4000.
	MaxToolResultTokens int

	// HistoryWindow is the number of recent conversation turns kept verbatim.
	// Turns older than this are compressed to summaries.
	// Default: 20.
	HistoryWindow int
}

// DefaultCapabilities returns sensible default capabilities for a provider/model.
//
// Description:
//
//	Returns provider-specific defaults based on known model capabilities.
//	For unknown providers or models, returns conservative defaults.
//
// Inputs:
//   - provider: The provider name (e.g., "anthropic", "openai", "gemini", "ollama").
//   - model: The model name (e.g., "claude-sonnet-4-20250514", "gpt-4o").
//
// Outputs:
//   - ProviderCapabilities: Capabilities with provider-appropriate defaults.
//
// Assumptions:
//   - Provider names are lowercase.
//   - Unknown providers get conservative defaults (128K context, 4K tool results).
func DefaultCapabilities(provider, model string) ProviderCapabilities {
	defaults := ProviderCapabilities{
		CanReceiveFileSystemInfo: true,
		MaxToolResultTokens:      4000,
		HistoryWindow:            20,
	}

	switch provider {
	case "anthropic":
		defaults.MaxContextTokens = 200000
	case "openai":
		defaults.MaxContextTokens = 128000
		if strings.Contains(model, "o1") || strings.Contains(model, "o3") {
			defaults.MaxContextTokens = 200000
		}
	case "gemini":
		defaults.MaxContextTokens = 2000000
	case "ollama":
		// Ollama models vary widely; default to a conservative 8K.
		// This is typically overridden per-model at runtime.
		defaults.MaxContextTokens = 8192
	default:
		defaults.MaxContextTokens = 128000
	}

	return defaults
}

// LoadCapabilitiesFromEnv reads provider-specific capability overrides from
// environment variables.
//
// Description:
//
//	Reads TRACE_PROVIDER_CAPABILITIES_<PROVIDER>_* environment variables
//	and overrides the corresponding default capabilities. Unset variables
//	leave the default in place.
//
// Environment variables:
//   - TRACE_PROVIDER_CAPABILITIES_<PROVIDER>_MAX_CONTEXT_TOKENS
//   - TRACE_PROVIDER_CAPABILITIES_<PROVIDER>_CAN_RECEIVE_FS_INFO
//   - TRACE_PROVIDER_CAPABILITIES_<PROVIDER>_MAX_TOOL_RESULT_TOKENS
//   - TRACE_PROVIDER_CAPABILITIES_<PROVIDER>_HISTORY_WINDOW
//
// Inputs:
//   - provider: The provider name (used to construct env var keys).
//   - model: The model name (passed to DefaultCapabilities for base values).
//
// Outputs:
//   - ProviderCapabilities: Capabilities with env var overrides applied.
func LoadCapabilitiesFromEnv(provider, model string) ProviderCapabilities {
	caps := DefaultCapabilities(provider, model)
	prefix := "TRACE_PROVIDER_CAPABILITIES_" + strings.ToUpper(provider) + "_"

	if val := os.Getenv(prefix + "MAX_CONTEXT_TOKENS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			caps.MaxContextTokens = n
		}
	}

	if val := os.Getenv(prefix + "CAN_RECEIVE_FS_INFO"); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			caps.CanReceiveFileSystemInfo = b
		}
	}

	if val := os.Getenv(prefix + "MAX_TOOL_RESULT_TOKENS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			caps.MaxToolResultTokens = n
		}
	}

	if val := os.Getenv(prefix + "HISTORY_WINDOW"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			caps.HistoryWindow = n
		}
	}

	return caps
}
