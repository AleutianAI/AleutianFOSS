// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import (
	"regexp"
	"sync"
	"time"
)

// -----------------------------------------------------------------------------
// Trace Types
// -----------------------------------------------------------------------------

// TraceStep represents one step in the reasoning process.
//
// Description:
//
//	Captures what action was taken, what was found, and how CRS was updated.
//	Steps are recorded in order and can be exported for audit/debugging.
type TraceStep struct {
	// Step is the 1-indexed step number (assigned by recorder).
	Step int `json:"step"`

	// Timestamp is when this step occurred (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`

	// Action describes what was done (e.g., "explore", "analyze", "trace_flow").
	Action string `json:"action"`

	// Target is the file or symbol being operated on.
	Target string `json:"target"`

	// Tool is the tool that triggered this action (optional).
	Tool string `json:"tool,omitempty"`

	// Duration is how long this step took.
	Duration time.Duration `json:"duration_ms"`

	// SymbolsFound lists symbols discovered in this step.
	SymbolsFound []string `json:"symbols_found,omitempty"`

	// ProofUpdates lists proof status changes.
	ProofUpdates []ProofUpdate `json:"proof_updates,omitempty"`

	// ConstraintsAdded lists new constraints added.
	ConstraintsAdded []ConstraintUpdate `json:"constraints_added,omitempty"`

	// DependenciesFound lists new dependency edges found.
	DependenciesFound []DependencyEdge `json:"dependencies_found,omitempty"`

	// Error contains any error that occurred.
	Error string `json:"error,omitempty"`

	// Metadata contains additional step context.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NOTE: ProofUpdate, ConstraintUpdate, and DependencyEdge are now defined in types.go
// with typed fields instead of string fields. This change is part of CRS-02.

// TraceStepToStepRecord converts a lightweight TraceStep into a full StepRecord.
//
// Description:
//
//	Bridge function connecting the execute phase's TraceStep recording with the
//	CRS StepRecord system. Maps available TraceStep fields to StepRecord fields
//	and provides sensible defaults for fields not present in TraceStep.
//	This enables CRS.CountToolExecutions() to return accurate counts,
//	which the circuit breaker uses as a fallback when no proof data exists.
//
// Inputs:
//
//   - step: The TraceStep to convert. Should have non-empty Tool field for
//     the conversion to be meaningful for execution counting.
//   - sessionID: The session ID to attach. Must not be empty.
//
// Outputs:
//
//   - StepRecord: The converted record ready for CRS.RecordStep().
//
// Limitations:
//
//   - TraceStep does not carry Actor or Decision context; defaults to
//     ActorSystem / DecisionExecuteTool.
//   - ToolParams are not available in TraceStep; left nil in StepRecord.
//   - Confidence is not available; defaults to 0.
func TraceStepToStepRecord(step TraceStep, sessionID string) StepRecord {
	outcome := OutcomeSuccess
	errorMsg := ""
	var errorCat ErrorCategory
	if step.Error != "" {
		outcome = OutcomeFailure
		errorMsg = step.Error
		errorCat = ErrorCategoryInternal
	}

	ts := step.Timestamp
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}

	return StepRecord{
		StepNumber:       step.Step,
		Timestamp:        ts,
		SessionID:        sessionID,
		Actor:            ActorSystem,
		Decision:         DecisionExecuteTool,
		Tool:             step.Tool,
		Outcome:          outcome,
		ErrorMessage:     errorMsg,
		ErrorCategory:    errorCat,
		DurationMs:       step.Duration.Milliseconds(),
		ResultSummary:    step.Action + ": " + step.Target,
		Propagate:        true,
		ProofUpdates:     step.ProofUpdates,
		ConstraintsAdded: step.ConstraintsAdded,
	}
}

// ReasoningTrace is the exportable trace format.
type ReasoningTrace struct {
	// SessionID identifies the session this trace belongs to.
	SessionID string `json:"session_id"`

	// TotalSteps is the number of steps recorded.
	TotalSteps int `json:"total_steps"`

	// Duration is the total time from first to last step.
	Duration string `json:"total_duration"`

	// StartTime is when the first step occurred (Unix milliseconds UTC).
	StartTime int64 `json:"start_time,omitempty"`

	// EndTime is when the last step occurred (Unix milliseconds UTC).
	EndTime int64 `json:"end_time,omitempty"`

	// Trace contains all recorded steps.
	Trace []TraceStep `json:"trace"`
}

