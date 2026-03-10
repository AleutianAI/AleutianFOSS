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
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/golang/snappy"
)

// BboltSchemaVersion is the version of the bbolt serialization schema.
// Increment when the bbolt format changes in a breaking way.
// GR-77a: Decode failure triggers delete + rebuild.
const BboltSchemaVersion = "1.0"

// nodePayload is the gob-serializable representation of a Node.
// Uses value copy of ast.Symbol (not pointer) for safe encoding.
type nodePayload struct {
	ID     string
	Symbol ast.Symbol
}

// edgePayload is the gob-serializable representation of an Edge.
type edgePayload struct {
	FromID   string
	ToID     string
	Type     EdgeType
	Location ast.Location
}

func init() {
	// Register types that gob needs to encode/decode.
	gob.Register(ast.Symbol{})
	gob.Register(ast.SymbolMetadata{})
	gob.Register(ast.CallSite{})
	gob.Register(ast.TypeReference{})
	gob.Register(nodePayload{})
	gob.Register(edgePayload{})
}

// encodeNode encodes a Node to snappy-compressed gob bytes.
//
// Description:
//
//	Converts a Node's ID and Symbol into a nodePayload, encodes it with gob,
//	then compresses the result with snappy. The resulting bytes are suitable
//	for storage in a bbolt bucket.
//
// Inputs:
//
//	node - The node to encode. Must not be nil, must have non-nil Symbol.
//
// Outputs:
//
//	[]byte - The compressed encoded bytes.
//	error - Non-nil if encoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeNode(node *Node) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("node must not be nil")
	}
	if node.Symbol == nil {
		return nil, fmt.Errorf("node symbol must not be nil for node %s", node.ID)
	}

	payload := nodePayload{
		ID:     node.ID,
		Symbol: *node.Symbol,
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, fmt.Errorf("gob encoding node %s: %w", node.ID, err)
	}

	return snappy.Encode(nil, buf.Bytes()), nil
}

// decodeNode decodes a Node from snappy-compressed gob bytes.
//
// Description:
//
//	Decompresses snappy bytes, then decodes the gob payload to reconstruct
//	a Node with its Symbol.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	*Node - The decoded node with Symbol populated.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeNode(data []byte) (*Node, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data for node decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return nil, fmt.Errorf("snappy decoding node: %w", err)
	}

	var payload nodePayload
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("gob decoding node: %w", err)
	}

	sym := payload.Symbol
	return &Node{
		ID:     payload.ID,
		Symbol: &sym,
	}, nil
}

// encodeEdges encodes a slice of Edges to snappy-compressed gob bytes.
//
// Description:
//
//	Converts edges to edgePayload slice, encodes with gob, compresses with snappy.
//
// Inputs:
//
//	edges - The edges to encode. May be nil or empty.
//
// Outputs:
//
//	[]byte - The compressed encoded bytes.
//	error - Non-nil if encoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeEdges(edges []*Edge) ([]byte, error) {
	payloads := make([]edgePayload, len(edges))
	for i, e := range edges {
		if e == nil {
			continue
		}
		payloads[i] = edgePayload{
			FromID:   e.FromID,
			ToID:     e.ToID,
			Type:     e.Type,
			Location: e.Location,
		}
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(payloads); err != nil {
		return nil, fmt.Errorf("gob encoding edges: %w", err)
	}

	return snappy.Encode(nil, buf.Bytes()), nil
}

// decodeEdges decodes a slice of Edges from snappy-compressed gob bytes.
//
// Description:
//
//	Decompresses snappy bytes, then decodes the gob payload to reconstruct
//	a slice of Edge pointers.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	[]*Edge - The decoded edges.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeEdges(data []byte) ([]*Edge, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data for edges decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return nil, fmt.Errorf("snappy decoding edges: %w", err)
	}

	var payloads []edgePayload
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&payloads); err != nil {
		return nil, fmt.Errorf("gob decoding edges: %w", err)
	}

	edges := make([]*Edge, len(payloads))
	for i, p := range payloads {
		edges[i] = &Edge{
			FromID:   p.FromID,
			ToID:     p.ToID,
			Type:     p.Type,
			Location: p.Location,
		}
	}

	return edges, nil
}

