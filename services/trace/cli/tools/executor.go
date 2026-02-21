// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/transaction"
	"github.com/google/uuid"
)

// Sentinel errors for the executor.
var (
	// ErrToolNotFound indicates the requested tool does not exist.
	ErrToolNotFound = errors.New("tool not found")

	// ErrValidationFailed indicates parameter validation failed.
	ErrValidationFailed = errors.New("parameter validation failed")

	// ErrExecutionFailed indicates tool execution failed.
	ErrExecutionFailed = errors.New("tool execution failed")

	// ErrTimeout indicates the tool execution timed out.
	ErrTimeout = errors.New("tool execution timed out")

	// ErrRequirementNotMet indicates a tool requirement is not satisfied.
	ErrRequirementNotMet = errors.New("tool requirement not met")
)

// Executor handles tool invocations with validation and observability.
//
// Thread Safety:
//
//	Executor is safe for concurrent use. Multiple tool executions can
//	run simultaneously.
type Executor struct {
	registry *Registry
	options  ExecutorOptions
	cache    *resultCache

	// satisfiedRequirements tracks which requirements are currently met.
	mu                    sync.RWMutex
	satisfiedRequirements map[string]bool

	// transactionManager provides transactional file operations for side-effect tools.
	// Optional - if nil, side-effect tools execute without transaction protection.
	transactionManager *transaction.Manager

	// sessionID correlates transactions with agent sessions for tracing.
	sessionID string
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithTransactionManager sets the transaction manager for the executor.
//
// When set, side-effect tools are wrapped in a transaction that is
// automatically committed on success or rolled back on error.
func WithTransactionManager(tm *transaction.Manager) ExecutorOption {
	return func(e *Executor) {
		e.transactionManager = tm
	}
}

// WithSessionID sets the session ID for trace correlation.
func WithSessionID(sessionID string) ExecutorOption {
	return func(e *Executor) {
		e.sessionID = sessionID
	}
}

// NewExecutor creates a new tool executor.
//
// Inputs:
//
//	registry - The tool registry
//	opts - Executor options (uses defaults if nil)
//
// Outputs:
//
//	*Executor - The configured executor
func NewExecutor(registry *Registry, opts *ExecutorOptions) *Executor {
	options := DefaultExecutorOptions()
	if opts != nil {
		options = *opts
	}

	e := &Executor{
		registry:              registry,
		options:               options,
		satisfiedRequirements: make(map[string]bool),
	}

	if options.EnableCaching {
		e.cache = newResultCache(options.CacheTTL)
	}

	return e
}

// NewExecutorWithOptions creates an executor with functional options.
//
// # Inputs
//
//   - registry: The tool registry.
//   - execOpts: Executor configuration options (uses defaults if nil).
//   - opts: Functional options for additional configuration.
//
// # Outputs
//
//   - *Executor: The configured executor.
func NewExecutorWithOptions(registry *Registry, execOpts *ExecutorOptions, opts ...ExecutorOption) *Executor {
	e := NewExecutor(registry, execOpts)

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// SetSessionID sets the session ID for trace correlation.
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) SetSessionID(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionID = sessionID
}

// Execute runs a tool with the given invocation.
//
// Description:
//
//	Validates the invocation, checks requirements, executes the tool,
//	and optionally caches the result.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	invocation - The tool invocation to execute
//
// Outputs:
//
//	*Result - The execution result
//	error - Non-nil if execution failed
//
// Errors:
//
//	ErrToolNotFound - Tool does not exist
//	ErrValidationFailed - Parameter validation failed
//	ErrRequirementNotMet - Tool requirement not satisfied
//	ErrTimeout - Execution timed out
//	ErrExecutionFailed - Tool returned an error
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) Execute(ctx context.Context, invocation *Invocation) (*Result, error) {
	// TO-001: Check context cancellation early to avoid wasted work
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before execution: %w", err)
	}

	if invocation == nil {
		return nil, fmt.Errorf("%w: nil invocation", ErrValidationFailed)
	}

	// Assign ID if not set
	if invocation.ID == "" {
		invocation.ID = uuid.NewString()
	}

	logger := slog.With(
		"tool", invocation.ToolName,
		"invocation_id", invocation.ID,
	)

	// Get the tool
	tool, ok := e.registry.Get(invocation.ToolName)
	if !ok {
		logger.Warn("Tool not found")
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, invocation.ToolName)
	}

	// Coerce parameters to expected types (handles LLM string-to-number conversion)
	e.coerceParams(tool, invocation.Parameters)

	// Validate parameters
	if err := e.validateParams(tool, invocation.Parameters); err != nil {
		logger.Warn("Parameter validation failed", "error", err)
		return nil, fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	// Check requirements
	if err := e.checkRequirements(tool); err != nil {
		logger.Warn("Requirement not met", "error", err)
		return nil, err
	}

	// Check cache
	if e.cache != nil && !tool.Definition().SideEffects {
		if cached, ok := e.cache.get(invocation.ToolName, invocation.Parameters); ok {
			logger.Debug("Cache hit")
			cached.Cached = true
			return cached, nil
		}
	}

	// Set up timeout
	timeout := e.options.DefaultTimeout
	if tool.Definition().Timeout > 0 {
		timeout = tool.Definition().Timeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check if this tool has side effects and we have a transaction manager
	hasSideEffects := tool.Definition().SideEffects
	var txActive bool

	if hasSideEffects && e.transactionManager != nil {
		// Get session ID for trace correlation
		e.mu.RLock()
		sessionID := e.sessionID
		e.mu.RUnlock()

		// Check if a transaction is already active
		if e.transactionManager.Active() == nil {
			// Start transaction for side-effect tools
			_, txErr := e.transactionManager.Begin(ctx, sessionID)
			if txErr != nil {
				logger.Error("Failed to start transaction", "error", txErr)
				return nil, fmt.Errorf("starting transaction: %w", txErr)
			}
			txActive = true
			logger.Debug("Transaction started for side-effect tool")
		}
	}

	// Execute the tool
	invocation.StartedAt = time.Now().UnixMilli()
	logger.Debug("Executing tool")

	result, err := tool.Execute(ctx, MapParams{Tool: invocation.ToolName, Params: invocation.Parameters})
	invocation.CompletedAt = time.Now().UnixMilli()

	// Handle transaction commit/rollback
	if txActive && e.transactionManager != nil {
		if err != nil || ctx.Err() != nil {
			// Rollback on error or context cancellation
			reason := "tool execution failed"
			if ctx.Err() != nil {
				reason = "context cancelled"
			}
			_, rollbackErr := e.transactionManager.Rollback(ctx, reason)
			if rollbackErr != nil {
				logger.Error("Transaction rollback failed", "error", rollbackErr)
			} else {
				logger.Debug("Transaction rolled back", "reason", reason)
			}
		} else {
			// Record modified files if available
			if result != nil && result.Metadata != nil {
				if files, ok := result.Metadata["modified_files"].([]string); ok {
					for _, file := range files {
						e.transactionManager.RecordModification(file)
					}
				}
			}

			// Commit on success
			_, commitErr := e.transactionManager.Commit(ctx, "agent tool execution")
			if commitErr != nil {
				logger.Error("Transaction commit failed", "error", commitErr)
				return nil, fmt.Errorf("committing transaction: %w", commitErr)
			}
			logger.Debug("Transaction committed")
		}
	}

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Error("Tool execution timed out", "timeout", timeout)
			return nil, fmt.Errorf("%w: %s after %v", ErrTimeout, invocation.ToolName, timeout)
		}
		logger.Error("Tool execution failed", "error", err)
		return nil, fmt.Errorf("%w: %v", ErrExecutionFailed, err)
	}

	// Set duration
	result.Duration = time.Duration(invocation.CompletedAt-invocation.StartedAt) * time.Millisecond

	// Truncate if needed
	if result.TokensUsed > e.options.MaxOutputTokens {
		result = e.truncateResult(result)
	}

	// Cache successful results
	if e.cache != nil && result.Success && !tool.Definition().SideEffects {
		e.cache.set(invocation.ToolName, invocation.Parameters, result)
	}

	// Attach result to invocation
	invocation.Result = result

	logger.Debug("Tool executed",
		"success", result.Success,
		"duration", result.Duration,
		"tokens", result.TokensUsed,
	)

	return result, nil
}

