// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package main implements the Aleutian Trace MCP server.
//
// Description:
//
//	This binary exposes all trace tools via the Model Context Protocol (MCP)
//	over stdio. It is designed to be used as an MCP server by Claude Code,
//	Cursor, Windsurf, and other MCP-compatible AI assistants.
//
//	The server delegates all tool calls to the Trace service via the ToolBridge
//	HTTP client, which handles graph_id management, parameter mapping, and
//	result truncation.
//
// Usage:
//
//	trace-mcp [flags]
//	  -trace-url string  Trace service URL (default: ALEUTIAN_TRACE_URL env or http://localhost:12217)
//
// Example Claude Code config (.claude/mcp.json):
//
//	{
//	  "mcpServers": {
//	    "aleutian-trace": {
//	      "command": "trace-mcp",
//	      "args": ["-trace-url", "http://localhost:12217"]
//	    }
//	  }
//	}
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	traceURL := flag.String("trace-url", "", "Trace service URL (default: ALEUTIAN_TRACE_URL env or http://localhost:12217)")
	flag.Parse()

	// Initialize OTel telemetry. AllowDegraded=true so the MCP server works
	// even when Jaeger/Prometheus are not running (common for local dev).
	telemetryCfg := telemetry.DefaultConfig()
	telemetryCfg.ServiceName = "aleutian-trace-mcp"
	telemetryCfg.AllowDegraded = true
	telemetryShutdown, telemetryErr := telemetry.Init(context.Background(), telemetryCfg)
	if telemetryErr != nil {
		slog.Warn("Telemetry init failed, running without OTel",
			slog.String("error", telemetryErr.Error()))
	}
	defer func() {
		if telemetryShutdown != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = telemetryShutdown(shutdownCtx)
		}
	}()

	// Resolve URL: flag > env > default.
	resolvedURL := resolveTraceURL(*traceURL)

	b := bridge.NewToolBridge(bridge.WithTraceURL(resolvedURL))

	server := mcp.NewServer(
		&mcp.Implementation{Name: "aleutian-trace", Version: version}, nil,
	)
	registerTraceTools(server, b)
	registerResources(server, b)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("MCP server error: %v", err)
	}
}

// resolveTraceURL resolves the trace service URL from flag, env, or default.
//
// Description:
//
//	Priority: flag value > ALEUTIAN_TRACE_URL env var > default (localhost:12217).
//
// Inputs:
//
//	flagValue - the value from the -trace-url flag (empty if not set)
//
// Outputs:
//
//	string - the resolved URL
func resolveTraceURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envURL := os.Getenv("ALEUTIAN_TRACE_URL"); envURL != "" {
		return envURL
	}
	return bridge.DefaultTraceURL
}