// -----------------------------------------------------------------------------
// TraceConfig
// -----------------------------------------------------------------------------

// TraceConfig configures trace recording behavior.
type TraceConfig struct {
	// MaxSteps limits trace size to prevent unbounded growth.
	// When exceeded, oldest steps are evicted.
	// Default: 1000.
	MaxSteps int

	// RecordSymbols enables recording of discovered symbols.
	// Default: true.
	RecordSymbols bool

	// RecordMetadata enables recording of step metadata.
	// Default: true.
	RecordMetadata bool

	// Sanitizer sanitizes trace data before recording.
	// If nil, no sanitization is performed.
	// SECURITY: Should be set to prevent secrets from leaking into traces.
	Sanitizer Sanitizer
}

// DefaultTraceConfig returns sensible defaults.
func DefaultTraceConfig() TraceConfig {
	return TraceConfig{
		MaxSteps:       1000,
		RecordSymbols:  true,
		RecordMetadata: true,
		Sanitizer:      nil, // Must be explicitly set for security
	}
}

// SecureTraceConfig returns config with secret sanitization enabled.
//
// Description:
//
//	Returns a TraceConfig with the default SecretSanitizer configured.
//	Use this configuration to prevent secrets from leaking into audit trails.
//
// Outputs:
//
//	TraceConfig - Configuration with sanitization enabled.
func SecureTraceConfig() TraceConfig {
	return TraceConfig{
		MaxSteps:       1000,
		RecordSymbols:  true,
		RecordMetadata: true,
		Sanitizer:      NewSecretSanitizer(),
	}
}

// -----------------------------------------------------------------------------
// Sanitizer Interface
// -----------------------------------------------------------------------------

// Sanitizer sanitizes sensitive data before recording.
//
// Description:
//
//	Implementations should redact secrets, PII, and other sensitive data
//	to prevent leakage into audit trails.
//
// Thread Safety:
//
//	Implementations must be safe for concurrent use.
type Sanitizer interface {
	// Sanitize redacts sensitive data from a string.
	//
	// Inputs:
	//   s - The string to sanitize.
	//
	// Outputs:
	//   string - The sanitized string with secrets replaced by [REDACTED].
	Sanitize(s string) string
}

// -----------------------------------------------------------------------------
// SecretSanitizer Implementation
// -----------------------------------------------------------------------------

// SecretSanitizer detects and redacts secrets from strings.
//
// Description:
//
//	SecretSanitizer uses regex patterns to detect common secret formats
//	including API keys, tokens, passwords, and private keys. Detected
//	secrets are replaced with [REDACTED].
//
// Thread Safety:
//
//	SecretSanitizer is safe for concurrent use after initialization.
type SecretSanitizer struct {
	patterns []*secretPattern
}

// secretPattern is a compiled secret detection pattern.
type secretPattern struct {
	name    string
	pattern *regexp.Regexp
}