// coerceParams attempts to convert parameter values to their expected types.
//
// Description:
//
//	LLMs sometimes pass numeric parameters as strings (e.g., "1.5" instead of 1.5)
//	or use semantic values (e.g., "high" instead of 2.0). This method coerces
//	these values to proper types before validation.
//
// Inputs:
//
//	tool - The tool whose parameters are being coerced
//	params - The parameter map (modified in place)
//
// Coercion Rules:
//
//	For ParamTypeFloat and ParamTypeInt:
//	- String numbers: "1.5" → 1.5, "10" → 10
//	- Semantic values: "high" → 2.0, "medium" → 1.0, "low" → 0.5
//	- Boolean-like: "true" → 1.0, "false" → 0.0 (for compatibility)
//
// Thread Safety: Modifies params in place; caller should not share params concurrently.
func (e *Executor) coerceParams(tool Tool, params map[string]any) {
	if params == nil {
		return
	}

	def := tool.Definition()

	for name, value := range params {
		paramDef, ok := def.Parameters[name]
		if !ok {
			continue
		}

		// Only coerce string values for numeric types
		strVal, isString := value.(string)
		if !isString {
			continue
		}

		switch paramDef.Type {
		case ParamTypeFloat, ParamTypeInt:
			// Try parsing as a number first
			if f, err := strconv.ParseFloat(strVal, 64); err == nil {
				params[name] = f
				continue
			}

			// Map semantic values to numbers
			// These cover common LLM outputs for resolution/granularity parameters
			switch strings.ToLower(strings.TrimSpace(strVal)) {
			case "high", "maximum", "max", "fine", "fine-grained", "fine_grained", "detailed", "granular", "small":
				params[name] = 2.0
			case "medium", "normal", "default", "balanced", "moderate", "standard":
				params[name] = 1.0
			case "low", "minimum", "min", "coarse", "broad", "large":
				params[name] = 0.5
			case "very_high", "veryhigh", "highest", "very_fine", "finest":
				params[name] = 3.0
			case "very_low", "verylow", "lowest", "very_coarse", "coarsest":
				params[name] = 0.25
			case "true", "yes", "on":
				params[name] = 1.0
			case "false", "no", "off":
				params[name] = 0.0
			default:
				// Log unrecognized values for debugging (GR-47)
				slog.Debug("coerceParams: unrecognized semantic value for numeric param",
					slog.String("tool", tool.Name()),
					slog.String("param", name),
					slog.String("value", strVal),
				)
			}
			// If no match, leave as string and let validation fail with clear error

		case ParamTypeBool:
			// Coerce string booleans
			switch strings.ToLower(strings.TrimSpace(strVal)) {
			case "true", "yes", "on", "1":
				params[name] = true
			case "false", "no", "off", "0":
				params[name] = false
			}
		}
	}
}

