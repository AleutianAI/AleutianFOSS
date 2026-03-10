<!-- Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
AGPL v3 - See LICENSE.txt and NOTICE.txt -->

# Aleutian: Code Intelligence for Local LLMs

Aleutian gives your local LLM deep understanding of any codebase. It parses source code into a call graph, indexes symbols into a vector database, and exposes 24+ agent tools through an OpenAI-compatible API. Any tool that speaks the OpenAI protocol — Open WebUI, Continue.dev, Aider, Cline, curl — gets structural code intelligence without plugins or custom integrations.

Ask "what are the callees of the main function?" and get an accurate, sourced answer from the actual code graph — not an LLM hallucination.

## How It Works

```
Your Code → [Tree-sitter AST] → [Call Graph + Symbol Index] → [Weaviate Vector DB]
                                                                      ↓
Open WebUI / Continue / Aider → [Trace Proxy :12218] → [Agent Loop] → [24+ Tools]
                                    OpenAI API            CRS Reasoning    ↓
                                                                    [LLM Synthesis]
```

1. **Parse** — Tree-sitter extracts every function, method, type, and interface from your source code (Go, Python, JS/TS, Java, Rust, C/C++)
2. **Graph** — Builds a call graph with edges for calls, implementations, and references
3. **Index** — Embeds all symbols into Weaviate for semantic search ("find code related to authentication")
4. **Agent** — A multi-step reasoning loop selects the right tools (graph queries, semantic search, analytics) and synthesizes answers using your local LLM
5. **Proxy** — Translates OpenAI `/v1/chat/completions` requests into agent loop calls, so any compatible client works out of the box

## Quick Start

### Prerequisites

