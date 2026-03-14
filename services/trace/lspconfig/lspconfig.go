// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package lspconfig provides LSP configuration types for Trace.
// Previously in cmd/aleutian/config; internalized here to decouple
// trace from the aleutian CLI package.
//
// Thread Safety:
//
//	All types are immutable after construction unless documented otherwise.
package lspconfig

import (
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// LSPConfig holds configuration for the Language Server Protocol enrichment pipeline.
//
// Description:
//
//	GR-75: Controls which language servers are activated and their timeouts.
//
// Thread Safety:
//
//	Immutable after construction.
type LSPConfig struct {
	// Enabled toggles the entire LSP enrichment pipeline.
	// Default: false (CLI), true (container, via LSP_ENABLED env var)
	Enabled bool `yaml:"enabled"`

	// PythonEnabled toggles Pyright for Python projects.
	// Default: true when Enabled is true
	PythonEnabled bool `yaml:"python_enabled"`

	// TypeScriptEnabled toggles typescript-language-server for TS projects.
	// Default: true when Enabled is true
	TypeScriptEnabled bool `yaml:"typescript_enabled"`

	// StartupTimeout is the maximum time to wait for a language server to start.
	// Default: 30s
	StartupTimeout time.Duration `yaml:"startup_timeout"`

	// RequestTimeout is the maximum time for a single LSP query.
	// Default: 5s
	RequestTimeout time.Duration `yaml:"request_timeout"`

	// IdleTimeout is how long an idle language server stays alive before shutdown.
	// Default: 10 minutes
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

// DefaultLSPConfig returns the default LSP configuration (disabled, for CLI mode).
//
// Description:
//
//	Returns a config with LSP disabled and sensible timeout defaults.
//	Container mode overrides Enabled via LSP_ENABLED env var.
//
// Outputs:
//
//	LSPConfig - Default configuration with LSP disabled.
func DefaultLSPConfig() LSPConfig {
	return LSPConfig{
		Enabled:           false,
		PythonEnabled:     true,
		TypeScriptEnabled: true,
		StartupTimeout:    30 * time.Second,
		RequestTimeout:    5 * time.Second,
		IdleTimeout:       10 * time.Minute,
	}
}

// LSPConfigFromEnv reads LSP configuration from environment variables.
//
// Description:
//
//	GR-75: Reads LSP_ENABLED, LSP_PYTHON_ENABLED, LSP_TYPESCRIPT_ENABLED,
//	LSP_STARTUP_TIMEOUT_SECONDS, and LSP_REQUEST_TIMEOUT_SECONDS from the
//	environment. Called during service initialization in container mode.
//
// Outputs:
//
//	LSPConfig - Configuration populated from environment variables.
//
// Thread Safety:
//
//	Safe for concurrent use (reads only environment).
func LSPConfigFromEnv() LSPConfig {
	cfg := DefaultLSPConfig()
	cfg.Enabled = os.Getenv("LSP_ENABLED") == "true"

	// PythonEnabled defaults to true; only disable if explicitly set to "false"
	if os.Getenv("LSP_PYTHON_ENABLED") == "false" {
		cfg.PythonEnabled = false
	}

	// TypeScriptEnabled defaults to true; only disable if explicitly set to "false"
	if os.Getenv("LSP_TYPESCRIPT_ENABLED") == "false" {
		cfg.TypeScriptEnabled = false
	}

	if v := os.Getenv("LSP_STARTUP_TIMEOUT_SECONDS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.StartupTimeout = time.Duration(d) * time.Second
		}
	}
	if v := os.Getenv("LSP_REQUEST_TIMEOUT_SECONDS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.RequestTimeout = time.Duration(d) * time.Second
		}
	}

	return cfg
}

// LSPVerifyResult holds the outcome of language server binary verification.
//
// Description:
//
//	GR-75: Returned by LSPServerVerifier.Verify() to indicate which language
//	servers are available in the runtime environment.
//
// Thread Safety:
//
//	Immutable after construction.
type LSPVerifyResult struct {
	// PythonAvailable is true if pyright-langserver is found in PATH.
	PythonAvailable bool

	// TypeScriptAvailable is true if typescript-language-server is found in PATH.
	TypeScriptAvailable bool

	// Warnings contains messages for missing binaries.
	Warnings []string
}

// LSPServerVerifier checks that required language server binaries exist.
//
// Description:
//
//	GR-75: Called during service startup when LSP is enabled. Checks
//	exec.LookPath for each enabled language's binary. Missing binaries
//	disable that language's enrichment gracefully (no startup failure).
//
// Thread Safety:
//
//	Safe for concurrent use (stateless, reads config and PATH only).
type LSPServerVerifier struct {
	config LSPConfig

	// lookPath is the function used to locate binaries. Defaults to
	// exec.LookPath. Overridable for testing.
	lookPath func(file string) (string, error)
}

// NewLSPServerVerifier creates a new verifier for the given config.
//
// Description:
//
//	Creates a verifier that uses exec.LookPath to check for language
//	server binaries.
//
// Inputs:
//
//	config - LSP configuration to verify against.
//
// Outputs:
//
//	*LSPServerVerifier - The verifier instance.
func NewLSPServerVerifier(config LSPConfig) *LSPServerVerifier {
	return &LSPServerVerifier{
		config:   config,
		lookPath: exec.LookPath,
	}
}

// Verify checks that all enabled language server binaries are available.
//
// Description:
//
//	GR-75: For each enabled language, checks that the corresponding binary
//	exists in PATH via exec.LookPath. Returns which servers are available
//	and warnings for any that are missing. Also logs warnings via slog.
//
// Outputs:
//
//	LSPVerifyResult - Availability status and warnings.
//
// Thread Safety:
//
//	Safe for concurrent use.
func (v *LSPServerVerifier) Verify() LSPVerifyResult {
	result := LSPVerifyResult{}

	if v.config.PythonEnabled {
		_, err := v.lookPath("pyright-langserver")
		result.PythonAvailable = (err == nil)
		if !result.PythonAvailable {
			msg := "pyright-langserver not found in PATH; Python LSP enrichment disabled"
			result.Warnings = append(result.Warnings, msg)
			slog.Warn("GR-75: " + msg)
		}
	}

	if v.config.TypeScriptEnabled {
		_, err := v.lookPath("typescript-language-server")
		result.TypeScriptAvailable = (err == nil)
		if !result.TypeScriptAvailable {
			msg := "typescript-language-server not found in PATH; TypeScript LSP enrichment disabled"
			result.Warnings = append(result.Warnings, msg)
			slog.Warn("GR-75: " + msg)
		}
	}

	return result
}
