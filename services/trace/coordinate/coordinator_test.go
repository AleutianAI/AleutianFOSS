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
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/analysis"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/reason"
)

// createTestGraph creates a test graph for coordinate tests.
func createTestGraph(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	symbols := []*ast.Symbol{
		{
			ID:        "pkg/service.go:10:ProcessOrder",
			Name:      "ProcessOrder",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/service.go",
			StartLine: 10,
			EndLine:   20,
			Exported:  true,
			Language:  "go",
			Signature: "func (s *Service) ProcessOrder(order *Order) error",
		},
		{
			ID:        "pkg/handler.go:15:HandleOrder",
			Name:      "HandleOrder",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/handler.go",
			StartLine: 15,
			EndLine:   30,
			Exported:  true,
			Language:  "go",
			Signature: "func HandleOrder(w http.ResponseWriter, r *http.Request)",
		},
		{
			ID:        "pkg/worker.go:20:ProcessOrderJob",
			Name:      "ProcessOrderJob",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/worker.go",
			StartLine: 20,
			EndLine:   40,
			Exported:  true,
			Language:  "go",
			Signature: "func ProcessOrderJob(job *Job)",
		},
		{
			ID:        "pkg/handler_test.go:10:TestHandleOrder",
			Name:      "TestHandleOrder",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/handler_test.go",
			StartLine: 10,
			EndLine:   30,
			Exported:  true,
			Language:  "go",
			Signature: "func TestHandleOrder(t *testing.T)",
		},
		{
			ID:        "pkg/storage.go:10:Storage",
			Name:      "Storage",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "pkg/storage.go",
			StartLine: 10,
			EndLine:   20,
			Exported:  true,
			Language:  "go",
			Signature: "type Storage interface { Save(data []byte) error }",
		},
		{
			ID:        "pkg/file_storage.go:10:FileStorage",
			Name:      "FileStorage",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "pkg/file_storage.go",
			StartLine: 10,
			EndLine:   15,
			Exported:  true,
			Language:  "go",
			Signature: "type FileStorage struct { path string }",
		},
		{
			ID:        "pkg/memory_storage.go:10:MemoryStorage",
			Name:      "MemoryStorage",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "pkg/memory_storage.go",
			StartLine: 10,
			EndLine:   15,
			Exported:  true,
			Language:  "go",
			Signature: "type MemoryStorage struct { data map[string][]byte }",
		},
	}

	g := graph.NewGraph("/test/project")
	idx := index.NewSymbolIndex()

	for _, sym := range symbols {
		idx.Add(sym)
		g.AddNode(sym)
	}

	// Add call edges: HandleOrder -> ProcessOrder, ProcessOrderJob -> ProcessOrder
	g.AddEdge("pkg/handler.go:15:HandleOrder", "pkg/service.go:10:ProcessOrder",
		graph.EdgeTypeCalls, ast.Location{StartLine: 25, StartCol: 10})
	g.AddEdge("pkg/worker.go:20:ProcessOrderJob", "pkg/service.go:10:ProcessOrder",
		graph.EdgeTypeCalls, ast.Location{StartLine: 30, StartCol: 5})
	g.AddEdge("pkg/handler_test.go:10:TestHandleOrder", "pkg/handler.go:15:HandleOrder",
		graph.EdgeTypeCalls, ast.Location{StartLine: 20, StartCol: 5})

	// Add implements edges
	g.AddEdge("pkg/file_storage.go:10:FileStorage", "pkg/storage.go:10:Storage",
		graph.EdgeTypeImplements, ast.Location{StartLine: 10, StartCol: 1})
	g.AddEdge("pkg/memory_storage.go:10:MemoryStorage", "pkg/storage.go:10:Storage",
		graph.EdgeTypeImplements, ast.Location{StartLine: 10, StartCol: 1})

	g.Freeze()

	return g, idx
}

