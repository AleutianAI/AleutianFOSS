// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package lsp provides Language Server Protocol integration for Trace.
//
// The LSP layer provides accurate type information, references, and rename
// capabilities by communicating with external language servers (gopls, pyright, etc.).
//
// # Architecture
//
// The package uses a hybrid approach where Tree-sitter handles fast graph building
// while LSP servers handle accuracy-critical queries like type resolution,
// references, and rename operations.
//
//	┌─────────────────────────────────────────────────────────────────────────────┐
//	│                           Trace Service                                 │
//	│                                                                             │
//	│  Graph Queries (Tree-sitter)  ◄──  Query Router  ──►  LSP Queries           │
//	│  Fast ~1ms, ~90% accuracy                            Accurate ~50ms, ~100%  │
//	└─────────────────────────────────────────────────────────────────────────────┘
//
// # Components
//
//   - Manager: Orchestrates multiple language server instances
//   - Server: Manages individual LSP server processes
//   - Protocol: Handles JSON-RPC communication
//   - Operations: Provides high-level LSP operations (definition, references, etc.)
//
// # Thread Safety
//
// All exported types are safe for concurrent use.
//
// # Example
//
//	mgr := lsp.NewManager("/path/to/project", lsp.DefaultManagerConfig())
//	defer mgr.ShutdownAll(context.Background())
//
//	ops := lsp.NewOperations(mgr)
//	locs, err := ops.Definition(ctx, "/path/to/file.go", 10, 5)
package lsp