// NewSecretSanitizer creates a new secret sanitizer with default patterns.
//
// Description:
//
//	Creates a sanitizer that detects common secret patterns including:
//	- AWS keys (AKIA*, ASIA*, etc.)
//	- Google Cloud API keys (AIza*)
//	- GitHub tokens (ghp_*, gho_*, etc.)
//	- Slack tokens (xox*)
//	- Private keys (-----BEGIN * PRIVATE KEY-----)
//	- Generic API keys and passwords
//	- Database connection strings with credentials
//	- JWT secrets
//
// Outputs:
//
//	*SecretSanitizer - The configured sanitizer.
func NewSecretSanitizer() *SecretSanitizer {
	patterns := []struct {
		name    string
		pattern string
	}{
		// AWS Keys
		{"aws_access_key", `(?:A3T[A-Z0-9]|AKIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[A-Z0-9]{16}`},
		{"aws_secret_key", `(?i)(?:aws)?[_-]?secret[_-]?(?:access)?[_-]?key\s*[=:]\s*["']?([a-zA-Z0-9/+=]{40})["']?`},

		// Google Cloud
		{"gcp_api_key", `AIza[0-9A-Za-z_-]{35}`},

		// GitHub
		{"github_token", `(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36,}`},
		{"github_pat", `github_pat_[a-zA-Z0-9]{22}_[a-zA-Z0-9]{59}`},

		// Slack
		{"slack_token", `xox[baprs]-[0-9a-zA-Z-]{10,}`},
		{"slack_webhook", `https://hooks\.slack\.com/services/T[0-9A-Z]+/B[0-9A-Z]+/[a-zA-Z0-9]+`},

		// Stripe
		{"stripe_key", `(?:sk|pk)_(?:live|test)_[0-9a-zA-Z]{24,}`},

		// Private Keys
		{"private_key", `-----BEGIN (?:RSA |DSA |EC |OPENSSH )?PRIVATE KEY-----`},
		{"private_key_pkcs8", `-----BEGIN PRIVATE KEY-----`},

		// Generic API Keys
		{"api_key", `(?i)(?:api[_-]?key|apikey)\s*[=:]\s*["']?([a-zA-Z0-9_\-]{20,})["']?`},

		// Passwords
		{"password", `(?i)(?:password|passwd|pwd)\s*[=:]\s*["']([^"']{8,})["']`},

		// Database Connection Strings
		{"database_url", `(?i)(?:postgres|mysql|mongodb|redis)://[^:]+:[^@]+@[^\s"']+`},

		// JWT Secrets
		{"jwt_secret", `(?i)(?:jwt[_-]?secret|signing[_-]?key)\s*[=:]\s*["']?([a-zA-Z0-9_\-]{20,})["']?`},

		// Generic Secrets and Tokens
		{"generic_secret", `(?i)(?:secret|token|credential)\s*[=:]\s*["']([a-zA-Z0-9_\-]{20,})["']`},

		// SendGrid
		{"sendgrid_key", `SG\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9_-]{43}`},

		// Twilio
		{"twilio_key", `SK[a-f0-9]{32}`},

		// NPM Token
		{"npm_token", `npm_[a-zA-Z0-9]{36}`},

		// PyPI Token
		{"pypi_token", `pypi-AgEIcHlwaS5vcmc[a-zA-Z0-9_-]+`},

		// Discord
		{"discord_token", `[MN][A-Za-z\d]{23,}\.[\w-]{6}\.[\w-]{27}`},

		// Heroku
		{"heroku_key", `(?i)heroku[_-]?(?:api)?[_-]?key\s*[=:]\s*["']?([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})["']?`},
	}

	sanitizer := &SecretSanitizer{
		patterns: make([]*secretPattern, 0, len(patterns)),
	}

	for _, p := range patterns {
		compiled := regexp.MustCompile(p.pattern)
		sanitizer.patterns = append(sanitizer.patterns, &secretPattern{
			name:    p.name,
			pattern: compiled,
		})
	}

	return sanitizer
}

// Sanitize replaces detected secrets with [REDACTED].
//
// Description:
//
//	Scans the input string for known secret patterns and replaces
//	any matches with [REDACTED]. Multiple secrets in the same string
//	are all replaced.
//
// Inputs:
//
//	s - The string to sanitize.
//
// Outputs:
//
//	string - The sanitized string with secrets replaced.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (ss *SecretSanitizer) Sanitize(s string) string {
	if s == "" {
		return s
	}

	result := s
	for _, p := range ss.patterns {
		result = p.pattern.ReplaceAllString(result, "[REDACTED]")
	}

	return result
}

// SanitizeMap sanitizes all values in a string map.
//
// Description:
//
//	Creates a new map with all values sanitized. Keys are preserved.
//
// Inputs:
//
//	m - The map to sanitize.
//
// Outputs:
//
//	map[string]string - New map with sanitized values.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (ss *SecretSanitizer) SanitizeMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = ss.Sanitize(v)
	}
	return result
}

// -----------------------------------------------------------------------------
// TraceRecorder
// -----------------------------------------------------------------------------

// TraceRecorder captures reasoning steps for audit and debugging.
//
// Description:
//
//	Records each reasoning action and its effects on CRS state.
//	Steps are stored in order and can be exported as a complete trace.
//
// Thread Safety: Safe for concurrent use.
type TraceRecorder struct {
	mu          sync.Mutex
	steps       []TraceStep
	config      TraceConfig
	nextStepNum int // Monotonically increasing step counter
}

