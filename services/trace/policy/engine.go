// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package policy

import (
	"fmt"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/policy/enforcement"
	"gopkg.in/yaml.v3"
)

// PolicyEngine serves as the main entry point for data classification operations.
// It holds the state of the loaded rules and provides methods to scan data against those rules.
type PolicyEngine struct {
	Classifiers []Classification
}

// NewPolicyEngine initializes a new instance of the PolicyEngine.
//
// Unlike previous versions, this function takes no arguments. It automatically loads
// the policy definitions embedded in the binary via the enforcement package.
//
// It performs the following operations:
// 1. Unmarshals the embedded YAML data.
// 2. Compiles all regex patterns.
// 3. Sorts classifications by priority.
//
// Returns an error if the embedded YAML is malformed or contains invalid regex.
func NewPolicyEngine() (*PolicyEngine, error) {
	// Parse the YAML into the types struct
	var classificationFile PolicyEngineClassificationFile
	if err := yaml.Unmarshal(enforcement.DataClassificationPatterns, &classificationFile); err != nil {
		return nil, fmt.Errorf("failed to unmarshal the embedded policy file: %w", err)
	}

	// Compile the regex patterns for performance and sort by priority
	if err := classificationFile.CompileRegexes(); err != nil {
		return nil, fmt.Errorf("failed to compile a regex %w", err)
	}

	// Sort the classifications from highest to lowest priority
	classificationFile.SortByPriority()

	// Return the fully initialized engine.
	engine := &PolicyEngine{Classifiers: classificationFile.ClassificationPatterns}
	return engine, nil
}

// ClassifyData performs a quick boolean check on a byte slice to determine its classification.
//
// It iterates through classifications by priority and returns the name of the *first*
// classification that matches the data. If no match is found, it returns "public".
//
// This is optimized for high-throughput categorization rather than detailed auditing.
func (e *PolicyEngine) ClassifyData(data []byte) string {
	for _, classifier := range e.Classifiers {
		for _, re := range classifier.CompiledPatterns {
			if re.Match(data) {
				return classifier.Name
			}
		}
	}
	return "public"
}

// ScanFileContent performs a comprehensive audit of a string.
//
// It splits the content into lines and checks every line against every pattern in the
// engine. It captures specific details about every match found, including line numbers
// and the specific text that triggered the match.
//
// This function is intended for the ingestion pipeline where detailed feedback is required.
func (e *PolicyEngine) ScanFileContent(content string) []ScanFinding {
	var findings []ScanFinding
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		for _, classifier := range e.Classifiers {
			for _, pattern := range classifier.Patterns {
				match := pattern.compiledPattern.FindString(line)
				if match != "" {
					finding := ScanFinding{
						LineNumber:         lineNum + 1,
						MatchedContent:     strings.TrimSpace(match),
						ClassificationName: classifier.Name,
						PatternId:          pattern.Id,
						PatternDescription: pattern.Description,
						Confidence:         pattern.Confidence,
					}
					findings = append(findings, finding)
				}
			}
		}
	}
	return findings
}
