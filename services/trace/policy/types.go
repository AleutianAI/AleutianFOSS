// Package policy provides the logic for scanning, classifying, and enforcing
// data governance rules on content passing through the Aleutian system.
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
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// ConfidenceLevel represents the degree of certainty that a matched pattern
// indicates a specific data classification.
//
// Allowed values are "low", "medium", and "high".
type ConfidenceLevel string

const (
	Low    ConfidenceLevel = "low"
	Medium ConfidenceLevel = "medium"
	High   ConfidenceLevel = "high"
)

// PolicyEngineClassificationFile represents the root structure of the YAML policy configuration.
// It maps directly to the top-level "classifications" key in the data_classification_patterns.yaml file.
type PolicyEngineClassificationFile struct {
	ClassificationPatterns []Classification `yaml:"classifications"`
}

// Classification represents a high-level category of data sensitivity (e.g., "Secret", "PII").
//
// It contains metadata about the category and a collection of specific regex patterns
// used to identify data belonging to this category.
type Classification struct {
	Name             string           `yaml:"name"`
	Description      string           `yaml:"description"`
	Priority         int              `yaml:"priority"`
	Patterns         []Pattern        `yaml:"patterns"`
	CompiledPatterns []*regexp.Regexp `yaml:"-"`
}

// Pattern defines a specific rule for identifying sensitive data within a Classification.
type Pattern struct {
	Id              string          `yaml:"id"`
	Description     string          `yaml:"description"`
	Regex           string          `yaml:"regex"`
	Confidence      ConfidenceLevel `yaml:"confidence"`
	compiledPattern *regexp.Regexp  `yaml:"-"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface for ConfidenceLevel.
//
// It validates that the confidence level string provided in the YAML config matches
// one of the allowed constants (low, medium, high), returning an error if invalid.
func (c *ConfidenceLevel) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	incomingConfidence := ConfidenceLevel(s)
	switch incomingConfidence {
	case High, Medium, Low:
		*c = incomingConfidence
		return nil
	default:
		return fmt.Errorf("invalid value for Confidence: %q", incomingConfidence)
	}
}

// CompileRegexes iterates through every pattern in the configuration and pre-compiles
// the regex strings into optimized *regexp.Regexp objects.
//
// This method must be called before any scanning occurs to ensure performance and
// to validate that all regex strings in the YAML are syntactically correct.
func (p *PolicyEngineClassificationFile) CompileRegexes() error {
	for i := range p.ClassificationPatterns {
		for j := range p.ClassificationPatterns[i].Patterns {
			pattern := &p.ClassificationPatterns[i].Patterns[j]
			re, err := regexp.Compile(pattern.Regex)
			if err != nil {
				return fmt.Errorf("failed to compile the regex %s: %w", pattern.Regex, err)
			}
			p.ClassificationPatterns[i].CompiledPatterns = append(p.ClassificationPatterns[i].
				CompiledPatterns, re)
			pattern.compiledPattern = re
		}
	}
	return nil
}

// SortByPriority reorders the internal ClassificationPatterns slice based on the
// Priority field, in descending order (highest priority first).
//
// This ensures that when multiple rules might match the same data, the most sensitive
// classification is applied.
func (p *PolicyEngineClassificationFile) SortByPriority() {
	sort.Slice(p.ClassificationPatterns, func(i, j int) bool {
		return p.ClassificationPatterns[i].Priority > p.ClassificationPatterns[j].Priority
	})
}

// ScanFinding represents a specific instance of a policy violation or data match found within a file.
type ScanFinding struct {
	FilePath           string          `json:"file_path"`
	LineNumber         int             `json:"line_number"`
	MatchedContent     string          `json:"matched_content"`
	ClassificationName string          `json:"classification_name"`
	PatternId          string          `json:"pattern_id"`
	PatternDescription string          `json:"pattern_description"`
	Confidence         ConfidenceLevel `json:"confidence"`
	ReviewTimestamp    int64           `json:"review_timestamp"`
	UserDecision       string          `json:"user_decision"`
	Reviewer           string          `json:"reviewer"`
}
