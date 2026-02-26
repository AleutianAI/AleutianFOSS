#!/bin/bash
# CRS Integration Tests for Aleutian Trace Agent
# Tests Phase 0 CRS features (GR-28 through GR-37)
#
# Refactored to use modular components from test_langs/common/
#
# Usage:
#   ./test_crs_integration.sh              # Run all CRS tests (remote mode)
#   ./test_crs_integration.sh -t 1,2,3     # Run specific tests
#   ./test_crs_integration.sh --local      # Run local Go tests (no GPU required)
#   ./test_crs_integration.sh --lang go    # Run Go tests only (future: multi-language)
#   ./test_crs_integration.sh --feature GR-36  # Run specific feature tests (future: YAML-based)

set -e

# ==============================================================================
# CONFIGURATION
# ==============================================================================

# Remote server configuration
REMOTE_HOST="${CRS_TEST_HOST:-10.0.0.250}"
REMOTE_PORT="${CRS_TEST_PORT:-13022}"
REMOTE_USER="${CRS_TEST_USER:-aleutiandevops}"
SSH_KEY="$HOME/.ssh/aleutiandevops_ansible_key"
SSH_CONTROL_SOCKET="$HOME/.ssh/crs_test_multiplex_%h_%p_%r"

# Model configuration
OLLAMA_MODEL="gpt-oss:20b"
ROUTER_MODEL="granite4:micro-h"
PARAM_EXTRACTOR_MODEL="ministral-3:3b"

# Project to analyze on remote
PROJECT_TO_ANALYZE="${TEST_PROJECT_ROOT:-/Users/jin/GolandProjects/AleutianOrchestrator}"

# Output files (timestamped)
OUTPUT_FILE="/tmp/crs_test_results_$(date +%Y%m%d_%H%M%S).json"

# Local test mode
LOCAL_MODE=false

# Language filter (for future multi-language support)
LANGUAGE_FILTER=""
FEATURE_FILTER=""
TOOL_FILTER=""

# ==============================================================================
# PARSE ARGUMENTS
# ==============================================================================

SPECIFIC_TESTS=""
while [[ $# -gt 0 ]]; do
    case $1 in
        -t|--tests)
            SPECIFIC_TESTS="$2"
            shift 2
            ;;
        --local)
            LOCAL_MODE=true
            shift
            ;;
        --lang|--language)
            LANGUAGE_FILTER="$2"
            shift 2
            ;;
        --feature)
            FEATURE_FILTER="$2"
            shift 2
            ;;
        --tool)
            TOOL_FILTER="$2"
            shift 2
            ;;
        --router-model)
            ROUTER_MODEL="$2"
            shift 2
            ;;
        --main-model)
            OLLAMA_MODEL="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [-t|--tests TEST_SPEC] [--local] [--lang LANGUAGE] [--feature FEATURE] [--tool TOOL] [--router-model MODEL] [--main-model MODEL]"
            echo ""
            echo "Options:"
            echo "  -t, --tests       Comma-separated test numbers or ranges (e.g., 1,2,3 or 1-5)"
            echo "  --local           Run local Go tests instead of remote integration tests"
            echo "  --lang            Filter by language (go, python, javascript, typescript)"
            echo "  --feature         Filter by feature (e.g., GR-36, TOOL-HAPPY-HUGO)"
            echo "  --tool            Run one tool across all projects (e.g., find_callers)"
            echo "  --router-model    Router model to use (default: granite4:micro-h)"
            echo "  --main-model      Main agent model to use (default: gpt-oss:20b)"
            echo ""
            echo "Test Categories:"
            echo "  1-3:   Session Restore (GR-36)"
            echo "  4-6:   Disk Persistence (GR-33)"
            echo "  7-9:   Graph Snapshots (GR-28)"
            echo "  10-12: Analytics Routing (GR-31)"
            echo "  13-15: Delta History (GR-35)"
            echo "  16-21: Graph Index Optimization (GR-01)"
            echo "  ... (see full list with --help-full)"
            echo ""
            echo "Environment Variables:"
            echo "  CRS_TEST_HOST    Remote host (default: 10.0.0.250)"
            echo "  CRS_TEST_PORT    SSH port (default: 13022)"
            echo "  CRS_TEST_USER    SSH user (default: aleutiandevops)"
            echo "  TEST_PROJECT_ROOT  Project to analyze"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# ==============================================================================
# SOURCE MODULAR COMPONENTS
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors (exported so modules can use them)
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export CYAN='\033[0;36m'
export NC='\033[0m'

# Source modules with error checking
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
for module in ssh_utils test_functions internal_tests project_utils; do
    module_path="$REPO_ROOT/test_langs/common/${module}.sh"
    if [ ! -f "$module_path" ]; then
        echo -e "${RED}ERROR: Required module not found: $module_path${NC}" >&2
        echo "Please ensure test_langs/common/ directory exists and contains all required modules." >&2
        exit 1
    fi
    source "$module_path"
done

# ==============================================================================
# HELPER FUNCTIONS FOR AUTO-DETECTION
# ==============================================================================

expand_test_spec() {
    local spec="$1"
    local result=()

    IFS=',' read -ra parts <<< "$spec"
    for part in "${parts[@]}"; do
        if [[ "$part" =~ ^([0-9]+)-([0-9]+)$ ]]; then
            # Range like "1-5"
            for ((i=${BASH_REMATCH[1]}; i<=${BASH_REMATCH[2]}; i++)); do
                result+=($i)
            done
        else
            # Single number
            result+=($part)
        fi
    done

    echo "${result[@]}"
}

# Auto-detect language from test ID ranges
# Returns: go, python, javascript, typescript, or empty if mixed/unknown
detect_language_from_tests() {
    local spec="$1"
    local tests=($(expand_test_spec "$spec"))
    local detected_lang=""

    for test_id in "${tests[@]}"; do
        local lang=""
        if [ "$test_id" -ge 101 ] && [ "$test_id" -le 133 ]; then
            lang="go"
        elif [ "$test_id" -ge 201 ] && [ "$test_id" -le 233 ]; then
            lang="python"
        elif [ "$test_id" -ge 301 ] && [ "$test_id" -le 333 ]; then
            lang="javascript"
        elif [ "$test_id" -ge 401 ] && [ "$test_id" -le 433 ]; then
            lang="typescript"
        # Tool Happy Path ranges — must match map_test_id_to_feature() ranges
        # Go: Hugo=50xx, Badger=51xx, Gin=52xx-53xx
        elif [ "$test_id" -ge 5000 ] && [ "$test_id" -le 5399 ]; then
            lang="go"
        # Python: Flask=60xx, Pandas=61xx-62xx
        elif [ "$test_id" -ge 6000 ] && [ "$test_id" -le 6299 ]; then
            lang="python"
        # JavaScript: Express=70xx, BabylonJS=71xx-72xx
        elif [ "$test_id" -ge 7000 ] && [ "$test_id" -le 7299 ]; then
            lang="javascript"
        # TypeScript: NestJS=80xx, Plottable=81xx-82xx
        elif [ "$test_id" -ge 8000 ] && [ "$test_id" -le 8299 ]; then
            lang="typescript"
        fi

        # If we detected a language
        if [ -n "$lang" ]; then
            # First detection
            if [ -z "$detected_lang" ]; then
                detected_lang="$lang"
            # Mixed languages - return empty
            elif [ "$detected_lang" != "$lang" ]; then
                echo ""
                return 1
            fi
        fi
    done

    echo "$detected_lang"
}

# ==============================================================================
# AUTO-DETECT LANGUAGE FROM TEST IDS
# ==============================================================================

# If specific tests were provided without --lang, auto-detect language
if [ -n "$SPECIFIC_TESTS" ] && [ -z "$LANGUAGE_FILTER" ]; then
    AUTO_DETECTED_LANG=$(detect_language_from_tests "$SPECIFIC_TESTS" || true)
    if [ -n "$AUTO_DETECTED_LANG" ]; then
        LANGUAGE_FILTER="$AUTO_DETECTED_LANG"
        echo -e "${CYAN}Auto-detected language from test IDs: ${BOLD}$LANGUAGE_FILTER${NC}"
        echo ""
    fi
fi

# Map a single test ID to its TOOL-HAPPY-* feature directory.
# Uses the "hundreds digit" to distinguish projects within the same language range.
# Ranges: Hugo=50xx, Badger=51xx, Gin=52xx, Flask=60xx, Pandas=61xx,
#          Express=70xx, BabylonJS=71xx, NestJS=80xx, Plottable=81xx
map_test_id_to_feature() {
    local tid="$1"
    if [ "$tid" -ge 5000 ] && [ "$tid" -le 5099 ]; then echo "TOOL-HAPPY-HUGO"
    elif [ "$tid" -ge 5100 ] && [ "$tid" -le 5199 ]; then echo "TOOL-HAPPY-BADGER"
    elif [ "$tid" -ge 5200 ] && [ "$tid" -le 5399 ]; then echo "TOOL-HAPPY-GIN"
    elif [ "$tid" -ge 6000 ] && [ "$tid" -le 6099 ]; then echo "TOOL-HAPPY-FLASK"
    elif [ "$tid" -ge 6100 ] && [ "$tid" -le 6299 ]; then echo "TOOL-HAPPY-PANDAS"
    elif [ "$tid" -ge 7000 ] && [ "$tid" -le 7099 ]; then echo "TOOL-HAPPY-EXPRESS"
    elif [ "$tid" -ge 7100 ] && [ "$tid" -le 7299 ]; then echo "TOOL-HAPPY-BABYLONJS"
    elif [ "$tid" -ge 8000 ] && [ "$tid" -le 8099 ]; then echo "TOOL-HAPPY-NESTJS"
    elif [ "$tid" -ge 8100 ] && [ "$tid" -le 8299 ]; then echo "TOOL-HAPPY-PLOTTABLE"
    fi
}

# Auto-detect TOOL-HAPPY feature dir(s) from test IDs in the 5000+ range.
# When test IDs span multiple projects, sets MULTI_FEATURE_FILTERS (space-separated)
# instead of a single FEATURE_FILTER, enabling cross-project mode.
MULTI_FEATURE_FILTERS=""
if [ -n "$SPECIFIC_TESTS" ] && [ -z "$FEATURE_FILTER" ]; then
    _all_tests=($(expand_test_spec "$SPECIFIC_TESTS"))
    _features_list=""
    for tid in "${_all_tests[@]}"; do
        auto_feature=$(map_test_id_to_feature "$tid")
        if [ -n "$auto_feature" ]; then
            # Deduplicate: only add if not already in the list (bash 3 compatible)
            case " $_features_list " in
                *" $auto_feature "*)
                    ;; # already present
                *)
                    _features_list="$_features_list $auto_feature"
                    ;;
            esac
        fi
    done
    # Trim leading space
    _features_list="${_features_list# }"

    # Count unique features
    _feature_count=$(echo "$_features_list" | wc -w | tr -d ' ')
    if [ "$_feature_count" -eq 1 ]; then
        # Single project — use existing single-feature path
        FEATURE_FILTER="$_features_list"
        echo -e "${CYAN}Auto-detected feature from test IDs: ${BOLD}$FEATURE_FILTER${NC}"
    elif [ "$_feature_count" -gt 1 ]; then
        # Multiple projects — store for cross-project loading
        MULTI_FEATURE_FILTERS="$_features_list"
        echo -e "${CYAN}Auto-detected $_feature_count features from test IDs: ${BOLD}${MULTI_FEATURE_FILTERS}${NC}"
    fi
fi

# ==============================================================================
# UTILITY FUNCTIONS
# ==============================================================================

# Get current time in milliseconds
get_time_ms() {
    python3 -c 'import time; print(int(time.time() * 1000))'
}

# ==============================================================================
# LOCAL TEST MODE
# ==============================================================================

run_local_tests() {
    echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  CRS Integration Tests - Local Mode${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
    echo ""

    local test_args=""
    if [ -n "$SPECIFIC_TESTS" ]; then
        # Map test numbers to Go test names
        case "$SPECIFIC_TESTS" in
            *1*|*2*|*3*)
                test_args="$test_args -run TestSession"
                ;;
            *4*|*5*|*6*)
                test_args="$test_args -run TestPersistence"
                ;;
            *7*|*8*|*9*)
                test_args="$test_args -run TestGraph"
                ;;
            # ... add more mappings as needed
        esac
    fi

    cd "$SCRIPT_DIR/.."
    go test -v ./services/trace/...${test_args:+ $test_args}
}

# ==============================================================================
# GLOBAL VARIABLES FOR TEST TRACKING
# ==============================================================================

declare -a DETAILED_RESULTS=()
FIRST_TEST_RUNTIME=0
LAST_TEST_RESULT="{}"


# ==============================================================================
# TEST DEFINITIONS (Will be migrated to YAML in Phases 3-5)
# ==============================================================================

