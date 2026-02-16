// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package context provides context management for the agent loop.
//
// The context manager wraps the existing context.Assembler and provides
// additional functionality for context updates, eviction, and summarization
// during agent execution.
//
// Thread Safety:
//
//	All types in this package are designed for concurrent use.
package context

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	cbcontext "github.com/AleutianAI/AleutianFOSS/services/trace/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/google/uuid"
)

// Default configuration.
const (
	// DefaultInitialBudget is the default token budget for initial context.
	DefaultInitialBudget = 8000

	// DefaultMaxContextSize is the maximum context size before eviction.
	DefaultMaxContextSize = 100000

	// DefaultEvictionTarget is the target size after eviction.
	DefaultEvictionTarget = 80000

	// CharsPerToken approximates characters per token.
	CharsPerToken = 4.0

	// DefaultMaxToolResultLength is the maximum length for tool result output.
	// Results longer than this are summarized/truncated.
	// Fixed in cb_30a after trace_logs_18 showed 16000+ char results causing context overflow.
	DefaultMaxToolResultLength = 4000

	// DefaultMaxToolResults is the maximum number of tool results to keep in context.
	// Older results are summarized when this limit is exceeded.
	// Fixed in cb_30a after trace_logs_18 showed 23 messages overwhelming the model.
	DefaultMaxToolResults = 10
)

