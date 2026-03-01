// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConceptSynonyms_Load(t *testing.T) {
	t.Run("YAML parses successfully", func(t *testing.T) {
		var raw map[string][]string
		if err := yaml.Unmarshal(defaultConceptSynonymsYAML, &raw); err != nil {
			t.Fatalf("failed to parse concept_synonyms.yaml: %v", err)
		}
		if len(raw) == 0 {
			t.Fatal("concept_synonyms.yaml is empty")
		}
		t.Logf("loaded %d concept entries", len(raw))
	})

	t.Run("key concepts have synonyms", func(t *testing.T) {
		var raw map[string][]string
		if err := yaml.Unmarshal(defaultConceptSynonymsYAML, &raw); err != nil {
			t.Fatalf("parse failed: %v", err)
		}

		required := []struct {
			concept  string
			mustHave []string
		}{
			{"initialization", []string{"init", "new", "build"}},
			{"creation", []string{"create", "new", "make"}},
			{"deletion", []string{"delete", "remove"}},
			{"validation", []string{"validate", "check"}},
			{"authentication", []string{"auth", "login"}},
			{"configuration", []string{"config", "setup"}},
			{"parsing", []string{"parse", "decode"}},
			{"rendering", []string{"render", "draw"}},
		}

		for _, req := range required {
			synonyms, ok := raw[req.concept]
			if !ok {
				t.Errorf("missing required concept %q", req.concept)
				continue
			}
			synSet := make(map[string]bool)
			for _, s := range synonyms {
				synSet[s] = true
			}
			for _, must := range req.mustHave {
				if !synSet[must] {
					t.Errorf("concept %q missing required synonym %q", req.concept, must)
				}
			}
		}
	})

	t.Run("no empty synonym lists", func(t *testing.T) {
		var raw map[string][]string
		if err := yaml.Unmarshal(defaultConceptSynonymsYAML, &raw); err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		for concept, synonyms := range raw {
			if len(synonyms) == 0 {
				t.Errorf("concept %q has empty synonym list", concept)
			}
		}
	})

	t.Run("no duplicate synonyms within a concept", func(t *testing.T) {
		var raw map[string][]string
		if err := yaml.Unmarshal(defaultConceptSynonymsYAML, &raw); err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		for concept, synonyms := range raw {
			seen := make(map[string]bool)
			for _, syn := range synonyms {
				if seen[syn] {
					t.Errorf("concept %q has duplicate synonym %q", concept, syn)
				}
				seen[syn] = true
			}
		}
	})
}
