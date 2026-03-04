// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// AGPL v3 - See LICENSE.txt and NOTICE.txt

package rag

import (
	"testing"
)

func TestTokenizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantAny  []string // at least these tokens must be present
		wantNone []string // these tokens must NOT be present
	}{
		{
			name:     "simple query filters stopwords and verbs",
			query:    "find hotspots in the materials package",
			wantAny:  []string{"hotspots", "materials", "package"},
			wantNone: []string{"find", "in", "the"},
		},
		{
			name:    "quoted strings extracted as candidates",
			query:   `find callers of "HandleAgent"`,
			wantAny: []string{"HandleAgent", "callers"},
		},
		{
			name:    "CamelCase split",
			query:   "look at FindHotspots",
			wantAny: []string{"FindHotspots", "Hotspots"},
		},
		{
			name:    "snake_case split",
			query:   "check find_hot_spots function",
			wantAny: []string{"find_hot_spots", "hot", "spots", "function"},
		},
		{
			name:    "path-like tokens kept whole and split",
			query:   "explore pkg/materials/render",
			wantAny: []string{"pkg/materials/render", "render"},
		},
		{
			name:    "compound phrases from adjacent tokens",
			query:   "find hotspots in rendering subsystem",
			wantAny: []string{"rendering", "subsystem", "rendering subsystem"},
		},
		{
			name:     "single-char tokens filtered",
			query:    "a b c materials",
			wantAny:  []string{"materials"},
			wantNone: []string{"a", "b", "c"},
		},
		{
			name:    "backtick quoted strings",
			query:   "find callers of `HandleRequest`",
			wantAny: []string{"HandleRequest", "callers"},
		},
		{
			name:     "empty query",
			query:    "",
			wantAny:  nil,
			wantNone: nil,
		},
		{
			name:    "query with punctuation stripped",
			query:   "what are the hotspots? (in materials)",
			wantAny: []string{"hotspots", "materials"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := TokenizeQuery(tt.query)
			tokenSet := make(map[string]bool)
			for _, tok := range tokens {
				tokenSet[tok] = true
			}

			for _, want := range tt.wantAny {
				if !tokenSet[want] {
					t.Errorf("expected token %q not found in %v", want, tokens)
				}
			}
			for _, notWant := range tt.wantNone {
				if tokenSet[notWant] {
					t.Errorf("unexpected token %q found in %v", notWant, tokens)
				}
			}
		})
	}
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"FindHotspots", []string{"Find", "Hotspots"}},
		{"handleHTTPRequest", []string{"handle", "HTTP", "Request"}},
		{"simpleword", []string{"simpleword"}},
		{"URLParser", []string{"URL", "Parser"}},
		{"getHTTPSConnection", []string{"get", "HTTPS", "Connection"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCamelCase(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractQuoted(t *testing.T) {
	tests := []struct {
		input     string
		wantExtra []string
		wantRest  string
	}{
		{
			input:     `find "HandleAgent" in code`,
			wantExtra: []string{"HandleAgent"},
		},
		{
			input:     `check 'materials' and "render"`,
			wantExtra: []string{"materials", "render"},
		},
		{
			input:     "no quotes here",
			wantExtra: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var extracted []string
			extractQuoted(tt.input, func(s string) { extracted = append(extracted, s) })

			if len(extracted) != len(tt.wantExtra) {
				t.Errorf("extractQuoted(%q) extracted %v, want %v", tt.input, extracted, tt.wantExtra)
				return
			}
			for i := range extracted {
				if extracted[i] != tt.wantExtra[i] {
					t.Errorf("extractQuoted(%q)[%d] = %q, want %q", tt.input, i, extracted[i], tt.wantExtra[i])
				}
			}
		})
	}
}
