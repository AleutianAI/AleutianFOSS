// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package config provides configuration loading for the trace service.
//
// This package implements the tool routing registry loader (CB-31e) which
// provides structured keywords and usage guidance for the tool router.
//
// Thread Safety:
//
//	All exported functions and types are safe for concurrent use.
package config

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/yaml.v3"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// =============================================================================
// Constants (SEC2: File size limits)
// =============================================================================

const (
	// MaxYAMLFileSize is the maximum allowed YAML file size (1MB).
	// SEC2: Prevents memory issues from large files.
	MaxYAMLFileSize = 1024 * 1024

	// MaxKeywordsPerTool is the maximum keywords allowed per tool.
	MaxKeywordsPerTool = 50

	// MaxToolsInRegistry is the maximum tools allowed in registry.
	MaxToolsInRegistry = 200
)

// =============================================================================
// Embedded Default Registry (P4: Embedded YAML for deployment simplicity)
// =============================================================================

//go:embed tool_registry.yaml
var defaultToolRegistryYAML []byte

// =============================================================================
// Prometheus Metrics (O2: Prometheus metrics for routing decisions)
// =============================================================================

var (
	toolRoutingDecisions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trace_tool_routing_decisions_total",
		Help: "Total tool routing decisions by tool and source",
	}, []string{"tool", "source"})

	toolRoutingLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_tool_routing_latency_seconds",
		Help:    "Tool routing decision latency",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1},
	})

	keywordMatchCount = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "trace_keyword_match_count",
		Help:    "Number of keywords matched per routing decision",
		Buckets: []float64{0, 1, 2, 3, 5, 10},
	}, []string{"tool"})

	fallbackBlockedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trace_fallback_blocked_total",
		Help: "Total times fallback to main LLM was blocked",
	})

	registryLoadErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trace_registry_load_errors_total",
		Help: "Total tool registry load errors",
	})

	registryLoadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_registry_load_duration_seconds",
		Help:    "Duration of tool registry loading",
		Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5},
	})
)

// =============================================================================
// OTel Tracer (O1: OTel spans for registry operations)
// =============================================================================

var toolRegistryTracer = otel.Tracer("trace.config.toolregistry")

// =============================================================================
// Types (I1: Concrete types only, no map[string]any)
// =============================================================================

// ToolRegistryYAML is the root structure for YAML deserialization.
//
// This struct uses concrete types (ToolEntryYAML, ToolSubstitutionYAML) rather
// than map[string]any to comply with CLAUDE.md Section 4.5. All nested slices
// are validated during parsing via parseToolRegistryYAML().
type ToolRegistryYAML struct {
	Tools []ToolEntryYAML `yaml:"tools"`
}

// ToolEntryYAML represents a single tool entry in the YAML file.
type ToolEntryYAML struct {
	Name      string                 `yaml:"name"`
	Keywords  []string               `yaml:"keywords"`
	UseWhen   string                 `yaml:"use_when"`
	AvoidWhen string                 `yaml:"avoid_when,omitempty"`
	InsteadOf []ToolSubstitutionYAML `yaml:"instead_of,omitempty"`
	Requires  []string               `yaml:"requires,omitempty"`
}

// ToolSubstitutionYAML represents a tool substitution in YAML.
type ToolSubstitutionYAML struct {
	Tool string `yaml:"tool"`
	When string `yaml:"when"`
}

// ToolRoutingRegistry provides tool routing metadata for the tool router.
//
// Thread Safety: Safe for concurrent use after initialization.
type ToolRoutingRegistry struct {
	// entries maps tool name to its routing entry.
	entries map[string]*ToolRoutingEntry

	// keywordIndex maps lowercase keywords to tool names for O(1) lookup.
	// Keys are always lowercase for case-insensitive matching.
	keywordIndex map[string][]string

	// loadedAt is when the registry was loaded (Unix milliseconds UTC - I3).
	loadedAt int64

	// yamlHash is the hash of the source YAML for cache invalidation.
	yamlHash string
}

// ToolRoutingEntry contains routing guidance for a single tool.
type ToolRoutingEntry struct {
	// Name is the tool name.
	Name string

	// Keywords are query terms that trigger this tool.
	Keywords []string

	// UseWhen describes when to use this tool.
	UseWhen string

	// AvoidWhen describes when NOT to use this tool.
	AvoidWhen string

	// InsteadOf lists tool substitutions.
	InsteadOf []tools.ToolSubstitution

	// Requires lists prerequisites.
	Requires []string
}

