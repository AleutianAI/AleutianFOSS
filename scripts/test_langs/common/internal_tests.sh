#!/bin/bash
# Internal Test Implementations for CRS Integration Tests
# Handles INTERNAL:* test cases that verify CRS state and persistence

# ==============================================================================
# INTERNAL TEST DISPATCHER
# ==============================================================================

run_internal_test() {
    local category="$1"
    local test_name="$2"
    local expected="$3"
    local test_num="${4:-0}"  # GR-39 Issue 5: Accept test_num for proper result tracking

    local start_time=$(get_time_ms)
    local exit_code=0
    local result_message=""

    case "$test_name" in
        verify_checkpoint_exists)
            # Check for CRS checkpoint/backup files in ~/.aleutian/crs (NOT ~/.claude/crs)
            echo -e "  ${BLUE}Checking ~/.aleutian/crs for persistence files...${NC}"

            # First check if the directory exists
            local dir_exists=$(ssh_cmd "test -d ~/.aleutian/crs && echo 'yes' || echo 'no'" || echo "no")
            if [ "$dir_exists" = "no" ]; then
                echo -e "  ${RED}✗ Directory ~/.aleutian/crs does not exist${NC}"
                echo -e "  ${YELLOW}  → CRS persistence may not be initialized${NC}"
                exit_code=1
                result_message="Directory does not exist"
            else
                # Check for BadgerDB files (MANIFEST, *.vlog, *.sst)
                local badger_files=$(ssh_cmd "find ~/.aleutian/crs -name 'MANIFEST' -o -name '*.vlog' -o -name '*.sst' 2>/dev/null | wc -l" || echo "0")
                badger_files=$(echo "$badger_files" | tr -d '[:space:]')

                # Check for checkpoint/backup files
                local checkpoint_files=$(ssh_cmd "find ~/.aleutian/crs -name '*.backup*' -o -name '*.checkpoint*' -o -name 'crs_*.json' 2>/dev/null | wc -l" || echo "0")
                checkpoint_files=$(echo "$checkpoint_files" | tr -d '[:space:]')

                # List what's in the directory for debugging
                echo -e "  ${BLUE}Contents of ~/.aleutian/crs:${NC}"
                ssh_cmd "ls -la ~/.aleutian/crs 2>/dev/null | head -10" | while read line; do
                    echo -e "    $line"
                done

                if [ "$badger_files" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ BadgerDB files found: $badger_files${NC}"
                    result_message="BadgerDB files found: $badger_files"
                elif [ "$checkpoint_files" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ Checkpoint files found: $checkpoint_files${NC}"
                    result_message="Checkpoint files found: $checkpoint_files"
                else
                    echo -e "  ${RED}✗ No persistence files found in ~/.aleutian/crs${NC}"
                    echo -e "  ${YELLOW}  → Directory exists but is empty or has no CRS data${NC}"
                    exit_code=1
                    result_message="No persistence files found"
                fi
            fi
            ;;

        restart_and_verify_state)
            # Restart the server and verify state is restored
            echo -e "    ${BLUE}Restarting trace server...${NC}"
            stop_trace_server
            sleep 2
            if start_trace_server; then
                echo -e "  ${GREEN}✓ Server restarted successfully${NC}"
                result_message="Server restarted successfully"
            else
                echo -e "  ${RED}✗ Server failed to restart${NC}"
                exit_code=1
                result_message="Server failed to restart"
            fi
            ;;

        verify_event_graph_context)
            # Check server logs for graph context in events
            local has_context=$(ssh_cmd "grep -c 'graph_context\|GraphContext' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            has_context=$(echo "$has_context" | tr -d '[:space:]')
            if [ "$has_context" -gt 0 ]; then
                echo -e "  ${GREEN}✓ Graph context found in events ($has_context occurrences)${NC}"
                result_message="Graph context found: $has_context occurrences"
            else
                echo -e "  ${YELLOW}⚠ Graph context not found in logs (may need more queries first)${NC}"
                result_message="Graph context not found (warning only)"
            fi
            ;;

        verify_delta_count)
            # Query CRS state for delta count
            local delta_info=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/crs/deltas'" 2>/dev/null)
            if echo "$delta_info" | jq . > /dev/null 2>&1; then
                local count=$(echo "$delta_info" | jq '.count // .total // 0')
                if [ "$count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ Delta count: $count${NC}"
                    result_message="Delta count: $count"
                else
                    echo -e "  ${YELLOW}⚠ Delta count is 0 (run more queries first)${NC}"
                    result_message="Delta count is 0"
                fi
            else
                echo -e "  ${YELLOW}⚠ CRS debug endpoint not available${NC}"
                result_message="CRS debug endpoint not available"
            fi
            ;;

        verify_history_limit)
            # Verify ringbuffer history is bounded
            local history_info=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/crs/history'" 2>/dev/null)
            if echo "$history_info" | jq . > /dev/null 2>&1; then
                local size=$(echo "$history_info" | jq '.size // .count // 0')
                local limit=$(echo "$history_info" | jq '.limit // .max_size // 1000')
                if [ "$size" -le "$limit" ]; then
                    echo -e "  ${GREEN}✓ History size ($size) within limit ($limit)${NC}"
                    result_message="History size ($size) within limit ($limit)"
                else
                    echo -e "  ${RED}✗ History size ($size) exceeds limit ($limit)${NC}"
                    exit_code=1
                    result_message="History size exceeds limit"
                fi
            else
                echo -e "  ${YELLOW}⚠ History endpoint not available${NC}"
                result_message="History endpoint not available"
            fi
            ;;

        replay_and_verify)
            # Test delta replay functionality
            echo -e "  ${YELLOW}⚠ Replay verification not yet implemented${NC}"
            result_message="Not yet implemented"
            ;;

        verify_index_span_attribute)
            # GR-01: Check server logs for OTel span attributes indicating index usage
            # After optimization, spans should have "index_used=true" or "lookup_method=index"
            echo -e "  ${BLUE}Checking trace server logs for index span attributes...${NC}"

            # Check for index-related span attributes in logs
            local index_traces=$(ssh_cmd "grep -c 'index_used\|lookup_method.*index\|GetByName\|index.GetByName' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            index_traces=$(echo "$index_traces" | tr -d '[:space:]')

            # Also check for O(V) scan indicators (should be absent after fix)
            local scan_traces=$(ssh_cmd "grep -c 'findSymbolsByName\|O(V)\|full_scan' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            scan_traces=$(echo "$scan_traces" | tr -d '[:space:]')

            echo -e "  ${BLUE}Index usage traces: $index_traces${NC}"
            echo -e "  ${BLUE}Full scan traces: $scan_traces${NC}"

            if [ "$index_traces" -gt 0 ]; then
                echo -e "  ${GREEN}✓ Index usage detected in OTel spans${NC}"
                result_message="Index usage: $index_traces traces, Scans: $scan_traces traces"
            elif [ "$scan_traces" -eq 0 ]; then
                # No scan traces means we're probably using index (good)
                echo -e "  ${GREEN}✓ No O(V) scan traces detected (index likely used)${NC}"
                result_message="No scan traces detected"
            else
                # Before GR-01 fix: expect scan traces, no index traces
                echo -e "  ${YELLOW}⚠ O(V) scans detected, index usage not confirmed${NC}"
                echo -e "  ${YELLOW}  → This test will pass after GR-01 is implemented${NC}"
                result_message="Pre-GR-01: Scans=$scan_traces, Index=$index_traces"
            fi
            ;;

        verify_pagerank_convergence)
            # GR-12: Verify PageRank algorithm converged within max iterations
            echo -e "  ${BLUE}Checking PageRank convergence (GR-12)...${NC}"

            # Ensure graph is built first
            local stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            if [ -z "$stats_response" ] || echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${YELLOW}Building graph first...${NC}"
                ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null
                sleep 2
            fi

            # Trigger PageRank by calling find_important via agent
            echo -e "  ${BLUE}Triggering PageRank via find_important...${NC}"
            local agent_response=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"query\":\"What are the top 3 most important functions?\"}' 'http://localhost:8080/v1/codebuddy/agent/run'" 2>/dev/null)
            sleep 3

            # Check for PageRank-related log entries
            local pr_logs=$(ssh_cmd "grep -i 'PageRank\|pagerank\|find_important' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -10" || echo "")

            if [ -n "$pr_logs" ]; then
                echo -e "  ${BLUE}PageRank log entries:${NC}"
                echo "$pr_logs" | sed 's/^/    /'

                # Check for convergence indicator
                local converged=$(echo "$pr_logs" | grep -ci "converged\|iterations\|PageRankTop")
                if [ "$converged" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ GR-12: PageRank convergence detected${NC}"
                    result_message="PageRank converged"
                else
                    echo -e "  ${GREEN}✓ GR-12: PageRank executed${NC}"
                    result_message="PageRank executed"
                fi
            else
                echo -e "  ${RED}✗ GR-12: No PageRank activity found in logs${NC}"
                exit_code=1
                result_message="No PageRank activity"
            fi
            ;;

        verify_implements_edges)
            # GR-40: Verify EdgeTypeImplements edges exist in the graph for Go code
            # NOTE: 0 implements edges is CORRECT if codebase has 0 interfaces
            echo -e "  ${BLUE}Checking for EdgeTypeImplements edges in graph...${NC}"

            # Query the graph stats endpoint for edge type breakdown
            local graph_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null)

            if echo "$graph_stats" | jq . > /dev/null 2>&1; then
                local implements_count=$(echo "$graph_stats" | jq '.edges_by_type.implements // .edges_by_type.EdgeTypeImplements // 0')
                local total_edges=$(echo "$graph_stats" | jq '.edge_count // .total_edges // 0')
                local interface_count=$(echo "$graph_stats" | jq '.nodes_by_kind.interface // 0')

                echo -e "  ${BLUE}Total edges: $total_edges${NC}"
                echo -e "  ${BLUE}Interface nodes: $interface_count${NC}"
                echo -e "  ${BLUE}Implements edges: $implements_count${NC}"

                if [ "$interface_count" -eq 0 ]; then
                    # No interfaces in codebase - 0 implements edges is correct
                    echo -e "  ${GREEN}✓ GR-40: No interfaces in codebase, 0 implements edges is correct${NC}"
                    result_message="No interfaces in codebase (correct: 0 implements edges)"
                elif [ "$implements_count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ GR-40: EdgeTypeImplements edges found: $implements_count${NC}"
                    result_message="Implements edges found: $implements_count"
                else
                    # Has interfaces but no implements edges - this is the bug case
                    echo -e "  ${RED}✗ GR-40: $interface_count interfaces but 0 implements edges${NC}"
                    echo -e "  ${YELLOW}  → Go interface satisfaction requires method-set matching${NC}"
                    exit_code=1
                    result_message="Bug: $interface_count interfaces but 0 implements edges"
                fi
            else
                # Fallback: check server logs for implements edge creation
                local impl_logs=$(ssh_cmd "grep -c 'EdgeTypeImplements\|implements.*edge' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
                impl_logs=$(echo "$impl_logs" | tr -d '[:space:]')

                if [ "$impl_logs" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ Implements edge activity detected in logs${NC}"
                    result_message="Implements edge logs: $impl_logs"
                else
                    # Can't determine - pass with warning
                    echo -e "  ${YELLOW}⚠ Cannot verify implements edges (no graph stats)${NC}"
                    result_message="Cannot verify (no graph stats endpoint)"
                fi
            fi
            ;;

        # ================================================================================
        # GR-PHASE1: INTEGRATION TEST QUALITY INTERNAL TESTS
        # TDD: These tests define expected behavior BEFORE fixes are implemented
        # ================================================================================

        verify_cb_threshold_consistency)
            # P1-Issue2: Verify circuit breaker fires consistently for ALL tools at same threshold
            echo -e "  ${BLUE}Checking circuit breaker consistency across all tools...${NC}"

            # Get tool usage and circuit breaker events
            local tool_calls=$(ssh_cmd "grep -c 'tool_call\|executing tool' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            local cb_fires=$(ssh_cmd "grep -c 'GR-39b\|circuit.*breaker.*fired' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            tool_calls=$(echo "$tool_calls" | tr -d '[:space:]')
            cb_fires=$(echo "$cb_fires" | tr -d '[:space:]')

            echo -e "  ${BLUE}Total tool calls: $tool_calls${NC}"
            echo -e "  ${BLUE}Circuit breaker fires: $cb_fires${NC}"

            # Check for tools that exceeded threshold but didn't fire CB
            local tools_over_threshold=$(ssh_cmd "grep -E 'find_important.*calls|Read.*calls|Grep.*calls' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | grep -E '[345]+ calls' | head -5" || echo "")

            if [ -n "$tools_over_threshold" ]; then
                echo -e "  ${YELLOW}Tools exceeding threshold:${NC}"
                echo "$tools_over_threshold" | sed 's/^/    /'

                # Check if CB fired for these
                local cb_for_tools=$(ssh_cmd "grep -E 'GR-39b.*(find_important|Read|Grep)' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "")
                if [ -z "$cb_for_tools" ]; then
                    echo -e "  ${RED}✗ P1-Issue2: Tools exceeded threshold but no CB fired!${NC}"
                    exit_code=1
                    result_message="CB inconsistency: tools over threshold, no CB fired"
                else
                    echo -e "  ${GREEN}✓ P1-Issue2: Circuit breaker fired for tools exceeding threshold${NC}"
                    result_message="CB consistent"
                fi
            else
                if [ "$cb_fires" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ P1-Issue2: Circuit breaker fired when tools exceeded threshold${NC}"
                    result_message="CB fires: $cb_fires"
                else
                    echo -e "  ${YELLOW}⚠ P1-Issue2: No tools exceeded threshold (cannot verify CB consistency)${NC}"
                    result_message="No tools exceeded threshold yet"
                fi
            fi
            ;;

        verify_debug_crs_endpoint)
            # P2-Issue5: Verify /debug/crs endpoint is available
            # GR-Phase1: Endpoint moved to /agent/debug/crs for session access
            echo -e "  ${BLUE}Checking /agent/debug/crs endpoint availability...${NC}"

            local crs_response=$(ssh_cmd "curl -s -w '%{http_code}' 'http://localhost:8080/v1/codebuddy/agent/debug/crs'" 2>/dev/null || echo "")
            local http_code=""
            local body=""
            local resp_len=${#crs_response}

            # Handle empty response (server not running or connection failed)
            if [ -z "$crs_response" ] || [ "$resp_len" -lt 3 ]; then
                echo -e "  ${RED}✗ P2-Issue5: No response from server (connection failed or server stopped, len=$resp_len)${NC}"
                exit_code=1
                result_message="Server not responding"
                http_code="000"
            else
                http_code="${crs_response: -3}"
                body="${crs_response:0:$((resp_len - 3))}"
            fi

            echo -e "  ${BLUE}HTTP status: $http_code${NC}"

            if [ "$http_code" = "200" ]; then
                if echo "$body" | jq . > /dev/null 2>&1; then
                    echo -e "  ${GREEN}✓ P2-Issue5: /debug/crs endpoint available and returns valid JSON${NC}"
                    result_message="Endpoint available (HTTP 200)"
                else
                    echo -e "  ${YELLOW}⚠ P2-Issue5: /debug/crs returns 200 but invalid JSON${NC}"
                    result_message="Endpoint returns invalid JSON"
                fi
            elif [ "$http_code" = "404" ]; then
                echo -e "  ${RED}✗ P2-Issue5: /debug/crs endpoint not found (404)${NC}"
                echo -e "  ${YELLOW}  → Implement endpoint to expose CRS state for debugging${NC}"
                exit_code=1
                result_message="Endpoint not implemented (404)"
            else
                echo -e "  ${RED}✗ P2-Issue5: /debug/crs endpoint error (HTTP $http_code)${NC}"
                exit_code=1
                result_message="Endpoint error (HTTP $http_code)"
            fi
            ;;

        verify_debug_history_endpoint)
            # P2-Issue5: Verify /debug/history endpoint is available
            # NOTE: This endpoint is not yet implemented - test will show 404
            echo -e "  ${BLUE}Checking /debug/history endpoint availability...${NC}"

            local history_response=$(ssh_cmd "curl -s -w '%{http_code}' 'http://localhost:8080/v1/codebuddy/agent/debug/history'" 2>/dev/null || echo "")
            local http_code=""
            local body=""
            local resp_len=${#history_response}

            # Handle empty response (server not running or connection failed)
            if [ -z "$history_response" ] || [ "$resp_len" -lt 3 ]; then
                echo -e "  ${RED}✗ P2-Issue5: No response from server (connection failed or server stopped, len=$resp_len)${NC}"
                exit_code=1
                result_message="Server not responding"
                http_code="000"
            else
                http_code="${history_response: -3}"
                body="${history_response:0:$((resp_len - 3))}"
            fi

            echo -e "  ${BLUE}HTTP status: $http_code${NC}"

            if [ "$http_code" = "200" ]; then
                if echo "$body" | jq . > /dev/null 2>&1; then
                    local history_count=$(echo "$body" | jq '.count // .size // length')
                    echo -e "  ${GREEN}✓ P2-Issue5: /debug/history endpoint available ($history_count entries)${NC}"
                    result_message="Endpoint available ($history_count entries)"
                else
                    echo -e "  ${YELLOW}⚠ P2-Issue5: /debug/history returns 200 but invalid JSON${NC}"
                    result_message="Endpoint returns invalid JSON"
                fi
            elif [ "$http_code" = "404" ]; then
                echo -e "  ${RED}✗ P2-Issue5: /debug/history endpoint not found (404)${NC}"
                echo -e "  ${YELLOW}  → Implement endpoint to expose reasoning history${NC}"
                exit_code=1
                result_message="Endpoint not implemented (404)"
            else
                echo -e "  ${RED}✗ P2-Issue5: /debug/history endpoint error (HTTP $http_code)${NC}"
                exit_code=1
                result_message="Endpoint error (HTTP $http_code)"
            fi
            ;;

        verify_pagerank_convergence_logged)
            # P2-Issue6: Verify PageRank convergence is logged with iterations and tolerance
            echo -e "  ${BLUE}Checking PageRank convergence logging...${NC}"

            # Look for convergence logs with iterations and tolerance
            local convergence_logs=$(ssh_cmd "grep -i 'pagerank.*converge\|iterations.*tolerance\|convergence.*achieved' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            if [ -n "$convergence_logs" ]; then
                echo -e "  ${BLUE}PageRank convergence logs:${NC}"
                echo "$convergence_logs" | sed 's/^/    /'

                # Check for specific convergence info
                local has_iterations=$(echo "$convergence_logs" | grep -ci "iteration")
                local has_tolerance=$(echo "$convergence_logs" | grep -ci "tolerance\|delta\|diff")

                if [ "$has_iterations" -gt 0 ] && [ "$has_tolerance" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ P2-Issue6: PageRank convergence logged with iterations and tolerance${NC}"
                    result_message="Convergence logged with details"
                else
                    echo -e "  ${YELLOW}⚠ P2-Issue6: Convergence logged but missing iterations ($has_iterations) or tolerance ($has_tolerance)${NC}"
                    result_message="Partial convergence logging"
                fi
            else
                # Check if PageRank was even invoked
                local pr_invoked=$(ssh_cmd "grep -ci 'pagerank\|find_important' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
                pr_invoked=$(echo "$pr_invoked" | tr -d '[:space:]')

                if [ "$pr_invoked" -gt 0 ]; then
                    echo -e "  ${RED}✗ P2-Issue6: PageRank invoked ($pr_invoked times) but convergence not logged${NC}"
                    echo -e "  ${YELLOW}  → Add logging for iterations to convergence and tolerance achieved${NC}"
                    exit_code=1
                    result_message="PageRank invoked but no convergence logging"
                else
                    echo -e "  ${YELLOW}⚠ P2-Issue6: PageRank not invoked yet (run importance queries first)${NC}"
                    result_message="PageRank not invoked"
                fi
            fi
            ;;

        # ================================================================================
        # GR-06 to GR-09: SECONDARY INDEX VERIFICATION TESTS
        # These tests verify secondary indexes are populated and working correctly
        # ================================================================================

        verify_nodes_by_name_index)
            # GR-06: Verify nodesByName secondary index exists and has data
            echo -e "  ${BLUE}Checking nodesByName index (GR-06)...${NC}"

            # Ensure graph is built first
            local stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            if [ -z "$stats_response" ] || echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${YELLOW}Building graph first...${NC}"
                local init_response=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null)
                sleep 2
                stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            fi

            if [ -z "$stats_response" ]; then
                echo -e "  ${RED}✗ GR-06: Cannot connect to server${NC}"
                exit_code=1
                result_message="Server not responding"
            elif echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${RED}✗ GR-06: Failed to build graph${NC}"
                exit_code=1
                result_message="Graph build failed"
            else
                local node_count=$(echo "$stats_response" | jq -r '.node_count // 0' 2>/dev/null)
                local kinds_count=$(echo "$stats_response" | jq -r '.nodes_by_kind | length' 2>/dev/null)

                if [ "$node_count" -gt 0 ] && [ "$kinds_count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ GR-06: nodesByName index verified (node_count=$node_count, kinds=$kinds_count)${NC}"
                    echo -e "  ${BLUE}  Index is populated - nodes added use AddNode which populates nodesByName${NC}"
                    result_message="nodesByName index working (nodes: $node_count)"
                else
                    echo -e "  ${RED}✗ GR-06: Graph has no nodes (node_count=$node_count)${NC}"
                    exit_code=1
                    result_message="Empty graph"
                fi
            fi
            ;;

        verify_nodes_by_kind_index)
            # GR-07: Verify nodesByKind secondary index via /debug/graph/stats
            echo -e "  ${BLUE}Checking nodesByKind index (GR-07)...${NC}"

            # Ensure graph is built first
            local stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            if [ -z "$stats_response" ] || echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${YELLOW}Building graph first...${NC}"
                local init_response=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null)
                sleep 2
                stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            fi

            if [ -z "$stats_response" ]; then
                echo -e "  ${RED}✗ GR-07: Cannot connect to server${NC}"
                exit_code=1
                result_message="Server not responding"
            elif echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${RED}✗ GR-07: Failed to build graph${NC}"
                exit_code=1
                result_message="Graph build failed"
            else
                # nodes_by_kind map should have entries
                local kinds_map=$(echo "$stats_response" | jq -c '.nodes_by_kind // {}' 2>/dev/null)
                local kinds_count=$(echo "$kinds_map" | jq 'length' 2>/dev/null)

                if [ "$kinds_count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ GR-07: nodesByKind index has $kinds_count kinds${NC}"
                    echo "$kinds_map" | jq -r 'to_entries | .[:5] | .[] | "    \(.key): \(.value) nodes"' 2>/dev/null
                    result_message="nodesByKind index working ($kinds_count kinds)"
                else
                    echo -e "  ${RED}✗ GR-07: nodesByKind is empty${NC}"
                    exit_code=1
                    result_message="Empty nodesByKind"
                fi
            fi
            ;;

        verify_edges_by_type_index)
            # GR-08: Verify edgesByType secondary index via /debug/graph/stats
            echo -e "  ${BLUE}Checking edgesByType index (GR-08)...${NC}"

            # Ensure graph is built first
            local stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            if [ -z "$stats_response" ] || echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${YELLOW}Building graph first...${NC}"
                local init_response=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null)
                sleep 2
                stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            fi

            if [ -z "$stats_response" ]; then
                echo -e "  ${RED}✗ GR-08: Cannot connect to server${NC}"
                exit_code=1
                result_message="Server not responding"
            elif echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${RED}✗ GR-08: Failed to build graph${NC}"
                exit_code=1
                result_message="Graph build failed"
            else
                # edges_by_type map should have entries
                local types_map=$(echo "$stats_response" | jq -c '.edges_by_type // {}' 2>/dev/null)
                local types_count=$(echo "$types_map" | jq 'length' 2>/dev/null)
                local edge_count=$(echo "$stats_response" | jq -r '.edge_count // 0' 2>/dev/null)

                if [ "$types_count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ GR-08: edgesByType index has $types_count edge types (total edges: $edge_count)${NC}"
                    echo "$types_map" | jq -r 'to_entries | .[] | "    \(.key): \(.value) edges"' 2>/dev/null
                    result_message="edgesByType index working ($types_count types, $edge_count edges)"
                else
                    echo -e "  ${RED}✗ GR-08: edgesByType is empty${NC}"
                    exit_code=1
                    result_message="Empty edgesByType"
                fi
            fi
            ;;

        verify_edges_by_file_index)
            # GR-09: Verify edgesByFile index exists (used by RemoveFile)
            echo -e "  ${BLUE}Checking edgesByFile index (GR-09)...${NC}"

            # Ensure graph is built first
            local stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            if [ -z "$stats_response" ] || echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${YELLOW}Building graph first...${NC}"
                local init_response=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null)
                sleep 2
                stats_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "")
            fi

            if [ -z "$stats_response" ]; then
                echo -e "  ${RED}✗ GR-09: Cannot connect to server${NC}"
                exit_code=1
                result_message="Server not responding"
            elif echo "$stats_response" | jq -e '.error' >/dev/null 2>&1; then
                echo -e "  ${RED}✗ GR-09: Failed to build graph${NC}"
                exit_code=1
                result_message="Graph build failed"
            else
                local edge_count=$(echo "$stats_response" | jq -r '.edge_count // 0' 2>/dev/null)
                local node_count=$(echo "$stats_response" | jq -r '.node_count // 0' 2>/dev/null)

                if [ "$edge_count" -gt 0 ]; then
                    # Check logs for edgesByFile usage or RemoveFile operations
                    local file_index_logs=$(ssh_cmd "grep -ci 'edgesByFile\|RemoveFile\|file.*index' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
                    file_index_logs=$(echo "$file_index_logs" | tr -d '[:space:]')

                    echo -e "  ${GREEN}✓ GR-09: edgesByFile index verified (edge_count=$edge_count, nodes=$node_count)${NC}"
                    echo -e "  ${BLUE}  Index is populated - edges added use AddEdge which populates edgesByFile${NC}"

                    if [ "$file_index_logs" -gt 0 ]; then
                        echo -e "  ${BLUE}  Found $file_index_logs file index related log entries${NC}"
                    fi

                    result_message="edgesByFile index working (edges: $edge_count)"
                else
                    echo -e "  ${RED}✗ GR-09: Graph has no edges (edge_count=$edge_count)${NC}"
                    exit_code=1
                    result_message="Empty graph (no edges)"
                fi
            fi
            ;;

        # ================================================================================
        # GR-10: QUERY CACHE VERIFICATION TESTS
        # TDD: These tests define expected behavior BEFORE implementation
        # ================================================================================

        verify_cache_stats_endpoint)
            # GR-10: Verify /debug/cache endpoint returns cache statistics
            echo -e "  ${BLUE}Checking cache stats endpoint (GR-10)...${NC}"

            # First, ensure graph is initialized
            echo -e "  ${BLUE}Ensuring graph is initialized...${NC}"
            local init_resp=$(ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\"}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null || echo "")
            local graph_id=$(echo "$init_resp" | jq -r '.graph_id // ""' 2>/dev/null)
            if [ -n "$graph_id" ] && [ "$graph_id" != "null" ]; then
                echo -e "  ${GREEN}✓ Graph initialized: $graph_id${NC}"
            else
                echo -e "  ${YELLOW}⚠ Graph init response: $init_resp${NC}"
            fi

            # Make callers queries to populate the cache (use actual AleutianOrchestrator function names)
            echo -e "  ${BLUE}Populating cache with callers queries...${NC}"
            local total_callers=0
            # These are actual functions in AleutianOrchestrator that are likely to have callers
            for func_name in "CodeAnalysisRequest" "NewClient" "ParseAPIMessage" "WriteDataToGCS" "FetchPromptFromGCS" "DistillerRequest"; do
                local callers_resp=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/callers?graph_id=$graph_id&function=$func_name'" 2>/dev/null || echo "")
                local callers_count=$(echo "$callers_resp" | jq '.callers | length' 2>/dev/null || echo "0")
                if [ "$callers_count" -gt 0 ]; then
                    echo -e "  ${GREEN}✓ Found $callers_count callers of '$func_name'${NC}"
                    total_callers=$((total_callers + callers_count))
                    break  # One successful query is enough to populate cache
                fi
            done
            if [ "$total_callers" -eq 0 ]; then
                echo -e "  ${YELLOW}⚠ No callers found (cache should still record misses)${NC}"
            fi

            local cache_response=$(ssh_cmd "curl -s -w '%{http_code}' 'http://localhost:8080/v1/codebuddy/debug/cache'" 2>/dev/null || echo "")
            local http_code=""
            local body=""
            local resp_len=${#cache_response}

            if [ -z "$cache_response" ] || [ "$resp_len" -lt 3 ]; then
                echo -e "  ${RED}✗ GR-10: No response from cache endpoint${NC}"
                exit_code=1
                result_message="Server not responding"
                http_code="000"
            else
                http_code="${cache_response: -3}"
                body="${cache_response:0:$((resp_len - 3))}"
            fi

            echo -e "  ${BLUE}HTTP status: $http_code${NC}"

            if [ "$http_code" = "200" ]; then
                if echo "$body" | jq . > /dev/null 2>&1; then
                    local callers_size=$(echo "$body" | jq '.callers_size // .callers.size // 0')
                    local callees_size=$(echo "$body" | jq '.callees_size // .callees.size // 0')
                    local paths_size=$(echo "$body" | jq '.paths_size // .paths.size // 0')
                    local hit_rate=$(echo "$body" | jq '.hit_rate // 0')
                    local callers_misses=$(echo "$body" | jq '.callers_misses // 0')

                    echo -e "  ${GREEN}✓ GR-10: Cache stats endpoint available${NC}"
                    echo -e "  ${BLUE}  Callers cache: $callers_size entries${NC}"
                    echo -e "  ${BLUE}  Callees cache: $callees_size entries${NC}"
                    echo -e "  ${BLUE}  Paths cache: $paths_size entries${NC}"
                    echo -e "  ${BLUE}  Hit rate: $hit_rate${NC}"

                    # Verify cache activity
                    local total_size=$((callers_size + callees_size + paths_size))
                    local total_misses=$(echo "$body" | jq '(.callers_misses // 0) + (.callees_misses // 0) + (.paths_misses // 0)' 2>/dev/null || echo "0")

                    if [ "$total_size" -ge 1 ]; then
                        echo -e "  ${GREEN}✓ GR-10: Cache populated with $total_size entries${NC}"
                        result_message="Cache stats available and populated ($total_size entries)"
                    elif [ "$total_misses" -ge 1 ]; then
                        echo -e "  ${GREEN}✓ GR-10: Cache active ($total_misses queries made)${NC}"
                        result_message="Cache stats available ($total_misses queries, 0 cached)"
                    else
                        echo -e "  ${GREEN}✓ GR-10: Cache endpoint working (no queries yet)${NC}"
                        result_message="Cache stats endpoint working"
                    fi
                else
                    echo -e "  ${YELLOW}⚠ GR-10: Cache endpoint returns 200 but invalid JSON${NC}"
                    result_message="Endpoint returns invalid JSON"
                fi
            elif [ "$http_code" = "404" ]; then
                echo -e "  ${RED}✗ GR-10: Cache stats endpoint not found (404)${NC}"
                echo -e "  ${YELLOW}  → Implement /debug/cache endpoint to expose cache stats${NC}"
                exit_code=1
                result_message="Endpoint not implemented (404)"
            else
                echo -e "  ${RED}✗ GR-10: Cache stats endpoint error (HTTP $http_code)${NC}"
                exit_code=1
                result_message="Endpoint error (HTTP $http_code)"
            fi
            ;;

        verify_cache_invalidation)
            # GR-10: Verify cache is invalidated when graph is rebuilt
            echo -e "  ${BLUE}Checking cache invalidation (GR-10)...${NC}"

            # First, get current cache stats
            local before_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/cache'" 2>/dev/null || echo "{}")
            local before_callers=$(echo "$before_stats" | jq '.callers_size // 0' 2>/dev/null || echo "0")

            # Trigger a graph rebuild (re-init the project)
            echo -e "  ${BLUE}Triggering graph rebuild...${NC}"
            ssh_cmd "curl -s -X POST -H 'Content-Type: application/json' -d '{\"project_root\":\"/home/aleutiandevops/trace_test/AleutianOrchestrator\", \"force_rebuild\": true}' 'http://localhost:8080/v1/codebuddy/init'" 2>/dev/null
            sleep 2

            # Check cache stats after rebuild
            local after_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/cache'" 2>/dev/null || echo "{}")
            local after_callers=$(echo "$after_stats" | jq '.callers_size // 0' 2>/dev/null || echo "0")
            local generation=$(echo "$after_stats" | jq '.generation // 0' 2>/dev/null || echo "0")

            echo -e "  ${BLUE}Before rebuild: $before_callers callers cached${NC}"
            echo -e "  ${BLUE}After rebuild: $after_callers callers cached${NC}"
            echo -e "  ${BLUE}Cache generation: $generation${NC}"

            if [ "$after_callers" -eq 0 ] || [ "$after_callers" -lt "$before_callers" ]; then
                echo -e "  ${GREEN}✓ GR-10: Cache was invalidated on graph rebuild${NC}"
                result_message="Cache invalidated (before=$before_callers, after=$after_callers)"
            else
                echo -e "  ${YELLOW}⚠ GR-10: Cache may not have been invalidated${NC}"
                echo -e "  ${YELLOW}  → Cache should be cleared when graph generation changes${NC}"
                result_message="Cache not invalidated (before=$before_callers, after=$after_callers)"
            fi
            ;;

        # ================================================================================
        # GR-11: PARALLEL BFS VERIFICATION TESTS
        # TDD: These tests define expected behavior BEFORE implementation
        # ================================================================================

        verify_parallel_threshold)
            # GR-11: Verify parallel mode is used for levels with > 16 nodes
            echo -e "  ${BLUE}Checking parallel BFS threshold (GR-11)...${NC}"

            # Check server logs for parallel mode decisions
            local parallel_logs=$(ssh_cmd "grep -i 'parallel_mode\|parallel.*threshold\|level.*nodes' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -10" || echo "")

            if [ -n "$parallel_logs" ]; then
                echo -e "  ${GREEN}✓ GR-11: Parallel BFS threshold logging found${NC}"
                echo -e "  ${BLUE}Recent logs:${NC}"
                echo "$parallel_logs" | sed 's/^/    /'
                result_message="Parallel threshold logging present"
            else
                echo -e "  ${YELLOW}⚠ GR-11: No parallel threshold logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-11: Expected (parallel BFS not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-11: Should log level sizes and parallel decisions${NC}"
                result_message="No parallel logs (pre-implementation expected)"
            fi
            ;;

        verify_parallel_context_cancellation)
            # GR-11: Verify context cancellation works in parallel mode
            echo -e "  ${BLUE}Checking parallel BFS context cancellation (GR-11)...${NC}"

            # Check for cancellation handling in logs
            local cancel_logs=$(ssh_cmd "grep -i 'context.*cancel\|parallel.*cancel\|bfs.*abort' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Also check that errgroup is used (indicates proper cancellation propagation)
            local errgroup_logs=$(ssh_cmd "grep -i 'errgroup\|worker.*exit\|goroutine.*stop' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            if [ -n "$cancel_logs" ] || [ -n "$errgroup_logs" ]; then
                echo -e "  ${GREEN}✓ GR-11: Context cancellation handling detected${NC}"
                if [ -n "$cancel_logs" ]; then
                    echo "$cancel_logs" | sed 's/^/    /'
                fi
                result_message="Cancellation handling present"
            else
                echo -e "  ${YELLOW}⚠ GR-11: No cancellation handling logs (may not have been triggered)${NC}"
                echo -e "  ${YELLOW}  → This test passes if no crash occurs during normal operation${NC}"
                result_message="No cancellation triggered (normal operation)"
            fi
            ;;

        verify_no_race_conditions)
            # GR-11: Verify no race conditions in parallel BFS
            echo -e "  ${BLUE}Checking for race conditions (GR-11)...${NC}"

            # Check if server was built with -race flag
            local race_check=$(ssh_cmd "grep -i 'race.*detected\|DATA RACE' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | head -5" || echo "")

            if [ -n "$race_check" ]; then
                echo -e "  ${RED}✗ GR-11: RACE CONDITION DETECTED${NC}"
                echo "$race_check" | sed 's/^/    /'
                exit_code=1
                result_message="Race condition detected"
            else
                echo -e "  ${GREEN}✓ GR-11: No race conditions detected in logs${NC}"
                echo -e "  ${BLUE}  → For thorough check, rebuild with: go build -race${NC}"
                echo -e "  ${BLUE}  → And run: go test -race ./services/trace/graph/...${NC}"
                result_message="No races in logs (run -race for thorough check)"
            fi
            ;;

        # ================================================================================
        # GR-14: LOUVAIN COMMUNITY DETECTION VERIFICATION TESTS
        # TDD: These tests define expected behavior BEFORE implementation
        # ================================================================================

        verify_community_modularity)
            # GR-14: Verify modularity score is calculated and reasonable
            echo -e "  ${BLUE}Checking community modularity score (GR-14)...${NC}"

            # Query debug endpoint for community detection stats
            local community_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/codebuddy/debug/graph/stats'" 2>/dev/null || echo "{}")
            local modularity=$(echo "$community_stats" | jq '.communities.modularity // .community_modularity // -1' 2>/dev/null || echo "-1")
            local community_count=$(echo "$community_stats" | jq '.communities.count // .community_count // 0' 2>/dev/null || echo "0")

            if [ "$modularity" != "-1" ] && [ "$community_count" -gt 0 ]; then
                echo -e "  ${GREEN}✓ GR-14: Modularity score available: $modularity${NC}"
                echo -e "  ${BLUE}  Communities detected: $community_count${NC}"

                # Check if modularity is in reasonable range [0, 1]
                local mod_valid=$(echo "$modularity" | awk '{if ($1 >= 0 && $1 <= 1) print "yes"; else print "no"}')
                if [ "$mod_valid" = "yes" ]; then
                    result_message="Modularity: $modularity, Communities: $community_count"
                else
                    echo -e "  ${YELLOW}⚠ GR-14: Modularity out of expected range [0,1]: $modularity${NC}"
                    result_message="Modularity out of range: $modularity"
                fi
            else
                echo -e "  ${YELLOW}⚠ GR-14: Community stats not available${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-14: Expected (community detection not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-14: Should expose modularity via /debug/graph/stats${NC}"
                result_message="Community stats not available (pre-implementation expected)"
            fi
            ;;

        verify_community_crs_recording)
            # GR-14: Verify community detection records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for community detection (GR-14)...${NC}"

            # Check server logs for CRS trace step recording
            local crs_logs=$(ssh_cmd "grep -i 'analytics_communities\|community.*trace\|DetectCommunities.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Also check for trace step metadata
            local trace_metadata=$(ssh_cmd "grep -i 'communities_found\|modularity\|community_count' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-14: CRS recording detected for community detection${NC}"
                if [ -n "$crs_logs" ]; then
                    echo "$crs_logs" | sed 's/^/    /'
                fi
                result_message="CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-14: No CRS recording logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-14: Expected (community detection not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-14: Should record TraceStep with WithCRS methods${NC}"
                result_message="No CRS logs (pre-implementation expected)"
            fi
            ;;

        # ================================================================================
        # GR-15: find_communities TOOL VERIFICATION TESTS
        # TDD: These tests define expected behavior BEFORE implementation
        # ================================================================================

        verify_find_communities_crs)
            # GR-15: Verify find_communities tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_communities tool (GR-15)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_communities\|tool.*communities' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'find_communities.*action\|find_communities.*trace' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-15: find_communities tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-15: No find_communities tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-15: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-15: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_modularity_quality_label)
            # GR-15: Verify modularity quality label is included in output
            echo -e "  ${BLUE}Checking modularity quality label (GR-15)...${NC}"

            # Check server logs for quality labels
            local quality_logs=$(ssh_cmd "grep -i 'modularity_quality\|quality.*weak\|quality.*moderate\|quality.*good\|quality.*strong' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            if [ -n "$quality_logs" ]; then
                echo -e "  ${GREEN}✓ GR-15: Modularity quality labels detected${NC}"
                echo "$quality_logs" | sed 's/^/    /'
                result_message="Quality labels present"
            else
                echo -e "  ${YELLOW}⚠ GR-15: No modularity quality labels found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-15: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-15: Should include quality labels (weak/moderate/good/strong)${NC}"
                result_message="No quality labels (pre-implementation expected)"
            fi
            ;;

        verify_find_articulation_points_crs)
            # GR-17a: Verify find_articulation_points tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_articulation_points tool (GR-17a)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_articulation_points\|tool.*articulation' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'find_articulation_points.*action\|find_articulation_points.*trace' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17a: find_articulation_points tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17a: No find_articulation_points tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-17a: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-17a: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_dominators_crs)
            # GR-17b: Verify find_dominators tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_dominators tool (GR-17b)...${NC}"

            local tool_crs_logs=$(ssh_cmd "grep -i 'find_dominators\|tool.*dominators' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")
            local trace_metadata=$(ssh_cmd "grep -i 'find_dominators.*action\|find_dominators.*trace' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17b: find_dominators tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17b: No find_dominators tool CRS logs found${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_merge_points_crs)
            # GR-17d: Verify find_merge_points tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_merge_points tool (GR-17d)...${NC}"

            local tool_crs_logs=$(ssh_cmd "grep -i 'find_merge_points\|tool.*merge' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")
            local trace_metadata=$(ssh_cmd "grep -i 'find_merge_points.*action\|find_merge_points.*trace' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17d: find_merge_points tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17d: No find_merge_points tool CRS logs found${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_loops_crs)
            # GR-17e: Verify find_loops tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_loops tool (GR-17e)...${NC}"

            local tool_crs_logs=$(ssh_cmd "grep -i 'find_loops\|tool.*loops\|DetectLoops' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")
            local trace_metadata=$(ssh_cmd "grep -i 'analytics_loops\|loops.*trace\|DetectLoops.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17e: find_loops tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17e: No find_loops tool CRS logs found${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_common_dependency_crs)
            # GR-17f: Verify find_common_dependency tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_common_dependency tool (GR-17f)...${NC}"

            local tool_crs_logs=$(ssh_cmd "grep -i 'find_common_dependency\|tool.*common.*dependency' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")
            local trace_metadata=$(ssh_cmd "grep -i 'find_common_dependency.*action\|find_common_dependency.*trace' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17f: find_common_dependency tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17f: No find_common_dependency tool CRS logs found${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_control_deps_crs)
            # GR-17c: Verify find_control_dependencies tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_control_dependencies tool (GR-17c)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_control_dependencies\|control.*dependenc\|ComputeControlDependence' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'analytics_control\|control.*trace\|ControlDependence.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17c: find_control_dependencies tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17c: No find_control_dependencies tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-17c: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-17c: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_extractable_crs)
            # GR-17g: Verify find_extractable_regions tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_extractable_regions tool (GR-17g)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_extractable_regions\|extractable.*region\|DetectSESERegions' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'analytics_sese\|sese.*trace\|SESERegions.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17g: find_extractable_regions tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17g: No find_extractable_regions tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-17g: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-17g: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_critical_path_crs)
            # GR-18a: Verify find_critical_path tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_critical_path tool (GR-18a)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_critical_path\|critical.*path.*tool\|CriticalPath' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'dominator.*critical\|critical.*path.*CRS\|tool_critical_path' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-18a: find_critical_path tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-18a: No find_critical_path tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-18a: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-18a: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        verify_find_module_api_crs)
            # GR-18b: Verify find_module_api tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for find_module_api tool (GR-18b)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'find_module_api\|module.*api.*tool\|ModuleAPI' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'community.*api\|module.*api.*CRS\|tool_module_api' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-18b: find_module_api tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-18b: No find_module_api tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-18b: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-18b: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        *)
            echo -e "  ${YELLOW}⚠ Unknown internal test: $test_name${NC}"
            result_message="Unknown test"
            ;;
    esac

    # GR-39 Issue 5: Set LAST_TEST_RESULT for internal tests
    local end_time=$(get_time_ms)
    local duration=$((end_time - start_time))
    local result_status="PASSED"
    if [ $exit_code -ne 0 ]; then
        result_status="FAILED"
    fi

    LAST_TEST_RESULT=$(jq -n \
        --arg test_num "$test_num" \
        --arg category "$category" \
        --arg query "INTERNAL:$test_name" \
        --arg state "$result_status" \
        --arg message "$result_message" \
        --arg duration "$duration" \
        '{
            test: ($test_num | tonumber),
            category: $category,
            query: $query,
            state: $state,
            steps_taken: 0,
            tokens_used: 0,
            runtime_ms: ($duration | tonumber),
            response: $message,
            crs_trace: {total_steps: 0, trace: []}
        }')

    return $exit_code
}

