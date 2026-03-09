#!/usr/bin/env bash
# Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
# AGPL v3 - See LICENSE.txt

# Trace Breakdown: Analyze Jaeger traces from aleutian-trace service.
#
# Usage:
#   ./scripts/trace_breakdown.sh                  # Latest trace > 1s
#   ./scripts/trace_breakdown.sh <trace_id>       # Specific trace
#   ./scripts/trace_breakdown.sh --list           # List recent traces
#   ./scripts/trace_breakdown.sh --list 20        # List last 20 traces

set -euo pipefail

JAEGER_URL="${JAEGER_URL:-http://localhost:12214}"
SERVICE="${TRACE_SERVICE:-aleutian-trace}"
LOOKBACK="${LOOKBACK:-2h}"

# Check Jaeger is reachable
if ! curl -sf "${JAEGER_URL}/api/services" > /dev/null 2>&1; then
    echo "ERROR: Jaeger not reachable at ${JAEGER_URL}"
    echo "  Check: podman ps | grep jaeger"
    echo "  Set JAEGER_URL env var if port differs"
    exit 1
fi

list_traces() {
    local limit="${1:-10}"
    local end_us
    local start_us
    end_us=$(python3 -c "import time; print(int(time.time() * 1000000))")
    start_us=$(python3 -c "import time; print(int((time.time() - 7200) * 1000000))")

    curl -s "${JAEGER_URL}/api/traces?service=${SERVICE}&start=${start_us}&end=${end_us}&limit=${limit}&minDuration=100ms&lookback=${LOOKBACK}" | python3 -c "
import json, sys, datetime

data = json.loads(sys.stdin.read())
traces = data.get('data') or []

if not traces:
    print('No traces found > 100ms in last ${LOOKBACK}')
    sys.exit(0)

print(f'Recent traces from ${SERVICE} (>{100}ms, last ${LOOKBACK}):')
print()
print(f'{\"#\":>3}  {\"Trace ID\":<34} {\"Spans\":>7} {\"Duration\":>10}  {\"Root Operation\"}')
print('-' * 100)

for i, t in enumerate(traces):
    spans = t['spans']
    spans.sort(key=lambda s: s['startTime'])
    base = spans[0]['startTime']
    total_dur = max(s['startTime'] + s['duration'] - base for s in spans) / 1000

    # Find root span
    root_op = '?'
    for s in spans:
        refs = s.get('references', [])
        if not refs or not any(r['refType'] == 'CHILD_OF' for r in refs):
            root_op = s['operationName']
            break

    dur_str = f'{total_dur:.0f}ms' if total_dur < 1000 else f'{total_dur/1000:.1f}s'
    print(f'{i+1:>3}  {t[\"traceID\"]:<34} {len(spans):>7} {dur_str:>10}  {root_op[:50]}')
"
}

