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
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBboltCodec_EncodeDecodeNode(t *testing.T) {
	t.Run("basic round-trip", func(t *testing.T) {
		node := &Node{
			ID: "main.go:10:HandleRequest",
			Symbol: &ast.Symbol{
				ID:        "main.go:10:HandleRequest",
				Name:      "HandleRequest",
				Kind:      ast.SymbolKindFunction,
				FilePath:  "main.go",
				StartLine: 10,
				EndLine:   25,
				StartCol:  0,
				EndCol:    1,
				Signature: "func(ctx context.Context) error",
				Package:   "main",
				Exported:  true,
				Language:  "go",
			},
		}

		data, err := encodeNode(node)
		require.NoError(t, err)
		require.NotEmpty(t, data)

		decoded, err := decodeNode(data)
		require.NoError(t, err)

		assert.Equal(t, node.ID, decoded.ID)
		assert.Equal(t, node.Symbol.Name, decoded.Symbol.Name)
		assert.Equal(t, node.Symbol.Kind, decoded.Symbol.Kind)
		assert.Equal(t, node.Symbol.FilePath, decoded.Symbol.FilePath)
		assert.Equal(t, node.Symbol.StartLine, decoded.Symbol.StartLine)
		assert.Equal(t, node.Symbol.EndLine, decoded.Symbol.EndLine)
		assert.Equal(t, node.Symbol.Signature, decoded.Symbol.Signature)
		assert.Equal(t, node.Symbol.Package, decoded.Symbol.Package)
		assert.Equal(t, node.Symbol.Exported, decoded.Symbol.Exported)
		assert.Equal(t, node.Symbol.Language, decoded.Symbol.Language)
	})

	t.Run("symbol with children", func(t *testing.T) {
		node := &Node{
			ID: "service.go:5:UserService",
			Symbol: &ast.Symbol{
				ID:        "service.go:5:UserService",
				Name:      "UserService",
				Kind:      ast.SymbolKindStruct,
				FilePath:  "service.go",
				StartLine: 5,
				EndLine:   50,
				Language:  "go",
				Children: []*ast.Symbol{
					{
						ID:        "service.go:10:Create",
						Name:      "Create",
						Kind:      ast.SymbolKindMethod,
						FilePath:  "service.go",
						StartLine: 10,
						EndLine:   20,
						Receiver:  "UserService",
						Language:  "go",
					},
					{
						ID:        "service.go:22:Delete",
						Name:      "Delete",
						Kind:      ast.SymbolKindMethod,
						FilePath:  "service.go",
						StartLine: 22,
						EndLine:   30,
						Receiver:  "UserService",
						Language:  "go",
					},
				},
			},
		}

		data, err := encodeNode(node)
		require.NoError(t, err)

		decoded, err := decodeNode(data)
		require.NoError(t, err)

		require.Len(t, decoded.Symbol.Children, 2)
		assert.Equal(t, "Create", decoded.Symbol.Children[0].Name)
		assert.Equal(t, "Delete", decoded.Symbol.Children[1].Name)
		assert.Equal(t, ast.SymbolKindMethod, decoded.Symbol.Children[0].Kind)
	})

	t.Run("unicode paths", func(t *testing.T) {
		node := &Node{
			ID: "日本語/ファイル.go:1:関数",
			Symbol: &ast.Symbol{
				ID:        "日本語/ファイル.go:1:関数",
				Name:      "関数",
				Kind:      ast.SymbolKindFunction,
				FilePath:  "日本語/ファイル.go",
				StartLine: 1,
				EndLine:   5,
				Language:  "go",
			},
		}

		data, err := encodeNode(node)
		require.NoError(t, err)

		decoded, err := decodeNode(data)
		require.NoError(t, err)

		assert.Equal(t, node.ID, decoded.ID)
		assert.Equal(t, "関数", decoded.Symbol.Name)
		assert.Equal(t, "日本語/ファイル.go", decoded.Symbol.FilePath)
	})

	t.Run("all SymbolKind values", func(t *testing.T) {
		kinds := []ast.SymbolKind{
			ast.SymbolKindUnknown, ast.SymbolKindPackage, ast.SymbolKindFile,
			ast.SymbolKindFunction, ast.SymbolKindMethod, ast.SymbolKindInterface,
			ast.SymbolKindStruct, ast.SymbolKindType, ast.SymbolKindVariable,
			ast.SymbolKindConstant, ast.SymbolKindField, ast.SymbolKindImport,
			ast.SymbolKindClass, ast.SymbolKindExternal,
		}

		for _, kind := range kinds {
			node := &Node{
				ID: "test.go:1:sym",
				Symbol: &ast.Symbol{
					ID:        "test.go:1:sym",
					Name:      "sym",
					Kind:      kind,
					FilePath:  "test.go",
					StartLine: 1,
					EndLine:   1,
					Language:  "go",
				},
			}

			data, err := encodeNode(node)
			require.NoError(t, err, "encode failed for kind %v", kind)

			decoded, err := decodeNode(data)
			require.NoError(t, err, "decode failed for kind %v", kind)
			assert.Equal(t, kind, decoded.Symbol.Kind)
		}
	})

	t.Run("symbol with metadata and calls", func(t *testing.T) {
		node := &Node{
			ID: "handler.go:10:HandleRequest",
			Symbol: &ast.Symbol{
				ID:        "handler.go:10:HandleRequest",
				Name:      "HandleRequest",
				Kind:      ast.SymbolKindFunction,
				FilePath:  "handler.go",
				StartLine: 10,
				EndLine:   30,
				Language:  "go",
				Metadata: &ast.SymbolMetadata{
					Decorators: []string{"middleware", "auth"},
					DecoratorArgs: map[string][]string{
						"middleware": {"LoggingMiddleware"},
					},
				},
				Calls: []ast.CallSite{
					{
						Target: "db.Query",
						Location: ast.Location{
							FilePath:  "handler.go",
							StartLine: 15,
							EndLine:   15,
							StartCol:  4,
							EndCol:    14,
						},
						IsMethod: true,
						Receiver: "db",
					},
					{
						Target: "json.Marshal",
						Location: ast.Location{
							FilePath:  "handler.go",
							StartLine: 20,
							EndLine:   20,
							StartCol:  8,
							EndCol:    20,
						},
					},
				},
			},
		}

		data, err := encodeNode(node)
		require.NoError(t, err)

		decoded, err := decodeNode(data)
		require.NoError(t, err)

		require.NotNil(t, decoded.Symbol.Metadata)
		assert.Equal(t, []string{"middleware", "auth"}, decoded.Symbol.Metadata.Decorators)
		assert.Equal(t, []string{"LoggingMiddleware"}, decoded.Symbol.Metadata.DecoratorArgs["middleware"])

		require.Len(t, decoded.Symbol.Calls, 2)
		assert.Equal(t, "db.Query", decoded.Symbol.Calls[0].Target)
		assert.Equal(t, 15, decoded.Symbol.Calls[0].Location.StartLine)
		assert.True(t, decoded.Symbol.Calls[0].IsMethod)
		assert.Equal(t, "db", decoded.Symbol.Calls[0].Receiver)
		assert.Equal(t, "json.Marshal", decoded.Symbol.Calls[1].Target)
	})

	t.Run("nil node errors", func(t *testing.T) {
		_, err := encodeNode(nil)
		assert.Error(t, err)
	})

	t.Run("nil symbol errors", func(t *testing.T) {
		_, err := encodeNode(&Node{ID: "test"})
		assert.Error(t, err)
	})

	t.Run("empty data errors", func(t *testing.T) {
		_, err := decodeNode(nil)
		assert.Error(t, err)

		_, err = decodeNode([]byte{})
		assert.Error(t, err)
	})
}

