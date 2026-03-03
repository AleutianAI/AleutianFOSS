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

// RefactorType categorizes the type of refactoring suggestion.
type RefactorType string

const (
	// RefactorTypeExtractFunction suggests extracting code into a new function.
	RefactorTypeExtractFunction RefactorType = "extract_function"

	// RefactorTypeExtractInterface suggests extracting an interface.
	RefactorTypeExtractInterface RefactorType = "extract_interface"

	// RefactorTypeRename suggests renaming for clarity.
	RefactorTypeRename RefactorType = "rename"

	// RefactorTypeSimplify suggests simplifying complex logic.
	RefactorTypeSimplify RefactorType = "simplify"

	// RefactorTypeSplitFunction suggests splitting a large function.
	RefactorTypeSplitFunction RefactorType = "split_function"

	// RefactorTypeReduceParameters suggests reducing parameter count.
	RefactorTypeReduceParameters RefactorType = "reduce_parameters"

	// RefactorTypeRemoveDeadCode suggests removing unused code.
	RefactorTypeRemoveDeadCode RefactorType = "remove_dead_code"

	// RefactorTypeReduceNesting suggests reducing nesting depth.
	RefactorTypeReduceNesting RefactorType = "reduce_nesting"
)

// RiskLevel categorizes the risk of a refactoring.
type RiskLevel string

const (
	// RiskLevelLow indicates minimal risk of breaking changes.
	RiskLevelLow RiskLevel = "low"

	// RiskLevelMedium indicates moderate risk requiring testing.
	RiskLevelMedium RiskLevel = "medium"

	// RiskLevelHigh indicates significant risk requiring careful review.
	RiskLevelHigh RiskLevel = "high"
)

// RefactorSuggester provides refactoring suggestions for code.
//
// Description:
//
//	RefactorSuggester analyzes code and suggests improvements such as
//	extracting functions, reducing complexity, and improving naming.
//	All suggestions are confidence-calibrated and include risk assessment.
//
// Thread Safety:
//
//	RefactorSuggester is safe for concurrent use.
type RefactorSuggester struct {
	graph *graph.Graph
	index *index.SymbolIndex
	crs   CRSRecorder
}

