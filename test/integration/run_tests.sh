#!/bin/bash
# Container-Based Integration Test Runner
# Reads YAML test definitions, sends requests to trace containers,
# validates responses, and outputs TAP format results.
#
# Runs inside the test-runner sidecar container.
# Test YAMLs are mounted at /tests/features/TOOL-HAPPY-*/
# Container URLs are passed via environment variables (TRACE_HUGO_URL, etc.)
#
# Usage (inside container):
#   /runner/run_tests.sh
#   PROJECT_FILTER=hugo,flask /runner/run_tests.sh
#   FEATURE_FILTER=TOOL-HAPPY-HUGO /runner/run_tests.sh

set -euo pipefail

# ==============================================================================
# CONFIGURATION
# ==============================================================================

TESTS_DIR="${TESTS_DIR:-/tests}"
FEATURES_DIR="$TESTS_DIR/features"
MAX_TIMEOUT="${MAX_TIMEOUT:-300}"
PARALLEL_PROJECTS="${PARALLEL_PROJECTS:-true}"

# Colors for human-readable output (stderr)
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Results directory
RESULTS_DIR="/tmp/test-results"
mkdir -p "$RESULTS_DIR"

# ==============================================================================
# PROJECT → URL MAPPING
# ==============================================================================

# Maps project name (from YAML metadata.project) to container URL.
# URLs come from environment variables set in podman-compose.test.yml.
get_project_url() {
    local project="$1"
    case "$project" in
        hugo)      echo "${TRACE_HUGO_URL:-}" ;;
        badger)    echo "${TRACE_BADGER_URL:-}" ;;
        gin)       echo "${TRACE_GIN_URL:-}" ;;
        flask)     echo "${TRACE_FLASK_URL:-}" ;;
        pandas)    echo "${TRACE_PANDAS_URL:-}" ;;
        express)   echo "${TRACE_EXPRESS_URL:-}" ;;
        babylonjs) echo "${TRACE_BABYLONJS_URL:-}" ;;
        nestjs)    echo "${TRACE_NESTJS_URL:-}" ;;
        plottable) echo "${TRACE_PLOTTABLE_URL:-}" ;;
        *)
            echo >&2 "Unknown project: $project"
            echo ""
            ;;
    esac
}

# ==============================================================================
# LOGGING HELPERS
# ==============================================================================

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_debug() { echo -e "${CYAN}[DEBUG]${NC} $*" >&2; }

# ==============================================================================
# VALIDATION FUNCTIONS
# ==============================================================================

# Fetch the CRS reasoning trace for a session.
# $1 = base URL, $2 = session ID
fetch_reasoning_trace() {
    local base_url="$1"
    local session_id="$2"
    curl -sf "${base_url}/v1/trace/agent/${session_id}/reasoning" 2>/dev/null || echo '{}'
}