func TestNewMultiFileChangeCoordinator(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)

	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	if coordinator == nil {
		t.Fatal("expected coordinator to be non-nil")
	}
	if coordinator.graph != g {
		t.Error("expected graph to be set")
	}
	if coordinator.index != idx {
		t.Error("expected index to be set")
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_AddParameter(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID:     "pkg/service.go:10:ProcessOrder",
			ChangeType:   ChangeAddParameter,
			NewSignature: "func (s *Service) ProcessOrder(ctx context.Context, order *Order) error",
		},
		Description: "Add context parameter to ProcessOrder",
	}

	plan, err := coordinator.PlanChanges(ctx, changes, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil {
		t.Fatal("expected plan to be non-nil")
	}
	if plan.ID == "" {
		t.Error("expected plan ID to be set")
	}
	if plan.Description != changes.Description {
		t.Errorf("expected description %q, got %q", changes.Description, plan.Description)
	}

	// Should have primary change + caller changes
	if len(plan.FileChanges) < 2 {
		t.Errorf("expected at least 2 file changes, got %d", len(plan.FileChanges))
	}

	// First change should be primary
	hasPrimary := false
	for _, fc := range plan.FileChanges {
		if fc.ChangeType == FileChangePrimary {
			hasPrimary = true
			if fc.FilePath != "pkg/service.go" {
				t.Errorf("expected primary file pkg/service.go, got %s", fc.FilePath)
			}
			break
		}
	}
	if !hasPrimary {
		t.Error("expected primary change in plan")
	}

	// Should have order
	if len(plan.Order) == 0 {
		t.Error("expected order to be set")
	}

	// Primary file should be first in order
	if plan.Order[0] != "pkg/service.go" {
		t.Errorf("expected first in order to be pkg/service.go, got %s", plan.Order[0])
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_RenameSymbol(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID:   "pkg/service.go:10:ProcessOrder",
			ChangeType: ChangeRenameSymbol,
			NewName:    "HandleOrder",
		},
		Description: "Rename ProcessOrder to HandleOrder",
	}

	plan, err := coordinator.PlanChanges(ctx, changes, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil {
		t.Fatal("expected plan to be non-nil")
	}

	// Primary change should have renamed code
	for _, fc := range plan.FileChanges {
		if fc.ChangeType == FileChangePrimary {
			if fc.ProposedCode == "" {
				t.Error("expected proposed code to be set for rename")
			}
			break
		}
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_ExcludeTests(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID:     "pkg/service.go:10:ProcessOrder",
			ChangeType:   ChangeAddParameter,
			NewSignature: "func (s *Service) ProcessOrder(ctx context.Context, order *Order) error",
		},
		Description: "Add context parameter",
	}

	// Exclude tests
	opts := DefaultPlanOptions()
	opts.IncludeTests = false

	plan, err := coordinator.PlanChanges(ctx, changes, &opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have test files
	for _, fc := range plan.FileChanges {
		if isTestFile(fc.FilePath) {
			t.Errorf("expected no test files, found %s", fc.FilePath)
		}
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_NilContext(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID: "pkg/service.go:10:ProcessOrder",
		},
	}

	_, err := coordinator.PlanChanges(nil, changes, nil)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_EmptyTargetID(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()
	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID: "",
		},
	}

	_, err := coordinator.PlanChanges(ctx, changes, nil)

	if err == nil {
		t.Error("expected error for empty target ID")
	}
}

func TestMultiFileChangeCoordinator_PlanChanges_SymbolNotFound(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()
	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID: "nonexistent:symbol",
		},
	}

	_, err := coordinator.PlanChanges(ctx, changes, nil)

	if err != ErrSymbolNotFound {
		t.Errorf("expected ErrSymbolNotFound, got %v", err)
	}
}

func TestMultiFileChangeCoordinator_ValidatePlan(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	// Create a plan with valid Go code
	plan := &ChangePlan{
		ID:          "test-plan",
		Description: "Test plan",
		FileChanges: []FileChange{
			{
				FilePath:     "pkg/service.go",
				ChangeType:   FileChangePrimary,
				CurrentCode:  "func old() {}",
				ProposedCode: "package pkg\n\nfunc new() {}",
				StartLine:    10,
				EndLine:      12,
			},
		},
	}

	result, err := coordinator.ValidatePlan(ctx, plan)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result to be non-nil")
	}
}

