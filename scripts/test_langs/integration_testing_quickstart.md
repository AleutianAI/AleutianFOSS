# CRS Integration Testing — Quickstart Guide

## Overview

The CRS Integration Test Suite validates the Trace Agent's graph analysis tools against real open-source codebases. It runs 533 tests across 17 features in 4 languages (Go, Python, JavaScript, TypeScript).

```
Test Suite Breakdown
┌──────────────────────────────────────────────────────────┐
│  Core CRS Features (GR-36, GR-33, GR-28)    27 tests   │
│  Graph Algorithms  (GR-01, GR-12, GR-16)    84 tests   │
│  Tool Features     (GR-40, GR-17)           62 tests   │
│  Tool Happy Path   (9 projects × 40 tests) 360 tests   │
│                                             ─────────   │
│  Total                                      533 tests   │
└──────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **SSH access** to the remote test server (`10.0.0.250:13022`)
2. **Ollama models** pulled on the remote: `gpt-oss:20b` (main) and `granite4:micro-h` (router)
3. **Test codebases** cloned at `~/projects/crs_test_codebases/` (see [Project Setup](#project-setup))
4. **yq** (Go flavor) installed locally for YAML parsing
5. **jq** installed locally for JSON result parsing

## Quick Start (3 commands)

```bash
# 1. Run the interactive test runner (recommended for first-time users)
./scripts/test_crs_interactive.sh

# 2. Or run all Go tests directly
./scripts/test_crs_integration.sh --lang go

# 3. Or run a specific feature
./scripts/test_crs_integration.sh --feature GR-01 --lang go
```

## Test Runner CLI Reference

```
./scripts/test_crs_integration.sh [OPTIONS]

Options:
  -t, --tests TEST_SPEC      Comma-separated test IDs or ranges (e.g., 1,2,3 or 16-21)
  --lang LANGUAGE             Filter by language: go, python, javascript, typescript
  --feature FEATURE           Filter by feature: GR-01, GR-36, TOOL-HAPPY-HUGO, etc.
  --local                     Run local Go unit tests (no SSH required)
  --router-model MODEL        Router model (default: granite4:micro-h)
  --main-model MODEL          Main agent model (default: gpt-oss:20b)
  -h, --help                  Show help
```

## Interactive Runner

The interactive runner provides a menu-driven interface:

```bash
./scripts/test_crs_interactive.sh
```

**Main menu options:**

| # | Option | Description |
|---|--------|-------------|
| 1 | Quick Select | Run by language or all tests |
| 2 | By Feature | Select a specific GR-* feature |
| 3 | By Category | Core CRS, Graph Algorithms, or Tools |
| 4 | Tool Happy Path | 23 tools x 9 projects (360 tests) |
| 5 | Custom Filter | Advanced: combine language + feature + mode |
| 6 | Reconfigure Models | Change router/main model selection |
| 7 | Help | Documentation and test ID reference |
| 8 | Quit | Exit |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CRS_TEST_HOST` | `10.0.0.250` | Remote test server IP |
| `CRS_TEST_PORT` | `13022` | SSH port |
| `CRS_TEST_USER` | `aleutiandevops` | SSH user |
| `TEST_PROJECT_ROOT` | `~/GolandProjects/AleutianOrchestrator` | Project to analyze |

## Test ID Ranges

### Core Features (IDs 1-90)

| IDs | Feature | Tests | Description |
|-----|---------|-------|-------------|
| 1-3 | GR-36 Session Restore | 3 | Learned state persists across sessions |
| 4-6 | GR-33 Disk Persistence | 3 | Checkpoint save/load |
| 7-9 | GR-28 Graph Snapshots | 3 | Graph state capture |
| 10-12 | GR-31 Analytics Routing | 3 | Analytics tools route through CRS |
| 13-15 | GR-35 Delta History | 3 | Delta recording and replay |
| 16-21 | GR-01 Graph Index | 6 | Graph tools use O(1) SymbolIndex lookup |
| 22-27 | GR-40 Go Interfaces | 6 | Go interface implementation detection |
| 28-30 | GR-41 Existence Tests | 3 | Verify call edge extraction |
| 31-35 | GR-12 PageRank | 5 | PageRank-based importance ranking |
| 36-44 | Quality Fixes | 9 | Response quality and performance |
| 45-90 | GR-06 to GR-17 | 46 | Secondary indexes, caching, BFS, communities, control flow |

### Multi-Language Features (IDs 101-410)

| IDs | Language | Feature Coverage |
|-----|----------|------------------|
| 101-133 | Go | GR-36, GR-28, GR-01, GR-12, GR-16, GR-17 |
| 201-233 | Python | Same features |
| 301-333 | JavaScript | Same features |
| 401-433 | TypeScript | Same features |

### Tool Happy Path (IDs 5000-8139)

| IDs | Project | Language | Tests |
|-----|---------|----------|-------|
| 5000-5039 | Hugo | Go | 40 |
| 5100-5139 | Badger | Go | 40 |
| 5200-5239 | Gin | Go | 40 |
| 6000-6039 | Flask | Python | 40 |
| 6100-6139 | Pandas | Python | 40 |
| 7000-7039 | Express | JavaScript | 40 |
| 7100-7139 | Babylon.js | JavaScript | 40 |
| 8000-8039 | NestJS | TypeScript | 40 |
| 8100-8139 | Plottable | TypeScript | 40 |

Within each project, tool index `00-22` = registered tools, `23-39` = TODO/unregistered tools.

## Directory Structure

