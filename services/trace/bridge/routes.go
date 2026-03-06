// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package bridge

// toolRoute describes how to map an MCP tool name to an HTTP request
// against the Trace service.
//
// Description:
//
//	Each toolRoute specifies the HTTP method, path, and parameter mapping
//	for one trace tool. GET tools map MCP param names to query string params.
//	POST tools pass params as JSON body fields alongside graph_id.
//
// Thread Safety: toolRoute is read-only after init; safe for concurrent use.
type toolRoute struct {
	// Method is the HTTP method ("GET" or "POST").
	Method string

	// Path is the URL path relative to the trace base URL (e.g., "/v1/trace/callers").
	Path string

	// ParamMap maps MCP parameter names to HTTP parameter names.
	// For GET requests, these become query string parameters.
	// For POST requests with non-empty ParamMap, mapped params go into the JSON body.
	ParamMap map[string]string
}

// toolRoutes maps MCP tool names to their HTTP route definitions.
//
// Description:
//
//	This routing table defines all 13 trace tools exposed via MCP.
//	GET tools use query parameters; POST tools use JSON bodies.
//	The graph_id is auto-injected by ToolBridge and should not appear here.
//
// Assumptions:
//
//	All paths are relative to the configured TraceURL.
//	The trace server implements all listed endpoints per CB-00.0.
var toolRoutes = map[string]toolRoute{
	"trace_init_project": {
		Method:   "POST",
		Path:     "/v1/trace/init",
		ParamMap: map[string]string{"project_root": "project_root"},
	},
	"trace_find_callers": {
		Method:   "GET",
		Path:     "/v1/trace/callers",
		ParamMap: map[string]string{"function_name": "function"},
	},
	"trace_find_callees": {
		Method:   "GET",
		Path:     "/v1/trace/callees",
		ParamMap: map[string]string{"function_name": "function"},
	},
	"trace_find_implementations": {
		Method:   "GET",
		Path:     "/v1/trace/implementations",
		ParamMap: map[string]string{"interface_name": "interface"},
	},
	"trace_find_symbol": {
		Method:   "GET",
		Path:     "/v1/trace/debug/graph/inspect",
		ParamMap: map[string]string{"name": "name"},
	},
	"trace_get_call_chain": {
		Method:   "GET",
		Path:     "/v1/trace/call-chain",
		ParamMap: map[string]string{"from": "from", "to": "to"},
	},
	"trace_find_references": {
		Method:   "GET",
		Path:     "/v1/trace/references",
		ParamMap: map[string]string{"symbol_name": "symbol"},
	},
	"trace_find_hotspots": {
		Method:   "POST",
		Path:     "/v1/trace/analytics/hotspots",
		ParamMap: map[string]string{"limit": "limit"},
	},
	"trace_find_dead_code": {
		Method:   "POST",
		Path:     "/v1/trace/patterns/dead_code",
		ParamMap: nil,
	},
	"trace_find_cycles": {
		Method:   "POST",
		Path:     "/v1/trace/analytics/cycles",
		ParamMap: nil,
	},
	"trace_find_important": {
		Method:   "POST",
		Path:     "/v1/trace/analytics/important",
		ParamMap: map[string]string{"limit": "limit"},
	},
	"trace_find_communities": {
		Method:   "POST",
		Path:     "/v1/trace/analytics/communities",
		ParamMap: nil,
	},
	"trace_find_path": {
		Method:   "POST",
		Path:     "/v1/trace/analytics/path",
		ParamMap: map[string]string{"from": "from", "to": "to"},
	},
}