// NewRefactorSuggester creates a new RefactorSuggester.
//
// Description:
//
//	Creates a suggester that can analyze code and provide refactoring
//	recommendations using the code graph and symbol index.
//
// Inputs:
//
//	g - The code graph. Must be frozen.
//	idx - The symbol index.
//
// Outputs:
//
//	*RefactorSuggester - The configured suggester.
func NewRefactorSuggester(g *graph.Graph, idx *index.SymbolIndex) *RefactorSuggester {
	return &RefactorSuggester{
		graph: g,
		index: idx,
		crs:   &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this suggester.
func (r *RefactorSuggester) SetCRS(recorder CRSRecorder) {
	r.crs = recorder
}

// CodeMetrics contains measured characteristics of code.
type CodeMetrics struct {
	// LineCount is the number of lines in the function body.
	LineCount int `json:"line_count"`

	// CyclomaticComplexity measures decision points (branches, loops).
	CyclomaticComplexity int `json:"cyclomatic_complexity"`

	// ParameterCount is the number of parameters.
	ParameterCount int `json:"parameter_count"`

	// ReturnCount is the number of return values.
	ReturnCount int `json:"return_count"`

	// NestingDepth is the maximum nesting level.
	NestingDepth int `json:"nesting_depth"`

	// CognitiveComplexity measures how hard the code is to understand.
	CognitiveComplexity int `json:"cognitive_complexity"`

	// CallerCount is the number of functions that call this one.
	CallerCount int `json:"caller_count"`

	// CalleeCount is the number of functions this one calls.
	CalleeCount int `json:"callee_count"`

	// IsExported indicates if the function is exported/public.
	IsExported bool `json:"is_exported"`
}

// Suggestion represents a single refactoring suggestion.
type Suggestion struct {
	// Type categorizes the refactoring.
	Type RefactorType `json:"type"`

	// Description explains what should be done.
	Description string `json:"description"`

	// Rationale explains why this refactoring is suggested.
	Rationale string `json:"rationale"`

	// CodeBefore shows the current code pattern (snippet or description).
	CodeBefore string `json:"code_before,omitempty"`

	// CodeAfter shows the suggested code pattern (snippet or description).
	CodeAfter string `json:"code_after,omitempty"`

	// Confidence is how confident we are in this suggestion (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// Basis explains what evidence supports this suggestion.
	Basis string `json:"basis"`

	// Risk indicates what could go wrong.
	Risk string `json:"risk"`

	// RiskLevel categorizes the risk.
	RiskLevel RiskLevel `json:"risk_level"`

	// Priority indicates suggested order of applying (1 = highest).
	Priority int `json:"priority"`

	// AffectedSymbols lists symbols that would be affected.
	AffectedSymbols []string `json:"affected_symbols,omitempty"`
}

// RefactorSuggestions is the result of refactoring analysis.
type RefactorSuggestions struct {
	// TargetID is the symbol being analyzed.
	TargetID string `json:"target_id"`

	// TargetName is the name of the symbol.
	TargetName string `json:"target_name"`

	// Suggestions lists all refactoring suggestions.
	Suggestions []Suggestion `json:"suggestions"`

	// Metrics contains the measured code characteristics.
	Metrics CodeMetrics `json:"metrics"`

	// OverallHealth is a score from 0-100 indicating code health.
	OverallHealth int `json:"overall_health"`

	// Limitations lists what we couldn't analyze.
	Limitations []string `json:"limitations"`
}

// SuggestRefactor analyzes a function and suggests refactorings.
//
// Description:
//
//	Analyzes the target function and provides calibrated refactoring
//	suggestions based on code metrics and patterns.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetID - The symbol ID to analyze.
//
// Outputs:
//
//	*RefactorSuggestions - Suggestions with confidence and risk assessment.
//	error - Non-nil if the analysis fails.
//
// Example:
//
//	suggestions, err := suggester.SuggestRefactor(ctx, "pkg/handlers.ProcessData")
//	for _, s := range suggestions.Suggestions {
//	    if s.Confidence > 0.7 {
//	        fmt.Printf("%s: %s\n", s.Type, s.Description)
//	    }
//	}
//
// Limitations:
//
//   - Cannot analyze runtime behavior
//   - Metrics are approximations without full AST parsing
//   - May not detect all code smells
func (r *RefactorSuggester) SuggestRefactor(
	ctx context.Context,
	targetID string,
) (*RefactorSuggestions, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startRefactorSpan(ctx, targetID)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if targetID == "" {
		return nil, ErrInvalidInput
	}
	if r.graph != nil && !r.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	// Find the target symbol
	symbol, found := r.index.GetByID(targetID)
	if !found || symbol == nil {
		return nil, ErrSymbolNotFound
	}

	result := &RefactorSuggestions{
		TargetID:    targetID,
		TargetName:  symbol.Name,
		Suggestions: make([]Suggestion, 0),
		Limitations: make([]string, 0),
	}

	// Calculate metrics
	metrics := r.calculateMetrics(symbol)
	result.Metrics = metrics

	// Generate suggestions based on metrics
	suggestions := r.generateSuggestions(symbol, metrics)
	result.Suggestions = suggestions

	// Calculate overall health
	result.OverallHealth = r.calculateHealth(metrics)

	// Sort suggestions by priority
	r.prioritizeSuggestions(result.Suggestions)

	dur := time.Since(start)
	setRefactorSpanResult(span, len(result.Suggestions), nil)
	recordRefactorMetrics(ctx, dur, len(result.Suggestions), nil)
	r.crs.RecordToolStep(ctx, "suggest_refactor", len(result.Suggestions), dur, nil)

	return result, nil
}

// calculateMetrics computes code metrics for a symbol.
func (r *RefactorSuggester) calculateMetrics(symbol *ast.Symbol) CodeMetrics {
	metrics := CodeMetrics{
		IsExported: symbol.Exported,
	}

	// Line count from symbol
	if symbol.EndLine > symbol.StartLine {
		metrics.LineCount = symbol.EndLine - symbol.StartLine + 1
	} else {
		metrics.LineCount = 1
	}

	// Parse signature for parameter/return counts
	if symbol.Signature != "" {
		parser := NewSignatureParser()
		sig, err := parser.ParseSignature(symbol.Signature, symbol.Language)
		if err == nil {
			metrics.ParameterCount = len(sig.Parameters)
			metrics.ReturnCount = len(sig.Returns)
		}
	}

	// Estimate complexity from symbol data
	// Real complexity would require full AST parsing
	metrics.CyclomaticComplexity = r.estimateCyclomaticComplexity(symbol)
	metrics.NestingDepth = r.estimateNestingDepth(symbol)
	metrics.CognitiveComplexity = r.estimateCognitiveComplexity(symbol, metrics)

	// Graph-based metrics
	if r.graph != nil {
		node, found := r.graph.GetNode(symbol.ID)
		if found && node != nil {
			for _, edge := range node.Incoming {
				if edge.Type == graph.EdgeTypeCalls {
					metrics.CallerCount++
				}
			}
			for _, edge := range node.Outgoing {
				if edge.Type == graph.EdgeTypeCalls {
					metrics.CalleeCount++
				}
			}
		}
	}

	return metrics
}

// estimateCyclomaticComplexity estimates complexity from available data.
func (r *RefactorSuggester) estimateCyclomaticComplexity(symbol *ast.Symbol) int {
	// Without full AST, we estimate based on line count
	// This is a rough heuristic: ~1 decision point per 5-10 lines
	complexity := 1 + (symbol.EndLine-symbol.StartLine)/7

	// Adjust based on symbol kind
	if symbol.Kind == ast.SymbolKindMethod {
		complexity++ // Methods often have implicit receiver checks
	}

	// Cap at reasonable values
	if complexity < 1 {
		complexity = 1
	}
	if complexity > 50 {
		complexity = 50
	}

	return complexity
}

// estimateNestingDepth estimates nesting from available data.
func (r *RefactorSuggester) estimateNestingDepth(symbol *ast.Symbol) int {
	// Without full AST, estimate based on line count
	// Longer functions tend to have deeper nesting
	lines := symbol.EndLine - symbol.StartLine
	if lines <= 10 {
		return 1
	}
	if lines <= 30 {
		return 2
	}
	if lines <= 60 {
		return 3
	}
	if lines <= 100 {
		return 4
	}
	return 5
}

// estimateCognitiveComplexity estimates cognitive load.
func (r *RefactorSuggester) estimateCognitiveComplexity(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) int {
	// Cognitive complexity combines multiple factors
	complexity := metrics.CyclomaticComplexity

	// Nesting increases cognitive load exponentially
	complexity += metrics.NestingDepth * 2

	// Many parameters are hard to remember
	if metrics.ParameterCount > 3 {
		complexity += metrics.ParameterCount - 3
	}

	// Long functions are harder to understand
	if metrics.LineCount > 30 {
		complexity += (metrics.LineCount - 30) / 10
	}

	return complexity
}

// generateSuggestions creates refactoring suggestions based on metrics.
func (r *RefactorSuggester) generateSuggestions(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) []Suggestion {
	suggestions := make([]Suggestion, 0)

	// Check for long function
	if metrics.LineCount > 30 {
		suggestions = append(suggestions, r.suggestSplitFunction(symbol, metrics))
	}

	// Check for too many parameters
	if metrics.ParameterCount > 4 {
		suggestions = append(suggestions, r.suggestReduceParameters(symbol, metrics))
	}

	// Check for high complexity
	if metrics.CyclomaticComplexity > 10 {
		suggestions = append(suggestions, r.suggestSimplify(symbol, metrics))
	}

	// Check for deep nesting
	if metrics.NestingDepth > 3 {
		suggestions = append(suggestions, r.suggestReduceNesting(symbol, metrics))
	}

	// Check for potential interface extraction
	if metrics.CallerCount > 5 && symbol.Exported {
		suggestions = append(suggestions, r.suggestExtractInterface(symbol, metrics))
	}

	// Check for unclear naming
	if r.hasUnclearName(symbol) {
		suggestions = append(suggestions, r.suggestRename(symbol, metrics))
	}

	// Check for unused function
	if metrics.CallerCount == 0 && symbol.Exported {
		suggestions = append(suggestions, r.suggestRemoveDeadCode(symbol, metrics))
	}

	return suggestions
}

// suggestSplitFunction suggests splitting a long function.
func (r *RefactorSuggester) suggestSplitFunction(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.75)

	// Stronger suggestion for very long functions
	if metrics.LineCount > 60 {
		cal.Apply(AdjustmentHighComplexity)
	} else {
		cal.Apply(AdjustmentLowComplexity)
	}

	// Higher risk for exported functions
	risk := RiskLevelMedium
	riskStr := "Callers may need to be updated if splitting changes the API"
	if symbol.Exported {
		cal.Apply(AdjustmentExportedSymbol)
		risk = RiskLevelHigh
		riskStr = "Exported function - splitting may require API changes"
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeSplitFunction,
		Description: "Split this function into smaller, focused functions",
		Rationale:   "Function has " + intToString(metrics.LineCount) + " lines, which exceeds the recommended ~30 lines",
		Basis:       "Line count analysis",
		Risk:        riskStr,
		RiskLevel:   risk,
		Confidence:  cal.FinalScore,
		Priority:    2,
	}
}

// suggestReduceParameters suggests using a config struct.
func (r *RefactorSuggester) suggestReduceParameters(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.8)

	if metrics.ParameterCount > 6 {
		cal.Apply(AdjustmentHighComplexity)
	}

	// Higher risk for exported functions
	risk := RiskLevelMedium
	if symbol.Exported {
		cal.Apply(AdjustmentExportedSymbol)
		risk = RiskLevelHigh
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeReduceParameters,
		Description: "Group parameters into a configuration struct",
		Rationale:   "Function has " + intToString(metrics.ParameterCount) + " parameters, which is hard to remember and use correctly",
		CodeBefore:  symbol.Signature,
		CodeAfter:   "func " + symbol.Name + "(cfg " + symbol.Name + "Config) ...",
		Basis:       "Parameter count analysis",
		Risk:        "All callers need to be updated to use the new struct",
		RiskLevel:   risk,
		Confidence:  cal.FinalScore,
		Priority:    3,
	}
}