// ManagerConfig configures the context manager.
type ManagerConfig struct {
	// InitialBudget is the token budget for initial assembly.
	InitialBudget int

	// MaxContextSize triggers eviction when exceeded.
	MaxContextSize int

	// EvictionTarget is the target size after eviction.
	EvictionTarget int

	// EvictionPolicy determines how to evict entries.
	// Options: "lru", "relevance", "hybrid"
	EvictionPolicy string

	// SystemPrompt is the base system prompt.
	SystemPrompt string

	// MaxToolResultLength is the maximum length for individual tool result output.
	// Results longer than this are truncated with an ellipsis indicator.
	// Fixed in cb_30a after trace_logs_18 showed 16000+ char results.
	MaxToolResultLength int

	// MaxToolResults is the maximum number of tool results to keep in context.
	// Older results are summarized/pruned when this limit is exceeded.
	// Fixed in cb_30a after trace_logs_18 showed 23 messages overwhelming the model.
	MaxToolResults int
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		InitialBudget:       DefaultInitialBudget,
		MaxContextSize:      DefaultMaxContextSize,
		EvictionTarget:      DefaultEvictionTarget,
		EvictionPolicy:      "hybrid",
		MaxToolResultLength: DefaultMaxToolResultLength,
		MaxToolResults:      DefaultMaxToolResults,
		SystemPrompt: `## MANDATORY: TOOL-FIRST RESPONSE

**Your response to any analytical question MUST start with a tool call.**

DO NOT:
- Say "I'm ready to help you analyze..." - USE TOOLS INSTEAD
- Say "What would you like me to investigate?" - DECIDE AND ACT
- Say "I'll analyze this codebase..." without immediately calling a tool
- Say "Hello! I'm here to help..." - JUST CALL THE TOOL
- Offer a menu of options - PICK THE RIGHT TOOL AND CALL IT
- Ask clarifying questions before trying tools first
- Describe what you could do - DO IT

The user asked a question. ANSWER IT by calling tools, not by offering to help.

## QUESTION → TOOL MAPPING

When you see these questions, call these tools FIRST:

| Question Pattern | Tool to Call |
|------------------|--------------|
| "What tests exist?" | find_entry_points(type="test") |
| "Entry points?" / "main functions?" | find_entry_points(type="main") |
| "How does X work?" / "Trace the flow" | trace_data_flow |
| "Project structure?" / "What packages?" | find_entry_points(type="main") |
| "Configuration?" / "How is X configured?" | find_config_usage |
| "Error handling?" | trace_error_flow |
| "Similar code?" / "Duplicates?" | find_similar_code |
| "Summarize file X" / "What's in X?" | summarize_file |
| "Security concerns?" | find_entry_points → trace_data_flow |
| "Logging patterns?" | find_config_usage(config_key="log") |

## STOPPING CRITERIA (When You Have Enough Information)

**Recognize when you have sufficient information to answer, even if the result is negative.**

### Complete Answers Include:

1. **Positive Results** - Tool found what was requested
   - Example: find_dominators returns dominator tree → Answer ready ✓

2. **Negative Results** - Tool definitively shows something doesn't exist
   - Example: find_dominators says "not reachable from entry point" → Answer ready ✓
   - Example: find_callers returns empty list → Answer ready ✓
   - **DO NOT** keep calling more tools hoping for a different result

3. **Partial Results** - Tool provides related information
   - Example: Can't find dominators, but find_callers shows the call chain → Answer ready ✓

### Domain-Specific Stopping Rules:

**For Dominator/Reachability Queries:**
- If find_dominators says "not reachable from entry point" → **STOP, synthesize answer**
- This IS a complete answer - it means the function is dead code or not in the main execution path
- DO NOT call find_callers or find_entry_points to "verify" - trust the graph analysis

**For Call Chain Queries:**
- If find_callers/get_call_chain returns a chain → **STOP, synthesize answer**
- If it returns empty → **STOP, synthesize answer** (no callers IS the answer)

**For Entry Point Queries:**
- If find_entry_points returns entries → **STOP, synthesize answer**
- If it returns empty → **STOP, synthesize answer** (no standard entry points IS the answer)

### Anti-Pattern (DO NOT DO THIS):

  Tool 1: find_dominators returns "not reachable from entry point"
  Tool 2: find_entry_points to verify entry points exist [UNNECESSARY]
  Tool 3: find_callers to check if there are callers [UNNECESSARY]
  Tool 4: Read to look at the source code [UNNECESSARY]

**Instead:** After Tool 1, synthesize the answer immediately.

## GROUNDING RULES (Prevents Hallucination)

### Evidence Requirements

1. **NEVER use hedging language for code facts:**
   - BANNED: "likely", "probably", "might", "may", "could", "appears to", "seems to"
   - If uncertain → call a tool to verify
   - If not found → say "I don't see X in the context"

2. **Every factual claim MUST have a [file.go:line] citation:**
   - BAD:  "The system uses flags for configuration"
   - GOOD: "Flags defined in [cmd/main.go:23-38]: -project, -api-key, -verbose"

3. **Quote actual code when explaining behavior:**
   - BAD:  "The function calculates complexity"
   - GOOD: "CalculateChangeComplexity [complexity.go:45] uses: score = lines * weight"

### Response Format by Question Type

| Question Type | Format | Required Elements |
|---------------|--------|-------------------|
| "What exists?" / "What packages?" | TABLE | Name, Path, File count, Responsibility |
| "Configuration?" / "Options?" | TABLE + CODE | Flag/env name, type, default, code snippet |
| "How does X work?" | FLOW + CITATIONS | Step-by-step with [file:line] at each step |
| "Where is X?" | LIST | File paths with line numbers |

### Prohibited Patterns

- "The system likely..." → FIND THE CODE
- "It appears to..." → CITE THE EVIDENCE
- "Based on the function names..." → READ THE ACTUAL IMPLEMENTATION
- Describing flow without any [file:line] citations

### Examples

**BAD Response (hedging, no citations):**
> The system likely uses flags for configuration. It appears to load settings from environment variables. Based on the function names, main probably calls init first.

**GOOD Response (evidence-based, cited):**
> ## Configuration
>
> ### CLI Flags [cmd/main.go:23-38]
> | Flag | Type | Default |
> |------|------|---------|
> | -project | string | "." |
> | -verbose | bool | false |
>
> ### Loading: flag.Parse() called at [main.go:45]

## RESPONSE PATTERN

1. CALL a tool first (your response starts with a tool call, not text)
2. Report findings with specific [file:line] citations
3. Explain based on actual code from tool results
4. Use tables for enumeration questions (packages, config, files)

Do NOT write explanatory text before calling tools. Call the tool FIRST.`,
	}
}

// Manager handles context assembly and management for the agent.
//
// It wraps the existing cbcontext.Assembler and provides additional
// functionality for maintaining context during multi-step agent execution.
//
// Thread Safety:
//
//	Manager is safe for concurrent use.
type Manager struct {
	mu sync.RWMutex

	assembler *cbcontext.Assembler
	graph     *graph.Graph
	index     *index.SymbolIndex
	config    ManagerConfig

	// relevance tracks the relevance score for each entry ID.
	relevance map[string]float64

	// addedAt tracks when each entry was added (step number).
	addedAt map[string]int

	// currentStep is the current step number.
	currentStep int
}

