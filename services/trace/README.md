<!-- Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
AGPL v3 - See LICENSE.txt and NOTICE.txt -->

# Trace Service

The Trace service provides HTTP endpoints for code graph construction, symbol querying, graph analytics, agentic tool execution, and LLM context assembly.

## Quick Start

```go
service := trace.NewService(trace.DefaultServiceConfig())
handlers := trace.NewHandlers(service)

router := gin.Default()
v1 := router.Group("/v1")
trace.RegisterRoutes(v1, handlers)
router.Run(":8080")
```

```bash
# Initialize a code graph
curl -X POST http://localhost:8080/v1/trace/init \
  -H "Content-Type: application/json" \
  -d '{"project_root": "/path/to/project", "languages": ["go"]}'

# Query callers
curl "http://localhost:8080/v1/trace/callers?graph_id=<id>&function=HandleInit"
```

## API Reference

All endpoints are under `/v1/trace` unless noted otherwise.

### Graph Lifecycle

| Method | Path | Description |
|--------|------|-------------|
| POST | `/init` | Initialize a code graph from a project root |
| POST | `/context` | Assemble context for LLM prompts |
| POST | `/seed` | Seed library documentation |

### Symbol Queries

| Method | Path | Description |
|--------|------|-------------|
| GET | `/symbol/:id` | Get symbol by ID |
| GET | `/callers` | Find functions that call a given function |
| GET | `/callees` | Find functions called by a given function |
| GET | `/implementations` | Find types implementing an interface |
| GET | `/call-chain` | Find shortest call chain between two functions |
| GET | `/references` | Find all locations referencing a symbol |

#### GET /callers

Find all functions that call the given function.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `graph_id` | string | yes | Graph ID from `/init` |
| `function` | string | yes | Function name to search |
| `limit` | int | no | Max results (default 50) |

Response: `CallersResponse` with `function` and `callers` array of `SymbolInfo`.

#### GET /callees

Find all functions that the given function calls.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `graph_id` | string | yes | Graph ID from `/init` |
| `function` | string | yes | Function name to search |
| `limit` | int | no | Max results (default 50) |

Response: `CalleesResponse` with `function` and `callees` array of `SymbolInfo`.

#### GET /call-chain

Find the shortest call chain between two functions.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `graph_id` | string | yes | Graph ID from `/init` |
| `from` | string | yes | Source function name |
| `to` | string | yes | Target function name |

Response: `CallChainResponse` with `from`, `to`, `path` (array of `SymbolInfo`), and `length` (-1 if no path).

#### GET /references

Find all locations that reference the given symbol.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `graph_id` | string | yes | Graph ID from `/init` |
| `symbol` | string | yes | Symbol name to search |
| `limit` | int | no | Max results (default 50) |

Response: `ReferencesResponse` with `symbol` and `references` array of `ReferenceInfo` (file_path, line, column).

### Graph Analytics

All analytics endpoints accept JSON POST bodies and return `AgenticResponse` wrappers with `result` and `latency_ms`.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/analytics/hotspots` | Find most-connected nodes by degree |
| POST | `/analytics/cycles` | Find cyclic dependencies (Tarjan's SCC) |
| POST | `/analytics/important` | Find most important nodes (PageRank) |
| POST | `/analytics/communities` | Detect code communities (Leiden algorithm) |
| POST | `/analytics/path` | Find shortest path between two functions |

#### POST /analytics/hotspots

```json
{"graph_id": "<id>", "limit": 10}
```

Returns top-k nodes sorted by connectivity score (inDegree*2 + outDegree).

#### POST /analytics/cycles

```json
{"graph_id": "<id>"}
```

Returns cyclic dependencies found via Tarjan's strongly connected components algorithm. Each cycle includes node IDs, packages, and length.

#### POST /analytics/important

```json
{"graph_id": "<id>", "limit": 10}
```

Returns top-k nodes ranked by PageRank score. Each result includes the score and comparison degree-based rank.

#### POST /analytics/communities

```json
{"graph_id": "<id>"}
```

Returns community detection results from the Leiden algorithm, including communities, modularity score, iteration count, and convergence status.

#### POST /analytics/path

```json
{"graph_id": "<id>", "from": "funcA", "to": "funcB"}
```

Returns the shortest path between two functions with symbols along the path and hop count.

### Memory Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/memories` | List all memories |
| POST | `/memories` | Store a new memory |
| POST | `/memories/retrieve` | Semantic memory retrieval |
| DELETE | `/memories/:id` | Delete a memory |
| POST | `/memories/:id/validate` | Validate a memory |
| POST | `/memories/:id/contradict` | Contradict a memory |

