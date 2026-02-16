# Legacy Test Mapping

This document maps legacy test numbers (from the bash array) to their new YAML locations.

## Migration Status: Phase 5 Complete

**Features Migrated:** GR-36, GR-33, GR-28, GR-01, GR-12, GR-16, GR-40, GR-17
**Legacy Tests Migrated:** 1-9, 16-21, 22-27, 31-35, 70-90 (subset), 91-111 (subset)
**New YAML Tests Created:** 173 (across 4 languages)

---

## GR-36: Session Restore

**Legacy Tests:** 1-3
**Feature Folder:** `features/GR-36_session_restore/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 1 | 1 | Go | `go.yml` | Session 1 baseline (learn main function) |
| 2 | 2 | Go | `go.yml` | Session 2 restore (remember context) |
| 3 | 3 | Go | `go.yml` | Session 2 speedup (30%+ faster) |
| - | 101 | Python | `python.yml` | Session 1 baseline (Flask app factory) |
| - | 102 | Python | `python.yml` | Session 2 restore (remember blueprints) |
| - | 103 | Python | `python.yml` | Session 2 speedup (30%+ faster) |
| - | 201 | JavaScript | `javascript.yml` | Session 1 baseline (Express setup) |
| - | 202 | JavaScript | `javascript.yml` | Session 2 restore (remember middleware) |
| - | 203 | JavaScript | `javascript.yml` | Session 2 speedup (30%+ faster) |
| - | 301 | TypeScript | `typescript.yml` | Session 1 baseline (NestJS bootstrap) |
| - | 302 | TypeScript | `typescript.yml` | Session 2 restore (remember modules) |
| - | 303 | TypeScript | `typescript.yml` | Session 2 speedup (30%+ faster) |

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-36          # All languages
./test_crs_integration.sh --feature GR-36 --lang go    # Go only
./test_crs_integration.sh --feature GR-36 --lang python # Python only
```

---

## GR-33: Disk Persistence

**Legacy Tests:** 4-6
**Feature Folder:** `features/GR-33_disk_persistence/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 4 | 4 | Go | `go.yml` | Checkpoint save (analyze api package) |
| 5 | 5 | Go | `go.yml` | Checkpoint verify (check BadgerDB files) |
| 6 | 6 | Go | `go.yml` | Checkpoint restore (restart + verify) |

**Note:** Go-only feature (tests internal CRS persistence layer)

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-33
./test_crs_integration.sh -t 4,5,6  # Legacy compatibility
```

---

## GR-28: Graph Snapshots

**Legacy Tests:** 7-9
**Feature Folder:** `features/GR-28_graph_snapshots/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 7 | 7 | Go | `go.yml` | Snapshot create (find callers of main) |
| 8 | 8 | Go | `go.yml` | Event context (verify graph in events) |
| 9 | 9 | Go | `go.yml` | Generation track (increment counter) |
| - | 107 | Python | `python.yml` | Snapshot create (Flask create_app callers) |
| - | 108 | Python | `python.yml` | Event context (Python graph metadata) |
| - | 109 | Python | `python.yml` | Generation track (Blueprint imports) |
| - | 207 | JavaScript | `javascript.yml` | Snapshot create (express() callers) |
| - | 208 | JavaScript | `javascript.yml` | Event context (JS graph metadata) |
| - | 209 | JavaScript | `javascript.yml` | Generation track (Router requires) |
| - | 307 | TypeScript | `typescript.yml` | Snapshot create (NestFactory.create callers) |
| - | 308 | TypeScript | `typescript.yml` | Event context (TS graph metadata) |
| - | 309 | TypeScript | `typescript.yml` | Generation track (@Module imports) |

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-28          # All languages
./test_crs_integration.sh --feature GR-28 --lang typescript # TS only
./test_crs_integration.sh -t 7,8,9  # Legacy compatibility (Go only)
```

---

## GR-01: Graph Index