// NewManager creates a new context manager.
//
// Description:
//
//	Creates a manager that wraps the existing Assembler and provides
//	additional context management capabilities.
//
// Inputs:
//
//	g - The code graph. Must be frozen. Must not be nil.
//	idx - The symbol index. Must not be nil.
//	config - Manager configuration (nil uses defaults).
//
// Outputs:
//
//	*Manager - The configured manager, or nil if validation fails.
//	error - Non-nil if g or idx is nil.
//
// Example:
//
//	manager, err := NewManager(graph, index, nil)
//	if err != nil {
//	    return fmt.Errorf("create context manager: %w", err)
//	}
func NewManager(g *graph.Graph, idx *index.SymbolIndex, config *ManagerConfig) (*Manager, error) {
	if g == nil {
		return nil, fmt.Errorf("graph must not be nil")
	}
	if idx == nil {
		return nil, fmt.Errorf("symbol index must not be nil")
	}

	cfg := DefaultManagerConfig()
	if config != nil {
		cfg = *config
	}

	return &Manager{
		assembler: cbcontext.NewAssembler(g, idx),
		graph:     g,
		index:     idx,
		config:    cfg,
		relevance: make(map[string]float64),
		addedAt:   make(map[string]int),
	}, nil
}

// Assemble builds initial context for a query.
//
// Description:
//
//	Uses the underlying Assembler to build initial context, then
//	wraps it in an AssembledContext suitable for the agent loop.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	query - The user's query.
//	budget - Maximum token budget.
//
// Outputs:
//
//	*agent.AssembledContext - The assembled context.
//	error - Non-nil if assembly fails.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) Assemble(ctx context.Context, query string, budget int) (*agent.AssembledContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if budget <= 0 {
		budget = m.config.InitialBudget
	}

	result, err := m.assembler.Assemble(ctx, query, budget)
	if err != nil {
		return nil, fmt.Errorf("assemble context: %w", err)
	}

	// Convert to agent.AssembledContext
	assembled := &agent.AssembledContext{
		SystemPrompt:        m.config.SystemPrompt,
		CodeContext:         make([]agent.CodeEntry, 0),
		LibraryDocs:         make([]agent.DocEntry, 0),
		ToolResults:         make([]agent.ToolResult, 0),
		ConversationHistory: make([]agent.Message, 0),
		TotalTokens:         result.TokensUsed,
		Relevance:           make(map[string]float64),
	}

	// Parse the assembled context into structured entries
	// The Assembler returns markdown-formatted context
	entries := m.parseContextEntries(result.Context, result.SymbolsIncluded)
	for _, entry := range entries {
		assembled.CodeContext = append(assembled.CodeContext, entry)
		assembled.Relevance[entry.ID] = entry.Relevance
		m.relevance[entry.ID] = entry.Relevance
		m.addedAt[entry.ID] = m.currentStep
	}

	// Detect and inject explicit project language into system prompt
	// This helps prevent hallucination by making the language constraint explicit upfront
	if lang := m.detectProjectLanguage(assembled.CodeContext); lang != "" {
		assembled.SystemPrompt = m.injectProjectLanguage(assembled.SystemPrompt, lang)
	}

	// Add initial user message
	assembled.ConversationHistory = append(assembled.ConversationHistory, agent.Message{
		Role:    "user",
		Content: query,
	})

	return assembled, nil
}