analyze_trace() {
    local trace_id="$1"

    curl -s "${JAEGER_URL}/api/traces/${trace_id}" | python3 -c "
import json, sys
from collections import defaultdict

data = json.loads(sys.stdin.read())
if not data.get('data'):
    print(f'No trace found: ${trace_id}')
    sys.exit(1)

trace = data['data'][0]
spans = trace['spans']
processes = trace['processes']
proc_names = {pid: p['serviceName'] for pid, p in processes.items()}

# Find root
root = None
for s in spans:
    refs = s.get('references', [])
    if not refs or not any(r['refType'] == 'CHILD_OF' for r in refs):
        root = s
        break
if not root:
    root = min(spans, key=lambda s: s['startTime'])

base = root['startTime']
total_ms = root['duration'] / 1000

# --- HEADER ---
print('=' * 90)
print(f'TRACE BREAKDOWN: {trace[\"traceID\"]}')
print(f'Root:  {root[\"operationName\"]}')
print(f'Total: {total_ms:.0f}ms ({total_ms/1000:.1f}s)')
print(f'Spans: {len(spans)}')
print(f'Services: {\", \".join(sorted(set(proc_names.values())))}')
print('=' * 90)

# --- KEY PHASES ---
key_ops = {
    'POST /v1/trace/agent/run',
    'POST /v1/trace/init',
    'MultiModelManager.Chat',
    'OllamaClient.Chat',
    'agent.llm.OllamaAdapter.Complete',
    'GraphBuilder.Build',
    'routing.PreFilter.Filter',
    'Assembler.Assemble',
    'SymbolIndex.Search',
    'ParamExtractor.ExtractParams',
    'rag.CombinedResolver.Resolve',
    'rag.StructuralResolver.Resolve',
    'rag.SemanticResolver.Resolve',
    'warmMainModel',
    'ExecutePhase.tryToolRouterSelection',
    'Granite4Router.SelectTool',
    'crs.Journal.Append',
    'crs.Journal.Replay',
    'crs.Journal.Checkpoint',
    'rag.SymbolStore.IndexSymbols',
    'rag.SymbolStore.DeleteAll',
    'rag.EmbedClient.EmbedDocuments',
    'findCalleesTool.Execute',
    'findCallersTool.Execute',
    'semanticSearchTool.Execute',
    'findSimilarSymbolsTool.Execute',
    'findSymbolTool.Execute',
    'getCallChainTool.Execute',
    'findImplementationsTool.Execute',
    'findReferencesTool.Execute',
}

sig_spans = []
for s in spans:
    op = s['operationName']
    dur_ms = s['duration'] / 1000
    if op in key_ops or dur_ms > 500:
        sig_spans.append(s)

sig_spans.sort(key=lambda s: s['startTime'])

print()
print('KEY PHASES (>500ms or key operations):')
print(f'{\"Operation\":<55} {\"Start\":>9} {\"Duration\":>10} {\"% Total\":>8}')
print('-' * 87)
for s in sig_spans:
    op = s['operationName'][:54]
    offset = (s['startTime'] - base) / 1000
    dur = s['duration'] / 1000
    pct = (s['duration'] / root['duration']) * 100 if root['duration'] > 0 else 0
    print(f'{op:<55} +{offset:>7.0f}ms {dur:>9.0f}ms {pct:>6.1f}%')

# --- AGGREGATE STATS ---
op_stats = defaultdict(lambda: {'count': 0, 'total_ms': 0, 'max_ms': 0})
for s in spans:
    op = s['operationName']
    dur = s['duration'] / 1000
    op_stats[op]['count'] += 1
    op_stats[op]['total_ms'] += dur
    op_stats[op]['max_ms'] = max(op_stats[op]['max_ms'], dur)

print()
print('TOP 20 OPERATIONS BY TOTAL TIME:')
print(f'{\"Operation\":<55} {\"Count\":>7} {\"Total\":>10} {\"Max\":>10}')
print('-' * 87)
sorted_ops = sorted(op_stats.items(), key=lambda x: -x[1]['total_ms'])
for op, stats in sorted_ops[:20]:
    print(f'{op[:54]:<55} {stats[\"count\"]:>7} {stats[\"total_ms\"]:>9.0f}ms {stats[\"max_ms\"]:>9.0f}ms')

# --- GAP ANALYSIS ---
# Find untraced gaps > 1s in the root span's timeline
print()
print('TIMELINE GAPS (>1s untraced):')

# Get all span intervals
intervals = []
for s in spans:
    start = (s['startTime'] - base) / 1000
    end = start + s['duration'] / 1000
    intervals.append((start, end))

intervals.sort()

# Merge overlapping intervals
merged = []
for start, end in intervals:
    if merged and start <= merged[-1][1]:
        merged[-1] = (merged[-1][0], max(merged[-1][1], end))
    else:
        merged.append((start, end))

# Find gaps
gaps_found = False
for i in range(len(merged) - 1):
    gap_start = merged[i][1]
    gap_end = merged[i+1][0]
    gap_ms = gap_end - gap_start
    if gap_ms > 1000:
        print(f'  GAP: +{gap_start:.0f}ms to +{gap_end:.0f}ms ({gap_ms:.0f}ms untraced)')
        gaps_found = True

# Check gap at start
if merged and merged[0][0] > 1000:
    print(f'  GAP: +0ms to +{merged[0][0]:.0f}ms ({merged[0][0]:.0f}ms before first span)')
    gaps_found = True

# Check gap at end
if merged and total_ms - merged[-1][1] > 1000:
    trail = total_ms - merged[-1][1]
    print(f'  GAP: +{merged[-1][1]:.0f}ms to +{total_ms:.0f}ms ({trail:.0f}ms trailing)')
    gaps_found = True

if not gaps_found:
    print('  No significant gaps found')

# --- SUMMARY ---
print()
print('SUMMARY:')

# Categorize time
llm_time = 0
parse_time = 0
graph_time = 0
tool_time = 0
routing_time = 0
index_time = 0

for s in spans:
    op = s['operationName']
    dur = s['duration'] / 1000
    if op in ('OllamaClient.Chat', 'agent.llm.OllamaAdapter.Complete'):
        llm_time = max(llm_time, dur)  # overlapping, take max
    elif op == 'Parser.Parse':
        pass  # counted in graph build
    elif op == 'GraphBuilder.Build':
        graph_time += dur
    elif op.endswith('Tool.Execute') or op.endswith('tool.Execute'):
        tool_time += dur
    elif op in ('routing.PreFilter.Filter', 'Granite4Router.SelectTool', 'ExecutePhase.tryToolRouterSelection'):
        routing_time = max(routing_time, dur)
    elif op.startswith('rag.SymbolStore') or op.startswith('rag.EmbedClient'):
        index_time += dur

accounted = llm_time + graph_time + tool_time + routing_time + index_time
unaccounted = total_ms - accounted

bar_width = 40
categories = [
    ('LLM Inference', llm_time, '\033[91m'),      # red
    ('Graph Build', graph_time, '\033[93m'),        # yellow
    ('Tool Routing', routing_time, '\033[96m'),     # cyan
    ('Tool Execution', tool_time, '\033[92m'),      # green
    ('Symbol Indexing', index_time, '\033[95m'),    # magenta
    ('Other/Untraced', unaccounted, '\033[90m'),    # gray
]

for name, ms, color in categories:
    pct = (ms / total_ms * 100) if total_ms > 0 else 0
    bar_len = int(pct / 100 * bar_width)
    bar = '\u2588' * bar_len
    reset = '\033[0m'
    dur_str = f'{ms:.0f}ms' if ms < 1000 else f'{ms/1000:.1f}s'
    print(f'  {color}{bar:<{bar_width}}{reset}  {name:<18} {dur_str:>8} ({pct:.1f}%)')

print()
"
}