// =============================================================================
// Singleton Registry (R1: sync.Once for thread-safe initialization)
// =============================================================================

var (
	registryMu      sync.RWMutex
	registryOnce    sync.Once
	cachedRegistry  *ToolRoutingRegistry
	registryLoadErr error
)

// GetToolRoutingRegistry returns the cached tool routing registry.
//
// Description:
//
//	Loads the tool routing registry on first call and caches it for
//	subsequent calls. Uses sync.Once for thread-safe initialization.
//
// Inputs:
//
//	ctx - Context for tracing. Must not be nil.
//
// Outputs:
//
//	*ToolRoutingRegistry - The loaded registry. Never nil on success.
//	error - Non-nil if loading failed.
//
// Thread Safety: Safe for concurrent use via sync.Once.
//
// Example:
//
//	registry, err := config.GetToolRoutingRegistry(ctx)
//	if err != nil {
//	    return fmt.Errorf("loading tool registry: %w", err)
//	}
//	matches := registry.FindToolsByKeyword("callers")
func GetToolRoutingRegistry(ctx context.Context) (*ToolRoutingRegistry, error) {
	// S3: Context validation
	if ctx == nil {
		return nil, fmt.Errorf("GetToolRoutingRegistry: ctx must not be nil")
	}

	// Thread-safe initialization with mutex protection for reset compatibility
	registryMu.RLock()
	if cachedRegistry != nil || registryLoadErr != nil {
		reg, err := cachedRegistry, registryLoadErr
		registryMu.RUnlock()
		return reg, err
	}
	registryMu.RUnlock()

	// Upgrade to write lock for initialization
	registryMu.Lock()
	defer registryMu.Unlock()

	// Double-check after acquiring write lock
	if cachedRegistry != nil || registryLoadErr != nil {
		return cachedRegistry, registryLoadErr
	}

	registryOnce.Do(func() {
		cachedRegistry, registryLoadErr = loadToolRoutingRegistry(ctx)
	})

	return cachedRegistry, registryLoadErr
}

// ResetToolRoutingRegistry resets the cached registry for testing.
//
// Description:
//
//	Clears the cached registry and sync.Once state to allow re-loading
//	the registry on the next call to GetToolRoutingRegistry.
//
// Thread Safety:
//
//	Safe for concurrent use. Uses mutex to protect against data races
//	when called concurrently with GetToolRoutingRegistry.
//
// WARNING: This function is intended for testing only. Do not use
// in production code as it can cause inconsistent state if called
// while other goroutines are using the registry.
func ResetToolRoutingRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryOnce = sync.Once{}
	cachedRegistry = nil
	registryLoadErr = nil
}

// =============================================================================
// Loading Logic
// =============================================================================

// loadToolRoutingRegistry loads the registry from YAML.
//
// R2: Graceful fallback if YAML missing/corrupt - uses embedded default.
// P1: YAML parsed once at startup.
func loadToolRoutingRegistry(ctx context.Context) (*ToolRoutingRegistry, error) {
	// O1: Start span for registry loading
	ctx, span := toolRegistryTracer.Start(ctx, "toolregistry.Load")
	defer span.End()

	startTime := time.Now()
	defer func() {
		registryLoadDuration.Observe(time.Since(startTime).Seconds())
	}()

	// Try to load from external file first (allows customization)
	externalPath := getExternalRegistryPath()
	var yamlData []byte
	var source string

	if externalPath != "" {
		data, err := loadExternalYAML(ctx, externalPath)
		if err == nil {
			yamlData = data
			source = "external"
			slog.Info("CB-31e: Loaded tool registry from external file",
				slog.String("path", externalPath))
		} else {
			// R2: Log warning but continue with embedded default
			slog.Warn("CB-31e: External tool registry not available, using embedded default",
				slog.String("path", externalPath),
				slog.String("error", err.Error()))
		}
	}

	// Fall back to embedded default
	if yamlData == nil {
		yamlData = defaultToolRegistryYAML
		source = "embedded"
		slog.Debug("CB-31e: Using embedded tool registry")
	}

	span.SetAttributes(
		attribute.String("source", source),
		attribute.Int("yaml_size", len(yamlData)),
	)

	// Parse YAML
	registry, err := parseToolRegistryYAML(ctx, yamlData)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse failed")
		registryLoadErrors.Inc()
		return nil, fmt.Errorf("parsing tool registry YAML: %w", err)
	}

	span.SetAttributes(
		attribute.Int("tool_count", len(registry.entries)),
		attribute.Int("keyword_count", len(registry.keywordIndex)),
	)

	slog.Info("CB-31e: Tool registry loaded successfully",
		slog.Int("tool_count", len(registry.entries)),
		slog.Int("keyword_count", len(registry.keywordIndex)),
		slog.String("source", source))

	return registry, nil
}

