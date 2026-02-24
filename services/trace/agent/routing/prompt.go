// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
)

// =============================================================================
// Prompt Builder
// =============================================================================

// PromptBuilder constructs dynamic system prompts for tool routing.
//
// # Description
//
// Builds prompts that include available tools, their descriptions, and
// context about the codebase. The prompt instructs the router to output
// a JSON object with the selected tool and confidence score.
//
// # Thread Safety
//
// PromptBuilder is safe for concurrent use.
type PromptBuilder struct {
	tmpl *template.Template
}

// PromptData contains the data for prompt template rendering.
type PromptData struct {
	// Tools is the list of available tools.
	Tools []ToolSpec

	// Context contains optional codebase context.
	Context *CodeContext

	// Query is the user's question/request.
	Query string
}

// systemPromptTemplate is the template for the routing system prompt.
//
// History-Aware Routing: This prompt is designed to leverage Mamba2's
// O(n) linear complexity and 1M token context window. By including full
// tool history with summaries, the router can make informed decisions
// about what information is still missing.
const systemPromptTemplate = `You are a tool router for a code assistant. Your job is to select the SINGLE BEST NEXT tool for the user's query.

## Available Tools
{{range .Tools}}
### {{.Name}}
{{.Description}}
{{- if .BestFor}}
Keywords: {{join .BestFor ", "}}
{{- end}}
{{- if .UseWhen}}
Use when: {{.UseWhen}}
{{- end}}
{{- if .AvoidWhen}}
Avoid when: {{.AvoidWhen}}
{{- end}}
{{- if .InsteadOf}}
Prefer over:{{range .InsteadOf}} {{.Tool}} ({{.When}}){{end}}
{{- end}}
{{- if .Params}}
Parameters: {{join .Params ", "}}
{{- end}}
{{- if .Category}}
Category: {{.Category}}
{{- end}}

{{end}}

## Common Patterns - Tool Disambiguation

**CB-31d Item 6: Critical disambiguation examples**

Understanding the difference between CALLERS, CALLEES, and REFERENCES is crucial:

**REFERENCES (where X is used, any usage):**
- "Where is X used?"
- "Where is the Entry type used across the codebase?"
- "Find references to Config"
- "Show usages of HandlerFunc"
- "What code references the Request object?"
→ Tool: find_references

**CALLERS (who calls X, upstream CALL relationships only):**
- "What calls X?"
- "Who invokes X?"
- "What functions call X?"
→ Tool: find_callers

**CALLEES (what X calls, downstream):**
- "What functions does X call?"
- "What does X invoke?"
- "Show me X's dependencies"
→ Tool: find_callees

**CRITICAL: "Where is X used?" → find_references (NOT find_callers)**
- "Where is X used?" asks about ALL usages of a symbol, not just call sites
- "Who calls X?" asks specifically about CALL relationships
- These are DIFFERENT tools for DIFFERENT questions

**Examples:**
- Query: "Where is the Entry type used across the codebase?" → find_references (usage query)
- Query: "Where is the HandlerFunc type used?" → find_references (usage query)
- Query: "What functions does main call?" → find_callees (main calls them)
- Query: "What functions call validateInput?" → find_callers (they call validateInput)
- Query: "Show dependencies of Handler" → find_callees (Handler depends on them)
- Query: "What depends on the config module?" → find_callers (they depend on config)

**Key phrase patterns:**
- "where is X used" / "references to X" / "usages of X" → find_references
- "does X call" / "X calls" → find_callees (downstream from X)
- "calls X" / "call X" / "invoke X" → find_callers (upstream to X)
- "from X" / "starting at X" → find_callees (traverse down from X)
- "to X" / "reaching X" → find_callers (traverse up to X)

**CRITICAL: NEGATED CALLER QUERIES → find_dead_code (NOT find_callers)**
- "functions with no callers" → find_dead_code (zero incoming edges = dead code)
- "no incoming calls" → find_dead_code
- "never called" / "not called" → find_dead_code
- "no internal callers" → find_dead_code
- "zero callers" / "unreferenced" → find_dead_code
The word "callers" alone triggers find_callers, but when NEGATED ("no callers", "no incoming calls") it means dead code detection.

**COMMUNITY BRIDGES vs ARTICULATION POINTS:**

COMMUNITY ANALYSIS (bridges between code communities):
- "Which functions bridge between communities?"
- "Bridges between the rendering and interaction communities"
- "Cross-community connections"
- "What connects different code communities?"
→ Tool: find_communities (includes cross-community edge analysis)

ARTICULATION POINTS (graph-theoretic single points of failure):
- "What are the single points of failure?"
- "Fragile architecture bottlenecks"
- "Cut vertices in the call graph"
→ Tool: find_articulation_points

KEY DIFFERENCE:
- If the query mentions "communities" + "bridges" → find_communities (community bridge analysis)
- If the query asks about fragility, single points of failure, cut vertices → find_articulation_points
- The word "bridges" alone is ambiguous — use "communities" as the deciding factor

**HOTSPOTS vs WEIGHTED_CRITICALITY:**

HOTSPOTS (connectivity/degree ranking):
- "Find the most connected functions"
- "What are the hotspot functions in the rendering code?"
- "Which functions have the highest fan-in or fan-out?"
- "Find hub functions with high coupling"
- "Show me the most called functions"
→ Tool: find_hotspots
→ Measures: inDegree + outDegree (how many edges does a node have?)

WEIGHTED_CRITICALITY (risk/impact ranking):
- "Find the most critical functions for stability"
- "Which functions would cause the most damage if broken?"
- "What is the risk profile of key services?"
- "Find mandatory dependencies that are architecturally important"
→ Tool: find_weighted_criticality
→ Measures: DominatorScore × PageRank (how critical is a node if changed?)

KEY DIFFERENCE:
- "most connected" / "hotspot" / "coupling" / "fan-in" / "fan-out" → find_hotspots
- "most critical" / "highest risk" / "would break" / "mandatory" → find_weighted_criticality
- A function can be highly connected but LOW risk (peripheral hub)
- A function can be low-degree but CRITICAL (hidden gatekeeper)

**IMPLEMENTATIONS vs CALLERS/CALLEES:**

IMPLEMENTATIONS (class hierarchy, inheritance, interface satisfaction):
- "What classes extend X?"
- "What types implement X?"
- "What subclasses does X have?"
- "What derives from X?"
→ Tool: find_implementations

CALLERS (who calls a specific function):
- "What calls X?"
- "Who invokes X?"
→ Tool: find_callers

CALLEES (what a specific function calls):
- "What does X call?"
- "What functions does X invoke?"
→ Tool: find_callees

KEY DIFFERENCE:
- "extends" / "implements" / "subclasses" / "base class" → find_implementations
- "calls X" (upstream) → find_callers
- "X calls" (downstream) → find_callees

**CYCLES vs CALLEES vs LOOPS:**

CYCLES (Tarjan SCC — circular dependencies between distinct functions):
- "Find circular call dependencies"
- "Are there dependency cycles?"
- "Find circular call chains"
- "What are the shortest circular dependencies?"
→ Tool: find_cycles

LOOPS (recursion patterns within individual functions):
- "Find recursive functions"
- "Which functions call themselves?"
- "Find self-recursive or mutually-recursive functions"
→ Tool: find_loops

CALLEES (what a function calls — NOT cycles):
- "What does X call?"
- "What functions does X depend on?"
→ Tool: find_callees

KEY DIFFERENCE:
- "circular dependencies" / "dependency cycles" / "circular call chains" → find_cycles
- "recursive functions" / "self-calls" / "mutual recursion" → find_loops
- "what does X call" / "X's dependencies" → find_callees

**DEAD CODE vs CALLEES:**

DEAD CODE (unused, deprecated, unreachable):
- "Find deprecated methods"
- "Find unreachable functions"
- "Find unused methods"
- "Find orphaned code"
→ Tool: find_dead_code

CALLEES (downstream dependencies):
- "What does X call?"
→ Tool: find_callees

**IMPORTANT vs WEIGHTED_CRITICALITY (full disambiguation):**

IMPORTANT (PageRank centrality — recursive influence):
- "Most important functions by PageRank"
- "Highest structural importance"
- "Most influential functions"
- "Lowest PageRank / peripheral functions"
→ Tool: find_important

WEIGHTED CRITICALITY (risk/crash assessment):
- "What production code would break the most if changed?"
- "Highest-risk functions for stability"
→ Tool: find_weighted_criticality

## Current Context
{{- if .Context}}
{{- if .Context.Language}}
- Language: {{.Context.Language}}
{{- end}}
{{- if gt .Context.Files 0}}
- Files indexed: {{.Context.Files}}
{{- end}}
{{- if gt .Context.Symbols 0}}
- Symbols available: {{.Context.Symbols}}
{{- end}}
{{- if .Context.CurrentFile}}
- Current file: {{.Context.CurrentFile}}
{{- end}}
{{- if gt .Context.StepNumber 0}}
- Current step: {{.Context.StepNumber}}
{{- end}}
{{- if .Context.Progress}}
- Progress so far: {{.Context.Progress}}
{{- end}}
{{- if .Context.ToolHistory}}

[!] CRITICAL CONSTRAINT: Tool History (DO NOT REPEAT) [!]
═══════════════════════════════════════════════════════════════════
The following tools have ALREADY been executed in this session.
You MUST NOT suggest these tools again:

{{range .Context.ToolHistory}}
{{if .Success}}[OK] Step {{.StepNumber}}: {{.Tool}} → SUCCESS
    Result: {{.Summary}}
{{else}}[FAIL] Step {{.StepNumber}}: {{.Tool}} → FAILED
    Error: {{.Summary}}
{{end}}
{{- end}}

FORBIDDEN TOOLS (DO NOT suggest these):
{{- range .Context.ToolHistory}}
{{- if .Success}}
  - {{.Tool}} (already succeeded)
{{- else}}
  - {{.Tool}} (already failed)
{{- end}}
{{- end}}

NEXT STEP LOGIC:
1. Review what information was successfully gathered above
2. Check if this information FULLY answers the query (including negative results)
3. If YES → select "answer" immediately
4. If NO → identify what is STILL MISSING
5. Select a DIFFERENT tool NOT in the forbidden list

**IMPORTANT: Negative results ARE complete answers!**
- "not reachable from entry point" = Complete answer ✓
- "no callers found" = Complete answer ✓
- "not found in index" = Complete answer ✓
- DO NOT call more tools hoping for a different result

Does the information above FULLY answer the query (yes/no)?
If yes, select "answer". If no, what critical information is MISSING?
{{- else if .Context.RecentTools}}
Recent tools used: {{join .Context.RecentTools ", "}}
Prefer DIFFERENT tools unless absolutely necessary.
{{- end}}
{{- if .Context.PreviousErrors}}

[!] FAILED TOOLS - AVOID THESE [!]
The following tools recently failed. DO NOT suggest them again:
{{range .Context.PreviousErrors}}
  - {{.Tool}}: {{.Error}}
{{- end}}
Choose a DIFFERENT tool that can accomplish the same goal.
{{- end}}
{{- else}}
- No additional context provided
{{- end}}

## DECISION PROTOCOL (FOLLOW STRICTLY)

STEP 1: Check Tool History
  - Have we already gathered sufficient information?
  - If YES → select "answer" (do not call more tools unnecessarily)
  - If NO → proceed to STEP 2

STEP 2: Identify Missing Information
  - What SPECIFIC information is STILL needed?
  - Be precise about what gap exists

STEP 3: Select NEXT Tool
  - Choose ONE tool NOT in the forbidden list above
  - The tool must gather NEW information we don't have yet
  - DO NOT repeat successful tools (check [OK] entries above)
  - DO NOT retry failed tools (check [FAIL] entries above)

STEP 4: Validate Your Selection
  - Is this tool in the forbidden list? → If YES, STOP and choose different tool
  - Will this tool add NEW information? → If NO, select "answer" instead
  - Have we tried this tool before? → If YES, STOP and choose different tool

CRITICAL: If unsure whether to continue or answer, choose "answer"

## Output Format
Respond with ONLY a JSON object. No explanation, no markdown, just JSON:
{"tool": "<tool_name>", "confidence": <0.0-1.0>, "reasoning": "<what we still need to know>"}

Example outputs:
{"tool": "find_symbol_usages", "confidence": 0.95, "reasoning": "Need to find callers after locating the function"}
{"tool": "read_file", "confidence": 0.8, "reasoning": "Found the file, need to see its contents"}
{"tool": "answer", "confidence": 0.9, "reasoning": "Have entry points and implementation details, ready to answer"}`