// parseContextEntries parses the assembled context into structured entries.
//
// Description:
//
//	Converts the raw context string and symbol IDs from the Assembler
//	into structured CodeEntry objects suitable for the agent loop.
//	Each entry is assigned a relevance score based on its position
//	in the symbol list (assuming ordered by relevance from Assembler).
//
// Inputs:
//
//	contextStr - The raw context string from Assembler (used for token estimation).
//	symbolIDs - List of symbol IDs included in the context, ordered by relevance.
//
// Outputs:
//
//	[]agent.CodeEntry - Structured entries with relevance scores and metadata.
//
// Limitations:
//
//	Currently uses simple position-based relevance scoring (1.0 - 0.05*index).
//	Does not parse contextStr for detailed content extraction.
func (m *Manager) parseContextEntries(contextStr string, symbolIDs []string) []agent.CodeEntry {
	entries := make([]agent.CodeEntry, 0, len(symbolIDs))

	// Simple parsing - in practice, the Assembler could return structured data
	for i, symbolID := range symbolIDs {
		entry := agent.CodeEntry{
			ID:        symbolID,
			Relevance: 1.0 - float64(i)*0.05, // Assume ordered by relevance
			AddedAt:   m.currentStep,
			Reason:    "initial context",
		}

		// Try to get symbol details from index
		if sym, ok := m.index.GetByID(symbolID); ok {
			entry.FilePath = sym.FilePath
			entry.SymbolName = sym.Name
			// Use full source code from assembler instead of just signature
			entry.Content = m.assembler.GetSymbolSourceCode(sym)
			entry.Tokens = estimateTokens(entry.Content)
		}

		entries = append(entries, entry)
	}

	return entries
}

// detectProjectLanguage determines the dominant programming language from code entries.
//
// Description:
//
//	Analyzes file extensions in the code context to identify the primary
//	programming language. This is used to inject explicit language constraints
//	into the system prompt to prevent hallucination.
//
// Inputs:
//
//	entries - Code entries from the assembled context.
//
// Outputs:
//
//	string - The dominant language (e.g., "Go", "Python"), or empty if unknown.
func (m *Manager) detectProjectLanguage(entries []agent.CodeEntry) string {
	langCounts := make(map[string]int)

	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.FilePath))
		var lang string
		switch ext {
		case ".go":
			lang = "Go"
		case ".py":
			lang = "Python"
		case ".js":
			lang = "JavaScript"
		case ".ts":
			lang = "TypeScript"
		case ".jsx":
			lang = "JavaScript"
		case ".tsx":
			lang = "TypeScript"
		case ".java":
			lang = "Java"
		case ".rs":
			lang = "Rust"
		case ".c", ".h":
			lang = "C"
		case ".cpp", ".hpp", ".cc":
			lang = "C++"
		}
		if lang != "" {
			langCounts[lang]++
		}
	}

	// Find dominant language
	maxCount := 0
	dominantLang := ""
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			dominantLang = lang
		}
	}

	return dominantLang
}

// injectProjectLanguage adds explicit language context to the system prompt.
//
// Description:
//
//	Inserts a clear statement about the project's primary programming language
//	at the start of the system prompt. This helps prevent the LLM from
//	hallucinating code in the wrong language (e.g., describing Python/Flask
//	patterns when analyzing a Go project).
//
// Inputs:
//
//	systemPrompt - The base system prompt.
//	lang - The detected project language (e.g., "Go").
//
// Outputs:
//
//	string - The system prompt with language context injected.
func (m *Manager) injectProjectLanguage(systemPrompt, lang string) string {
	langNotice := fmt.Sprintf(`## PROJECT LANGUAGE

You are analyzing a **%s** project. Do NOT reference code patterns from other languages
unless those files are explicitly shown in your context.

`, lang)

	// Insert AFTER MANDATORY section, BEFORE QUESTION → TOOL MAPPING (or AVAILABLE TOOLS if present)
	// This keeps the critical instruction first
	insertMarker := "## QUESTION → TOOL MAPPING"

	// Check if AVAILABLE TOOLS section exists (it would be inserted before QUESTION)
	if strings.Contains(systemPrompt, "## AVAILABLE TOOLS") {
		insertMarker = "## AVAILABLE TOOLS"
	}

	idx := strings.Index(systemPrompt, insertMarker)
	if idx > 0 {
		return systemPrompt[:idx] + langNotice + systemPrompt[idx:]
	}

	// Fallback: insert after first section (after the DO NOT list ends)
	doNotEnd := strings.Index(systemPrompt, "The user asked a question")
	if doNotEnd > 0 {
		// Find end of that line
		lineEnd := strings.Index(systemPrompt[doNotEnd:], "\n")
		if lineEnd > 0 {
			insertPos := doNotEnd + lineEnd + 1
			return systemPrompt[:insertPos] + "\n" + langNotice + systemPrompt[insertPos:]
		}
	}

	return systemPrompt + langNotice
}

