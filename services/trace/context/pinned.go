// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Constants for bounds enforcement.
const (
	// DefaultPinnedBudget is the default token budget for the pinned block.
	DefaultPinnedBudget = 2000

	// MinPinnedBudget is the minimum viable pinned block budget.
	MinPinnedBudget = 500

	// MaxFindings is the maximum number of key findings to preserve.
	MaxFindings = 10

	// MaxConstraints is the maximum number of constraints.
	MaxConstraints = 10

	// MaxPlanSteps is the maximum number of plan steps.
	MaxPlanSteps = 20

	// MaxFindingSummaryLen is the maximum characters for a finding summary.
	MaxFindingSummaryLen = 100

	// MaxFindingDetailLen is the maximum characters for a finding detail.
	MaxFindingDetailLen = 500

	// MaxConstraintLen is the maximum characters for a constraint.
	MaxConstraintLen = 200

	// MaxOriginalQueryLen is the maximum characters for the original query.
	MaxOriginalQueryLen = 2000

	// MaxStepDescriptionLen is the maximum characters for a plan step description.
	MaxStepDescriptionLen = 200
)

// Pinned instructions errors.
var (
	// ErrQueryAlreadySet indicates the original query has already been set.
	ErrQueryAlreadySet = errors.New("original query already set and is immutable")

	// ErrFindingsLimitReached indicates the maximum findings limit was reached.
	ErrFindingsLimitReached = errors.New("maximum findings limit reached")

	// ErrConstraintsLimitReached indicates the maximum constraints limit was reached.
	ErrConstraintsLimitReached = errors.New("maximum constraints limit reached")

	// ErrPlanStepsLimitReached indicates the maximum plan steps limit was reached.
	ErrPlanStepsLimitReached = errors.New("maximum plan steps limit reached")

	// ErrInvalidStepIndex indicates a step index is out of range.
	ErrInvalidStepIndex = errors.New("step index out of range")

	// ErrNilPinnedInstructions indicates a nil PinnedInstructions was passed.
	ErrNilPinnedInstructions = errors.New("pinned instructions is nil")
)

// StepStatus represents the state of a plan step.
type StepStatus string

const (
	// StepPending indicates the step has not started.
	StepPending StepStatus = "pending"

	// StepInProgress indicates the step is currently being executed.
	StepInProgress StepStatus = "in_progress"

	// StepDone indicates the step has completed successfully.
	StepDone StepStatus = "done"

	// StepSkipped indicates the step was skipped.
	StepSkipped StepStatus = "skipped"
)

// String returns the string representation of StepStatus.
func (s StepStatus) String() string {
	return string(s)
}

// IsValid returns true if the status is a valid StepStatus.
func (s StepStatus) IsValid() bool {
	switch s {
	case StepPending, StepInProgress, StepDone, StepSkipped:
		return true
	default:
		return false
	}
}

// Symbol returns the display symbol for the status.
func (s StepStatus) Symbol() string {
	switch s {
	case StepPending:
		return "[ ]"
	case StepInProgress:
		return "[>]"
	case StepDone:
		return "[x]"
	case StepSkipped:
		return "[-]"
	default:
		return "[?]"
	}
}

// PlanStep represents one step in the agent's plan.
// Index is determined by position in the slice, not stored explicitly.
type PlanStep struct {
	// Description is the human-readable description of the step.
	Description string `json:"description"`

	// Status is the current state of the step.
	Status StepStatus `json:"status"`

	// StartedAt is when the step began execution (Unix milliseconds UTC).
	StartedAt int64 `json:"started_at,omitempty"`

	// CompletedAt is when the step finished (done or skipped) (Unix milliseconds UTC).
	CompletedAt int64 `json:"completed_at,omitempty"`
}

// Finding represents a key discovery worth preserving.
type Finding struct {
	// Summary is a short description (max 100 chars, enforced).
	Summary string `json:"summary"`

	// Detail is a longer explanation (max 500 chars, enforced).
	Detail string `json:"detail,omitempty"`

	// Source is the file:line or tool that produced this finding.
	Source string `json:"source"`

	// Timestamp is when the finding was recorded (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`
}