// suggestSimplify suggests reducing complexity.
func (r *RefactorSuggester) suggestSimplify(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.7)

	if metrics.CyclomaticComplexity > 15 {
		cal.Apply(AdjustmentHighComplexity)
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeSimplify,
		Description: "Simplify complex logic by extracting decision branches",
		Rationale:   "Cyclomatic complexity of ~" + intToString(metrics.CyclomaticComplexity) + " indicates many code paths",
		Basis:       "Complexity estimation",
		Risk:        "Logic changes may introduce subtle bugs",
		RiskLevel:   RiskLevelMedium,
		Confidence:  cal.FinalScore,
		Priority:    1,
	}
}

// suggestReduceNesting suggests flattening nested code.
func (r *RefactorSuggester) suggestReduceNesting(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.75)

	if metrics.NestingDepth > 4 {
		cal.Apply(AdjustmentHighComplexity)
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeReduceNesting,
		Description: "Reduce nesting by using early returns or extracting functions",
		Rationale:   "Deep nesting (~" + intToString(metrics.NestingDepth) + " levels) makes code hard to follow",
		Basis:       "Nesting depth estimation",
		Risk:        "Control flow changes may affect behavior",
		RiskLevel:   RiskLevelMedium,
		Confidence:  cal.FinalScore,
		Priority:    2,
	}
}

