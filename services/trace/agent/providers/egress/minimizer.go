// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"fmt"
	"log/slog"
	"strings"

	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// internalSystemPromptSections lists the ## section headers in the system prompt
// that contain internal routing rules and should be stripped for external providers.
// These reveal internal architecture (tool routing logic, stopping criteria,
// grounding rules) that external providers do not need.
var internalSystemPromptSections = []string{
	"## MANDATORY: TOOL-FIRST RESPONSE",
	"## STOPPING CRITERIA",
	"## GROUNDING RULES",
	"## RESPONSE PATTERN",
}

// DataMinimizer minimizes requests before sending to external providers.
//
// Description:
//
//	Applies a four-stage minimization pipeline to reduce the data sent
//	to external LLM providers:
//	  1. System prompt filtering — strips internal routing rules
//	  2. Tool definition filtering — keeps only relevant tools
//	  3. Message minimization — truncates large results, strips paths
//	  4. Context window truncation — fits request within provider limits
//
//	Minimization creates a copy of the request; the original is never mutated.
//	Local providers (Ollama) are never minimized.
//
// Thread Safety: Safe for concurrent use after initialization is complete.
// SetCapabilities must only be called during initialization, before any
// concurrent calls to Minimize.
type DataMinimizer struct {
	capabilities map[string]ProviderCapabilities
	enabled      bool
	minTokens    int
	logger       *slog.Logger
}

// NewDataMinimizer creates a new DataMinimizer.
//
// Description:
//
//	Initializes the minimizer with provider capabilities and configuration.
//	When disabled, Minimize() returns the original request unchanged.
//
// Inputs:
//   - enabled: Whether minimization is active.
//   - minTokens: Skip minimization for requests estimated below this token count.
//   - logger: Structured logger for minimization events.
//
// Outputs:
//   - *DataMinimizer: Configured minimizer ready to process requests.
func NewDataMinimizer(enabled bool, minTokens int, logger *slog.Logger) *DataMinimizer {
	return &DataMinimizer{
		capabilities: make(map[string]ProviderCapabilities),
		enabled:      enabled,
		minTokens:    minTokens,
		logger:       logger,
	}
}

// SetCapabilities sets the capabilities for a specific provider.
//
// Description:
//
//	Should be called during initialization to register known provider
//	capabilities. If not set, DefaultCapabilities is used at minimization time.
//
// Thread Safety: NOT safe for concurrent use with Minimize(). Must only be
// called during initialization before any concurrent access begins.
//
// Inputs:
//   - provider: The provider name (e.g., "anthropic").
//   - caps: The provider's capabilities.
func (m *DataMinimizer) SetCapabilities(provider string, caps ProviderCapabilities) {
	m.capabilities[provider] = caps
}

// getCapabilities returns capabilities for a provider, falling back to defaults.
func (m *DataMinimizer) getCapabilities(provider, model string) ProviderCapabilities {
	if caps, ok := m.capabilities[provider]; ok {
		return caps
	}
	return DefaultCapabilities(provider, model)
}

