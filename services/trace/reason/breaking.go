// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package reason

import (
	"context"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// BreakingChangeAnalyzer analyzes proposed changes for breaking impact.
//
// Description:
//
//	BreakingChangeAnalyzer helps the agent understand the impact of proposed
//	changes before making them. It identifies callers that would break and
//	provides confidence-calibrated assessments.
//
// Thread Safety:
//
//	BreakingChangeAnalyzer is safe for concurrent use. It performs read-only
//	operations on the graph and index.
type BreakingChangeAnalyzer struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	parser *SignatureParser
	crs    CRSRecorder
}

// NewBreakingChangeAnalyzer creates a new BreakingChangeAnalyzer.
//
// Description:
//
//	Creates an analyzer that can detect breaking changes in proposed
//	signature modifications.
//
// Inputs:
//
//	g - The code graph. Must be frozen.
//	idx - The symbol index.
//
// Outputs:
//
//	*BreakingChangeAnalyzer - The configured analyzer.
//
// Example:
//
//	analyzer := NewBreakingChangeAnalyzer(graph, index)
//	analysis, err := analyzer.AnalyzeBreaking(ctx, "pkg.Handler", "func(ctx, req) error")
func NewBreakingChangeAnalyzer(g *graph.Graph, idx *index.SymbolIndex) *BreakingChangeAnalyzer {
	return &BreakingChangeAnalyzer{
		graph:  g,
		index:  idx,
		parser: NewSignatureParser(),
		crs:    &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this analyzer.
func (a *BreakingChangeAnalyzer) SetCRS(recorder CRSRecorder) {
	a.crs = recorder
}

// AnalyzeBreaking analyzes whether a proposed signature change would break callers.
//
// Description:
//
//	Compares the current signature of the target symbol against a proposed
//	new signature. Identifies all breaking changes and the callers that
//	would be affected.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetID - The symbol ID to analyze.
//	proposedSig - The proposed new signature.
//
// Outputs:
//
//	*BreakingAnalysis - Analysis results including affected callers.
//	error - Non-nil if the analysis fails.
//
// Example:
//
//	analysis, err := analyzer.AnalyzeBreaking(ctx,
//	    "handlers/user.go:10:HandleUser",
//	    "func(ctx context.Context, req *UserRequest, opts Options) (*Response, error)",
//	)
//	if analysis.IsBreaking {
//	    fmt.Printf("%d callers would break\n", analysis.CallersAffected)
//	}
//
// Limitations:
//
//   - Only detects structural breaking changes (signatures, types)
//   - Does not detect behavioral breaking changes
//   - Requires the graph to be frozen
//   - May not detect all edge cases
func (a *BreakingChangeAnalyzer) AnalyzeBreaking(
	ctx context.Context,
	targetID string,
	proposedSig string,
) (*BreakingAnalysis, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	ctx, span := startAnalysisSpan(ctx, "AnalyzeBreaking", targetID)
	defer span.End()
	start := time.Now()

	if err := ctx.Err(); err != nil {
		setAnalysisSpanResult(span, false, 0, false)
		recordAnalysisMetrics(ctx, "analyze_breaking", time.Since(start), false, 0, false)
		return nil, ErrContextCanceled
	}
	if targetID == "" || proposedSig == "" {
		setAnalysisSpanResult(span, false, 0, false)
		recordAnalysisMetrics(ctx, "analyze_breaking", time.Since(start), false, 0, false)
		return nil, ErrInvalidInput
	}
	if a.graph != nil && !a.graph.IsFrozen() {
		setAnalysisSpanResult(span, false, 0, false)
		recordAnalysisMetrics(ctx, "analyze_breaking", time.Since(start), false, 0, false)
		return nil, ErrGraphNotReady
	}

	// Find the target symbol
	symbol, found := a.index.GetByID(targetID)
	if !found || symbol == nil {
		return nil, ErrSymbolNotFound
	}

	result := &BreakingAnalysis{
		TargetID:        targetID,
		BreakingChanges: make([]BreakingChange, 0),
		SafeChanges:     make([]string, 0),
		Limitations:     make([]string, 0),
	}

	// Parse current and proposed signatures
	currentSig, err := a.parser.ParseSignature(symbol.Signature, symbol.Language)
	if err != nil {
		result.Limitations = append(result.Limitations,
			"Could not parse current signature: "+err.Error())
		// Continue with limited analysis
	}

	proposedParsed, err := a.parser.ParseSignature(proposedSig, symbol.Language)
	if err != nil {
		result.Limitations = append(result.Limitations,
			"Could not parse proposed signature: "+err.Error())
		return result, nil
	}

	// Compare signatures
	if currentSig != nil {
		changes := CompareSignatures(currentSig, proposedParsed)
		result.BreakingChanges = append(result.BreakingChanges, changes...)
	}

	// Check visibility changes
	visibilityChange := a.checkVisibilityChange(symbol, proposedSig)
	if visibilityChange != nil {
		result.BreakingChanges = append(result.BreakingChanges, *visibilityChange)
	}

	// Find affected callers
	if a.graph != nil {
		callers := a.findCallers(targetID)
		for _, change := range result.BreakingChanges {
			// Copy to avoid modifying the same slice
			change.Affected = callers
		}
		result.CallersAffected = len(callers)

		// Update breaking changes with caller info
		for i := range result.BreakingChanges {
			result.BreakingChanges[i].Affected = callers
		}
	} else {
		result.Limitations = append(result.Limitations,
			"Graph not available - cannot determine affected callers")
	}

	// Determine if breaking
	result.IsBreaking = len(result.BreakingChanges) > 0

	// Calculate confidence
	result.Confidence = a.calculateConfidence(symbol, result)

	// Identify safe changes
	result.SafeChanges = a.identifySafeChanges(currentSig, proposedParsed)

	dur := time.Since(start)
	setAnalysisSpanResult(span, result.IsBreaking, result.CallersAffected, true)
	recordAnalysisMetrics(ctx, "analyze_breaking", dur, result.IsBreaking, result.CallersAffected, true)
	a.crs.RecordToolStep(ctx, "check_breaking_changes", len(result.BreakingChanges), dur, nil)

	return result, nil
}

// checkVisibilityChange checks for visibility changes (exported → unexported).
func (a *BreakingChangeAnalyzer) checkVisibilityChange(symbol *ast.Symbol, proposedSig string) *BreakingChange {
	if symbol.Language != "go" {
		return nil
	}

	// Extract name from proposed signature
	proposedName := extractFunctionName(proposedSig)
	if proposedName == "" {
		return nil
	}

	currentExported := symbol.Exported
	proposedExported := isGoExported(proposedName)

	if currentExported && !proposedExported {
		return &BreakingChange{
			Type:        BreakingChangeVisibility,
			Description: "Symbol changed from exported to unexported",
			Severity:    SeverityCritical,
			AutoFixable: false,
		}
	}

	return nil
}

// findCallers finds all callers of the target symbol.
func (a *BreakingChangeAnalyzer) findCallers(targetID string) []string {
	callers := make([]string, 0)

	node, found := a.graph.GetNode(targetID)
	if !found || node == nil {
		return callers
	}

	for _, edge := range node.Incoming {
		if edge.Type == graph.EdgeTypeCalls {
			callers = append(callers, edge.FromID)
		}
	}

	return callers
}

// calculateConfidence computes a calibrated confidence score for the analysis.
func (a *BreakingChangeAnalyzer) calculateConfidence(
	symbol *ast.Symbol,
	analysis *BreakingAnalysis,
) float64 {
	// Start with base confidence
	cal := NewConfidenceCalibration(0.8)

	// Apply adjustments based on context
	cal.ApplyIf(symbol.Exported, AdjustmentExportedSymbol)
	cal.ApplyIf(!symbol.Exported, AdjustmentUnexportedSymbol)
	cal.ApplyIf(analysis.CallersAffected > 10, AdjustmentManyCallers)
	cal.ApplyIf(analysis.CallersAffected < 3, AdjustmentFewCallers)
	cal.ApplyIf(isTestFile(symbol.FilePath), AdjustmentInTestFile)
	cal.ApplyIf(symbol.Kind == ast.SymbolKindMethod &&
		symbol.Receiver != "" &&
		strings.Contains(symbol.Receiver, "interface"),
		AdjustmentInterfaceMethod)

	// Reduce confidence for each limitation
	for range analysis.Limitations {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "analysis limitation",
			Multiplier: 0.9,
		})
	}

	// Always apply static analysis limitation
	cal.Apply(AdjustmentStaticAnalysisOnly)

	return cal.FinalScore
}

