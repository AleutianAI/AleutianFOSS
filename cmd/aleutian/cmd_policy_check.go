// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/policy_engine"
	"github.com/spf13/cobra"
)

// =============================================================================
// CONSTANTS AND TYPES
// =============================================================================

// Exit codes for policy check.
const (
	PolicyCheckExitSuccess   = 0
	PolicyCheckExitViolation = 1
	PolicyCheckExitError     = 2
)

// Default values.
const (
	DefaultMaxFileSize = 1024 * 1024 // 1MB
	DefaultWorkers     = 0           // 0 means 2 * NumCPU
	DefaultThreshold   = "high"
	DefaultMinSeverity = "low"
)

// Severity levels for filtering.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// severityOrder maps severity to numeric order (higher = more severe).
var severityOrder = map[Severity]int{
	SeverityInfo:     0,
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

// ParseSeverity converts a string to Severity.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	case "low":
		return SeverityLow
	case "info":
		return SeverityInfo
	default:
		return SeverityLow
	}
}

// AtLeast returns true if this severity is at or above the threshold.
func (s Severity) AtLeast(threshold Severity) bool {
	return severityOrder[s] >= severityOrder[threshold]
}

// Violation represents a policy violation found during scanning.
type Violation struct {
	FilePath           string   `json:"file_path"`
	Line               int      `json:"line"`
	Severity           Severity `json:"severity"`
	RuleID             string   `json:"rule_id"`
	RuleName           string   `json:"rule_name"`
	Description        string   `json:"description"`
	MatchedContent     string   `json:"matched_content,omitempty"`
	ClassificationName string   `json:"classification_name"`
}

// CheckResult holds the results of a policy check.
type CheckResult struct {
	Violations      []Violation      `json:"violations"`
	FilesScanned    int              `json:"files_scanned"`
	FilesSkipped    int              `json:"files_skipped"`
	ViolationCounts map[Severity]int `json:"violation_counts"`
	DurationMs      int64            `json:"duration_ms"`
	Warnings        []string         `json:"warnings,omitempty"`
}

// NewCheckResult creates an initialized CheckResult.
func NewCheckResult() *CheckResult {
	return &CheckResult{
		Violations:      make([]Violation, 0),
		ViolationCounts: make(map[Severity]int),
		Warnings:        make([]string, 0),
	}
}

// =============================================================================
// COMMAND FLAGS
// =============================================================================

var (
	policyCheckRecursive   bool
	policyCheckInclude     []string
	policyCheckExclude     []string
	policyCheckMaxFileSize int64
	policyCheckSeverity    string
	policyCheckThreshold   string
	policyCheckJSON        bool
	policyCheckQuiet       bool
	policyCheckRedact      bool
	policyCheckWorkers     int
)

// =============================================================================
// COMMAND DEFINITION
// =============================================================================

var policyCheckCmd = &cobra.Command{
	Use:   "check [path]",
	Short: "Scan files for policy violations",
	Long: `Scan codebase for secrets, PII, and policy violations.

This command walks through files and checks them against embedded
policy rules for secrets, credentials, PII, and other sensitive data.

Examples:
  aleutian policy check
  aleutian policy check ./src
  aleutian policy check --exclude "vendor/**,*_test.go"
  aleutian policy check --threshold high --json

Exit Codes:
  0 = No violations at/above threshold
  1 = Violations found at/above threshold
  2 = Error (invalid path, scan failure)`,
	Args: cobra.MaximumNArgs(1),
	Run:  runPolicyCheck,
}