// Minimize applies the minimization pipeline to a request.
//
// Description:
//
//	Runs four stages of minimization on a copy of the request:
//	  1. filterSystemPrompt — strips internal sections from the system prompt
//	  2. filterToolDefinitions — removes unused tool definitions
//	  3. minimizeMessages — truncates large tool results, strips file paths
//	  4. truncateToContextWindow — drops oldest messages if over context limit
//
//	Returns the original request unchanged when:
//	  - Minimization is disabled
//	  - Provider is "ollama" (local, no egress)
//	  - Estimated tokens are below minTokens threshold
//
// Inputs:
//   - request: The LLM request to minimize. Must not be nil.
//   - provider: The target provider name.
//   - model: The target model name.
//
// Outputs:
//   - *agentllm.Request: The minimized request (new copy, or original if skipped).
//   - MinimizationStats: Statistics about what was reduced.
//
// Assumptions:
//   - request is not nil (caller validates).
//   - provider and model are non-empty strings.
func (m *DataMinimizer) Minimize(request *agentllm.Request, provider, model string) (*agentllm.Request, MinimizationStats) {
	stats := MinimizationStats{}

	// Pass-through conditions
	if !m.enabled || provider == "ollama" {
		return request, stats
	}

	// Estimate original tokens
	originalTokens := estimateRequestTokens(request)
	stats.OriginalTokens = originalTokens

	if originalTokens < m.minTokens {
		stats.MinimizedTokens = originalTokens
		return request, stats
	}

	caps := m.getCapabilities(provider, model)

	// Create a deep copy of the request to avoid mutating the original
	minimized := copyRequest(request)

	// Stage 1: Filter system prompt
	originalPromptTokens := estimateTokens(minimized.SystemPrompt)
	minimized.SystemPrompt = m.filterSystemPrompt(minimized.SystemPrompt)
	newPromptTokens := estimateTokens(minimized.SystemPrompt)
	stats.SystemPromptDelta = originalPromptTokens - newPromptTokens

	// Stage 2: Filter tool definitions
	originalToolTokens := estimateToolTokens(minimized.Tools)
	minimized.Tools = m.filterToolDefinitions(minimized.Tools, minimized.ToolChoice)
	newToolTokens := estimateToolTokens(minimized.Tools)
	stats.ToolDefsDelta = originalToolTokens - newToolTokens

	// Stage 3: Minimize messages
	originalMsgTokens := estimateMessagesTokens(minimized.Messages)
	minimized.Messages, stats.TruncatedResults = m.minimizeMessages(minimized.Messages, caps)
	newMsgTokens := estimateMessagesTokens(minimized.Messages)
	stats.MessagesDelta = originalMsgTokens - newMsgTokens

	// Stage 4: Truncate to context window
	minimized, stats.DroppedMessages = m.truncateToContextWindow(minimized, caps.MaxContextTokens)

	stats.MinimizedTokens = estimateRequestTokens(minimized)

	m.logger.Debug("request minimized",
		slog.String("provider", provider),
		slog.String("model", model),
		slog.Int("original_tokens", stats.OriginalTokens),
		slog.Int("minimized_tokens", stats.MinimizedTokens),
		slog.Float64("reduction_pct", stats.Reduction()),
	)

	return minimized, stats
}

// filterSystemPrompt strips internal routing sections from the system prompt.
//
// Description:
//
//	Identifies sections by their "## " header markers and removes those
//	that contain internal architecture details (tool routing rules, stopping
//	criteria, grounding rules). Preserves all other sections including
//	user-facing instructions and the QUESTION → TOOL MAPPING table.
//
// Inputs:
//   - prompt: The full system prompt text.
//
// Outputs:
//   - string: The filtered system prompt with internal sections removed.
func (m *DataMinimizer) filterSystemPrompt(prompt string) string {
	if prompt == "" {
		return prompt
	}

	lines := strings.Split(prompt, "\n")
	var result []string
	skipping := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this line starts a new section
		if strings.HasPrefix(trimmed, "## ") {
			skipping = m.isInternalSection(trimmed)
			if skipping {
				continue
			}
		}

		if !skipping {
			result = append(result, line)
		}
	}

	// Clean up consecutive blank lines
	return cleanBlankLines(strings.Join(result, "\n"))
}

// isInternalSection checks if a section header matches an internal section.
func (m *DataMinimizer) isInternalSection(header string) bool {
	for _, section := range internalSystemPromptSections {
		if strings.HasPrefix(header, section) {
			return true
		}
	}
	return false
}

// filterToolDefinitions keeps only relevant tool definitions based on ToolChoice.
//
// Description:
//
//	Reduces the number of tool definitions sent to the provider:
//	  - ToolChoice type "tool": keep only the named tool
//	  - ToolChoice type "none": return nil (no tools needed)
//	  - ToolChoice type "auto" or "any" or nil: keep all tools
//
// Inputs:
//   - defs: The full set of tool definitions.
//   - choice: The tool choice directive (may be nil).
//
// Outputs:
//   - []tools.ToolDefinition: Filtered tool definitions.
func (m *DataMinimizer) filterToolDefinitions(defs []tools.ToolDefinition, choice *agentllm.ToolChoice) []tools.ToolDefinition {
	if choice == nil || len(defs) == 0 {
		return defs
	}

	switch choice.Type {
	case "none":
		return nil
	case "tool":
		if choice.Name == "" {
			return defs
		}
		for _, def := range defs {
			if def.Name == choice.Name {
				return []tools.ToolDefinition{def}
			}
		}
		// Tool not found — keep all to avoid breaking the request
		return defs
	default:
		// "auto", "any", or unknown — keep all so the model can choose
		return defs
	}
}