func TestMultiFileChangeCoordinator_ValidatePlan_NilContext(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	plan := &ChangePlan{ID: "test"}

	_, err := coordinator.ValidatePlan(nil, plan)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestMultiFileChangeCoordinator_ValidatePlan_NilPlan(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	_, err := coordinator.ValidatePlan(ctx, nil)

	if err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestMultiFileChangeCoordinator_PreviewChanges(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	plan := &ChangePlan{
		ID:          "test-plan",
		Description: "Test plan",
		FileChanges: []FileChange{
			{
				FilePath:     "pkg/service.go",
				ChangeType:   FileChangePrimary,
				CurrentCode:  "func old() {}",
				ProposedCode: "func new() {}",
				StartLine:    10,
				EndLine:      12,
			},
			{
				FilePath:     "pkg/handler.go",
				ChangeType:   FileChangeCallerUpdate,
				CurrentCode:  "old()",
				ProposedCode: "new()",
				StartLine:    25,
				EndLine:      25,
			},
		},
		Order: []string{"pkg/service.go", "pkg/handler.go"},
	}

	diffs, err := coordinator.PreviewChanges(ctx, plan)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diffs) != 2 {
		t.Errorf("expected 2 diffs, got %d", len(diffs))
	}

	// Check diff structure
	for _, diff := range diffs {
		if diff.FilePath == "" {
			t.Error("expected file path to be set")
		}
		if len(diff.Hunks) == 0 {
			t.Error("expected hunks to be present")
		}
	}
}

func TestMultiFileChangeCoordinator_PreviewChanges_NilContext(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	plan := &ChangePlan{ID: "test"}

	_, err := coordinator.PreviewChanges(nil, plan)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestMultiFileChangeCoordinator_PreviewChanges_NilPlan(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	_, err := coordinator.PreviewChanges(ctx, nil)

	if err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestMultiFileChangeCoordinator_GetPlan(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	ctx := context.Background()

	// Create a plan
	changes := ChangeSet{
		PrimaryChange: ChangeRequest{
			TargetID:     "pkg/service.go:10:ProcessOrder",
			ChangeType:   ChangeAddParameter,
			NewSignature: "func (s *Service) ProcessOrder(ctx context.Context, order *Order) error",
		},
		Description: "Add context",
	}

	plan, err := coordinator.PlanChanges(ctx, changes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Retrieve the plan
	retrieved, found := coordinator.GetPlan(plan.ID)

	if !found {
		t.Error("expected plan to be found")
	}
	if retrieved.ID != plan.ID {
		t.Errorf("expected plan ID %s, got %s", plan.ID, retrieved.ID)
	}
}

func TestMultiFileChangeCoordinator_GetPlan_NotFound(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	_, found := coordinator.GetPlan("nonexistent")

	if found {
		t.Error("expected plan to not be found")
	}
}

func TestBuildChangeOrder(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	changes := []FileChange{
		{FilePath: "caller.go", ChangeType: FileChangeCallerUpdate},
		{FilePath: "primary.go", ChangeType: FileChangePrimary},
		{FilePath: "impl.go", ChangeType: FileChangeImplementerUpdate},
		{FilePath: "import.go", ChangeType: FileChangeImportUpdate},
	}

	order := coordinator.buildChangeOrder(changes)

	if len(order) != 4 {
		t.Errorf("expected 4 items in order, got %d", len(order))
	}

	// Primary should be first
	if order[0] != "primary.go" {
		t.Errorf("expected primary.go first, got %s", order[0])
	}

	// Caller should be second
	if order[1] != "caller.go" {
		t.Errorf("expected caller.go second, got %s", order[1])
	}
}

func TestCalculateRiskLevel(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	tests := []struct {
		name        string
		totalFiles  int
		blastRisk   analysis.RiskLevel
		expectedMin RiskLevel
	}{
		{
			name:        "low from blast",
			totalFiles:  1,
			blastRisk:   analysis.RiskLow,
			expectedMin: RiskLow,
		},
		{
			name:        "medium from blast",
			totalFiles:  2,
			blastRisk:   analysis.RiskMedium,
			expectedMin: RiskMedium,
		},
		{
			name:        "high from blast",
			totalFiles:  3,
			blastRisk:   analysis.RiskHigh,
			expectedMin: RiskHigh,
		},
		{
			name:        "critical from blast",
			totalFiles:  5,
			blastRisk:   analysis.RiskCritical,
			expectedMin: RiskCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &ChangePlan{TotalFiles: tt.totalFiles}
			blastResult := &analysis.BlastRadius{RiskLevel: tt.blastRisk}

			risk := coordinator.calculateRiskLevel(plan, blastResult)

			if risk != tt.expectedMin {
				t.Errorf("expected risk %s, got %s", tt.expectedMin, risk)
			}
		})
	}
}

func TestCalculateConfidence(t *testing.T) {
	g, idx := createTestGraph(t)
	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)
	coordinator := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	t.Run("base confidence", func(t *testing.T) {
		plan := &ChangePlan{TotalFiles: 2}
		blastResult := &analysis.BlastRadius{}

		confidence := coordinator.calculateConfidence(plan, blastResult)

		if confidence < 0.7 || confidence > 0.9 {
			t.Errorf("expected confidence around 0.8, got %f", confidence)
		}
	})

	t.Run("reduced for many files", func(t *testing.T) {
		plan := &ChangePlan{TotalFiles: 15}
		blastResult := &analysis.BlastRadius{}

		confidence := coordinator.calculateConfidence(plan, blastResult)

		if confidence >= 0.8 {
			t.Errorf("expected reduced confidence, got %f", confidence)
		}
	})

	t.Run("reduced for truncated blast", func(t *testing.T) {
		plan := &ChangePlan{TotalFiles: 2}
		blastResult := &analysis.BlastRadius{Truncated: true}

		confidence := coordinator.calculateConfidence(plan, blastResult)

		if confidence >= 0.7 {
			t.Errorf("expected reduced confidence for truncated, got %f", confidence)
		}
	})
}

func TestExtractName(t *testing.T) {
	tests := []struct {
		symbolID string
		expected string
	}{
		{"pkg/file.go:10:FuncName", "FuncName"},
		{"pkg/file.go:10:20:FuncName", "FuncName"},
		{"simple", "simple"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.symbolID, func(t *testing.T) {
			result := extractName(tt.symbolID)
			if result != tt.expected {
				t.Errorf("extractName(%q) = %q, expected %q", tt.symbolID, result, tt.expected)
			}
		})
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		filePath string
		expected bool
	}{
		{"pkg/service_test.go", true},
		{"pkg/service.go", false},
		{"pkg/service_test.py", true},
		{"pkg/service.py", false},
		{"pkg/service.test.ts", true},
		{"pkg/service.ts", false},
		{"pkg/service.spec.js", true},
		{"pkg/service.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			result := isTestFile(tt.filePath)
			if result != tt.expected {
				t.Errorf("isTestFile(%q) = %v, expected %v", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestCountUniqueFiles(t *testing.T) {
	changes := []FileChange{
		{FilePath: "file1.go"},
		{FilePath: "file2.go"},
		{FilePath: "file1.go"}, // Duplicate
		{FilePath: "file3.go"},
	}

	count := countUniqueFiles(changes)

	if count != 3 {
		t.Errorf("expected 3 unique files, got %d", count)
	}
}

// IT_CRS_03: Verify CRS RecordToolStep is called from PlanChanges and ValidatePlan.
func TestCoordinator_CRS_RecordToolStep(t *testing.T) {
	g, idx := createTestGraph(t)

	breaking := reason.NewBreakingChangeAnalyzer(g, idx)
	blast := analysis.NewBlastRadiusAnalyzer(g, idx, nil)
	validator := reason.NewChangeValidator(idx)

	coord := NewMultiFileChangeCoordinator(g, idx, breaking, blast, validator)

	// Use a recording CRS mock
	recorder := &mockCRSRecorder{}
	coord.SetCRS(recorder)

	ctx := context.Background()

	t.Run("PlanChanges_records_tool_step", func(t *testing.T) {
		recorder.reset()
		_, err := coord.PlanChanges(ctx, ChangeSet{
			PrimaryChange: ChangeRequest{
				TargetID:   "pkg/service.go:10:ProcessOrder",
				ChangeType: ChangeRenameSymbol,
			},
			Description: "test plan",
		}, nil)
		if err != nil {
			t.Fatalf("PlanChanges() error = %v", err)
		}

		if recorder.callCount == 0 {
			t.Error("expected RecordToolStep to be called from PlanChanges")
		}
		if recorder.lastToolName != "plan_changes" {
			t.Errorf("expected toolName=plan_changes, got %s", recorder.lastToolName)
		}
		if recorder.lastDuration <= 0 {
			t.Error("expected duration > 0")
		}
		if recorder.lastErr != nil {
			t.Errorf("expected no error, got %v", recorder.lastErr)
		}
	})

	t.Run("ValidatePlan_records_tool_step", func(t *testing.T) {
		recorder.reset()
		plan, err := coord.PlanChanges(ctx, ChangeSet{
			PrimaryChange: ChangeRequest{
				TargetID:   "pkg/service.go:10:ProcessOrder",
				ChangeType: ChangeRenameSymbol,
			},
			Description: "test validate",
		}, nil)
		if err != nil {
			t.Fatalf("PlanChanges() error = %v", err)
		}

		recorder.reset()
		_, err = coord.ValidatePlan(ctx, plan)
		if err != nil {
			t.Fatalf("ValidatePlan() error = %v", err)
		}

		if recorder.callCount == 0 {
			t.Error("expected RecordToolStep to be called from ValidatePlan")
		}
		if recorder.lastToolName != "validate_plan" {
			t.Errorf("expected toolName=validate_plan, got %s", recorder.lastToolName)
		}
		if recorder.lastDuration <= 0 {
			t.Error("expected duration > 0")
		}
	})
}

// mockCRSRecorder tracks calls to RecordToolStep for testing.
type mockCRSRecorder struct {
	callCount       int
	lastToolName    string
	lastResultCount int
	lastDuration    time.Duration
	lastErr         error
}

func (m *mockCRSRecorder) RecordToolStep(_ context.Context, toolName string, resultCount int, duration time.Duration, err error) {
	m.callCount++
	m.lastToolName = toolName
	m.lastResultCount = resultCount
	m.lastDuration = duration
	m.lastErr = err
}

func (m *mockCRSRecorder) reset() {
	m.callCount = 0
	m.lastToolName = ""
	m.lastResultCount = 0
	m.lastDuration = 0
	m.lastErr = nil
}
