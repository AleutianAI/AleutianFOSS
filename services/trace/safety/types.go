// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package safety provides security analysis tools for Trace.
//
// # Description
//
// This package implements trust flow analysis, vulnerability scanning,
// error handling auditing, secret detection, auth enforcement checking,
// and trust boundary analysis. It is the foundation for Aleutian's
// security-first approach to AI-assisted coding.
//
// # Thread Safety
//
// All types in this package are safe for concurrent use after initialization.
package safety

import (
	"errors"
	"time"
)

// TrustLevel classifies the trust zone of code or data.
//
// Description:
//
//	TrustLevel represents the inherent trustworthiness of a code region
//	or data source. Higher levels indicate more trusted code that should
//	be protected from lower-level inputs.
//
// Thread Safety:
//
//	TrustLevel is an immutable value type, safe for concurrent use.
type TrustLevel int

const (
	// TrustExternal represents untrusted external input.
	// Sources: HTTP requests, CLI args, file reads, environment variables.
	// Treatment: NEVER trust, always validate before use.
	TrustExternal TrustLevel = iota

	// TrustValidation represents validation/sanitization points.
	// Sources: Input validators, sanitizers, type converters.
	// Treatment: Data crossing this boundary should be validated.
	TrustValidation

	// TrustInternal represents internal business logic.
	// Sources: Service layer, domain logic, internal APIs.
	// Treatment: Conditionally trust, validate inputs from external.
	TrustInternal

	// TrustPrivileged represents admin/system operations.
	// Sources: Admin endpoints, system commands, privileged APIs.
	// Treatment: Highly trusted, protect from external access.
	TrustPrivileged
)

// String returns the string representation of TrustLevel.
func (t TrustLevel) String() string {
	switch t {
	case TrustExternal:
		return "EXTERNAL"
	case TrustValidation:
		return "VALIDATION"
	case TrustInternal:
		return "INTERNAL"
	case TrustPrivileged:
		return "PRIVILEGED"
	default:
		return "UNKNOWN"
	}
}

// DataTaint tracks the taintedness state of specific data.
//
// Description:
//
//	DataTaint represents whether data has been validated/sanitized.
//	This is used for taint tracking during trust flow analysis to
//	determine if untrusted data reaches sensitive sinks.
//
// Thread Safety:
//
//	DataTaint is an immutable value type, safe for concurrent use.
type DataTaint int

const (
	// TaintUnknown indicates the data has not been analyzed.
	TaintUnknown DataTaint = iota

	// TaintClean indicates the data is sanitized or a constant.
	// Safe to use in any sink.
	TaintClean

	// TaintUntrusted indicates the data is from an external source
	// and has not been sanitized. Dangerous if it reaches a sink.
	TaintUntrusted

	// TaintMixed indicates the data is merged from multiple sources
	// where at least one may be untrusted. Conservative assumption.
	TaintMixed
)

// String returns the string representation of DataTaint.
func (t DataTaint) String() string {
	switch t {
	case TaintClean:
		return "CLEAN"
	case TaintUntrusted:
		return "UNTRUSTED"
	case TaintMixed:
		return "MIXED"
	default:
		return "UNKNOWN"
	}
}

// MergeTaints returns the most conservative taint from multiple values.
//
// Description:
//
//	Implements the abstract interpretation lattice for taint states:
//	  UNTRUSTED ∪ CLEAN = UNTRUSTED (conservative)
//	  MIXED ∪ anything = MIXED (unless UNTRUSTED)
//	  UNKNOWN ∪ anything = that thing
//
// Inputs:
//
//	taints - Variable number of DataTaint values to merge.
//
// Outputs:
//
//	DataTaint - The most conservative (least trusted) taint state.
//
// Thread Safety:
//
//	This function is safe for concurrent use.
func MergeTaints(taints ...DataTaint) DataTaint {
	// UNTRUSTED is the most conservative
	for _, t := range taints {
		if t == TaintUntrusted {
			return TaintUntrusted
		}
	}
	// MIXED is next most conservative
	for _, t := range taints {
		if t == TaintMixed {
			return TaintMixed
		}
	}
	// CLEAN if all are clean
	for _, t := range taints {
		if t == TaintClean {
			return TaintClean
		}
	}
	return TaintUnknown
}