// encodeStringSlice encodes a string slice to snappy-compressed gob bytes.
//
// Description:
//
//	Used for encoding index bucket values (lists of node IDs).
//
// Inputs:
//
//	ids - The string slice to encode. May be nil or empty.
//
// Outputs:
//
//	[]byte - The compressed encoded bytes.
//	error - Non-nil if encoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeStringSlice(ids []string) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(ids); err != nil {
		return nil, fmt.Errorf("gob encoding string slice: %w", err)
	}

	return snappy.Encode(nil, buf.Bytes()), nil
}

// decodeStringSlice decodes a string slice from snappy-compressed gob bytes.
//
// Description:
//
//	Used for decoding index bucket values (lists of node IDs).
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	[]string - The decoded string slice.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeStringSlice(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data for string slice decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return nil, fmt.Errorf("snappy decoding string slice: %w", err)
	}

	var ids []string
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&ids); err != nil {
		return nil, fmt.Errorf("gob decoding string slice: %w", err)
	}

	return ids, nil
}

// gobSnappyEncode encodes a value to snappy-compressed gob bytes.
// Internal helper used by type-safe encode functions.
func gobSnappyEncode(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return nil, fmt.Errorf("gob encoding: %w", err)
	}
	return snappy.Encode(nil, buf.Bytes()), nil
}

// encodeMetaString encodes a string to snappy-compressed gob bytes.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeMetaString(value string) ([]byte, error) {
	return gobSnappyEncode(value)
}

// encodeMetaInt encodes an int to snappy-compressed gob bytes.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeMetaInt(value int) ([]byte, error) {
	return gobSnappyEncode(value)
}

// encodeMetaInt64 encodes an int64 to snappy-compressed gob bytes.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeMetaInt64(value int64) ([]byte, error) {
	return gobSnappyEncode(value)
}

// encodeMetaFileMtimes encodes a file mtimes map to snappy-compressed gob bytes.
//
// Thread Safety: Safe for concurrent use (no shared state).
func encodeMetaFileMtimes(value map[string]int64) ([]byte, error) {
	return gobSnappyEncode(value)
}

// decodeMetaString decodes a string metadata value from snappy-compressed gob bytes.
//
// Description:
//
//	Convenience function for decoding string metadata values.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	string - The decoded string value.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeMetaString(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty data for meta string decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return "", fmt.Errorf("snappy decoding meta string: %w", err)
	}

	var s string
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&s); err != nil {
		return "", fmt.Errorf("gob decoding meta string: %w", err)
	}

	return s, nil
}

// decodeMetaInt decodes an int metadata value from snappy-compressed gob bytes.
//
// Description:
//
//	Convenience function for decoding int metadata values.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	int - The decoded int value.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeMetaInt(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty data for meta int decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return 0, fmt.Errorf("snappy decoding meta int: %w", err)
	}

	var n int
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&n); err != nil {
		return 0, fmt.Errorf("gob decoding meta int: %w", err)
	}

	return n, nil
}

// decodeMetaInt64 decodes an int64 metadata value from snappy-compressed gob bytes.
//
// Description:
//
//	Convenience function for decoding int64 metadata values.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	int64 - The decoded int64 value.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeMetaInt64(data []byte) (int64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty data for meta int64 decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return 0, fmt.Errorf("snappy decoding meta int64: %w", err)
	}

	var n int64
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&n); err != nil {
		return 0, fmt.Errorf("gob decoding meta int64: %w", err)
	}

	return n, nil
}

// decodeMetaFileMtimes decodes file mtimes map from snappy-compressed gob bytes.
//
// Description:
//
//	Convenience function for decoding file modification times map.
//
// Inputs:
//
//	data - The compressed encoded bytes. Must not be empty.
//
// Outputs:
//
//	map[string]int64 - The decoded file mtimes map.
//	error - Non-nil if decompression or decoding fails.
//
// Thread Safety: Safe for concurrent use (no shared state).
func decodeMetaFileMtimes(data []byte) (map[string]int64, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data for file mtimes decode")
	}

	decoded, err := snappy.Decode(nil, data)
	if err != nil {
		return nil, fmt.Errorf("snappy decoding file mtimes: %w", err)
	}

	var mtimes map[string]int64
	if err := gob.NewDecoder(bytes.NewReader(decoded)).Decode(&mtimes); err != nil {
		return nil, fmt.Errorf("gob decoding file mtimes: %w", err)
	}

	return mtimes, nil
}