// getExternalRegistryPath returns the path to external registry file.
// Returns empty string if no external path is configured.
func getExternalRegistryPath() string {
	// Check environment variable first
	if path := os.Getenv("TOOL_REGISTRY_PATH"); path != "" {
		return path
	}

	// Check common locations
	locations := []string{
		"./config/tool_registry.yaml",
		"./tool_registry.yaml",
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			absPath, _ := filepath.Abs(loc)
			return absPath
		}
	}

	return ""
}

// loadExternalYAML loads YAML from an external file with security checks.
//
// SEC1: Path validation
// SEC2: File size limits
func loadExternalYAML(ctx context.Context, path string) ([]byte, error) {
	ctx, span := toolRegistryTracer.Start(ctx, "toolregistry.LoadExternal",
		trace.WithAttributes(attribute.String("path", path)),
	)
	defer span.End()

	// SEC1: Validate path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	// Check for traversal attempts
	if strings.Contains(absPath, "..") {
		return nil, fmt.Errorf("loadExternalYAML: path traversal not allowed: %s", absPath)
	}

	// SEC2: Check file size
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	if info.Size() > MaxYAMLFileSize {
		return nil, fmt.Errorf("YAML file too large: %d bytes (max %d)", info.Size(), MaxYAMLFileSize)
	}

	span.SetAttributes(attribute.Int64("file_size", info.Size()))

	// Read file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return data, nil
}

// parseToolRegistryYAML parses YAML data into a registry.
//
// I1: Uses concrete types only.
// I2: Validates tool entries.
// P2: Builds keyword index for O(1) lookup.
func parseToolRegistryYAML(ctx context.Context, data []byte) (*ToolRoutingRegistry, error) {
	ctx, span := toolRegistryTracer.Start(ctx, "toolregistry.Parse")
	defer span.End()

	// Parse YAML into concrete type (I1)
	var yamlReg ToolRegistryYAML
	if err := yaml.Unmarshal(data, &yamlReg); err != nil {
		return nil, fmt.Errorf("unmarshaling YAML: %w", err)
	}

	// Validate size limits
	if len(yamlReg.Tools) > MaxToolsInRegistry {
		return nil, fmt.Errorf("too many tools: %d (max %d)", len(yamlReg.Tools), MaxToolsInRegistry)
	}

	// Build registry
	registry := &ToolRoutingRegistry{
		entries:      make(map[string]*ToolRoutingEntry, len(yamlReg.Tools)),
		keywordIndex: make(map[string][]string),
		loadedAt:     time.Now().UnixMilli(), // I3: Unix milliseconds
	}

	for i, tool := range yamlReg.Tools {
		// I2: Validate entry
		if tool.Name == "" {
			return nil, fmt.Errorf("parseToolRegistryYAML: tool at index %d has empty name", i)
		}
		if len(tool.Keywords) == 0 {
			slog.Warn("CB-31e: Tool has no keywords",
				slog.String("tool", tool.Name))
		}
		if len(tool.Keywords) > MaxKeywordsPerTool {
			return nil, fmt.Errorf("tool %s has too many keywords: %d (max %d)",
				tool.Name, len(tool.Keywords), MaxKeywordsPerTool)
		}

		// Convert substitutions
		var substitutions []tools.ToolSubstitution
		for _, sub := range tool.InsteadOf {
			substitutions = append(substitutions, tools.ToolSubstitution{
				Tool: sub.Tool,
				When: sub.When,
			})
		}

		// Create entry
		entry := &ToolRoutingEntry{
			Name:      tool.Name,
			Keywords:  tool.Keywords,
			UseWhen:   tool.UseWhen,
			AvoidWhen: tool.AvoidWhen,
			InsteadOf: substitutions,
			Requires:  tool.Requires,
		}
		registry.entries[tool.Name] = entry

		// P2: Build keyword index for O(1) lookup
		for _, keyword := range tool.Keywords {
			lowerKeyword := strings.ToLower(keyword)
			registry.keywordIndex[lowerKeyword] = append(registry.keywordIndex[lowerKeyword], tool.Name)
		}
	}

	span.SetAttributes(
		attribute.Int("tool_count", len(registry.entries)),
		attribute.Int("keyword_count", len(registry.keywordIndex)),
	)

	return registry, nil
}

