<!-- Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
AGPL v3 - See LICENSE.txt and NOTICE.txt -->

# Aleutian Trace: Code Intelligence for Any LLM

Aleutian Trace gives any LLM deep understanding of a codebase. It parses source code into a call graph, indexes symbols, and exposes 24+ agent tools through an OpenAI-compatible API. Works with Ollama (local), Claude, Gemini, ChatGPT, or any OpenAI-compatible provider — no plugins or custom integrations required.

Any client that speaks the OpenAI protocol — Open WebUI, Continue.dev, Aider, Cline, curl — routes through Trace and gets structural code intelligence on top of whichever model you choose.

Ask "what are the callees of the main function?" and get an accurate, sourced answer from the actual code graph — not an LLM hallucination.

![gemma3n tracing the call chain from main() to template rendering in Hugo, with file:line citations](docs/demos/screenshots/call_chain_hugo_gemma3n.png)

*gemma3n:latest answering "What's the call chain from main() to template rendering?" against the Hugo codebase via Open WebUI. Every step is cited to the actual file and line — no hallucination.*

To try this yourself, run the demo script and Open WebUI starts automatically:

```bash
./docs/demos/run_demo.sh --phase trace --project ~/projects/hugo --model gemma3n
```

Or spin up Open WebUI manually pointed at the proxy:

```bash
podman run -d --name open-webui-baseline -p 3001:8080 \
  -e OPENAI_API_BASE_URL=http://host.containers.internal:12218/v1 \
  -e OPENAI_API_KEY=not-needed \
  ghcr.io/open-webui/open-webui:main
```

Once Open WebUI loads, go to **Admin Settings → Connections** and turn off the Ollama connection — otherwise Open WebUI will route some requests directly to Ollama and bypass Trace.

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
4. **Agent** — A multi-step reasoning loop selects the right tools (graph queries, semantic search, analytics) and synthesizes answers using whichever LLM you configure — Ollama, Claude, Gemini, ChatGPT, or any OpenAI-compatible provider
5. **Proxy** — Translates OpenAI `/v1/chat/completions` requests into agent loop calls, so any compatible client works out of the box

## Repository Structure

This repo contains the core trace service. Related repos:

- [**RagStack**](https://github.com/AleutianAI/RagStack) — Orchestrator, RAG engine, LLM adapters, embeddings, and the `aleutian` CLI. Provides document processing, embedding coordination, and retrieval pipelines that Trace relies on for semantic search.
- [**Observability**](https://github.com/AleutianAI/Observability) — Grafana dashboards, Prometheus configs, and OpenTelemetry Collector setup for monitoring the full Aleutian stack.

```
AleutianFOSS (this repo)          RagStack                    Observability
├── cmd/trace/                    ├── cmd/aleutian/            ├── compose/
├── cmd/trace-mcp/                ├── cmd/orchestrator/        ├── grafana/
├── cmd/trace-proxy/              ├── services/orchestrator/   ├── prometheus/
├── services/trace/               ├── services/rag_engine/     └── otel/
└── test/                         ├── services/llm/
                                  ├── services/embeddings/
                                  └── pkg/
```

## Quick Start

### Prerequisites

- [Podman](https://podman.io/)
- [Ollama](https://ollama.com/) running on the host with at least one model pulled (e.g., `ollama pull gemma3n`)
- Go 1.25+ (to build from source)

### 1. Clone and build

```bash
git clone https://github.com/AleutianAI/AleutianFOSS.git
cd AleutianFOSS
go build -o trace ./cmd/trace
go build -o trace-proxy ./cmd/trace-proxy
go build -o trace-mcp ./cmd/trace-mcp
```

### 2. Run

Point the demo script at a local codebase and a model:

```bash
./docs/demos/run_demo.sh --phase trace --project ~/projects/hugo --model gemma3n
```

The script builds the binaries if needed, starts the trace server and proxy natively, then launches Open WebUI via `podman run` pointed at the proxy. The proxy parses your project, builds the call graph, and indexes all symbols on first run.

To use the bundled sample project instead:

```bash
./docs/demos/run_demo.sh --phase trace
```

To run the full before/after comparison demo (Open WebUI direct vs. through Trace):

```bash
./docs/demos/run_demo.sh --phase both --project ~/projects/hugo --model gemma3n
```

To tear everything down:

```bash
./docs/demos/run_demo.sh --cleanup
```

### 3. Verify

```bash
# Check proxy is healthy
curl http://localhost:12218/health

# Ask a question via curl
curl -s http://localhost:12218/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma3n",
    "messages": [{"role": "user", "content": "What are the entry points in this project?"}]
  }'
```

### Manual startup (without the demo script)

Run the binaries directly if you want to manage the processes yourself:

```bash
# Terminal 1: start the trace server (port 12217)
./trace -with-context -with-tools

# Terminal 2: start the proxy (port 12218)
./trace-proxy \
  --project-root /path/to/your/project \
  --trace-url http://localhost:12217 \
  --ollama-url http://localhost:11434
```

Then start Open WebUI pointed at the proxy:

```bash
podman run -d --name open-webui -p 3001:8080 \
  -e OPENAI_API_BASE_URL=http://host.containers.internal:12218/v1 \
  -e OPENAI_API_KEY=not-needed \
  ghcr.io/open-webui/open-webui:main
```

Open `http://localhost:3001`, create an account, then go to Admin Settings > Connections and disable the Ollama API (so all traffic routes through Aleutian). Models appear automatically.

### 4. Connect other clients (optional)

The proxy is a drop-in OpenAI endpoint at `http://localhost:12218/v1`. Any tool with a configurable OpenAI base URL works without further setup.

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

**Cline (VS Code):**

In Cline settings, set **API Provider** to `OpenAI Compatible` and **Base URL** to `http://localhost:12218/v1`. Set the API key to any non-empty string.

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

### 5. Cloud providers (optional)

Trace defaults to Ollama for local inference, but can use cloud LLM providers instead. Set the provider and model for each role (main, router, param extractor) via environment variables.

Create a `.env` file with your API keys:

```bash
# .env (do NOT commit this file)
OPENAI_API_KEY=your-key-here
GEMINI_API_KEY=your-key-here
ANTHROPIC_API_KEY=your-key-here
```

**Gemini** (supports 2.5 and 3.x series, including `gemini-2.5-flash`, `gemini-3-flash-preview`, `gemini-3.1-pro-preview`, `gemini-3.1-flash-lite-preview`):

```bash
podman run -d --name aleutian-trace \
  --env-file .env \
  -e TRACE_MAIN_PROVIDER=gemini \
  -e TRACE_MAIN_MODEL=gemini-2.5-flash \
  -e TRACE_ROUTER_PROVIDER=gemini \
  -e TRACE_ROUTER_MODEL=gemini-2.5-flash \
  -e TRACE_PARAM_PROVIDER=gemini \
  -e TRACE_PARAM_MODEL=gemini-2.5-flash \
  -e TRACE_CONSENT_GEMINI=true \
  -e TRACE_CLASSIFY_DATA=false \
  -p 12217:12217 \
  aleutian-trace:latest
```

**OpenAI:**

```bash
podman run -d --name aleutian-trace \
  --env-file .env \
  -e TRACE_MAIN_PROVIDER=openai \
  -e TRACE_MAIN_MODEL=gpt-4o-mini \
  -e TRACE_ROUTER_PROVIDER=openai \
  -e TRACE_ROUTER_MODEL=gpt-4o-mini \
  -e TRACE_PARAM_PROVIDER=openai \
  -e TRACE_PARAM_MODEL=gpt-4o-mini \
  -e TRACE_CONSENT_OPENAI=true \
  -e TRACE_CLASSIFY_DATA=false \
  -p 12217:12217 \
  aleutian-trace:latest
```

**Anthropic (Claude):**

```bash
podman run -d --name aleutian-trace \
  --env-file .env \
  -e TRACE_MAIN_PROVIDER=anthropic \
  -e TRACE_MAIN_MODEL=claude-sonnet-4-6 \
  -e TRACE_ROUTER_PROVIDER=anthropic \
  -e TRACE_ROUTER_MODEL=claude-sonnet-4-6 \
  -e TRACE_PARAM_PROVIDER=anthropic \
  -e TRACE_PARAM_MODEL=claude-sonnet-4-6 \
  -e TRACE_CONSENT_ANTHROPIC=true \
  -e TRACE_CLASSIFY_DATA=false \
  -p 12217:12217 \
  aleutian-trace:latest
```

**OpenAI-Compatible Providers (Groq, DeepSeek, Mistral, vLLM, etc.):**

Any provider that implements the OpenAI chat completions API works via `OPENAI_BASE_URL`:

```bash
# Example: Groq (fast inference on open-weights models)
OPENAI_API_KEY=your-groq-key
OPENAI_BASE_URL=https://api.groq.com/openai/v1/chat/completions

# Example: DeepSeek
OPENAI_API_KEY=your-deepseek-key
OPENAI_BASE_URL=https://api.deepseek.com/v1/chat/completions

# Example: local vLLM server
OPENAI_API_KEY=not-needed
OPENAI_BASE_URL=http://host.containers.internal:8000/v1/chat/completions
```

```bash
podman run -d --name aleutian-trace \
  -e OPENAI_API_KEY=your-groq-key \
  -e OPENAI_BASE_URL=https://api.groq.com/openai/v1/chat/completions \
  -e TRACE_MAIN_PROVIDER=openai \
  -e TRACE_MAIN_MODEL=llama-3.3-70b-versatile \
  -e TRACE_ROUTER_PROVIDER=openai \
  -e TRACE_ROUTER_MODEL=llama-3.3-70b-versatile \
  -e TRACE_PARAM_PROVIDER=openai \
  -e TRACE_PARAM_MODEL=llama-3.3-70b-versatile \
  -e TRACE_CONSENT_OPENAI=true \
  -e TRACE_CLASSIFY_DATA=false \
  -p 12217:12217 \
  aleutian-trace:latest
```

**Environment variables:**

| Variable | Description |
|----------|-------------|
| `TRACE_MAIN_PROVIDER` | LLM provider for synthesis: `ollama`, `gemini`, `openai`, `anthropic` |
| `TRACE_MAIN_MODEL` | Model name for the main LLM |
| `TRACE_ROUTER_PROVIDER` | LLM provider for tool routing (can differ from main) |
| `TRACE_ROUTER_MODEL` | Model name for the router |
| `TRACE_PARAM_PROVIDER` | LLM provider for parameter extraction |
| `TRACE_PARAM_MODEL` | Model name for parameter extraction |
| `TRACE_CONSENT_<PROVIDER>` | Required consent flag (`true`) to send data to a cloud provider |
| `TRACE_CLASSIFY_DATA` | Set to `false` to skip data classification (required for cloud providers analyzing your own code) |
| `OPENAI_BASE_URL` | Override the OpenAI endpoint for compatible providers (Groq, DeepSeek, Mistral, vLLM, etc.) |

You can mix providers — for example, use Gemini for routing (fast, cheap) and Anthropic for synthesis (higher quality).

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
        │Weaviate │ │NATS │ │Orchestrator│  ← from RagStack repo
        │(vectors)│ │(CRS)│ │   :12210   │
        │  :12212 │ │:4222│ └──────┬─────┘
        └─────────┘ └─────┘        │
                            ┌──────▼──────┐
                            │  RAG Engine  │  ← from RagStack repo
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

| Service | Port | Repo | Description |
|---------|------|------|-------------|
| Trace Proxy | 12218 | AleutianFOSS | OpenAI-compatible API gateway |
| Trace Server | 12217 | AleutianFOSS | Code graph, agent loop, 24+ tools |
| Orchestrator | 12210 | RagStack | Embedding coordination, document processing |
| RAG Engine | 12211 | RagStack | Python retrieval and generation pipelines |
| Weaviate | 12212 | — | Vector database for symbol embeddings |
| NATS | 4222 | — | CRS delta streaming and state persistence |
| Jaeger | 12214 | Observability | Distributed tracing UI |
| Ollama | 11434 | — | Local LLM inference (runs on host) |

### Agent Tools

The trace agent has access to 24+ tools organized by category:

**Graph Queries** — find_callers, find_callees, find_implementations, find_symbol, get_call_chain, find_references

**Semantic Search** — semantic_search (vector similarity over indexed symbols), find_similar_symbols

**Analytics** — hotspots (most-connected nodes), cycles (Tarjan's SCC), important (PageRank), communities (Leiden algorithm), dead_code, shortest_path

**Exploration** — entry_points, data_flow, error_flow, config_usage, similar_code, minimal_context, summarize_file, summarize_package, change_impact

**Reasoning** — breaking_changes, simulate_change, validate_change, test_coverage, side_effects, suggest_refactor

## Observability

All services export OpenTelemetry traces. The [Observability](https://github.com/AleutianAI/Observability) repo provides pre-configured Grafana dashboards and Prometheus alerting rules for the full stack. View traces in Jaeger at `http://localhost:12214`.

Analyze trace performance from the command line:

```bash
# List recent traces
./scripts/trace_breakdown.sh --list

# Analyze a specific trace
./scripts/trace_breakdown.sh <trace_id>
```

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
