# Multi-Language CRS Integration Test Suite

This directory contains a modular, multi-language test framework for the Aleutian Trace CRS (Code Reasoning State) integration tests.

## Migration Status: ✅ COMPLETE

**Migration completed:** 2026-02-14
**Total features migrated:** 8 (GR-36, GR-33, GR-28, GR-01, GR-12, GR-16, GR-40, GR-17)
**Total YAML tests created:** 173 tests across 26 YAML files
**Languages supported:** Go, Python, JavaScript, TypeScript
**Legacy tests migrated:** 50 (from 4,438-line monolithic bash script)

See `migrations/migration_status.json` for detailed breakdown.

## Directory Structure

```
test_langs/
├── common/                      # Shared infrastructure
│   ├── test_functions.sh       # 93 validation functions
│   ├── ssh_utils.sh            # SSH connection and remote setup
│   ├── internal_tests.sh       # INTERNAL:* test implementations
│   └── project_utils.sh        # Multi-language project path resolution
│
├── test_projects/              # Language-specific test codebases
│   ├── go/                     # Go projects
│   ├── python/                 # Python projects
│   ├── javascript/             # JavaScript projects
│   └── typescript/             # TypeScript projects
│
├── features/                   # YAML test definitions (173 tests across 8 features)
│   ├── GR-36_session_restore/  # Session persistence (12 tests, 4 languages)
│   ├── GR-33_disk_persistence/ # BadgerDB persistence (3 tests, Go only)
│   ├── GR-28_graph_snapshots/  # Graph state in events (12 tests, 4 languages)
│   ├── GR-01_graph_index/      # Graph index optimization (24 tests, 4 languages)
│   ├── GR-12_pagerank/         # PageRank algorithm (20 tests, 4 languages)
│   ├── GR-16_control_flow/     # Control flow analysis (40 tests, 4 languages)
│   ├── GR-40_go_interfaces/    # Interface detection (6 tests, Go only)
│   └── GR-17_graph_tools/      # Graph analysis tools (56 tests, 4 languages)
│
└── migrations/                 # Migration tracking
    ├── migration_status.json   # Progress tracker
    └── legacy_test_mapping.md  # Old test# → new location
```

## Architecture

### Phase 1: Modular Infrastructure (COMPLETE)

**What Changed:**
- Refactored monolithic `test_crs_integration.sh` (4,438 lines → 1,386 lines)
- Extracted 4 modules to `common/`:
  - `ssh_utils.sh` - 240 lines of SSH/remote server management
  - `test_functions.sh` - 1,274 lines of validation logic (93 validators)
  - `internal_tests.sh` - 1,122 lines of internal state verification
  - `project_utils.sh` - 184 lines of multi-language path resolution

**Benefits:**
- 68.8% reduction in main script size
- Reusable validation functions
- Easier to maintain and extend
- Path to multi-language support

### Phase 2: Test Projects (PLANNED)

Will create 6 new test projects (87 files total):
- Python: `flask_api/`, `data_pipeline/`
- JavaScript: `express_server/`, `react_app/`
- TypeScript: `nest_api/`, `lib_utils/`
- Go: Symlinks to existing projects

### Phases 3-5: YAML Migration (PLANNED)

Will migrate 124 existing Go tests to YAML format and create Python/JS/TS equivalents (~372 total tests).

## Usage

### Run All Tests (Remote Mode)
```bash
./test_crs_integration.sh
```

### Run Specific Tests
```bash
./test_crs_integration.sh -t 1,2,3
./test_crs_integration.sh -t 1-10
```

### Run Local Go Tests
```bash
./test_crs_integration.sh --local
```

### Future: Multi-Language Support
```bash
# Run Go tests only
./test_crs_integration.sh --lang go

# Run specific feature across all languages
./test_crs_integration.sh --feature GR-36

# Run Python tests for session restore
./test_crs_integration.sh --lang python --feature GR-36
```

## Common Modules

### ssh_utils.sh

SSH connection management and remote server lifecycle.