declare -a CRS_TESTS=(
    # === SESSION RESTORE (GR-36) ===
    # These tests verify learned state persists across sessions

    # Test 1: Learn something in session 1, verify it persists
    "SESSION_RESTORE|session1|What is the main function in this codebase?|COMPLETE"

    # Test 2: Session 2 should restore and remember previous context
    "SESSION_RESTORE|session2_restore|Based on our previous conversation about main, what does it import?|COMPLETE"

    # Test 3: Verify proof numbers are restored (faster queries)
    "SESSION_RESTORE|session2_speed|What functions does main call?|COMPLETE|faster_than_first"

    # === DISK PERSISTENCE (GR-33) ===
    # These verify checkpoint save/load works

    # Test 4: Trigger checkpoint save
    "PERSISTENCE|checkpoint_save|Analyze the api package and remember the key types|COMPLETE"

    # Test 5: Verify checkpoint exists on disk
    "PERSISTENCE|checkpoint_verify|INTERNAL:verify_checkpoint_exists|COMPLETE"

    # Test 6: Restore from checkpoint after crash simulation
    "PERSISTENCE|checkpoint_restore|INTERNAL:restart_and_verify_state|COMPLETE"

    # === GRAPH SNAPSHOTS (GR-28) ===
    # These test graph state capture

    # Test 7: Build graph and verify snapshot
    "GRAPH|snapshot_create|Find all callers of the main function|COMPLETE"

    # Test 8: Verify graph context in events
    "GRAPH|event_context|INTERNAL:verify_event_graph_context|COMPLETE"

    # Test 9: Verify graph generation tracking
    "GRAPH|generation_track|Find callees of parseConfig|COMPLETE|generation_incremented"

    # === ANALYTICS ROUTING (GR-31) ===
    # These verify analytics tools route through CRS

    # Test 10: Run hotspots analysis
    "ANALYTICS|hotspots|Find the hotspots in this codebase|COMPLETE|analytics_recorded"

    # Test 11: Run dead code analysis
    "ANALYTICS|dead_code|Find any dead code in this project|COMPLETE|analytics_recorded"

    # Test 12: Run cycle detection
    "ANALYTICS|cycles|Are there any dependency cycles?|COMPLETE|analytics_recorded"

    # === DELTA HISTORY (GR-35) ===
    # These verify delta recording

    # Test 13: Verify deltas are recorded
    "HISTORY|delta_record|INTERNAL:verify_delta_count|COMPLETE"

    # Test 14: Verify history ringbuffer limits
    "HISTORY|ringbuffer|INTERNAL:verify_history_limit|COMPLETE"

    # Test 15: Verify delta replay works
    "HISTORY|replay|INTERNAL:replay_and_verify|COMPLETE"

    # === GR-01: GRAPH INDEX OPTIMIZATION ===
    # These tests verify graph tools use SymbolIndex O(1) lookup instead of O(V) scan

    # Test 16: Verify find_callers returns results correctly
    "GRAPH_INDEX|find_callers_basic|Find all callers of the Setup function|COMPLETE|graph_tool_used"

    # Test 17: Verify find_callees returns results correctly
    "GRAPH_INDEX|find_callees_basic|Find all functions called by main|COMPLETE|graph_tool_used"

    # Test 18: Verify find_implementations returns results correctly
    "GRAPH_INDEX|find_impls_basic|Find all implementations of the Handler interface|COMPLETE|graph_tool_used"

    # Test 19: Performance - second query should be fast (index warmed)
    "GRAPH_INDEX|perf_warm|Find callers of Execute in this codebase|COMPLETE|fast_execution"

    # Test 20: Verify OTel spans capture index usage (check logs for index_used=true)
    "GRAPH_INDEX|otel_trace|INTERNAL:verify_index_span_attribute|COMPLETE"

    # Test 21: Edge case - symbol not found should return quickly (O(1) fail fast)
    "GRAPH_INDEX|not_found_fast|Find callers of NonExistentFunctionXYZ123|COMPLETE|fast_not_found"

    # === GR-40: GO INTERFACE IMPLEMENTATION DETECTION ===
    # These tests verify that find_implementations works for Go code
    # Pre-GR-40: These tests are expected to FAIL (empty results, Grep fallback)
    # Post-GR-40: These tests should PASS (correct implementations found)

    # Test 22: Basic interface implementation - should find concrete types
    "GO_INTERFACE|basic_impl|Find all implementations of the Handler interface in this Go codebase|COMPLETE|implementations_found"

    # Test 23: Interface with multiple implementations
    "GO_INTERFACE|multi_impl|What types implement the Service interface?|COMPLETE|implementations_found"

    # Test 24: Empty interface (interface{}/any) - should handle gracefully
    "GO_INTERFACE|empty_interface|Find implementations of the Reader interface|COMPLETE|implementations_found"

    # Test 25: Verify no Grep fallback - should use graph tools only
    "GO_INTERFACE|no_grep_fallback|List all types that implement Closer|COMPLETE|no_grep_used"

    # Test 26: Verify EdgeTypeImplements exists in graph (internal check)
    "GO_INTERFACE|edge_exists|INTERNAL:verify_implements_edges|COMPLETE"

    # Test 27: Performance - implementation lookup should be O(k) not O(V)
    "GO_INTERFACE|perf_check|Find implementations of the Writer interface|COMPLETE|fast_execution"

    # === EXISTENCE TESTS (Tests for things that EXIST in AleutianOrchestrator) ===
    # These tests verify graph tools work when the target actually exists
    # GR-41: Added to validate call edge extraction works correctly

    # Test 28: find_callers for function that HAS callers (getDatesToProcess called by main)
    "GRAPH_INDEX|find_callers_exists|Find all callers of the getDatesToProcess function|COMPLETE|graph_tool_used"

    # Test 29: find_references for struct that EXISTS (Handler is a struct, not interface)
    "GRAPH_INDEX|find_refs_exists|Find all references to the Handler type|COMPLETE|graph_tool_used"

    # Test 30: find_callees for function that HAS callees (main calls multiple functions)
    "GRAPH_INDEX|find_callees_exists|Find all functions called by the main function|COMPLETE|graph_tool_used"

    # === GR-12/GR-13: PAGERANK ALGORITHM & find_important TOOL ===
    # These tests verify PageRank-based importance ranking is working

    # Test 31: Basic find_important query - should use PageRank not degree-based
    "PAGERANK|basic|What are the most important functions in this codebase?|COMPLETE|pagerank_used"

    # Test 32: find_important with top parameter
    "PAGERANK|top_param|Find the top 5 most important symbols|COMPLETE|pagerank_used"

    # Test 33: Comparison query - should mention PageRank vs degree difference
    "PAGERANK|compare|Which functions have the highest PageRank score?|COMPLETE|pagerank_used"

    # Test 34: Verify PageRank converges (internal check)
    "PAGERANK|convergence|INTERNAL:verify_pagerank_convergence|COMPLETE"

    # Test 35: Performance - PageRank should complete within reasonable time
    "PAGERANK|perf_check|Find the most architecturally important functions using PageRank|COMPLETE|fast_pagerank"

    # === GR-PHASE1: INTEGRATION TEST QUALITY FIXES ===
    # These tests verify the quality and efficiency issues identified in Phase 0-1 testing
    # TDD: These tests define expected behavior BEFORE fixes are implemented

    # Test 36: P0 - Empty response warnings should be minimal (< 50 total)
    "QUALITY|empty_response|What is the entry point of this codebase?|COMPLETE|empty_response_threshold"

    # Test 37: P0 - Average test runtime should be reasonable (< 15s for simple queries)
    "QUALITY|runtime_check|List the main packages in this project|COMPLETE|avg_runtime_threshold"

    # Test 38: P1 - Circuit breaker should fire consistently for all tools at threshold
    "QUALITY|cb_consistency|INTERNAL:verify_cb_threshold_consistency|COMPLETE"

    # Test 39: P1 - CRS speedup verification (session 2 faster than session 1)
    "QUALITY|crs_speedup|What does the main function do?|COMPLETE|crs_speedup_verified"

    # Test 40: P2 - Not-found queries should be fast (< 5 seconds)
    "QUALITY|not_found_fast|Find the function named CompletelyNonExistentXYZ999|COMPLETE|fast_not_found_strict"

    # Test 41: P2 - Debug endpoint /debug/crs should be available
    "QUALITY|debug_crs|INTERNAL:verify_debug_crs_endpoint|COMPLETE"

    # Test 42: P2 - Debug endpoint /debug/history should be available
    "QUALITY|debug_history|INTERNAL:verify_debug_history_endpoint|COMPLETE"

    # Test 43: P2 - PageRank convergence should be logged
    "QUALITY|pr_convergence|INTERNAL:verify_pagerank_convergence_logged|COMPLETE"

    # Test 44: P3 - Response should include [file:line] citations
    "QUALITY|citations|Where is the Handler type defined?|COMPLETE|citations_present"

    # === GR-06 to GR-09: SECONDARY INDEXES ===
    # These tests verify secondary indexes are working correctly
    # NOTE: Test 45 builds the graph first, then 46-49 verify indexes

    # Test 45: Build graph first (prerequisite for index verification)
    "SECONDARY_INDEX|build_graph|Find the function named main in this codebase|COMPLETE|graph_tool_used"

    # Test 46: GR-06 - Verify nodesByName index exists and has data
    "SECONDARY_INDEX|nodes_by_name|INTERNAL:verify_nodes_by_name_index|COMPLETE"

    # Test 47: GR-07 - Verify nodesByKind index via /debug/graph/stats
    "SECONDARY_INDEX|nodes_by_kind|INTERNAL:verify_nodes_by_kind_index|COMPLETE"

    # Test 48: GR-08 - Verify edgesByType index via /debug/graph/stats
    "SECONDARY_INDEX|edges_by_type|INTERNAL:verify_edges_by_type_index|COMPLETE"

    # Test 49: GR-09 - Verify edgesByFile index exists (RemoveFile uses it)
    "SECONDARY_INDEX|edges_by_file|INTERNAL:verify_edges_by_file_index|COMPLETE"

    # === GR-10: QUERY CACHING WITH LRU ===
    # These tests verify query caching is working correctly
    # TDD: Tests added BEFORE implementation

    # Test 50: First callers query (should populate cache)
    "QUERY_CACHE|cache_populate|Find all callers of the main function|COMPLETE|cache_miss_expected"

    # Test 51: Second identical callers query (should hit cache)
    "QUERY_CACHE|cache_hit|Find all callers of the main function|COMPLETE|cache_hit_expected"

    # Test 52: Verify cache stats endpoint returns data
    "QUERY_CACHE|cache_stats|INTERNAL:verify_cache_stats_endpoint|COMPLETE"

    # Test 53: Cache invalidation on graph rebuild (internal)
    "QUERY_CACHE|cache_invalidation|INTERNAL:verify_cache_invalidation|COMPLETE"

    # Test 54: Performance - cached query should be faster than uncached
    "QUERY_CACHE|cache_perf|Find callees of parseConfig|COMPLETE|cache_speedup_expected"

    # === GR-11: PARALLEL BFS FOR WIDE GRAPHS ===
    # These tests verify parallel BFS is working correctly
    # TDD: Tests added BEFORE implementation

    # Test 55: Parallel BFS returns same results as sequential (correctness)
    "PARALLEL_BFS|correctness|Find the complete call graph starting from main|COMPLETE|parallel_correctness"

    # Test 56: Verify parallel mode is enabled for wide graphs (threshold check)
    "PARALLEL_BFS|threshold|INTERNAL:verify_parallel_threshold|COMPLETE"

    # Test 57: Performance - parallel should be faster for wide graph traversal
    "PARALLEL_BFS|speedup|Get the full call chain from main to all functions it reaches|COMPLETE|parallel_speedup"

    # Test 58: Context cancellation works correctly in parallel mode
    "PARALLEL_BFS|cancellation|INTERNAL:verify_parallel_context_cancellation|COMPLETE"

    # Test 59: Race detector verification (internal - run with -race flag)
    "PARALLEL_BFS|race_free|INTERNAL:verify_no_race_conditions|COMPLETE"

    # === GR-14: LOUVAIN COMMUNITY DETECTION ===
    # These tests verify community detection is working correctly
    # TDD: Tests added BEFORE implementation

    # Test 60: Basic community detection query - should find natural code modules
    "COMMUNITY|basic|Find the natural communities or modules in this codebase|COMPLETE|communities_found"

    # Test 61: find_communities tool should be used (not fallback to grep)
    "COMMUNITY|tool_used|What are the main architectural modules in this code?|COMPLETE|find_communities_used"

    # Test 62: Verify modularity score is calculated and reasonable (internal)
    "COMMUNITY|modularity|INTERNAL:verify_community_modularity|COMPLETE"

    # Test 63: CRS integration - community detection should record TraceStep
    "COMMUNITY|crs_integration|INTERNAL:verify_community_crs_recording|COMPLETE"

    # Test 64: Performance - community detection should complete in reasonable time
    "COMMUNITY|perf_check|Detect all code communities and their relationships|COMPLETE|fast_community_detection"

    # === GR-15: find_communities TOOL ===
    # These tests verify the find_communities tool is properly exposed and integrated

    # Test 65: Basic find_communities tool query
    "FIND_COMMUNITIES|basic|What are the natural module boundaries in this codebase?|COMPLETE|find_communities_tool_used"

    # Test 66: find_communities with resolution parameter
    "FIND_COMMUNITIES|resolution|Find fine-grained code clusters using high resolution|COMPLETE|find_communities_params"

    # Test 67: Cross-package community detection
    "FIND_COMMUNITIES|cross_pkg|Which code communities span multiple packages?|COMPLETE|cross_package_found"

    # Test 68: CRS trace step recording for tool
    "FIND_COMMUNITIES|crs_trace|INTERNAL:verify_find_communities_crs|COMPLETE"

    # Test 69: Modularity quality label in output
    "FIND_COMMUNITIES|quality_label|INTERNAL:verify_modularity_quality_label|COMPLETE"

    # === GR-16a: ARTICULATION POINTS ===
    # These tests verify articulation point (cut vertex) detection using Tarjan's algorithm

    # Test 70: Basic articulation point detection
    "ARTICULATION|basic|Find the single points of failure in this codebase|COMPLETE|articulation_points_found"

    # Test 71: CRS trace step recording for articulation points
    "ARTICULATION|crs_trace|INTERNAL:verify_articulation_crs_recording|COMPLETE"

    # Test 72: Performance check - should complete in reasonable time
    "ARTICULATION|perf_check|Find architectural bottlenecks that are single points of failure|COMPLETE|fast_articulation_detection"

    # === GR-16b: DOMINATOR TREES ===
    # These tests verify dominator tree computation using Cooper-Harvey-Kennedy algorithm

    # Test 73: Basic dominator query - find all dominators of a function
    "DOMINATOR|basic|What functions must be called before reaching the main function?|COMPLETE|dominators_found"

    # Test 74: CRS trace step recording for dominator analysis
    "DOMINATOR|crs_trace|INTERNAL:verify_dominator_crs_recording|COMPLETE"

    # Test 75: Convergence verification - algorithm should converge quickly for well-structured code
    "DOMINATOR|convergence|INTERNAL:verify_dominator_convergence|COMPLETE"

    # Test 76: Performance check - should complete in reasonable time
    "DOMINATOR|perf_check|Find the mandatory call sequence from entry to the Handler|COMPLETE|fast_dominator_detection"

    # === GR-16c: POST-DOMINATOR TREES ===
    # These tests verify post-dominator tree computation (dual of dominators)

    # Test 77: Basic post-dominator query - find what must happen after a function
    "POST_DOMINATOR|basic|What functions must be called after the Handler function returns?|COMPLETE|post_dominators_found"

    # Test 78: CRS trace step recording for post-dominator analysis
    "POST_DOMINATOR|crs_trace|INTERNAL:verify_post_dominator_crs_recording|COMPLETE"

    # === GR-16d: DOMINANCE FRONTIER ===
    # These tests verify dominance frontier computation (merge points where control converges)

    # Test 79: Basic dominance frontier query - find merge points
    "DOMINANCE_FRONTIER|basic|Find the merge points in the control flow where different paths converge|COMPLETE|merge_points_found"

    # Test 80: CRS trace step recording for dominance frontier analysis
    "DOMINANCE_FRONTIER|crs_trace|INTERNAL:verify_dominance_frontier_crs_recording|COMPLETE"

    # === GR-16e: CONTROL DEPENDENCE ===
    # These tests verify control dependence computation (what conditionals control execution)

    # Test 81: Basic control dependence query - find what controls a function's execution
    "CONTROL_DEPENDENCE|basic|Find what conditionals control whether the Handler function executes|COMPLETE|control_dependencies_found"

    # Test 82: CRS trace step recording for control dependence analysis
    "CONTROL_DEPENDENCE|crs_trace|INTERNAL:verify_control_dependence_crs_recording|COMPLETE"

    # === GR-16f: NATURAL LOOP DETECTION ===
    # These tests verify natural loop detection via back edges and dominator analysis

    # Test 83: Basic loop detection - find recursive patterns and back edges
    "LOOP_DETECTION|basic|Find all recursive call patterns and loops in this codebase|COMPLETE|loops_found"

    # Test 84: Loop nesting hierarchy - verify nested loops are detected correctly
    "LOOP_DETECTION|nesting|What is the loop nesting structure in the main execution path?|COMPLETE|loop_nesting_found"

    # Test 85: CRS trace step recording for loop detection
    "LOOP_DETECTION|crs_trace|INTERNAL:verify_loop_detection_crs_recording|COMPLETE"

    # === GR-16g: LOWEST COMMON DOMINATOR ===
    # These tests verify LCD computation (finding shared mandatory dependencies)

    # Test 86: Basic LCD query - find common dominator of two functions
    "LCD|basic|What is the common dependency between the Handler and Middleware functions?|COMPLETE|lcd_found"

    # Test 87: CRS trace step recording for LCD analysis
    "LCD|crs_trace|INTERNAL:verify_lcd_crs_recording|COMPLETE"

    # === GR-16h: SESE REGION DETECTION ===
    # These tests verify SESE (Single-Entry Single-Exit) region detection for refactoring

    # Test 88: Basic SESE detection - find extractable code regions
    "SESE|basic|What code regions can be safely extracted into separate functions?|COMPLETE|sese_regions_found"

    # Test 89: SESE hierarchy - verify nested region detection
    "SESE|hierarchy|Show me the hierarchy of extractable code regions|COMPLETE|sese_hierarchy"

    # Test 90: CRS trace step recording for SESE analysis
    "SESE|crs_trace|INTERNAL:verify_sese_crs_recording|COMPLETE"

    # === GR-17a: find_articulation_points TOOL ===
    # These tests verify the find_articulation_points tool is properly exposed and integrated

    # Test 91: Basic find_articulation_points tool query
    "FIND_ARTICULATION|basic|What are the single points of failure in this codebase?|COMPLETE|find_articulation_points_tool_used"

    # Test 92: find_articulation_points with include_bridges parameter
    "FIND_ARTICULATION|bridges|Find critical bottleneck functions and the critical edges connecting them|COMPLETE|find_articulation_points_bridges"

    # Test 93: CRS trace step recording for tool
    "FIND_ARTICULATION|crs_trace|INTERNAL:verify_find_articulation_points_crs|COMPLETE"

    # === GR-17b: find_dominators TOOL ===
    # These tests verify the find_dominators tool is properly exposed and integrated

    # Test 94: Basic find_dominators tool query
    "FIND_DOMINATORS|basic|What functions dominate the NewUploadFromAPI function?|COMPLETE|find_dominators_tool_used"

    # Test 95: find_dominators with show_tree parameter
    "FIND_DOMINATORS|tree|Show the dominator tree starting from main|COMPLETE|find_dominators_tree"

    # Test 96: CRS trace step recording for find_dominators tool
    "FIND_DOMINATORS|crs_trace|INTERNAL:verify_find_dominators_crs|COMPLETE"

    # === GR-17d: find_merge_points TOOL ===
    # These tests verify the find_merge_points tool finds convergence points

    # Test 97: Basic find_merge_points tool query
    "FIND_MERGE_POINTS|basic|Where do different code paths converge in this codebase?|COMPLETE|find_merge_points_tool_used"

    # Test 98: find_merge_points with specific sources
    "FIND_MERGE_POINTS|sources|Find merge points for Handler and Middleware functions|COMPLETE|find_merge_points_sources"

    # Test 99: CRS trace step recording for find_merge_points tool
    "FIND_MERGE_POINTS|crs_trace|INTERNAL:verify_find_merge_points_crs|COMPLETE"

    # === GR-17e: find_loops TOOL ===
    # These tests verify the find_loops tool detects natural loops and recursion patterns

    # Test 100: Basic find_loops tool query
    "FIND_LOOPS|basic|Find recursive functions and call loops in this codebase|COMPLETE|find_loops_tool_used"

    # Test 101: find_loops with min_size parameter
    "FIND_LOOPS|min_size|Find mutual recursion patterns with at least 2 functions involved|COMPLETE|find_loops_min_size"

    # Test 102: CRS trace step recording for find_loops tool
    "FIND_LOOPS|crs_trace|INTERNAL:verify_find_loops_crs|COMPLETE"

    # === GR-17f: find_common_dependency TOOL ===
    # These tests verify the find_common_dependency tool finds shared dependencies (LCD)

    # Test 103: Basic find_common_dependency tool query
    "FIND_COMMON_DEPENDENCY|basic|What is the common dependency between Handler and Middleware functions?|COMPLETE|find_common_dependency_tool_used"

    # Test 104: find_common_dependency with entry point
    "FIND_COMMON_DEPENDENCY|entry|Find the lowest common dominator of Parser and Writer from main|COMPLETE|find_common_dependency_entry"

    # Test 105: CRS trace step recording for find_common_dependency tool
    "FIND_COMMON_DEPENDENCY|crs_trace|INTERNAL:verify_find_common_dependency_crs|COMPLETE"

    # === GR-17c: find_control_dependencies TOOL ===
    # These tests verify the find_control_dependencies tool shows which conditionals control execution

    # Test 106: Basic find_control_dependencies tool query
    "FIND_CONTROL_DEPS|basic|What conditionals control whether HandleRequest executes|COMPLETE|find_control_deps_tool_used"

    # Test 107: find_control_dependencies with depth parameter
    "FIND_CONTROL_DEPS|depth|Show control dependencies for Process function with depth 3|COMPLETE|find_control_deps_depth"

    # Test 108: CRS trace step recording for find_control_dependencies tool
    "FIND_CONTROL_DEPS|crs_trace|INTERNAL:verify_find_control_deps_crs|COMPLETE"

    # === GR-17g: find_extractable_regions TOOL ===
    # These tests verify the find_extractable_regions tool identifies SESE regions for refactoring

    # Test 109: Basic find_extractable_regions tool query
    "FIND_EXTRACTABLE|basic|Find code regions that can be safely extracted into separate functions|COMPLETE|find_extractable_tool_used"

    # Test 110: find_extractable_regions with size parameters
    "FIND_EXTRACTABLE|size|Find extractable regions between 5 and 30 nodes in size|COMPLETE|find_extractable_size"

    # Test 111: CRS trace step recording for find_extractable_regions tool
    "FIND_EXTRACTABLE|crs_trace|INTERNAL:verify_find_extractable_crs|COMPLETE"

    # === GR-17h: check_reducibility TOOL ===
    # These tests verify the check_reducibility tool analyzes graph structure quality

    # Test 112: Basic check_reducibility tool query
    "CHECK_REDUCIBILITY|basic|Check if this codebase has well-structured control flow|COMPLETE|check_reducibility_tool_used"

    # Test 113: check_reducibility with irreducible region details
    "CHECK_REDUCIBILITY|details|Show any complex or poorly structured code regions|COMPLETE|check_reducibility_details"

    # Test 114: CRS trace step recording for check_reducibility tool
    "CHECK_REDUCIBILITY|crs_trace|INTERNAL:verify_check_reducibility_crs|COMPLETE"

    # === GR-18a: find_critical_path TOOL ===
    # These tests verify the find_critical_path tool shows mandatory call sequences

    # Test 115: Basic find_critical_path tool query
    "FIND_CRITICAL_PATH|basic|What is the mandatory call sequence to reach ExecuteQuery|COMPLETE|find_critical_path_tool_used"

    # Test 116: find_critical_path with entry point
    "FIND_CRITICAL_PATH|entry|Show the critical path from StartServer to HandleRequest|COMPLETE|find_critical_path_entry"

    # Test 117: CRS trace step recording for find_critical_path tool
    "FIND_CRITICAL_PATH|crs_trace|INTERNAL:verify_find_critical_path_crs|COMPLETE"

    # === GR-19a: Heavy-Light Decomposition Construction ===
    # These tests verify the HLD construction algorithm and CRS integration

    # Test 115: Basic HLD construction on small tree
    "HLD_CONST|basic|INTERNAL:verify_hld_basic_construction|COMPLETE"

    # Test 116: HLD construction with CRS integration
    "HLD_CONST|crs|INTERNAL:verify_hld_crs_integration|COMPLETE"

    # Test 117: HLD determinism - same graph produces same HLD structure
    "HLD_CONST|determinism|INTERNAL:verify_hld_determinism|COMPLETE"

    # === GR-19b: Segment Tree for Path/Subtree Aggregations ===
    # These tests verify segment tree construction, queries, and CRS integration

    # Test 118: Basic segment tree construction with SUM aggregation
    "SEGTREE|build_sum|INTERNAL:verify_segtree_build_sum|COMPLETE"

    # Test 119: Segment tree range queries
    "SEGTREE|query|INTERNAL:verify_segtree_query|COMPLETE"

    # Test 120: Segment tree updates and range updates
    "SEGTREE|update|INTERNAL:verify_segtree_update|COMPLETE"

    # Test 121: Segment tree with CRS integration
    "SEGTREE|crs|INTERNAL:verify_segtree_crs_integration|COMPLETE"
)

