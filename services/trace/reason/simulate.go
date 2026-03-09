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

// ChangeSimulator simulates the impact of proposed code changes.
//
// Description:
//
//	ChangeSimulator previews what would happen if a change were made,
//	identifying callers that need updates, imports needed, type mismatches,
//	and affected tests.
//
// Thread Safety:
//
//	ChangeSimulator is safe for concurrent use.
type ChangeSimulator struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	parser *SignatureParser
	crs    CRSRecorder
}

// NewChangeSimulator creates a new ChangeSimulator.
//
// Description:
//
//	Creates a simulator that can preview the impact of proposed changes
//	using the code graph and symbol index.
//
// Inputs:
//
//	g - The code graph. Must be frozen.
//	idx - The symbol index.
//
// Outputs:
//
//	*ChangeSimulator - The configured simulator.
func NewChangeSimulator(g *graph.Graph, idx *index.SymbolIndex) *ChangeSimulator {
	return &ChangeSimulator{
		graph:  g,
		index:  idx,
		parser: NewSignatureParser(),
		crs:    &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this simulator.
func (s *ChangeSimulator) SetCRS(recorder CRSRecorder) {
	s.crs = recorder
}

// ChangeSimulation is the result of simulating a change.
type ChangeSimulation struct {
	// TargetID is the symbol being changed.
	TargetID string `json:"target_id"`

	// Valid indicates if the change appears valid.
	Valid bool `json:"valid"`

	// CallersToUpdate lists callers that need to be updated.
	CallersToUpdate []CallerUpdate `json:"callers_to_update"`

	// ImportsNeeded lists imports that would need to be added.
	ImportsNeeded []string `json:"imports_needed"`

	// TypeMismatches lists type incompatibilities introduced.
	TypeMismatches []TypeMismatch `json:"type_mismatches"`

	// TestsAffected lists test symbols that would be affected.
	TestsAffected []string `json:"tests_affected"`

	// Confidence is how confident we are in the simulation (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// Limitations lists what we couldn't simulate.
	Limitations []string `json:"limitations"`
}

// CallerUpdate describes a caller that needs to be updated.
type CallerUpdate struct {
	// CallerID is the symbol ID of the caller.
	CallerID string `json:"caller_id"`

	// CallerName is the name of the calling function.
	CallerName string `json:"caller_name"`

	// CurrentCall is the current call expression (if extractable).
	CurrentCall string `json:"current_call,omitempty"`

	// NeededCall is what the call should be changed to.
	NeededCall string `json:"needed_call,omitempty"`

	// FilePath is the file containing the caller.
	FilePath string `json:"file_path"`

	// Line is the line number of the call.
	Line int `json:"line"`

	// UpdateType describes the type of update needed.
	UpdateType string `json:"update_type"`
}

// TypeMismatch describes a type incompatibility.
type TypeMismatch struct {
	// Location describes where the mismatch occurs.
	Location string `json:"location"`

	// Expected is the type that was expected.
	Expected string `json:"expected"`

	// Got is the type that was found.
	Got string `json:"got"`

	// Suggestion is how to fix the mismatch.
	Suggestion string `json:"suggestion,omitempty"`
}

// SimulateChange simulates what would happen if a symbol were changed.
//
// Description:
//
//	Simulates the impact of changing a symbol's signature or implementation.
//	Identifies all callers that would need updates, any type mismatches
//	that would be introduced, and tests that would be affected.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetID - The symbol ID to change.
//	newSignature - The proposed new signature.
//
// Outputs:
//
//	*ChangeSimulation - Simulation results.
//	error - Non-nil if the simulation fails.
//
// Example:
//
//	sim, err := simulator.SimulateChange(ctx,
//	    "pkg/handlers.Handle",
//	    "func(ctx context.Context, req *Request, opts Options) error",
//	)
//	if len(sim.CallersToUpdate) > 0 {
//	    fmt.Printf("%d callers need updates\n", len(sim.CallersToUpdate))
//	}
//
// Limitations:
//
//   - Cannot simulate runtime behavior changes
//   - Type checking is structural, not semantic
//   - May not detect all indirect impacts
func (s *ChangeSimulator) SimulateChange(
	ctx context.Context,
	targetID string,
	newSignature string,
) (*ChangeSimulation, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startSimulateSpan(ctx, targetID)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if targetID == "" || newSignature == "" {
		return nil, ErrInvalidInput
	}
	if s.graph != nil && !s.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	// Find the target symbol
	symbol, found := s.index.GetByID(targetID)
	if !found || symbol == nil {
		return nil, ErrSymbolNotFound
	}

	result := &ChangeSimulation{
		TargetID:        targetID,
		Valid:           true,
		CallersToUpdate: make([]CallerUpdate, 0),
		ImportsNeeded:   make([]string, 0),
		TypeMismatches:  make([]TypeMismatch, 0),
		TestsAffected:   make([]string, 0),
		Limitations:     make([]string, 0),
	}

	// Parse signatures
	currentSig, err := s.parser.ParseSignature(symbol.Signature, symbol.Language)
	if err != nil {
		result.Limitations = append(result.Limitations,
			"Could not parse current signature")
	}

	newSig, err := s.parser.ParseSignature(newSignature, symbol.Language)
	if err != nil {
		result.Limitations = append(result.Limitations,
			"Could not parse new signature")
		result.Valid = false
		return result, nil
	}

	// Find callers that need updates
	if s.graph != nil {
		callerUpdates := s.findCallersToUpdate(symbol, currentSig, newSig)
		result.CallersToUpdate = callerUpdates
	} else {
		result.Limitations = append(result.Limitations,
			"Graph not available - cannot identify affected callers")
	}

	// Detect type mismatches
	if currentSig != nil && newSig != nil {
		mismatches := s.detectTypeMismatches(currentSig, newSig)
		result.TypeMismatches = mismatches
	}

	// Find imports needed for new types
	importsNeeded := s.findImportsNeeded(newSig, symbol)
	result.ImportsNeeded = importsNeeded

	// Find affected tests
	if s.graph != nil && s.index != nil {
		testsAffected := s.findAffectedTests(targetID)
		result.TestsAffected = testsAffected
	}

	// Calculate confidence
	result.Confidence = s.calculateSimulationConfidence(result)

	dur := time.Since(start)
	setSimulateSpanResult(span, len(result.CallersToUpdate), nil)
	recordSimulateMetrics(ctx, dur, nil)
	s.crs.RecordToolStep(ctx, "simulate_change", len(result.CallersToUpdate), dur, nil)

	return result, nil
}

// findCallersToUpdate identifies callers that need updates.
func (s *ChangeSimulator) findCallersToUpdate(
	symbol *ast.Symbol,
	currentSig, newSig *ParsedSignature,
) []CallerUpdate {
	updates := make([]CallerUpdate, 0)

	node, found := s.graph.GetNode(symbol.ID)
	if !found || node == nil {
		return updates
	}

	// Determine what kind of update is needed
	updateType := s.determineUpdateType(currentSig, newSig)
	if updateType == "" {
		return updates // No update needed
	}

	for _, edge := range node.Incoming {
		if edge.Type != graph.EdgeTypeCalls {
			continue
		}

		callerNode, callerFound := s.graph.GetNode(edge.FromID)
		if !callerFound || callerNode == nil || callerNode.Symbol == nil {
			continue
		}

		update := CallerUpdate{
			CallerID:   edge.FromID,
			CallerName: callerNode.Symbol.Name,
			FilePath:   callerNode.Symbol.FilePath,
			Line:       edge.Location.StartLine,
			UpdateType: updateType,
		}

		// Generate suggested call update
		if newSig != nil {
			update.NeededCall = s.generateCallSuggestion(symbol.Name, newSig)
		}

		updates = append(updates, update)
	}

	return updates
}

// determineUpdateType determines what type of update callers need.
func (s *ChangeSimulator) determineUpdateType(current, proposed *ParsedSignature) string {
	if current == nil || proposed == nil {
		return "signature_change"
	}

	// Check for added parameters
	if len(proposed.Parameters) > len(current.Parameters) {
		// Check if new params have defaults
		allOptional := true
		for i := len(current.Parameters); i < len(proposed.Parameters); i++ {
			if !proposed.Parameters[i].Optional {
				allOptional = false
				break
			}
		}
		if !allOptional {
			return "add_arguments"
		}
	}

	// Check for removed parameters
	if len(proposed.Parameters) < len(current.Parameters) {
		return "remove_arguments"
	}

	// Check for type changes
	minLen := len(current.Parameters)
	if len(proposed.Parameters) < minLen {
		minLen = len(proposed.Parameters)
	}
	for i := 0; i < minLen; i++ {
		if !typesEqual(current.Parameters[i].Type, proposed.Parameters[i].Type) {
			return "change_argument_types"
		}
	}

	// Check for return type changes
	if len(current.Returns) != len(proposed.Returns) {
		return "change_return_handling"
	}
	for i := range current.Returns {
		if !typesEqual(current.Returns[i], proposed.Returns[i]) {
			return "change_return_handling"
		}
	}

	return ""
}

// generateCallSuggestion generates a suggested call expression.
func (s *ChangeSimulator) generateCallSuggestion(funcName string, sig *ParsedSignature) string {
	var sb strings.Builder
	sb.WriteString(funcName)
	sb.WriteString("(")

	for i, param := range sig.Parameters {
		if i > 0 {
			sb.WriteString(", ")
		}
		if param.Name != "" {
			sb.WriteString(param.Name)
		} else {
			sb.WriteString("arg")
			sb.WriteString(string(rune('0' + i)))
		}
	}

	sb.WriteString(")")
	return sb.String()
}

// detectTypeMismatches finds type incompatibilities between signatures.
func (s *ChangeSimulator) detectTypeMismatches(current, proposed *ParsedSignature) []TypeMismatch {
	mismatches := make([]TypeMismatch, 0)

	// Check parameter type changes
	minLen := len(current.Parameters)
	if len(proposed.Parameters) < minLen {
		minLen = len(proposed.Parameters)
	}

	for i := 0; i < minLen; i++ {
		curr := current.Parameters[i]
		prop := proposed.Parameters[i]

		if !typesEqual(curr.Type, prop.Type) {
			mismatch := TypeMismatch{
				Location: "parameter " + curr.Name,
				Expected: curr.Type.Name,
				Got:      prop.Type.Name,
			}

			// Suggest conversion if possible
			convs := suggestConversions(prop.Type.Name, curr.Type.Name)
			if len(convs) > 0 {
				mismatch.Suggestion = "Convert using: " + convs[0]
			}

			mismatches = append(mismatches, mismatch)
		}
	}

	// Check return type changes
	minRetLen := len(current.Returns)
	if len(proposed.Returns) < minRetLen {
		minRetLen = len(proposed.Returns)
	}

	for i := 0; i < minRetLen; i++ {
		if !typesEqual(current.Returns[i], proposed.Returns[i]) {
			mismatches = append(mismatches, TypeMismatch{
				Location: "return value " + string(rune('0'+i)),
				Expected: current.Returns[i].Name,
				Got:      proposed.Returns[i].Name,
			})
		}
	}

	return mismatches
}

// findImportsNeeded identifies imports needed for new types.
func (s *ChangeSimulator) findImportsNeeded(sig *ParsedSignature, symbol *ast.Symbol) []string {
	imports := make([]string, 0)
	if sig == nil {
		return imports
	}

	seen := make(map[string]bool)

	// Check parameters for package-qualified types
	for _, param := range sig.Parameters {
		if pkg := extractPackageFromType(param.Type.Name); pkg != "" {
			if !seen[pkg] {
				seen[pkg] = true
				imports = append(imports, pkg)
			}
		}
	}

	// Check returns
	for _, ret := range sig.Returns {
		if pkg := extractPackageFromType(ret.Name); pkg != "" {
			if !seen[pkg] {
				seen[pkg] = true
				imports = append(imports, pkg)
			}
		}
	}

	return imports
}

// findAffectedTests finds tests that would be affected by the change.
func (s *ChangeSimulator) findAffectedTests(targetID string) []string {
	tests := make([]string, 0)

	// Get all test functions
	allFuncs := s.index.GetByKind(ast.SymbolKindFunction)

	for _, fn := range allFuncs {
		if !isTestFunction(fn) {
			continue
		}

		// Check if test calls target (directly or indirectly)
		if s.testCallsTarget(fn.ID, targetID, make(map[string]bool)) {
			tests = append(tests, fn.ID)
		}
	}

	return tests
}

// testCallsTarget checks if a test calls the target (with cycle detection).
func (s *ChangeSimulator) testCallsTarget(testID, targetID string, visited map[string]bool) bool {
	if visited[testID] {
		return false
	}
	visited[testID] = true

	node, found := s.graph.GetNode(testID)
	if !found || node == nil {
		return false
	}

	for _, edge := range node.Outgoing {
		if edge.Type != graph.EdgeTypeCalls {
			continue
		}

		if edge.ToID == targetID {
			return true
		}

		// Check transitive calls (limit depth to avoid performance issues)
		if len(visited) < 10 {
			if s.testCallsTarget(edge.ToID, targetID, visited) {
				return true
			}
		}
	}

	return false
}

// calculateSimulationConfidence calculates confidence for the simulation.
func (s *ChangeSimulator) calculateSimulationConfidence(sim *ChangeSimulation) float64 {
	cal := NewConfidenceCalibration(0.85)

	// Reduce confidence for each limitation
	for range sim.Limitations {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "simulation limitation",
			Multiplier: 0.9,
		})
	}

	// Increase confidence if we found affected callers
	if len(sim.CallersToUpdate) > 0 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "found affected callers",
			Multiplier: 1.05,
		})
	}

	// Decrease confidence if many type mismatches
	if len(sim.TypeMismatches) > 3 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "many type mismatches",
			Multiplier: 0.9,
		})
	}

	cal.Apply(AdjustmentStaticAnalysisOnly)

	return cal.FinalScore
}