// NewTraceRecorder creates a new trace recorder.
//
// Inputs:
//
//	config - Configuration for the recorder. Uses defaults if zero-valued.
//
// Outputs:
//
//	*TraceRecorder - The configured recorder.
func NewTraceRecorder(config TraceConfig) *TraceRecorder {
	if config.MaxSteps <= 0 {
		config.MaxSteps = DefaultTraceConfig().MaxSteps
	}
	return &TraceRecorder{
		steps:       make([]TraceStep, 0, min(config.MaxSteps, 100)),
		config:      config,
		nextStepNum: 1, // Step numbers start at 1
	}
}

// RecordStep adds a step to the trace.
//
// Description:
//
//	Called after each reasoning action to capture what was done,
//	what was found, and how CRS was updated. Automatically assigns
//	step numbers and timestamps.
//
//	SECURITY: If a Sanitizer is configured, all string fields are
//	sanitized before storage to prevent secrets from leaking into
//	audit trails. This is critical because safety scanners may block
//	actions that contain secrets, but the attempted action (including
//	the secret) would otherwise be recorded in the trace.
//
// Inputs:
//
//	step - The trace step to record. Step number and timestamp
//	       will be overwritten by the recorder.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) RecordStep(step TraceStep) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Evict oldest step if at capacity
	if len(r.steps) >= r.config.MaxSteps {
		r.steps = r.steps[1:]
	}

	// Assign step number using monotonically increasing counter
	step.Step = r.nextStepNum
	r.nextStepNum++

	// Set timestamp if not provided
	if step.Timestamp == 0 {
		step.Timestamp = time.Now().UnixMilli()
	}

	// Apply config filters
	if !r.config.RecordSymbols {
		step.SymbolsFound = nil
	}
	if !r.config.RecordMetadata {
		step.Metadata = nil
	}

	// SECURITY: Sanitize before storing to prevent secret leakage
	if r.config.Sanitizer != nil {
		step = r.sanitizeStep(step)
	}

	r.steps = append(r.steps, step)
}

// sanitizeStep removes secrets from all string fields in a TraceStep.
//
// Description:
//
//	Creates a sanitized copy of the step with secrets replaced by [REDACTED].
//	This prevents secrets from persisting in audit trails even when the
//	safety scanner blocks the original action.
//
// Inputs:
//
//	step - The step to sanitize.
//
// Outputs:
//
//	TraceStep - A sanitized copy of the step.
func (r *TraceRecorder) sanitizeStep(step TraceStep) TraceStep {
	sanitizer := r.config.Sanitizer

	// Sanitize direct string fields
	step.Action = sanitizer.Sanitize(step.Action)
	step.Target = sanitizer.Sanitize(step.Target)
	step.Tool = sanitizer.Sanitize(step.Tool)
	step.Error = sanitizer.Sanitize(step.Error)

	// Sanitize symbols found
	if len(step.SymbolsFound) > 0 {
		sanitizedSymbols := make([]string, len(step.SymbolsFound))
		for i, s := range step.SymbolsFound {
			sanitizedSymbols[i] = sanitizer.Sanitize(s)
		}
		step.SymbolsFound = sanitizedSymbols
	}

	// Sanitize proof updates
	if len(step.ProofUpdates) > 0 {
		sanitizedUpdates := make([]ProofUpdate, len(step.ProofUpdates))
		for i, pu := range step.ProofUpdates {
			sanitizedUpdates[i] = ProofUpdate{
				NodeID: sanitizer.Sanitize(pu.NodeID),
				Type:   pu.Type,  // Type is enum, no secrets
				Delta:  pu.Delta, // Delta is numeric, no secrets
				Reason: sanitizer.Sanitize(pu.Reason),
				Source: pu.Source, // Source is enum, no secrets
				Status: pu.Status, // Status string for backwards compat
			}
		}
		step.ProofUpdates = sanitizedUpdates
	}

	// Sanitize constraints added
	if len(step.ConstraintsAdded) > 0 {
		sanitizedConstraints := make([]ConstraintUpdate, len(step.ConstraintsAdded))
		for i, cu := range step.ConstraintsAdded {
			sanitizedNodes := make([]string, len(cu.Nodes))
			for j, n := range cu.Nodes {
				sanitizedNodes[j] = sanitizer.Sanitize(n)
			}
			sanitizedConstraints[i] = ConstraintUpdate{
				ID:     sanitizer.Sanitize(cu.ID),
				Type:   cu.Type, // Type is enum, no secrets
				Nodes:  sanitizedNodes,
				Source: cu.Source, // Source is enum, no secrets
			}
		}
		step.ConstraintsAdded = sanitizedConstraints
	}

	// Sanitize dependencies found
	if len(step.DependenciesFound) > 0 {
		sanitizedDeps := make([]DependencyEdge, len(step.DependenciesFound))
		for i, d := range step.DependenciesFound {
			sanitizedDeps[i] = DependencyEdge{
				From:   sanitizer.Sanitize(d.From),
				To:     sanitizer.Sanitize(d.To),
				Source: d.Source, // Source is enum, no secrets
			}
		}
		step.DependenciesFound = sanitizedDeps
	}

	// Sanitize metadata
	if len(step.Metadata) > 0 {
		sanitizedMeta := make(map[string]string, len(step.Metadata))
		for k, v := range step.Metadata {
			sanitizedMeta[k] = sanitizer.Sanitize(v)
		}
		step.Metadata = sanitizedMeta
	}

	return step
}