// identifySafeChanges identifies changes that are safe (non-breaking).
func (a *BreakingChangeAnalyzer) identifySafeChanges(
	current, proposed *ParsedSignature,
) []string {
	safe := make([]string, 0)

	if current == nil || proposed == nil {
		return safe
	}

	// Adding optional parameters is safe
	if len(proposed.Parameters) > len(current.Parameters) {
		allOptional := true
		for i := len(current.Parameters); i < len(proposed.Parameters); i++ {
			if !proposed.Parameters[i].Optional {
				allOptional = false
				break
			}
		}
		if allOptional {
			safe = append(safe, "New optional parameters added (safe)")
		}
	}

	// Documentation-only changes are safe
	if current.Name == proposed.Name &&
		len(current.Parameters) == len(proposed.Parameters) &&
		len(current.Returns) == len(proposed.Returns) {
		allSame := true
		for i := range current.Parameters {
			if !typesEqual(current.Parameters[i].Type, proposed.Parameters[i].Type) {
				allSame = false
				break
			}
		}
		for i := range current.Returns {
			if !typesEqual(current.Returns[i], proposed.Returns[i]) {
				allSame = false
				break
			}
		}
		if allSame {
			safe = append(safe, "Signature unchanged (safe)")
		}
	}

	return safe
}