// injectToolList adds available tools to the system prompt.
//
// Description:
//
//	Inserts a dynamically generated list of available tools into the
//	system prompt. The list is formatted as a markdown table showing
//	tool names and descriptions.
//
// Inputs:
//
//	systemPrompt - The base system prompt.
//	toolDefs - Available tool definitions.
//
// Outputs:
//
//	string - The system prompt with tool list injected.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) injectToolList(systemPrompt string, toolDefs []tools.ToolDefinition) string {
	if len(toolDefs) == 0 {
		return systemPrompt
	}

	var sb strings.Builder
	sb.WriteString("## AVAILABLE TOOLS\n\n")
	sb.WriteString("| Tool | Purpose |\n")
	sb.WriteString("|------|--------|\n")

	for _, tool := range toolDefs {
		// Truncate description to keep prompt concise
		desc := tool.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", tool.Name, desc))
	}
	sb.WriteString("\n")

	// Insert AFTER MANDATORY section, BEFORE QUESTION → TOOL MAPPING
	// This ensures the critical instruction comes first, then tools, then how to use them
	insertMarker := "## QUESTION → TOOL MAPPING"
	idx := strings.Index(systemPrompt, insertMarker)
	if idx > 0 {
		return systemPrompt[:idx] + sb.String() + systemPrompt[idx:]
	}

	// Fallback: append to end if marker not found
	return systemPrompt + sb.String()
}

// InjectToolsIntoPrompt adds available tools to an assembled context's system prompt.
//
// Description:
//
//	Public method to inject tool definitions into an existing assembled
//	context. This is called by the execute phase when it has access to
//	the tool registry.
//
// Inputs:
//
//	ctx - The assembled context to modify.
//	toolDefs - Available tool definitions from ToolRegistry.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) InjectToolsIntoPrompt(ctx *agent.AssembledContext, toolDefs []tools.ToolDefinition) {
	if ctx == nil || len(toolDefs) == 0 {
		return
	}
	ctx.SystemPrompt = m.injectToolList(ctx.SystemPrompt, toolDefs)
}