// SimulateMultipleChanges simulates multiple related changes.
//
// Description:
//
//	Simulates a batch of related changes, useful when making
//	coordinated updates across multiple symbols.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	changes - Map of targetID to new signature.
//
// Outputs:
//
//	map[string]*ChangeSimulation - Simulation results keyed by targetID.
//	error - Non-nil if the simulation fails.
func (s *ChangeSimulator) SimulateMultipleChanges(
	ctx context.Context,
	changes map[string]string,
) (map[string]*ChangeSimulation, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	results := make(map[string]*ChangeSimulation)

	for targetID, newSig := range changes {
		if err := ctx.Err(); err != nil {
			return results, ErrContextCanceled
		}

		sim, err := s.SimulateChange(ctx, targetID, newSig)
		if err != nil {
			results[targetID] = &ChangeSimulation{
				TargetID:    targetID,
				Valid:       false,
				Limitations: []string{"Simulation failed: " + err.Error()},
			}
			continue
		}
		results[targetID] = sim
	}

	return results, nil
}

// Helper functions

func extractPackageFromType(typeName string) string {
	// Extract package from qualified type like "context.Context" or "http.Request"
	typeName = strings.TrimPrefix(typeName, "*")
	typeName = strings.TrimPrefix(typeName, "[]")

	if idx := strings.LastIndex(typeName, "."); idx > 0 {
		return typeName[:idx]
	}
	return ""
}

func isTestFunction(fn *ast.Symbol) bool {
	if fn == nil {
		return false
	}

	name := fn.Name

	// Go test functions
	if strings.HasPrefix(name, "Test") && fn.Language == "go" {
		return true
	}

	// Python test functions
	if strings.HasPrefix(name, "test_") && fn.Language == "python" {
		return true
	}

	// Check file path for test files
	return isTestFile(fn.FilePath)
}
