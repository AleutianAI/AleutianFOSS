// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package control

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Package-level meter for frustration metrics.
var frustrationMeter = otel.Meter("aleutian.control.frustration")

// Default configuration values.
const (
	// DefaultMaxHistory is the maximum number of violations to track.
	DefaultMaxHistory = 10

	// DefaultStreakThreshold triggers stuck on N same-type violations.
	DefaultStreakThreshold = 3

	// DefaultCategoryThreshold triggers stuck on N same-category violations.
	DefaultCategoryThreshold = 4

	// DefaultOscillationWindow detects A-B-A-B patterns in last N violations.
	DefaultOscillationWindow = 6
)

// ViolationType represents categories of agent failures.
type ViolationType string

const (
	// Resource violations - external dependencies unavailable.
	ViolationFileNotFound      ViolationType = "file_not_found"
	ViolationPermissionDenied  ViolationType = "permission_denied"
	ViolationNetworkError      ViolationType = "network_error"
	ViolationResourceExhausted ViolationType = "resource_exhausted"

	// Validation violations - safety layers rejecting output.
	ViolationIntentLoop      ViolationType = "intent_loop"
	ViolationMalformedTool   ViolationType = "malformed_tool"
	ViolationInvalidCitation ViolationType = "invalid_citation"
	ViolationConstraint      ViolationType = "constraint_violation"

	// Semantic violations - understanding issues.
	ViolationAmbiguous      ViolationType = "ambiguous_request"
	ViolationConflicting    ViolationType = "conflicting_info"
	ViolationMissingContext ViolationType = "missing_context"
	ViolationScopeUnclear   ViolationType = "scope_unclear"

	// Unknown violation type.
	ViolationUnknown ViolationType = "unknown"
)

// ViolationCategory groups violation types for category-based detection.
type ViolationCategory string

const (
	// CategoryResource covers file, network, and resource access issues.
	CategoryResource ViolationCategory = "resource"

	// CategoryValidation covers safety layer rejections.
	CategoryValidation ViolationCategory = "validation"

	// CategorySemantic covers understanding and context issues.
	CategorySemantic ViolationCategory = "semantic"

	// CategoryUnknown for unclassified violations.
	CategoryUnknown ViolationCategory = "unknown"
)

// CategoryForType returns the category for a violation type.
func CategoryForType(vt ViolationType) ViolationCategory {
	switch vt {
	case ViolationFileNotFound, ViolationPermissionDenied,
		ViolationNetworkError, ViolationResourceExhausted:
		return CategoryResource
	case ViolationIntentLoop, ViolationMalformedTool,
		ViolationInvalidCitation, ViolationConstraint:
		return CategoryValidation
	case ViolationAmbiguous, ViolationConflicting,
		ViolationMissingContext, ViolationScopeUnclear:
		return CategorySemantic
	default:
		return CategoryUnknown
	}
}

// Violation records a single failure event.
type Violation struct {
	// Type is the specific violation type.
	Type ViolationType

	// Category is the violation category (derived from Type).
	Category ViolationCategory

	// Message is a human-readable description.
	Message string

	// Context contains additional details (e.g., {"path": "/config.yaml"}).
	Context map[string]string

	// Timestamp is when the violation occurred.
	Timestamp time.Time

	// Attempt is which retry attempt this was.
	Attempt int
}

// NewViolation creates a violation with auto-derived category.
//
// Description:
//
//	Creates a new Violation with the category automatically derived
//	from the violation type.
//
// Inputs:
//
//	vt - The violation type.
//	message - Human-readable description.
//	context - Additional context (can be nil).
//
// Outputs:
//
//	Violation - The created violation.
func NewViolation(vt ViolationType, message string, context map[string]string) Violation {
	return Violation{
		Type:      vt,
		Category:  CategoryForType(vt),
		Message:   message,
		Context:   context,
		Timestamp: time.Now(),
	}
}

// HelpRequest is a structured request for user assistance.
type HelpRequest struct {
	// Title is the help request title.
	Title string

	// WhatITried lists attempted actions.
	WhatITried []string

	// TheProblem explains the issue.
	TheProblem string

	// Suggestions lists how the user can help.
	Suggestions []string

	// CanSkip indicates if this step is optional.
	CanSkip bool

	// SkipMessage explains what happens if skipped.
	SkipMessage string
}