func TestBboltCodec_EncodeDecodeEdges(t *testing.T) {
	t.Run("basic round-trip", func(t *testing.T) {
		edges := []*Edge{
			{
				FromID: "a.go:1:Foo",
				ToID:   "b.go:5:Bar",
				Type:   EdgeTypeCalls,
				Location: ast.Location{
					FilePath:  "a.go",
					StartLine: 15,
					EndLine:   15,
					StartCol:  4,
					EndCol:    12,
				},
			},
			{
				FromID: "a.go:1:Foo",
				ToID:   "c.go:10:Baz",
				Type:   EdgeTypeImports,
				Location: ast.Location{
					FilePath:  "a.go",
					StartLine: 3,
					EndLine:   3,
				},
			},
		}

		data, err := encodeEdges(edges)
		require.NoError(t, err)

		decoded, err := decodeEdges(data)
		require.NoError(t, err)

		require.Len(t, decoded, 2)
		assert.Equal(t, "a.go:1:Foo", decoded[0].FromID)
		assert.Equal(t, "b.go:5:Bar", decoded[0].ToID)
		assert.Equal(t, EdgeTypeCalls, decoded[0].Type)
		assert.Equal(t, 15, decoded[0].Location.StartLine)
		assert.Equal(t, EdgeTypeImports, decoded[1].Type)
	})

	t.Run("empty slice", func(t *testing.T) {
		data, err := encodeEdges([]*Edge{})
		require.NoError(t, err)

		decoded, err := decodeEdges(data)
		require.NoError(t, err)
		assert.Empty(t, decoded)
	})

	t.Run("nil slice", func(t *testing.T) {
		data, err := encodeEdges(nil)
		require.NoError(t, err)

		decoded, err := decodeEdges(data)
		require.NoError(t, err)
		assert.Empty(t, decoded)
	})

	t.Run("all edge types", func(t *testing.T) {
		edgeTypes := []EdgeType{
			EdgeTypeUnknown, EdgeTypeCalls, EdgeTypeImports, EdgeTypeDefines,
			EdgeTypeImplements, EdgeTypeEmbeds, EdgeTypeReferences,
			EdgeTypeReturns, EdgeTypeReceives, EdgeTypeParameters,
		}

		for _, et := range edgeTypes {
			edges := []*Edge{{
				FromID: "a:1:x",
				ToID:   "b:2:y",
				Type:   et,
			}}

			data, err := encodeEdges(edges)
			require.NoError(t, err, "encode failed for type %v", et)

			decoded, err := decodeEdges(data)
			require.NoError(t, err, "decode failed for type %v", et)
			assert.Equal(t, et, decoded[0].Type)
		}
	})
}