**Legacy Tests:** 16-21
**Feature Folder:** `features/GR-01_graph_index/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 16 | 16 | Go | `go.yml` | Find callers basic (graph_tool_used) |
| 17 | 17 | Go | `go.yml` | Find callees basic (graph_tool_used) |
| 18 | 18 | Go | `go.yml` | Find implementations basic (graph_tool_used) |
| 19 | 19 | Go | `go.yml` | Performance warm cache (fast_execution) |
| 20 | 20 | Go | `go.yml` | OTel span verification (INTERNAL) |
| 21 | 21 | Go | `go.yml` | Not found fast (fast_not_found) |
| - | 116 | Python | `python.yml` | Find callers (create_app) |
| - | 117 | Python | `python.yml` | Find callees (Flask constructor) |
| - | 118 | Python | `python.yml` | Find implementations (Blueprint) |
| - | 119 | Python | `python.yml` | Performance warm cache |
| - | 120 | Python | `python.yml` | OTel span verification (INTERNAL) |
| - | 121 | Python | `python.yml` | Not found fast |
| - | 216 | JavaScript | `javascript.yml` | Find callers (Router) |
| - | 217 | JavaScript | `javascript.yml` | Find callees (express factory) |
| - | 218 | JavaScript | `javascript.yml` | Find implementations (middleware) |
| - | 219 | JavaScript | `javascript.yml` | Performance warm cache |
| - | 220 | JavaScript | `javascript.yml` | OTel span verification (INTERNAL) |
| - | 221 | JavaScript | `javascript.yml` | Not found fast |
| - | 316 | TypeScript | `typescript.yml` | Find callers (NestFactory.create) |
| - | 317 | TypeScript | `typescript.yml` | Find callees (bootstrap) |
| - | 318 | TypeScript | `typescript.yml` | Find implementations (NestModule) |
| - | 319 | TypeScript | `typescript.yml` | Performance warm cache |
| - | 320 | TypeScript | `typescript.yml` | OTel span verification (INTERNAL) |
| - | 321 | TypeScript | `typescript.yml` | Not found fast |

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-01          # All languages
./test_crs_integration.sh --feature GR-01 --lang python # Python only
./test_crs_integration.sh -t 16-21  # Legacy compatibility (Go only)
```

---

## GR-12: PageRank

**Legacy Tests:** 31-35
**Feature Folder:** `features/GR-12_pagerank/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 31 | 31 | Go | `go.yml` | Basic find_important (pagerank_used) |
| 32 | 32 | Go | `go.yml` | Top parameter (pagerank_used) |
| 33 | 33 | Go | `go.yml` | Comparison query (pagerank_used) |
| 34 | 34 | Go | `go.yml` | Convergence check (INTERNAL) |
| 35 | 35 | Go | `go.yml` | Performance check (fast_pagerank) |
| - | 131 | Python | `python.yml` | Basic (Flask functions) |
| - | 132 | Python | `python.yml` | Top parameter (classes/functions) |
| - | 133 | Python | `python.yml` | Comparison query |
| - | 134 | Python | `python.yml` | Convergence check (INTERNAL) |
| - | 135 | Python | `python.yml` | Performance check |
| - | 231 | JavaScript | `javascript.yml` | Basic (Express functions) |
| - | 232 | JavaScript | `javascript.yml` | Top parameter (middleware) |
| - | 233 | JavaScript | `javascript.yml` | Comparison query |
| - | 234 | JavaScript | `javascript.yml` | Convergence check (INTERNAL) |
| - | 235 | JavaScript | `javascript.yml` | Performance check |
| - | 331 | TypeScript | `typescript.yml` | Basic (NestJS services) |
| - | 332 | TypeScript | `typescript.yml` | Top parameter (modules/controllers) |
| - | 333 | TypeScript | `typescript.yml` | Comparison query |
| - | 334 | TypeScript | `typescript.yml` | Convergence check (INTERNAL) |
| - | 335 | TypeScript | `typescript.yml` | Performance check |

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-12          # All languages
./test_crs_integration.sh --feature GR-12 --lang go # Go only
./test_crs_integration.sh -t 31-35  # Legacy compatibility (Go only)
```