// suggestExtractInterface suggests creating an interface.
func (r *RefactorSuggester) suggestExtractInterface(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.65)

	// More callers = stronger suggestion
	if metrics.CallerCount > 10 {
		cal.Apply(AdjustmentManyCallers)
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeExtractInterface,
		Description: "Extract interface to enable testing and flexibility",
		Rationale:   "Function has " + intToString(metrics.CallerCount) + " callers - an interface would allow mocking",
		Basis:       "Caller analysis",
		Risk:        "Minimal risk - interface extraction is additive",
		RiskLevel:   RiskLevelLow,
		Confidence:  cal.FinalScore,
		Priority:    4,
	}
}

// suggestRename suggests improving the name.
func (r *RefactorSuggester) suggestRename(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.6)

	// Higher risk for exported functions
	risk := RiskLevelLow
	if symbol.Exported {
		cal.Apply(AdjustmentExportedSymbol)
		risk = RiskLevelMedium
	}

	// More callers = more risk
	if metrics.CallerCount > 10 {
		cal.Apply(AdjustmentManyCallers)
		risk = RiskLevelMedium
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeRename,
		Description: "Consider renaming for clarity",
		Rationale:   "Name '" + symbol.Name + "' may not clearly describe the function's purpose",
		Basis:       "Naming pattern analysis",
		Risk:        "All callers need to be updated",
		RiskLevel:   risk,
		Confidence:  cal.FinalScore,
		Priority:    5,
	}
}