// PinnedStats contains metrics about the pinned block.
type PinnedStats struct {
	// QueryTokens is the estimated tokens for the original query.
	QueryTokens int `json:"query_tokens"`

	// PlanTokens is the estimated tokens for the plan.
	PlanTokens int `json:"plan_tokens"`

	// FindingsTokens is the estimated tokens for findings.
	FindingsTokens int `json:"findings_tokens"`

	// ConstraintTokens is the estimated tokens for constraints.
	ConstraintTokens int `json:"constraint_tokens"`

	// TotalTokens is the total estimated tokens.
	TotalTokens int `json:"total_tokens"`

	// FindingsCount is the number of findings.
	FindingsCount int `json:"findings_count"`

	// ConstraintsCount is the number of constraints.
	ConstraintsCount int `json:"constraints_count"`

	// PlanStepsCount is the number of plan steps.
	PlanStepsCount int `json:"plan_steps_count"`

	// CacheHit indicates if the last render was from cache.
	CacheHit bool `json:"cache_hit"`

	// TokenBudget is the configured token budget.
	TokenBudget int `json:"token_budget"`
}

// TokenCounter is the interface for counting tokens.
type TokenCounter interface {
	// Count returns the number of tokens in the text.
	Count(text string) int
}

// defaultTokenCounter uses character-based estimation.
type defaultTokenCounter struct{}

// Count estimates tokens using the character ratio.
func (d *defaultTokenCounter) Count(text string) int {
	return int(float64(len(text)) / CharsPerToken)
}

// PinnedInstructions contains context that survives all truncation.
//
// Thread Safety: Safe for concurrent use via RWMutex.
// All mutating methods invalidate the render cache.
type PinnedInstructions struct {
	mu             sync.RWMutex
	originalQuery  string
	currentPlan    []PlanStep
	keyFindings    []Finding
	constraints    []string
	maxTokenBudget int
	tokenCounter   TokenCounter

	// Cache fields (protected by mu)
	cachedTokenCount int
	cachedRender     string
	cacheValid       bool
}

// PinnedOption is a functional option for configuring PinnedInstructions.
type PinnedOption func(*PinnedInstructions)

// WithTokenBudget sets the maximum token budget.
//
// Description:
//
//	Sets the maximum number of tokens the pinned block can use.
//	If budget < MinPinnedBudget, MinPinnedBudget is used instead.
//
// Inputs:
//
//	budget - The token budget (minimum MinPinnedBudget).
func WithTokenBudget(budget int) PinnedOption {
	return func(p *PinnedInstructions) {
		if budget < MinPinnedBudget {
			budget = MinPinnedBudget
		}
		p.maxTokenBudget = budget
	}
}

// WithTokenCounter sets a custom token counter.
//
// Description:
//
//	Sets a custom implementation for counting tokens.
//	Use this to integrate with tiktoken or model-specific counters.
//
// Inputs:
//
//	tc - The token counter implementation. If nil, default is used.
func WithTokenCounter(tc TokenCounter) PinnedOption {
	return func(p *PinnedInstructions) {
		if tc != nil {
			p.tokenCounter = tc
		}
	}
}

// NewPinnedInstructions creates a new pinned instructions block.
//
// Description:
//
//	Creates a PinnedInstructions with the specified options.
//	Default budget is DefaultPinnedBudget (2000 tokens).
//
// Inputs:
//
//	opts - Functional options for configuration.
//
// Outputs:
//
//	*PinnedInstructions - The configured pinned instructions block.
//
// Example:
//
//	pinned := NewPinnedInstructions(
//	    WithTokenBudget(3000),
//	)
func NewPinnedInstructions(opts ...PinnedOption) *PinnedInstructions {
	p := &PinnedInstructions{
		maxTokenBudget: DefaultPinnedBudget,
		tokenCounter:   &defaultTokenCounter{},
		currentPlan:    make([]PlanStep, 0),
		keyFindings:    make([]Finding, 0),
		constraints:    make([]string, 0),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// invalidateCache marks the cache as invalid.
// Must be called with mu held (Lock, not RLock).
func (p *PinnedInstructions) invalidateCache() {
	p.cacheValid = false
	p.cachedTokenCount = 0
	p.cachedRender = ""
}

// SetOriginalQuery stores the user's query (immutable after set).
//
// Description:
//
//	Sets the original query that the agent is working on.
//	This is immutable after the first call - subsequent calls return ErrQueryAlreadySet.
//	The query is truncated to MaxOriginalQueryLen if too long.
//
// Inputs:
//
//	query - The user's original query.
//
// Outputs:
//
//	error - ErrQueryAlreadySet if already set, ErrEmptyQuery if empty.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) SetOriginalQuery(query string) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.originalQuery != "" {
		return ErrQueryAlreadySet
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return ErrEmptyQuery
	}

	// Truncate if too long
	if len(query) > MaxOriginalQueryLen {
		query = query[:MaxOriginalQueryLen-3] + "..."
	}

	p.originalQuery = query
	p.invalidateCache()

	// Record metric
	recordPinnedQuerySet(len(query))

	return nil
}