// =============================================================================
// Registry Methods
// =============================================================================

// GetEntry returns the routing entry for a tool.
//
// Inputs:
//
//	toolName - The tool name to look up.
//
// Outputs:
//
//	*ToolRoutingEntry - The entry, or nil if not found.
//	bool - True if found.
func (r *ToolRoutingRegistry) GetEntry(toolName string) (*ToolRoutingEntry, bool) {
	entry, ok := r.entries[toolName]
	return entry, ok
}

// FindToolsByKeyword finds tools matching keywords in a query.
//
// Description:
//
//	Performs O(1) keyword lookup using the pre-built keyword index.
//	Returns tools sorted by number of matching keywords (most matches first).
//
// Inputs:
//
//	query - The user query to match against.
//
// Outputs:
//
//	[]ToolMatch - Tools matching the query, sorted by match count.
//
// P2: Uses pre-built keyword index for O(1) lookup.
func (r *ToolRoutingRegistry) FindToolsByKeyword(query string) []ToolMatch {
	// S3: Input validation
	if r == nil || r.entries == nil {
		return []ToolMatch{}
	}
	// Limit query length to prevent DoS (10KB max)
	const maxQueryLen = 10240
	if len(query) > maxQueryLen {
		slog.Warn("FindToolsByKeyword: query too long, truncating",
			slog.Int("original_len", len(query)),
			slog.Int("max_len", maxQueryLen))
		query = query[:maxQueryLen]
	}
	if len(query) == 0 {
		return []ToolMatch{}
	}

	startTime := time.Now()
	defer func() {
		toolRoutingLatency.Observe(time.Since(startTime).Seconds())
	}()

	queryLower := strings.ToLower(query)
	words := strings.Fields(queryLower)

	// Count matches per tool
	matchCounts := make(map[string]int)
	matchedKeywords := make(map[string][]string)

	for _, rawWord := range words {
		// IT-11: Strip trailing punctuation so "communities?" matches keyword "communities".
		// Query tokenization splits on whitespace only, leaving punctuation attached to words.
		word := strings.TrimRight(rawWord, "?.,!;:\"'()[]{}…")
		if toolNames, ok := r.keywordIndex[word]; ok {
			for _, toolName := range toolNames {
				matchCounts[toolName]++
				matchedKeywords[toolName] = append(matchedKeywords[toolName], word)
			}
		}
	}

	// Also check multi-word keywords
	for keyword, toolNames := range r.keywordIndex {
		if strings.Contains(keyword, " ") && strings.Contains(queryLower, keyword) {
			for _, toolName := range toolNames {
				matchCounts[toolName]++
				matchedKeywords[toolName] = append(matchedKeywords[toolName], keyword)
			}
		}
	}

	// Build result
	var matches []ToolMatch
	for toolName, count := range matchCounts {
		entry := r.entries[toolName]
		if entry == nil {
			continue
		}

		matches = append(matches, ToolMatch{
			ToolName:        toolName,
			MatchCount:      count,
			MatchedKeywords: matchedKeywords[toolName],
			Entry:           entry,
		})

		// Record metric
		keywordMatchCount.WithLabelValues(toolName).Observe(float64(count))
	}

	// Sort by match count (descending)
	sortToolMatches(matches)

	return matches
}

// ToolMatch represents a tool that matched keywords in a query.
type ToolMatch struct {
	ToolName        string
	MatchCount      int
	MatchedKeywords []string
	Entry           *ToolRoutingEntry
}

// sortToolMatches sorts matches by count (descending) using O(n log n) sort.
func sortToolMatches(matches []ToolMatch) {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].MatchCount > matches[j].MatchCount
	})
}