// Update modifies context based on a tool result.
//
// Description:
//
//	Integrates tool results into the current context, potentially
//	adding new code entries or updating relevance scores.
//	Tool results are truncated if they exceed MaxToolResultLength.
//	Older results are summarized if MaxToolResults is exceeded.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	current - Current assembled context.
//	result - Tool result to integrate.
//
// Outputs:
//
//	*agent.AssembledContext - Updated context.
//	error - Non-nil if update fails.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) Update(ctx context.Context, current *agent.AssembledContext, result *tools.Result) (*agent.AssembledContext, error) {
	if current == nil {
		return nil, fmt.Errorf("current context is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentStep++

	// Create a copy to avoid mutation
	updated := m.copyContext(current)

	// Truncate tool result output if it exceeds the limit (cb_30a fix).
	// trace_logs_18 showed 16000+ char results causing context overflow and empty LLM responses.
	outputText := result.OutputText
	truncated := result.Truncated
	if m.config.MaxToolResultLength > 0 && len(outputText) > m.config.MaxToolResultLength {
		outputText = m.truncateToolResult(outputText, m.config.MaxToolResultLength)
		truncated = true
	}

	// Add tool result to history
	agentResult := agent.ToolResult{
		InvocationID: uuid.NewString(),
		Success:      result.Success,
		Output:       outputText,
		Error:        result.Error,
		Duration:     result.Duration,
		TokensUsed:   estimateTokens(outputText), // Recalculate based on truncated output
		Cached:       result.Cached,
		Truncated:    truncated,
	}
	updated.ToolResults = append(updated.ToolResults, agentResult)
	updated.TotalTokens += agentResult.TokensUsed

	// Prune old tool results if we exceed the limit (cb_30a fix).
	// trace_logs_18 showed 23 messages overwhelming the model with context.
	if m.config.MaxToolResults > 0 && len(updated.ToolResults) > m.config.MaxToolResults {
		updated = m.pruneOldToolResults(updated)
	}

	// Extract any new code entries from the result
	newEntries := m.extractEntriesFromResult(result)
	for _, entry := range newEntries {
		// Check if entry already exists
		exists := false
		for i, existing := range updated.CodeContext {
			if existing.ID == entry.ID {
				// Update relevance (boost by recency)
				updated.CodeContext[i].Relevance += 0.1
				updated.Relevance[entry.ID] = updated.CodeContext[i].Relevance
				m.relevance[entry.ID] = updated.CodeContext[i].Relevance
				exists = true
				break
			}
		}

		if !exists {
			entry.AddedAt = m.currentStep
			updated.CodeContext = append(updated.CodeContext, entry)
			updated.Relevance[entry.ID] = entry.Relevance
			m.relevance[entry.ID] = entry.Relevance
			m.addedAt[entry.ID] = m.currentStep
			updated.TotalTokens += entry.Tokens
		}
	}

	// Check if eviction is needed
	if updated.TotalTokens > m.config.MaxContextSize {
		updated = m.evict(updated, updated.TotalTokens-m.config.EvictionTarget)
	}

	return updated, nil
}

// extractEntriesFromResult extracts code entries from a tool result.
func (m *Manager) extractEntriesFromResult(result *tools.Result) []agent.CodeEntry {
	var entries []agent.CodeEntry

	// If the result contains structured output, extract entries
	if result.Output != nil {
		// This would typically parse the tool-specific output format
		// For now, we just estimate based on output text
		entry := agent.CodeEntry{
			ID:        uuid.NewString(),
			Content:   result.OutputText,
			Tokens:    result.TokensUsed,
			Relevance: 0.8,
			Reason:    "tool result",
		}
		if entry.Tokens > 0 {
			entries = append(entries, entry)
		}
	}

	return entries
}

// truncateToolResult truncates a tool result to the specified max length.
//
// Description:
//
//	Truncates the output while preserving structure. For structured output
//	(like lists or tables), it tries to truncate at natural boundaries.
//	Adds an ellipsis indicator showing how much was truncated.
//
// Inputs:
//
//	output - The tool result output text.
//	maxLen - Maximum allowed length.
//
// Outputs:
//
//	string - Truncated output with ellipsis indicator.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) truncateToolResult(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}

	// Reserve space for the truncation indicator
	truncationIndicator := fmt.Sprintf("\n\n[...truncated %d chars]", len(output)-maxLen+50)
	effectiveMax := maxLen - len(truncationIndicator)

	if effectiveMax < 100 {
		// If we can't even fit 100 chars plus indicator, just hard truncate
		return output[:maxLen-20] + "\n\n[...truncated]"
	}

	// Try to truncate at a natural boundary (newline)
	truncated := output[:effectiveMax]
	lastNewline := strings.LastIndex(truncated, "\n")
	if lastNewline > effectiveMax/2 {
		// Found a reasonable newline boundary in the second half
		truncated = truncated[:lastNewline]
	}

	return truncated + truncationIndicator
}

// pruneOldToolResults summarizes/removes old tool results to stay under the limit.
//
// Description:
//
//	When the number of tool results exceeds MaxToolResults, this method
//	summarizes older results into a single "summary" entry. The most recent
//	results are kept intact for the LLM to reference accurately.
//
// Inputs:
//
//	current - The context with tool results to prune.
//
// Outputs:
//
//	*agent.AssembledContext - Context with pruned tool results.
//
// Thread Safety: Caller must hold the Manager's write lock.
func (m *Manager) pruneOldToolResults(current *agent.AssembledContext) *agent.AssembledContext {
	if m.config.MaxToolResults <= 0 || len(current.ToolResults) <= m.config.MaxToolResults {
		return current
	}

	// Keep the most recent MaxToolResults-1 results, summarize the rest
	numToSummarize := len(current.ToolResults) - (m.config.MaxToolResults - 1)
	oldResults := current.ToolResults[:numToSummarize]
	recentResults := current.ToolResults[numToSummarize:]

	// Create a summary of old results
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString(fmt.Sprintf("[Summary of %d previous tool calls]\n", numToSummarize))

	tokensFreed := 0
	for i, result := range oldResults {
		tokensFreed += result.TokensUsed
		status := "success"
		if !result.Success {
			status = "failed"
		}
		// Include just a brief note about each call
		outputPreview := result.Output
		if len(outputPreview) > 100 {
			outputPreview = outputPreview[:100] + "..."
		}
		summaryBuilder.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, status, outputPreview))
	}

	summaryText := summaryBuilder.String()
	summaryTokens := estimateTokens(summaryText)

	// Create summary entry
	summaryResult := agent.ToolResult{
		InvocationID: uuid.NewString(),
		Success:      true,
		Output:       summaryText,
		TokensUsed:   summaryTokens,
		Truncated:    true, // Mark as truncated/summarized
	}

	// Replace old results with summary + recent results
	current.ToolResults = append([]agent.ToolResult{summaryResult}, recentResults...)
	current.TotalTokens -= tokensFreed - summaryTokens

	return current
}