// suggestRemoveDeadCode suggests removing unused code.
func (r *RefactorSuggester) suggestRemoveDeadCode(
	symbol *ast.Symbol,
	metrics CodeMetrics,
) Suggestion {
	cal := NewConfidenceCalibration(0.5)

	// Lower confidence because it might be used via reflection or external calls
	if symbol.Exported {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "exported may be used externally",
			Multiplier: 0.6,
		})
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return Suggestion{
		Type:        RefactorTypeRemoveDeadCode,
		Description: "Consider removing unused function",
		Rationale:   "No callers found in the codebase",
		Basis:       "Call graph analysis",
		Risk:        "May be used via reflection, tests, or external packages",
		RiskLevel:   RiskLevelMedium,
		Confidence:  cal.FinalScore,
		Priority:    5,
	}
}

// hasUnclearName checks if a symbol has an unclear name.
func (r *RefactorSuggester) hasUnclearName(symbol *ast.Symbol) bool {
	name := symbol.Name

	// Too short
	if len(name) <= 2 && name != "Do" && name != "Run" {
		return true
	}

	// Generic names
	genericNames := []string{
		"Process", "Handle", "Do", "Execute", "Run",
		"Func", "Helper", "Util", "Data", "Info",
	}
	for _, generic := range genericNames {
		if name == generic {
			return true
		}
	}

	// Too many abbreviations (rough check)
	vowels := 0
	for _, c := range name {
		if c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u' ||
			c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U' {
			vowels++
		}
	}
	if len(name) >= 4 && vowels < 2 {
		return true // Likely an abbreviation
	}

	return false
}

// calculateHealth computes an overall health score (0-100).
func (r *RefactorSuggester) calculateHealth(metrics CodeMetrics) int {
	health := 100

	// Deduct for long functions
	if metrics.LineCount > 30 {
		health -= (metrics.LineCount - 30) / 2
	}

	// Deduct for high complexity
	if metrics.CyclomaticComplexity > 10 {
		health -= (metrics.CyclomaticComplexity - 10) * 3
	}

	// Deduct for many parameters
	if metrics.ParameterCount > 4 {
		health -= (metrics.ParameterCount - 4) * 5
	}

	// Deduct for deep nesting
	if metrics.NestingDepth > 3 {
		health -= (metrics.NestingDepth - 3) * 8
	}

	// Deduct for high cognitive complexity
	if metrics.CognitiveComplexity > 15 {
		health -= (metrics.CognitiveComplexity - 15) * 2
	}

	// Clamp to valid range
	if health < 0 {
		health = 0
	}
	if health > 100 {
		health = 100
	}

	return health
}

// prioritizeSuggestions sorts suggestions by priority.
func (r *RefactorSuggester) prioritizeSuggestions(suggestions []Suggestion) {
	// Simple insertion sort since we expect few suggestions
	for i := 1; i < len(suggestions); i++ {
		for j := i; j > 0 && suggestions[j].Priority < suggestions[j-1].Priority; j-- {
			suggestions[j], suggestions[j-1] = suggestions[j-1], suggestions[j]
		}
	}
}

// SuggestRefactorBatch analyzes multiple functions for refactoring.
//
// Description:
//
//	Analyzes multiple functions and provides refactoring suggestions
//	for each one. More efficient than individual calls.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetIDs - The symbol IDs to analyze.
//
// Outputs:
//
//	map[string]*RefactorSuggestions - Suggestions keyed by targetID.
//	error - Non-nil if the analysis fails completely.
func (r *RefactorSuggester) SuggestRefactorBatch(
	ctx context.Context,
	targetIDs []string,
) (map[string]*RefactorSuggestions, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	results := make(map[string]*RefactorSuggestions)

	for _, targetID := range targetIDs {
		if err := ctx.Err(); err != nil {
			return results, ErrContextCanceled
		}

		suggestions, err := r.SuggestRefactor(ctx, targetID)
		if err != nil {
			results[targetID] = &RefactorSuggestions{
				TargetID:    targetID,
				Limitations: []string{"Analysis failed: " + err.Error()},
			}
			continue
		}
		results[targetID] = suggestions
	}

	return results, nil
}