// GetWhenToUse returns WhenToUse guidance for a tool.
//
// Description:
//
//	Converts the routing entry to a WhenToUse struct for use in
//	ToolDefinition population.
//
// Inputs:
//
//	toolName - The tool name.
//
// Outputs:
//
//	tools.WhenToUse - The guidance struct.
//	bool - True if tool was found.
func (r *ToolRoutingRegistry) GetWhenToUse(toolName string) (tools.WhenToUse, bool) {
	entry, ok := r.entries[toolName]
	if !ok {
		return tools.WhenToUse{}, false
	}

	return tools.WhenToUse{
		Keywords:  entry.Keywords,
		UseWhen:   entry.UseWhen,
		AvoidWhen: entry.AvoidWhen,
		InsteadOf: entry.InsteadOf,
	}, true
}

// ToolCount returns the number of tools in the registry.
//
// Outputs:
//
//	int - Number of registered tools.
//
// Thread Safety: Safe for concurrent use (read-only after initialization).
func (r *ToolRoutingRegistry) ToolCount() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// KeywordCount returns the number of unique keywords indexed.
//
// Outputs:
//
//	int - Number of unique keywords across all tools.
//
// Thread Safety: Safe for concurrent use (read-only after initialization).
func (r *ToolRoutingRegistry) KeywordCount() int {
	if r == nil {
		return 0
	}
	return len(r.keywordIndex)
}

// LoadedAt returns when the registry was loaded.
//
// Outputs:
//
//	int64 - Unix milliseconds UTC when the registry was loaded.
//
// Thread Safety: Safe for concurrent use (read-only after initialization).
func (r *ToolRoutingRegistry) LoadedAt() int64 {
	if r == nil {
		return 0
	}
	return r.loadedAt
}

// =============================================================================
// Metric Helpers (O2: Prometheus metrics for routing decisions)
// =============================================================================

// RecordRoutingDecision records a routing decision metric.
//
// Description:
//
//	Increments the tool_routing_decisions_total Prometheus counter
//	with the specified tool name and decision source.
//
// Inputs:
//
//	toolName - The name of the tool that was selected.
//	source - The decision source (e.g., "router", "keywords", "fallback").
//
// Thread Safety: Safe for concurrent use.
func RecordRoutingDecision(toolName, source string) {
	toolRoutingDecisions.WithLabelValues(toolName, source).Inc()
}

// RecordFallbackBlocked records when fallback was blocked.
//
// Description:
//
//	Increments the fallback_blocked_total Prometheus counter
//	when the router forces synthesis instead of falling back to main LLM.
//
// Thread Safety: Safe for concurrent use.
func RecordFallbackBlocked() {
	fallbackBlockedTotal.Inc()
}

// =============================================================================
// Tool Definition Population
// =============================================================================

// PopulateToolDefinitionsWhenToUse populates WhenToUse on tool definitions from the registry.
//
// Description:
//
//	This function bridges the gap between the YAML-defined routing metadata
//	and the code-defined ToolDefinitions. Call this after loading tools
//	to enrich them with routing guidance.
//
// Inputs:
//
//	ctx - Context for tracing. Must not be nil.
//	defs - Slice of tool definitions to populate (modified in place).
//
// Outputs:
//
//	int - Number of tools that were populated.
//	error - Non-nil if registry loading fails.
//
// Thread Safety: Safe for concurrent use.
//
// Example:
//
//	defs := registry.GetToolDefinitions()
//	count, err := config.PopulateToolDefinitionsWhenToUse(ctx, defs)
//	if err != nil {
//	    slog.Warn("Failed to populate WhenToUse", "error", err)
//	}
func PopulateToolDefinitionsWhenToUse(ctx context.Context, defs []tools.ToolDefinition) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("PopulateToolDefinitionsWhenToUse: ctx must not be nil")
	}
	if defs == nil {
		return 0, nil // Nothing to populate
	}

	reg, err := GetToolRoutingRegistry(ctx)
	if err != nil {
		return 0, fmt.Errorf("PopulateToolDefinitionsWhenToUse: loading tool registry: %w", err)
	}

	populated := 0
	for i := range defs {
		if whenToUse, ok := reg.GetWhenToUse(defs[i].Name); ok {
			defs[i].WhenToUse = whenToUse
			populated++
		}
	}

	slog.Debug("CB-31e: Populated WhenToUse on tool definitions",
		slog.Int("populated", populated),
		slog.Int("total", len(defs)),
	)

	return populated, nil
}