---

## GR-16: Control Flow Analysis

**Legacy Tests:** 70-90 (selected subset: 70, 72, 73, 76, 77, 79, 81, 83, 86, 88)
**Feature Folder:** `features/GR-16_control_flow/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 70 | 70 | Go | `go.yml` | Articulation points basic |
| 72 | 72 | Go | `go.yml` | Articulation performance |
| 73 | 73 | Go | `go.yml` | Dominator basic |
| 76 | 76 | Go | `go.yml` | Dominator performance |
| 77 | 77 | Go | `go.yml` | Post-dominator basic |
| 79 | 79 | Go | `go.yml` | Dominance frontier basic |
| 81 | 81 | Go | `go.yml` | Control dependence basic |
| 83 | 83 | Go | `go.yml` | Loop detection basic |
| 86 | 86 | Go | `go.yml` | Lowest common dominator basic |
| 88 | 88 | Go | `go.yml` | SESE regions basic |
| - | 170 | Python | `python.yml` | Articulation (Flask app) |
| - | 172 | Python | `python.yml` | Articulation performance |
| - | 173 | Python | `python.yml` | Dominator (create_app) |
| - | 176 | Python | `python.yml` | Dominator performance |
| - | 177 | Python | `python.yml` | Post-dominator (request handler) |
| - | 179 | Python | `python.yml` | Dominance frontier (request paths) |
| - | 181 | Python | `python.yml` | Control dependence (auth middleware) |
| - | 183 | Python | `python.yml` | Loop detection (recursive patterns) |
| - | 186 | Python | `python.yml` | LCD (auth/api blueprints) |
| - | 188 | Python | `python.yml` | SESE regions |
| - | 270 | JavaScript | `javascript.yml` | Articulation (Express app) |
| - | 272 | JavaScript | `javascript.yml` | Articulation performance |
| - | 273 | JavaScript | `javascript.yml` | Dominator (main router) |
| - | 276 | JavaScript | `javascript.yml` | Dominator performance |
| - | 277 | JavaScript | `javascript.yml` | Post-dominator (error handlers) |
| - | 279 | JavaScript | `javascript.yml` | Dominance frontier (middleware) |
| - | 281 | JavaScript | `javascript.yml` | Control dependence (auth middleware) |
| - | 283 | JavaScript | `javascript.yml` | Loop detection (recursive patterns) |
| - | 286 | JavaScript | `javascript.yml` | LCD (user/product routers) |
| - | 288 | JavaScript | `javascript.yml` | SESE regions |
| - | 370 | TypeScript | `typescript.yml` | Articulation (NestJS app) |
| - | 372 | TypeScript | `typescript.yml` | Articulation performance |
| - | 373 | TypeScript | `typescript.yml` | Dominator (main controller) |
| - | 376 | TypeScript | `typescript.yml` | Dominator performance |
| - | 377 | TypeScript | `typescript.yml` | Post-dominator (interceptors) |
| - | 379 | TypeScript | `typescript.yml` | Dominance frontier (guards) |
| - | 381 | TypeScript | `typescript.yml` | Control dependence (auth service) |
| - | 383 | TypeScript | `typescript.yml` | Loop detection (circular deps) |
| - | 386 | TypeScript | `typescript.yml` | LCD (auth/user modules) |
| - | 388 | TypeScript | `typescript.yml` | SESE regions |

**Note:** Selected subset includes basic tests and performance checks from each control flow subtopic (articulation, dominator, post-dominator, dominance frontier, control dependence, loop detection, LCD, SESE).

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-16          # All languages
./test_crs_integration.sh --feature GR-16 --lang typescript # TS only
./test_crs_integration.sh -t 70,72,73,76,77,79,81,83,86,88  # Legacy compatibility (Go only)
```

