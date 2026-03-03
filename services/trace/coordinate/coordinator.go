// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package coordinate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/analysis"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/reason"
)

// MultiFileChangeCoordinator plans, validates, and previews coordinated changes.
//
// # Description
//
// MultiFileChangeCoordinator handles multi-file change coordination for the agent.
// It analyzes a proposed change to one symbol and generates a complete plan for
// all files that need to be updated together. This is READ-ONLY analysis - actual
// file editing is done by the agent using Edit tools after user approval.
//
// # Thread Safety
//
// This type is safe for concurrent use.
type MultiFileChangeCoordinator struct {
	graph     *graph.Graph
	index     *index.SymbolIndex
	breaking  *reason.BreakingChangeAnalyzer
	blast     *analysis.BlastRadiusAnalyzer
	validator *reason.ChangeValidator

	// CRS Layer 2: CDCL clause generation recorder.
	crs CRSRecorder

	// Internal state
	mu    sync.RWMutex
	plans map[string]*ChangePlan
}

// NewMultiFileChangeCoordinator creates a new coordinator.
//
// # Description
//
// Creates a coordinator that uses the provided graph, index, and analyzers
// to generate multi-file change plans.
//
// # Inputs
//
//   - g: Code graph. Must be frozen before PlanChanges().
//   - idx: Symbol index for lookups.
//   - breaking: Breaking change analyzer.
//   - blast: Blast radius analyzer.
//   - validator: Change validator.
//
// # Outputs
//
//   - *MultiFileChangeCoordinator: Configured coordinator.
//
// # Example
//
//	coordinator := NewMultiFileChangeCoordinator(
//	    graph, index,
//	    reason.NewBreakingChangeAnalyzer(graph, index),
//	    analysis.NewBlastRadiusAnalyzer(graph, index, nil),
//	    reason.NewChangeValidator(index),
//	)
func NewMultiFileChangeCoordinator(
	g *graph.Graph,
	idx *index.SymbolIndex,
	breaking *reason.BreakingChangeAnalyzer,
	blast *analysis.BlastRadiusAnalyzer,
	validator *reason.ChangeValidator,
) *MultiFileChangeCoordinator {
	return &MultiFileChangeCoordinator{
		graph:     g,
		index:     idx,
		breaking:  breaking,
		blast:     blast,
		validator: validator,
		crs:       &NopCRSRecorder{},
		plans:     make(map[string]*ChangePlan),
	}
}

// SetCRS sets the CRS recorder for CDCL clause generation.
//
// # Description
//
// Replaces the default NopCRSRecorder with a real recorder that emits
// CRS tool steps for PlanChanges and ValidatePlan operations.
//
// # Inputs
//
//   - recorder: The CRS recorder to use. Must not be nil.
//
// # Thread Safety
//
// Must be called before any concurrent use of PlanChanges or ValidatePlan.
// Typically called once during initialization.
func (c *MultiFileChangeCoordinator) SetCRS(recorder CRSRecorder) {
	if recorder == nil {
		return
	}
	c.crs = recorder
}