**Functions:**
- `setup_ssh_agent()` - Setup SSH agent for connection caching
- `ssh_cmd()` - Execute command on remote server via multiplexed connection
- `establish_connection()` - Setup SSH multiplexing
- `close_connection()` - Cleanup SSH connections
- `setup_remote()` - Sync project and build trace server
- `check_remote_ollama()` - Verify Ollama models available
- `start_trace_server()` - Start trace server with warmup wait
- `stop_trace_server()` - Stop remote trace server

### test_functions.sh

93 validation functions for verifying CRS behavior.

**Categories:**
- **Session Restore**: `faster_than_first`, `analytics_recorded`, `generation_incremented`
- **Graph Tools**: `graph_tool_used`, `fast_execution`, `fast_not_found`, `implementations_found`
- **PageRank**: `pagerank_used`, `fast_pagerank`
- **Community Detection**: `communities_found`, `find_communities_used`, `fast_community_detection`
- **Control Flow**: `find_articulation_points_tool_used`, `find_dominators_tool_used`, `find_loops_tool_used`
- **Performance**: `empty_response_threshold`, `avg_runtime_threshold`, `crs_speedup_verified`
- **Cache**: `cache_miss_expected`, `cache_hit_expected`, `cache_speedup_expected`
- **Parallel BFS**: `parallel_correctness`, `parallel_speedup`

**Main Function:**
```bash
run_extra_check "validation_name" "$response" "$duration" "$session_id"
```

### internal_tests.sh

Internal CRS state verification functions.

**Tests:**
- `verify_checkpoint_exists` - Check BadgerDB persistence files
- `restart_and_verify_state` - Verify state restore after restart
- `verify_event_graph_context` - Check event graph in CRS
- `verify_delta_count` - Verify delta history recording
- `verify_history_limit` - Check delta pruning
- `verify_index_span_attribute` - OTel span verification
- And 30+ more CRS internal validations

**Main Function:**
```bash
run_internal_test "$category" "$test_name" "$expected" "$test_num"
```

### project_utils.sh

Multi-language project path resolution utilities.

**Functions:**
- `get_project_root "language" "project_name"` - Resolve absolute path to test project
- `list_projects "language"` - List all projects for a language
- `validate_project "language" "project_name"` - Check if project exists
- `get_project_file_count "language" "project_name"` - Count source files
- `extract_language_from_yaml "yaml_file"` - Parse language from YAML metadata
- `extract_project_from_yaml "yaml_file"` - Parse project name from YAML
- `sync_project_to_remote "lang" "proj" "user@host" "path"` - Rsync to remote server

**Usage Example:**
```bash
# Get project path
project_root=$(get_project_root "python" "flask_api")

# Validate project exists
if validate_project "go" "orchestrator"; then
    echo "Project found at: $project_root"
fi

# Count files
file_count=$(get_project_file_count "typescript" "nest_api")
echo "TypeScript files: $file_count"
```

## Test ID Ranges

**Convention:**
```
   1-999:   Go tests
1001-1999:  Python tests
2001-2999:  JavaScript tests
3001-3999:  TypeScript tests
```

**Example Mapping:**
```
GR-36 Session Restore:
  Test 1 (Go):         Baseline session
  Test 2 (Go):         Session restore
  Test 3 (Go):         CRS speedup verification
  Test 101 (Python):   Baseline session (Python)
  Test 102 (Python):   Session restore (Python)
  Test 103 (Python):   CRS speedup (Python)
  Test 201 (JavaScript): Baseline (JS)
  Test 202 (JavaScript): Restore (JS)
  Test 203 (JavaScript): Speedup (JS)
  Test 301 (TypeScript): Baseline (TS)
  Test 302 (TypeScript): Restore (TS)
  Test 303 (TypeScript): Speedup (TS)
```

## Validation Function Examples

### Session Restore Validation
```bash
# Check if session 2+ is faster due to CRS state restore
run_extra_check "faster_than_first" "$response" "$duration" "$session_id"
# Output: ✓ 35% faster than first query (2500ms vs 3800ms)
```

### Graph Tool Validation
```bash
# Verify find_callers/find_callees was used
run_extra_check "graph_tool_used" "$response" "$duration" "$session_id"
# Output: ✓ Graph tools used: 3 invocations
```