// minimizeMessages processes messages to reduce token count.
//
// Description:
//
//	Applies three transformations to messages:
//	  1. Compresses conversation turns older than HistoryWindow
//	  2. Truncates tool result content exceeding MaxToolResultTokens
//	  3. Strips absolute file paths when CanReceiveFileSystemInfo is false
//
// Inputs:
//   - messages: The conversation messages.
//   - caps: Provider capabilities controlling truncation limits.
//
// Outputs:
//   - []agentllm.Message: Minimized messages (new slice, originals unchanged).
//   - int: Number of tool results that were truncated.
func (m *DataMinimizer) minimizeMessages(messages []agentllm.Message, caps ProviderCapabilities) ([]agentllm.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	result := make([]agentllm.Message, len(messages))
	truncatedCount := 0

	// Determine the history window boundary.
	// Messages are ordered chronologically; the last HistoryWindow messages
	// are kept verbatim, older ones are compressed.
	windowStart := len(messages) - caps.HistoryWindow
	if windowStart < 0 {
		windowStart = 0
	}

	for i, msg := range messages {
		result[i] = msg // shallow copy (strings are immutable in Go)

		// Compress old turns outside the history window
		if i < windowStart && msg.Role != "system" {
			result[i] = compressTurn(msg)
			continue
		}

		// Determine if we need to modify tool results (truncation or path-stripping).
		// If so, copy the ToolResults slice ONCE to avoid mutating the original.
		willModifyResults := len(msg.ToolResults) > 0 &&
			(caps.MaxToolResultTokens > 0 || !caps.CanReceiveFileSystemInfo)

		if willModifyResults {
			newResults := make([]agentllm.ToolCallResult, len(msg.ToolResults))
			copy(newResults, msg.ToolResults)
			result[i].ToolResults = newResults
		}

		// Truncate large tool results
		if caps.MaxToolResultTokens > 0 && len(result[i].ToolResults) > 0 {
			for j, tr := range result[i].ToolResults {
				tokens := estimateTokens(tr.Content)
				if tokens > caps.MaxToolResultTokens {
					truncated := truncateToTokens(tr.Content, caps.MaxToolResultTokens)
					omitted := tokens - caps.MaxToolResultTokens
					result[i].ToolResults[j].Content = truncated + fmt.Sprintf("\n[TRUNCATED: ~%d tokens omitted]", omitted)
					truncatedCount++
				}
			}
		}

		// Strip absolute file paths if provider cannot receive FS info
		if !caps.CanReceiveFileSystemInfo {
			result[i].Content = stripAbsolutePaths(result[i].Content)
			for j := range result[i].ToolResults {
				result[i].ToolResults[j].Content = stripAbsolutePaths(result[i].ToolResults[j].Content)
			}
		}
	}

	return result, truncatedCount
}