// PlanChanges creates a coordinated change plan.
//
// # Description
//
// Analyzes the primary change and generates a complete plan covering all
// files that need to be updated. Uses blast radius to find affected callers
// and generates specific code changes for each file.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - changes: The change set to plan.
//   - opts: Optional configuration.
//
// # Outputs
//
//   - *ChangePlan: Complete plan with all file changes.
//   - error: Non-nil on validation failure or if symbol not found.
//
// # Example
//
//	plan, err := coordinator.PlanChanges(ctx, ChangeSet{
//	    PrimaryChange: ChangeRequest{
//	        TargetID:     "order/service.go:10:ProcessOrder",
//	        ChangeType:   ChangeAddParameter,
//	        NewSignature: "func (s *Service) ProcessOrder(ctx context.Context, order *Order) error",
//	    },
//	    Description: "Add context parameter to ProcessOrder",
//	}, nil)
func (c *MultiFileChangeCoordinator) PlanChanges(
	ctx context.Context,
	changes ChangeSet,
	opts *PlanOptions,
) (*ChangePlan, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startPlanSpan(ctx, changes.PrimaryChange.TargetID)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if changes.PrimaryChange.TargetID == "" {
		return nil, fmt.Errorf("%w: target ID is empty", ErrInvalidInput)
	}

	options := DefaultPlanOptions()
	if opts != nil {
		options = *opts
	}

	// Find the target symbol
	symbol, found := c.index.GetByID(changes.PrimaryChange.TargetID)
	if !found {
		return nil, ErrSymbolNotFound
	}

	// Generate unique plan ID
	planID := fmt.Sprintf("plan_%d", time.Now().UnixNano())

	plan := &ChangePlan{
		ID:            planID,
		Description:   changes.Description,
		PrimaryChange: changes.PrimaryChange,
		FileChanges:   make([]FileChange, 0),
		Order:         make([]string, 0),
		Warnings:      make([]string, 0),
		Limitations:   make([]string, 0),
		CreatedAt:     time.Now().UnixMilli(),
	}

	// Get blast radius to find affected files
	blastResult, err := c.blast.Analyze(ctx, changes.PrimaryChange.TargetID, nil)
	if err != nil {
		plan.Limitations = append(plan.Limitations, "Could not analyze blast radius: "+err.Error())
	}

	// Generate primary file change
	primaryChange := c.generatePrimaryChange(symbol, changes.PrimaryChange)
	plan.FileChanges = append(plan.FileChanges, primaryChange)

	// Generate changes for affected callers
	if blastResult != nil {
		callerChanges := c.generateCallerChanges(ctx, blastResult, changes.PrimaryChange, options)
		plan.FileChanges = append(plan.FileChanges, callerChanges...)

		// Generate changes for implementers (if interface change)
		if symbol.Kind == ast.SymbolKindInterface {
			implChanges := c.generateImplementerChanges(ctx, blastResult.Implementers, changes.PrimaryChange)
			plan.FileChanges = append(plan.FileChanges, implChanges...)
		}
	}

	// Build dependency order (target first, then callers)
	plan.Order = c.buildChangeOrder(plan.FileChanges)

	// Calculate risk level
	plan.RiskLevel = c.calculateRiskLevel(plan, blastResult)

	// Calculate confidence
	plan.Confidence = c.calculateConfidence(plan, blastResult)

	// Set totals
	plan.TotalFiles = countUniqueFiles(plan.FileChanges)
	plan.TotalChanges = len(plan.FileChanges)

	// Store plan for later retrieval
	c.mu.Lock()
	c.plans[planID] = plan
	c.mu.Unlock()

	dur := time.Since(start)
	setPlanSpanResult(span, plan.TotalFiles, nil)
	recordPlanMetrics(ctx, dur, nil)
	c.crs.RecordToolStep(ctx, "plan_changes", plan.TotalFiles, dur, nil)

	return plan, nil
}

// ValidatePlan validates that a change plan would compile.
//
// # Description
//
// Validates each file change for syntax errors, type reference existence,
// and import resolution. Reports all errors with file:line locations.
//
// IMPORTANT: This performs syntactic validation only. Full type checking
// requires the compiler or LSP.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - plan: The change plan to validate.
//
// # Outputs
//
//   - *ValidationResult: Validation results including all errors.
//   - error: Non-nil if validation itself fails.
//
// # Example
//
//	result, err := coordinator.ValidatePlan(ctx, plan)
//	if !result.Valid {
//	    for _, e := range result.SyntaxErrors {
//	        fmt.Printf("%s:%d: %s\n", e.FilePath, e.Line, e.Message)
//	    }
//	}
func (c *MultiFileChangeCoordinator) ValidatePlan(
	ctx context.Context,
	plan *ChangePlan,
) (*ValidationResult, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	planID := ""
	if plan != nil {
		planID = plan.ID
	}
	start := time.Now()
	ctx, span := startValidatePlanSpan(ctx, planID)
	defer span.End()

	if plan == nil {
		return nil, fmt.Errorf("%w: plan is nil", ErrInvalidInput)
	}

	result := &ValidationResult{
		Valid:        true,
		SyntaxErrors: make([]ValidationError, 0),
		TypeErrors:   make([]ValidationError, 0),
		ImportErrors: make([]ValidationError, 0),
		Warnings:     make([]string, 0),
	}

	// Validate each file change in parallel
	type validationResult struct {
		filePath   string
		validation *reason.ChangeValidation
		err        error
	}

	results := make(chan validationResult, len(plan.FileChanges))
	var wg sync.WaitGroup

	for _, fc := range plan.FileChanges {
		wg.Add(1)
		go func(fileChange FileChange) {
			defer wg.Done()

			validation, err := c.validator.ValidateChange(ctx, fileChange.FilePath, fileChange.ProposedCode)
			results <- validationResult{
				filePath:   fileChange.FilePath,
				validation: validation,
				err:        err,
			}
		}(fc)
	}

	// Close results channel when all validations complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for vr := range results {
		if vr.err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Could not validate %s: %s", vr.filePath, vr.err.Error()))
			continue
		}

		if vr.validation == nil {
			continue
		}

		// Collect errors
		for _, e := range vr.validation.Errors {
			ve := ValidationError{
				FilePath: vr.filePath,
				Line:     e.Line,
				Message:  e.Message,
				Severity: e.Severity,
			}

			switch e.Type {
			case "syntax", "missing":
				result.SyntaxErrors = append(result.SyntaxErrors, ve)
				result.Valid = false
			case "type_ref":
				result.TypeErrors = append(result.TypeErrors, ve)
				result.Valid = false
			case "import":
				result.ImportErrors = append(result.ImportErrors, ve)
			}
		}

		// Collect warnings
		for _, w := range vr.validation.Warnings {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s:%d: %s", vr.filePath, w.Line, w.Message))
		}
	}

	dur := time.Since(start)
	totalErrors := len(result.SyntaxErrors) + len(result.TypeErrors) + len(result.ImportErrors)
	setValidatePlanSpanResult(span, result.Valid, totalErrors, nil)
	recordValidatePlanMetrics(ctx, dur, nil)
	var validationErr error
	if !result.Valid {
		validationErr = fmt.Errorf("validation failed with %d errors", totalErrors)
	}
	c.crs.RecordToolStep(ctx, "validate_plan", totalErrors, dur, validationErr)

	return result, nil
}