### Agentic Tools

Tool discovery and 24 agentic tool endpoints organized by category.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tools` | Discover available tools |

#### Exploration (9 endpoints)

| POST | `/explore/entry_points` | Find entry points |
|------|------------------------|-------------------|
| POST | `/explore/data_flow` | Trace data flow |
| POST | `/explore/error_flow` | Trace error flow |
| POST | `/explore/config_usage` | Find config usages |
| POST | `/explore/similar_code` | Find similar code |
| POST | `/explore/minimal_context` | Build minimal context |
| POST | `/explore/summarize_file` | Summarize a file |
| POST | `/explore/summarize_package` | Summarize a package |
| POST | `/explore/change_impact` | Analyze change impact |

#### Reasoning (6 endpoints)

| POST | `/reason/breaking_changes` | Check breaking changes |
|------|---------------------------|----------------------|
| POST | `/reason/simulate_change` | Simulate a change |
| POST | `/reason/validate_change` | Validate code syntax |
| POST | `/reason/test_coverage` | Find test coverage |
| POST | `/reason/side_effects` | Detect side effects |
| POST | `/reason/suggest_refactor` | Suggest refactoring |

#### Coordination (3 endpoints)

| POST | `/coordinate/plan_changes` | Plan multi-file changes |
|------|---------------------------|------------------------|
| POST | `/coordinate/validate_plan` | Validate a change plan |
| POST | `/coordinate/preview_changes` | Preview changes as diffs |

#### Patterns (6 endpoints)

| POST | `/patterns/detect` | Detect design patterns |
|------|-------------------|----------------------|
| POST | `/patterns/code_smells` | Find code smells |
| POST | `/patterns/duplication` | Find duplicate code |
| POST | `/patterns/circular_deps` | Find circular dependencies |
| POST | `/patterns/conventions` | Extract conventions |
| POST | `/patterns/dead_code` | Find dead code |

### Agent Loop

Registered separately via `RegisterAgentRoutes()`.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/agent/run` | Start a new agent session |
| POST | `/agent/continue` | Continue from CLARIFY state |
| POST | `/agent/abort` | Abort an active session |
| GET | `/agent/:id` | Get session state |
| GET | `/agent/:id/reasoning` | Get reasoning trace |
| GET | `/agent/:id/crs` | Get CRS state export |

### Debug

| Method | Path | Description |
|--------|------|-------------|
| GET | `/debug/graph/stats` | Graph statistics |
| GET | `/debug/cache` | Cache statistics |
| GET | `/debug/graph/inspect` | Inspect a graph node |
| GET | `/debug/graph/export` | Export graph data |
| POST | `/debug/graph/snapshot` | Save graph snapshot |
| GET | `/debug/graph/snapshots` | List snapshots |
| GET | `/debug/graph/snapshot/:id` | Load a snapshot |
| DELETE | `/debug/graph/snapshot/:id` | Delete a snapshot |
| GET | `/debug/graph/snapshot/diff` | Diff two snapshots |

### Health & Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/ready` | Readiness check |
| GET | `/v1/metrics` | Prometheus metrics (not under /trace) |

## Error Handling

All endpoints return errors as JSON:

```json
{
  "error": "human-readable message",
  "code": "MACHINE_READABLE_CODE",
  "details": "optional additional context"
}
```

Common error codes:

| Code | HTTP Status | Meaning |
|------|-------------|---------|
| `INVALID_REQUEST` | 400 | Missing or invalid parameters |
| `GRAPH_NOT_INITIALIZED` | 400 | Graph not found, expired, nil, or not frozen |
| `SYMBOL_NOT_FOUND` | 400 | Named function/symbol not found in graph |
| `QUERY_FAILED` | 500 | Internal query error |
| `INTERNAL_ERROR` | 500 | Unexpected server error |