// Evict removes entries to free tokens from context.
//
// Description:
//
//	Removes the least relevant/oldest entries to bring context
//	under the target size.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	current - Current assembled context.
//	tokensToFree - Number of tokens to free.
//
// Outputs:
//
//	*agent.AssembledContext - Context with entries removed.
//	error - Non-nil if eviction fails.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) Evict(ctx context.Context, current *agent.AssembledContext, tokensToFree int) (*agent.AssembledContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.evict(current, tokensToFree), nil
}

// evict performs the actual eviction. Caller must hold the lock.
func (m *Manager) evict(current *agent.AssembledContext, tokensToFree int) *agent.AssembledContext {
	if tokensToFree <= 0 {
		return current
	}

	updated := m.copyContext(current)

	switch m.config.EvictionPolicy {
	case "lru":
		updated = m.evictLRU(updated, tokensToFree)
	case "relevance":
		updated = m.evictByRelevance(updated, tokensToFree)
	default: // "hybrid"
		updated = m.evictHybrid(updated, tokensToFree)
	}

	return updated
}

// evictLRU removes the oldest entries first.
func (m *Manager) evictLRU(current *agent.AssembledContext, tokensToFree int) *agent.AssembledContext {
	// Sort by addedAt (oldest first)
	type entry struct {
		index   int
		addedAt int
		tokens  int
	}

	entries := make([]entry, len(current.CodeContext))
	for i, e := range current.CodeContext {
		addedAt := m.addedAt[e.ID]
		entries[i] = entry{index: i, addedAt: addedAt, tokens: e.Tokens}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].addedAt < entries[j].addedAt
	})

	freed := 0
	toRemove := make(map[int]bool)
	for _, e := range entries {
		if freed >= tokensToFree {
			break
		}
		toRemove[e.index] = true
		freed += e.tokens
	}

	return m.removeEntries(current, toRemove)
}

// evictByRelevance removes the lowest relevance entries first.
func (m *Manager) evictByRelevance(current *agent.AssembledContext, tokensToFree int) *agent.AssembledContext {
	// Sort by relevance (lowest first)
	type entry struct {
		index     int
		relevance float64
		tokens    int
	}

	entries := make([]entry, len(current.CodeContext))
	for i, e := range current.CodeContext {
		entries[i] = entry{index: i, relevance: e.Relevance, tokens: e.Tokens}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relevance < entries[j].relevance
	})

	freed := 0
	toRemove := make(map[int]bool)
	for _, e := range entries {
		if freed >= tokensToFree {
			break
		}
		toRemove[e.index] = true
		freed += e.tokens
	}

	return m.removeEntries(current, toRemove)
}

// evictHybrid uses a combination of LRU and relevance.
func (m *Manager) evictHybrid(current *agent.AssembledContext, tokensToFree int) *agent.AssembledContext {
	// Score = relevance * recencyBoost
	// recencyBoost decays with age
	type entry struct {
		index  int
		score  float64
		tokens int
	}

	entries := make([]entry, len(current.CodeContext))
	for i, e := range current.CodeContext {
		addedAt := m.addedAt[e.ID]
		age := m.currentStep - addedAt
		recencyBoost := 1.0 / (1.0 + float64(age)*0.1)
		score := e.Relevance * recencyBoost
		entries[i] = entry{index: i, score: score, tokens: e.Tokens}
	}

	// Sort by score (lowest first for eviction)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score < entries[j].score
	})

	freed := 0
	toRemove := make(map[int]bool)
	for _, e := range entries {
		if freed >= tokensToFree {
			break
		}
		toRemove[e.index] = true
		freed += e.tokens
	}

	return m.removeEntries(current, toRemove)
}