// Severity indicates the severity of a security finding.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
)

// Exploitability indicates how likely a vulnerability can be exploited.
type Exploitability string

const (
	ExploitabilityYes     Exploitability = "yes"     // Proven exploitable
	ExploitabilityNo      Exploitability = "no"      // Not exploitable
	ExploitabilityUnknown Exploitability = "unknown" // Cannot determine
)

// ResourceLimits defines memory and computation bounds for analysis.
//
// Description:
//
//	ResourceLimits prevents runaway analysis on large codebases by
//	enforcing hard limits on memory, nodes visited, depth, and time.
//
// Thread Safety:
//
//	ResourceLimits is an immutable value type, safe for concurrent use.
type ResourceLimits struct {
	// MaxMemoryBytes is the maximum memory to use (0 = unlimited).
	MaxMemoryBytes int64 `json:"max_memory_bytes"`

	// MaxNodes is the maximum graph nodes to visit.
	MaxNodes int `json:"max_nodes"`

	// MaxDepth is the maximum call chain depth to traverse.
	MaxDepth int `json:"max_depth"`

	// Timeout is the hard timeout for the operation.
	Timeout time.Duration `json:"timeout"`
}

// DefaultResourceLimits returns conservative defaults.
//
// Description:
//
//	Returns resource limits suitable for most analyses. These limits
//	balance thoroughness with performance.
//
// Outputs:
//
//	ResourceLimits - Default configuration with:
//	  - 256MB memory limit
//	  - 50,000 node limit
//	  - 20 depth limit
//	  - 30 second timeout
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		MaxMemoryBytes: 256 * 1024 * 1024, // 256MB
		MaxNodes:       50000,             // 50K nodes
		MaxDepth:       20,                // 20 hops
		Timeout:        30 * time.Second,  // 30s hard timeout
	}
}

// PartialFailure describes what couldn't be analyzed.
//
// Description:
//
//	PartialFailure records analysis gaps when part of the analysis
//	fails but the rest can continue. This enables graceful degradation
//	while maintaining transparency about what was missed.
type PartialFailure struct {
	// Scope is the file or function that failed.
	Scope string `json:"scope"`

	// Reason explains why the analysis failed.
	Reason string `json:"reason"`

	// Impact describes what security properties couldn't be verified.
	Impact string `json:"impact"`

	// Severity indicates how serious this gap is.
	Severity Severity `json:"severity"`
}

// Common errors for the safety package.
var (
	// ErrSymbolNotFound indicates the requested symbol doesn't exist.
	ErrSymbolNotFound = errors.New("symbol not found")

	// ErrGraphNotReady indicates the graph hasn't been frozen.
	ErrGraphNotReady = errors.New("graph not ready (not frozen)")

	// ErrInvalidInput indicates invalid input parameters.
	ErrInvalidInput = errors.New("invalid input")

	// ErrTimeoutExceeded indicates the operation timed out.
	ErrTimeoutExceeded = errors.New("analysis timeout exceeded")

	// ErrMaxNodesExceeded indicates the node limit was reached.
	ErrMaxNodesExceeded = errors.New("maximum nodes exceeded")

	// ErrMaxDepthExceeded indicates the depth limit was reached.
	ErrMaxDepthExceeded = errors.New("maximum depth exceeded")

	// ErrMemoryExceeded indicates the memory limit was reached.
	ErrMemoryExceeded = errors.New("memory limit exceeded")

	// ErrContextCanceled indicates the context was canceled.
	ErrContextCanceled = errors.New("context canceled")

	// ErrNoVulnerabilityFound indicates no matching vulnerability exists.
	ErrNoVulnerabilityFound = errors.New("no vulnerability found with that ID")
)