// GetUnhealthyFunctions returns functions with health below threshold.
//
// Description:
//
//	Scans all functions and returns those with a health score below
//	the specified threshold. Useful for finding code that needs attention.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	threshold - Minimum health score (0-100).
//
// Outputs:
//
//	[]RefactorSuggestions - Analysis for unhealthy functions.
//	error - Non-nil if the analysis fails.
func (r *RefactorSuggester) GetUnhealthyFunctions(
	ctx context.Context,
	threshold int,
) ([]RefactorSuggestions, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	unhealthy := make([]RefactorSuggestions, 0)

	// Get all functions
	allFuncs := r.index.GetByKind(ast.SymbolKindFunction)
	allMethods := r.index.GetByKind(ast.SymbolKindMethod)
	allSymbols := append(allFuncs, allMethods...)

	for _, fn := range allSymbols {
		if ctx.Err() != nil {
			return unhealthy, ErrContextCanceled
		}

		// Skip test functions
		if isTestFunction(fn) {
			continue
		}

		suggestions, err := r.SuggestRefactor(ctx, fn.ID)
		if err != nil {
			continue
		}

		if suggestions.OverallHealth < threshold {
			unhealthy = append(unhealthy, *suggestions)
		}
	}

	return unhealthy, nil
}

// CalculateMetricsOnly calculates metrics without generating suggestions.
//
// Description:
//
//	Calculates code metrics for a symbol without the overhead of
//	generating suggestions. Useful for dashboards and reporting.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetID - The symbol ID to analyze.
//
// Outputs:
//
//	*CodeMetrics - The calculated metrics.
//	error - Non-nil if the analysis fails.
func (r *RefactorSuggester) CalculateMetricsOnly(
	ctx context.Context,
	targetID string,
) (*CodeMetrics, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if targetID == "" {
		return nil, ErrInvalidInput
	}

	symbol, found := r.index.GetByID(targetID)
	if !found || symbol == nil {
		return nil, ErrSymbolNotFound
	}

	metrics := r.calculateMetrics(symbol)
	return &metrics, nil
}

// GetHealthDistribution returns a distribution of function health scores.
//
// Description:
//
//	Analyzes all functions and groups them by health score ranges.
//	Useful for understanding overall codebase health.
//
// Inputs:
//
//	ctx - Context for cancellation.
//
// Outputs:
//
//	map[string]int - Count of functions in each health range.
//	error - Non-nil if the analysis fails.
func (r *RefactorSuggester) GetHealthDistribution(
	ctx context.Context,
) (map[string]int, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	distribution := map[string]int{
		"excellent (90-100)": 0,
		"good (70-89)":       0,
		"fair (50-69)":       0,
		"poor (30-49)":       0,
		"critical (0-29)":    0,
	}

	// Get all functions
	allFuncs := r.index.GetByKind(ast.SymbolKindFunction)
	allMethods := r.index.GetByKind(ast.SymbolKindMethod)
	allSymbols := append(allFuncs, allMethods...)

	for _, fn := range allSymbols {
		if ctx.Err() != nil {
			return distribution, ErrContextCanceled
		}

		// Skip test functions
		if isTestFunction(fn) {
			continue
		}

		metrics := r.calculateMetrics(fn)
		health := r.calculateHealth(metrics)

		switch {
		case health >= 90:
			distribution["excellent (90-100)"]++
		case health >= 70:
			distribution["good (70-89)"]++
		case health >= 50:
			distribution["fair (50-69)"]++
		case health >= 30:
			distribution["poor (30-49)"]++
		default:
			distribution["critical (0-29)"]++
		}
	}

	return distribution, nil
}

// Helper to generate unique names
func uniqueName(base string, existing map[string]bool) string {
	name := base
	counter := 1
	for existing[name] {
		name = base + intToString(counter)
		counter++
	}
	return name
}

// Helper to check if a string contains any of the patterns
func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