// String returns a formatted help request message.
func (h HelpRequest) String() string {
	var b strings.Builder

	b.WriteString("## ")
	b.WriteString(h.Title)
	b.WriteString("\n\n")

	if len(h.WhatITried) > 0 {
		b.WriteString("### What I Tried\n")
		for i, attempt := range h.WhatITried {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, attempt))
		}
		b.WriteString("\n")
	}

	if h.TheProblem != "" {
		b.WriteString("### The Problem\n")
		b.WriteString(h.TheProblem)
		b.WriteString("\n\n")
	}

	if len(h.Suggestions) > 0 {
		b.WriteString("### How You Can Help\n")
		for _, s := range h.Suggestions {
			b.WriteString("- ")
			b.WriteString(s)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if h.CanSkip && h.SkipMessage != "" {
		b.WriteString("### Or Skip This Step\n")
		b.WriteString(h.SkipMessage)
		b.WriteString("\n")
	}

	return b.String()
}

// StuckResult is returned when frustration threshold is exceeded.
type StuckResult struct {
	// IsStuck indicates if the agent is in a deadlock.
	IsStuck bool

	// ViolationType is the primary violation type.
	ViolationType ViolationType

	// ViolationCategory is the primary violation category.
	ViolationCategory ViolationCategory

	// Streak is the number of consecutive same-type violations.
	Streak int

	// DetectionRule is which rule triggered stuck ("same_type", "category_saturation", "oscillation").
	DetectionRule string

	// Violations are the recent violations for context.
	Violations []Violation

	// HelpRequest is the generated help message.
	HelpRequest HelpRequest
}

// FrustrationConfig configures detection thresholds.
type FrustrationConfig struct {
	// MaxHistory is how many violations to remember (default: 10).
	MaxHistory int

	// StreakThreshold triggers STUCK on N same-type violations (default: 3).
	StreakThreshold int

	// CategoryThreshold triggers STUCK on N same-category violations (default: 4).
	CategoryThreshold int

	// OscillationWindow detects A-B-A-B patterns in last N (default: 6).
	OscillationWindow int
}

// DefaultFrustrationConfig returns sensible defaults.
func DefaultFrustrationConfig() FrustrationConfig {
	return FrustrationConfig{
		MaxHistory:        DefaultMaxHistory,
		StreakThreshold:   DefaultStreakThreshold,
		CategoryThreshold: DefaultCategoryThreshold,
		OscillationWindow: DefaultOscillationWindow,
	}
}

// FrustrationTracker detects behavioral deadlocks.
//
// Description:
//
//	Tracks violations and detects patterns that indicate the agent is stuck
//	in an unresolvable loop. Uses three detection rules:
//	1. Same-type streak: N consecutive violations of the same type
//	2. Category saturation: N consecutive violations in the same category
//	3. Oscillation: Alternating between two violation types
//
// Thread Safety: Safe for concurrent use via mutex.
type FrustrationTracker struct {
	mu                sync.Mutex
	violations        []Violation
	maxHistory        int
	streakThreshold   int
	categoryThreshold int
	oscillationWindow int
}

// NewFrustrationTracker creates a tracker with the given configuration.
//
// Description:
//
//	Creates a FrustrationTracker with configured thresholds. Uses defaults
//	for any zero values in the config.
//
// Inputs:
//
//	config - Configuration options. Uses defaults if zero values.
//
// Outputs:
//
//	*FrustrationTracker - The configured tracker.
func NewFrustrationTracker(config FrustrationConfig) *FrustrationTracker {
	if config.MaxHistory <= 0 {
		config.MaxHistory = DefaultMaxHistory
	}
	if config.StreakThreshold <= 0 {
		config.StreakThreshold = DefaultStreakThreshold
	}
	if config.CategoryThreshold <= 0 {
		config.CategoryThreshold = DefaultCategoryThreshold
	}
	if config.OscillationWindow <= 0 {
		config.OscillationWindow = DefaultOscillationWindow
	}

	return &FrustrationTracker{
		violations:        make([]Violation, 0, config.MaxHistory),
		maxHistory:        config.MaxHistory,
		streakThreshold:   config.StreakThreshold,
		categoryThreshold: config.CategoryThreshold,
		oscillationWindow: config.OscillationWindow,
	}
}

// RecordViolation adds a violation and checks for deadlock.
//
// Description:
//
//	Records the violation, trims history if needed, checks all detection
//	rules, and returns a StuckResult indicating whether the agent is stuck.
//
// Inputs:
//
//	v - The violation to record.
//
// Outputs:
//
//	StuckResult - The detection result.
//
// Thread Safety: This method is safe for concurrent use.
func (f *FrustrationTracker) RecordViolation(v Violation) StuckResult {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Ensure timestamp is set
	if v.Timestamp.IsZero() {
		v.Timestamp = time.Now()
	}

	// Ensure category is derived
	if v.Category == "" {
		v.Category = CategoryForType(v.Type)
	}

	// Add violation
	f.violations = append(f.violations, v)

	// Trim to max history
	if len(f.violations) > f.maxHistory {
		f.violations = f.violations[len(f.violations)-f.maxHistory:]
	}

	// Record metric
	recordViolationMetric(v)

	// Check detection rules
	return f.checkStuckLocked()
}

// Reset clears violation history.
//
// Description:
//
//	Clears all recorded violations. Call this after user provides
//	clarification to give the agent a fresh start.
//
// Thread Safety: This method is safe for concurrent use.
func (f *FrustrationTracker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.violations = f.violations[:0]
}

// GetRecentViolations returns the last N violations.
//
// Description:
//
//	Returns a copy of the most recent violations for inspection.
//	Returns fewer than N if not enough violations recorded.
//
// Inputs:
//
//	n - Maximum number of violations to return.
//
// Outputs:
//
//	[]Violation - Copy of recent violations (newest last).
//
// Thread Safety: This method is safe for concurrent use.
func (f *FrustrationTracker) GetRecentViolations(n int) []Violation {
	f.mu.Lock()
	defer f.mu.Unlock()

	if n > len(f.violations) {
		n = len(f.violations)
	}

	result := make([]Violation, n)
	copy(result, f.violations[len(f.violations)-n:])
	return result
}

// IsStuck checks current state without adding a violation.
//
// Description:
//
//	Checks if the current violation history indicates a stuck state.
//	Does not modify the violation history.
//
// Outputs:
//
//	bool - True if the agent is stuck.
//
// Thread Safety: This method is safe for concurrent use.
func (f *FrustrationTracker) IsStuck() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkStuckLocked().IsStuck
}

