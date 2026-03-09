// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"context"
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// mcpTracer is the package-level OTel tracer for MCP tool handlers.
var mcpTracer = otel.Tracer("aleutian.mcp.server")

// startToolSpan creates an OTel span for an MCP tool handler.
//
// Description:
//
//	Creates a span named "mcp.tool.<tool_name>" with the tool name as an attribute.
//	Additional attributes for input parameters should be set by the caller.
//
// Inputs:
//
//	ctx - Parent context from the MCP handler.
//	toolName - The MCP tool name.
//	attrs - Additional span attributes for input parameters.
//
// Outputs:
//
//	context.Context - Context with span attached.
//	trace.Span - The created span. Caller must call span.End().
//
// Thread Safety: Safe for concurrent use.
func startToolSpan(ctx context.Context, toolName string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	allAttrs := make([]attribute.KeyValue, 0, len(attrs)+1)
	allAttrs = append(allAttrs, attribute.String("mcp.tool", toolName))
	allAttrs = append(allAttrs, attrs...)
	return mcpTracer.Start(ctx, "mcp.tool."+toolName,
		trace.WithAttributes(allAttrs...),
	)
}

// finishToolSpan sets the span status based on the bridge result and ends it.
//
// Description:
//
//	Sets span status to Error if the result indicates an error, or OK otherwise.
//	Logs a structured message with trace context for correlation.
//
// Inputs:
//
//	ctx - Context with active span.
//	span - The span to finish.
//	toolName - The MCP tool name (for logging).
//	result - The bridge ToolResult (nil if err is non-nil).
//	err - Go error from the bridge call (nil on success).
//
// Thread Safety: Safe for concurrent use.
func finishToolSpan(ctx context.Context, span trace.Span, toolName string, result *bridge.ToolResult, err error) {
	logger := telemetry.LoggerWithTrace(ctx, slog.Default())
	if err != nil {
		telemetry.RecordError(span, err)
		logger.Error("MCP tool call failed", slog.String("tool", toolName), slog.String("error", err.Error()))
		return
	}
	if result != nil && result.IsError {
		span.SetStatus(codes.Error, "tool returned error")
		logger.Warn("MCP tool returned error", slog.String("tool", toolName))
		return
	}
	telemetry.SetSpanOK(span)
	logger.Debug("MCP tool call succeeded", slog.String("tool", toolName))
}

// Input structs for each tool. The jsonschema tags generate the MCP input schema.

// InitProjectInput is the input for trace_init_project.
type InitProjectInput struct {
	ProjectRoot string `json:"project_root" jsonschema:"Absolute path to the project root directory to analyze"`
}

// FindCallersInput is the input for trace_find_callers.
type FindCallersInput struct {
	FunctionName string `json:"function_name" jsonschema:"Name of the function to find callers for"`
}

// FindCalleesInput is the input for trace_find_callees.
type FindCalleesInput struct {
	FunctionName string `json:"function_name" jsonschema:"Name of the function to find callees for"`
}

// FindImplementationsInput is the input for trace_find_implementations.
type FindImplementationsInput struct {
	InterfaceName string `json:"interface_name" jsonschema:"Name of the interface to find implementations for"`
}

// FindSymbolInput is the input for trace_find_symbol.
type FindSymbolInput struct {
	Name string `json:"name" jsonschema:"Name of the symbol to look up (function, struct, interface, etc.)"`
}

// GetCallChainInput is the input for trace_get_call_chain.
type GetCallChainInput struct {
	From string `json:"from" jsonschema:"Source function name"`
	To   string `json:"to" jsonschema:"Target function name"`
}

// FindReferencesInput is the input for trace_find_references.
type FindReferencesInput struct {
	SymbolName string `json:"symbol_name" jsonschema:"Name of the symbol to find references for"`
}

// FindHotspotsInput is the input for trace_find_hotspots.
type FindHotspotsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of hotspots to return (default: 10)"`
}

// FindDeadCodeInput is the input for trace_find_dead_code (no parameters).
type FindDeadCodeInput struct{}

// FindCyclesInput is the input for trace_find_cycles (no parameters).
type FindCyclesInput struct{}

// FindImportantInput is the input for trace_find_important.
type FindImportantInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of important nodes to return (default: 10)"`
}

// FindCommunitiesInput is the input for trace_find_communities (no parameters).
type FindCommunitiesInput struct{}

// FindPathInput is the input for trace_find_path.
type FindPathInput struct {
	From string `json:"from" jsonschema:"Source function name"`
	To   string `json:"to" jsonschema:"Target function name"`
}

// toolResultFromBridge converts a bridge.ToolResult to an MCP CallToolResult.
func toolResultFromBridge(result *bridge.ToolResult) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: result.Content},
		},
		IsError: result.IsError,
	}
}