// OriginalQuery returns the stored original query.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) OriginalQuery() string {
	if p == nil {
		return ""
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.originalQuery
}

// SetPlan sets the current plan steps.
//
// Description:
//
//	Sets the plan steps. Replaces any existing plan.
//	Returns ErrPlanStepsLimitReached if len(steps) > MaxPlanSteps.
//
// Inputs:
//
//	steps - The plan steps to set.
//
// Outputs:
//
//	error - ErrPlanStepsLimitReached if too many steps.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) SetPlan(steps []PlanStep) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	if len(steps) > MaxPlanSteps {
		return ErrPlanStepsLimitReached
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Copy to avoid aliasing, truncating long descriptions
	p.currentPlan = make([]PlanStep, len(steps))
	for i, step := range steps {
		if len(step.Description) > MaxStepDescriptionLen {
			step.Description = step.Description[:MaxStepDescriptionLen-3] + "..."
		}
		p.currentPlan[i] = step
	}

	p.invalidateCache()

	// Record metric
	recordPinnedPlanSet(len(steps))

	return nil
}

// GetPlan returns a copy of the current plan steps.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) GetPlan() []PlanStep {
	if p == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]PlanStep, len(p.currentPlan))
	copy(result, p.currentPlan)
	return result
}

// UpdateStepStatus marks a step as complete/in-progress by position.
//
// Description:
//
//	Updates the status of a plan step at the given position (0-indexed).
//	Automatically sets StartedAt when moving to InProgress.
//	Automatically sets CompletedAt when moving to Done or Skipped.
//
// Inputs:
//
//	position - The 0-indexed position of the step.
//	status - The new status to set.
//
// Outputs:
//
//	error - ErrInvalidStepIndex if position is out of range.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) UpdateStepStatus(position int, status StepStatus) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	if !status.IsValid() {
		return fmt.Errorf("invalid step status: %s", status)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if position < 0 || position >= len(p.currentPlan) {
		return ErrInvalidStepIndex
	}

	oldStatus := p.currentPlan[position].Status
	p.currentPlan[position].Status = status

	now := time.Now().UnixMilli()
	if status == StepInProgress && oldStatus != StepInProgress {
		p.currentPlan[position].StartedAt = now
	}
	if (status == StepDone || status == StepSkipped) && oldStatus != StepDone && oldStatus != StepSkipped {
		p.currentPlan[position].CompletedAt = now
	}

	p.invalidateCache()

	// Record metric
	recordPinnedStepUpdate(string(status))

	return nil
}

// AddFinding adds a key discovery to preserve.
//
// Description:
//
//	Adds a finding to the pinned block. Findings are key discoveries
//	that should be preserved across context truncation.
//	Truncates Summary to MaxFindingSummaryLen and Detail to MaxFindingDetailLen.
//
// Inputs:
//
//	ctx - Context for tracing.
//	f - The finding to add.
//
// Outputs:
//
//	error - ErrFindingsLimitReached if at capacity.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) AddFinding(ctx context.Context, f Finding) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	_, span := tracer.Start(ctx, "PinnedInstructions.AddFinding",
		trace.WithAttributes(
			attribute.String("finding.source", f.Source),
			attribute.Int("finding.summary_len", len(f.Summary)),
		),
	)
	defer span.End()

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.keyFindings) >= MaxFindings {
		span.SetAttributes(attribute.Bool("limit_reached", true))
		return ErrFindingsLimitReached
	}

	// Truncate if needed
	if len(f.Summary) > MaxFindingSummaryLen {
		f.Summary = f.Summary[:MaxFindingSummaryLen-3] + "..."
	}
	if len(f.Detail) > MaxFindingDetailLen {
		f.Detail = f.Detail[:MaxFindingDetailLen-3] + "..."
	}

	// Set timestamp if not set
	if f.Timestamp == 0 {
		f.Timestamp = time.Now().UnixMilli()
	}

	p.keyFindings = append(p.keyFindings, f)
	p.invalidateCache()

	// Record metric
	recordPinnedFindingAdded()

	return nil
}

