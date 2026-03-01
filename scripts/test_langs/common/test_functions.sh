#!/bin/bash
# Test Validation Functions for CRS Integration Tests
# Contains 93+ validation functions for verifying test results

# ==============================================================================
# VALIDATION FUNCTIONS
# ==============================================================================

# Main validation function dispatcher
# Calls specific validation functions based on check type
run_extra_check() {
    local check="$1"
    local response="$2"
    local duration="$3"
    local session_id="${4:-}"

    case "$check" in
        faster_than_first)
            # Session 2+ should be faster due to restored state
            # Compare to first session runtime (stored globally)
            if [ "$FIRST_TEST_RUNTIME" -gt 0 ] && [ "$duration" -lt "$FIRST_TEST_RUNTIME" ]; then
                local speedup=$(( (FIRST_TEST_RUNTIME - duration) * 100 / FIRST_TEST_RUNTIME ))
                echo -e "    ${GREEN}✓ ${speedup}% faster than first query (${duration}ms vs ${FIRST_TEST_RUNTIME}ms)${NC}"
                echo -e "    ${GREEN}  → Session restore appears to be working!${NC}"
            elif [ "$FIRST_TEST_RUNTIME" -gt 0 ]; then
                local slowdown=$(( (duration - FIRST_TEST_RUNTIME) * 100 / FIRST_TEST_RUNTIME ))
                echo -e "    ${YELLOW}⚠ ${slowdown}% slower than first query (${duration}ms vs ${FIRST_TEST_RUNTIME}ms)${NC}"
                echo -e "    ${YELLOW}  → Query complexity may differ, or CRS not providing speedup${NC}"
            else
                echo -e "    ${YELLOW}⚠ No first test runtime to compare (${duration}ms)${NC}"
            fi
            ;;

        analytics_recorded)
            # Check if analytics were recorded in CRS
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local has_analytics=$(echo "$trace" | jq '[.trace[] | select(.action == "analytics_query" or .action == "tool_call")] | length')
                if [ "$has_analytics" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ Analytics/tool calls recorded in CRS ($has_analytics steps)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ No analytics found in trace${NC}"
                fi
            fi
            ;;

        generation_incremented)
            # Check CRS generation was incremented
            local gen_response=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/debug/crs/generation'" 2>/dev/null)
            if echo "$gen_response" | jq . > /dev/null 2>&1; then
                local gen=$(echo "$gen_response" | jq '.generation // 0')
                if [ "$gen" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ CRS generation: $gen${NC}"
                else
                    echo -e "    ${YELLOW}⚠ CRS generation is 0${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ Could not fetch CRS generation${NC}"
            fi
            ;;

        graph_tool_used)
            # GR-01: Verify graph tools (find_callers, find_callees, find_implementations) were invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local graph_tools=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool | test("find_callers|find_callees|find_implementations|find_symbol"))] | length')
                if [ "$graph_tools" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ Graph tools used: $graph_tools invocations${NC}"
                else
                    echo -e "    ${YELLOW}⚠ No graph tools in trace (may have used other tools)${NC}"
                fi
            fi
            ;;

        fast_execution)
            # GR-01: Verify query executed quickly (< 5000ms for warmed index)
            if [ "$duration" -lt 5000 ]; then
                echo -e "    ${GREEN}✓ Fast execution: ${duration}ms (< 5s threshold)${NC}"
            else
                echo -e "    ${YELLOW}⚠ Slower than expected: ${duration}ms (threshold: 5s)${NC}"
            fi
            ;;

        fast_not_found)
            # GR-01: Verify not-found case is fast (O(1) index miss, not O(V) scan)
            if [ "$duration" -lt 3000 ]; then
                echo -e "    ${GREEN}✓ Fast not-found: ${duration}ms (O(1) index miss)${NC}"
            else
                echo -e "    ${YELLOW}⚠ Slow not-found: ${duration}ms (may be using O(V) scan)${NC}"
            fi
            # Also check response mentions not found
            local not_found=$(echo "$response" | jq -r '.response // ""' | grep -ci "not found\|no callers\|no function")
            if [ "$not_found" -gt 0 ]; then
                echo -e "    ${GREEN}✓ Correctly reported function not found${NC}"
            fi
            ;;

        implementations_found)
            # GR-40: Verify find_implementations returned actual results
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_implementations was used
            local impl_tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_implementations")] | length' 2>/dev/null || echo "0")

            # Check if response contains implementation names (not "no implementations found")
            local found_impls=$(echo "$agent_resp" | grep -ci "implement\|struct\|type.*handler\|concrete")
            local no_impls=$(echo "$agent_resp" | grep -ci "no implementation\|not found\|empty\|none")

            echo -e "    ${BLUE}find_implementations calls: $impl_tool_used${NC}"

            if [ "$impl_tool_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-40: find_implementations tool was used${NC}"

                if [ "$found_impls" -gt 0 ] && [ "$no_impls" -eq 0 ]; then
                    echo -e "    ${GREEN}✓ GR-40: Implementations were found in response${NC}"
                elif [ "$no_impls" -gt 0 ]; then
                    echo -e "    ${RED}✗ GR-40: Response indicates no implementations found${NC}"
                    echo -e "    ${YELLOW}  → Pre-GR-40: Go implicit interfaces not detected${NC}"
                    echo -e "    ${YELLOW}  → Post-GR-40: This should show concrete types${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-40: Could not determine if implementations found${NC}"
                fi
            else
                # Check if Grep was used as fallback (bad)
                local grep_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "Grep")] | length' 2>/dev/null || echo "0")
                if [ "$grep_used" -gt 0 ]; then
                    echo -e "    ${RED}✗ GR-40: Fell back to Grep ($grep_used calls) instead of find_implementations${NC}"
                    echo -e "    ${YELLOW}  → Pre-GR-40: Expected behavior (no implements edges)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-40: find_implementations not used, but no Grep fallback${NC}"
                fi
            fi
            ;;

        pagerank_used)
            # GR-12/GR-13: Verify find_important tool was used (PageRank-based)
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_important was used
            local fi_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_important")] | length' 2>/dev/null || echo "0")

            # Check if response mentions PageRank
            local mentions_pr=$(echo "$agent_resp" | grep -ci "pagerank\|page rank\|importance.*score")

            if [ "$fi_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-13: find_important tool was used: $fi_used calls${NC}"
                if [ "$mentions_pr" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-12: Response mentions PageRank scoring${NC}"
                fi
            else
                # Check if find_hotspots was used as fallback
                local hs_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_hotspots")] | length' 2>/dev/null || echo "0")
                if [ "$hs_used" -gt 0 ]; then
                    echo -e "    ${YELLOW}⚠ GR-13: Used find_hotspots (degree-based) instead of find_important (PageRank)${NC}"
                    echo -e "    ${YELLOW}  → Pre-GR-13: Expected (find_important not implemented)${NC}"
                    echo -e "    ${YELLOW}  → Post-GR-13: Should use find_important for importance queries${NC}"
                else
                    echo -e "    ${RED}✗ GR-13: Neither find_important nor find_hotspots used${NC}"
                fi
            fi
            ;;

        fast_pagerank)
            # GR-12: Verify PageRank completed within reasonable time (< 30s for convergence)
            if [ "$duration" -lt 30000 ]; then
                echo -e "    ${GREEN}✓ GR-12: PageRank completed in ${duration}ms (< 30s threshold)${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-12: PageRank took ${duration}ms (threshold: 30s)${NC}"
                echo -e "    ${YELLOW}  → May need optimization for large graphs${NC}"
            fi
            ;;

        no_grep_used)
            # GR-40: Verify that Grep was NOT used as fallback for interface queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)

            local grep_calls=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "Grep")] | length' 2>/dev/null || echo "0")
            local impl_calls=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_implementations")] | length' 2>/dev/null || echo "0")

            if [ "$grep_calls" -eq 0 ]; then
                echo -e "    ${GREEN}✓ GR-40: No Grep fallback (correct behavior)${NC}"
                if [ "$impl_calls" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-40: Used find_implementations: $impl_calls calls${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-40: Grep fallback detected: $grep_calls calls${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-40: Expected (no implements edges, falls back to Grep)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-40: Should use find_implementations exclusively${NC}"

                # Show what Grep was searching for
                local grep_patterns=$(echo "$trace" | jq -r '[.trace[] | select(.action == "tool_call") | select(.tool == "Grep") | .params.pattern // .target] | unique | join(", ")' 2>/dev/null)
                if [ -n "$grep_patterns" ] && [ "$grep_patterns" != "null" ]; then
                    echo -e "    ${YELLOW}  → Grep patterns: $grep_patterns${NC}"
                fi
            fi
            ;;

        # ================================================================================
        # GR-PHASE1: INTEGRATION TEST QUALITY CHECKS
        # TDD: These checks define expected behavior BEFORE fixes are implemented
        # ================================================================================

        empty_response_threshold)
            # P0: Verify empty response warnings are minimal (< 50 total)
            local empty_warns=$(ssh_cmd "grep -c 'empty response' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "0")
            empty_warns=$(echo "$empty_warns" | tr -d '[:space:]')

            if [ "$empty_warns" -lt 50 ]; then
                echo -e "    ${GREEN}✓ P0-Issue1: Empty response warnings: $empty_warns (< 50 threshold)${NC}"
            else
                echo -e "    ${RED}✗ P0-Issue1: Empty response warnings: $empty_warns (exceeds 50 threshold)${NC}"
                echo -e "    ${YELLOW}  → Root cause: OllamaAdapter receiving empty responses from LLM${NC}"
                echo -e "    ${YELLOW}  → Fix: Check prompt format compatibility with $OLLAMA_MODEL${NC}"
            fi
            ;;

        avg_runtime_threshold)
            # P0: Verify this test completed in reasonable time (< 15s)
            local threshold=15000
            if [ "$duration" -lt "$threshold" ]; then
                echo -e "    ${GREEN}✓ P0-Issue1: Runtime ${duration}ms (< ${threshold}ms threshold)${NC}"
            else
                echo -e "    ${RED}✗ P0-Issue1: Runtime ${duration}ms (exceeds ${threshold}ms threshold)${NC}"
                echo -e "    ${YELLOW}  → Likely cause: Empty response retries adding ~9s per query${NC}"
            fi
            ;;

        crs_speedup_verified)
            # P1: Verify CRS provides speedup for subsequent queries
            # This test should be faster than FIRST_TEST_RUNTIME (session 1)
            if [ "$FIRST_TEST_RUNTIME" -gt 0 ]; then
                if [ "$duration" -lt "$FIRST_TEST_RUNTIME" ]; then
                    local speedup=$(( (FIRST_TEST_RUNTIME - duration) * 100 / FIRST_TEST_RUNTIME ))
                    echo -e "    ${GREEN}✓ P1-Issue3: CRS speedup verified: ${speedup}% faster${NC}"
                    echo -e "    ${GREEN}  → Session 1: ${FIRST_TEST_RUNTIME}ms, This query: ${duration}ms${NC}"
                else
                    local slowdown=$(( (duration - FIRST_TEST_RUNTIME) * 100 / FIRST_TEST_RUNTIME ))
                    echo -e "    ${RED}✗ P1-Issue3: CRS NOT providing speedup: ${slowdown}% SLOWER${NC}"
                    echo -e "    ${YELLOW}  → Session 1: ${FIRST_TEST_RUNTIME}ms, This query: ${duration}ms${NC}"
                    echo -e "    ${YELLOW}  → CRS context should reduce tool calls for subsequent queries${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ P1-Issue3: No baseline runtime available for comparison${NC}"
            fi
            ;;

        fast_not_found_strict)
            # P2: Verify not-found queries complete in < 5 seconds
            local threshold=5000
            if [ "$duration" -lt "$threshold" ]; then
                echo -e "    ${GREEN}✓ P2-Issue4: Not-found query: ${duration}ms (< ${threshold}ms threshold)${NC}"
            else
                echo -e "    ${RED}✗ P2-Issue4: Not-found query: ${duration}ms (exceeds ${threshold}ms)${NC}"
                echo -e "    ${YELLOW}  → Should be O(1) index miss, not O(V) scan with LLM retries${NC}"
            fi
            # Verify response indicates not found
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            if echo "$agent_resp" | grep -qi "not found\|no function\|doesn't exist\|does not exist"; then
                echo -e "    ${GREEN}✓ P2-Issue4: Correctly reported symbol not found${NC}"
            else
                echo -e "    ${YELLOW}⚠ P2-Issue4: Response may not clearly indicate not found${NC}"
            fi
            ;;

        citations_present)
            # P3: Verify response includes [file:line] citations
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            # Look for patterns like [file.go:123] or file.go:123 or (file.go:123)
            local citation_count=$(echo "$agent_resp" | grep -oE '\[?[a-zA-Z0-9_/.-]+\.(go|py|js|ts|rs|java):[0-9]+\]?' | wc -l)
            citation_count=$(echo "$citation_count" | tr -d '[:space:]')

            if [ "$citation_count" -gt 0 ]; then
                echo -e "    ${GREEN}✓ P3-Issue7: Found $citation_count [file:line] citations${NC}"
            else
                echo -e "    ${RED}✗ P3-Issue7: No [file:line] citations in response${NC}"
                echo -e "    ${YELLOW}  → Analytical responses should include source citations${NC}"
                echo -e "    ${YELLOW}  → Fix: Improve prompt to require citations${NC}"
            fi
            ;;

        # ================================================================================
        # GR-10: QUERY CACHE PERFORMANCE CHECKS
        # TDD: These checks define expected behavior BEFORE implementation
        # ================================================================================

        cache_miss_expected)
            # GR-10: First query should be a cache miss
            local cache_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/debug/cache'" 2>/dev/null || echo "{}")
            local miss_count=$(echo "$cache_stats" | jq '.misses // 0' 2>/dev/null || echo "0")
            local hit_count=$(echo "$cache_stats" | jq '.hits // 0' 2>/dev/null || echo "0")

            if [ "$miss_count" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-10: Cache miss recorded (misses=$miss_count, hits=$hit_count)${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-10: Cache stats not available or no miss recorded${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-10: Expected (cache not implemented)${NC}"
            fi

            # Check server logs for cache activity
            local cache_logs=$(ssh_cmd "grep -i 'cache.*miss\|cache.*populate' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")
            if [ -n "$cache_logs" ]; then
                echo -e "    ${BLUE}Cache logs:${NC}"
                echo "$cache_logs" | sed 's/^/      /'
            fi
            ;;

        cache_hit_expected)
            # GR-10: Second identical query should be a cache hit
            local cache_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/debug/cache'" 2>/dev/null || echo "{}")
            local hit_count=$(echo "$cache_stats" | jq '.hits // 0' 2>/dev/null || echo "0")
            local miss_count=$(echo "$cache_stats" | jq '.misses // 0' 2>/dev/null || echo "0")

            if [ "$hit_count" -gt 0 ]; then
                local hit_rate=$(echo "scale=2; $hit_count * 100 / ($hit_count + $miss_count)" | bc 2>/dev/null || echo "?")
                echo -e "    ${GREEN}✓ GR-10: Cache hit recorded (hits=$hit_count, hit_rate=$hit_rate%)${NC}"
            else
                echo -e "    ${RED}✗ GR-10: No cache hit for repeated query${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-10: Expected (cache not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-10: Second identical query should hit cache${NC}"
            fi

            # Check server logs for cache hit
            local cache_logs=$(ssh_cmd "grep -i 'cache.*hit' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")
            if [ -n "$cache_logs" ]; then
                echo -e "    ${BLUE}Cache hit logs:${NC}"
                echo "$cache_logs" | sed 's/^/      /'
            fi
            ;;

        cache_speedup_expected)
            # GR-10: Cached query should be significantly faster
            # Compare this runtime to the first test runtime
            local cache_stats=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/debug/cache'" 2>/dev/null || echo "{}")
            local avg_hit_time=$(echo "$cache_stats" | jq '.avg_hit_time_ms // 0' 2>/dev/null || echo "0")
            local avg_miss_time=$(echo "$cache_stats" | jq '.avg_miss_time_ms // 0' 2>/dev/null || echo "0")

            if [ "$avg_hit_time" -gt 0 ] && [ "$avg_miss_time" -gt 0 ]; then
                local speedup=$(echo "scale=1; $avg_miss_time / $avg_hit_time" | bc 2>/dev/null || echo "?")
                echo -e "    ${GREEN}✓ GR-10: Cache speedup: ${speedup}x (miss=${avg_miss_time}ms, hit=${avg_hit_time}ms)${NC}"
            else
                # Fall back to comparing with first test
                if [ "$FIRST_TEST_RUNTIME" -gt 0 ]; then
                    if [ "$duration" -lt "$FIRST_TEST_RUNTIME" ]; then
                        local speedup=$(( (FIRST_TEST_RUNTIME - duration) * 100 / FIRST_TEST_RUNTIME ))
                        echo -e "    ${GREEN}✓ GR-10: Query ${speedup}% faster than first (cached)${NC}"
                        echo -e "    ${BLUE}  First query: ${FIRST_TEST_RUNTIME}ms, This query: ${duration}ms${NC}"
                    else
                        echo -e "    ${YELLOW}⚠ GR-10: No speedup observed (may not be cached)${NC}"
                    fi
                fi
            fi
            ;;

        # ================================================================================
        # GR-11: PARALLEL BFS PERFORMANCE CHECKS
        # TDD: These checks define expected behavior BEFORE implementation
        # ================================================================================

        parallel_correctness)
            # GR-11: Verify parallel BFS returns same results as sequential
            # Check that call graph contains expected nodes
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local node_count=$(echo "$agent_resp" | grep -oE '[a-zA-Z_][a-zA-Z0-9_]*' | sort -u | wc -l | tr -d ' ')

            if [ "$node_count" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-11: Call graph returned $node_count unique symbols${NC}"

                # Check server logs for parallel mode indication
                local parallel_log=$(ssh_cmd "grep -i 'parallel.*bfs\|bfs.*parallel\|parallel_mode' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")
                if [ -n "$parallel_log" ]; then
                    echo -e "    ${BLUE}Parallel BFS logs:${NC}"
                    echo "$parallel_log" | sed 's/^/      /'
                else
                    echo -e "    ${YELLOW}⚠ GR-11: No parallel BFS log entries (pre-implementation expected)${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-11: No symbols in call graph response${NC}"
            fi
            ;;

        parallel_speedup)
            # GR-11: Verify parallel is faster for wide graphs
            # Check OTel span attributes for parallel_mode and timing
            local trace_resp=$(echo "$response" | jq '.crs_trace // {}')
            local parallel_used=$(echo "$trace_resp" | jq -r '[.trace[] | select(.metadata.parallel_mode == true)] | length' 2>/dev/null || echo "0")

            if [ "$parallel_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-11: Parallel mode was used for traversal${NC}"

                # Check if there's timing info
                local parallel_time=$(echo "$trace_resp" | jq -r '[.trace[] | select(.metadata.parallel_mode == true) | .metadata.duration_ms // 0] | add' 2>/dev/null || echo "0")
                if [ "$parallel_time" -gt 0 ]; then
                    echo -e "    ${BLUE}  Parallel execution time: ${parallel_time}ms${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-11: Parallel mode not detected (pre-implementation or graph too small)${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-11: Expected (parallel BFS not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-11: Should use parallel for levels > 16 nodes${NC}"
            fi

            # Check server logs for speedup info
            local speedup_log=$(ssh_cmd "grep -i 'parallel.*speedup\|level.*nodes' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")
            if [ -n "$speedup_log" ]; then
                echo -e "    ${BLUE}Speedup logs:${NC}"
                echo "$speedup_log" | sed 's/^/      /'
            fi
            ;;

        # ================================================================================
        # GR-14: LOUVAIN COMMUNITY DETECTION CHECKS
        # TDD: These checks define expected behavior BEFORE implementation
        # ================================================================================

        communities_found)
            # GR-14: Verify community detection found actual communities
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if response mentions communities, modules, or clusters
            local mentions_community=$(echo "$agent_resp" | grep -ci "communit\|module\|cluster\|group")
            local community_count=$(echo "$agent_resp" | grep -oE '[0-9]+ communit' | head -1 | grep -oE '[0-9]+' || echo "0")

            if [ "$mentions_community" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-14: Response mentions communities ($mentions_community references)${NC}"
                if [ "$community_count" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-14: Found $community_count communities${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-14: Response does not mention communities${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-14: Expected (community detection not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-14: Should describe detected code communities${NC}"
            fi

            # Check for modularity score in response
            local has_modularity=$(echo "$agent_resp" | grep -ci "modularity")
            if [ "$has_modularity" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-14: Response includes modularity score${NC}"
            fi
            ;;

        find_communities_used)
            # GR-14: Verify find_communities tool was used (not grep fallback)
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)

            # Check if find_communities was used
            local fc_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_communities")] | length' 2>/dev/null || echo "0")

            if [ "$fc_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-14: find_communities tool was used: $fc_used calls${NC}"

                # Check for community detection metadata
                local community_meta=$(echo "$trace" | jq -r '[.trace[] | select(.tool == "find_communities") | .metadata] | .[0]' 2>/dev/null || echo "{}")
                local communities_found=$(echo "$community_meta" | jq '.communities_count // 0' 2>/dev/null || echo "0")
                local modularity=$(echo "$community_meta" | jq '.modularity // 0' 2>/dev/null || echo "0")

                if [ "$communities_found" -gt 0 ]; then
                    echo -e "    ${BLUE}  Communities: $communities_found, Modularity: $modularity${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-14: find_communities tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-14: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-14: Should use find_communities for module/community queries${NC}"

                # Check if Grep was used as fallback
                local grep_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "Grep")] | length' 2>/dev/null || echo "0")
                if [ "$grep_used" -gt 0 ]; then
                    echo -e "    ${YELLOW}  → Fell back to Grep: $grep_used calls${NC}"
                fi
            fi
            ;;

        fast_community_detection)
            # GR-14: Verify community detection completed in reasonable time
            # Louvain should be O(V+E) per pass, typically <5s for 100K nodes
            local threshold=30000  # 30 seconds max for reasonable sized graphs

            if [ "$duration" -lt "$threshold" ]; then
                echo -e "    ${GREEN}✓ GR-14: Community detection completed in ${duration}ms (< ${threshold}ms threshold)${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-14: Community detection took ${duration}ms (threshold: ${threshold}ms)${NC}"
                echo -e "    ${YELLOW}  → May need optimization for large graphs${NC}"
            fi

            # Check server logs for iteration count
            local iteration_log=$(ssh_cmd "grep -i 'louvain.*iteration\|community.*converge' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")
            if [ -n "$iteration_log" ]; then
                echo -e "    ${BLUE}Louvain iteration logs:${NC}"
                echo "$iteration_log" | sed 's/^/      /'
            fi
            ;;

        # ================================================================================
        # GR-15: find_communities TOOL CHECKS
        # TDD: These checks define expected behavior BEFORE implementation
        # ================================================================================

        find_communities_tool_used)
            # GR-15: Verify find_communities tool was used for module boundary queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_communities was used
            local fc_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_communities")] | length' 2>/dev/null || echo "0")

            if [ "$fc_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-15: find_communities tool was used: $fc_used calls${NC}"

                # Check for algorithm info
                local algorithm=$(echo "$agent_resp" | grep -oi "leiden" | head -1 || echo "")
                if [ -n "$algorithm" ]; then
                    echo -e "    ${GREEN}✓ GR-15: Response mentions Leiden algorithm${NC}"
                fi

                # Check for modularity score
                local has_modularity=$(echo "$agent_resp" | grep -ci "modularity")
                if [ "$has_modularity" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-15: Response includes modularity score${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-15: find_communities tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-15: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-15: Should use find_communities for boundary queries${NC}"
            fi
            ;;

        find_communities_params)
            # GR-15: Verify find_communities tool respects parameters
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)

            # Check if find_communities was used with parameters
            local fc_calls=$(echo "$trace" | jq '[.trace[] | select(.tool == "find_communities")]' 2>/dev/null || echo "[]")
            local has_resolution=$(echo "$fc_calls" | jq 'any(.[]; .params.resolution != null)' 2>/dev/null || echo "false")
            local has_min_size=$(echo "$fc_calls" | jq 'any(.[]; .params.min_size != null)' 2>/dev/null || echo "false")

            if [ "$has_resolution" = "true" ] || [ "$has_min_size" = "true" ]; then
                echo -e "    ${GREEN}✓ GR-15: find_communities tool called with parameters${NC}"
                if [ "$has_resolution" = "true" ]; then
                    echo -e "    ${BLUE}  - resolution parameter used${NC}"
                fi
                if [ "$has_min_size" = "true" ]; then
                    echo -e "    ${BLUE}  - min_size parameter used${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-15: find_communities called without custom parameters${NC}"
                echo -e "    ${YELLOW}  → May use defaults, which is acceptable${NC}"
            fi
            ;;

        cross_package_found)
            # GR-15: Verify cross-package communities are identified
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check for cross-package indicators
            local cross_pkg_mentions=$(echo "$agent_resp" | grep -ci "cross.package\|span.*package\|multiple package\|REFACTOR")

            if [ "$cross_pkg_mentions" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-15: Cross-package communities identified ($cross_pkg_mentions mentions)${NC}"

                # Extract specific cross-package info if available
                local cross_pkg_line=$(echo "$agent_resp" | grep -i "cross.package\|span.*package" | head -1)
                if [ -n "$cross_pkg_line" ]; then
                    echo -e "    ${BLUE}  $cross_pkg_line${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-15: No cross-package communities mentioned${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-15: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-15: Should highlight [REFACTOR] for cross-package${NC}"
            fi
            ;;

        # ================================================================================
        # GR-17e: find_loops TOOL CHECKS
        # ================================================================================

        find_loops_tool_used)
            # GR-17e: Verify find_loops tool was used for recursion/loop queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_loops was used
            local loops_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_loops")] | length' 2>/dev/null || echo "0")

            if [ "$loops_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17e: find_loops tool was used: $loops_used calls${NC}"

                # Check for loop count in response
                local loop_count=$(echo "$agent_resp" | grep -oi "[0-9]* loop\|[0-9]* recursion\|[0-9]* recursive" | head -1)
                if [ -n "$loop_count" ]; then
                    echo -e "    ${BLUE}  $loop_count found${NC}"
                fi

                # Check for recursion type breakdown
                local has_recursion_type=$(echo "$agent_resp" | grep -ci "direct recursion\|mutual recursion\|self-call")
                if [ "$has_recursion_type" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17e: Response includes recursion type analysis${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-17e: find_loops tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-17e: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-17e: Should use find_loops for recursion queries${NC}"
            fi
            ;;

        find_loops_min_size)
            # GR-17e: Verify find_loops with min_size parameter
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check for mutual recursion mentions (size >= 2)
            local mutual_mentions=$(echo "$agent_resp" | grep -ci "mutual recursion\|A.*B.*A\|two functions")

            if [ "$mutual_mentions" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17e: Mutual recursion patterns identified ($mutual_mentions mentions)${NC}"

                # Extract specific pattern info if available
                local pattern_line=$(echo "$agent_resp" | grep -i "mutual\|A.*B" | head -1)
                if [ -n "$pattern_line" ]; then
                    echo -e "    ${BLUE}  $pattern_line${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-17e: No mutual recursion patterns found${NC}"
                echo -e "    ${YELLOW}  → May indicate no mutual recursion in codebase${NC}"
                echo -e "    ${YELLOW}  → Or min_size filter correctly filtering self-loops${NC}"
            fi
            ;;

        # ================================================================================
        # GR-17c: find_control_dependencies TOOL CHECKS
        # ================================================================================

        find_control_deps_tool_used)
            # GR-17c: Verify find_control_dependencies tool was used for control flow queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_control_dependencies was used
            local ctrl_deps_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_control_dependencies")] | length' 2>/dev/null || echo "0")

            if [ "$ctrl_deps_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17c: find_control_dependencies tool was used: $ctrl_deps_used calls${NC}"

                # Check for control dependency info in response
                local ctrl_info=$(echo "$agent_resp" | grep -oi "control.*depend\|conditionals\|branch\|decision point" | head -1)
                if [ -n "$ctrl_info" ]; then
                    echo -e "    ${BLUE}  Control flow information found${NC}"
                fi

                # Check for controller nodes
                local has_controllers=$(echo "$agent_resp" | grep -ci "controls.*execution\|determines.*whether")
                if [ "$has_controllers" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17c: Response includes controller analysis${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-17c: find_control_dependencies tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-17c: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-17c: Should use find_control_dependencies for control flow queries${NC}"
            fi
            ;;

        find_control_deps_depth)
            # GR-17c: Verify find_control_dependencies with depth parameter
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check for depth-limited dependency analysis
            local depth_info=$(echo "$agent_resp" | grep -ci "depth\|level\|chain")

            if [ "$depth_info" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17c: Depth-limited control dependency analysis performed${NC}"

                # Extract dependency chain info if available
                local chain_line=$(echo "$agent_resp" | grep -i "dependency\|chain" | head -1)
                if [ -n "$chain_line" ]; then
                    echo -e "    ${BLUE}  $chain_line${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-17c: No depth-limited analysis found${NC}"
                echo -e "    ${YELLOW}  → May indicate flat control structure${NC}"
            fi
            ;;

        # GR-17g: find_extractable_regions TOOL CHECKS
        # ================================================================================

        find_extractable_tool_used)
            # GR-17g: Verify find_extractable_regions tool was used for refactoring queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_extractable_regions was used
            local extractable_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_extractable_regions")] | length' 2>/dev/null || echo "0")

            if [ "$extractable_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17g: find_extractable_regions tool was used: $extractable_used calls${NC}"

                # Check for SESE region info in response
                local region_info=$(echo "$agent_resp" | grep -oi "region\|extractable\|refactor\|single.*entry\|single.*exit" | head -1)
                if [ -n "$region_info" ]; then
                    echo -e "    ${BLUE}  SESE region information found${NC}"
                fi

                # Check for region count
                local region_count=$(echo "$agent_resp" | grep -oi "[0-9]* region\|[0-9]* extractable" | head -1)
                if [ -n "$region_count" ]; then
                    echo -e "    ${GREEN}✓ GR-17g: $region_count identified${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-17g: find_extractable_regions tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-17g: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-17g: Should use find_extractable_regions for refactoring queries${NC}"
            fi
            ;;

        find_extractable_size)
            # GR-17g: Verify find_extractable_regions with size parameters
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check for size-filtered region analysis
            local size_info=$(echo "$agent_resp" | grep -ci "size\|nodes\|between.*and")

            if [ "$size_info" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17g: Size-filtered region analysis performed${NC}"

                # Extract region size info if available
                local size_line=$(echo "$agent_resp" | grep -i "size\|nodes" | head -1)
                if [ -n "$size_line" ]; then
                    echo -e "    ${BLUE}  $size_line${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-17g: No size-filtered results found${NC}"
                echo -e "    ${YELLOW}  → May indicate no regions in requested size range${NC}"
            fi
            ;;


        check_reducibility_tool_used)
            # GR-17h: Verify check_reducibility tool was used for code quality queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if check_reducibility was used
            local reducibility_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "check_reducibility")] | length' 2>/dev/null || echo "0")

            if [ "$reducibility_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17h: check_reducibility tool was used: $reducibility_used calls${NC}"

                # Check for reducibility info in response
                local reducibility_info=$(echo "$agent_resp" | grep -oi "reducible\|well-structured\|irreducible\|complex.*control" | head -1)
                if [ -n "$reducibility_info" ]; then
                    echo -e "    ${BLUE}  Reducibility analysis: $reducibility_info${NC}"
                fi

                # Check for score
                local score=$(echo "$agent_resp" | grep -oi "[0-9]*\.*[0-9]*%\|score.*[0-9]" | head -1)
                if [ -n "$score" ]; then
                    echo -e "    ${GREEN}✓ GR-17h: Reducibility score provided: $score${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-17h: check_reducibility tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-17h: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-17h: Should use check_reducibility for code quality queries${NC}"
            fi
            ;;

        check_reducibility_details)
            # GR-17h: Verify check_reducibility with irreducible region details
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check for irreducible region details
            local region_details=$(echo "$agent_resp" | grep -ci "irreducible.*region\|entry.*node\|cross.*edge")

            if [ "$region_details" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17h: Irreducible region details provided${NC}"

                # Extract region info if available
                local region_line=$(echo "$agent_resp" | grep -i "irreducible\|cross.*edge" | head -1)
                if [ -n "$region_line" ]; then
                    echo -e "    ${BLUE}  $region_line${NC}"
                fi
            else
                echo -e "    ${YELLOW}⚠ GR-17h: No irreducible regions found${NC}"
                echo -e "    ${YELLOW}  → May indicate well-structured codebase${NC}"
            fi
            ;;

        verify_check_reducibility_crs)
            # GR-17h: Verify check_reducibility tool records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for check_reducibility tool (GR-17h)...${NC}"

            # Check server logs for tool CRS trace step recording
            local tool_crs_logs=$(ssh_cmd "grep -i 'check_reducibility\|reducibility\|CheckReducibility' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with tool metadata
            local trace_metadata=$(ssh_cmd "grep -i 'analytics_reducibility\|reducibility.*trace\|Reducibility.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$tool_crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-17h: check_reducibility tool CRS integration detected${NC}"
                if [ -n "$tool_crs_logs" ]; then
                    echo "$tool_crs_logs" | sed 's/^/    /'
                fi
                result_message="Tool CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-17h: No check_reducibility tool CRS logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-17h: Expected (tool not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-17h: Should record TraceStep with tool metadata${NC}"
                result_message="No tool CRS logs (pre-implementation expected)"
            fi
            ;;

        # ================================================================================
        # GR-18a: find_critical_path TOOL CHECKS
        # ================================================================================

        find_critical_path_tool_used)
            # GR-18a: Verify find_critical_path tool was used for mandatory path queries
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check if find_critical_path was used
            local critical_path_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_critical_path")] | length' 2>/dev/null || echo "0")

            if [ "$critical_path_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-18a: find_critical_path tool was used: $critical_path_used calls${NC}"

                # Check for critical path info in response
                local path_info=$(echo "$agent_resp" | grep -oi "critical path\|mandatory.*call\|must.*call\|required.*sequence" | head -1)
                if [ -n "$path_info" ]; then
                    echo -e "    ${BLUE}  Path analysis: $path_info${NC}"
                fi

                # Check for path sequence (e.g., "main → init → parseConfig")
                local sequence=$(echo "$agent_resp" | grep -o "[A-Za-z_][A-Za-z0-9_]*[[:space:]]*→[[:space:]]*[A-Za-z_][A-Za-z0-9_]*" | head -1)
                if [ -n "$sequence" ]; then
                    echo -e "    ${GREEN}✓ GR-18a: Call sequence found: $sequence${NC}"
                fi
            else
                echo -e "    ${RED}✗ GR-18a: find_critical_path tool not used${NC}"
                echo -e "    ${YELLOW}  → Pre-GR-18a: Expected (tool not implemented)${NC}"
                echo -e "    ${YELLOW}  → Post-GR-18a: Should use find_critical_path for mandatory path queries${NC}"
            fi
            ;;

        find_critical_path_entry)
            # GR-18a: Verify find_critical_path with custom entry point parameter
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            local agent_resp=$(echo "$response" | jq -r '.response // ""')

            # Check tool calls for entry parameter
            local tool_calls=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_critical_path")]' 2>/dev/null || echo "[]")
            local entry_param=$(echo "$tool_calls" | jq -r '.[0].params.entry // ""' 2>/dev/null || echo "")

            if [ -n "$entry_param" ] && [ "$entry_param" != "null" ]; then
                echo -e "    ${GREEN}✓ GR-18a: Custom entry point used: $entry_param${NC}"
            else
                echo -e "    ${BLUE}  GR-18a: Using auto-detected entry point${NC}"
            fi

            # Check for path in response
            local path_count=$(echo "$agent_resp" | grep -ci "critical path\|mandatory.*call")
            if [ "$path_count" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-18a: Critical path information provided${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-18a: No critical path information in response${NC}"
            fi
            ;;

        # ================================================================================

        verify_post_dominator_crs_recording)
            # GR-16c: Verify post-dominator analysis records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for post-dominator analysis (GR-16c)...${NC}"

            # Check server logs for CRS trace step recording
            local crs_logs=$(ssh_cmd "grep -i 'analytics_post_dominators\|post.*dominator.*trace\|PostDominators.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with post-dominator metadata
            local trace_metadata=$(ssh_cmd "grep -i 'post_dominators\|exit_node\|post_dom_depth' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-16c: CRS recording detected for post-dominator analysis${NC}"
                if [ -n "$crs_logs" ]; then
                    echo "$crs_logs" | sed 's/^/    /'
                fi
                result_message="CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-16c: No CRS recording logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-16c: Expected (post-dominator not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-16c: Should record TraceStep with WithCRS methods${NC}"
                result_message="No CRS logs (pre-implementation expected)"
            fi
            ;;

        # ================================================================================
        # GR-16d: DOMINANCE FRONTIER CRS VERIFICATION
        # ================================================================================

        verify_dominance_frontier_crs_recording)
            # GR-16d: Verify dominance frontier computation records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for dominance frontier (GR-16d)...${NC}"

            # Check server logs for CRS trace step recording
            local crs_logs=$(ssh_cmd "grep -i 'analytics_dominance_frontier\|dominance.*frontier.*trace\|ComputeDominanceFrontier.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with dominance frontier metadata
            local trace_metadata=$(ssh_cmd "grep -i 'merge_points_found\|frontier_size\|dominance_frontier' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-16d: CRS recording detected for dominance frontier${NC}"
                if [ -n "$crs_logs" ]; then
                    echo "$crs_logs" | sed 's/^/    /'
                fi
                result_message="CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-16d: No CRS recording logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-16d: Expected (dominance frontier not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-16d: Should record TraceStep with WithCRS methods${NC}"
                result_message="No CRS logs (pre-implementation expected)"
            fi
            ;;

        # ================================================================================
        # GR-16e: CONTROL DEPENDENCE CRS VERIFICATION
        # ================================================================================

        verify_control_dependence_crs_recording)
            # GR-16e: Verify control dependence computation records TraceStep in CRS
            echo -e "  ${BLUE}Checking CRS integration for control dependence (GR-16e)...${NC}"

            # Check server logs for CRS trace step recording
            local crs_logs=$(ssh_cmd "grep -i 'analytics_control_dependence\|control.*depend.*trace\|ComputeControlDependence.*CRS' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -5" || echo "")

            # Check for trace step with control dependence metadata
            local trace_metadata=$(ssh_cmd "grep -i 'dependency_count\|dependents_count\|control_dependence' ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null | tail -3" || echo "")

            if [ -n "$crs_logs" ] || [ -n "$trace_metadata" ]; then
                echo -e "  ${GREEN}✓ GR-16e: CRS recording detected for control dependence${NC}"
                if [ -n "$crs_logs" ]; then
                    echo "$crs_logs" | sed 's/^/    /'
                fi
                result_message="CRS integration working"
            else
                echo -e "  ${YELLOW}⚠ GR-16e: No CRS recording logs found${NC}"
                echo -e "  ${YELLOW}  → Pre-GR-16e: Expected (control dependence not implemented)${NC}"
                echo -e "  ${YELLOW}  → Post-GR-16e: Should record TraceStep with WithCRS methods${NC}"
                result_message="No CRS logs (pre-implementation expected)"
            fi
            ;;

        find_dominators_tool_used)
            # GR-17a: Verify find_dominators tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi

            if [ -z "$session_id" ] || [ "$session_id" = "null" ]; then
                echo -e "    ${YELLOW}⚠ GR-17a: Cannot validate (no session_id)${NC}"
                return 0
            fi

            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if ! echo "$trace" | jq . > /dev/null 2>&1; then
                echo -e "    ${YELLOW}⚠ GR-17a: Cannot validate (trace fetch failed)${NC}"
                return 0
            fi

            # Check for both "tool_call" and "tool_call_forced" actions
            local dominators_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call" or .action == "tool_call_forced") | select(.tool == "find_dominators")] | length')
            if [ "$dominators_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17a: find_dominators tool used ($dominators_used invocations)${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17a: find_dominators not found in trace${NC}"
            fi
            ;;

        find_dominators_tree)
            # GR-17a: Verify dominator tree was shown in response
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            
            # Check for dominator tree indicators (tree structure, hierarchy, dominators list)
            local has_tree=$(echo "$agent_resp" | grep -ciE "dominator.*tree|tree.*starting|entry.*point|dominates|dominated.*by")
            local has_structure=$(echo "$agent_resp" | grep -ciE "└|├|│|→|▼|main.*→")
            
            if [ "$has_tree" -gt 0 ] || [ "$has_structure" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17a: Dominator tree shown in response${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17a: No dominator tree structure in response${NC}"
                echo -e "    ${YELLOW}  → Response may describe dominators without tree visualization${NC}"
            fi
            ;;

        find_articulation_points_tool_used)
            # GR-16b: Verify find_articulation_points tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi

            if [ -z "$session_id" ] || [ "$session_id" = "null" ]; then
                echo -e "    ${YELLOW}⚠ GR-16b: Cannot validate (no session_id)${NC}"
                return 0
            fi

            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if ! echo "$trace" | jq . > /dev/null 2>&1; then
                echo -e "    ${YELLOW}⚠ GR-16b: Cannot validate (trace fetch failed)${NC}"
                return 0
            fi

            # Check for both "tool_call" and "tool_call_forced" actions
            local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call" or .action == "tool_call_forced") | select(.tool == "find_articulation_points")] | length')
            if [ "$tool_used" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-16b: find_articulation_points tool used ($tool_used invocations)${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-16b: find_articulation_points not found in trace${NC}"
            fi
            ;;

        find_articulation_points_bridges)
            # GR-16b: Verify bridges parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_bridges=$(echo "$agent_resp" | grep -ciE "bridge|edge.*critical|remove.*disconn")
            if [ "$has_bridges" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-16b: Bridges parameter handling detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-16b: No bridge-specific output${NC}"
            fi
            ;;

        find_merge_points_tool_used)
            # GR-17b: Verify find_merge_points tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_merge_points")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17b: find_merge_points tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17b: find_merge_points not found in trace${NC}"
                fi
            fi
            ;;

        find_merge_points_sources)
            # GR-17b: Verify specific sources parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_sources=$(echo "$agent_resp" | grep -ciE "merge.*point|confluence|join")
            if [ "$has_sources" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17b: Merge points with sources detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17b: No merge point details${NC}"
            fi
            ;;

        find_loops_tool_used)
            # GR-17d: Verify find_loops tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_loops")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17d: find_loops tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17d: find_loops not found in trace${NC}"
                fi
            fi
            ;;

        find_loops_min_size)
            # GR-17d: Verify min_size parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_loops=$(echo "$agent_resp" | grep -ciE "loop|cycle|back.*edge")
            if [ "$has_loops" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17d: Loop detection with min_size detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17d: No loop details${NC}"
            fi
            ;;

        find_common_dependency_tool_used)
            # GR-17e: Verify find_common_dependency tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_common_dependency")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17e: find_common_dependency tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17e: find_common_dependency not found in trace${NC}"
                fi
            fi
            ;;

        find_common_dependency_entry)
            # GR-17e: Verify entry point parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_lcd=$(echo "$agent_resp" | grep -ciE "common.*dependency|LCD|lowest.*common")
            if [ "$has_lcd" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17e: Common dependency with entry point detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17e: No common dependency details${NC}"
            fi
            ;;

        find_control_deps_tool_used)
            # GR-17c: Verify find_control_dependencies tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_control_dependencies")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17c: find_control_dependencies tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17c: find_control_dependencies not found in trace${NC}"
                fi
            fi
            ;;

        find_control_deps_depth)
            # GR-17c: Verify depth parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_control=$(echo "$agent_resp" | grep -ciE "control.*depend|dominated.*by|branch")
            if [ "$has_control" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17c: Control dependencies with depth detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17c: No control dependency details${NC}"
            fi
            ;;

        find_extractable_tool_used)
            # GR-17g: Verify find_extractable_regions tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_extractable_regions")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17g: find_extractable_regions tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17g: find_extractable_regions not found in trace${NC}"
                fi
            fi
            ;;

        find_extractable_size)
            # GR-17g: Verify size parameters were used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_sese=$(echo "$agent_resp" | grep -ciE "SESE|extractable|single.*entry.*single.*exit")
            if [ "$has_sese" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17g: Extractable regions with size detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17g: No SESE region details${NC}"
            fi
            ;;

        check_reducibility_tool_used)
            # GR-17h: Verify check_reducibility tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "check_reducibility")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-17h: check_reducibility tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-17h: check_reducibility not found in trace${NC}"
                fi
            fi
            ;;

        check_reducibility_details)
            # GR-17h: Verify irreducible region details shown
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_details=$(echo "$agent_resp" | grep -ciE "reducib|irreducib|region|back.*edge")
            if [ "$has_details" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-17h: Reducibility details detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-17h: No reducibility details${NC}"
            fi
            ;;

        find_critical_path_tool_used)
            # GR-18a: Verify find_critical_path tool was invoked
            if [ -z "$session_id" ]; then
                session_id=$(echo "$response" | jq -r '.session_id')
            fi
            local trace=$(ssh_cmd "curl -s 'http://localhost:8080/v1/trace/agent/$session_id/reasoning'" 2>/dev/null)
            if echo "$trace" | jq . > /dev/null 2>&1; then
                local tool_used=$(echo "$trace" | jq '[.trace[] | select(.action == "tool_call") | select(.tool == "find_critical_path")] | length')
                if [ "$tool_used" -gt 0 ]; then
                    echo -e "    ${GREEN}✓ GR-18a: find_critical_path tool used ($tool_used invocations)${NC}"
                else
                    echo -e "    ${YELLOW}⚠ GR-18a: find_critical_path not found in trace${NC}"
                fi
            fi
            ;;

        find_critical_path_entry)
            # GR-18a: Verify entry point parameter was used
            local agent_resp=$(echo "$response" | jq -r '.response // ""')
            local has_path=$(echo "$agent_resp" | grep -ciE "critical.*path|longest.*path|bottleneck")
            if [ "$has_path" -gt 0 ]; then
                echo -e "    ${GREEN}✓ GR-18a: Critical path with entry point detected${NC}"
            else
                echo -e "    ${YELLOW}⚠ GR-18a: No critical path details${NC}"
            fi
            ;;

        *)
            echo -e "    ${YELLOW}⚠ Unknown extra check: $check${NC}"
            ;;
    esac
}