```
scripts/
├── test_crs_integration.sh           # Main test runner
├── test_crs_interactive.sh           # Interactive menu runner
├── compare_to_gold_standard.sh       # Gold standard comparison pipeline
├── benchmark_crs_advantage.sh        # CRS advantage benchmarking
└── test_langs/
    ├── common/
    │   ├── ssh_utils.sh              # SSH connection management
    │   ├── test_functions.sh         # 93+ validation functions
    │   ├── internal_tests.sh         # INTERNAL: test implementations
    │   └── project_utils.sh          # Project path resolution & cloning
    └── features/
        ├── GR-01_graph_index/        # 4 YAMLs (go, python, js, ts)
        ├── GR-12_pagerank/
        ├── GR-16_control_flow/
        ├── GR-17_graph_tools/
        ├── GR-28_graph_snapshots/
        ├── GR-33_disk_persistence/
        ├── GR-36_session_restore/
        ├── GR-40_go_interfaces/
        ├── TOOL-HAPPY-HUGO/          # go.yml (40 tests)
        ├── TOOL-HAPPY-BADGER/        # go.yml
        ├── TOOL-HAPPY-GIN/           # go.yml
        ├── TOOL-HAPPY-FLASK/         # python.yml
        ├── TOOL-HAPPY-PANDAS/        # python.yml
        ├── TOOL-HAPPY-EXPRESS/       # javascript.yml
        ├── TOOL-HAPPY-BABYLONJS/     # javascript.yml
        ├── TOOL-HAPPY-NESTJS/        # typescript.yml
        └── TOOL-HAPPY-PLOTTABLE/     # typescript.yml
```

## YAML Test Format

Each feature directory contains per-language YAML files. Format:

```yaml
metadata:
  feature: GR-01                          # Feature ID (matches directory name)
  title: "Graph Index Optimization"       # Human-readable title
  language: go                            # go | python | javascript | typescript
  project: orchestrator                   # Target project name
  ticket: tickets/merged/GR-01.md        # Related ticket
  description: >
    Multi-line description of what this feature tests.

tests:
  - id: 16                                # Unique numeric ID
    name: find_callers_basic              # Descriptive test name
    category: GRAPH_INDEX                 # Test category
    description: >
      What this test validates.
    query: |
      Find all callers of the Setup function
    expected_state: COMPLETE              # COMPLETE or CLARIFY
    reference_answer: |                   # Gold standard answer (optional)
      The Setup function is called from main.go and config.go.
    validations:
      - type: graph_tool_used             # Validation function to run
```

## Project Setup

### Core test codebases (auto-cloned by project_utils.sh)

```bash
# These are cloned automatically on first test run:
~/projects/crs_test_codebases/
├── go/hugo/          # Hugo v0.139.4 (~65K lines)
├── python/flask/     # Flask 3.1.0 (~15K lines)
├── javascript/express/ # Express 4.21.2 (~6K lines)
└── typescript/nest/  # NestJS v10.4.15 (~50K lines)
```

### Additional projects (for Tool Happy Path tests)

```bash
# Clone these manually for full Tool Happy Path coverage:
cd ~/projects
git clone https://github.com/dgraph-io/badger.git
git clone https://github.com/gin-gonic/gin.git
git clone https://github.com/pandas-dev/pandas.git
git clone https://github.com/BabylonJS/Babylon.js.git
git clone https://github.com/palantir/plottable.git
```

## Common Workflows

### Run a single test by ID

```bash
./scripts/test_crs_integration.sh -t 5000
```

### Run a range of tests

```bash
./scripts/test_crs_integration.sh -t 5000-5022
```

### Run all Tool Happy Path tests for one project

```bash
./scripts/test_crs_integration.sh --feature TOOL-HAPPY-HUGO --lang go
```

### Run gold standard comparison

```bash
./scripts/compare_to_gold_standard.sh --feature TOOL-HAPPY-HUGO --lang go -t 5000,5001,5002
```

### Run in local mode (no SSH, Go unit tests only)

```bash
./scripts/test_crs_integration.sh --local
```

### Override models

```bash
./scripts/test_crs_integration.sh --main-model "glm-4.7-flash" --router-model "granite4:micro-h" --feature GR-01
```

## Adding New Tests

1. Create or edit a YAML file in `scripts/test_langs/features/<FEATURE>/<language>.yml`
2. Assign a unique test ID (check existing ranges above to avoid conflicts)
3. Set `expected_state` to `COMPLETE` (tool should answer) or `CLARIFY` (tool not available)
4. Add `validations` entries referencing functions in `test_langs/common/test_functions.sh`
5. Validate syntax: `yq eval '.' scripts/test_langs/features/<FEATURE>/<language>.yml`

## Validation Functions

The test runner supports 93+ validation functions in `test_functions.sh`. Common ones:

| Validation | Description |
|------------|-------------|
| `graph_tool_used` | Verifies a graph tool (find_callers, find_callees, etc.) was invoked |
| `fast_execution` | Checks query completed within performance threshold |
| `implementations_found` | Verifies interface implementations were returned |
| `pagerank_used` | Confirms PageRank algorithm was used (not degree-based) |
| `communities_found` | Checks community detection returned results |
| `cache_hit_expected` | Verifies LRU cache was hit on repeated query |
| `citations_present` | Checks response includes `[file:line]` citations |

## Troubleshooting

| Problem | Solution |
|---------|----------|
| SSH connection refused | Verify `CRS_TEST_HOST`, `CRS_TEST_PORT`, and SSH key at `~/.ssh/aleutiandevops_ansible_key` |
| "Project not found" | Run project setup commands above, or set `TEST_PROJECT_ROOT` |
| YAML parse error | Run `yq eval '.' <file>` to identify syntax issues |
| Test auto-detection fails | Use explicit `--lang` and `--feature` flags |
| Model not found | Pull models on remote: `ssh remote 'ollama pull gpt-oss:20b'` |