// GetFindings returns a copy of the current findings.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) GetFindings() []Finding {
	if p == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]Finding, len(p.keyFindings))
	copy(result, p.keyFindings)
	return result
}

// AddConstraint adds a constraint the agent must respect.
//
// Description:
//
//	Adds a constraint to the pinned block. Constraints are rules
//	that the agent must follow while working on the task.
//	Truncates to MaxConstraintLen if too long.
//
// Inputs:
//
//	constraint - The constraint to add.
//
// Outputs:
//
//	error - ErrConstraintsLimitReached if at capacity.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) AddConstraint(constraint string) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return nil // Silently ignore empty constraints
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.constraints) >= MaxConstraints {
		return ErrConstraintsLimitReached
	}

	// Truncate if needed
	if len(constraint) > MaxConstraintLen {
		constraint = constraint[:MaxConstraintLen-3] + "..."
	}

	p.constraints = append(p.constraints, constraint)
	p.invalidateCache()

	// Record metric
	recordPinnedConstraintAdded()

	return nil
}

// GetConstraints returns a copy of the current constraints.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) GetConstraints() []string {
	if p == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]string, len(p.constraints))
	copy(result, p.constraints)
	return result
}

// Render produces the pinned block text for context injection.
//
// Description:
//
//	Generates a markdown-formatted pinned block containing the original
//	query, current plan, key findings, and constraints.
//	Uses cache if valid, otherwise regenerates.
//	Applies graceful truncation if over budget (removes oldest findings first).
//
// Outputs:
//
//	string - The rendered pinned block markdown.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) Render() string {
	if p == nil {
		return ""
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cacheValid && p.cachedRender != "" {
		recordPinnedCacheHit()
		return p.cachedRender
	}

	recordPinnedCacheMiss()

	// Build full render
	full := p.renderFullLocked()

	// Check if within budget
	tokens := p.tokenCounter.Count(full)
	if tokens <= p.maxTokenBudget {
		p.cachedRender = full
		p.cachedTokenCount = tokens
		p.cacheValid = true
		return full
	}

	// Graceful degradation: truncate oldest findings first
	truncated := p.renderTruncatedLocked()
	p.cachedRender = truncated
	p.cachedTokenCount = p.tokenCounter.Count(truncated)
	p.cacheValid = true

	recordPinnedTruncation()

	return truncated
}

