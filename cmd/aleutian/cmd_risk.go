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
	"os"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/initializer"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/risk"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// =============================================================================
// COMMAND FLAGS
// =============================================================================

var (
	riskDiff        bool
	riskStaged      bool
	riskCommit      string
	riskBranch      string
	riskThreshold   string
	riskStrict      bool
	riskPermissive  bool
	riskSkipImpact  bool
	riskSkipPolicy  bool
	riskSkipComplex bool
	riskJSON        bool
	riskQuiet       bool
	riskExplain     bool
	riskBestEffort  bool
	riskTimeout     int
)

// =============================================================================
// COMMAND DEFINITION
// =============================================================================

var riskCmd = &cobra.Command{
	Use:   "risk [files...]",
	Short: "Assess overall risk of code changes",
	Long: `Aggregate impact, policy, and complexity signals into a risk score.

The risk command combines multiple analysis signals to produce an overall
risk assessment for code changes. This is useful for CI/CD gating and
code review prioritization.

Signals collected:
  - Impact: Blast radius, affected symbols, security paths
  - Policy: Secret detection, PII exposure, credential leaks
  - Complexity: Lines changed, files modified

Examples:
  aleutian risk                    # Assess uncommitted changes
  aleutian risk --staged           # Assess staged changes
  aleutian risk --commit abc123    # Assess specific commit
  aleutian risk --branch main      # Assess changes since main
  aleutian risk src/auth.go        # Assess specific files
  aleutian risk --threshold medium # Fail if risk > medium
  aleutian risk --strict           # Fail on any risk (threshold=low)
  aleutian risk --json             # JSON output for automation

Exit Codes:
  0 = Risk at or below threshold (safe to proceed)
  1 = Risk above threshold (requires review)
  2 = Error (no index, analysis failure)`,
	Run: runRiskCommand,
}

func init() {
	riskCmd.Flags().BoolVar(&riskDiff, "diff", false,
		"Assess uncommitted changes (default)")
	riskCmd.Flags().BoolVar(&riskStaged, "staged", false,
		"Assess staged changes")
	riskCmd.Flags().StringVar(&riskCommit, "commit", "",
		"Assess specific commit")
	riskCmd.Flags().StringVar(&riskBranch, "branch", "",
		"Assess changes since branch point")
	riskCmd.Flags().StringVar(&riskThreshold, "threshold", "high",
		"Exit 0 if at/below: low, medium, high, critical")
	riskCmd.Flags().BoolVar(&riskStrict, "strict", false,
		"Alias for --threshold low")
	riskCmd.Flags().BoolVar(&riskPermissive, "permissive", false,
		"Alias for --threshold critical")
	riskCmd.Flags().BoolVar(&riskSkipImpact, "skip-impact", false,
		"Skip impact analysis")
	riskCmd.Flags().BoolVar(&riskSkipPolicy, "skip-policy", false,
		"Skip policy check")
	riskCmd.Flags().BoolVar(&riskSkipComplex, "skip-complexity", false,
		"Skip complexity analysis")
	riskCmd.Flags().BoolVar(&riskJSON, "json", false,
		"Output as JSON")
	riskCmd.Flags().BoolVar(&riskQuiet, "quiet", false,
		"Only exit code, no output")
	riskCmd.Flags().BoolVar(&riskExplain, "explain", false,
		"Show detailed signal breakdown")
	riskCmd.Flags().BoolVar(&riskBestEffort, "best-effort", false,
		"Continue on signal failure")
	riskCmd.Flags().IntVar(&riskTimeout, "timeout", 60,
		"Total timeout in seconds")
}

// =============================================================================
// COMMAND IMPLEMENTATION
// =============================================================================

func runRiskCommand(cmd *cobra.Command, args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(riskTimeout)*time.Second)
	defer cancel()

	// Determine project root
	projectRoot, err := os.Getwd()
	if err != nil {
		outputRiskError("Failed to get working directory", err)
		os.Exit(risk.ExitError)
	}

	// Load index (optional - impact analysis needs it)
	var index *initializer.MemoryIndex
	if !riskSkipImpact {
		storage := initializer.NewStorage(projectRoot)
		index, err = storage.LoadIndex(ctx)
		if err != nil {
			// Warn but continue without impact
			if !riskQuiet && !riskJSON {
				fmt.Fprintf(os.Stderr, "Warning: No index found, skipping impact analysis\n")
			}
			riskSkipImpact = true
		}
	}

	// Build configuration
	cfg := buildRiskConfig(args, projectRoot)

	// Create aggregator
	aggregator := risk.NewAggregator(index, projectRoot)

	// Run assessment
	result, err := aggregator.Assess(ctx, cfg)
	if err != nil {
		outputRiskError("Risk assessment failed", err)
		os.Exit(risk.ExitError)
	}

	// Output result
	if !riskQuiet {
		if riskJSON {
			outputRiskJSON(result)
		} else {
			outputRiskText(result, cfg)
		}
	}

	// Determine exit code
	if result.RiskLevel.Exceeds(cfg.Threshold) {
		os.Exit(risk.ExitRiskFound)
	}
	os.Exit(risk.ExitSuccess)
}