// ViolationCount returns the number of recorded violations.
//
// Thread Safety: This method is safe for concurrent use.
func (f *FrustrationTracker) ViolationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.violations)
}

// checkStuckLocked checks all detection rules (caller must hold lock).
func (f *FrustrationTracker) checkStuckLocked() StuckResult {
	if len(f.violations) == 0 {
		return StuckResult{IsStuck: false}
	}

	// Rule 1: Same-type streak
	if result := f.checkSameTypeStreak(); result.IsStuck {
		return result
	}

	// Rule 2: Category saturation
	if result := f.checkCategorySaturation(); result.IsStuck {
		return result
	}

	// Rule 3: Oscillation
	if result := f.checkOscillation(); result.IsStuck {
		return result
	}

	return StuckResult{IsStuck: false}
}

// checkSameTypeStreak detects N consecutive same-type violations.
func (f *FrustrationTracker) checkSameTypeStreak() StuckResult {
	if len(f.violations) < f.streakThreshold {
		return StuckResult{IsStuck: false}
	}

	// Check last N violations for same type
	lastType := f.violations[len(f.violations)-1].Type
	streak := 1

	for i := len(f.violations) - 2; i >= 0 && streak < f.streakThreshold; i-- {
		if f.violations[i].Type == lastType {
			streak++
		} else {
			break
		}
	}

	if streak >= f.streakThreshold {
		recentViolations := f.getRecentViolationsLocked(streak)
		return StuckResult{
			IsStuck:           true,
			ViolationType:     lastType,
			ViolationCategory: CategoryForType(lastType),
			Streak:            streak,
			DetectionRule:     "same_type",
			Violations:        recentViolations,
			HelpRequest:       f.generateHelpRequest(recentViolations, "same_type"),
		}
	}

	return StuckResult{IsStuck: false}
}