- [Podman](https://podman.io/) and [podman-compose](https://github.com/containers/podman-compose)
- [Ollama](https://ollama.com/) running on the host with at least one model pulled (e.g., `ollama pull gemma3n`)
- Go 1.25+ (to build from source)

### 1. Build the CLI

```bash
git clone https://github.com/AleutianAI/AleutianFOSS.git
cd AleutianFOSS
go build -o aleutian ./cmd/aleutian
```

### 2. Start the stack

Point `TRACE_PROJECTS_DIR` at the codebase you want to analyze:

```bash
TRACE_PROJECTS_DIR=/path/to/your/project ./aleutian stack start --build
```

This starts all services: trace server, trace proxy, orchestrator, Weaviate, NATS, and Jaeger. The proxy automatically parses your code, builds the call graph, and indexes all symbols on startup.

### 3. Verify

```bash
# Check all services are healthy
curl http://localhost:12218/health

# Ask a question via curl
curl -s http://localhost:12218/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma3n",
    "messages": [{"role": "user", "content": "What are the entry points in this project?"}]
  }'
```

### 4. Connect a UI (optional)

**Open WebUI:**

```bash
podman run -d --name open-webui -p 3001:8080 \
  -e OPENAI_API_BASE_URL=http://host.containers.internal:12218/v1 \
  -e OPENAI_API_KEY=not-needed \
  ghcr.io/open-webui/open-webui:main
```

Open `http://localhost:3001`, create an account, then go to Admin Settings > Connections and disable the Ollama API (so all traffic routes through Aleutian). Models appear automatically.

**Continue.dev (VS Code / JetBrains):**

In `~/.continue/config.json`:
```json
{
  "models": [{
    "title": "Aleutian",
    "provider": "openai",
    "model": "gemma3n",
    "apiBase": "http://localhost:12218/v1",
    "apiKey": "not-needed"
  }]
}
```

**Aider:**

```bash
aider --openai-api-base http://localhost:12218/v1 --openai-api-key not-needed
```

**Any OpenAI SDK:**

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:12218/v1", api_key="not-needed")
response = client.chat.completions.create(
    model="gemma3n",
    messages=[{"role": "user", "content": "Find dead code in this project"}]
)
print(response.choices[0].message.content)
```

## MCP Server (Claude Code / Cursor / Windsurf)

For AI coding assistants that support MCP, a dedicated `trace-mcp` binary exposes all trace tools over the Model Context Protocol:

```bash
go build -o trace-mcp ./cmd/trace-mcp
```

Add to `.claude/mcp.json` in your project root:

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

See [services/trace/README.md](services/trace/README.md) for the full MCP tool list and configuration details.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Clients: Open WebUI, Continue, Aider, curl, MCP        │
└──────────────────────┬──────────────────────────────────┘
                       │ OpenAI API / MCP
              ┌────────▼────────┐
              │  Trace Proxy    │ :12218  ← OpenAI-compatible gateway
              │  (trace-proxy)  │
              └────────┬────────┘
                       │ HTTP
              ┌────────▼────────┐
              │  Trace Server   │ :12217  ← Agent loop, CRS, 24+ tools
              │  (trace)        │
              └──┬─────┬────┬───┘
                 │     │    │
        ┌────────▼┐ ┌──▼──┐ ┌▼──────────┐
        │Weaviate │ │NATS │ │Orchestrator│
        │(vectors)│ │(CRS)│ │(embeddings)│
        │  :12212 │ │:4222│ │   :12210   │
        └─────────┘ └─────┘ └──────┬─────┘
                                   │
                            ┌──────▼──────┐
                            │  RAG Engine  │
                            │  (Python)    │
                            │    :12211    │
                            └─────────────┘
                                   │
                            ┌──────▼──────┐
                            │   Ollama     │  ← Runs on host
                            │  (LLM +     │
                            │  embeddings) │
                            │   :11434     │
                            └─────────────┘
```

### Services

| Service | Port | Description |
|---------|------|-------------|
| Trace Proxy | 12218 | OpenAI-compatible API gateway |
| Trace Server | 12217 | Code graph, agent loop, 24+ tools |
| Orchestrator | 12210 | Embedding coordination, document processing |
| RAG Engine | 12211 | Python retrieval and generation pipelines |
| Weaviate | 12212 | Vector database for symbol embeddings |
| NATS | 4222 | CRS delta streaming and state persistence |
| Jaeger | 12214 | Distributed tracing UI |
| Ollama | 11434 | Local LLM inference (runs on host) |

### Agent Tools

The trace agent has access to 24+ tools organized by category:

**Graph Queries** — find_callers, find_callees, find_implementations, find_symbol, get_call_chain, find_references

**Semantic Search** — semantic_search (vector similarity over indexed symbols), find_similar_symbols

**Analytics** — hotspots (most-connected nodes), cycles (Tarjan's SCC), important (PageRank), communities (Leiden algorithm), dead_code, shortest_path

**Exploration** — entry_points, data_flow, error_flow, config_usage, similar_code, minimal_context, summarize_file, summarize_package, change_impact

**Reasoning** — breaking_changes, simulate_change, validate_change, test_coverage, side_effects, suggest_refactor

## Observability

All services export OpenTelemetry traces. View them in Jaeger at `http://localhost:12214`.

Analyze trace performance from the command line:

```bash
# List recent traces
./scripts/trace_breakdown.sh --list

# Analyze a specific trace
./scripts/trace_breakdown.sh <trace_id>
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `aleutian stack start` | Start all services |
| `aleutian stack start --build` | Rebuild images and start |
| `aleutian stack stop` | Stop all services |
| `aleutian stack status` | Show health and resource usage |
| `aleutian stack logs [service]` | Stream container logs |
| `aleutian stack destroy` | Remove all containers and data |
| `aleutian health` | Check service health |

## System Requirements

- **macOS:** Apple Silicon M1+ recommended (Metal acceleration for Ollama)
- **Linux:** Modern kernel for Podman; NVIDIA GPU optional but beneficial
- **RAM:** 16GB minimum (12B models), 32GB+ recommended (20B+ models)
- **Disk:** 20GB+ free space (excluding model weights)

Performance scales with memory bandwidth. LLM inference dominates response time:
- M4 Max (~546 GB/s): ~15-20s per query
- RTX 5090 (~1.8 TB/s): ~5-10s per query

## License

This project is licensed under the GNU Affero General Public License v3.0 — see [LICENSE.txt](LICENSE.txt) for details.

Additional terms in [NOTICE.txt](NOTICE.txt) regarding AI system attribution under AGPLv3 Section 7.