// GetSteps returns a copy of all recorded steps.
//
// Outputs:
//
//	[]TraceStep - Copy of recorded steps in order.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) GetSteps() []TraceStep {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]TraceStep, len(r.steps))
	copy(result, r.steps)
	return result
}

// StepCount returns the number of recorded steps.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) StepCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.steps)
}

// Clear removes all recorded steps.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = r.steps[:0]
	r.nextStepNum = 1
}

// Export returns the trace in exportable format.
//
// Description:
//
//	Creates a ReasoningTrace containing all recorded steps,
//	suitable for JSON serialization.
//
// Inputs:
//
//	sessionID - Session identifier for the export.
//
// Outputs:
//
//	ReasoningTrace - The exportable trace.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) Export(sessionID string) ReasoningTrace {
	steps := r.GetSteps()

	trace := ReasoningTrace{
		SessionID:  sessionID,
		TotalSteps: len(steps),
		Trace:      steps,
	}

	if len(steps) > 0 {
		trace.StartTime = steps[0].Timestamp
		trace.EndTime = steps[len(steps)-1].Timestamp
		durationMs := trace.EndTime - trace.StartTime
		duration := time.Duration(durationMs) * time.Millisecond
		trace.Duration = duration.String()
	} else {
		trace.Duration = "0s"
	}

	return trace
}

// LastStep returns the most recently recorded step, or nil if empty.
//
// Thread Safety: Safe for concurrent use.
func (r *TraceRecorder) LastStep() *TraceStep {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.steps) == 0 {
		return nil
	}

	// Return a copy
	step := r.steps[len(r.steps)-1]
	return &step
}

// -----------------------------------------------------------------------------
// Builder Pattern for TraceStep
// -----------------------------------------------------------------------------

// TraceStepBuilder helps construct TraceStep instances.
type TraceStepBuilder struct {
	step TraceStep
}