// AnalyzeBreakingBatch analyzes multiple symbols for breaking changes.
//
// Description:
//
//	Analyzes multiple target symbols against proposed signatures in a batch.
//	More efficient than individual calls when analyzing related changes.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	changes - Map of targetID to proposed signature.
//
// Outputs:
//
//	map[string]*BreakingAnalysis - Analysis results keyed by targetID.
//	error - Non-nil if the analysis fails.
func (a *BreakingChangeAnalyzer) AnalyzeBreakingBatch(
	ctx context.Context,
	changes map[string]string,
) (map[string]*BreakingAnalysis, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	results := make(map[string]*BreakingAnalysis)

	for targetID, proposedSig := range changes {
		if err := ctx.Err(); err != nil {
			return results, ErrContextCanceled
		}

		analysis, err := a.AnalyzeBreaking(ctx, targetID, proposedSig)
		if err != nil {
			// Store error in limitations rather than failing the whole batch
			results[targetID] = &BreakingAnalysis{
				TargetID:    targetID,
				Limitations: []string{"Analysis failed: " + err.Error()},
			}
			continue
		}
		results[targetID] = analysis
	}

	return results, nil
}

// Helper functions

func extractFunctionName(sig string) string {
	// Extract function name from signature
	// Handles: "func Name(...)" or "func (r *T) Name(...)"
	sig = strings.TrimSpace(sig)

	if !strings.HasPrefix(sig, "func") {
		return ""
	}

	rest := strings.TrimPrefix(sig, "func")
	rest = strings.TrimSpace(rest)

	// Check for receiver
	if strings.HasPrefix(rest, "(") {
		// Find end of receiver by counting parentheses
		depth := 0
		endIdx := -1
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					endIdx = i
				}
			}
			if endIdx >= 0 {
				break
			}
		}
		if endIdx >= 0 && endIdx+1 < len(rest) {
			rest = strings.TrimSpace(rest[endIdx+1:])
		} else {
			return ""
		}
	}

	// Now rest should start with name
	if idx := strings.Index(rest, "("); idx > 0 {
		return strings.TrimSpace(rest[:idx])
	}

	return ""
}

func isGoExported(name string) bool {
	if name == "" {
		return false
	}
	// First letter uppercase = exported
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

func isTestFile(filePath string) bool {
	return strings.HasSuffix(filePath, "_test.go") ||
		strings.HasSuffix(filePath, "_test.py") ||
		strings.HasSuffix(filePath, ".test.ts") ||
		strings.HasSuffix(filePath, ".test.js") ||
		strings.HasSuffix(filePath, ".spec.ts") ||
		strings.HasSuffix(filePath, ".spec.js")
}