find_latest_trace() {
    local end_us
    local start_us
    end_us=$(python3 -c "import time; print(int(time.time() * 1000000))")
    start_us=$(python3 -c "import time; print(int((time.time() - 7200) * 1000000))")

    curl -s "${JAEGER_URL}/api/traces?service=${SERVICE}&start=${start_us}&end=${end_us}&limit=1&minDuration=1s&lookback=${LOOKBACK}" | python3 -c "
import json, sys
data = json.loads(sys.stdin.read())
traces = data.get('data') or []
if not traces:
    print('')
else:
    print(traces[0]['traceID'])
"
}

# --- MAIN ---
case "${1:-}" in
    --list)
        list_traces "${2:-10}"
        ;;
    --help|-h)
        echo "Usage: $0 [trace_id | --list [N] | --help]"
        echo ""
        echo "  (no args)      Analyze latest trace > 1s"
        echo "  <trace_id>     Analyze specific trace"
        echo "  --list [N]     List last N traces > 100ms (default 10)"
        echo ""
        echo "Environment:"
        echo "  JAEGER_URL     Jaeger API (default: http://localhost:12214)"
        echo "  TRACE_SERVICE  Service name (default: aleutian-trace)"
        echo "  LOOKBACK       Time window (default: 2h)"
        ;;
    "")
        trace_id=$(find_latest_trace)
        if [ -z "$trace_id" ]; then
            echo "No traces > 1s found in last ${LOOKBACK}. Try: $0 --list"
            exit 1
        fi
        echo "Analyzing latest trace: ${trace_id}"
        echo ""
        analyze_trace "$trace_id"
        ;;
    *)
        analyze_trace "$1"
        ;;
esac
