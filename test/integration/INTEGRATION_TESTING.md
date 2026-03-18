# Container-Based Integration Testing

This document explains the container-based integration test stack: what it does, why it exists, how to run it, and how each piece fits together.

## Why This Exists

The original test infrastructure (`scripts/test_crs_integration.sh`) was built around SSH-to-remote-server execution of a bare binary. It works, but it has limitations:

- **Sequential project testing**: The SSH runner starts one trace server, loads one project, runs its tests, then stops and restarts for the next project. Testing all 9 projects takes hours.
- **Stateful server reuse**: A single trace process serves all tests for a project, so graph cache and CRS state from earlier tests affect later ones. This makes results order-dependent.
- **Manual lifecycle management**: The SSH runner handles `pkill`, port checks, log wipes, cache cleanup, and warmup polling. Any failure in this chain leaves stale processes.

The container stack solves these by running 9 isolated trace containers in parallel, each with a clean filesystem and a single mounted project. A sidecar container runs the tests and reports results in TAP format.

## Architecture

```
Remote GPU Server (10.0.0.250) — host network mode
+------------------------------------------------------------+
|  Ollama (port 11434)  <- shared, reachable at localhost      |
|                                                              |
|  +----------+ +----------+ +----------+       +-----------+ |
|  | trace-1  | | trace-2  | | trace-3  |  ...  | trace-9   | |
|  | Hugo     | | Badger   | | Gin      |       | Plottable | |
|  | :12301   | | :12302   | | :12303   |       | :12309    | |
|  | /projects| | /projects| | /projects|       | /projects | |
|  +----------+ +----------+ +----------+       +-----------+ |
|       All containers share host network (network_mode: host) |
|       Test-runner reaches them at localhost:<port>            |
|       +------------------------------------------------+     |
|       | test-runner (sidecar, also host network)       |     |
|       | Reads YAML -> curl -> validate -> TAP output   |     |
|       +------------------------------------------------+     |
+------------------------------------------------------------+
```

Each trace container:
- Mounts one FOSS project read-only at `/projects`
- Runs the standard trace image (`/app/trace -with-context -with-tools -lsp-enabled`)
- Uses `network_mode: host` so it shares the host's network stack
- Binds to a unique port (12301-12309) via the `PORT` env var
- Reaches Ollama at `localhost:11434` (no bridge networking needed)
- Starts with a clean filesystem (no stale graph cache, no CRS state)

The test-runner container:
- Waits for all 9 trace containers to pass their healthcheck (`/v1/trace/ready` returns 200)
- Discovers YAML test files from the mounted `scripts/test_langs/` directory
- Runs all 9 projects in parallel, tests within each project sequentially
- Outputs TAP v13 to stdout for CI consumption

## Port Mapping

| Service | Project | Language | External Port |
|---------|---------|----------|---------------|
| trace-hugo | hugo | Go | 12301 |
| trace-badger | badger | Go | 12302 |
| trace-gin | gin | Go | 12303 |
| trace-flask | flask | Python | 12304 |
| trace-pandas | pandas | Python | 12305 |
| trace-express | express | JavaScript | 12306 |
| trace-babylonjs | babylonjs | JavaScript | 12307 |
| trace-nestjs | nestjs | TypeScript | 12308 |
| trace-plottable | plottable | TypeScript | 12309 |

All containers use host networking, so both external debugging and the test-runner use the same ports (e.g., `curl http://localhost:12301/v1/trace/health`).

## File Inventory

```
test/integration/
  podman-compose.test.yml    # Compose file: 9 trace services + test-runner sidecar
  Dockerfile.test-runner     # Alpine image: bash, curl, jq, yq
  run_tests.sh               # YAML-driven test executor (runs inside container)
  run_stack.sh               # Orchestration script (runs from dev machine)
  INTEGRATION_TESTING.md     # This document

scripts/test_langs/
  features/TOOL-HAPPY-*/     # YAML test definitions (9 files, one per project)
  common/test_functions.sh   # Original SSH-based validation functions (still used by test_crs_integration.sh)
  common/ssh_utils.sh        # SSH lifecycle management (still used by test_crs_integration.sh)
```

### How the pieces relate