// PreviewChanges generates unified diffs for review.
//
// # Description
//
// Generates a unified diff for each file in the change plan, suitable
// for showing the user before applying changes.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - plan: The change plan to preview.
//
// # Outputs
//
//   - []FileDiff: Unified diffs for all affected files.
//   - error: Non-nil on failure.
//
// # Example
//
//	diffs, err := coordinator.PreviewChanges(ctx, plan)
//	for _, diff := range diffs {
//	    fmt.Printf("=== %s ===\n", diff.FilePath)
//	    for _, hunk := range diff.Hunks {
//	        for _, line := range hunk.OldLines {
//	            fmt.Println("-" + line)
//	        }
//	        for _, line := range hunk.NewLines {
//	            fmt.Println("+" + line)
//	        }
//	    }
//	}
func (c *MultiFileChangeCoordinator) PreviewChanges(
	ctx context.Context,
	plan *ChangePlan,
) ([]FileDiff, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: plan is nil", ErrInvalidInput)
	}

	diffs := make([]FileDiff, 0, len(plan.FileChanges))

	for _, fc := range plan.FileChanges {
		if err := ctx.Err(); err != nil {
			return diffs, ErrContextCanceled
		}

		diff := c.generateFileDiff(fc)
		diffs = append(diffs, diff)
	}

	// Sort by order in plan
	orderMap := make(map[string]int)
	for i, path := range plan.Order {
		orderMap[path] = i
	}
	sort.Slice(diffs, func(i, j int) bool {
		return orderMap[diffs[i].FilePath] < orderMap[diffs[j].FilePath]
	})

	return diffs, nil
}

// GetPlan retrieves a previously created plan by ID.
//
// # Inputs
//
//   - planID: The plan ID returned from PlanChanges.
//
// # Outputs
//
//   - *ChangePlan: The plan, or nil if not found.
//   - bool: True if the plan was found.
func (c *MultiFileChangeCoordinator) GetPlan(planID string) (*ChangePlan, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	plan, found := c.plans[planID]
	return plan, found
}

// generatePrimaryChange generates the change for the primary target file.
func (c *MultiFileChangeCoordinator) generatePrimaryChange(
	symbol *ast.Symbol,
	request ChangeRequest,
) FileChange {
	change := FileChange{
		FilePath:   symbol.FilePath,
		SymbolID:   symbol.ID,
		ChangeType: FileChangePrimary,
		StartLine:  symbol.StartLine,
		EndLine:    symbol.EndLine,
		Reason:     "Primary change target",
	}

	// Set current code from symbol
	change.CurrentCode = symbol.Signature

	// Generate proposed code based on change type
	switch request.ChangeType {
	case ChangeAddParameter, ChangeRemoveParameter, ChangeAddReturn, ChangeRemoveReturn:
		change.ProposedCode = request.NewSignature
	case ChangeRenameSymbol:
		change.ProposedCode = c.generateRenamedCode(symbol, request.NewName)
	case ChangeChangeType:
		change.ProposedCode = request.NewSignature
	default:
		change.ProposedCode = request.NewSignature
	}

	return change
}