func init() {
	policyCheckCmd.Flags().BoolVar(&policyCheckRecursive, "recursive", true,
		"Scan subdirectories recursively")
	policyCheckCmd.Flags().StringSliceVar(&policyCheckInclude, "include", nil,
		"Only scan files matching these patterns (e.g., '*.go,*.py')")
	policyCheckCmd.Flags().StringSliceVar(&policyCheckExclude, "exclude", nil,
		"Skip files/directories matching these patterns")
	policyCheckCmd.Flags().Int64Var(&policyCheckMaxFileSize, "max-file-size", DefaultMaxFileSize,
		"Skip files larger than this size in bytes")
	policyCheckCmd.Flags().StringVar(&policyCheckSeverity, "severity", DefaultMinSeverity,
		"Minimum severity to report: critical, high, medium, low, info")
	policyCheckCmd.Flags().StringVar(&policyCheckThreshold, "threshold", DefaultThreshold,
		"Minimum severity for non-zero exit: critical, high, medium, low")
	policyCheckCmd.Flags().BoolVar(&policyCheckJSON, "json", false,
		"Output as JSON")
	policyCheckCmd.Flags().BoolVar(&policyCheckQuiet, "quiet", false,
		"Only exit code, no output")
	policyCheckCmd.Flags().BoolVar(&policyCheckRedact, "redact", false,
		"Hide matched content in output")
	policyCheckCmd.Flags().IntVar(&policyCheckWorkers, "workers", DefaultWorkers,
		"Number of parallel workers (0 = 2 * NumCPU)")

	// Add to policy command
	policyCmd.AddCommand(policyCheckCmd)
}

// =============================================================================
// COMMAND IMPLEMENTATION
// =============================================================================

func runPolicyCheck(cmd *cobra.Command, args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	result := NewCheckResult()

	// Determine path to scan
	scanPath := "."
	if len(args) > 0 {
		scanPath = args[0]
	}

	// Validate path exists
	info, err := os.Stat(scanPath)
	if err != nil {
		outputPolicyCheckError("Path not found", err)
		os.Exit(PolicyCheckExitError)
	}

	// Initialize policy engine
	engine, err := policy_engine.NewPolicyEngine()
	if err != nil {
		outputPolicyCheckError("Failed to load policy engine", err)
		os.Exit(PolicyCheckExitError)
	}

	// Determine number of workers
	workers := policyCheckWorkers
	if workers <= 0 {
		workers = 2 * runtime.NumCPU()
	}

	// Collect files to scan
	var files []string
	if info.IsDir() {
		files, err = collectFiles(scanPath, policyCheckRecursive, policyCheckInclude, policyCheckExclude)
		if err != nil {
			outputPolicyCheckError("Failed to collect files", err)
			os.Exit(PolicyCheckExitError)
		}
	} else {
		files = []string{scanPath}
	}

	// Scan files in parallel
	violations, scanned, skipped, warnings := scanFilesParallel(ctx, files, engine, workers, policyCheckMaxFileSize)

	// Filter by severity
	minSeverity := ParseSeverity(policyCheckSeverity)
	filtered := make([]Violation, 0, len(violations))
	for _, v := range violations {
		if v.Severity.AtLeast(minSeverity) {
			filtered = append(filtered, v)
		}
	}

	result.Violations = filtered
	result.FilesScanned = scanned
	result.FilesSkipped = skipped
	result.Warnings = warnings
	result.DurationMs = time.Since(start).Milliseconds()

	// Count by severity
	for _, v := range filtered {
		result.ViolationCounts[v.Severity]++
	}

	// Redact if requested
	if policyCheckRedact {
		for i := range result.Violations {
			result.Violations[i].MatchedContent = "[REDACTED]"
		}
	}

	// Output
	if !policyCheckQuiet {
		if policyCheckJSON {
			outputPolicyCheckJSON(result)
		} else {
			outputPolicyCheckText(result)
		}
	}

	// Determine exit code based on threshold
	threshold := ParseSeverity(policyCheckThreshold)
	for _, v := range result.Violations {
		if v.Severity.AtLeast(threshold) {
			os.Exit(PolicyCheckExitViolation)
		}
	}
	os.Exit(PolicyCheckExitSuccess)
}

// =============================================================================
// FILE COLLECTION
// =============================================================================

func collectFiles(root string, recursive bool, includes, excludes []string) ([]string, error) {
	var files []string

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on error
		}

		// Skip directories if not recursive
		if d.IsDir() {
			if path != root && !recursive {
				return fs.SkipDir
			}
			// Check if directory should be excluded
			if matchesPatterns(path, excludes) {
				return fs.SkipDir
			}
			return nil
		}

		// Skip excluded files
		if matchesPatterns(path, excludes) {
			return nil
		}

		// Check includes (if specified)
		if len(includes) > 0 && !matchesPatterns(path, includes) {
			return nil
		}

		// Skip binary files
		if isBinaryFile(path) {
			return nil
		}

		files = append(files, path)
		return nil
	}

	if err := filepath.WalkDir(root, walkFn); err != nil {
		return nil, err
	}

	return files, nil
}