// registerTraceTools registers all 13 trace tools with the MCP server.
//
// Description:
//
//	Each tool is registered with a name, description (including when-to-use guidance),
//	and a handler that delegates to the ToolBridge. The SDK auto-generates JSON Schema
//	from the input struct tags.
//
// Inputs:
//
//	s - the MCP server
//	b - the ToolBridge for HTTP delegation
//
// Thread Safety: Safe; called once during server setup.
func registerTraceTools(s *mcp.Server, b *bridge.ToolBridge) {
	// trace_init_project
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_init_project",
		Description: "Initialize a code graph for a project. " +
			"Use this FIRST before any other trace tool. Parses source files and builds a code graph. " +
			"Do NOT use if the project has already been initialized in this session.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input InitProjectInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_init_project",
			attribute.String("mcp.param.project_root", input.ProjectRoot),
		)
		defer span.End()

		result, err := b.InitProject(ctx, input.ProjectRoot)
		finishToolSpan(ctx, span, "trace_init_project", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_callers
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_callers",
		Description: "Find all functions that call a given function. " +
			"Use when you need to understand who depends on a function (impact analysis, refactoring). " +
			"Do NOT use for finding what a function calls (use trace_find_callees instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindCallersInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_callers",
			attribute.String("mcp.param.function_name", input.FunctionName),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_callers", map[string]any{"function_name": input.FunctionName})
		finishToolSpan(ctx, span, "trace_find_callers", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_callees
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_callees",
		Description: "Find all functions called by a given function. " +
			"Use when you need to understand a function's dependencies. " +
			"Do NOT use for finding who calls a function (use trace_find_callers instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindCalleesInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_callees",
			attribute.String("mcp.param.function_name", input.FunctionName),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_callees", map[string]any{"function_name": input.FunctionName})
		finishToolSpan(ctx, span, "trace_find_callees", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_implementations
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_implementations",
		Description: "Find all types that implement a given interface. " +
			"Use when you need to find concrete implementations of an interface. " +
			"Do NOT use for finding what interfaces a type implements.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindImplementationsInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_implementations",
			attribute.String("mcp.param.interface_name", input.InterfaceName),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_implementations", map[string]any{"interface_name": input.InterfaceName})
		finishToolSpan(ctx, span, "trace_find_implementations", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_symbol
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_symbol",
		Description: "Look up a symbol (function, struct, interface, variable) by name. " +
			"Use when you need detailed information about a specific symbol. " +
			"Do NOT use for finding relationships (use callers/callees/references instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindSymbolInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_symbol",
			attribute.String("mcp.param.name", input.Name),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_symbol", map[string]any{"name": input.Name})
		finishToolSpan(ctx, span, "trace_find_symbol", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_get_call_chain
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_get_call_chain",
		Description: "Find the shortest call chain between two functions. " +
			"Use when you need to understand how control flows from one function to another. " +
			"Do NOT use when you only need direct callers/callees.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetCallChainInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_get_call_chain",
			attribute.String("mcp.param.from", input.From),
			attribute.String("mcp.param.to", input.To),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_get_call_chain", map[string]any{"from": input.From, "to": input.To})
		finishToolSpan(ctx, span, "trace_get_call_chain", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_references
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_references",
		Description: "Find all references to a symbol across the codebase. " +
			"Use when you need to find every usage of a type, function, or variable. " +
			"Do NOT use for call-graph analysis (use callers/callees instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindReferencesInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_references",
			attribute.String("mcp.param.symbol_name", input.SymbolName),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_references", map[string]any{"symbol_name": input.SymbolName})
		finishToolSpan(ctx, span, "trace_find_references", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_hotspots
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_hotspots",
		Description: "Find the most-connected nodes in the code graph (high in-degree + out-degree). " +
			"Use when you need to identify central/critical functions that many things depend on. " +
			"Do NOT use for finding unused code (use trace_find_dead_code instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindHotspotsInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_hotspots",
			attribute.Int("mcp.param.limit", input.Limit),
		)
		defer span.End()

		params := map[string]any{}
		if input.Limit > 0 {
			params["limit"] = input.Limit
		}
		result, err := b.CallTool(ctx, "trace_find_hotspots", params)
		finishToolSpan(ctx, span, "trace_find_hotspots", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_dead_code
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_dead_code",
		Description: "Find unreachable or unused code in the project. " +
			"Use when cleaning up the codebase or looking for functions that are never called. " +
			"Do NOT use for finding highly-connected code (use trace_find_hotspots instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ FindDeadCodeInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_dead_code")
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_dead_code", nil)
		finishToolSpan(ctx, span, "trace_find_dead_code", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_cycles
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_cycles",
		Description: "Find cyclic dependencies in the code graph. " +
			"Use when investigating circular imports or mutual dependencies. " +
			"Do NOT use for general dependency analysis (use callers/callees instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ FindCyclesInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_cycles")
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_cycles", nil)
		finishToolSpan(ctx, span, "trace_find_cycles", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_important
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_important",
		Description: "Find the most important nodes using PageRank analysis. " +
			"Use when you need to identify architecturally significant functions. " +
			"Do NOT use for finding most-connected nodes (use trace_find_hotspots instead).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindImportantInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_important",
			attribute.Int("mcp.param.limit", input.Limit),
		)
		defer span.End()

		params := map[string]any{}
		if input.Limit > 0 {
			params["limit"] = input.Limit
		}
		result, err := b.CallTool(ctx, "trace_find_important", params)
		finishToolSpan(ctx, span, "trace_find_important", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_communities
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_communities",
		Description: "Detect code communities (clusters of tightly-coupled functions). " +
			"Use when analyzing code architecture or looking for natural module boundaries. " +
			"Do NOT use for finding individual function relationships.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ FindCommunitiesInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_communities")
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_communities", nil)
		finishToolSpan(ctx, span, "trace_find_communities", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})

	// trace_find_path
	mcp.AddTool(s, &mcp.Tool{
		Name: "trace_find_path",
		Description: "Find the shortest path between two functions in the call graph. " +
			"Use when you need to understand how two functions are connected. " +
			"Do NOT use when you only need direct callers/callees of one function.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindPathInput) (*mcp.CallToolResult, any, error) {
		ctx, span := startToolSpan(ctx, "trace_find_path",
			attribute.String("mcp.param.from", input.From),
			attribute.String("mcp.param.to", input.To),
		)
		defer span.End()

		result, err := b.CallTool(ctx, "trace_find_path", map[string]any{"from": input.From, "to": input.To})
		finishToolSpan(ctx, span, "trace_find_path", result, err)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return toolResultFromBridge(result), nil, nil
	})
}

// errorResult creates an MCP error result from a Go error.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		},
		IsError: true,
	}
}