// validateParams validates tool parameters against the definition.
func (e *Executor) validateParams(tool Tool, params map[string]any) error {
	def := tool.Definition()

	// Check required parameters
	for name, paramDef := range def.Parameters {
		if paramDef.Required {
			if _, ok := params[name]; !ok {
				return &ValidationError{
					Parameter: name,
					Message:   "required parameter missing",
				}
			}
		}
	}

	// Validate provided parameters
	for name, value := range params {
		paramDef, ok := def.Parameters[name]
		if !ok {
			// Unknown parameter - ignore or error based on strictness
			continue
		}

		if err := e.validateParam(name, value, paramDef); err != nil {
			return err
		}
	}

	return nil
}

// validateParam validates a single parameter value.
func (e *Executor) validateParam(name string, value any, def ParamDef) error {
	if value == nil {
		if def.Required {
			return &ValidationError{
				Parameter: name,
				Message:   "required parameter is nil",
			}
		}
		return nil
	}

	switch def.Type {
	case ParamTypeString:
		str, ok := value.(string)
		if !ok {
			return &ValidationError{
				Parameter: name,
				Message:   "expected string",
				Actual:    fmt.Sprintf("%T", value),
			}
		}
		if def.MinLength > 0 && len(str) < def.MinLength {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("string length must be at least %d", def.MinLength),
			}
		}
		if def.MaxLength > 0 && len(str) > def.MaxLength {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("string length must be at most %d", def.MaxLength),
			}
		}

	case ParamTypeInt:
		// Accept both int and float64 (JSON unmarshals numbers as float64)
		var num float64
		switch v := value.(type) {
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		case float64:
			num = v
		default:
			return &ValidationError{
				Parameter: name,
				Message:   "expected integer",
				Actual:    fmt.Sprintf("%T", value),
			}
		}
		if def.Minimum != nil && num < *def.Minimum {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("value must be at least %v", *def.Minimum),
			}
		}
		if def.Maximum != nil && num > *def.Maximum {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("value must be at most %v", *def.Maximum),
			}
		}

	case ParamTypeFloat:
		num, ok := value.(float64)
		if !ok {
			return &ValidationError{
				Parameter: name,
				Message:   "expected number",
				Actual:    fmt.Sprintf("%T", value),
			}
		}
		if def.Minimum != nil && num < *def.Minimum {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("value must be at least %v", *def.Minimum),
			}
		}
		if def.Maximum != nil && num > *def.Maximum {
			return &ValidationError{
				Parameter: name,
				Message:   fmt.Sprintf("value must be at most %v", *def.Maximum),
			}
		}

	case ParamTypeBool:
		if _, ok := value.(bool); !ok {
			return &ValidationError{
				Parameter: name,
				Message:   "expected boolean",
				Actual:    fmt.Sprintf("%T", value),
			}
		}

	case ParamTypeArray:
		if _, ok := value.([]any); !ok {
			// Also accept typed slices
			return nil // Relaxed validation for arrays
		}

	case ParamTypeObject:
		if _, ok := value.(map[string]any); !ok {
			return &ValidationError{
				Parameter: name,
				Message:   "expected object",
				Actual:    fmt.Sprintf("%T", value),
			}
		}
	}

	// Check enum constraint
	if len(def.Enum) > 0 {
		found := false
		for _, allowed := range def.Enum {
			if value == allowed {
				found = true
				break
			}
		}
		if !found {
			return &ValidationError{
				Parameter: name,
				Message:   "value not in allowed enum",
				Expected:  fmt.Sprintf("%v", def.Enum),
				Actual:    fmt.Sprintf("%v", value),
			}
		}
	}

	return nil
}