// checkCategorySaturation detects N consecutive same-category violations.
func (f *FrustrationTracker) checkCategorySaturation() StuckResult {
	if len(f.violations) < f.categoryThreshold {
		return StuckResult{IsStuck: false}
	}

	// Check last N violations for same category
	lastCategory := f.violations[len(f.violations)-1].Category
	streak := 1

	for i := len(f.violations) - 2; i >= 0 && streak < f.categoryThreshold; i-- {
		if f.violations[i].Category == lastCategory {
			streak++
		} else {
			break
		}
	}

	if streak >= f.categoryThreshold {
		recentViolations := f.getRecentViolationsLocked(streak)
		return StuckResult{
			IsStuck:           true,
			ViolationType:     f.violations[len(f.violations)-1].Type,
			ViolationCategory: lastCategory,
			Streak:            streak,
			DetectionRule:     "category_saturation",
			Violations:        recentViolations,
			HelpRequest:       f.generateHelpRequest(recentViolations, "category_saturation"),
		}
	}

	return StuckResult{IsStuck: false}
}

// checkOscillation detects A-B-A-B alternating patterns.
func (f *FrustrationTracker) checkOscillation() StuckResult {
	if len(f.violations) < f.oscillationWindow {
		return StuckResult{IsStuck: false}
	}

	// Get last oscillationWindow violations
	window := f.violations[len(f.violations)-f.oscillationWindow:]

	// Check for alternating pattern
	if len(window) < 4 {
		return StuckResult{IsStuck: false}
	}

	// Extract the two types that might be alternating
	typeA := window[0].Type
	typeB := window[1].Type

	// Must be two different types
	if typeA == typeB {
		return StuckResult{IsStuck: false}
	}

	// Check if pattern is A-B-A-B-A-B or B-A-B-A-B-A
	oscillating := true
	for i := 0; i < len(window); i++ {
		expected := typeA
		if i%2 == 1 {
			expected = typeB
		}
		if window[i].Type != expected {
			oscillating = false
			break
		}
	}

	if oscillating {
		recentViolations := f.getRecentViolationsLocked(f.oscillationWindow)
		return StuckResult{
			IsStuck:           true,
			ViolationType:     typeA,
			ViolationCategory: CategoryForType(typeA),
			Streak:            f.oscillationWindow,
			DetectionRule:     "oscillation",
			Violations:        recentViolations,
			HelpRequest:       f.generateHelpRequest(recentViolations, "oscillation"),
		}
	}

	return StuckResult{IsStuck: false}
}

// getRecentViolationsLocked returns recent violations (caller must hold lock).
func (f *FrustrationTracker) getRecentViolationsLocked(n int) []Violation {
	if n > len(f.violations) {
		n = len(f.violations)
	}
	result := make([]Violation, n)
	copy(result, f.violations[len(f.violations)-n:])
	return result
}

