# Aleutian Trace v1.0 Release Notes

**Date:** March 2026
**License:** AGPL v3

---

## What This Is

Aleutian Trace is a structural code intelligence layer for LLM agents. It builds a semantic code graph of your project and exposes it as tools that any model -- local or cloud -- can call through an OpenAI-compatible API or MCP server.

One command starts everything:

```bash
aleutian stack start
```

---

## Core Capabilities

### Semantic Code Graph

Tree-sitter parsing builds a typed, indexed call graph across Go, Python, JavaScript, and TypeScript. For pandas (250K LOC): 52,000 nodes, 292,000 edges. Every function, class, method, call relationship, type hierarchy, and import is indexed and queryable.

LSP enrichment via Pyright and typescript-language-server resolves cross-module call targets that tree-sitter alone misses. Coverage: Go ~98%, Python ~95% (with LSP), TypeScript ~98% (with LSP), JavaScript ~52%.

### 37 Analysis Tools

| Category | Tools | What They Do |
|----------|-------|-------------|
| **Graph Query** (6) | find_callers, find_callees, find_implementations, find_symbol, get_call_chain, find_references | Navigate relationships in the call graph |
| **Source Navigation** (4) | read_symbol, read_file, get_signature, list_symbols_in_file | Read source code and signatures from the index |
| **Structural Analysis** (10) | find_hotspots, find_dead_code, find_cycles, find_important, find_communities, find_articulation_points, find_weighted_criticality, find_path, find_circular_deps, find_common_dependency | PageRank, Tarjan SCC, Leiden clustering, dominator trees, reachability analysis |
| **Control Flow** (6) | find_dominators, find_loops, find_merge_points, find_control_dependencies, find_extractable_regions, check_reducibility | Dominator analysis, natural loop detection, SESE region extraction |
| **HLD Path Queries** (3) | find_critical_path, find_module_api, find_config_usage | Heavy-Light Decomposition for O(log^2 V) path queries |
| **Code Exploration** (5) | graph_overview, explore_package, list_packages, summarize_file, find_entry_points | Navigate and understand project structure |
| **Data Flow** (3) | trace_data_flow, trace_error_flow, build_minimal_context | Trace how data moves through the codebase |

### Code Reasoning State (CRS)

Six-index persistent memory architecture for stateful agent sessions:

| Index | Purpose |
|-------|---------|
| **Proof** | Verified/disproven claims with hard/soft signal boundary |
| **Constraint** | CDCL-learned failure conditions |
| **Similarity** | MinHash code signatures for approximate matching |
| **Dependency** | Call graph with O(1) secondary indexes |
| **History** | Tool call traces and session continuity |
| **Streaming** | HyperLogLog cardinality, Count-Min frequency |

Snapshot/delta model: algorithms never mutate CRS directly. Atomic updates with generation counter. Full persistence to BadgerDB between sessions.

### Tool Routing Pipeline

Four-layer routing narrows 37 tools to 3-8 candidates before the LLM selects:

1. **Routing Encyclopedia** -- deterministic pattern matching (<1ms)
2. **Prefilter** -- embedding similarity + confusion pair boosts (~5ms)
3. **Micro-LLM Router** -- 2B model classification (50-100ms)
4. **Escalation Router** -- larger model fallback for ambiguous queries

UCB1 exploration/exploitation balances known-good tools with discovery. Negation detection catches inverted queries ("no callers" → find_dead_code, not find_callers).

### Dual Persistence

| Backend | Format | Use Case | Restart Time |
|---------|--------|----------|-------------|
| **BadgerDB** | gzip JSON + SHA256 integrity | CRS indexes, snapshot history, source of truth | ~1-2 seconds |
| **bbolt** | snappy gob, B+ tree | Fast graph cache, single file per project | ~200ms target |

Staleness detection via file modification times. Incremental refresh updates only changed files.

### Observability (Ships with the Stack)

| Service | Port | Purpose |
|---------|------|---------|
| **Jaeger** | 12214 | Distributed traces -- every query, every routing decision |
| **Prometheus** | 12215 | 18 metric types: latency histograms, counters, gauges |
| **Grafana** | 12216 | 5 pre-built dashboards (MCTS, DAG, Grounding, A/B testing) |

OpenTelemetry instrumentation throughout. 15+ named tracers. Every routing decision recorded with confidence scores, candidate counts, UCB1 state, and circuit breaker status.

### MCP Server

```json
{
  "mcpServers": {
    "aleutian-trace": {
      "command": "./trace-mcp",
      "args": ["--project", "/path/to/your/project"]
    }
  }
}
```

All 37 tools exposed via Model Context Protocol. Works with Claude Code, Cursor, Windsurf, and any MCP-compatible client.

### OpenAI-Compatible Proxy

The trace-proxy service speaks the OpenAI tool-calling protocol. Any client that calls the OpenAI API can use the graph tools without modification.

---

## Languages Supported