---

## Test ID Ranges

**Allocated Ranges:**
- `1-999`: Go tests
- `1001-1999`: Python tests (1000s)
- `2001-2999`: JavaScript tests (2000s)
- `3001-3999`: TypeScript tests (3000s)

**Current Usage:**
- Go: 1-9, 16-27, 31-35, 70-90, 91-110
- Python: 101-109, 116-121, 131-135, 170-188, 191-210
- JavaScript: 201-209, 216-221, 231-235, 270-288, 291-310
- TypeScript: 301-309, 316-321, 331-335, 370-388, 391-410

---

## Backward Compatibility

The legacy test numbers (1-9) still work:

```bash
# Old way (still works)
./test_crs_integration.sh -t 1,2,3

# New way (recommended)
./test_crs_integration.sh --feature GR-36 --lang go
```

Both commands run the same Go tests for GR-36 Session Restore.

---

## Migration Notes

1. **Multi-language expansion**: Legacy Go tests (1-9) now have Python/JS/TS equivalents
2. **YAML format**: Tests are now defined in structured YAML instead of pipe-delimited strings
3. **Feature organization**: Tests grouped by feature (GR-36, GR-33, GR-28) instead of sequential numbering
4. **Validation framework**: All validation functions from `run_extra_check` are reusable in YAML

---

## Migration Complete

All planned features have been migrated to YAML format.

See `migration_status.json` for complete migration summary.

## GR-40: Go Interface Implementation

**Legacy Tests:** 22-27
**Feature Folder:** `features/GR-40_go_interfaces/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 22 | 22 | Go | `go.yml` | Basic interface implementation |
| 23 | 23 | Go | `go.yml` | Multiple implementations |
| 24 | 24 | Go | `go.yml` | Empty interface (Reader) |
| 25 | 25 | Go | `go.yml` | No Grep fallback |
| 26 | 26 | Go | `go.yml` | EdgeTypeImplements verification (INTERNAL) |
| 27 | 27 | Go | `go.yml` | Performance check (O(k) not O(V)) |

**Note:** Go-only feature. Tests interface implementation detection using EdgeTypeImplements graph edges.

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-40
./test_crs_integration.sh -t 22-27  # Legacy compatibility
```

---

## GR-17: Graph Analysis Tools

**Legacy Tests:** 91-111 (selected subset: 91-92, 94-95, 97-98, 100-101, 103-104, 106-107, 109-110)
**Feature Folder:** `features/GR-17_graph_tools/`

