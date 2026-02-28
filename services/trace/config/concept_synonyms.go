// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package config

import (
	_ "embed"
	"fmt"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"
)

// =============================================================================
// Embedded Concept Synonyms Configuration
// =============================================================================

//go:embed concept_synonyms.yaml
var defaultConceptSynonymsYAML []byte

// =============================================================================
// Concept Synonyms Types and Loading
// =============================================================================

// ConceptSynonyms maps natural-language concept nouns to the verb/prefix forms
// that developers use in function names. Used by IT-12 conceptual symbol
// resolution to bridge the gap between user descriptions ("initialization")
// and actual code naming conventions ("init", "new", "build").
//
// The map is loaded from concept_synonyms.yaml at startup and cached.
//
// # Thread Safety
//
// Safe for concurrent use after initialization (immutable after load).
type ConceptSynonyms map[string][]string

var (
	cachedConceptSynonyms ConceptSynonyms
	conceptSynonymsOnce   sync.Once
	conceptSynonymsErr    error
)

// LoadConceptSynonyms loads and caches the concept synonym mappings from the
// embedded YAML configuration. Returns the cached result on subsequent calls.
//
// # Description
//
//	Parses concept_synonyms.yaml which maps concept nouns to function name
//	verbs/prefixes. The YAML format is a simple map of string to string list.
//
// # Outputs
//
//   - ConceptSynonyms: The loaded mapping. Never nil on success.
//   - error: Non-nil if YAML parsing fails.
//
// # Thread Safety
//
// Safe for concurrent use (uses sync.Once internally).
func LoadConceptSynonyms() (ConceptSynonyms, error) {
	conceptSynonymsOnce.Do(func() {
		var raw map[string][]string
		if err := yaml.Unmarshal(defaultConceptSynonymsYAML, &raw); err != nil {
			conceptSynonymsErr = fmt.Errorf("parsing concept_synonyms.yaml: %w", err)
			return
		}
		cachedConceptSynonyms = raw
		slog.Info("IT-12: concept synonyms loaded",
			slog.Int("concept_count", len(raw)),
		)
	})
	return cachedConceptSynonyms, conceptSynonymsErr
}

// MustLoadConceptSynonyms loads concept synonyms or returns an empty map on error.
// Logs a warning if loading fails but does not panic — conceptual resolution
// will still work, just without synonym expansion.
//
// # Description
//
//	Convenience wrapper for code paths where synonym loading failure should
//	degrade gracefully rather than stop the system.
//
// # Outputs
//
//   - ConceptSynonyms: The loaded mapping, or an empty map on error.
//
// # Thread Safety
//
// Safe for concurrent use.
func MustLoadConceptSynonyms() ConceptSynonyms {
	synonyms, err := LoadConceptSynonyms()
	if err != nil {
		slog.Warn("IT-12: concept synonyms loading failed, continuing without expansion",
			slog.String("error", err.Error()),
		)
		return make(ConceptSynonyms)
	}
	return synonyms
}