### PageRank Validation
```bash
# Verify find_important (PageRank-based) was used
run_extra_check "pagerank_used" "$response" "$duration" "$session_id"
# Output: ✓ GR-13: find_important tool was used: 1 calls
#         ✓ GR-12: Response mentions PageRank scoring
```

## Migration Status

**Phase 1: Infrastructure Setup** ✅ COMPLETE
- [x] Extracted SSH utilities to `ssh_utils.sh`
- [x] Extracted 93 validation functions to `test_functions.sh`
- [x] Extracted internal tests to `internal_tests.sh`
- [x] Created `project_utils.sh` for multi-language support
- [x] Refactored main script (4,438 → 1,386 lines)
- [x] All existing tests still passing

**Phase 2: Test Projects** ⏳ PLANNED
- [ ] Create Python test projects (2 projects, ~27 files)
- [ ] Create JavaScript test projects (2 projects, ~25 files)
- [ ] Create TypeScript test projects (2 projects, ~32 files)
- [ ] Symlink Go projects (2 symlinks)
- [ ] Verify parsers work for all projects

**Phase 3: Core CRS Features Migration** ⏳ PLANNED
- [ ] GR-36 Session Restore → YAML (12 tests)
- [ ] GR-33 Disk Persistence → YAML (3 tests)
- [ ] GR-28 Graph Snapshots → YAML (12 tests)

**Phase 4: Graph Algorithms Migration** ⏳ PLANNED
- [ ] GR-01 Graph Index → YAML (24 tests)
- [ ] GR-12 PageRank → YAML (20 tests)
- [ ] GR-16 Control Flow → YAML (~40 tests)

**Phase 5: Tool-Specific Features Migration** ⏳ PLANNED
- [ ] GR-40 Go Interfaces → YAML (6 tests, Go only)
- [ ] GR-17 Graph Tools → YAML (~60 tests)

**Phase 6: Validation and Cleanup** ⏳ PLANNED
- [ ] Full test suite verification
- [ ] Documentation updates
- [ ] Migration completion report

## Contributing

### Adding a New Validation Function

1. Add the validation case to `test_functions.sh`:
```bash
run_extra_check() {
    case "$check" in
        # ... existing cases ...

        my_new_validation)
            # Your validation logic here
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            if [ -n "$agent_resp" ]; then
                echo -e "    ${GREEN}✓ Validation passed${NC}"
            else
                echo -e "    ${RED}✗ Validation failed${NC}"
            fi
            ;;
    esac
}
```

2. Use in tests by adding to extra_check field in CRS_TESTS array:
```bash
"CATEGORY|session_id|query|COMPLETE|my_new_validation"
```

### Adding a New Test Project

1. Create project directory:
```bash
mkdir -p test_projects/python/my_project
```

2. Add source files with realistic code structure

3. Verify parser works:
```bash
go test ./services/trace/ast/... -v
```

4. Add tests referencing the project

## Troubleshooting

### Module Not Found
If you get "No such file or directory" when sourcing modules:
```bash
# Check modules exist
ls -la scripts/test_langs/common/

# Verify script is run from correct directory
pwd  # Should be in AleutianFOSS root

# Run with explicit path
./scripts/test_crs_integration.sh
```

### SSH Connection Issues
```bash
# Check SSH key
ls -la ~/.ssh/aleutiandevops_ansible_key

# Test SSH manually
ssh -i ~/.ssh/aleutiandevops_ansible_key -p 13022 aleutiandevops@10.0.0.250 echo "OK"

# Check SSH multiplexing socket
ls -la ~/.ssh/crs_test_multiplex_*
```

### Validation Function Not Working
```bash
# Check if function is defined
grep -n "my_validation)" scripts/test_langs/common/test_functions.sh

# Test function in isolation
source scripts/test_langs/common/test_functions.sh
run_extra_check "my_validation" '{"response":"test"}' "1000" "session_123"
```

## References

- Main ticket: `tickets/in_progress/test_suite_01_multi_language_refactor.md`
- Original script: `scripts/test_crs_integration.sh.backup`
- CRS architecture: `docs/opensource/trace/mcts/`
- Parser documentation: `services/trace/ast/README.md`