## MCP Server (Claude Code / Cursor / Windsurf)

The `trace-mcp` binary exposes all trace tools via the Model Context Protocol over stdio. AI assistants call tools through MCP; the server delegates to the trace service over HTTP and exports OTel traces + Prometheus metrics for every tool call.

### Step 1: Build the MCP server

```bash
go build -o trace-mcp ./cmd/trace-mcp
```

Or install it to your `$GOPATH/bin`:

```bash
go install ./cmd/trace-mcp
```

### Step 2: Start the stack

The trace server, Jaeger, and Prometheus must be running to receive tool calls and telemetry:

```bash
aleutian stack start
```

This starts:
- Trace server on `:12217`
- Jaeger UI on `:12214` (OTLP receiver on `:4317`)
- Prometheus on `:12215`

### Step 3: Configure your AI assistant

**Claude Code** — add to `.claude/mcp.json` in your project root:

```json
{
  "mcpServers": {
    "aleutian-trace": {
      "command": "trace-mcp",
      "args": ["-trace-url", "http://localhost:12217"]
    }
  }
}
```

**Cursor / Windsurf** — add the equivalent MCP server config per their docs, pointing to the `trace-mcp` binary with the same args.

If `trace-mcp` is not on your `$PATH`, use the full path to the binary.

### Step 4: Restart your AI assistant

Claude Code: restart the session so it picks up the new MCP config. The 13 trace tools will appear in the tool list.

### Step 5: Use trace tools

Ask your assistant to analyze a codebase:

> "Initialize the trace graph for this project, then find the callers of HandleInit"

The assistant will call `trace_init_project` followed by `trace_find_callers` through MCP.

### Step 6: View traces in Jaeger