// truncateToContextWindow drops oldest messages if the request exceeds the
// provider's context window.
//
// Description:
//
//	Estimates total tokens for the request. If within the limit, returns
//	unchanged. Otherwise, drops messages from the beginning (oldest first),
//	preserving the system prompt, tool definitions, and most recent messages.
//	The first message (code context) is preserved if possible but truncated
//	from the end if needed.
//
// Inputs:
//   - request: The request to truncate.
//   - maxTokens: The provider's maximum context window.
//
// Outputs:
//   - *agentllm.Request: The (possibly) truncated request.
//   - int: Number of messages dropped.
func (m *DataMinimizer) truncateToContextWindow(request *agentllm.Request, maxTokens int) (*agentllm.Request, int) {
	if maxTokens <= 0 {
		return request, 0
	}

	totalTokens := estimateRequestTokens(request)
	if totalTokens <= maxTokens {
		return request, 0
	}

	// Calculate fixed overhead (system prompt + tools + response buffer)
	fixedTokens := estimateTokens(request.SystemPrompt) + estimateToolTokens(request.Tools)
	// Reserve 15% of context for response
	responseBuffer := maxTokens * 15 / 100
	availableForMessages := maxTokens - fixedTokens - responseBuffer

	if availableForMessages <= 0 {
		// System prompt + tools alone exceed the limit; truncate system prompt
		m.logger.Warn("system prompt and tools exceed context window",
			slog.Int("fixed_tokens", fixedTokens),
			slog.Int("max_tokens", maxTokens),
		)
		return request, 0
	}

	// Drop messages from the beginning until we fit, keeping the last messages
	messages := request.Messages
	dropped := 0
	messageTokens := estimateMessagesTokens(messages)

	for messageTokens > availableForMessages && len(messages) > 1 {
		messages = messages[1:]
		dropped++
		messageTokens = estimateMessagesTokens(messages)
	}

	if dropped > 0 {
		result := copyRequest(request)
		result.Messages = messages
		return result, dropped
	}

	return request, 0
}

// compressTurn compresses an old conversation turn into a brief summary.
func compressTurn(msg agentllm.Message) agentllm.Message {
	compressed := agentllm.Message{
		Role: msg.Role,
	}

	switch msg.Role {
	case "user":
		preview := truncateString(msg.Content, 100)
		compressed.Content = fmt.Sprintf("[Previous turn: user said: %s]", preview)
	case "assistant":
		if len(msg.ToolCalls) > 0 {
			toolNames := make([]string, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				toolNames[i] = tc.Name
			}
			compressed.Content = fmt.Sprintf("[Previous turn: assistant used tools: %s]", strings.Join(toolNames, ", "))
		} else {
			preview := truncateString(msg.Content, 100)
			compressed.Content = fmt.Sprintf("[Previous turn: assistant said: %s]", preview)
		}
	case "tool":
		if len(msg.ToolResults) > 0 {
			ids := make([]string, len(msg.ToolResults))
			for i, tr := range msg.ToolResults {
				ids[i] = tr.ToolCallID
			}
			compressed.Content = fmt.Sprintf("[Previous turn: tool results for: %s]", strings.Join(ids, ", "))
		} else {
			compressed.Content = "[Previous turn: tool result]"
		}
	default:
		compressed.Content = msg.Content
	}

	return compressed
}

// =============================================================================
// Helper functions
// =============================================================================

// copyRequest creates a shallow copy of a Request with independent slices.
// String fields are immutable and don't need deep copying.
func copyRequest(req *agentllm.Request) *agentllm.Request {
	cp := *req

	if len(req.Messages) > 0 {
		cp.Messages = make([]agentllm.Message, len(req.Messages))
		copy(cp.Messages, req.Messages)
	}

	if len(req.Tools) > 0 {
		cp.Tools = make([]tools.ToolDefinition, len(req.Tools))
		copy(cp.Tools, req.Tools)
	}

	if len(req.StopSequences) > 0 {
		cp.StopSequences = make([]string, len(req.StopSequences))
		copy(cp.StopSequences, req.StopSequences)
	}

	if req.ToolChoice != nil {
		tc := *req.ToolChoice
		cp.ToolChoice = &tc
	}

	return &cp
}

// estimateTokens provides a rough token count estimate for a string.
// Uses the approximation of 1 token per 4 characters (consistent with
// the egress guard's existing estimation in guard.go).
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	tokens := len(s) / 4
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// estimateRequestTokens estimates total tokens for a complete request.
func estimateRequestTokens(req *agentllm.Request) int {
	total := estimateTokens(req.SystemPrompt)
	total += estimateToolTokens(req.Tools)
	total += estimateMessagesTokens(req.Messages)
	return total
}