// renderFullLocked generates the full pinned block without truncation.
// Must be called with mu held.
func (p *PinnedInstructions) renderFullLocked() string {
	var b strings.Builder

	b.WriteString("## Session Context (Do Not Forget)\n\n")

	// Original Request
	if p.originalQuery != "" {
		b.WriteString("### Original Request\n")
		b.WriteString("> ")
		b.WriteString(p.originalQuery)
		b.WriteString("\n\n")
	}

	// Current Plan
	if len(p.currentPlan) > 0 {
		b.WriteString("### Current Plan\n")
		for i, step := range p.currentPlan {
			b.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, step.Status.Symbol(), step.Description))
		}
		b.WriteString("\n")
	}

	// Key Findings
	if len(p.keyFindings) > 0 {
		b.WriteString("### Key Findings\n")
		for _, f := range p.keyFindings {
			if f.Source != "" {
				b.WriteString(fmt.Sprintf("- `%s` - %s", f.Source, f.Summary))
			} else {
				b.WriteString(fmt.Sprintf("- %s", f.Summary))
			}
			if f.Detail != "" {
				b.WriteString(fmt.Sprintf(": %s", f.Detail))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Constraints
	if len(p.constraints) > 0 {
		b.WriteString("### Constraints\n")
		for _, c := range p.constraints {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// renderTruncatedLocked generates a truncated pinned block to fit budget.
// Uses binary search to find optimal number of findings/constraints to keep.
// Removes oldest findings first, then oldest constraints.
// Must be called with mu held.
func (p *PinnedInstructions) renderTruncatedLocked() string {
	// Copy content for manipulation
	findings := make([]Finding, len(p.keyFindings))
	copy(findings, p.keyFindings)

	constraints := make([]string, len(p.constraints))
	copy(constraints, p.constraints)

	// Helper to check if content fits in budget
	fits := func(numFindings, numConstraints int) bool {
		var f []Finding
		var c []string
		if numFindings > 0 && numFindings <= len(findings) {
			// Keep most recent findings (remove oldest first)
			f = findings[len(findings)-numFindings:]
		}
		if numConstraints > 0 && numConstraints <= len(constraints) {
			// Keep most recent constraints (remove oldest first)
			c = constraints[len(constraints)-numConstraints:]
		}
		rendered := p.renderWithContentLocked(f, c)
		return p.tokenCounter.Count(rendered) <= p.maxTokenBudget
	}

	// Binary search for max findings we can keep (with all constraints)
	findingsToKeep := binarySearchMax(len(findings), func(n int) bool {
		return fits(n, len(constraints))
	})

	if findingsToKeep > 0 || len(constraints) > 0 {
		// If we can keep some findings with all constraints, we're done
		if findingsToKeep > 0 {
			findings = findings[len(findings)-findingsToKeep:]
		} else {
			findings = nil
		}

		// Try to fit all constraints
		if fits(findingsToKeep, len(constraints)) {
			return p.renderWithContentLocked(findings, constraints)
		}

		// Need to reduce constraints too
		constraintsToKeep := binarySearchMax(len(constraints), func(n int) bool {
			return fits(findingsToKeep, n)
		})
		if constraintsToKeep > 0 {
			constraints = constraints[len(constraints)-constraintsToKeep:]
		} else {
			constraints = nil
		}
		return p.renderWithContentLocked(findings, constraints)
	}

	// Can't fit any findings, try just constraints
	constraintsToKeep := binarySearchMax(len(constraints), func(n int) bool {
		return fits(0, n)
	})
	if constraintsToKeep > 0 {
		constraints = constraints[len(constraints)-constraintsToKeep:]
		return p.renderWithContentLocked(nil, constraints)
	}

	// Absolute minimum: just query and plan
	return p.renderWithContentLocked(nil, nil)
}

// binarySearchMax finds the maximum n in [0, max] where predicate(n) is true.
// Returns 0 if predicate(1) is false.
func binarySearchMax(max int, predicate func(int) bool) int {
	if max <= 0 || !predicate(1) {
		return 0
	}
	if predicate(max) {
		return max
	}

	// Binary search for largest n where predicate(n) is true
	lo, hi := 1, max
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if predicate(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// renderWithContentLocked renders with specific findings and constraints.
// Must be called with mu held.
func (p *PinnedInstructions) renderWithContentLocked(findings []Finding, constraints []string) string {
	var b strings.Builder

	b.WriteString("## Session Context (Do Not Forget)\n\n")

	// Original Request (always included)
	if p.originalQuery != "" {
		b.WriteString("### Original Request\n")
		b.WriteString("> ")
		b.WriteString(p.originalQuery)
		b.WriteString("\n\n")
	}

	// Current Plan (always included)
	if len(p.currentPlan) > 0 {
		b.WriteString("### Current Plan\n")
		for i, step := range p.currentPlan {
			b.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, step.Status.Symbol(), step.Description))
		}
		b.WriteString("\n")
	}

	// Key Findings (may be truncated)
	if len(findings) > 0 {
		b.WriteString("### Key Findings\n")
		for _, f := range findings {
			if f.Source != "" {
				b.WriteString(fmt.Sprintf("- `%s` - %s", f.Source, f.Summary))
			} else {
				b.WriteString(fmt.Sprintf("- %s", f.Summary))
			}
			if f.Detail != "" {
				b.WriteString(fmt.Sprintf(": %s", f.Detail))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Constraints (may be truncated)
	if len(constraints) > 0 {
		b.WriteString("### Constraints\n")
		for _, c := range constraints {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// TokenCount returns estimated token count of pinned block.
//
// Description:
//
//	Returns the estimated number of tokens in the rendered pinned block.
//	Uses cache if valid.
//
// Outputs:
//
//	int - The estimated token count.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) TokenCount() int {
	if p == nil {
		return 0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cacheValid && p.cachedTokenCount > 0 {
		return p.cachedTokenCount
	}

	// Need to render to get accurate count
	rendered := p.renderFullLocked()
	count := p.tokenCounter.Count(rendered)

	// Update cache
	p.cachedRender = rendered
	p.cachedTokenCount = count
	p.cacheValid = true

	return count
}

// Stats returns statistics about the pinned block.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) Stats() PinnedStats {
	if p == nil {
		return PinnedStats{}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := PinnedStats{
		FindingsCount:    len(p.keyFindings),
		ConstraintsCount: len(p.constraints),
		PlanStepsCount:   len(p.currentPlan),
		CacheHit:         p.cacheValid,
		TokenBudget:      p.maxTokenBudget,
	}

	// Calculate token breakdown
	if p.originalQuery != "" {
		stats.QueryTokens = p.tokenCounter.Count(p.originalQuery)
	}

	var planText strings.Builder
	for _, step := range p.currentPlan {
		planText.WriteString(step.Description)
	}
	stats.PlanTokens = p.tokenCounter.Count(planText.String())

	var findingsText strings.Builder
	for _, f := range p.keyFindings {
		findingsText.WriteString(f.Summary)
		findingsText.WriteString(f.Detail)
	}
	stats.FindingsTokens = p.tokenCounter.Count(findingsText.String())

	var constraintsText strings.Builder
	for _, c := range p.constraints {
		constraintsText.WriteString(c)
	}
	stats.ConstraintTokens = p.tokenCounter.Count(constraintsText.String())

	// Total includes formatting overhead
	if p.cacheValid {
		stats.TotalTokens = p.cachedTokenCount
	} else {
		stats.TotalTokens = stats.QueryTokens + stats.PlanTokens + stats.FindingsTokens + stats.ConstraintTokens
		// Add ~20% for markdown formatting overhead
		stats.TotalTokens = int(float64(stats.TotalTokens) * 1.2)
	}

	return stats
}

// Clear resets the pinned instructions to empty state.
//
// Description:
//
//	Clears all content including the original query.
//	Use this when starting a completely new session.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) Clear() {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.originalQuery = ""
	p.currentPlan = make([]PlanStep, 0)
	p.keyFindings = make([]Finding, 0)
	p.constraints = make([]string, 0)
	p.invalidateCache()
}

// IsEmpty returns true if the pinned block has no content.
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) IsEmpty() bool {
	if p == nil {
		return true
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.originalQuery == "" &&
		len(p.currentPlan) == 0 &&
		len(p.keyFindings) == 0 &&
		len(p.constraints) == 0
}

// Compress aggressively reduces token usage by summarizing findings.
//
// Description:
//
//	When the pinned block exceeds its budget, Compress() actively reduces
//	content size by:
//	  1. Removing finding details (keeping only summaries)
//	  2. Grouping similar findings into aggregated bullets
//	  3. Shortening constraint text
//
//	This addresses the "Pinned Budget Context Squeeze" problem where an
//	ever-growing pinned block squeezes out the code budget:
//
//	Before Compress:
//	  - Finding 1: [long summary] - [long detail]
//	  - Finding 2: [long summary] - [long detail]
//	  ...10 findings...
//
//	After Compress:
//	  - Found 10 issues: [brief aggregated summary]
//
//	Unlike renderTruncatedLocked() which just removes items, Compress()
//	preserves information through summarization.
//
// Inputs:
//
//	targetTokens - Target token count to achieve. If 0, uses maxTokenBudget/2.
//
// Outputs:
//
//	int - Tokens freed (difference between old and new token count).
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) Compress(targetTokens int) int {
	if p == nil {
		return 0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Get current token count
	currentTokens := p.tokenCounter.Count(p.renderFullLocked())
	if targetTokens <= 0 {
		targetTokens = p.maxTokenBudget / 2
	}

	if currentTokens <= targetTokens {
		return 0 // Already under target
	}

	oldTokens := currentTokens

	// Strategy 1: Remove finding details (keep summaries only)
	if len(p.keyFindings) > 0 {
		for i := range p.keyFindings {
			p.keyFindings[i].Detail = ""
		}
		p.invalidateCache()

		currentTokens = p.tokenCounter.Count(p.renderFullLocked())
		if currentTokens <= targetTokens {
			recordPinnedCompression(oldTokens - currentTokens)
			return oldTokens - currentTokens
		}
	}

	// Strategy 2: Aggregate findings if more than 3
	if len(p.keyFindings) > 3 {
		// Group findings by counting them
		aggregated := Finding{
			Summary:   fmt.Sprintf("Identified %d key findings (see trace for details)", len(p.keyFindings)),
			Source:    "aggregated",
			Timestamp: time.Now().UnixMilli(),
		}

		// Keep at most 2 most recent findings plus aggregate
		if len(p.keyFindings) >= 2 {
			p.keyFindings = []Finding{
				p.keyFindings[len(p.keyFindings)-1], // Most recent
				p.keyFindings[len(p.keyFindings)-2], // Second most recent
				aggregated,
			}
		} else {
			p.keyFindings = []Finding{aggregated}
		}
		p.invalidateCache()

		currentTokens = p.tokenCounter.Count(p.renderFullLocked())
		if currentTokens <= targetTokens {
			recordPinnedCompression(oldTokens - currentTokens)
			return oldTokens - currentTokens
		}
	}

	// Strategy 3: Shorten constraints to first 50 chars
	if len(p.constraints) > 0 {
		for i := range p.constraints {
			if len(p.constraints[i]) > 50 {
				p.constraints[i] = p.constraints[i][:47] + "..."
			}
		}
		p.invalidateCache()

		currentTokens = p.tokenCounter.Count(p.renderFullLocked())
		if currentTokens <= targetTokens {
			recordPinnedCompression(oldTokens - currentTokens)
			return oldTokens - currentTokens
		}
	}

	// Strategy 4: Aggregate constraints
	if len(p.constraints) > 3 {
		aggregated := fmt.Sprintf("%d constraints apply (see original task)", len(p.constraints))
		p.constraints = []string{aggregated}
		p.invalidateCache()

		currentTokens = p.tokenCounter.Count(p.renderFullLocked())
	}

	recordPinnedCompression(oldTokens - currentTokens)
	return oldTokens - currentTokens
}

// CompressToFit reduces the pinned block to ensure a minimum code budget.
//
// Description:
//
//	Given a total token window and the current trace size, calculates how
//	much space is available for code. If the pinned block is consuming
//	too much, it compresses to ensure MinCodeBudget tokens remain.
//
//	This is the primary method for enforcing the context budget hierarchy:
//	  1. MinCodeBudget is guaranteed (code visibility)
//	  2. Pinned block is compressed if needed
//	  3. Trace is truncated last (handled by TraceRecorder)
//
// Inputs:
//
//	totalWindow - Total tokens available for the LLM context.
//	traceTokens - Tokens currently used by the trace/history.
//
// Outputs:
//
//	int - Tokens freed from the pinned block.
//
// Example:
//
//	// Before a session turn, ensure code budget is available
//	traceTokens := trace.TokenCount()
//	pinnedTokens := pinned.TokenCount()
//	codeAvailable := totalWindow - traceTokens - pinnedTokens
//
//	if codeAvailable < MinCodeBudget {
//	    freed := pinned.CompressToFit(totalWindow, traceTokens)
//	    // If still not enough, truncate trace
//	}
//
// Thread Safety: Safe for concurrent use.
func (p *PinnedInstructions) CompressToFit(totalWindow, traceTokens int) int {
	if p == nil {
		return 0
	}

	currentPinnedTokens := p.TokenCount()
	codeAvailable := totalWindow - traceTokens - currentPinnedTokens

	if codeAvailable >= MinCodeBudget {
		return 0 // Already have enough space for code
	}

	// Calculate how much pinned needs to shrink
	deficit := MinCodeBudget - codeAvailable
	targetPinnedTokens := currentPinnedTokens - deficit

	// Ensure pinned doesn't go below minimum viable
	if targetPinnedTokens < MinPinnedBudget {
		targetPinnedTokens = MinPinnedBudget
	}

	return p.Compress(targetPinnedTokens)
}

// MarshalJSON implements json.Marshaler for persistence.
func (p *PinnedInstructions) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	type pinnedJSON struct {
		OriginalQuery  string     `json:"original_query"`
		CurrentPlan    []PlanStep `json:"current_plan"`
		KeyFindings    []Finding  `json:"key_findings"`
		Constraints    []string   `json:"constraints"`
		MaxTokenBudget int        `json:"max_token_budget"`
	}

	return json.Marshal(pinnedJSON{
		OriginalQuery:  p.originalQuery,
		CurrentPlan:    p.currentPlan,
		KeyFindings:    p.keyFindings,
		Constraints:    p.constraints,
		MaxTokenBudget: p.maxTokenBudget,
	})
}

// UnmarshalJSON implements json.Unmarshaler for persistence.
func (p *PinnedInstructions) UnmarshalJSON(data []byte) error {
	if p == nil {
		return ErrNilPinnedInstructions
	}

	type pinnedJSON struct {
		OriginalQuery  string     `json:"original_query"`
		CurrentPlan    []PlanStep `json:"current_plan"`
		KeyFindings    []Finding  `json:"key_findings"`
		Constraints    []string   `json:"constraints"`
		MaxTokenBudget int        `json:"max_token_budget"`
	}

	var pj pinnedJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		return fmt.Errorf("unmarshal pinned instructions: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.originalQuery = pj.OriginalQuery
	p.currentPlan = pj.CurrentPlan
	if p.currentPlan == nil {
		p.currentPlan = make([]PlanStep, 0)
	}
	p.keyFindings = pj.KeyFindings
	if p.keyFindings == nil {
		p.keyFindings = make([]Finding, 0)
	}
	p.constraints = pj.Constraints
	if p.constraints == nil {
		p.constraints = make([]string, 0)
	}

	if pj.MaxTokenBudget > 0 {
		p.maxTokenBudget = pj.MaxTokenBudget
	} else {
		p.maxTokenBudget = DefaultPinnedBudget
	}

	if p.tokenCounter == nil {
		p.tokenCounter = &defaultTokenCounter{}
	}

	p.invalidateCache()

	return nil
}

// Pinned block metrics.
var (
	pinnedQuerySet      metric.Int64Counter
	pinnedPlanSet       metric.Int64Counter
	pinnedStepUpdate    metric.Int64Counter
	pinnedFindingAdded  metric.Int64Counter
	pinnedConstraintAdd metric.Int64Counter
	pinnedCacheHits     metric.Int64Counter
	pinnedCacheMisses   metric.Int64Counter
	pinnedTruncations   metric.Int64Counter
	pinnedCompressions  metric.Int64Counter
	pinnedTokensFreed   metric.Int64Counter

	pinnedMetricsOnce sync.Once
	pinnedMetricsErr  error
)

// initPinnedMetrics initializes pinned block metrics.
func initPinnedMetrics() error {
	pinnedMetricsOnce.Do(func() {
		var err error

		pinnedQuerySet, err = meter.Int64Counter(
			"trace_pinned_query_set_total",
			metric.WithDescription("Number of times original query was set"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedPlanSet, err = meter.Int64Counter(
			"trace_pinned_plan_set_total",
			metric.WithDescription("Number of times plan was set"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedStepUpdate, err = meter.Int64Counter(
			"trace_pinned_step_update_total",
			metric.WithDescription("Number of plan step status updates"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedFindingAdded, err = meter.Int64Counter(
			"trace_pinned_finding_added_total",
			metric.WithDescription("Number of findings added"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedConstraintAdd, err = meter.Int64Counter(
			"trace_pinned_constraint_added_total",
			metric.WithDescription("Number of constraints added"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedCacheHits, err = meter.Int64Counter(
			"trace_pinned_cache_hits_total",
			metric.WithDescription("Number of render cache hits"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedCacheMisses, err = meter.Int64Counter(
			"trace_pinned_cache_misses_total",
			metric.WithDescription("Number of render cache misses"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedTruncations, err = meter.Int64Counter(
			"trace_pinned_truncation_total",
			metric.WithDescription("Number of times pinned block was truncated"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedCompressions, err = meter.Int64Counter(
			"trace_pinned_compression_total",
			metric.WithDescription("Number of times pinned block was compressed"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}

		pinnedTokensFreed, err = meter.Int64Counter(
			"trace_pinned_tokens_freed_total",
			metric.WithDescription("Total tokens freed via compression"),
		)
		if err != nil {
			pinnedMetricsErr = err
			return
		}
	})
	return pinnedMetricsErr
}

func recordPinnedQuerySet(queryLen int) {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedQuerySet.Add(context.Background(), 1, metric.WithAttributes(
		attribute.Int("query_length", queryLen),
	))
}

func recordPinnedPlanSet(stepCount int) {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedPlanSet.Add(context.Background(), 1, metric.WithAttributes(
		attribute.Int("step_count", stepCount),
	))
}

func recordPinnedStepUpdate(status string) {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedStepUpdate.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("status", status),
	))
}

func recordPinnedFindingAdded() {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedFindingAdded.Add(context.Background(), 1)
}

func recordPinnedConstraintAdded() {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedConstraintAdd.Add(context.Background(), 1)
}

func recordPinnedCacheHit() {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedCacheHits.Add(context.Background(), 1)
}

func recordPinnedCacheMiss() {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedCacheMisses.Add(context.Background(), 1)
}

func recordPinnedTruncation() {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedTruncations.Add(context.Background(), 1)
}

func recordPinnedCompression(tokensFreed int) {
	if err := initPinnedMetrics(); err != nil {
		return
	}
	pinnedCompressions.Add(context.Background(), 1)
	if tokensFreed > 0 {
		pinnedTokensFreed.Add(context.Background(), int64(tokensFreed))
	}
}