// buildRiskConfig constructs configuration from flags.
func buildRiskConfig(args []string, projectRoot string) risk.Config {
	cfg := risk.DefaultConfig()
	cfg.ProjectRoot = projectRoot
	cfg.Timeout = riskTimeout
	cfg.SkipImpact = riskSkipImpact
	cfg.SkipPolicy = riskSkipPolicy
	cfg.SkipComplexity = riskSkipComplex
	cfg.Quiet = riskQuiet
	cfg.Explain = riskExplain
	cfg.BestEffort = riskBestEffort

	// Determine change mode
	if len(args) > 0 {
		cfg.Mode = risk.ChangeModeFiles
		cfg.Files = args
	} else if riskStaged {
		cfg.Mode = risk.ChangeModeStaged
	} else if riskCommit != "" {
		cfg.Mode = risk.ChangeModeCommit
		cfg.CommitHash = riskCommit
	} else if riskBranch != "" {
		cfg.Mode = risk.ChangeModeBranch
		cfg.BaseBranch = riskBranch
	} else {
		cfg.Mode = risk.ChangeModeDiff
	}

	// Determine threshold
	if riskStrict {
		cfg.Threshold = risk.RiskLow
	} else if riskPermissive {
		cfg.Threshold = risk.RiskCritical
	} else {
		cfg.Threshold = risk.ParseRiskLevel(riskThreshold)
	}

	return cfg
}

// =============================================================================
// OUTPUT FUNCTIONS
// =============================================================================

func outputRiskError(msg string, err error) {
	if riskJSON {
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

func outputRiskJSON(result *risk.Result) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to encode JSON: %v\n", err)
		os.Exit(risk.ExitError)
	}
}

func outputRiskText(result *risk.Result, cfg risk.Config) {
	// Risk level with color indicator
	levelIndicator := getRiskIndicator(result.RiskLevel)
	fmt.Printf("Risk Level: %s %s\n", result.RiskLevel, levelIndicator)
	fmt.Println()

	// Contributing factors
	if len(result.Factors) > 0 {
		fmt.Println("Contributing Factors:")
		for _, f := range result.Factors {
			icon := getFactorIcon(f.Severity)
			fmt.Printf("  %s %s: %s\n", icon, cases.Title(language.English).String(f.Signal), f.Message)
		}
		fmt.Println()
	}

	// Recommendation
	fmt.Printf("Recommendation: %s\n", result.Recommendation)
	fmt.Println()

	// Errors/warnings
	if len(result.Errors) > 0 {
		fmt.Println("Warnings:")
		for _, e := range result.Errors {
			fmt.Printf("  ! %s\n", e)
		}
		fmt.Println()
	}

	// Detailed breakdown (with --explain)
	if cfg.Explain {
		fmt.Printf("Risk Score: %.2f (algorithm v%s)\n", result.Score, result.RiskAlgorithmVersion)
		fmt.Println()
		fmt.Println("Signal Breakdown:")

		if result.Signals.Impact != nil {
			fmt.Printf("  Impact (weight %.1f):\n", cfg.Weights.Impact)
			fmt.Printf("    Score: %.2f\n", result.Signals.Impact.Score)
			fmt.Printf("    - Direct callers: %d\n", result.Signals.Impact.DirectCallers)
			fmt.Printf("    - Transitive impact: %d\n", result.Signals.Impact.TransitiveCallers)
			fmt.Printf("    - Security path: %v\n", result.Signals.Impact.IsSecurityPath)
			fmt.Printf("    - Public API: %v\n", result.Signals.Impact.IsPublicAPI)
			fmt.Println()
		} else if !cfg.SkipImpact {
			fmt.Println("  Impact: (not available)")
		}

		if result.Signals.Policy != nil {
			fmt.Printf("  Policy (weight %.1f):\n", cfg.Weights.Policy)
			fmt.Printf("    Score: %.2f\n", result.Signals.Policy.Score)
			fmt.Printf("    - Critical violations: %d\n", result.Signals.Policy.CriticalCount)
			fmt.Printf("    - High violations: %d\n", result.Signals.Policy.HighCount)
			fmt.Printf("    - Medium violations: %d\n", result.Signals.Policy.MediumCount)
			fmt.Println()
		} else if !cfg.SkipPolicy {
			fmt.Println("  Policy: (not available)")
		}

		if result.Signals.Complexity != nil {
			fmt.Printf("  Complexity (weight %.1f):\n", cfg.Weights.Complexity)
			fmt.Printf("    Score: %.2f\n", result.Signals.Complexity.Score)
			fmt.Printf("    - Lines added: %d\n", result.Signals.Complexity.LinesAdded)
			fmt.Printf("    - Lines removed: %d\n", result.Signals.Complexity.LinesRemoved)
			fmt.Printf("    - Files changed: %d\n", result.Signals.Complexity.FilesChanged)
			fmt.Println()
		} else if !cfg.SkipComplexity {
			fmt.Println("  Complexity: (not available)")
		}
	}

	fmt.Printf("Assessment completed in %dms\n", result.DurationMs)
}

func getRiskIndicator(level risk.RiskLevel) string {
	switch level {
	case risk.RiskCritical:
		return "[!!!]"
	case risk.RiskHigh:
		return "[!!]"
	case risk.RiskMedium:
		return "[!]"
	default:
		return "[ok]"
	}
}

func getFactorIcon(severity string) string {
	switch severity {
	case "critical":
		return "!!!"
	case "warning":
		return " ! "
	default:
		return " - "
	}
}