// matchesPatterns reports whether path matches any of the given glob patterns.
//
// Supported pattern forms:
//   - "*.go"           — basename match only
//   - "vendor/**"      — any path whose segment contains "vendor/"
//   - "**/suffix"      — any path whose base matches "suffix"
//   - "**/*.go"        — any path whose base matches "*.go"
//   - "**/dir/file.go" — prefix="" + suffix "dir/file.go" checked via filepath.Match on tail
//   - "src/**"         — any path containing path segment "src/"
func matchesPatterns(path string, patterns []string) bool {
	// Normalize path separators for consistent matching.
	normPath := filepath.ToSlash(path)

	for _, pattern := range patterns {
		normPattern := filepath.ToSlash(pattern)

		if !strings.Contains(normPattern, "**") {
			// Simple pattern: try basename match, then full-path match.
			if matched, _ := filepath.Match(normPattern, filepath.Base(normPath)); matched {
				return true
			}
			if matched, _ := filepath.Match(normPattern, normPath); matched {
				return true
			}
			continue
		}

		// Pattern contains "**". Split on the first occurrence.
		idx := strings.Index(normPattern, "**")
		prefix := normPattern[:idx]   // e.g. "vendor/" or "" or "src/"
		suffix := normPattern[idx+2:] // e.g. "/*.go" or "/suffix" or ""
		suffix = strings.TrimPrefix(suffix, "/")

		// Check prefix: path must contain the prefix segment.
		if prefix != "" && !strings.Contains(normPath, prefix) {
			continue
		}

		// Check suffix: if present, the base of the path must match.
		if suffix == "" {
			// Bare "**" or "something/**" — prefix check above is sufficient.
			return true
		}

		// suffix may itself be a glob (e.g. "*.go") or a plain name.
		if matched, _ := filepath.Match(suffix, filepath.Base(normPath)); matched {
			return true
		}

		// Also try matching suffix against the tail of the path to support
		// patterns like "**/dir/file.go".
		if strings.HasSuffix(normPath, "/"+suffix) || normPath == suffix {
			return true
		}
	}
	return false
}

func isBinaryFile(path string) bool {
	// Check extension first
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".bin": true, ".obj": true, ".o": true, ".a": true,
		".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".pdf": true, ".doc": true, ".docx": true,
		".wasm": true, ".pyc": true, ".class": true,
	}
	if binaryExts[ext] {
		return true
	}

	// Read first 512 bytes to detect null bytes
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	// Check for null bytes (binary indicator)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

// =============================================================================
// PARALLEL SCANNING
// =============================================================================

func scanFilesParallel(
	ctx context.Context,
	files []string,
	engine *policy_engine.PolicyEngine,
	workers int,
	maxSize int64,
) (violations []Violation, scanned, skipped int, warnings []string) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		fileChan = make(chan string, workers*2)
	)

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case path, ok := <-fileChan:
					if !ok {
						return
					}

					fileViolations, wasSkipped, warning := scanSingleFile(path, engine, maxSize)

					mu.Lock()
					if wasSkipped {
						skipped++
					} else {
						scanned++
					}
					violations = append(violations, fileViolations...)
					if warning != "" {
						warnings = append(warnings, warning)
					}
					mu.Unlock()
				}
			}
		}()
	}

	// Send files to workers
	for _, f := range files {
		select {
		case <-ctx.Done():
			break
		case fileChan <- f:
		}
	}
	close(fileChan)

	wg.Wait()
	return
}