Open [http://localhost:12214](http://localhost:12214) and select service **aleutian-trace-mcp**. Each tool call appears as a trace with:

- **`mcp.tool.<name>`** span — the MCP handler (tool name, input parameters)
- **`bridge.CallTool`** span — the HTTP request to the trace server (method, URL, status code, result size, truncation)
- **Trace server spans** — linked via W3C TraceContext headers (graph query execution)

### Available tools

| Tool | Description |
|------|-------------|
| `trace_init_project` | Initialize a code graph (call first) |
| `trace_find_callers` | Find functions that call a given function |
| `trace_find_callees` | Find functions called by a given function |
| `trace_find_implementations` | Find types implementing an interface |
| `trace_find_symbol` | Look up a symbol by name |
| `trace_get_call_chain` | Find shortest call chain between two functions |
| `trace_find_references` | Find all references to a symbol |
| `trace_find_hotspots` | Find most-connected nodes (high degree) |
| `trace_find_dead_code` | Find unreachable/unused code |
| `trace_find_cycles` | Find cyclic dependencies |
| `trace_find_important` | Find architecturally significant nodes (PageRank) |
| `trace_find_communities` | Detect code communities (Leiden) |
| `trace_find_path` | Find shortest path between two functions |

### Metrics (Prometheus)

Available at `http://localhost:12215` after tool calls:

| Metric | Type | Labels |
|--------|------|--------|
| `mcp_tool_calls_total` | Counter | `tool`, `status` |
| `mcp_tool_call_duration_seconds` | Histogram | `tool`, `method` |
| `mcp_tool_result_bytes` | Histogram | `tool` |
| `mcp_tool_truncations_total` | Counter | `tool` |
| `mcp_tool_errors_total` | Counter | `tool`, `error_type` |

### Troubleshooting

- **Tools not showing up** — verify `trace-mcp` is on your `$PATH` or use the full path in the MCP config. Restart the assistant after config changes.
- **"Trace server not reachable"** — run `aleutian stack start`. The trace server must be running on `:12217`.
- **No traces in Jaeger** — Jaeger must be running (`:4317` for OTLP). The MCP server uses `AllowDegraded=true` so it works without Jaeger, but traces are silently dropped.
- **Custom trace URL** — use `-trace-url` flag or set `ALEUTIAN_TRACE_URL` env var.

## OpenAI-Compatible Proxy (Open WebUI / Continue.dev / Cline / Aider)

The `trace-proxy` exposes an OpenAI-compatible `/v1/chat/completions` API that translates chat requests into trace agent loop calls. Any tool that speaks the OpenAI protocol gets full CRS reasoning + all 24+ agent tools transparently — no plugins, no custom integrations.

The proxy does **not** do its own tool loop. It delegates to the trace server's agent loop (`/v1/trace/agent/run` and `/continue`), which handles CRS disambiguation, tool selection, multi-step reasoning, and response synthesis. The proxy is a protocol translator.

### Quick Start (Compose Stack)

The proxy runs automatically with the stack. Point `TRACE_PROJECTS_DIR` at your codebase:

```bash
# Start the full stack — proxy included on :12218
TRACE_PROJECTS_DIR=/path/to/your/repo aleutian stack start --build

# Verify everything is running
curl http://localhost:12218/health
```

On startup, the proxy auto-initializes the code graph: it parses every source file under the project root, builds the call graph, and indexes all symbols into Weaviate for semantic search. This happens in the background — you can start querying immediately, though results improve once indexing completes.

For a typical project (~1400 files, ~50K symbols), auto-init takes 2-3 minutes. You can monitor progress in the proxy logs:

```bash
podman logs -f aleutian-trace-proxy
```

Look for:
```
CRS-26l: Auto-init completed, symbol indexing triggered in background
CRS-26i: Symbol indexing complete: indexed 33694 symbols
```

### How Code Indexing Works

When the proxy starts with `--project-root` (or `TRACE_PROJECTS_DIR` in compose), it triggers this sequence automatically:

1. **Graph construction** — the trace server parses all source files (Go, Python, JS/TS, Java, Rust, C/C++) using tree-sitter ASTs, extracts symbols (functions, methods, types, interfaces), and builds a call graph with edges for calls, implementations, and references.

2. **Symbol indexing** — all extracted symbols are embedded via the orchestrator's `/v1/embed` endpoint (using `nomic-embed-text-v2-moe` on Ollama) and stored in Weaviate. This enables semantic search — "find code related to authentication" works even when no symbol contains the word "authentication."

3. **Ready to query** — once the graph is built and symbols are indexed, every question you ask through the proxy has access to: structural graph queries (callers, callees, call chains, implementations, references), semantic vector search (conceptual similarity), graph analytics (hotspots, cycles, PageRank, communities, dead code), and 24+ agent tools.

You do not need to manually call `/init`. The proxy handles it. If you switch projects, either restart the stack with a different `TRACE_PROJECTS_DIR` or send an `X-Project-Root` header per-request.

### Connect Your Tools

After `aleutian stack start`, point any OpenAI-compatible tool at `http://localhost:12218`:

**Open WebUI** (recommended for chat UI):

```bash
podman run -d --name open-webui -p 3001:8080 \
  -e OPENAI_API_BASE_URL=http://host.containers.internal:12218/v1 \
  -e OPENAI_API_KEY=not-needed \
  ghcr.io/open-webui/open-webui:main
```

Then open `http://localhost:3001`. Models from Ollama appear automatically. Every question you ask goes through the trace agent with full code intelligence.

**Continue.dev** (VS Code / JetBrains):

In `~/.continue/config.json`:
```json
{
  "models": [{
    "title": "Aleutian Trace",
    "provider": "openai",
    "model": "gemma3n",
    "apiBase": "http://localhost:12218/v1",
    "apiKey": "not-needed"
  }]
}
```

**Aider**:

```bash
aider --openai-api-base http://localhost:12218/v1 --openai-api-key not-needed
```

**Cline**:

Set the API base URL to `http://localhost:12218/v1` in Cline settings. No API key required.

**curl / scripts**:

```bash
curl -s http://localhost:12218/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma3n",
    "messages": [{"role": "user", "content": "What are the callees of the main function?"}]
  }'
```

**Any OpenAI SDK** (Python, Node, etc.):

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:12218/v1", api_key="not-needed")
response = client.chat.completions.create(
    model="gemma3n",
    messages=[{"role": "user", "content": "Find dead code in this project"}]
)
print(response.choices[0].message.content)
```

### Why This Works

Without Aleutian, a local LLM has no knowledge of your codebase. Ask "what are the callees of the main function?" and it will hallucinate or refuse. With Aleutian, the same LLM gets:

- A parsed call graph with every function, method, and type relationship
- Semantic search over 50K+ indexed symbols
- 24+ agent tools for structural and analytical queries
- Multi-step CRS reasoning that selects the right tools automatically

The LLM becomes a code intelligence interface, not just a chat model.

### Running the Proxy Standalone

If you prefer to run the proxy outside the compose stack (e.g., for development):

```bash
# Build the proxy
go build -o trace-proxy ./cmd/trace-proxy

# Start the trace server stack (without proxy)
aleutian stack start

# Run proxy on host, pointed at your project
./trace-proxy --project-root /path/to/your/repo
```

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--listen-addr` | — | `:12218` | Proxy listen address |
| `--trace-url` | `ALEUTIAN_TRACE_URL` | `http://localhost:12217` | Trace server URL |
| `--ollama-url` | `OLLAMA_URL` | `http://localhost:11434` | Ollama URL (for `/v1/models`) |
| `--project-root` | — | cwd | Default project root |
| `--timeout` | — | `5m` | Agent run timeout |
| `--host-prefix` | `TRACE_HOST_PREFIX` | — | Host path prefix for container path translation |
| `--container-prefix` | `TRACE_CONTAINER_PREFIX` | — | Container mount point for path translation |

### Proxy Endpoints

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | Main proxy — translates to agent loop, returns OpenAI format |
| `GET /v1/models` | Lists models from Ollama in OpenAI format |
| `GET /health` | Combined health: proxy + trace server + Ollama |
| `POST /init` | Initialize a project's code graph via the trace server |

### Session Management

The proxy correlates OpenAI conversations to agent sessions automatically. It hashes the first user message to generate a stable thread key — same conversation thread reuses the same agent session via `/continue`. Sessions expire after 1 hour of inactivity.

### Streaming

When `stream: true`, the proxy buffers the full agent response and emits it as a single SSE chunk followed by `[DONE]`. This satisfies client libraries that require SSE format. Real token-level streaming during agent execution is planned for a future release.

### Metrics (Prometheus)

| Metric | Type | Labels |
|--------|------|--------|
| `proxy_requests_total` | Counter | `state`, `session_reused` |
| `proxy_request_duration_seconds` | Histogram | `state` |

### Troubleshooting

- **"project_root required"** — set `TRACE_PROJECTS_DIR` env var before `aleutian stack start`, or send `X-Project-Root` header per request.
- **"agent loop error"** — the trace server is not reachable. Run `aleutian stack start` and check `curl http://localhost:12217/v1/trace/health`.
- **"failed to reach Ollama"** on `/v1/models` — Ollama is not running on the host. Start it with `ollama serve`. The proxy works without Ollama for the models endpoint; chat still works if the trace server can reach Ollama.
- **Slow first query** — auto-init may still be indexing symbols. Check `podman logs aleutian-trace-proxy` for progress. Subsequent queries are faster.
- **Slow responses** — LLM inference dominates response time. On an M4 Max, expect ~15-20s warm; on an RTX 5090, ~5-10s. Check Jaeger (`http://localhost:12214`, service `aleutian-trace-proxy`) for trace breakdowns.
- **Switching projects** — restart the stack with a different `TRACE_PROJECTS_DIR`, or use the `X-Project-Root` header for per-request overrides.

## Architecture

```
services/trace/
  service.go              Service struct, graph lifecycle, query methods
  handlers.go             Core HTTP handlers (init, context, callers, etc.)
  graph_query_handlers.go Graph query + analytics HTTP handlers (CB-00.0)
  agentic_handlers.go     Agentic tool HTTP handlers (CB-22b)
  routes.go               Route registration
  types.go                Request/response types, SymbolInfo, ErrorResponse
  graph/                  Code graph, analytics, PageRank, community detection
  ast/                    AST parsing (Go, Python, JS/TS)
  index/                  Symbol index with O(1) lookup
  agent/                  Agent loop with CRS integration
  cli/tools/              Tool implementations for agent loop
  context/                LLM context assembly
  telemetry/              OpenTelemetry setup (traces, metrics, logs)
```