| Language | Parser | LSP Enrichment | Edge Coverage |
|----------|--------|----------------|---------------|
| Go | tree-sitter-go | -- | ~98% |
| Python | tree-sitter-python | Pyright | ~95% |
| TypeScript | tree-sitter-typescript | typescript-language-server | ~98% |
| JavaScript | tree-sitter-javascript | typescript-language-server | ~52% |

Additional tree-sitter parsers: Bash, SQL, HTML, CSS, YAML, Markdown, Dockerfile.

---

## Test Results

296 integration tests across 9 open-source projects:

| Language | Projects |
|----------|----------|
| Go | badger, gin, hugo |
| Python | flask, pandas |
| JS/TS | babylonjs, express, nestjs, plottable |

| Tool Category | Accuracy (EXACT/EXCELLENT) |
|---------------|---------------------------|
| find_symbol | 96% |
| find_cycles | 93% |
| find_implementations | 92% |
| find_references | 92% |
| find_callers | 75% |
| find_callees | 72% |
| get_call_chain | 37% |
| find_path | 15% |

Top 4 tools exceed 90%. Weakest tools have a known bottleneck: natural language → symbol name resolution (parameter extraction), not graph accuracy.

---

## Graph Algorithms

| Algorithm | Tool | Complexity | Source |
|-----------|------|-----------|--------|
| PageRank | find_important | O(V + E) per iteration | Brin & Page (1998) |
| Tarjan's SCC | find_cycles | O(V + E) | Tarjan (1972) |
| Leiden | find_communities | O(E log V) | Traag et al. (2019) |
| Cooper-Harvey-Kennedy | find_dominators | O(V + E) | Cooper et al. (2001) |
| Heavy-Light Decomposition | find_critical_path | O(log^2 V) query | Sleator & Tarjan (1983) |
| Reachability | find_dead_code | O(V + E) | BFS from entry points |
| BFS | find_path | O(V + E) | Shortest path |
| Articulation points | find_articulation_points | O(V + E) | Hopcroft & Tarjan (1973) |

---

## Architecture

```
  User Query
      │
      ▼
  [Trace Proxy]  ─── OpenAI-compatible API ───  Any LLM client
      │
      ▼
  [Routing Pipeline]
      ├── Routing Encyclopedia (deterministic, <1ms)
      ├── Prefilter (embedding + confusion pairs, ~5ms)
      ├── Micro-LLM Router (2B params, 50-100ms)
      └── Escalation Router (fallback)
      │
      ▼
  [Tool Execution]
      ├── SymbolIndex (O(1) name lookup)
      ├── Graph (52K nodes, 292K edges)
      ├── Secondary indexes (name, kind, type, file)
      └── LSP enrichment (Pyright, TS)
      │
      ▼
  [CRS]
      ├── 6 indexes (proof, constraint, similarity, dependency, history, streaming)
      ├── Snapshot/delta model (atomic, concurrent readers)
      └── BadgerDB + bbolt persistence
      │
      ▼
  [Grounding Pipeline]
      ├── 12 checkers (6 hard, 6 soft)
      └── Hard signal override (prevents hallucination loops)
      │
      ▼
  Verified Answer with file:line citations
```

---

## Getting Started

```bash
# Clone and build
git clone https://github.com/aleutian-foss/aleutian
cd aleutian && go build ./...

# Start the full stack (first run pulls images)
aleutian stack start

# Point at a project
export TRACE_PROJECTS_DIR=/path/to/your/projects

# Query via API
curl localhost:8080/v1/trace/query \
  -d '{"query": "Who calls read_csv?"}'

# Or use the MCP server with Claude Code / Cursor
```

---

## Known Limitations

- **Untyped JavaScript edge coverage is low (~52%).** Runtime prototype injection (`Object.setPrototypeOf`) and dynamic dispatch create relationships that no static analysis can resolve. TypeScript coverage is ~98% with LSP enrichment because type annotations provide the edges that plain JS lacks. This is a language limitation, not a tool limitation. See [Article 46: The Invisible Edges](docs/marketing/articles/46_invisible_edges.md).
- **Parameter extraction is the primary accuracy bottleneck.** Conceptual queries ("trace from menu assembly to rendering") require resolving natural language descriptions to exact symbol names. The graph itself is accurate -- the bottleneck is translating intent to parameters.
- **Graph build is CPU-intensive.** Parsing is ~85% of wall time. A 250K-line codebase takes ~40 seconds on first build. Subsequent starts load from cache in <1 second (bbolt) or ~1-2 seconds (BadgerDB).
- **No code generation or refactoring.** The system answers structural questions. It does not write code or apply patches.
- **CRS is unproven at scale with real users.** The architecture is tested against 9 open-source projects but has not been deployed in production environments.

---

## Documentation

- **51 technical articles**: `docs/marketing/articles/` -- architecture, algorithms, design decisions, limitations, integration testing results
- **Architecture docs**: `docs/opensource/trace/` -- CLI reference, graph architecture, tool routing, parameter extraction
- **Demo scripts**: `docs/marketing/` -- PageRank, dead code, cycles, path analysis, refactoring assessment

---

*Aleutian FOSS -- AGPL v3*
