// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package format

import (
	"encoding/json"
	"errors"
	"io"
)

// CompactSchemaVersion is the current compact schema version.
const CompactSchemaVersion = "1"

// CompactSchemaName is the schema identifier.
const CompactSchemaName = "trace/compact/v1"

// CompactKeyMapping maps short keys to full names.
var CompactKeyMapping = map[string]string{
	"_v":    "version",
	"_s":    "schema",
	"_keys": "key_mapping",
	"q":     "query",
	"f":     "focus_nodes",
	"g":     "graph_stats",
	"k":     "key_nodes",
	"r":     "risk",
	"n":     "node_count",
	"e":     "edge_count",
	"d":     "depth",
	"i":     "id",
	"nm":    "name",
	"l":     "location",
	"t":     "type",
	"p":     "package",
	"c":     "callers",
	"o":     "callees",
	"fl":    "flag",
	"w":     "warning",
	"cn":    "connections",
	"ti":    "target_id",
	"ct":    "connection_type",
	"lv":    "level",
	"di":    "direct_impact",
	"ii":    "indirect_impact",
	"ws":    "warnings",
}

// CompactFormatter formats results as minimal-token JSON.
type CompactFormatter struct {
	includeSchema bool
}

// NewCompactFormatter creates a new compact formatter.
func NewCompactFormatter() *CompactFormatter {
	return &CompactFormatter{includeSchema: true}
}

// NewCompactFormatterNoSchema creates a compact formatter without schema info.
func NewCompactFormatterNoSchema() *CompactFormatter {
	return &CompactFormatter{includeSchema: false}
}

// compactResult is the compact JSON structure.
type compactResult struct {
	Version string            `json:"_v,omitempty"`
	Schema  string            `json:"_s,omitempty"`
	Keys    map[string]string `json:"_keys,omitempty"`
	Query   string            `json:"q,omitempty"`
	Focus   []compactNode     `json:"f,omitempty"`
	Graph   *compactGraph     `json:"g,omitempty"`
	Key     []compactKeyNode  `json:"k,omitempty"`
	Risk    *compactRisk      `json:"r,omitempty"`
}

type compactNode struct {
	ID       string `json:"i,omitempty"`
	Name     string `json:"nm,omitempty"`
	Location string `json:"l,omitempty"`
	Type     string `json:"t,omitempty"`
	Package  string `json:"p,omitempty"`
}

type compactGraph struct {
	Nodes int `json:"n"`
	Edges int `json:"e"`
	Depth int `json:"d"`
}

type compactKeyNode struct {
	ID          string        `json:"i,omitempty"`
	Name        string        `json:"nm,omitempty"`
	Location    string        `json:"l,omitempty"`
	Type        string        `json:"t,omitempty"`
	Callers     int           `json:"c,omitempty"`
	Callees     int           `json:"o,omitempty"`
	Flag        string        `json:"fl,omitempty"`
	Warning     string        `json:"w,omitempty"`
	Connections []compactConn `json:"cn,omitempty"`
}

type compactConn struct {
	Target string `json:"ti"`
	Type   string `json:"ct"`
}

type compactRisk struct {
	Level    string   `json:"lv,omitempty"`
	Direct   int      `json:"di,omitempty"`
	Indirect int      `json:"ii,omitempty"`
	Warnings []string `json:"ws,omitempty"`
}

// Format converts the result to compact JSON string.
func (f *CompactFormatter) Format(result interface{}) (string, error) {
	compact, err := f.toCompact(result)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(compact)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// toCompact converts a result to compact format.
func (f *CompactFormatter) toCompact(result interface{}) (*compactResult, error) {
	switch r := result.(type) {
	case *GraphResult:
		return f.graphToCompact(r), nil
	case GraphResult:
		return f.graphToCompact(&r), nil
	default:
		return nil, errors.New("unsupported result type for compact format")
	}
}

// graphToCompact converts a GraphResult to compact format.
func (f *CompactFormatter) graphToCompact(r *GraphResult) *compactResult {
	compact := &compactResult{
		Query: r.Query,
	}

	if f.includeSchema {
		compact.Version = CompactSchemaVersion
		compact.Schema = CompactSchemaName
		compact.Keys = CompactKeyMapping
	}

	// Focus nodes
	if len(r.FocusNodes) > 0 {
		compact.Focus = make([]compactNode, len(r.FocusNodes))
		for i, n := range r.FocusNodes {
			compact.Focus[i] = compactNode{
				ID:       n.ID,
				Name:     n.Name,
				Location: n.Location,
				Type:     n.Type,
				Package:  n.Package,
			}
		}
	}

	// Graph stats
	compact.Graph = &compactGraph{
		Nodes: r.Graph.NodeCount,
		Edges: r.Graph.EdgeCount,
		Depth: r.Graph.Depth,
	}

	// Key nodes
	if len(r.KeyNodes) > 0 {
		compact.Key = make([]compactKeyNode, len(r.KeyNodes))
		for i, k := range r.KeyNodes {
			ck := compactKeyNode{
				ID:       k.ID,
				Name:     k.Name,
				Location: k.Location,
				Type:     k.Type,
				Callers:  k.Callers,
				Callees:  k.Callees,
				Flag:     k.Flag,
				Warning:  k.Warning,
			}

			if len(k.Connections) > 0 {
				ck.Connections = make([]compactConn, len(k.Connections))
				for j, c := range k.Connections {
					ck.Connections[j] = compactConn{
						Target: c.TargetID,
						Type:   c.Type,
					}
				}
			}

			compact.Key[i] = ck
		}
	}

	// Risk
	if r.Risk.Level != "" {
		compact.Risk = &compactRisk{
			Level:    r.Risk.Level,
			Direct:   r.Risk.DirectImpact,
			Indirect: r.Risk.IndirectImpact,
			Warnings: r.Risk.Warnings,
		}
	}

	return compact
}

// Name returns the format name.
func (f *CompactFormatter) Name() FormatType {
	return FormatCompact
}

// TokenEstimate estimates the number of tokens.
func (f *CompactFormatter) TokenEstimate(result interface{}, tokenizer ...string) int {
	output, err := f.Format(result)
	if err != nil {
		return 0
	}

	ratio := TokenRatio["default"]
	if len(tokenizer) > 0 {
		if r, ok := TokenRatio[tokenizer[0]]; ok {
			ratio = r
		}
	}

	return int(float64(len(output)) / ratio)
}

// IsReversible returns true - with schema, compact JSON is reversible.
func (f *CompactFormatter) IsReversible() bool {
	return f.includeSchema
}

// SupportsStreaming returns false - compact needs full data for compression.
func (f *CompactFormatter) SupportsStreaming() bool {
	return false
}

// FormatStreaming is not supported for compact format.
func (f *CompactFormatter) FormatStreaming(result interface{}, w io.Writer) error {
	output, err := f.Format(result)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, output)
	return err
}

// GetSchema returns the compact schema information.
func GetSchema() map[string]string {
	return CompactKeyMapping
}