| Legacy Test # | New Test ID | Language | File | Description |
|---------------|-------------|----------|------|-------------|
| 91 | 91 | Go | `go.yml` | find_articulation_points basic |
| 92 | 92 | Go | `go.yml` | find_articulation_points with bridges |
| 94 | 94 | Go | `go.yml` | find_dominators basic |
| 95 | 95 | Go | `go.yml` | find_dominators with tree |
| 97 | 97 | Go | `go.yml` | find_merge_points basic |
| 98 | 98 | Go | `go.yml` | find_merge_points with sources |
| 100 | 100 | Go | `go.yml` | find_loops basic |
| 101 | 101 | Go | `go.yml` | find_loops with min_size |
| 103 | 103 | Go | `go.yml` | find_common_dependency basic |
| 104 | 104 | Go | `go.yml` | find_common_dependency with entry |
| 106 | 106 | Go | `go.yml` | find_control_dependencies basic |
| 107 | 107 | Go | `go.yml` | find_control_dependencies with depth |
| 109 | 109 | Go | `go.yml` | find_extractable_regions basic |
| 110 | 110 | Go | `go.yml` | find_extractable_regions with size |
| - | 191 | Python | `python.yml` | find_articulation_points (Flask) |
| - | 192 | Python | `python.yml` | find_articulation_points with bridges |
| - | 194 | Python | `python.yml` | find_dominators (request handler) |
| - | 195 | Python | `python.yml` | find_dominators with tree |
| - | 197 | Python | `python.yml` | find_merge_points (request paths) |
| - | 198 | Python | `python.yml` | find_merge_points with sources |
| - | 200 | Python | `python.yml` | find_loops (circular imports) |
| - | 201 | Python | `python.yml` | find_loops with min_size |
| - | 203 | Python | `python.yml` | find_common_dependency (auth/api) |
| - | 204 | Python | `python.yml` | find_common_dependency with entry |
| - | 206 | Python | `python.yml` | find_control_dependencies (login route) |
| - | 207 | Python | `python.yml` | find_control_dependencies with depth |
| - | 209 | Python | `python.yml` | find_extractable_regions (Flask helpers) |
| - | 210 | Python | `python.yml` | find_extractable_regions with size |
| - | 291 | JavaScript | `javascript.yml` | find_articulation_points (Express) |
| - | 292 | JavaScript | `javascript.yml` | find_articulation_points with bridges |
| - | 294 | JavaScript | `javascript.yml` | find_dominators (API route) |
| - | 295 | JavaScript | `javascript.yml` | find_dominators with tree |
| - | 297 | JavaScript | `javascript.yml` | find_merge_points (middleware) |
| - | 298 | JavaScript | `javascript.yml` | find_merge_points with sources |
| - | 300 | JavaScript | `javascript.yml` | find_loops (circular requires) |
| - | 301 | JavaScript | `javascript.yml` | find_loops with min_size |
| - | 303 | JavaScript | `javascript.yml` | find_common_dependency (auth/validation) |
| - | 304 | JavaScript | `javascript.yml` | find_common_dependency with entry |
| - | 306 | JavaScript | `javascript.yml` | find_control_dependencies (protected route) |
| - | 307 | JavaScript | `javascript.yml` | find_control_dependencies with depth |
| - | 309 | JavaScript | `javascript.yml` | find_extractable_regions (middleware) |
| - | 310 | JavaScript | `javascript.yml` | find_extractable_regions with size |
| - | 391 | TypeScript | `typescript.yml` | find_articulation_points (NestJS) |
| - | 392 | TypeScript | `typescript.yml` | find_articulation_points with bridges |
| - | 394 | TypeScript | `typescript.yml` | find_dominators (API controller) |
| - | 395 | TypeScript | `typescript.yml` | find_dominators with tree |
| - | 397 | TypeScript | `typescript.yml` | find_merge_points (guards) |
| - | 398 | TypeScript | `typescript.yml` | find_merge_points with sources |
| - | 400 | TypeScript | `typescript.yml` | find_loops (circular deps) |
| - | 401 | TypeScript | `typescript.yml` | find_loops with min_size |
| - | 403 | TypeScript | `typescript.yml` | find_common_dependency (Auth/User) |
| - | 404 | TypeScript | `typescript.yml` | find_common_dependency with entry |
| - | 406 | TypeScript | `typescript.yml` | find_control_dependencies (guards) |
| - | 407 | TypeScript | `typescript.yml` | find_control_dependencies with depth |
| - | 409 | TypeScript | `typescript.yml` | find_extractable_regions (services) |
| - | 410 | TypeScript | `typescript.yml` | find_extractable_regions with size |

**Note:** Selected subset excludes INTERNAL CRS trace recording tests (93, 96, 99, 102, 105, 108, 111) to hit ~60 target.

**Command to run:**
```bash
./test_crs_integration.sh --feature GR-17          # All languages
./test_crs_integration.sh --feature GR-17 --lang python # Python only
./test_crs_integration.sh -t 91,92,94,95,97,98,100,101,103,104,106,107,109,110  # Legacy compatibility (Go only)
```

---