// userPromptTemplate is the template for the user message.
const userPromptTemplate = `User query: {{.Query}}

Select the best tool and respond with JSON only.`

// NewPromptBuilder creates a new PromptBuilder.
//
// # Outputs
//
//   - *PromptBuilder: Configured builder.
//   - error: Non-nil if template parsing fails.
func NewPromptBuilder() (*PromptBuilder, error) {
	slog.Debug("NewPromptBuilder: Creating prompt builder")

	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	slog.Debug("NewPromptBuilder: Parsing system prompt template",
		"template_length", len(systemPromptTemplate))

	tmpl, err := template.New("system").Funcs(funcMap).Parse(systemPromptTemplate)
	if err != nil {
		slog.Error("NewPromptBuilder: Template parsing failed",
			"error", err,
			"error_type", fmt.Sprintf("%T", err))
		return nil, err
	}

	slog.Debug("NewPromptBuilder: Prompt builder created successfully")
	return &PromptBuilder{
		tmpl: tmpl,
	}, nil
}

// BuildSystemPrompt generates the system prompt for the router.
//
// # Description
//
// Creates a system prompt that includes all available tools, their
// descriptions, and instructions for JSON output format.
//
// # Inputs
//
//   - tools: Available tools for selection.
//   - context: Optional codebase context.
//
// # Outputs
//
//   - string: The rendered system prompt.
//   - error: Non-nil if template rendering fails.
func (p *PromptBuilder) BuildSystemPrompt(tools []ToolSpec, context *CodeContext) (string, error) {
	data := PromptData{
		Tools:   tools,
		Context: context,
	}

	var buf bytes.Buffer
	if err := p.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// BuildUserPrompt generates the user message containing the query.
//
// # Inputs
//
//   - query: The user's question/request.
//
// # Outputs
//
//   - string: The rendered user prompt.
func (p *PromptBuilder) BuildUserPrompt(query string) string {
	return "User query: " + query + "\n\nSelect the best tool and respond with JSON only."
}

// =============================================================================
// Default Tool Specs
// =============================================================================

// DefaultToolSpecs returns the standard Trace tool specifications.
//
// # Description
//
// Returns ToolSpec entries for the core Trace tools. These can be
// filtered based on what's actually available in the current session.
//
// # Outputs
//
//   - []ToolSpec: Standard tool specifications.
func DefaultToolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Name:        "find_symbol",
			Description: "Find a symbol (function, type, variable) by name in the codebase.",
			BestFor:     []string{"finding where something is defined", "locating a function", "finding a type"},
			Params:      []string{"name", "kind"},
			Category:    "search",
		},
		{
			Name:        "find_symbol_usages",
			Description: "Find all places where a symbol is used/called.",
			BestFor:     []string{"finding callers", "finding references", "impact analysis"},
			Params:      []string{"symbol_id"},
			Category:    "search",
		},
		{
			Name:        "find_config_usage",
			Description: "Find configuration settings and environment variables.",
			BestFor:     []string{"finding config options", "environment variables", "settings"},
			Params:      []string{"pattern"},
			Category:    "search",
		},
		{
			Name:        "read_file",
			Description: "Read the contents of a specific file.",
			BestFor:     []string{"viewing file contents", "reading code", "examining implementation"},
			Params:      []string{"path", "start_line", "end_line"},
			Category:    "read",
		},
		{
			Name:        "read_symbol",
			Description: "Read the source code of a specific symbol.",
			BestFor:     []string{"viewing function code", "reading type definition", "examining symbol"},
			Params:      []string{"symbol_id"},
			Category:    "read",
		},
		{
			Name:        "grep_codebase",
			Description: "Search for text patterns across the codebase.",
			BestFor:     []string{"finding text", "searching strings", "regex search"},
			Params:      []string{"pattern", "file_pattern"},
			Category:    "search",
		},
		{
			Name:        "list_files",
			Description: "List files in a directory or matching a pattern.",
			BestFor:     []string{"exploring directory structure", "finding files", "listing contents"},
			Params:      []string{"path", "pattern"},
			Category:    "explore",
		},
		{
			Name:        "get_symbol_graph",
			Description: "Get the dependency graph for a symbol.",
			BestFor:     []string{"understanding dependencies", "call graph", "type hierarchy"},
			Params:      []string{"symbol_id", "depth"},
			Category:    "analyze",
		},
		{
			Name:        "answer",
			Description: "Provide a direct answer when no tool is needed.",
			BestFor:     []string{"general questions", "explanations", "no code lookup needed"},
			Params:      []string{},
			Category:    "respond",
		},
	}
}