# ==============================================================================
# TEST EXECUTION
# ==============================================================================

run_crs_test() {
    local test_spec="$1"
    local test_num="$2"

    IFS='|' read -r category session_id query expected_state extra_check <<< "$test_spec"

    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo -e "${YELLOW}Test $test_num [$category]: $session_id${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo -e "  Query: ${query:0:80}..."

    # Handle internal verification tests
    if [[ "$query" == INTERNAL:* ]]; then
        # GR-39 Issue 5: Pass test_num to internal tests for proper result tracking
        run_internal_test "$category" "${query#INTERNAL:}" "$expected_state" "$test_num"
        return $?
    fi

    # Run agent query using the remote project path
    local start_time=$(get_time_ms)

    # Use REMOTE_PROJECT_PATH (set by setup_remote/sync_project_to_remote)
    local remote_project="${REMOTE_PROJECT_PATH:-/home/$REMOTE_USER/trace_test/$(basename "$PROJECT_TO_ANALYZE")}"

    # Build JSON payload safely using jq to handle special characters (apostrophes, quotes, etc.)
    local json_payload
    json_payload=$(jq -n \
        --arg project_root "$remote_project" \
        --arg query "$query" \
        --arg model "$OLLAMA_MODEL" \
        --arg router_model "$ROUTER_MODEL" \
        '{project_root: $project_root, query: $query, model: $model, router_model: $router_model}')

    # XC-6 fix: Use base64 encoding to avoid all shell quoting issues.
    # Queries containing apostrophes (Flask's, NestJS's) caused 'unexpected EOF'
    # errors when embedded via single-quote escaping through SSH.
    local b64_payload
    b64_payload=$(printf '%s' "$json_payload" | base64 | tr -d '\n')
    local response=$(ssh_cmd "printf '%s' '$b64_payload' | base64 -d | curl -s -X POST 'http://localhost:8080/v1/trace/agent/run' -H 'Content-Type: application/json' -H 'X-Session-ID: crs_test_${session_id}' -d @- --max-time 300")

    local end_time=$(get_time_ms)
    local duration=$((end_time - start_time))

    # Validate response
    if [ -z "$response" ] || ! echo "$response" | jq . > /dev/null 2>&1; then
        echo -e "  ${RED}✗ FAILED - Invalid or empty response${NC}"
        return 1
    fi

    local state=$(echo "$response" | jq -r '.state // "ERROR"')
    local session_actual=$(echo "$response" | jq -r '.session_id // "unknown"')
    local steps_taken=$(echo "$response" | jq -r '.steps_taken // 0')
    local tokens_used=$(echo "$response" | jq -r '.tokens_used // 0')
    local agent_response=$(echo "$response" | jq -r '.response // ""')

    echo ""
    echo -e "  ${BLUE}─── Agent Response ───${NC}"
    echo -e "  State: $state | Steps: $steps_taken | Tokens: $tokens_used | Time: ${duration}ms"
    echo ""
    # Show truncated response
    echo "$agent_response" | head -20 | sed 's/^/    /'
    if [ $(echo "$agent_response" | wc -l) -gt 20 ]; then
        echo -e "    ${YELLOW}... (truncated, $(echo "$agent_response" | wc -l) total lines)${NC}"
    fi

    # Fetch and display CRS reasoning trace
    local trace_json="{}"
    local crs_details=""
    if [ "$session_actual" != "unknown" ]; then
        local trace_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_actual/reasoning'" 2>/dev/null)
        if echo "$trace_response" | jq . > /dev/null 2>&1; then
            trace_json="$trace_response"
            local trace_count=$(echo "$trace_response" | jq '.total_steps // 0')

            echo ""
            echo -e "  ${BLUE}─── CRS Reasoning Trace ($trace_count steps) ───${NC}"

            # Show each reasoning step with details
            echo "$trace_response" | jq -r '.trace[] |
                "    [\(.timestamp // "?")] \(.action // "unknown")" +
                (if .tool then " → Tool: \(.tool)" else "" end) +
                (if .target then " → Target: \(.target)" else "" end) +
                (if .result then " → Result: \(.result | tostring | .[0:60])" else "" end) +
                (if .error and .error != "" then " ⚠ Error: \(.error)" else "" end)
            ' 2>/dev/null | head -30

            # Show tool call summary with threshold warnings
            echo ""
            echo -e "  ${BLUE}─── Tool Usage Summary ───${NC}"
            local tool_summary=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action == "tool_call" or .action == "tool_call_forced")] |
                group_by(.tool) |
                map({tool: .[0].tool, count: length}) |
                sort_by(-.count) |
                .[] | "    \(.tool): \(.count) call(s)" +
                    (if .count > 2 then " ⚠ EXCEEDS THRESHOLD" else "" end)
            ' 2>/dev/null)
            if [ -n "$tool_summary" ]; then
                echo "$tool_summary"
            else
                echo "    (no tool calls recorded)"
            fi

            # GR-39b: Show circuit breaker events
            local cb_events=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action == "circuit_breaker")] |
                if length > 0 then
                    .[] | "    🛑 \(.tool // .target): \(.error // "fired")"
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$cb_events" != "null" ] && [ -n "$cb_events" ]; then
                echo ""
                echo -e "  ${YELLOW}─── Circuit Breaker Events (GR-39b) ───${NC}"
                echo "$cb_events"
            fi

            # CB-30c: Show semantic repetition blocks
            local sem_blocks=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.error != null and (.error | test("[Ss]emantic repetition|similar to")))] |
                if length > 0 then
                    .[] | "    🔄 \(.tool // .target): \(.error | .[0:80])"
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$sem_blocks" != "null" ] && [ -n "$sem_blocks" ]; then
                echo ""
                echo -e "  ${YELLOW}─── Semantic Repetition Blocks (CB-30c) ───${NC}"
                echo "$sem_blocks"
            fi

            # GR-41b: Show LLM prompt/response info from llm_call steps
            local llm_calls=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action == "llm_call")] |
                if length > 0 then
                    .[] |
                    "    [LLM Call] msgs=\(.metadata.message_count // "?") tokens_out=\(.metadata.output_tokens // "?")" +
                    (if .metadata.last_user_message and (.metadata.last_user_message | length) > 0 then
                        "\n      Query: \(.metadata.last_user_message | .[0:100])..."
                    else "" end) +
                    (if .metadata.content_preview and (.metadata.content_preview | length) > 0 then
                        "\n      Response: \(.metadata.content_preview | .[0:100])..."
                    else "" end)
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$llm_calls" != "null" ] && [ -n "$llm_calls" ]; then
                echo ""
                echo -e "  ${CYAN}─── LLM Prompts & Responses (GR-41b) ───${NC}"
                echo "$llm_calls"
            fi

            # Show routing decisions
            local routing=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action == "tool_routing")] |
                if length > 0 then
                    "    Router selected: " + ([.[].target] | unique | join(", "))
                else
                    "    (no routing decisions recorded)"
                end
            ' 2>/dev/null)
            echo ""
            echo -e "  ${BLUE}─── Router Decisions ───${NC}"
            echo "$routing"

            # Show any learned clauses or CRS state changes
            local crs_state=$(echo "$trace_response" | jq -r '
                .crs_state // {} |
                if . != {} then
                    "    Clauses: \(.clauses_count // "?") | " +
                    "Generation: \(.generation // "?") | " +
                    "Proof Numbers: \(.proof_numbers_count // "?")"
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$crs_state" != "null" ] && [ -n "$crs_state" ]; then
                echo ""
                echo -e "  ${BLUE}─── CRS State ───${NC}"
                echo "$crs_state"
            fi

            # Show proof number updates (CRS-02)
            local proof_updates=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action | test("proof_update|disproven"))] |
                if length > 0 then
                    .[] | "    📊 \(.tool // .target): \(.metadata.reason // .error // "updated")"
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$proof_updates" != "null" ] && [ -n "$proof_updates" ]; then
                echo ""
                echo -e "  ${BLUE}─── Proof Number Updates (CRS-02) ───${NC}"
                echo "$proof_updates"
            fi

            # Show learning events (CDCL clauses)
            local learn_events=$(echo "$trace_response" | jq -r '
                [.trace[] | select(.action | test("learn|clause|cdcl"))] |
                if length > 0 then
                    .[] | "    📚 \(.tool // .target): \(.metadata.failure_type // .error // "learned")"
                else
                    null
                end
            ' 2>/dev/null)
            if [ "$learn_events" != "null" ] && [ -n "$learn_events" ]; then
                echo ""
                echo -e "  ${BLUE}─── CDCL Learning Events (CRS-04) ───${NC}"
                echo "$learn_events"
            fi

            # Check for repeated tool calls (potential issue)
            local repeated=$(echo "$trace_response" | jq '
                [.trace[] | select(.action == "tool_call")] |
                group_by(.tool) |
                map(select(length > 3)) |
                length
            ' 2>/dev/null)
            if [ "$repeated" -gt 0 ]; then
                echo ""
                echo -e "  ${RED}⚠ WARNING: Detected tool called >3 times (potential loop)${NC}"
                # Show which tools exceeded
                echo "$trace_response" | jq -r '
                    [.trace[] | select(.action == "tool_call")] |
                    group_by(.tool) |
                    map(select(length > 3)) |
                    .[] | "    → \(.[0].tool): \(length) calls"
                ' 2>/dev/null
            fi

            # GR-39b verification: Check if circuit breaker fired appropriately
            local tool_counts=$(echo "$trace_response" | jq '
                [.trace[] | select(.action == "tool_call" or .action == "tool_call_forced")] |
                group_by(.tool) |
                map({tool: .[0].tool, count: length}) |
                map(select(.count > 2))
            ' 2>/dev/null)
            local cb_fired=$(echo "$trace_response" | jq '[.trace[] | select(.action == "circuit_breaker")] | length' 2>/dev/null)

            if [ "$(echo "$tool_counts" | jq 'length')" -gt 0 ] && [ "$cb_fired" -eq 0 ]; then
                echo ""
                echo -e "  ${RED}⚠ GR-39b ISSUE: Tools exceeded threshold but no circuit breaker fired!${NC}"
                echo "$tool_counts" | jq -r '.[] | "    → \(.tool): \(.count) calls (threshold: 2)"'
            elif [ "$cb_fired" -gt 0 ]; then
                echo ""
                echo -e "  ${GREEN}✓ GR-39b: Circuit breaker fired $cb_fired time(s)${NC}"
            fi
        fi
    fi

    # Fetch server logs for CRS-related entries (last 50 lines since test started)
    echo ""
    echo -e "  ${BLUE}─── Server Log Analysis ───${NC}"

    # GR-39b: Check for count-based circuit breaker (filtered by session)
    local gr39b_logs=$(ssh_cmd "grep 'GR-39b' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -5" || echo "")
    if [ -n "$gr39b_logs" ]; then
        echo -e "  ${YELLOW}GR-39b (Count Circuit Breaker):${NC}"
        echo "$gr39b_logs" | sed 's/^/    /'
    fi

    # CB-30c: Check for semantic repetition (filtered by session)
    local cb30c_logs=$(ssh_cmd "grep -E 'CB-30c|[Ss]emantic repetition' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -5" || echo "")
    if [ -n "$cb30c_logs" ]; then
        echo -e "  ${YELLOW}CB-30c (Semantic Repetition):${NC}"
        echo "$cb30c_logs" | sed 's/^/    /'
    fi

    # CRS-02: Check for proof number updates (filtered by session)
    local crs02_logs=$(ssh_cmd "grep -E 'CRS-02|proof.*number|disproven' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -3" || echo "")
    if [ -n "$crs02_logs" ]; then
        echo -e "  ${YELLOW}CRS-02 (Proof Numbers):${NC}"
        echo "$crs02_logs" | sed 's/^/    /'
    fi

    # CRS-04: Check for learning events (filtered by session)
    local crs04_logs=$(ssh_cmd "grep -E 'CRS-04|learnFromFailure|CDCL' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -3" || echo "")
    if [ -n "$crs04_logs" ]; then
        echo -e "  ${YELLOW}CRS-04 (CDCL Learning):${NC}"
        echo "$crs04_logs" | sed 's/^/    /'
    fi

    # CRS-06: Check for coordinator events (filtered by session)
    local crs06_logs=$(ssh_cmd "grep -E 'CRS-06|EventCircuitBreaker|EventSemanticRepetition' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -3" || echo "")
    if [ -n "$crs06_logs" ]; then
        echo -e "  ${YELLOW}CRS-06 (Coordinator Events):${NC}"
        echo "$crs06_logs" | sed 's/^/    /'
    fi

    # Check for any errors or warnings (R2-4: filtered by session, R2-5: case-sensitive level prefix)
    local error_logs=$(ssh_cmd "grep -E '(ERROR|WARN) ' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep '$session_actual' | tail -5" || echo "")
    if [ -n "$error_logs" ]; then
        echo -e "  ${RED}Errors/Warnings:${NC}"
        echo "$error_logs" | sed 's/^/    /'
    fi

    # If no CRS logs found, mention it
    if [ -z "$gr39b_logs" ] && [ -z "$cb30c_logs" ] && [ -z "$crs02_logs" ] && [ -z "$crs04_logs" ] && [ -z "$crs06_logs" ]; then
        echo "    (no CRS-specific log entries found)"
    fi

    # Store detailed result for JSON output
    LAST_TEST_RESULT=$(jq -n \
        --arg test_num "$test_num" \
        --arg category "$category" \
        --arg session_id "$session_id" \
        --arg query "$query" \
        --arg state "$state" \
        --arg steps "$steps_taken" \
        --arg tokens "$tokens_used" \
        --arg duration "$duration" \
        --arg response "$agent_response" \
        --arg session_actual "$session_actual" \
        --argjson trace "$trace_json" \
        '{
            test: ($test_num | tonumber),
            category: $category,
            session_id: $session_id,
            query: $query,
            state: $state,
            steps_taken: ($steps | tonumber),
            tokens_used: ($tokens | tonumber),
            runtime_ms: ($duration | tonumber),
            response: $response,
            actual_session_id: $session_actual,
            crs_trace: $trace
        }')

    echo ""
    if [ "$state" = "$expected_state" ]; then
        # IT-03 C-1: Detect surrender/empty responses that technically reach COMPLETE state
        # but don't actually answer the query. These should be flagged as FAILED.
        local surrender_detected=false
        local agent_response_lower=$(echo "$agent_response" | tr '[:upper:]' '[:lower:]')
        local response_length=${#agent_response}

        # Check for empty or near-empty responses (< 20 chars after trimming)
        local trimmed_response=$(echo "$agent_response" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
        if [ ${#trimmed_response} -lt 20 ]; then
            surrender_detected=true
            echo -e "  ${RED}════ FAILED (SURRENDER) ════${NC} Response too short (${#trimmed_response} chars)"
        fi

        # Check for explicit "I don't know" / surrender patterns
        if [ "$surrender_detected" = false ]; then
            if echo "$agent_response_lower" | grep -qE "(i don.t know|i cannot|i.m unable to|i am unable to|unable to determine|cannot determine|no information available|i have no|i lack|not enough information|insufficient information|i couldn.t find|i could not find|no results|no data available)"; then
                surrender_detected=true
                echo -e "  ${RED}════ FAILED (SURRENDER) ════${NC} Agent surrendered instead of answering"
                echo "$agent_response" | head -3 | sed 's/^/    /'
            fi
        fi

        if [ "$surrender_detected" = true ]; then
            return 1
        fi

        echo -e "  ${GREEN}════ PASSED ════${NC} State: $state (${duration}ms)"

        # Run extra checks if specified (M-3: guard against undefined function)
        if [ -n "$extra_check" ] && type -t run_extra_check &>/dev/null; then
            run_extra_check "$extra_check" "$response" "$duration" "$session_actual"
        fi

        return 0
    else
        echo -e "  ${RED}════ FAILED ════${NC} Expected: $expected_state, Got: $state"
        # Show error details if available
        local error_msg=$(echo "$response" | jq -r '.error // ""')
        if [ -n "$error_msg" ] && [ "$error_msg" != "null" ]; then
            echo -e "    ${RED}Error: $error_msg${NC}"
        fi
        return 1
    fi
}


# ==============================================================================
# TOOL NAME MAPPING (for --tool flag)
# ==============================================================================

# Tool name lists (bash 3.x compatible — no associative arrays)
TOOL_NAMES=(
    find_callers find_callees find_implementations find_symbol
    find_references get_call_chain find_path find_hotspots
    find_dead_code find_cycles find_important find_communities
    find_articulation_points find_dominators find_loops find_merge_points
    find_common_dependency find_control_dependencies find_extractable_regions
    check_reducibility find_critical_path find_module_api find_weighted_criticality
)

# tool_name_to_index: returns index (0-22) for a tool name, or "" if not found
tool_name_to_index() {
    local name="$1"
    for i in "${!TOOL_NAMES[@]}"; do
        if [ "${TOOL_NAMES[$i]}" = "$name" ]; then
            echo "$i"
            return 0
        fi
    done
    echo ""
    return 1
}

# tool_index_to_name: returns tool name for an index (0-22)
tool_index_to_name() {
    local idx="$1"
    if [ "$idx" -ge 0 ] && [ "$idx" -lt ${#TOOL_NAMES[@]} ] 2>/dev/null; then
        echo "${TOOL_NAMES[$idx]}"
    else
        echo "tool_$idx"
    fi
}

# Parallel arrays: maps test array index → project_root / project name
CRS_TEST_PROJECT_ROOTS=()
CRS_TEST_PROJECT_NAMES=()

# Resolve tool filter to a list of tool indices
# Input: tool name (find_callers), index (01), or comma-separated list
# Output: space-separated list of indices
resolve_tool_indices() {
    local filter="$1"
    local indices=()

    IFS=',' read -ra parts <<< "$filter"
    for part in "${parts[@]}"; do
        part=$(echo "$part" | xargs)  # trim whitespace

        # Special aliases that expand to multiple indices
        if [ "$part" = "find_callers_all" ]; then
            indices+=(0 40 41)
            continue
        fi
        if [ "$part" = "find_callees_all" ]; then
            indices+=(1 44 45)
            continue
        fi
        if [ "$part" = "find_implementations_all" ]; then
            indices+=(2 42 43)
            continue
        fi
        if [ "$part" = "find_symbol_all" ]; then
            indices+=(3 46 47)
            continue
        fi
        if [ "$part" = "find_references_all" ]; then
            indices+=(4 48 49)
            continue
        fi
        if [ "$part" = "get_call_chain_all" ]; then
            indices+=(5 50 51)
            continue
        fi
        if [ "$part" = "find_path_all" ]; then
            indices+=(6 52 53)
            continue
        fi
        if [ "$part" = "find_hotspots_all" ]; then
            indices+=(7 54 55)
            continue
        fi
        if [ "$part" = "find_dead_code_all" ]; then
            indices+=(8 56 57)
            continue
        fi
        if [ "$part" = "find_cycles_all" ]; then
            indices+=(9 58 59)
            continue
        fi
        if [ "$part" = "find_important_all" ]; then
            indices+=(10 60 61)
            continue
        fi
        if [ "$part" = "find_communities_all" ]; then
            indices+=(11 62 63)
            continue
        fi
        if [ "$part" = "find_articulation_points_all" ]; then
            indices+=(12 64 65)
            continue
        fi
        if [ "$part" = "find_dominators_all" ]; then
            indices+=(13 66 67)
            continue
        fi
        if [ "$part" = "find_loops_all" ]; then
            indices+=(14 68 69)
            continue
        fi
        if [ "$part" = "find_merge_points_all" ]; then
            indices+=(15 70 71)
            continue
        fi
        if [ "$part" = "find_common_dependency_all" ]; then
            indices+=(16 72 73)
            continue
        fi
        if [ "$part" = "find_control_dependencies_all" ]; then
            indices+=(17 74 75)
            continue
        fi
        if [ "$part" = "find_extractable_regions_all" ]; then
            indices+=(18 76 77)
            continue
        fi
        if [ "$part" = "check_reducibility_all" ]; then
            indices+=(19 78 79)
            continue
        fi
        if [ "$part" = "find_critical_path_all" ]; then
            indices+=(20 80 81)
            continue
        fi
        if [ "$part" = "find_module_api_all" ]; then
            indices+=(21 82 83)
            continue
        fi
        if [ "$part" = "find_weighted_criticality_all" ]; then
            indices+=(22 84 85)
            continue
        fi

        if [[ "$part" =~ ^[0-9]+$ ]]; then
            # Numeric index
            indices+=("$part")
        else
            # Tool name lookup
            local idx
            idx=$(tool_name_to_index "$part")
            if [ -n "$idx" ]; then
                indices+=("$idx")
            else
                echo -e "${RED}Unknown tool: $part${NC}" >&2
                echo -e "${YELLOW}Available tools:${NC}" >&2
                for i in "${!TOOL_NAMES[@]}"; do
                    printf "  %02d  %s\n" "$i" "${TOOL_NAMES[$i]}" >&2
                done
                echo -e "${YELLOW}Aliases: <tool_name>_all runs 3 tests per project for that tool.${NC}" >&2
                echo -e "${YELLOW}  e.g., find_callers_all, find_symbol_all, find_hotspots_all, etc.${NC}" >&2
                return 1
            fi
        fi
    done

    echo "${indices[@]}"
}

# ==============================================================================
# YAML TEST LOADING
# ==============================================================================

# Load tests from YAML files based on feature and language filters
load_yaml_tests() {
    local yaml_files=()
    local yaml_dir="$SCRIPT_DIR/test_langs/features"

    # Check if YAML directory exists
    if [ ! -d "$yaml_dir" ]; then
        echo -e "${YELLOW}Warning: YAML test directory not found: $yaml_dir${NC}"
        echo -e "${YELLOW}Falling back to hardcoded test array${NC}"
        return 1
    fi

    # Check for yq
    if ! command -v yq &> /dev/null; then
        echo -e "${YELLOW}Warning: yq not found. Install with: brew install yq${NC}"
        echo -e "${YELLOW}Falling back to hardcoded test array${NC}"
        return 1
    fi

    # Build list of YAML files to load based on filters
    if [ -n "$FEATURE_FILTER" ] && [ -n "$LANGUAGE_FILTER" ]; then
        # Specific feature + language
        local feature_file="$yaml_dir/$FEATURE_FILTER/${LANGUAGE_FILTER}.yml"
        if [ -f "$feature_file" ]; then
            yaml_files+=("$feature_file")
        fi
    elif [ -n "$FEATURE_FILTER" ]; then
        # All languages for specific feature
        for lang_file in "$yaml_dir/$FEATURE_FILTER"/*.yml; do
            if [ -f "$lang_file" ]; then
                yaml_files+=("$lang_file")
            fi
        done
    elif [ -n "$LANGUAGE_FILTER" ]; then
        # All features for specific language
        for lang_file in "$yaml_dir"/*/"$LANGUAGE_FILTER.yml"; do
            if [ -f "$lang_file" ]; then
                yaml_files+=("$lang_file")
            fi
        done
    else
        # All tests
        for lang_file in "$yaml_dir"/*/*.yml; do
            if [ -f "$lang_file" ]; then
                yaml_files+=("$lang_file")
            fi
        done
    fi

    if [ ${#yaml_files[@]} -eq 0 ]; then
        echo -e "${YELLOW}Warning: No YAML test files matched filters${NC}"
        echo -e "${YELLOW}  Feature: ${FEATURE_FILTER:-all}${NC}"
        echo -e "${YELLOW}  Language: ${LANGUAGE_FILTER:-all}${NC}"
        echo -e "${YELLOW}Falling back to hardcoded test array${NC}"
        return 1
    fi

    # Extract project_root from the first YAML file's metadata
    local first_yaml="${yaml_files[0]}"
    local yaml_project_root=$(yq eval '.metadata.project_root' "$first_yaml")
    if [ "$yaml_project_root" != "null" ] && [ -n "$yaml_project_root" ]; then
        # Expand ~ to home directory
        yaml_project_root="${yaml_project_root/#\~/$HOME}"
        PROJECT_TO_ANALYZE="$yaml_project_root"
        echo -e "${GREEN}✓ Using project from YAML metadata.project_root: $PROJECT_TO_ANALYZE${NC}"
    else
        # Fallback: resolve from metadata.language + metadata.project via get_project_root()
        local yaml_lang=$(yq eval '.metadata.language' "$first_yaml")
        local yaml_project=$(yq eval '.metadata.project' "$first_yaml")
        if [ "$yaml_lang" != "null" ] && [ "$yaml_project" != "null" ]; then
            local resolved_path
            if resolved_path=$(get_project_root "$yaml_lang" "$yaml_project" 2>/dev/null); then
                PROJECT_TO_ANALYZE="$resolved_path"
                echo -e "${GREEN}✓ Resolved project from language=$yaml_lang project=$yaml_project: $PROJECT_TO_ANALYZE${NC}"
            else
                echo -e "${YELLOW}⚠ Could not resolve project path for language=$yaml_lang project=$yaml_project${NC}"
                echo -e "${YELLOW}  Set project_root in YAML metadata or TEST_PROJECT_ROOT env var${NC}"
            fi
        fi
    fi

    # Parse YAML files and build CRS_TESTS array
    CRS_TESTS=()
    CRS_TEST_IDS=()  # Parallel array: maps array index → YAML test ID
    CRS_TEST_PROJECT_ROOTS=()  # Parallel array: maps array index → project_root
    CRS_TEST_PROJECT_NAMES=()  # Parallel array: maps array index → project name
    local test_count=0

    for yaml_file in "${yaml_files[@]}"; do
        # Extract per-file metadata for project tracking
        local file_project_root=$(yq eval '.metadata.project_root' "$yaml_file")
        local file_project_name=$(yq eval '.metadata.project' "$yaml_file")
        if [ "$file_project_root" != "null" ] && [ -n "$file_project_root" ]; then
            file_project_root="${file_project_root/#\~/$HOME}"
        else
            file_project_root=""
        fi
        [ "$file_project_name" = "null" ] && file_project_name=""

        # Get number of tests in this file
        local num_tests=$(yq eval '.tests | length' "$yaml_file")

        # Extract each test
        for ((i=0; i<num_tests; i++)); do
            local test_id=$(yq eval ".tests[$i].id" "$yaml_file")
            local category=$(yq eval ".tests[$i].category" "$yaml_file")
            local name=$(yq eval ".tests[$i].name" "$yaml_file")
            local query=$(yq eval ".tests[$i].query" "$yaml_file")
            local expected_state=$(yq eval ".tests[$i].expected_state" "$yaml_file")

            # Build validation string (pipe-separated list)
            local validations=""
            local num_validations=$(yq eval ".tests[$i].validations | length" "$yaml_file")
            if [ "$num_validations" != "null" ] && [ "$num_validations" -gt 0 ]; then
                for ((v=0; v<num_validations; v++)); do
                    local val_type=$(yq eval ".tests[$i].validations[$v].type" "$yaml_file")
                    if [ -n "$validations" ]; then
                        validations="${validations}|${val_type}"
                    else
                        validations="$val_type"
                    fi
                done
            fi

            # Build pipe-delimited test string
            local test_string="$category|$name|$query|$expected_state"
            if [ -n "$validations" ]; then
                test_string="${test_string}|${validations}"
            fi

            CRS_TESTS+=("$test_string")
            CRS_TEST_IDS+=("$test_id")
            CRS_TEST_PROJECT_ROOTS+=("$file_project_root")
            CRS_TEST_PROJECT_NAMES+=("$file_project_name")
            ((test_count++))
        done
    done

    echo -e "${GREEN}Loaded $test_count tests from ${#yaml_files[@]} YAML files${NC}"
    return 0
}

# Load tests for specific tool(s) across all TOOL-HAPPY projects
# Uses TOOL_FILTER and optionally LANGUAGE_FILTER
load_tool_across_projects() {
    local yaml_dir="$SCRIPT_DIR/test_langs/features"

    if [ ! -d "$yaml_dir" ]; then
        echo -e "${RED}YAML test directory not found: $yaml_dir${NC}"
        return 1
    fi

    if ! command -v yq &> /dev/null; then
        echo -e "${RED}yq not found. Install with: brew install yq${NC}"
        return 1
    fi

    # Resolve tool names to indices
    local tool_indices
    tool_indices=$(resolve_tool_indices "$TOOL_FILTER") || return 1
    local indices_arr=($tool_indices)

    echo -e "${CYAN}Tool(s): ${TOOL_FILTER} → indices: ${tool_indices}${NC}"

    # Find all TOOL-HAPPY YAML files
    local yaml_files=()
    for feature_dir in "$yaml_dir"/TOOL-HAPPY-*; do
        if [ ! -d "$feature_dir" ]; then
            continue
        fi
        for yml_file in "$feature_dir"/*.yml; do
            if [ ! -f "$yml_file" ]; then
                continue
            fi
            # Apply language filter if set
            if [ -n "$LANGUAGE_FILTER" ]; then
                local yml_basename=$(basename "$yml_file" .yml)
                if [ "$yml_basename" != "$LANGUAGE_FILTER" ]; then
                    continue
                fi
            fi
            yaml_files+=("$yml_file")
        done
    done

    if [ ${#yaml_files[@]} -eq 0 ]; then
        echo -e "${RED}No TOOL-HAPPY YAML files found${NC}"
        return 1
    fi

    echo -e "${CYAN}Scanning ${#yaml_files[@]} YAML files for matching tests...${NC}"

    # Build test arrays — extract only matching tool indices from each file
    CRS_TESTS=()
    CRS_TEST_IDS=()
    CRS_TEST_PROJECT_ROOTS=()
    CRS_TEST_PROJECT_NAMES=()
    local test_count=0

    for yaml_file in "${yaml_files[@]}"; do
        local file_project_root=$(yq eval '.metadata.project_root' "$yaml_file")
        local file_project_name=$(yq eval '.metadata.project' "$yaml_file")
        local file_language=$(yq eval '.metadata.language' "$yaml_file")
        local file_feature=$(yq eval '.metadata.feature' "$yaml_file")
        if [ "$file_project_root" != "null" ] && [ -n "$file_project_root" ]; then
            file_project_root="${file_project_root/#\~/$HOME}"
        else
            file_project_root=""
        fi
        [ "$file_project_name" = "null" ] && file_project_name=""
        [ "$file_language" = "null" ] && file_language=""

        local num_tests=$(yq eval '.tests | length' "$yaml_file")

        for tool_idx in "${indices_arr[@]}"; do
            # Only pick from the first 23 tests (registered tools at indices 0-22)
            if [ "$tool_idx" -ge "$num_tests" ]; then
                echo -e "${YELLOW}  Skip $file_feature: only $num_tests tests, need index $tool_idx${NC}"
                continue
            fi

            local test_id=$(yq eval ".tests[$tool_idx].id" "$yaml_file")
            local category=$(yq eval ".tests[$tool_idx].category" "$yaml_file")
            local name=$(yq eval ".tests[$tool_idx].name" "$yaml_file")
            local query=$(yq eval ".tests[$tool_idx].query" "$yaml_file")
            local expected_state=$(yq eval ".tests[$tool_idx].expected_state" "$yaml_file")

            local validations=""
            local num_validations=$(yq eval ".tests[$tool_idx].validations | length" "$yaml_file")
            if [ "$num_validations" != "null" ] && [ "$num_validations" -gt 0 ] 2>/dev/null; then
                for ((v=0; v<num_validations; v++)); do
                    local val_type=$(yq eval ".tests[$tool_idx].validations[$v].type" "$yaml_file")
                    if [ -n "$validations" ]; then
                        validations="${validations}|${val_type}"
                    else
                        validations="$val_type"
                    fi
                done
            fi

            local test_string="$category|$name|$query|$expected_state"
            if [ -n "$validations" ]; then
                test_string="${test_string}|${validations}"
            fi

            CRS_TESTS+=("$test_string")
            CRS_TEST_IDS+=("$test_id")
            CRS_TEST_PROJECT_ROOTS+=("$file_project_root")
            CRS_TEST_PROJECT_NAMES+=("$file_project_name")
            ((test_count++))

            local tool_display
            tool_display=$(tool_index_to_name "$tool_idx")
            echo -e "  ${GREEN}✓${NC} ${file_project_name} (${file_language}): test $test_id — $tool_display"
        done

        # Second pass: scan for TOOL_CRS_VERIFICATION entries matching the tool name.
        # CRS entries live at the end of YAML files (not at positional indices 0-22),
        # so we must search by name pattern (e.g., "*_crs_find_callers*").
        for tool_idx in "${indices_arr[@]}"; do
            local tool_name
            tool_name=$(tool_index_to_name "$tool_idx")

            # Use yq to find all CRS verification tests whose name contains "crs_<tool_name>"
            local crs_indices
            crs_indices=$(yq eval "[.tests | to_entries[] | select(.value.category == \"TOOL_CRS_VERIFICATION\" and (.value.name | test(\"crs_${tool_name}\"))) | .key] | .[]" "$yaml_file" 2>/dev/null)

            for crs_idx in $crs_indices; do
                local crs_test_id=$(yq eval ".tests[$crs_idx].id" "$yaml_file")

                # Skip if already loaded (avoid duplicates with _all aliases)
                local already_loaded=false
                for existing_id in "${CRS_TEST_IDS[@]}"; do
                    if [ "$existing_id" = "$crs_test_id" ]; then
                        already_loaded=true
                        break
                    fi
                done
                if $already_loaded; then
                    continue
                fi

                local crs_category=$(yq eval ".tests[$crs_idx].category" "$yaml_file")
                local crs_name=$(yq eval ".tests[$crs_idx].name" "$yaml_file")
                local crs_query=$(yq eval ".tests[$crs_idx].query" "$yaml_file")
                local crs_expected=$(yq eval ".tests[$crs_idx].expected_state" "$yaml_file")

                local crs_validations=""
                local crs_num_val=$(yq eval ".tests[$crs_idx].validations | length" "$yaml_file")
                if [ "$crs_num_val" != "null" ] && [ "$crs_num_val" -gt 0 ] 2>/dev/null; then
                    for ((v=0; v<crs_num_val; v++)); do
                        local val_type=$(yq eval ".tests[$crs_idx].validations[$v].type" "$yaml_file")
                        if [ -n "$crs_validations" ]; then
                            crs_validations="${crs_validations}|${val_type}"
                        else
                            crs_validations="$val_type"
                        fi
                    done
                fi

                local crs_test_string="$crs_category|$crs_name|$crs_query|$crs_expected"
                if [ -n "$crs_validations" ]; then
                    crs_test_string="${crs_test_string}|${crs_validations}"
                fi

                CRS_TESTS+=("$crs_test_string")
                CRS_TEST_IDS+=("$crs_test_id")
                CRS_TEST_PROJECT_ROOTS+=("$file_project_root")
                CRS_TEST_PROJECT_NAMES+=("$file_project_name")
                ((test_count++))

                echo -e "  ${GREEN}✓${NC} ${file_project_name} (${file_language}): test $crs_test_id — ${tool_name} [CRS]"
            done
        done
    done

    if [ $test_count -eq 0 ]; then
        echo -e "${RED}No tests matched tool filter: $TOOL_FILTER${NC}"
        return 1
    fi

    # Set PROJECT_TO_ANALYZE to the first project (will be overridden per-test in main loop)
    if [ -n "${CRS_TEST_PROJECT_ROOTS[0]}" ]; then
        PROJECT_TO_ANALYZE="${CRS_TEST_PROJECT_ROOTS[0]}"
    fi

    echo ""
    echo -e "${GREEN}Loaded $test_count tests across ${#yaml_files[@]} projects${NC}"
    return 0
}

# Look up array index for a given test ID
# For YAML tests: searches CRS_TEST_IDS for matching ID
# For legacy tests: returns (test_num - 1) as before
find_test_index() {
    local target="$1"

    # If we have YAML test IDs, search by ID
    if [ ${#CRS_TEST_IDS[@]} -gt 0 ]; then
        for i in "${!CRS_TEST_IDS[@]}"; do
            if [ "${CRS_TEST_IDS[$i]}" = "$target" ]; then
                echo "$i"
                return 0
            fi
        done
        echo "-1"
        return 1
    fi

    # Legacy fallback: test_num maps to array index (test_num - 1)
    echo $((target - 1))
    return 0
}

# ==============================================================================
# MAIN
# ==============================================================================

main() {
    # Local mode runs Go tests directly
    if [ "$LOCAL_MODE" = true ]; then
        run_local_tests
        exit $?
    fi

    # Load tests from YAML based on flags (MUST happen before banner display)
    local CROSS_PROJECT_MODE=false
    if [ -n "$TOOL_FILTER" ]; then
        # --tool mode: load specific tool(s) across all TOOL-HAPPY projects
        echo ""
        echo -e "${CYAN}Loading tool tests across projects...${NC}"
        echo "  Tool: $TOOL_FILTER"
        if [ -n "$LANGUAGE_FILTER" ]; then
            echo "  Language: $LANGUAGE_FILTER"
        fi
        if ! load_tool_across_projects; then
            echo -e "${RED}Failed to load tool tests${NC}"
            exit 1
        fi
        CROSS_PROJECT_MODE=true
        echo ""
    elif [ -n "$MULTI_FEATURE_FILTERS" ]; then
        # Cross-project mode via -t with test IDs spanning multiple projects
        echo ""
        echo -e "${CYAN}Loading tests from multiple features (cross-project)...${NC}"
        echo "  Features: $MULTI_FEATURE_FILTERS"

        # Load each feature's YAML files by temporarily setting FEATURE_FILTER
        local _saved_feature="$FEATURE_FILTER"
        local _first=true
        for mf_feature in $MULTI_FEATURE_FILTERS; do
            FEATURE_FILTER="$mf_feature"
            if [ "$_first" = true ]; then
                # First load: initializes CRS_TESTS arrays.
                # Clear hardcoded arrays BEFORE attempting YAML load so that
                # a failure doesn't leave stale hardcoded tests that misalign
                # CRS_TESTS and CRS_TEST_IDS when subsequent features merge.
                CRS_TESTS=()
                CRS_TEST_IDS=()
                CRS_TEST_PROJECT_ROOTS=()
                CRS_TEST_PROJECT_NAMES=()
                if ! load_yaml_tests; then
                    echo -e "${YELLOW}Warning: failed to load $mf_feature${NC}"
                fi
                _first=false
            else
                # Subsequent loads: append to existing arrays
                # We need to call load_yaml_tests but it resets arrays.
                # Instead, save current state and merge after.
                local _prev_tests=("${CRS_TESTS[@]}")
                local _prev_ids=("${CRS_TEST_IDS[@]}")
                local _prev_roots=("${CRS_TEST_PROJECT_ROOTS[@]}")
                local _prev_names=("${CRS_TEST_PROJECT_NAMES[@]}")
                if load_yaml_tests; then
                    # Merge: prepend saved tests before newly loaded ones
                    CRS_TESTS=("${_prev_tests[@]}" "${CRS_TESTS[@]}")
                    CRS_TEST_IDS=("${_prev_ids[@]}" "${CRS_TEST_IDS[@]}")
                    CRS_TEST_PROJECT_ROOTS=("${_prev_roots[@]}" "${CRS_TEST_PROJECT_ROOTS[@]}")
                    CRS_TEST_PROJECT_NAMES=("${_prev_names[@]}" "${CRS_TEST_PROJECT_NAMES[@]}")
                else
                    # Load failed, restore
                    CRS_TESTS=("${_prev_tests[@]}")
                    CRS_TEST_IDS=("${_prev_ids[@]}")
                    CRS_TEST_PROJECT_ROOTS=("${_prev_roots[@]}")
                    CRS_TEST_PROJECT_NAMES=("${_prev_names[@]}")
                    echo -e "${YELLOW}Warning: failed to load $mf_feature${NC}"
                fi
            fi
        done
        FEATURE_FILTER="$_saved_feature"
        echo -e "${GREEN}Total loaded: ${#CRS_TESTS[@]} tests across ${MULTI_FEATURE_FILTERS}${NC}"
        CROSS_PROJECT_MODE=true
        echo ""
    elif [ -n "$FEATURE_FILTER" ] || [ -n "$LANGUAGE_FILTER" ]; then
        echo ""
        echo -e "${CYAN}Loading tests from YAML...${NC}"
        if [ -n "$FEATURE_FILTER" ]; then
            echo "  Feature: $FEATURE_FILTER"
        fi
        if [ -n "$LANGUAGE_FILTER" ]; then
            echo "  Language: $LANGUAGE_FILTER"
        fi
        if ! load_yaml_tests; then
            echo -e "${YELLOW}Using hardcoded test array instead${NC}"
        fi
        echo ""
    fi

    if [ "$CROSS_PROJECT_MODE" = true ]; then
        echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
        echo -e "${BLUE}  CRS Integration Tests - Cross-Project Tool Mode${NC}"
        echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
        echo ""
        echo "Remote: $REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT"
        echo "Tool: $TOOL_FILTER"
        echo "Projects: ${#CRS_TESTS[@]} tests across multiple projects"
        echo "Main Agent: $OLLAMA_MODEL"
        echo "Router: $ROUTER_MODEL"
        echo "ParamExtractor: $PARAM_EXTRACTOR_MODEL"
    else
        echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
        echo -e "${BLUE}  CRS Integration Tests - Remote GPU Mode${NC}"
        echo -e "${BLUE}═══════════════════════════════════════════════════${NC}"
        echo ""
        echo "Remote: $REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT"
        echo "Project: $PROJECT_TO_ANALYZE"
        echo "Main Agent: $OLLAMA_MODEL"
        echo "Router: $ROUTER_MODEL"
        echo "ParamExtractor: $PARAM_EXTRACTOR_MODEL"
    fi
    echo ""
    echo "Output: $OUTPUT_FILE"
    echo ""

    # Setup ssh-agent first (enter passphrase once)
    setup_ssh_agent

    # Establish master connection for multiplexing
    establish_connection
    trap 'stop_trace_server; close_connection' EXIT

    # Test SSH connection
    if ! test_ssh_connection; then
        exit 1
    fi

    # Check remote Ollama
    check_remote_ollama

    # Setup remote environment (sync project, build trace server, start server)
    if ! setup_remote "$PROJECT_TO_ANALYZE"; then
        echo -e "${RED}Failed to setup remote environment${NC}"
        exit 1
    fi

    # Track which project is currently synced (for cross-project switching)
    local CURRENT_SYNCED_PROJECT="$PROJECT_TO_ANALYZE"

    # Determine which tests to run
    local tests_to_run=()
    if [ -n "$SPECIFIC_TESTS" ]; then
        tests_to_run=($(expand_test_spec "$SPECIFIC_TESTS"))
        echo ""
        echo -e "${BLUE}Running ${#tests_to_run[@]} specific tests${NC}"
        echo "Tests: ${tests_to_run[*]}"
    else
        # Run all tests (either from YAML or hardcoded array)
        if [ ${#CRS_TEST_IDS[@]} -gt 0 ]; then
            # YAML tests: use actual test IDs
            tests_to_run=("${CRS_TEST_IDS[@]}")
        else
            # Legacy: sequential 1..N
            for ((i=1; i<=${#CRS_TESTS[@]}; i++)); do
                tests_to_run+=($i)
            done
        fi
        echo ""
        if [ "$CROSS_PROJECT_MODE" = true ]; then
            echo -e "${BLUE}Running ${#tests_to_run[@]} tests across projects (tool: $TOOL_FILTER)${NC}"
        elif [ -n "$FEATURE_FILTER" ] || [ -n "$LANGUAGE_FILTER" ]; then
            echo -e "${BLUE}Running ${#tests_to_run[@]} filtered tests${NC}"
        else
            echo -e "${BLUE}Running all ${#tests_to_run[@]} tests${NC}"
        fi
    fi
    echo ""

    # Initialize results
    local results="[]"
    local passed=0
    local failed=0
    local total_runtime=0
    local total_tokens=0
    local total_steps=0

    # Run tests
    for test_num in "${tests_to_run[@]}"; do
        local idx=$(find_test_index "$test_num")
        if [ $idx -ge 0 ] && [ $idx -lt ${#CRS_TESTS[@]} ]; then

            # Cross-project mode: switch project if needed
            if [ "$CROSS_PROJECT_MODE" = true ] && [ ${#CRS_TEST_PROJECT_ROOTS[@]} -gt 0 ]; then
                local test_project="${CRS_TEST_PROJECT_ROOTS[$idx]}"
                local test_project_name="${CRS_TEST_PROJECT_NAMES[$idx]:-unknown}"
                if [ -n "$test_project" ] && [ "$test_project" != "$CURRENT_SYNCED_PROJECT" ]; then
                    echo ""
                    echo -e "${CYAN}╔══════════════════════════════════════════════════════════════╗${NC}"
                    echo -e "${CYAN}║  Switching to project: $test_project_name${NC}"
                    echo -e "${CYAN}║  Path: $test_project${NC}"
                    echo -e "${CYAN}╚══════════════════════════════════════════════════════════════╝${NC}"

                    # Sync new project to remote
                    REMOTE_PROJECT_PATH=$(sync_project_to_remote "$test_project")
                    PROJECT_TO_ANALYZE="$test_project"
                    CURRENT_SYNCED_PROJECT="$test_project"

                    # Restart trace server to pick up new project
                    stop_trace_server
                    sleep 2
                    if ! start_trace_server; then
                        echo -e "${RED}Failed to restart trace server for $test_project_name${NC}"
                        ((failed++))
                        continue
                    fi
                fi
            fi

            # LAST_TEST_RESULT is set by run_crs_test
            LAST_TEST_RESULT="{}"

            if run_crs_test "${CRS_TESTS[$idx]}" "$test_num"; then
                ((passed++))
                LAST_TEST_RESULT=$(echo "$LAST_TEST_RESULT" | jq '.status = "PASSED"')
            else
                ((failed++))
                LAST_TEST_RESULT=$(echo "$LAST_TEST_RESULT" | jq '.status = "FAILED"')
            fi

            # Add project info to result for cross-project tracking
            if [ "$CROSS_PROJECT_MODE" = true ] && [ ${#CRS_TEST_PROJECT_NAMES[@]} -gt 0 ]; then
                local proj_name="${CRS_TEST_PROJECT_NAMES[$idx]:-unknown}"
                local proj_root="${CRS_TEST_PROJECT_ROOTS[$idx]:-unknown}"
                LAST_TEST_RESULT=$(echo "$LAST_TEST_RESULT" | jq \
                    --arg pn "$proj_name" --arg pr "$proj_root" \
                    '.project_name = $pn | .project_root = $pr')
            fi

            # Extract stats from test result
            local runtime=$(echo "$LAST_TEST_RESULT" | jq -r '.runtime_ms // 0')
            local tokens=$(echo "$LAST_TEST_RESULT" | jq -r '.tokens_used // 0')
            local steps=$(echo "$LAST_TEST_RESULT" | jq -r '.steps_taken // 0')

            # Track first test runtime for speed comparisons
            if [ "$FIRST_TEST_RUNTIME" -eq 0 ]; then
                FIRST_TEST_RUNTIME=$runtime
            fi

            total_runtime=$((total_runtime + runtime))
            total_tokens=$((total_tokens + tokens))
            total_steps=$((total_steps + steps))

            # Add to results array
            results=$(echo "$results" | jq --argjson new "$LAST_TEST_RESULT" '. + [$new]')
        else
            echo -e "${YELLOW}Skipping invalid test number: $test_num${NC}"
        fi
    done

    # Calculate averages
    local tests_run=${#tests_to_run[@]}
    local avg_runtime=0
    local avg_tokens=0
    local avg_steps=0
    if [ $tests_run -gt 0 ]; then
        avg_runtime=$((total_runtime / tests_run))
        avg_tokens=$((total_tokens / tests_run))
        avg_steps=$((total_steps / tests_run))
    fi

    # Calculate tool usage across all tests
    local tool_usage=$(echo "$results" | jq '
        [.[].crs_trace.trace // [] | .[] | select(.action == "tool_call" or .action == "tool_call_forced")] |
        group_by(.tool) |
        map({tool: .[0].tool, count: length}) |
        sort_by(-.count)
    ' 2>/dev/null || echo "[]")

    # Calculate CRS event counts across all tests
    local circuit_breaker_count=$(echo "$results" | jq '
        [.[].crs_trace.trace // [] | .[] | select(.action == "circuit_breaker")] | length
    ' 2>/dev/null || echo "0")

    local semantic_rep_count=$(echo "$results" | jq '
        [.[].crs_trace.trace // [] | .[] | select(.error != null and (.error | test("[Ss]emantic repetition|similar to")))] | length
    ' 2>/dev/null || echo "0")

    # Count tests with per-test tool violations (not total across all tests)
    # A violation is when a single test has a tool called >2 times WITHOUT CB firing
    local tools_exceeding_threshold=$(echo "$results" | jq '
        [.[] |
            {
                test: .test,
                tool_counts: ([.crs_trace.trace // [] | .[] | select(.action == "tool_call" or .action == "tool_call_forced")] | group_by(.tool) | map({tool: .[0].tool, count: length})),
                cb_fired: ([.crs_trace.trace // [] | .[] | select(.action == "circuit_breaker")] | length > 0)
            } |
            # Select tests where a tool exceeded threshold AND CB did NOT fire
            select((.tool_counts | map(select(.count > 2)) | length > 0) and (.cb_fired == false))
        ] | length
    ' 2>/dev/null || echo "0")

    local learning_events=$(echo "$results" | jq '
        [.[].crs_trace.trace // [] | .[] | select(.action | test("learn|clause|cdcl"))] | length
    ' 2>/dev/null || echo "0")

    # GR-Phase1: Calculate quality metrics
    local empty_response_warnings=$(ssh_cmd "grep -c 'empty response' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    empty_response_warnings=$(echo "$empty_response_warnings" | tr -d '[:space:]')

    local tests_under_15s=$(echo "$results" | jq '[.[] | select(.runtime_ms < 15000)] | length' 2>/dev/null || echo "0")
    local tests_over_60s=$(echo "$results" | jq '[.[] | select(.runtime_ms >= 60000)] | length' 2>/dev/null || echo "0")

    # Build output JSON
    local test_type="CRS Integration"
    if [ "$CROSS_PROJECT_MODE" = true ]; then
        test_type="CRS Cross-Project Tool ($TOOL_FILTER)"
    fi
    local output=$(jq -n \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg test_type "$test_type" \
        --arg project "$PROJECT_TO_ANALYZE" \
        --arg tool_filter "${TOOL_FILTER:-}" \
        --arg remote "$REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT" \
        --arg model "$OLLAMA_MODEL" \
        --arg router "$ROUTER_MODEL" \
        --arg total "$tests_run" \
        --arg passed "$passed" \
        --arg failed "$failed" \
        --arg total_runtime "$total_runtime" \
        --arg avg_runtime "$avg_runtime" \
        --arg total_tokens "$total_tokens" \
        --arg avg_tokens "$avg_tokens" \
        --arg total_steps "$total_steps" \
        --arg avg_steps "$avg_steps" \
        --argjson tool_usage "$tool_usage" \
        --argjson results "$results" \
        '{
            metadata: {
                timestamp: $timestamp,
                test_type: $test_type,
                project_root: $project,
                tool_filter: (if $tool_filter == "" then null else $tool_filter end),
                remote_host: $remote,
                models: {
                    main_agent: $model,
                    router: $router
                },
                total_tests: ($total | tonumber),
                passed: ($passed | tonumber),
                failed: ($failed | tonumber),
                timing: {
                    total_runtime_ms: ($total_runtime | tonumber),
                    avg_runtime_ms: ($avg_runtime | tonumber)
                },
                usage: {
                    total_tokens: ($total_tokens | tonumber),
                    avg_tokens: ($avg_tokens | tonumber),
                    total_steps: ($total_steps | tonumber),
                    avg_steps: ($avg_steps | tonumber)
                },
                tool_usage_summary: $tool_usage
            },
            results: $results
        }')

    echo "$output" > "$OUTPUT_FILE"

    # Summary
    echo ""
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                     CRS TEST SUMMARY                             ║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  Remote: $REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT                             ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  Models: $OLLAMA_MODEL / $ROUTER_MODEL / $PARAM_EXTRACTOR_MODEL  ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}  RESULTS                                                         ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Tests run:    $tests_run                                              ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ ${GREEN}Passed:       $passed${NC}                                              ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  └─ ${RED}Failed:       $failed${NC}                                              ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}  PERFORMANCE                                                     ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Total runtime:  ${total_runtime}ms                                    ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Avg runtime:    ${avg_runtime}ms                                      ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Total tokens:   ${total_tokens}                                       ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Avg tokens:     ${avg_tokens}                                         ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  ├─ Total steps:    ${total_steps}                                        ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  └─ Avg steps:      ${avg_steps}                                          ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}  TOOL USAGE (across all tests)                                   ${BLUE}║${NC}"
    # Note: Don't flag tools with >2 calls here - CB checks are per-test, not across tests
    echo "$tool_usage" | jq -r '.[] | "  ├─ \(.tool): \(.count) calls"' 2>/dev/null | head -10 | while read line; do
        printf "${BLUE}║${NC}  %-64s ${BLUE}║${NC}\n" "$line"
    done
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}  CRS EVENTS (Code Reasoning State)                               ${BLUE}║${NC}"
    printf "${BLUE}║${NC}  ├─ Circuit breakers fired:    %-5s                             ${BLUE}║${NC}\n" "$circuit_breaker_count"
    printf "${BLUE}║${NC}  ├─ Semantic repetitions:      %-5s                             ${BLUE}║${NC}\n" "$semantic_rep_count"
    printf "${BLUE}║${NC}  ├─ Tools exceeding threshold: %-5s                             ${BLUE}║${NC}\n" "$tools_exceeding_threshold"
    printf "${BLUE}║${NC}  └─ Learning events:           %-5s                             ${BLUE}║${NC}\n" "$learning_events"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    # GR-39b Verification
    if [ "$tools_exceeding_threshold" -gt 0 ] && [ "$circuit_breaker_count" -eq 0 ]; then
        echo -e "${BLUE}║${NC}  ${RED}⚠ GR-39b ISSUE: Tools exceeded threshold but CB didn't fire!${NC}   ${BLUE}║${NC}"
    elif [ "$circuit_breaker_count" -gt 0 ]; then
        echo -e "${BLUE}║${NC}  ${GREEN}✓ GR-39b: Circuit breaker working correctly${NC}                    ${BLUE}║${NC}"
    fi
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║${NC}  GR-PHASE1 QUALITY METRICS                                       ${BLUE}║${NC}"
    printf "${BLUE}║${NC}  ├─ Empty response warnings:  %-5s (should be <50)              ${BLUE}║${NC}\n" "$empty_response_warnings"
    printf "${BLUE}║${NC}  ├─ Avg runtime:              %-5sms (should be <15000ms)       ${BLUE}║${NC}\n" "$avg_runtime"
    printf "${BLUE}║${NC}  ├─ Tests under 15s:          %-5s                              ${BLUE}║${NC}\n" "$tests_under_15s"
    printf "${BLUE}║${NC}  └─ Tests over 60s:           %-5s (should be 0)                ${BLUE}║${NC}\n" "$tests_over_60s"
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    # Quality assessment
    if [ "$empty_response_warnings" -lt 50 ] && [ "$avg_runtime" -lt 15000 ]; then
        echo -e "${BLUE}║${NC}  ${GREEN}✓ GR-Phase1: Quality thresholds MET${NC}                            ${BLUE}║${NC}"
    else
        echo -e "${BLUE}║${NC}  ${RED}✗ GR-Phase1: Quality thresholds NOT met${NC}                        ${BLUE}║${NC}"
        if [ "$empty_response_warnings" -ge 50 ]; then
            echo -e "${BLUE}║${NC}    ${YELLOW}→ P0: Empty response warnings exceed threshold${NC}               ${BLUE}║${NC}"
        fi
        if [ "$avg_runtime" -ge 15000 ]; then
            echo -e "${BLUE}║${NC}    ${YELLOW}→ P0: Average runtime exceeds 15s threshold${NC}                  ${BLUE}║${NC}"
        fi
    fi
    echo -e "${BLUE}║${NC}                                                                  ${BLUE}║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "Results saved to: ${GREEN}$OUTPUT_FILE${NC}"
    echo ""

    # Show failed test details
    if [ $failed -gt 0 ]; then
        echo -e "${RED}╔══════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║                     FAILED TESTS                                 ║${NC}"
        echo -e "${RED}╠══════════════════════════════════════════════════════════════════╣${NC}"
        echo "$results" | jq -r '.[] | select(.status == "FAILED") |
            "Test \(.test) [\(.category)]: \(.query | .[0:50])...\n  State: \(.state)\n  Error: \(.response | .[0:100] // "none")\n"
        ' | while read line; do
            echo -e "${RED}║${NC}  $line"
        done
        echo -e "${RED}╚══════════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${YELLOW}Remote server logs (last 30 lines):${NC}"
        ssh_cmd "tail -30 ~/trace_test/AleutianFOSS/trace_server.log" 2>/dev/null || true
    fi

    # Per-test breakdown
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                     PER-TEST BREAKDOWN                           ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "$results" | jq -r '.[] |
        "Test \(.test) [\(.category)] - \(.status)\n" +
        "  Query: \(.query | .[0:60])...\n" +
        "  Time: \(.runtime_ms)ms | Steps: \(.steps_taken) | Tokens: \(.tokens_used)\n" +
        "  CRS Trace: \(.crs_trace.total_steps // 0) reasoning steps\n" +
        "  Tools: \([.crs_trace.trace // [] | .[] | select(.action == "tool_call" or .action == "tool_call_forced") | .tool] | group_by(.) | map("\(.[0]):\(length)") | join(", ") | if . == "" then "none" else . end)\n" +
        "  Circuit Breakers: \([.crs_trace.trace // [] | .[] | select(.action == "circuit_breaker")] | length)\n" +
        "  Semantic Blocks: \([.crs_trace.trace // [] | .[] | select(.error != null and (.error | test("[Ss]emantic|similar")))] | length)\n"
    ' 2>/dev/null

    # CRS Server Log Summary
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                     CRS SERVER LOG SUMMARY                       ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""

    # Count key CRS events in server logs
    local log_gr39b=$(ssh_cmd "grep -c 'GR-39b' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_cb30c=$(ssh_cmd "grep -c 'CB-30c' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_crs02=$(ssh_cmd "grep -c 'CRS-02' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_crs04=$(ssh_cmd "grep -c 'CRS-04' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_crs06=$(ssh_cmd "grep -c 'CRS-06' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_errors=$(ssh_cmd "grep -c 'ERROR' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
    local log_warns=$(ssh_cmd "grep -c 'WARN' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")

    echo -e "  Log event counts:"
    printf "    GR-39b (Count Circuit Breaker):  %s\n" "$log_gr39b"
    printf "    CB-30c (Semantic Repetition):    %s\n" "$log_cb30c"
    printf "    CRS-02 (Proof Numbers):          %s\n" "$log_crs02"
    printf "    CRS-04 (CDCL Learning):          %s\n" "$log_crs04"
    printf "    CRS-06 (Coordinator Events):     %s\n" "$log_crs06"
    printf "    Errors:                          %s\n" "$log_errors"
    printf "    Warnings:                        %s\n" "$log_warns"
    echo ""

    # Show recent GR-39b and CB-30c logs if any
    if [ "$log_gr39b" != "0" ]; then
        echo -e "  ${YELLOW}Recent GR-39b (Count Circuit Breaker) entries:${NC}"
        ssh_cmd "grep 'GR-39b' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" | sed 's/^/    /'
        echo ""
    fi

    if [ "$log_cb30c" != "0" ]; then
        echo -e "  ${YELLOW}Recent CB-30c (Semantic Repetition) entries:${NC}"
        ssh_cmd "grep 'CB-30c' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" | sed 's/^/    /'
        echo ""
    fi

    # Cross-project summary table (only in --tool mode)
    if [ "$CROSS_PROJECT_MODE" = true ]; then
        echo ""
        echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║   Tool: $TOOL_FILTER — Cross-Project Results${NC}"
        echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
        printf "${BLUE}║${NC}  %-14s │ %-4s │ %-8s │ %-7s │ %-8s ${BLUE}║${NC}\n" "Project" "Lang" "State" "Time" "Verdict"
        printf "${BLUE}║${NC}  %-14s─┼─%-4s─┼─%-8s─┼─%-7s─┼─%-8s ${BLUE}║${NC}\n" "──────────────" "────" "────────" "───────" "────────"

        echo "$results" | jq -r '.[] |
            [
                (.project_name // .category),
                (.state // "?"),
                (.runtime_ms // 0 | tostring),
                (.status // "?")
            ] | @tsv
        ' 2>/dev/null | while IFS=$'\t' read -r proj state runtime verdict; do
            # Determine language from project name
            local lang="?"
            case "$proj" in
                hugo|badger|gin) lang="Go" ;;
                flask|pandas) lang="Py" ;;
                express|babylonjs) lang="JS" ;;
                nest|nestjs|plottable) lang="TS" ;;
            esac

            # Format runtime
            local time_s
            if [ "$runtime" -gt 0 ] 2>/dev/null; then
                time_s=$(echo "scale=1; $runtime / 1000" | bc 2>/dev/null || echo "${runtime}ms")
                time_s="${time_s}s"
            else
                time_s="0s"
            fi

            # Color the verdict
            local verdict_colored="$verdict"
            if [ "$verdict" = "PASSED" ]; then
                verdict_colored="${GREEN}PASSED${NC}"
            else
                verdict_colored="${RED}FAILED${NC}"
            fi

            printf "${BLUE}║${NC}  %-14s │ %-4s │ %-8s │ %7s │ " "$proj" "$lang" "$state" "$time_s"
            echo -e "${verdict_colored}   ${BLUE}║${NC}"
        done

        local pass_rate="$passed/$tests_run"
        echo -e "${BLUE}╠══════════════════════════════════════════════════════════════════╣${NC}"
        printf "${BLUE}║${NC}  Pass rate: %-10s │  Avg time: %-8s                      ${BLUE}║${NC}\n" "$pass_rate" "${avg_runtime}ms"
        echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"
    fi

    # Option to view full JSON
    echo ""
    echo -e "${YELLOW}Full JSON results saved to: $OUTPUT_FILE${NC}"
    echo -e "${YELLOW}View with: cat $OUTPUT_FILE | jq .${NC}"
    echo -e "${YELLOW}View specific test: cat $OUTPUT_FILE | jq '.results[0]'${NC}"
    echo -e "${YELLOW}View all CRS traces: cat $OUTPUT_FILE | jq '.results[].crs_trace'${NC}"
    echo ""
    echo -e "${YELLOW}View server logs: ssh -p $REMOTE_PORT $REMOTE_USER@$REMOTE_HOST 'cat ~/trace_test/AleutianFOSS/trace_server.log'${NC}"
    echo -e "${YELLOW}Search for GR-39b: ssh -p $REMOTE_PORT $REMOTE_USER@$REMOTE_HOST 'grep GR-39b ~/trace_test/AleutianFOSS/trace_server.log'${NC}"

    if [ $failed -gt 0 ]; then
        return 1
    fi
}

# ==============================================================================
# ENTRY POINT
# ==============================================================================

# Run tests
# The || captures the exit code without triggering set -e
main_exit=0
main "$@" || main_exit=$?

# ==============================================================================
# POST-RUN: AUTO-COMPARE TO GOLD STANDARD (REMOTE)
# ==============================================================================

# Run comparison on the remote server where Ollama is available.
# The synced repo at ~/trace_test/AleutianFOSS/ already has the comparison script.
if [ -n "$LANGUAGE_FILTER" ] && [ -f "$OUTPUT_FILE" ]; then
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         Running gold standard comparison (remote)...             ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"

    RESULTS_BASENAME=$(basename "$OUTPUT_FILE")

    # 1. Upload results JSON to remote /tmp/
    scp -i "$SSH_KEY" -P "$REMOTE_PORT" \
        -o StrictHostKeyChecking=no \
        -o ControlPath="$SSH_CONTROL_SOCKET" \
        "$OUTPUT_FILE" "$REMOTE_USER@$REMOTE_HOST:/tmp/$RESULTS_BASENAME"

    # 2. Run comparison on remote (Ollama is at localhost:11434 there)
    REMOTE_COMPARISON_OUTPUT=$(ssh_cmd "cd ~/trace_test/AleutianFOSS && \
        OLLAMA_BASE_URL=http://localhost:11434 \
        JUDGE_MODEL=${OLLAMA_MODEL:-gpt-oss:20b} \
        bash scripts/compare_to_gold_standard.sh /tmp/$RESULTS_BASENAME $LANGUAGE_FILTER /tmp" 2>&1) || true

    echo "$REMOTE_COMPARISON_OUTPUT" | tail -30

    # 3. Find and pull back comparison files from remote /tmp/
    REMOTE_COMPARISON_JSON=$(ssh_cmd "ls -t /tmp/gold_standard_comparison_${LANGUAGE_FILTER}_*.json 2>/dev/null | head -1" 2>/dev/null || true)
    REMOTE_COMPARISON_TXT=$(ssh_cmd "ls -t /tmp/gold_standard_comparison_${LANGUAGE_FILTER}_*.txt 2>/dev/null | head -1" 2>/dev/null || true)

    if [ -n "$REMOTE_COMPARISON_JSON" ] && [ -n "$REMOTE_COMPARISON_TXT" ]; then
        scp -i "$SSH_KEY" -P "$REMOTE_PORT" \
            -o StrictHostKeyChecking=no \
            -o ControlPath="$SSH_CONTROL_SOCKET" \
            "$REMOTE_USER@$REMOTE_HOST:$REMOTE_COMPARISON_JSON" /tmp/
        scp -i "$SSH_KEY" -P "$REMOTE_PORT" \
            -o StrictHostKeyChecking=no \
            -o ControlPath="$SSH_CONTROL_SOCKET" \
            "$REMOTE_USER@$REMOTE_HOST:$REMOTE_COMPARISON_TXT" /tmp/

        LOCAL_COMPARISON_JSON="/tmp/$(basename "$REMOTE_COMPARISON_JSON")"
        LOCAL_COMPARISON_TXT="/tmp/$(basename "$REMOTE_COMPARISON_TXT")"

        echo ""
        echo -e "${GREEN}Gold standard comparison complete!${NC}"
        echo -e "  Text report: ${GREEN}$LOCAL_COMPARISON_TXT${NC}"
        echo -e "  JSON results: ${GREEN}$LOCAL_COMPARISON_JSON${NC}"
        echo -e "${YELLOW}View mismatches: cat $LOCAL_COMPARISON_JSON | jq '.[] | select(.verdict == \"FAIL\")'${NC}"
    else
        echo -e "${YELLOW}WARNING: Could not retrieve comparison files from remote${NC}"
        echo "Remote output:"
        echo "$REMOTE_COMPARISON_OUTPUT"
    fi
fi

# Tip for capturing console logs
echo ""
echo -e "${YELLOW}Tip: To also capture console output, run with:${NC}"
echo -e "  $0 $* 2>&1 | tee /tmp/trace_logs_\$(date +%Y%m%d_%H%M%S).txt"

exit $main_exit
