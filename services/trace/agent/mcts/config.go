// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package mcts

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// MCTSFullConfig contains all MCTS-related configuration.
// This is the top-level config struct that can be loaded from files/env.
//
// Thread Safety: Safe to read concurrently. Not safe to modify after creation.
type MCTSFullConfig struct {
	// Budget contains resource limit settings.
	Budget BudgetConfig `json:"budget" yaml:"budget"`

	// Algorithm contains MCTS algorithm settings.
	Algorithm AlgorithmConfig `json:"algorithm" yaml:"algorithm"`

	// Simulation contains simulation settings.
	Simulation SimulationFullConfig `json:"simulation" yaml:"simulation"`

	// CircuitBreaker contains circuit breaker settings.
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker" yaml:"circuit_breaker"`

	// Degradation contains degradation settings.
	Degradation DegradationConfig `json:"degradation" yaml:"degradation"`

	// Parallel contains parallel execution settings.
	Parallel ParallelConfig `json:"parallel" yaml:"parallel"`

	// Pruning contains tree pruning settings.
	Pruning PruningConfig `json:"pruning" yaml:"pruning"`

	// Observability contains observability settings.
	Observability ObservabilityConfig `json:"observability" yaml:"observability"`
}

// BudgetConfig contains budget-related settings.
type BudgetConfig struct {
	MaxNodes      int           `json:"max_nodes" yaml:"max_nodes"`
	MaxDepth      int           `json:"max_depth" yaml:"max_depth"`
	MaxExpansions int           `json:"max_expansions" yaml:"max_expansions"`
	TimeLimit     time.Duration `json:"time_limit" yaml:"time_limit"`
	LLMTokenLimit int           `json:"llm_token_limit" yaml:"llm_token_limit"`
	LLMCallLimit  int           `json:"llm_call_limit" yaml:"llm_call_limit"`
	CostLimitUSD  float64       `json:"cost_limit_usd" yaml:"cost_limit_usd"`
}

// AlgorithmConfig contains MCTS algorithm settings.
type AlgorithmConfig struct {
	ExplorationConstant   float64 `json:"exploration_constant" yaml:"exploration_constant"`
	MinVisitsBeforeExpand int     `json:"min_visits_before_expand" yaml:"min_visits_before_expand"`
	MaxAlternatives       int     `json:"max_alternatives" yaml:"max_alternatives"`
	SimulationDepth       int     `json:"simulation_depth" yaml:"simulation_depth"`
}

// SimulationFullConfig contains simulation settings.
type SimulationFullConfig struct {
	QuickScoreThreshold    float64       `json:"quick_score_threshold" yaml:"quick_score_threshold"`
	StandardScoreThreshold float64       `json:"standard_score_threshold" yaml:"standard_score_threshold"`
	QuickTimeout           time.Duration `json:"quick_timeout" yaml:"quick_timeout"`
	StandardTimeout        time.Duration `json:"standard_timeout" yaml:"standard_timeout"`
	FullTimeout            time.Duration `json:"full_timeout" yaml:"full_timeout"`
	EnableSecurityScan     bool          `json:"enable_security_scan" yaml:"enable_security_scan"`
}

// ParallelConfig contains parallel execution settings.
type ParallelConfig struct {
	Enabled        bool `json:"enabled" yaml:"enabled"`
	MaxConcurrency int  `json:"max_concurrency" yaml:"max_concurrency"`
	BatchSize      int  `json:"batch_size" yaml:"batch_size"`
}

// PruningConfig contains tree pruning settings.
type PruningConfig struct {
	PruneInterval   int           `json:"prune_interval" yaml:"prune_interval"`
	MinVisitsToKeep int           `json:"min_visits_to_keep" yaml:"min_visits_to_keep"`
	MaxAbandonedAge time.Duration `json:"max_abandoned_age" yaml:"max_abandoned_age"`
	KeepBestN       int           `json:"keep_best_n" yaml:"keep_best_n"`
	ScoreThreshold  float64       `json:"score_threshold" yaml:"score_threshold"`
	VisitsThreshold int64         `json:"visits_threshold" yaml:"visits_threshold"`
}