// checkRequirements verifies all tool requirements are satisfied.
func (e *Executor) checkRequirements(tool Tool) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, req := range tool.Definition().Requires {
		if !e.satisfiedRequirements[req] {
			return fmt.Errorf("%w: %s requires %s", ErrRequirementNotMet, tool.Name(), req)
		}
	}
	return nil
}

// SatisfyRequirement marks a requirement as satisfied.
//
// Inputs:
//
//	requirement - The requirement name (e.g., "graph_initialized")
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) SatisfyRequirement(requirement string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.satisfiedRequirements[requirement] = true
}

// UnsatisfyRequirement marks a requirement as not satisfied.
//
// Inputs:
//
//	requirement - The requirement name
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) UnsatisfyRequirement(requirement string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.satisfiedRequirements, requirement)
}

// IsRequirementSatisfied checks if a requirement is satisfied.
//
// Inputs:
//
//	requirement - The requirement name
//
// Outputs:
//
//	bool - True if the requirement is satisfied
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) IsRequirementSatisfied(requirement string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.satisfiedRequirements[requirement]
}

// truncateResult truncates a result to fit within token limits.
//
// Description:
//
//	Truncates the OutputText field if it exceeds the estimated token limit.
//	Uses an approximation of ~4 characters per token.
//
// Inputs:
//
//	result - The result to truncate
//
// Outputs:
//
//	*Result - The same result pointer (modified in place)
//
// Limitations:
//
//	Modifies the input result in place rather than returning a copy.
//	The caller should be aware that the original result is mutated.
//
// Assumptions:
//
//	Token count is approximately 4 characters per token.
func (e *Executor) truncateResult(result *Result) *Result {
	// Simple truncation based on estimated tokens
	// In practice, this would be more sophisticated
	maxChars := e.options.MaxOutputTokens * 4 // ~4 chars per token

	if len(result.OutputText) > maxChars {
		result.OutputText = result.OutputText[:maxChars] + "\n... [truncated]"
		result.Truncated = true
		result.TokensUsed = e.options.MaxOutputTokens
	}

	return result
}

// GetAvailableTools returns tools available with current requirements.
//
// Inputs:
//
//	enabledCategories - Categories to include (empty = all)
//	disabledTools - Specific tool names to exclude
//
// Outputs:
//
//	[]ToolDefinition - Definitions for available tools
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) GetAvailableTools(enabledCategories []string, disabledTools []string) []ToolDefinition {
	tools := e.registry.GetEnabled(enabledCategories, disabledTools)

	e.mu.RLock()
	defer e.mu.RUnlock()

	var available []ToolDefinition
	for _, tool := range tools {
		// Check if all requirements are satisfied
		reqsMet := true
		for _, req := range tool.Definition().Requires {
			if !e.satisfiedRequirements[req] {
				reqsMet = false
				break
			}
		}
		if reqsMet {
			available = append(available, tool.Definition())
		}
	}

	return available
}

// ClearCache clears the result cache.
//
// Thread Safety: This method is safe for concurrent use.
func (e *Executor) ClearCache() {
	if e.cache != nil {
		e.cache.clear()
	}
}

// resultCache provides thread-safe caching of tool results.
type resultCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	result    *Result
	expiresAt time.Time
}

func newResultCache(ttl time.Duration) *resultCache {
	return &resultCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// key generates a deterministic cache key from tool name and parameters.
//
// Description:
//
//	Creates a cache key by sorting parameter keys alphabetically before
//	formatting. This ensures the same parameters always produce the same
//	key regardless of map iteration order.
//
// Inputs:
//
//	toolName - The tool name
//	params - The parameter map
//
// Outputs:
//
//	string - Deterministic cache key
func (c *resultCache) key(toolName string, params map[string]any) string {
	if len(params) == 0 {
		return toolName
	}

	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build key with sorted parameters
	var keyParts []string
	for _, k := range keys {
		keyParts = append(keyParts, fmt.Sprintf("%s=%v", k, params[k]))
	}

	return fmt.Sprintf("%s:{%s}", toolName, strings.Join(keyParts, ","))
}

func (c *resultCache) get(toolName string, params map[string]any) (*Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[c.key(toolName, params)]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	// Return a copy to avoid mutation
	result := *entry.result
	return &result, true
}

func (c *resultCache) set(toolName string, params map[string]any, result *Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[c.key(toolName, params)] = &cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *resultCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}