`run_stack.sh` is the entry point you run from your dev machine. It handles SSH, rsync, image builds, and compose lifecycle. It calls `podman-compose` with `podman-compose.test.yml`, which starts the trace containers and the test-runner. The test-runner runs `run_tests.sh`, which reads the YAML files mounted from `scripts/test_langs/features/`.

```
Developer machine                    Remote GPU server
  run_stack.sh ----SSH/rsync----->  podman-compose.test.yml
                                      |
                                      +-> trace-hugo (container)
                                      +-> trace-badger (container)
                                      +-> ... (7 more)
                                      +-> test-runner (container)
                                            |
                                            +-> run_tests.sh
                                                  reads: /tests/features/TOOL-HAPPY-*/*.yml
                                                  curls: http://trace-hugo:12217/v1/trace/agent/run
                                                  outputs: TAP v13 to stdout
```

## How to Run

### Full remote run (all 9 projects)

```bash
./test/integration/run_stack.sh
```

This will:
1. SSH to `10.0.0.250` (override with `CRS_TEST_HOST`)
2. Verify Ollama is running and test codebases exist
3. Rsync the repo
4. Build the trace image and test-runner image
5. `podman-compose up --abort-on-container-exit`
6. Print TAP output and exit with the test-runner's exit code
7. Clean up containers

### Specific projects only

```bash
./test/integration/run_stack.sh --projects hugo,flask
```

This sets `PROJECT_FILTER=hugo,flask`, which the test-runner uses to only discover YAML files for those projects. The compose file still starts all 9 containers, but only 2 will receive test traffic.

### Override models

```bash
./test/integration/run_stack.sh --main-model qwen3:14b --router-model granite4:micro-h
```

### Run locally (no SSH)

```bash
./test/integration/run_stack.sh --local
```

Requires Ollama running locally and test codebases at `~/projects/crs_test_codebases/`. Builds and runs everything with local podman.

### Run compose directly (already on the remote server)

```bash
cd /path/to/AleutianFOSS
OLLAMA_MODEL=gpt-oss:20b \
CRS_TEST_CODEBASES=~/projects/crs_test_codebases \
podman-compose -f test/integration/podman-compose.test.yml up \
  --abort-on-container-exit \
  --exit-code-from test-runner
```

### Smoke test with sample-go-project

```bash
podman build -t aleutian-trace:latest -f services/trace/Dockerfile .
podman run -d --name trace-test \
  -v ./test/fixtures/sample-go-project:/projects:ro \
  -p 12301:12217 aleutian-trace:latest
# Wait for warmup...
curl -s http://localhost:12301/v1/trace/agent/run \
  -H "Content-Type: application/json" \
  -d '{"query":"What does main.go do?","project_root":"/projects"}'
```

## Test YAML Format

Each YAML file in `scripts/test_langs/features/TOOL-HAPPY-*/` defines tests for one project. The format:

```yaml
metadata:
  feature: TOOL-HAPPY-HUGO
  language: go
  project: hugo
  project_root: ~/projects/crs_test_codebases/go/hugo   # SSH runner path
  container: trace-hugo                                   # container service name
  container_project_root: /projects                       # mount point inside container

tests:
  - id: 5000                          # globally unique integer
    name: hugo_find_callers           # snake_case identifier
    category: TOOL_HAPPY_PATH         # or TOOL_HAPPY_PATH_TODO (skipped)
    query: |
      Who calls the Publish function in this codebase?
    expected_state: COMPLETE           # or CLARIFY
    validations:
      - type: graph_tool_used          # validation function name
      - type: fast_execution           # multiple validations allowed
```

The `project_root` field is used by the original SSH-based runner (`test_crs_integration.sh`). The container runner ignores it and always sends `project_root: /projects` in the API request, since that is where the project is mounted.

The `container` and `container_project_root` fields were added to make the YAML files self-documenting about which container serves which project. The test-runner currently maps project names to URLs via environment variables rather than reading these fields, but they serve as documentation and could be used by future tooling.

## Validation Functions

The test-runner includes adapted versions of the validation functions from `scripts/test_langs/common/test_functions.sh`. The key difference is that the original functions use `ssh_cmd "curl ..."` to reach the trace server, while the container versions use direct HTTP to the container URL.

Supported validations:

| Validation | What it checks |
|------------|----------------|
| `graph_tool_used` | CRS reasoning trace contains find_callers/find_callees/find_implementations/find_symbol/get_call_chain/find_references tool calls |
| `pagerank_used` | Reasoning trace contains find_important tool call |
| `implementations_found` | find_implementations was called AND response contains positive results |
| `no_grep_used` | Grep tool was NOT called (no fallback) |
| `fast_execution` | Duration < 5000ms |
| `fast_not_found` | Duration < 3000ms (O(1) index miss) |
| `fast_pagerank` | Duration < 30000ms |
| `citations_present` | Response contains `[file.go:123]` style citations |
| `analytics_recorded` | Reasoning trace contains analytics_query or tool_call steps |
| `communities_found` | Response mentions communities/modules/clusters |
| `find_communities_used` | find_communities tool in trace |
| `find_loops_tool_used` | find_loops tool in trace |
| `find_extractable_tool_used` | find_extractable tool in trace |
| `find_control_deps_tool_used` | find_control_deps tool in trace |
| `check_reducibility_tool_used` | check_reducibility tool in trace |
| `find_critical_path_tool_used` | find_critical_path tool in trace |
| `find_dominators_tool_used` | find_dominators tool in trace |
| `find_articulation_points_tool_used` | find_articulation tool in trace |
| `find_merge_points_tool_used` | find_merge tool in trace |

Unknown validation types are logged and skipped (non-blocking). This means SSH-specific checks like `cache_hit_expected` or `generation_incremented` (which call debug endpoints on the same server) will pass with a warning rather than failing the test.

## TAP Output

The test-runner outputs [TAP v13](https://testanything.org/) to stdout:

```
TAP version 13
1..180
ok 5000 - hugo_find_callers (12345) - COMPLETE
ok 5001 - hugo_find_callees (8901) - COMPLETE
not ok 5002 - hugo_find_implementations # validation failed (15234ms)
ok 5030 - hugo_summarize_package # SKIP TODO (tool not registered)
...
# Results: 165 passed, 7 failed, 8 skipped (180 total)
# Pass rate: 95.5%
```

TAP is machine-parseable and supported by most CI systems. The `# SKIP` directive marks TODO tests and internal tests that do not apply in container mode.

## Backward Compatibility

The container stack does not replace the SSH-based runner. Both coexist:

- `scripts/test_crs_integration.sh` — SSH-based, single server, sequential projects. Uses `project_root` from YAML metadata. Supports legacy hardcoded tests (IDs 1-121) and INTERNAL: tests.
- `test/integration/run_stack.sh` — Container-based, 9 parallel servers, parallel projects. Uses container URLs. Skips INTERNAL: tests (they test server-level state that is not meaningful per-container).

The YAML files serve both runners. The `container` and `container_project_root` fields are ignored by the SSH runner (yq only reads `project_root`). The `project_root` field is ignored by the container runner (it always uses `/projects`).

## Troubleshooting

### Container fails healthcheck

The healthcheck polls `/v1/trace/ready` with a 120s start period. If a container does not become ready, check:

```bash
# View container logs
podman logs trace-hugo

# Common causes:
# 1. Ollama not reachable (check host.containers.internal:11434)
# 2. Model not pulled (check OLLAMA_MODEL is available)
# 3. Project mount failed (check CRS_TEST_CODEBASES path)
```

### Test-runner cannot reach containers

All containers use host networking. The test-runner reaches trace containers at `localhost:<port>`:

```bash
# Check from host
curl -v http://localhost:12301/v1/trace/health

# Verify ports are listening
ss -tlnp | grep '1230[1-9]'
```

### Debugging a single test

Use the external port to interact with a specific container:

```bash
# Send a query to the Hugo container
curl -s http://10.0.0.250:12301/v1/trace/agent/run \
  -H "Content-Type: application/json" \
  -d '{"query":"Who calls Publish?","project_root":"/projects"}'

# Check the reasoning trace
curl -s http://10.0.0.250:12301/v1/trace/agent/<session_id>/reasoning | jq .
```

### Running a subset of tests

```bash
# By project
./test/integration/run_stack.sh --projects hugo

# By feature directory
FEATURE_FILTER=TOOL-HAPPY-HUGO ./test/integration/run_stack.sh
```