// ObservabilityConfig contains observability settings.
type ObservabilityConfig struct {
	TracingEnabled bool    `json:"tracing_enabled" yaml:"tracing_enabled"`
	MetricsEnabled bool    `json:"metrics_enabled" yaml:"metrics_enabled"`
	LogLevel       string  `json:"log_level" yaml:"log_level"`
	SampleRate     float64 `json:"sample_rate" yaml:"sample_rate"`
	ServiceName    string  `json:"service_name" yaml:"service_name"`
}

// DefaultMCTSFullConfig returns the default configuration.
//
// Outputs:
//   - MCTSFullConfig: Default configuration with sensible values.
func DefaultMCTSFullConfig() MCTSFullConfig {
	return MCTSFullConfig{
		Budget: BudgetConfig{
			MaxNodes:      10,
			MaxDepth:      4,
			MaxExpansions: 3,
			TimeLimit:     30 * time.Second,
			LLMTokenLimit: 5000,
			LLMCallLimit:  10,
			CostLimitUSD:  0.10,
		},
		Algorithm: AlgorithmConfig{
			ExplorationConstant:   1.41,
			MinVisitsBeforeExpand: 1,
			MaxAlternatives:       3,
			SimulationDepth:       1,
		},
		Simulation: SimulationFullConfig{
			QuickScoreThreshold:    0.5,
			StandardScoreThreshold: 0.7,
			QuickTimeout:           5 * time.Second,
			StandardTimeout:        30 * time.Second,
			FullTimeout:            2 * time.Minute,
			EnableSecurityScan:     true,
		},
		CircuitBreaker: DefaultCircuitBreakerConfig(),
		Degradation:    DefaultDegradationConfig(),
		Parallel: ParallelConfig{
			Enabled:        false, // Off by default
			MaxConcurrency: 4,
			BatchSize:      4,
		},
		Pruning: PruningConfig{
			PruneInterval:   10,
			MinVisitsToKeep: 2,
			MaxAbandonedAge: 5 * time.Minute,
			KeepBestN:       3,
			ScoreThreshold:  0.3,
			VisitsThreshold: 1,
		},
		Observability: ObservabilityConfig{
			TracingEnabled: true,
			MetricsEnabled: true,
			LogLevel:       "info",
			SampleRate:     1.0,
			ServiceName:    "trace-mcts",
		},
	}
}

// LoadMCTSConfig loads configuration with priority: env > file > defaults.
//
// Inputs:
//   - configPath: Path to YAML/JSON config file (optional, can be empty).
//
// Outputs:
//   - MCTSFullConfig: Merged configuration.
//   - error: Non-nil if file exists but is invalid.
func LoadMCTSConfig(configPath string) (MCTSFullConfig, error) {
	// Start with defaults
	config := DefaultMCTSFullConfig()

	// Load from file if specified
	if configPath != "" {
		if err := loadConfigFile(configPath, &config); err != nil {
			return config, fmt.Errorf("load config file: %w", err)
		}
	}

	// Override from environment variables
	loadConfigFromEnv(&config)

	// Validate
	if err := config.Validate(); err != nil {
		return config, fmt.Errorf("invalid config: %w", err)
	}

	return config, nil
}

func loadConfigFile(path string, config *MCTSFullConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist, use defaults
		}
		return err
	}

	// Try YAML first, then JSON
	if err := yaml.Unmarshal(data, config); err != nil {
		if jsonErr := json.Unmarshal(data, config); jsonErr != nil {
			return fmt.Errorf("parse config (tried YAML and JSON): YAML error: %v, JSON error: %w", err, jsonErr)
		}
	}

	return nil
}