// generateCallerChanges generates changes for all affected callers.
func (c *MultiFileChangeCoordinator) generateCallerChanges(
	ctx context.Context,
	blastResult *analysis.BlastRadius,
	request ChangeRequest,
	opts PlanOptions,
) []FileChange {
	changes := make([]FileChange, 0)

	// Combine direct and indirect callers (up to limit)
	allCallers := make([]analysis.Caller, 0)
	allCallers = append(allCallers, blastResult.DirectCallers...)

	if len(allCallers) < opts.MaxCallers {
		remaining := opts.MaxCallers - len(allCallers)
		for i, ic := range blastResult.IndirectCallers {
			if i >= remaining {
				break
			}
			// Only include indirect callers up to MaxHops
			if ic.Hops <= opts.MaxHops {
				allCallers = append(allCallers, ic)
			}
		}
	}

	for _, caller := range allCallers {
		if err := ctx.Err(); err != nil {
			break
		}

		// Skip test files if not included
		if !opts.IncludeTests && isTestFile(caller.FilePath) {
			continue
		}

		change := c.generateCallerUpdateChange(caller, request)
		changes = append(changes, change)
	}

	return changes
}

// generateCallerUpdateChange generates a change for a single caller.
func (c *MultiFileChangeCoordinator) generateCallerUpdateChange(
	caller analysis.Caller,
	request ChangeRequest,
) FileChange {
	change := FileChange{
		FilePath:   caller.FilePath,
		SymbolID:   caller.ID,
		ChangeType: FileChangeCallerUpdate,
		StartLine:  caller.Line,
		EndLine:    caller.Line,
		Reason:     fmt.Sprintf("Caller of %s needs update", request.TargetID),
	}

	// Generate proposed code based on change type
	switch request.ChangeType {
	case ChangeAddParameter:
		// Caller needs to pass additional argument
		change.CurrentCode = fmt.Sprintf("/* call to %s */", extractName(request.TargetID))
		change.ProposedCode = fmt.Sprintf("/* call to %s with new parameter */", extractName(request.TargetID))

	case ChangeRemoveParameter:
		// Caller needs to remove argument
		change.CurrentCode = fmt.Sprintf("/* call to %s */", extractName(request.TargetID))
		change.ProposedCode = fmt.Sprintf("/* call to %s without removed parameter */", extractName(request.TargetID))

	case ChangeRenameSymbol:
		// Caller needs to use new name
		change.CurrentCode = extractName(request.TargetID)
		change.ProposedCode = request.NewName

	default:
		change.CurrentCode = "/* requires update */"
		change.ProposedCode = "/* requires manual update */"
	}

	return change
}

// generateImplementerChanges generates changes for interface implementers.
func (c *MultiFileChangeCoordinator) generateImplementerChanges(
	ctx context.Context,
	implementers []analysis.Implementer,
	request ChangeRequest,
) []FileChange {
	changes := make([]FileChange, 0)

	for _, impl := range implementers {
		if err := ctx.Err(); err != nil {
			break
		}

		change := FileChange{
			FilePath:   impl.FilePath,
			SymbolID:   impl.TypeID,
			ChangeType: FileChangeImplementerUpdate,
			StartLine:  impl.Line,
			EndLine:    impl.Line,
			Reason:     fmt.Sprintf("Implements %s", request.TargetID),
		}

		switch request.ChangeType {
		case ChangeAddMethod:
			// Implementer needs to add new method
			change.CurrentCode = fmt.Sprintf("type %s struct { ... }", impl.TypeName)
			change.ProposedCode = fmt.Sprintf("type %s struct { ... } /* needs new method */", impl.TypeName)

		case ChangeRenameSymbol:
			change.CurrentCode = impl.TypeName
			change.ProposedCode = request.NewName

		default:
			change.CurrentCode = "/* requires update */"
			change.ProposedCode = request.NewSignature
		}

		changes = append(changes, change)
	}

	return changes
}

// generateRenamedCode generates code with a renamed symbol.
func (c *MultiFileChangeCoordinator) generateRenamedCode(symbol *ast.Symbol, newName string) string {
	// Replace the symbol name in the signature
	return strings.Replace(symbol.Signature, symbol.Name, newName, 1)
}