func TestBboltCodec_EncodeDecodeStringSlice(t *testing.T) {
	t.Run("basic round-trip", func(t *testing.T) {
		ids := []string{"node1", "node2", "node3"}

		data, err := encodeStringSlice(ids)
		require.NoError(t, err)

		decoded, err := decodeStringSlice(data)
		require.NoError(t, err)
		assert.Equal(t, ids, decoded)
	})

	t.Run("empty slice", func(t *testing.T) {
		data, err := encodeStringSlice([]string{})
		require.NoError(t, err)

		decoded, err := decodeStringSlice(data)
		require.NoError(t, err)
		assert.Empty(t, decoded)
	})

	t.Run("nil slice", func(t *testing.T) {
		data, err := encodeStringSlice(nil)
		require.NoError(t, err)

		decoded, err := decodeStringSlice(data)
		require.NoError(t, err)
		// gob decodes nil slice as nil
		assert.Nil(t, decoded)
	})

	t.Run("empty data errors", func(t *testing.T) {
		_, err := decodeStringSlice(nil)
		assert.Error(t, err)
	})
}

func TestBboltCodec_EncodeMeta(t *testing.T) {
	t.Run("string round-trip", func(t *testing.T) {
		data, err := encodeMetaString("1.0")
		require.NoError(t, err)

		decoded, err := decodeMetaString(data)
		require.NoError(t, err)
		assert.Equal(t, "1.0", decoded)
	})

	t.Run("int round-trip", func(t *testing.T) {
		data, err := encodeMetaInt(42)
		require.NoError(t, err)

		decoded, err := decodeMetaInt(data)
		require.NoError(t, err)
		assert.Equal(t, 42, decoded)
	})

	t.Run("int64 round-trip", func(t *testing.T) {
		data, err := encodeMetaInt64(int64(1710000000000))
		require.NoError(t, err)

		decoded, err := decodeMetaInt64(data)
		require.NoError(t, err)
		assert.Equal(t, int64(1710000000000), decoded)
	})

	t.Run("file mtimes round-trip", func(t *testing.T) {
		mtimes := map[string]int64{
			"main.go":    1710000000,
			"handler.go": 1710000001,
		}

		data, err := encodeMetaFileMtimes(mtimes)
		require.NoError(t, err)

		decoded, err := decodeMetaFileMtimes(data)
		require.NoError(t, err)
		assert.Equal(t, mtimes, decoded)
	})

	t.Run("empty file mtimes", func(t *testing.T) {
		mtimes := map[string]int64{}

		data, err := encodeMetaFileMtimes(mtimes)
		require.NoError(t, err)

		decoded, err := decodeMetaFileMtimes(data)
		require.NoError(t, err)
		assert.Empty(t, decoded)
	})
}