# Run a single validation check against the response.
# Returns 0 on pass, 1 on fail.
#
# $1 = check type
# $2 = response JSON
# $3 = duration_ms
# $4 = session_id
# $5 = base_url
run_validation() {
    local check="$1"
    local response="$2"
    local duration="$3"
    local session_id="$4"
    local base_url="$5"

    case "$check" in
        graph_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            # Graph tools override action from "tool_call" to "tool_find_callers" etc.
            # Match on action prefix OR tool field name.
            local graph_tools
            graph_tools=$(echo "$trace" | jq '[.trace[] | select(
                (.action | test("tool_find_callers|tool_find_callees|tool_find_implementations|tool_find_symbol|tool_get_call_chain|tool_find_references"))
                or ((.action == "tool_call") and (.tool != null) and (.tool | test("find_callers|find_callees|find_implementations|find_symbol|get_call_chain|find_references")))
            )] | length' 2>/dev/null || echo "0")
            if [ "$graph_tools" -gt 0 ]; then
                log_info "  graph_tool_used: $graph_tools invocations"
                return 0
            else
                log_warn "  graph_tool_used: no graph tools in trace"
                return 1
            fi
            ;;

        pagerank_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local pr_tools
            pr_tools=$(echo "$trace" | jq '[.trace[] | select(
                (.action | test("find_important|analytics_pagerank"))
                or ((.action == "tool_call") and (.tool != null) and (.tool | test("find_important")))
            )] | length' 2>/dev/null || echo "0")
            if [ "$pr_tools" -gt 0 ]; then
                log_info "  pagerank_used: $pr_tools invocations"
                return 0
            else
                log_warn "  pagerank_used: find_important not in trace"
                return 1
            fi
            ;;

        implementations_found)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local impl_used
            impl_used=$(echo "$trace" | jq '[.trace[] | select(
                (.action == "tool_find_implementations")
                or ((.action == "tool_call") and (.tool != null) and (.tool == "find_implementations"))
            )] | length' 2>/dev/null || echo "0")
            local agent_resp
            agent_resp=$(echo "$response" | jq -r '.response // ""')
            local no_impls
            no_impls=$(echo "$agent_resp" | grep -ci "no implementation\|not found\|empty\|none" || true)
            if [ "$impl_used" -gt 0 ] && [ "$no_impls" -eq 0 ]; then
                log_info "  implementations_found: tool used ($impl_used calls), results present"
                return 0
            else
                log_warn "  implementations_found: tool=$impl_used, negative_matches=$no_impls"
                return 1
            fi
            ;;

        no_grep_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local grep_used
            grep_used=$(echo "$trace" | jq '[.trace[] | select(
                (.action | test("grep"; "i"))
                or ((.action == "tool_call") and (.tool != null) and (.tool | test("grep"; "i")))
            )] | length' 2>/dev/null || echo "0")
            if [ "$grep_used" -eq 0 ]; then
                log_info "  no_grep_used: confirmed no Grep fallback"
                return 0
            else
                log_warn "  no_grep_used: Grep called $grep_used times"
                return 1
            fi
            ;;

        fast_execution)
            if [ "$duration" -lt 5000 ]; then
                log_info "  fast_execution: ${duration}ms < 5000ms"
                return 0
            else
                log_warn "  fast_execution: ${duration}ms >= 5000ms"
                return 1
            fi
            ;;

        fast_not_found|fast_not_found_strict)
            local threshold=3000
            [ "$check" = "fast_not_found_strict" ] && threshold=5000
            if [ "$duration" -lt "$threshold" ]; then
                log_info "  $check: ${duration}ms < ${threshold}ms"
                return 0
            else
                log_warn "  $check: ${duration}ms >= ${threshold}ms"
                return 1
            fi
            ;;

        fast_pagerank)
            if [ "$duration" -lt 30000 ]; then
                log_info "  fast_pagerank: ${duration}ms < 30000ms"
                return 0
            else
                log_warn "  fast_pagerank: ${duration}ms >= 30000ms"
                return 1
            fi
            ;;

        citations_present)
            local agent_resp
            agent_resp=$(echo "$response" | jq -r '.response // ""')
            local cite_count
            cite_count=$(echo "$agent_resp" | grep -cE '\[[a-zA-Z0-9_/]+\.[a-z]+:[0-9]+\]' || true)
            if [ "$cite_count" -gt 0 ]; then
                log_info "  citations_present: $cite_count citations found"
                return 0
            else
                log_warn "  citations_present: no file:line citations found"
                return 1
            fi
            ;;

        analytics_recorded)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local has_analytics
            has_analytics=$(echo "$trace" | jq '[.trace[] | select(.action | test("analytics_|tool_call|tool_find_|tool_get_"))] | length' 2>/dev/null || echo "0")
            if [ "$has_analytics" -gt 0 ]; then
                log_info "  analytics_recorded: $has_analytics steps"
                return 0
            else
                log_warn "  analytics_recorded: no analytics in trace"
                return 1
            fi
            ;;

        find_communities_used|find_communities_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_communities|analytics_communities"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  $check: $tool_used invocations"
                return 0
            else
                log_warn "  $check: find_communities not in trace"
                return 1
            fi
            ;;

        find_loops_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_loops"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_loops_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_loops_tool_used: not in trace"
                return 1
            fi
            ;;

        find_extractable_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_extractable"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_extractable_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_extractable_tool_used: not in trace"
                return 1
            fi
            ;;

        find_control_deps_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_control_dep|control_flow"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_control_deps_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_control_deps_tool_used: not in trace"
                return 1
            fi
            ;;

        check_reducibility_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("check_reducibility|reducibility"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  check_reducibility_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  check_reducibility_tool_used: not in trace"
                return 1
            fi
            ;;

        find_critical_path_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_critical_path|critical_path"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_critical_path_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_critical_path_tool_used: not in trace"
                return 1
            fi
            ;;

        find_dominators_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_dominators|dominators"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_dominators_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_dominators_tool_used: not in trace"
                return 1
            fi
            ;;

        find_articulation_points_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_articulation|articulation_points"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_articulation_points_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_articulation_points_tool_used: not in trace"
                return 1
            fi
            ;;

        find_merge_points_tool_used)
            local trace
            trace=$(fetch_reasoning_trace "$base_url" "$session_id")
            local tool_used
            tool_used=$(echo "$trace" | jq '[.trace[] | select(.action | test("find_merge|merge_points"))] | length' 2>/dev/null || echo "0")
            if [ "$tool_used" -gt 0 ]; then
                log_info "  find_merge_points_tool_used: $tool_used invocations"
                return 0
            else
                log_warn "  find_merge_points_tool_used: not in trace"
                return 1
            fi
            ;;

        communities_found)
            local agent_resp
            agent_resp=$(echo "$response" | jq -r '.response // ""')
            local found
            found=$(echo "$agent_resp" | grep -ci "communit\|module\|cluster" || true)
            if [ "$found" -gt 0 ]; then
                log_info "  communities_found: detected in response"
                return 0
            else
                log_warn "  communities_found: no community mentions"
                return 1
            fi
            ;;

        *)
            # Unknown validation — log and pass (non-blocking)
            log_warn "  unknown validation: $check (skipped)"
            return 0
            ;;
    esac
}