// buildChangeOrder determines the order in which changes should be applied.
func (c *MultiFileChangeCoordinator) buildChangeOrder(changes []FileChange) []string {
	order := make([]string, 0)
	seen := make(map[string]bool)

	// Primary changes first
	for _, change := range changes {
		if change.ChangeType == FileChangePrimary && !seen[change.FilePath] {
			order = append(order, change.FilePath)
			seen[change.FilePath] = true
		}
	}

	// Then caller updates
	for _, change := range changes {
		if change.ChangeType == FileChangeCallerUpdate && !seen[change.FilePath] {
			order = append(order, change.FilePath)
			seen[change.FilePath] = true
		}
	}

	// Then import updates
	for _, change := range changes {
		if change.ChangeType == FileChangeImportUpdate && !seen[change.FilePath] {
			order = append(order, change.FilePath)
			seen[change.FilePath] = true
		}
	}

	// Then implementer updates
	for _, change := range changes {
		if change.ChangeType == FileChangeImplementerUpdate && !seen[change.FilePath] {
			order = append(order, change.FilePath)
			seen[change.FilePath] = true
		}
	}

	// Any remaining
	for _, change := range changes {
		if !seen[change.FilePath] {
			order = append(order, change.FilePath)
			seen[change.FilePath] = true
		}
	}

	return order
}

// calculateRiskLevel determines the overall risk of the change plan.
func (c *MultiFileChangeCoordinator) calculateRiskLevel(
	plan *ChangePlan,
	blastResult *analysis.BlastRadius,
) RiskLevel {
	if blastResult != nil {
		// Use blast radius risk level as base
		switch blastResult.RiskLevel {
		case analysis.RiskCritical:
			return RiskCritical
		case analysis.RiskHigh:
			return RiskHigh
		case analysis.RiskMedium:
			return RiskMedium
		case analysis.RiskLow:
			return RiskLow
		}
	}

	// Fall back to heuristics based on file count
	switch {
	case plan.TotalFiles >= 10:
		return RiskCritical
	case plan.TotalFiles >= 5:
		return RiskHigh
	case plan.TotalFiles >= 3:
		return RiskMedium
	default:
		return RiskLow
	}
}

// calculateConfidence computes confidence in the plan.
func (c *MultiFileChangeCoordinator) calculateConfidence(
	plan *ChangePlan,
	blastResult *analysis.BlastRadius,
) float64 {
	confidence := 0.8 // Base confidence

	// Reduce for limitations
	confidence -= float64(len(plan.Limitations)) * 0.05

	// Reduce for warnings
	confidence -= float64(len(plan.Warnings)) * 0.02

	// Reduce for large change sets
	if plan.TotalFiles > 10 {
		confidence -= 0.1
	}

	// Reduce if blast radius was truncated
	if blastResult != nil && blastResult.Truncated {
		confidence -= 0.15
	}

	// Clamp to [0.3, 1.0]
	if confidence < 0.3 {
		confidence = 0.3
	}
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// generateFileDiff generates a unified diff for a file change.
func (c *MultiFileChangeCoordinator) generateFileDiff(fc FileChange) FileDiff {
	diff := FileDiff{
		FilePath:   fc.FilePath,
		Hunks:      make([]Hunk, 0),
		ChangeType: fc.ChangeType,
		Reason:     fc.Reason,
	}

	// Parse current and proposed code into lines
	oldLines := strings.Split(fc.CurrentCode, "\n")
	newLines := strings.Split(fc.ProposedCode, "\n")

	// Create a single hunk for the change
	hunk := Hunk{
		StartLine: fc.StartLine,
		OldLines:  oldLines,
		NewLines:  newLines,
	}

	diff.Hunks = append(diff.Hunks, hunk)
	diff.LinesRemoved = len(oldLines)
	diff.LinesAdded = len(newLines)

	return diff
}

// Helper functions

func countUniqueFiles(changes []FileChange) int {
	seen := make(map[string]bool)
	for _, c := range changes {
		seen[c.FilePath] = true
	}
	return len(seen)
}

func extractName(symbolID string) string {
	// Symbol ID format: "path/file.go:line:Name"
	parts := strings.Split(symbolID, ":")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	return symbolID
}

func isTestFile(filePath string) bool {
	return strings.HasSuffix(filePath, "_test.go") ||
		strings.HasSuffix(filePath, "_test.py") ||
		strings.HasSuffix(filePath, ".test.ts") ||
		strings.HasSuffix(filePath, ".test.js") ||
		strings.HasSuffix(filePath, ".spec.ts") ||
		strings.HasSuffix(filePath, ".spec.js")
}