func scanSingleFile(path string, engine *policy_engine.PolicyEngine, maxSize int64) ([]Violation, bool, string) {
	// Check file size
	info, err := os.Stat(path)
	if err != nil {
		return nil, true, fmt.Sprintf("Cannot stat %s: %v", path, err)
	}

	if info.Size() > maxSize {
		return nil, true, ""
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, true, fmt.Sprintf("Cannot read %s: %v", path, err)
	}

	// Scan with policy engine
	findings := engine.ScanFileContent(string(content))

	// Convert findings to violations
	var violations []Violation
	for _, f := range findings {
		v := Violation{
			FilePath:           path,
			Line:               f.LineNumber,
			Severity:           confidenceToSeverity(f.ClassificationName, f.Confidence),
			RuleID:             f.PatternId,
			RuleName:           f.ClassificationName,
			Description:        f.PatternDescription,
			MatchedContent:     f.MatchedContent,
			ClassificationName: f.ClassificationName,
		}
		violations = append(violations, v)
	}

	return violations, false, ""
}

// confidenceToSeverity maps classification + confidence to a severity level.
func confidenceToSeverity(classification string, confidence policy_engine.ConfidenceLevel) Severity {
	// Higher classifications get higher severity
	classLower := strings.ToLower(classification)

	// Secret classifications are always critical/high
	if strings.Contains(classLower, "secret") ||
		strings.Contains(classLower, "credential") ||
		strings.Contains(classLower, "password") {
		if confidence == policy_engine.High {
			return SeverityCritical
		}
		return SeverityHigh
	}

	// PII classifications are high/medium
	if strings.Contains(classLower, "pii") ||
		strings.Contains(classLower, "personal") {
		if confidence == policy_engine.High {
			return SeverityHigh
		}
		return SeverityMedium
	}

	// Default based on confidence
	switch confidence {
	case policy_engine.High:
		return SeverityHigh
	case policy_engine.Medium:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// =============================================================================
// OUTPUT FUNCTIONS
// =============================================================================

func outputPolicyCheckError(msg string, err error) {
	if policyCheckJSON {
		result := map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("%s: %v", msg, err),
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.Encode(result)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s: %v\n", msg, err)
	}
}

func outputPolicyCheckJSON(result *CheckResult) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to encode JSON: %v\n", err)
		os.Exit(PolicyCheckExitError)
	}
}

func outputPolicyCheckText(result *CheckResult) {
	// Header
	fmt.Println("Policy Check Results")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// Summary
	fmt.Printf("Files scanned: %d\n", result.FilesScanned)
	fmt.Printf("Files skipped: %d\n", result.FilesSkipped)
	fmt.Printf("Violations found: %d\n", len(result.Violations))
	fmt.Println()

	if len(result.Violations) == 0 {
		fmt.Println("No violations found.")
		return
	}

	// Violations by severity
	fmt.Println("Violations:")
	fmt.Println()

	// Group by severity
	bySeverity := map[Severity][]Violation{
		SeverityCritical: {},
		SeverityHigh:     {},
		SeverityMedium:   {},
		SeverityLow:      {},
		SeverityInfo:     {},
	}
	for _, v := range result.Violations {
		bySeverity[v.Severity] = append(bySeverity[v.Severity], v)
	}

	// Print in severity order
	for _, sev := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo} {
		violations := bySeverity[sev]
		if len(violations) == 0 {
			continue
		}

		for _, v := range violations {
			fmt.Printf("%-8s  %s:%d\n", strings.ToUpper(string(v.Severity)), v.FilePath, v.Line)
			fmt.Printf("          %s\n", v.Description)
			fmt.Printf("          Rule: %s\n", v.RuleID)
			if v.MatchedContent != "" && v.MatchedContent != "[REDACTED]" {
				// Truncate long matches
				match := v.MatchedContent
				if len(match) > 50 {
					match = match[:47] + "..."
				}
				fmt.Printf("          Match: %s\n", match)
			}
			fmt.Println()
		}
	}

	// Summary counts
	fmt.Println("Summary:")
	for _, sev := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo} {
		count := result.ViolationCounts[sev]
		if count > 0 {
			fmt.Printf("  %s: %d\n", strings.ToUpper(string(sev)), count)
		}
	}

	// Warnings
	if len(result.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  %s\n", w)
		}
	}

	fmt.Println()
	fmt.Printf("Scan completed in %dms\n", result.DurationMs)
}