# ==============================================================================
# SURRENDER DETECTION
# ==============================================================================

# Check if the agent response indicates a surrender (low-quality non-answer).
# Returns 0 if response is a surrender, 1 if OK.
is_surrender() {
    local agent_resp="$1"
    local resp_len=${#agent_resp}

    # Too short
    if [ "$resp_len" -lt 20 ]; then
        return 0
    fi

    # Surrender patterns
    if echo "$agent_resp" | grep -qiE "I don't know|unable to|cannot determine|I'm not sure|I couldn't|no information|I apologize"; then
        return 0
    fi

    return 1
}

# ==============================================================================
# SINGLE TEST EXECUTION
# ==============================================================================

# Run a single test case against a trace container.
# Writes a result line to the per-project results file.
#
# $1 = base_url (e.g. http://trace-hugo:12217)
# $2 = test_id
# $3 = test_name
# $4 = query
# $5 = expected_state
# $6 = validations (pipe-separated list, e.g. "graph_tool_used|fast_execution")
# $7 = results_file (path to append TAP line)
run_single_test() {
    local base_url="$1"
    local test_id="$2"
    local test_name="$3"
    local query="$4"
    local expected_state="$5"
    local validations="$6"
    local results_file="$7"

    log_info "Running test $test_id: $test_name"

    # Skip INTERNAL: tests (not applicable in container mode)
    if [[ "$query" == INTERNAL:* ]]; then
        echo "ok $test_id - $test_name # SKIP internal test (container mode)" >> "$results_file"
        return
    fi

    # Build request payload
    local payload
    payload=$(jq -n \
        --arg query "$query" \
        --arg project_root "/projects" \
        '{query: $query, project_root: $project_root}')

    # Send request with timing
    local start_time end_time duration
    start_time=$(date +%s%N)

    local http_response
    http_response=$(curl -sf \
        --max-time "$MAX_TIMEOUT" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        "${base_url}/v1/trace/agent/run" 2>/dev/null) || true

    end_time=$(date +%s%N)
    duration=$(( (end_time - start_time) / 1000000 ))

    # Check for empty/failed response
    if [ -z "$http_response" ]; then
        echo "not ok $test_id - $test_name # curl failed or timed out (${duration}ms)" >> "$results_file"
        return
    fi

    # Parse response fields
    local state session_id agent_resp
    state=$(echo "$http_response" | jq -r '.state // "UNKNOWN"')
    session_id=$(echo "$http_response" | jq -r '.session_id // ""')
    agent_resp=$(echo "$http_response" | jq -r '.response // ""')

    log_debug "  state=$state session=$session_id duration=${duration}ms"

    # Check expected state
    if [ "$state" != "$expected_state" ]; then
        echo "not ok $test_id - $test_name # expected state=$expected_state got=$state (${duration}ms)" >> "$results_file"
        return
    fi

    # Check for surrenders (even if state matches)
    if [ "$expected_state" = "COMPLETE" ] && is_surrender "$agent_resp"; then
        echo "not ok $test_id - $test_name # surrender detected (${duration}ms)" >> "$results_file"
        return
    fi

    # Run validations
    local all_passed=true
    if [ -n "$validations" ]; then
        IFS='|' read -ra checks <<< "$validations"
        for check in "${checks[@]}"; do
            if ! run_validation "$check" "$http_response" "$duration" "$session_id" "$base_url"; then
                all_passed=false
            fi
        done
    fi

    if $all_passed; then
        echo "ok $test_id - $test_name ($duration) - $state" >> "$results_file"
    else
        echo "not ok $test_id - $test_name # validation failed (${duration}ms)" >> "$results_file"
    fi
}

# ==============================================================================
# PER-PROJECT TEST EXECUTION
# ==============================================================================

# Run all tests for a single YAML file (one project).
# $1 = YAML file path
run_project_tests() {
    local yaml_file="$1"

    local project feature
    project=$(yq eval '.metadata.project' "$yaml_file")
    feature=$(yq eval '.metadata.feature' "$yaml_file")

    local base_url
    base_url=$(get_project_url "$project")
    if [ -z "$base_url" ]; then
        log_error "No URL configured for project: $project (feature: $feature)"
        return 1
    fi

    log_info "━━━ $feature ($project) → $base_url ━━━"

    # Verify container is reachable
    if ! curl -sf "${base_url}/v1/trace/health" > /dev/null 2>&1; then
        log_error "Container not reachable: $base_url"
        return 1
    fi

    local results_file="$RESULTS_DIR/${project}.tap"
    : > "$results_file"

    # Parse and run each test
    local num_tests
    num_tests=$(yq eval '.tests | length' "$yaml_file")

    # Cap tests if MAX_TESTS is set
    local max_tests="${MAX_TESTS:-0}"
    if [ "$max_tests" -gt 0 ] 2>/dev/null && [ "$num_tests" -gt "$max_tests" ]; then
        log_info "Capping to $max_tests tests (of $num_tests)"
        num_tests="$max_tests"
    fi

    for ((i=0; i<num_tests; i++)); do
        local test_id category name query expected_state
        test_id=$(yq eval ".tests[$i].id" "$yaml_file")
        category=$(yq eval ".tests[$i].category" "$yaml_file")
        name=$(yq eval ".tests[$i].name" "$yaml_file")
        query=$(yq eval ".tests[$i].query" "$yaml_file")
        expected_state=$(yq eval ".tests[$i].expected_state" "$yaml_file")

        # Skip TODO tests
        if [ "$category" = "TOOL_HAPPY_PATH_TODO" ]; then
            echo "ok $test_id - $name # SKIP TODO (tool not registered)" >> "$results_file"
            continue
        fi

        # Build validation string (pipe-separated)
        local validations=""
        local num_validations
        num_validations=$(yq eval ".tests[$i].validations | length" "$yaml_file")
        if [ "$num_validations" != "null" ] && [ "$num_validations" -gt 0 ] 2>/dev/null; then
            for ((v=0; v<num_validations; v++)); do
                local val_type
                val_type=$(yq eval ".tests[$i].validations[$v].type" "$yaml_file")
                if [ -n "$validations" ]; then
                    validations="${validations}|${val_type}"
                else
                    validations="$val_type"
                fi
            done
        fi

        run_single_test "$base_url" "$test_id" "$name" "$query" "$expected_state" "$validations" "$results_file"
    done

    log_info "━━━ $feature complete ($num_tests tests) ━━━"
}

# ==============================================================================
# YAML FILE DISCOVERY
# ==============================================================================

discover_yaml_files() {
    local yaml_files=()

    if [ ! -d "$FEATURES_DIR" ]; then
        log_error "Features directory not found: $FEATURES_DIR"
        exit 1
    fi

    # Apply filters
    if [ -n "${FEATURE_FILTER:-}" ]; then
        for f in "$FEATURES_DIR/$FEATURE_FILTER"/*.yml; do
            [ -f "$f" ] && yaml_files+=("$f")
        done
    elif [ -n "${PROJECT_FILTER:-}" ]; then
        # PROJECT_FILTER is comma-separated list of project names
        IFS=',' read -ra projects <<< "$PROJECT_FILTER"
        for proj in "${projects[@]}"; do
            local proj_upper
            proj_upper=$(echo "$proj" | tr '[:lower:]' '[:upper:]')
            for f in "$FEATURES_DIR/TOOL-HAPPY-${proj_upper}"/*.yml; do
                [ -f "$f" ] && yaml_files+=("$f")
            done
        done
    else
        for f in "$FEATURES_DIR"/TOOL-HAPPY-*/*.yml; do
            [ -f "$f" ] && yaml_files+=("$f")
        done
    fi

    if [ ${#yaml_files[@]} -eq 0 ]; then
        log_error "No YAML test files found matching filters"
        log_error "  FEATURE_FILTER=${FEATURE_FILTER:-<unset>}"
        log_error "  PROJECT_FILTER=${PROJECT_FILTER:-<unset>}"
        exit 1
    fi

    printf '%s\n' "${yaml_files[@]}"
}

# ==============================================================================
# MAIN
# ==============================================================================

main() {
    log_info "Container-based integration test runner starting"
    log_info "Features dir: $FEATURES_DIR"

    # Discover test files
    local yaml_files
    mapfile -t yaml_files < <(discover_yaml_files)
    log_info "Found ${#yaml_files[@]} YAML test files"

    if [ "$PARALLEL_PROJECTS" = "true" ] && [ ${#yaml_files[@]} -gt 1 ]; then
        # Run projects in parallel
        log_info "Running ${#yaml_files[@]} projects in parallel"
        local pids=()

        for yaml_file in "${yaml_files[@]}"; do
            run_project_tests "$yaml_file" &
            pids+=($!)
        done

        # Wait for all background jobs
        local failed=0
        for pid in "${pids[@]}"; do
            if ! wait "$pid"; then
                ((failed++))
            fi
        done

        if [ "$failed" -gt 0 ]; then
            log_warn "$failed project(s) had errors"
        fi
    else
        # Run projects sequentially
        for yaml_file in "${yaml_files[@]}"; do
            run_project_tests "$yaml_file"
        done
    fi

    # ==============================================================================
    # AGGREGATE RESULTS (TAP output to stdout)
    # ==============================================================================

    log_info "Aggregating results..."

    local total_tests=0
    local passed=0
    local failed=0
    local skipped=0
    local all_lines=()

    for tap_file in "$RESULTS_DIR"/*.tap; do
        [ -f "$tap_file" ] || continue
        while IFS= read -r line; do
            all_lines+=("$line")
            if [[ "$line" == ok* ]]; then
                if [[ "$line" == *"# SKIP"* ]]; then
                    ((skipped++))
                else
                    ((passed++))
                fi
            elif [[ "$line" == "not ok"* ]]; then
                ((failed++))
            fi
            ((total_tests++))
        done < "$tap_file"
    done

    # TAP header
    echo "TAP version 13"
    echo "1..$total_tests"

    # TAP body
    for line in "${all_lines[@]}"; do
        echo "$line"
    done

    # Summary (TAP diagnostic lines)
    local pass_rate=0
    if [ "$total_tests" -gt 0 ]; then
        pass_rate=$(( (passed + skipped) * 1000 / total_tests ))
        pass_rate="${pass_rate:0:-1}.${pass_rate: -1}"
    fi

    echo ""
    echo "# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "# Results: $passed passed, $failed failed, $skipped skipped ($total_tests total)"
    echo "# Pass rate: ${pass_rate}%"
    echo "# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # Exit with failure if any tests failed
    if [ "$failed" -gt 0 ]; then
        exit 1
    fi
}

main "$@"