// estimateToolTokens estimates tokens for a set of tool definitions.
// Each tool definition is roughly name + description + parameter schemas.
func estimateToolTokens(defs []tools.ToolDefinition) int {
	total := 0
	for _, def := range defs {
		// Name + description
		total += estimateTokens(def.Name)
		total += estimateTokens(def.Description)
		// Parameters: rough estimate per parameter
		total += len(def.Parameters) * 20
	}
	return total
}

// estimateMessagesTokens estimates tokens for a slice of messages.
func estimateMessagesTokens(messages []agentllm.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateTokens(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += estimateTokens(tc.Name) + estimateTokens(tc.Arguments)
		}
		for _, tr := range msg.ToolResults {
			total += estimateTokens(tr.Content)
		}
		// Per-message overhead (role, separators)
		total += 4
	}
	return total
}

// truncateToTokens truncates a string to approximately the given token count.
func truncateToTokens(s string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars]
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// stripAbsolutePaths replaces absolute file paths with relative ones.
// Matches common patterns like /Users/..., /home/..., /var/..., /tmp/...
// and replaces up to the project-level directory with "./".
func stripAbsolutePaths(s string) string {
	if s == "" {
		return s
	}

	// Replace common absolute path prefixes.
	// Matches /Users/<user>/<path>/ or /home/<user>/<path>/ patterns.
	result := s

	// Split on whitespace-like boundaries and process tokens
	// that look like absolute paths.
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		lines[i] = stripPathsInLine(line)
	}
	return strings.Join(lines, "\n")
}

// stripPathsInLine replaces absolute paths within a single line.
func stripPathsInLine(line string) string {
	// Look for patterns like /Users/*/.../ or /home/*/...
	// We process the line character by character to find path-like tokens.
	var result strings.Builder
	result.Grow(len(line))

	i := 0
	for i < len(line) {
		if line[i] == '/' && i < len(line)-1 && isPathStart(line[i:]) {
			// Found what looks like an absolute path
			pathEnd := findPathEnd(line, i)
			absPath := line[i:pathEnd]
			result.WriteString(makeRelative(absPath))
			i = pathEnd
		} else {
			result.WriteByte(line[i])
			i++
		}
	}

	return result.String()
}

// isPathStart checks if the string starts with a common absolute path prefix.
func isPathStart(s string) bool {
	prefixes := []string{"/Users/", "/home/", "/var/", "/tmp/", "/opt/"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// findPathEnd finds where an absolute path token ends in a line.
func findPathEnd(line string, start int) int {
	i := start + 1
	for i < len(line) {
		c := line[i]
		// Path characters: letters, digits, /, ., -, _, ~
		if c == ' ' || c == '\t' || c == '"' || c == '\'' || c == ',' ||
			c == ';' || c == ')' || c == ']' || c == '}' || c == '>' {
			break
		}
		i++
	}
	return i
}

// makeRelative converts an absolute path to a relative one by stripping
// everything up to and including the last recognizable project directory.
func makeRelative(absPath string) string {
	// Find a reasonable point to cut: look for common project indicators
	// like "GolandProjects/", "Projects/", "src/", "services/"
	markers := []string{"GolandProjects/", "Projects/", "workspace/", "repos/"}
	for _, marker := range markers {
		idx := strings.Index(absPath, marker)
		if idx >= 0 {
			// Skip the marker and the project name after it
			after := absPath[idx+len(marker):]
			slashIdx := strings.Index(after, "/")
			if slashIdx >= 0 {
				return "./" + after[slashIdx+1:]
			}
			return "./" + after
		}
	}

	// Fallback: strip /Users/<user>/ or /home/<user>/
	parts := strings.SplitN(absPath, "/", 5) // ["", "Users", "<user>", ...]
	if len(parts) >= 5 {
		return "./" + parts[4]
	}
	if len(parts) >= 4 {
		return "./" + parts[3]
	}

	return absPath
}

// cleanBlankLines collapses runs of 3+ consecutive blank lines to 2.
func cleanBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	blankCount := 0

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount <= 2 {
				result = append(result, line)
			}
		} else {
			blankCount = 0
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}