// generateHelpRequest creates a help request from violations.
func (f *FrustrationTracker) generateHelpRequest(violations []Violation, rule string) HelpRequest {
	if len(violations) == 0 {
		return HelpRequest{
			Title:      "I Need Your Help",
			TheProblem: "I'm having trouble completing your request.",
		}
	}

	// Extract what was tried
	attempts := make([]string, 0, len(violations))
	for _, v := range violations {
		attempt := v.Message
		if v.Context != nil {
			if path, ok := v.Context["path"]; ok {
				attempt = fmt.Sprintf("%s (path: %s)", v.Message, path)
			}
		}
		attempts = append(attempts, attempt)
	}

	// Get the primary violation type
	vType := violations[len(violations)-1].Type

	// Generate problem description and suggestions based on type
	var problem string
	var suggestions []string
	var canSkip bool
	var skipMsg string

	switch vType {
	case ViolationFileNotFound:
		problem = "I cannot locate the requested file. I've searched but the file doesn't exist at the expected location."
		suggestions = []string{
			"**Provide the path**: Tell me the exact location of the file",
			"**Confirm it exists**: Check if the file exists with `ls -la`",
			"**Clarify the task**: Maybe we can approach this differently",
		}
		canSkip = true
		skipMsg = "If this file isn't critical, I can proceed without it. Just let me know."

	case ViolationPermissionDenied:
		problem = "I don't have permission to access the required resource."
		suggestions = []string{
			"**Check permissions**: Verify the file/directory permissions",
			"**Provide alternative**: Point me to a copy I can access",
			"**Grant access**: Update permissions if appropriate",
		}
		canSkip = false

	case ViolationIntentLoop:
		problem = "I keep generating responses that don't directly answer your question. I may be misunderstanding what you're asking for."
		suggestions = []string{
			"**Rephrase the question**: Try asking in a different way",
			"**Be more specific**: Tell me exactly what output format you need",
			"**Provide an example**: Show me what a good answer would look like",
		}
		canSkip = false

	case ViolationMalformedTool:
		problem = "I'm having trouble formatting my tool calls correctly. This is an internal issue I'm working around."
		suggestions = []string{
			"**Simplify the request**: Try breaking it into smaller steps",
			"**Be explicit**: Specify exactly which tool or action you want",
		}
		canSkip = false

	case ViolationAmbiguous:
		problem = "Your request could be interpreted in multiple ways and I'm not sure which approach you want."
		suggestions = []string{
			"**Clarify the goal**: What specific outcome are you looking for?",
			"**Provide context**: What problem are you trying to solve?",
			"**Choose an approach**: Would you prefer A or B?",
		}
		canSkip = false

	default:
		problem = "I've encountered repeated issues while trying to complete your request."
		suggestions = []string{
			"**Clarify your request**: Tell me more about what you need",
			"**Try a different approach**: Would an alternative method work?",
			"**Break it down**: Let's tackle this step by step",
		}
		canSkip = true
		skipMsg = "If this step isn't critical, I can try to proceed without it."
	}

	// Customize title based on rule
	title := "I Need Your Help"
	if rule == "oscillation" {
		title = "I'm Going in Circles"
		problem = "I keep alternating between different approaches but none are working. I may need clearer direction."
	} else if rule == "category_saturation" {
		title = "Repeated Issues"
	}

	return HelpRequest{
		Title:       title,
		WhatITried:  attempts,
		TheProblem:  problem,
		Suggestions: suggestions,
		CanSkip:     canSkip,
		SkipMessage: skipMsg,
	}
}

// Frustration metrics.
var (
	violationsTotal metric.Int64Counter
	stuckTotal      metric.Int64Counter

	frustrationMetricsOnce sync.Once
	frustrationMetricsErr  error
)

// initFrustrationMetrics initializes metrics.
func initFrustrationMetrics() error {
	frustrationMetricsOnce.Do(func() {
		var err error

		violationsTotal, err = frustrationMeter.Int64Counter(
			"trace_violations_total",
			metric.WithDescription("Total violations recorded by type"),
		)
		if err != nil {
			frustrationMetricsErr = err
			return
		}

		stuckTotal, err = frustrationMeter.Int64Counter(
			"trace_stuck_total",
			metric.WithDescription("Total stuck state entries by rule"),
		)
		if err != nil {
			frustrationMetricsErr = err
			return
		}
	})
	return frustrationMetricsErr
}

// recordViolationMetric records a violation metric.
func recordViolationMetric(v Violation) {
	if err := initFrustrationMetrics(); err != nil {
		return
	}

	violationsTotal.Add(nil, 1,
		metric.WithAttributes(
			attribute.String("type", string(v.Type)),
			attribute.String("category", string(v.Category)),
		),
	)
}

// RecordStuckMetric records when stuck state is entered.
func RecordStuckMetric(result StuckResult) {
	if err := initFrustrationMetrics(); err != nil || !result.IsStuck {
		return
	}

	stuckTotal.Add(nil, 1,
		metric.WithAttributes(
			attribute.String("type", string(result.ViolationType)),
			attribute.String("rule", result.DetectionRule),
		),
	)
}