// removeEntries removes entries at the specified indices.
//
// Description:
//
//	Removes code entries from the context at the specified indices.
//	Also cleans up the manager's internal tracking maps (relevance, addedAt)
//	and updates the context's relevance map and total token count.
//
// Inputs:
//
//	current - The context to modify. MUTATED IN PLACE.
//	toRemove - Map of indices to remove (true = remove).
//
// Outputs:
//
//	*agent.AssembledContext - The same pointer as input (modified).
//
// Limitations:
//
//	MUTATES the input context. The caller should be aware that the
//	original context is modified rather than a new copy being returned.
//	For immutable behavior, use copyContext before calling this method.
//
// Thread Safety:
//
//	Caller must hold the Manager's write lock.
func (m *Manager) removeEntries(current *agent.AssembledContext, toRemove map[int]bool) *agent.AssembledContext {
	newContext := make([]agent.CodeEntry, 0, len(current.CodeContext)-len(toRemove))
	tokenReduction := 0

	for i, entry := range current.CodeContext {
		if toRemove[i] {
			tokenReduction += entry.Tokens
			delete(m.relevance, entry.ID)
			delete(m.addedAt, entry.ID)
			delete(current.Relevance, entry.ID)
		} else {
			newContext = append(newContext, entry)
		}
	}

	current.CodeContext = newContext
	current.TotalTokens -= tokenReduction

	return current
}

// copyContext creates a deep copy of the context.
func (m *Manager) copyContext(ctx *agent.AssembledContext) *agent.AssembledContext {
	if ctx == nil {
		return nil
	}

	copied := &agent.AssembledContext{
		SystemPrompt:        ctx.SystemPrompt,
		CodeContext:         make([]agent.CodeEntry, len(ctx.CodeContext)),
		LibraryDocs:         make([]agent.DocEntry, len(ctx.LibraryDocs)),
		ToolResults:         make([]agent.ToolResult, len(ctx.ToolResults)),
		ConversationHistory: make([]agent.Message, len(ctx.ConversationHistory)),
		TotalTokens:         ctx.TotalTokens,
		Relevance:           make(map[string]float64),
	}

	copy(copied.CodeContext, ctx.CodeContext)
	copy(copied.LibraryDocs, ctx.LibraryDocs)
	copy(copied.ToolResults, ctx.ToolResults)
	copy(copied.ConversationHistory, ctx.ConversationHistory)

	for k, v := range ctx.Relevance {
		copied.Relevance[k] = v
	}

	return copied
}

// AddMessage adds a message to the conversation history.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) AddMessage(ctx *agent.AssembledContext, role, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx.ConversationHistory = append(ctx.ConversationHistory, agent.Message{
		Role:    role,
		Content: content,
	})
	ctx.TotalTokens += estimateTokens(content)
}

// GetTokenCount returns the current token count.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) GetTokenCount(ctx *agent.AssembledContext) int {
	if ctx == nil {
		return 0
	}
	return ctx.TotalTokens
}

// FormatForLLM formats the context for LLM consumption.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) FormatForLLM(ctx *agent.AssembledContext) string {
	if ctx == nil {
		return ""
	}

	var builder strings.Builder

	// System prompt
	if ctx.SystemPrompt != "" {
		builder.WriteString("## System\n\n")
		builder.WriteString(ctx.SystemPrompt)
		builder.WriteString("\n\n")
	}

	// Code context
	if len(ctx.CodeContext) > 0 {
		builder.WriteString("## Code Context\n\n")
		for _, entry := range ctx.CodeContext {
			if entry.FilePath != "" {
				builder.WriteString(fmt.Sprintf("### %s", entry.FilePath))
				if entry.SymbolName != "" {
					builder.WriteString(fmt.Sprintf(" - %s", entry.SymbolName))
				}
				builder.WriteString("\n")
			}
			if entry.Content != "" {
				builder.WriteString("```\n")
				builder.WriteString(entry.Content)
				builder.WriteString("\n```\n\n")
			}
		}
	}

	// Conversation history
	if len(ctx.ConversationHistory) > 0 {
		builder.WriteString("## Conversation\n\n")
		for _, msg := range ctx.ConversationHistory {
			builder.WriteString(fmt.Sprintf("**%s**: %s\n\n", msg.Role, msg.Content))
		}
	}

	return builder.String()
}

// estimateTokens estimates token count from string length.
func estimateTokens(s string) int {
	return int(float64(len(s)) / CharsPerToken)
}

// Reset clears the manager state for a new session.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.relevance = make(map[string]float64)
	m.addedAt = make(map[string]int)
	m.currentStep = 0
}