// NewTraceStepBuilder creates a new builder.
func NewTraceStepBuilder() *TraceStepBuilder {
	return &TraceStepBuilder{
		step: TraceStep{
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

// WithAction sets the action.
func (b *TraceStepBuilder) WithAction(action string) *TraceStepBuilder {
	b.step.Action = action
	return b
}

// WithTarget sets the target.
func (b *TraceStepBuilder) WithTarget(target string) *TraceStepBuilder {
	b.step.Target = target
	return b
}

// WithTool sets the tool.
func (b *TraceStepBuilder) WithTool(tool string) *TraceStepBuilder {
	b.step.Tool = tool
	return b
}

// WithDuration sets the duration.
func (b *TraceStepBuilder) WithDuration(d time.Duration) *TraceStepBuilder {
	b.step.Duration = d
	return b
}

// WithSymbolsFound sets the symbols found.
func (b *TraceStepBuilder) WithSymbolsFound(symbols []string) *TraceStepBuilder {
	b.step.SymbolsFound = symbols
	return b
}

// WithProofUpdate adds a proof update.
//
// Parameters accept string values for backwards compatibility:
//   - status: "proven", "disproven", "expanded", "unknown", "increment", "decrement"
//   - source: "hard", "soft", "safety"
func (b *TraceStepBuilder) WithProofUpdate(nodeID, status, reason, source string) *TraceStepBuilder {
	// Convert string status to ProofUpdateType
	updateType := stringToProofUpdateType(status)

	// Convert string source to SignalSource
	signalSource := stringToSignalSource(source)

	b.step.ProofUpdates = append(b.step.ProofUpdates, ProofUpdate{
		NodeID: nodeID,
		Type:   updateType,
		Delta:  1, // Default delta for status-based updates
		Reason: reason,
		Source: signalSource,
		Status: status, // Keep original string for JSON serialization compatibility
	})
	return b
}

// WithProofUpdateTyped adds a proof update with typed parameters.
// Prefer this over WithProofUpdate for new code.
func (b *TraceStepBuilder) WithProofUpdateTyped(nodeID string, updateType ProofUpdateType, delta uint64, reason string, source SignalSource) *TraceStepBuilder {
	b.step.ProofUpdates = append(b.step.ProofUpdates, ProofUpdate{
		NodeID: nodeID,
		Type:   updateType,
		Delta:  delta,
		Reason: reason,
		Source: source,
	})
	return b
}

// WithConstraint adds a constraint update.
func (b *TraceStepBuilder) WithConstraint(id, constraintType string, nodes []string) *TraceStepBuilder {
	// Convert string type to ConstraintType
	cType := stringToConstraintType(constraintType)

	b.step.ConstraintsAdded = append(b.step.ConstraintsAdded, ConstraintUpdate{
		ID:     id,
		Type:   cType,
		Nodes:  nodes,
		Source: SignalSourceUnknown, // Default source
	})
	return b
}

// WithDependency adds a dependency edge.
func (b *TraceStepBuilder) WithDependency(from, to string) *TraceStepBuilder {
	b.step.DependenciesFound = append(b.step.DependenciesFound, DependencyEdge{
		From:   from,
		To:     to,
		Source: SignalSourceUnknown, // Default source
	})
	return b
}

// stringToProofUpdateType converts a string status to ProofUpdateType.
func stringToProofUpdateType(status string) ProofUpdateType {
	switch status {
	case "proven":
		return ProofUpdateTypeProven
	case "disproven":
		return ProofUpdateTypeDisproven
	case "expanded", "unknown":
		return ProofUpdateTypeIncrement // Expanded/unknown map to increment
	case "increment":
		return ProofUpdateTypeIncrement
	case "decrement":
		return ProofUpdateTypeDecrement
	case "reset":
		return ProofUpdateTypeReset
	default:
		return ProofUpdateTypeUnknown
	}
}

// stringToSignalSource converts a string source to SignalSource.
func stringToSignalSource(source string) SignalSource {
	switch source {
	case "hard":
		return SignalSourceHard
	case "soft":
		return SignalSourceSoft
	case "safety":
		return SignalSourceSafety
	default:
		return SignalSourceUnknown
	}
}

// stringToConstraintType converts a string type to ConstraintType.
func stringToConstraintType(constraintType string) ConstraintType {
	switch constraintType {
	case "mutual_exclusion":
		return ConstraintTypeMutualExclusion
	case "implication":
		return ConstraintTypeImplication
	case "ordering":
		return ConstraintTypeOrdering
	case "resource":
		return ConstraintTypeResource
	default:
		return ConstraintTypeUnknown
	}
}

// WithError sets the error.
func (b *TraceStepBuilder) WithError(err string) *TraceStepBuilder {
	b.step.Error = err
	return b
}

// WithMetadata adds a metadata key-value pair.
func (b *TraceStepBuilder) WithMetadata(key, value string) *TraceStepBuilder {
	if b.step.Metadata == nil {
		b.step.Metadata = make(map[string]string)
	}
	b.step.Metadata[key] = value
	return b
}

// Build returns the constructed TraceStep.
func (b *TraceStepBuilder) Build() TraceStep {
	return b.step
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