func loadConfigFromEnv(config *MCTSFullConfig) {
	// Budget
	if v := os.Getenv("MCTS_MAX_NODES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Budget.MaxNodes = i
		}
	}
	if v := os.Getenv("MCTS_MAX_DEPTH"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Budget.MaxDepth = i
		}
	}
	if v := os.Getenv("MCTS_MAX_EXPANSIONS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Budget.MaxExpansions = i
		}
	}
	if v := os.Getenv("MCTS_LLM_TOKEN_LIMIT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Budget.LLMTokenLimit = i
		}
	}
	if v := os.Getenv("MCTS_LLM_CALL_LIMIT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Budget.LLMCallLimit = i
		}
	}
	if v := os.Getenv("MCTS_COST_LIMIT_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			config.Budget.CostLimitUSD = f
		}
	}
	if v := os.Getenv("MCTS_TIME_LIMIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			config.Budget.TimeLimit = d
		}
	}

	// Algorithm
	if v := os.Getenv("MCTS_EXPLORATION_CONSTANT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			config.Algorithm.ExplorationConstant = f
		}
	}
	if v := os.Getenv("MCTS_MAX_ALTERNATIVES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Algorithm.MaxAlternatives = i
		}
	}

	// Parallel
	if v := os.Getenv("MCTS_PARALLEL_ENABLED"); v != "" {
		config.Parallel.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MCTS_MAX_CONCURRENCY"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			config.Parallel.MaxConcurrency = i
		}
	}

	// Observability
	if v := os.Getenv("MCTS_TRACING_ENABLED"); v != "" {
		config.Observability.TracingEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MCTS_METRICS_ENABLED"); v != "" {
		config.Observability.MetricsEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MCTS_LOG_LEVEL"); v != "" {
		config.Observability.LogLevel = v
	}
	if v := os.Getenv("MCTS_TRACE_SAMPLE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			config.Observability.SampleRate = f
		}
	}
	if v := os.Getenv("MCTS_ENABLE_SECURITY_SCAN"); v != "" {
		config.Simulation.EnableSecurityScan = v == "true" || v == "1"
	}
}

// Validate checks that the configuration is valid.
//
// Outputs:
//   - error: Non-nil if configuration is invalid.
func (c MCTSFullConfig) Validate() error {
	if c.Budget.MaxNodes < 1 {
		return fmt.Errorf("max_nodes must be >= 1")
	}
	if c.Budget.MaxDepth < 1 {
		return fmt.Errorf("max_depth must be >= 1")
	}
	if c.Budget.LLMCallLimit < 1 {
		return fmt.Errorf("llm_call_limit must be >= 1")
	}
	if c.Budget.CostLimitUSD <= 0 {
		return fmt.Errorf("cost_limit_usd must be > 0")
	}
	if c.Algorithm.ExplorationConstant <= 0 {
		return fmt.Errorf("exploration_constant must be > 0")
	}
	if c.Observability.SampleRate < 0 || c.Observability.SampleRate > 1 {
		return fmt.Errorf("sample_rate must be between 0 and 1")
	}
	return nil
}

// ToTreeBudgetConfig converts BudgetConfig to TreeBudgetConfig.
//
// Outputs:
//   - TreeBudgetConfig: Budget configuration for tree operations.
func (c BudgetConfig) ToTreeBudgetConfig() TreeBudgetConfig {
	return TreeBudgetConfig{
		MaxNodes:      c.MaxNodes,
		MaxDepth:      c.MaxDepth,
		MaxExpansions: c.MaxExpansions,
		TimeLimit:     c.TimeLimit,
		LLMTokenLimit: c.LLMTokenLimit,
		LLMCallLimit:  c.LLMCallLimit,
		CostLimitUSD:  c.CostLimitUSD,
	}
}

// ToSimulatorConfig converts SimulationFullConfig to SimulatorConfig.
//
// Outputs:
//   - SimulatorConfig: Simulator configuration.
func (c SimulationFullConfig) ToSimulatorConfig() SimulatorConfig {
	base := DefaultSimulatorConfig()
	base.QuickScoreThreshold = c.QuickScoreThreshold
	base.StandardScoreThreshold = c.StandardScoreThreshold
	base.QuickTimeout = c.QuickTimeout
	base.StandardTimeout = c.StandardTimeout
	base.FullTimeout = c.FullTimeout
	return base
}

// DefaultPruningConfig returns the default pruning configuration.
func DefaultPruningConfig() PruningConfig {
	return PruningConfig{
		PruneInterval:   10,
		MinVisitsToKeep: 2,
		MaxAbandonedAge: 5 * time.Minute,
		KeepBestN:       3,
		ScoreThreshold:  0.3,
		VisitsThreshold: 1,
	}
}
