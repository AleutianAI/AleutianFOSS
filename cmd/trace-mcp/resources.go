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

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerResources registers MCP resources for health and graph stats.
//
// Description:
//
//	Registers two read-only resources:
//	  - trace://health → calls the trace health endpoint
//	  - trace://graph/stats → calls the graph stats debug endpoint
//
// Inputs:
//
//	s - the MCP server
//	b - the ToolBridge for HTTP delegation
//
// Thread Safety: Safe; called once during server setup.
func registerResources(s *mcp.Server, b *bridge.ToolBridge) {
	// trace://health
	s.AddResource(&mcp.Resource{
		URI:         "trace://health",
		Name:        "Trace Health",
		Description: "Health status of the Aleutian Trace service",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ServerRequest[*mcp.ReadResourceParams]) (*mcp.ReadResourceResult, error) {
		ctx, span := mcpTracer.Start(ctx, "mcp.resource.health")
		defer span.End()

		result, err := b.HealthCheck(ctx)
		if err != nil {
			telemetry.RecordError(span, err)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      "trace://health",
					MIMEType: "application/json",
					Text:     err.Error(),
				}},
			}, nil
		}
		telemetry.SetSpanOK(span)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "trace://health",
				MIMEType: "application/json",
				Text:     result.Content,
			}},
		}, nil
	})

	// trace://graph/stats
	s.AddResource(&mcp.Resource{
		URI:         "trace://graph/stats",
		Name:        "Graph Statistics",
		Description: "Statistics for all loaded code graphs",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ServerRequest[*mcp.ReadResourceParams]) (*mcp.ReadResourceResult, error) {
		ctx, span := mcpTracer.Start(ctx, "mcp.resource.graph_stats")
		defer span.End()

		result, err := b.GetGraphStats(ctx)
		if err != nil {
			telemetry.RecordError(span, err)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      "trace://graph/stats",
					MIMEType: "application/json",
					Text:     err.Error(),
				}},
			}, nil
		}
		telemetry.SetSpanOK(span)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "trace://graph/stats",
				MIMEType: "application/json",
				Text:     result.Content,
			}},
		}, nil
	})
}
