// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// TraceConfig holds user-provided overrides for file classification.
//
// Description:
//
//	Loaded from <projectRoot>/trace.config.yaml. All fields are optional.
//	A missing config file is not an error (zero-config works out of the box).
//
// Thread Safety: Safe for concurrent reads after construction.
type TraceConfig struct {
	// ExcludeFromAnalysis lists file path prefixes to force-classify as non-production.
	// Example: ["vendor/", "third_party/", "generated/"]
	ExcludeFromAnalysis []string `yaml:"exclude_from_analysis"`

	// IncludeOverride lists file path prefixes to force-classify as production,
	// overriding the graph topology result.
	// Example: ["integration/core/"]
	IncludeOverride []string `yaml:"include_override"`
}

// loadTraceConfig reads trace.config.yaml from the project root.
//
// Description:
//
//	Reads and parses the trace config file. If the project root is empty
//	or the file does not exist, returns an empty config with no error.
//	Only returns an error if the file exists but cannot be parsed.
//
// Inputs:
//
//	projectRoot - Absolute path to the project root. May be empty.
//
// Outputs:
//
//	TraceConfig - The parsed config, or empty config if file is missing.
//	error - Non-nil only if the file exists but has invalid YAML.
//
// Thread Safety: Safe for concurrent use (stateless function).
func loadTraceConfig(projectRoot string) (TraceConfig, error) {
	if projectRoot == "" {
		return TraceConfig{}, nil
	}

	configPath := filepath.Join(projectRoot, "trace.config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return TraceConfig{}, nil
		}
		return TraceConfig{}, fmt.Errorf("reading trace.config.yaml: %w", err)
	}

	var config TraceConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return TraceConfig{}, fmt.Errorf("parsing trace.config.yaml: %w", err)
	}

	return config, nil
}
