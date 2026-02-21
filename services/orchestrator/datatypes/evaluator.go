// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package datatypes

import (
	"time"
)

// TickerInfo represents a ticker to evaluate
type TickerInfo struct {
	Ticker      string `json:"ticker"`
	Description string `json:"description"`
}

// EvaluationConfig configures the evaluation run
type EvaluationConfig struct {
	Tickers        []TickerInfo
	Models         []string
	EvaluationDate string // "20250117"
	RunID          string

	// Strategy configuration
	StrategyType   string
	StrategyParams map[string]interface{}

	// Forecast configuration
	ContextSize int
	HorizonSize int

	// Portfolio configuration
	InitialCapital  float64
	InitialPosition float64
	InitialCash     float64
}

// ForecastResult matches the JSON response from /v1/timeseries/forecast
type ForecastResult struct {
	Name     string    `json:"name"`
	Forecast []float64 `json:"forecast"`
	Message  string    `json:"message"`
}

// TradingSignalResponse is the response from the trading service
type TradingSignalResponse struct {
	Action        string  `json:"action"`         // "buy", "sell", or "hold"
	Size          float64 `json:"size"`           // Position size to execute
	Value         float64 `json:"value"`          // Dollar value of trade
	Reason        string  `json:"reason"`         // Explanation
	AvailableCash float64 `json:"available_cash"` // Cash after trade
	PositionAfter float64 `json:"position_after"` // Position after trade
	Stopped       bool    `json:"stopped"`        // Strategy stopped flag
	CurrentPrice  float64 `json:"current_price"`  // Current price used
	ForecastPrice float64 `json:"forecast_price"` // Forecast price used
	Ticker        string  `json:"ticker"`         // Ticker symbol
}

// MetricsResponse contains computed performance metrics from the metrics service.
// These metrics are calculated from the portfolio's return series after a backtest.
type MetricsResponse struct {
	SharpeRatio float64 `json:"sharpe_ratio"` // Risk-adjusted return (annualized)
	MaxDrawdown float64 `json:"max_drawdown"` // Maximum peak-to-trough decline (negative value)
	CAGR        float64 `json:"cagr"`         // Compound Annual Growth Rate
	CalmarRatio float64 `json:"calmar_ratio"` // CAGR / |MaxDrawdown|
	WinRate     float64 `json:"win_rate"`     // Percentage of positive return periods (0-1)
}

// EvaluationResult is the final data point stored in InfluxDB.
//
// Description:
//
//	EvaluationResult contains all information about a single evaluation point,
//	including forecast data, trading signal results, and inference metadata.
//	This struct is persisted to InfluxDB for analysis and reporting.
//
// Fields:
//   - Core fields: Ticker, Model, EvaluationDate, RunID, ForecastHorizon, StrategyType
//   - Forecast fields: ForecastPrice, CurrentPrice
//   - Trading fields: Action, Size, Value, Reason, AvailableCash, PositionAfter, Stopped
//   - Strategy params: ThresholdValue, ExecutionSize
//   - Metadata fields: RequestID, ResponseID, InferenceTimeMs, Device, ModelFamily
//
// Limitations:
//   - Metadata fields are only populated when using unified compute mode
//   - Legacy mode leaves metadata fields empty/zero
//
// Assumptions:
//   - Timestamp is set by the caller at evaluation time
//   - RequestID and ResponseID are UUIDs when populated
type EvaluationResult struct {
	Ticker          string
	Model           string
	EvaluationDate  string
	RunID           string
	ForecastHorizon int
	StrategyType    string

	ForecastPrice  float64
	CurrentPrice   float64
	Action         string
	Size           float64
	Value          float64
	Reason         string
	AvailableCash  float64
	PositionAfter  float64
	Stopped        bool
	ThresholdValue float64
	ExecutionSize  float64

	Timestamp time.Time

	// Inference metadata (populated only in unified compute mode)
	RequestID       string // Request tracing ID (empty in legacy mode)
	ResponseID      string // Response tracing ID (empty in legacy mode)
	InferenceTimeMs int    // Model inference time in milliseconds (0 in legacy mode)
	Device          string // Compute device: "cpu", "cuda:0", "mps" (empty in legacy mode)
	ModelFamily     string // Model family: "chronos", "timesfm", etc. (empty in legacy mode)
}

// ForecastRecord represents a forecast to persist in the "forecasts" InfluxDB measurement.
// Stores the full forecast context for auditability and multi-horizon analysis.
type ForecastRecord struct {
	Ticker          string
	Model           string
	ModelFamily     string
	ForecastPrice   float64
	CurrentPrice    float64
	Horizon         int
	InferenceTimeMs int
	ForecastDate    string // ISO date string (YYYYMMDD)
	ConfidenceLower float64
	ConfidenceUpper float64
	Timestamp       time.Time // Evaluation date (used as InfluxDB point timestamp)
}

// TradingSignalRecord represents a trading signal to persist in the "trading_signals" measurement.
// Captures both before and after state for each trade decision.
type TradingSignalRecord struct {
	Ticker         string
	StrategyType   string
	Action         string
	ForecastPrice  float64
	CurrentPrice   float64
	PositionBefore float64
	PositionAfter  float64
	TradeSize      float64
	TradeValue     float64
	CashBefore     float64
	CashAfter      float64
	Reason         string
	SignalDate     string    // ISO date string (YYYYMMDD)
	Timestamp      time.Time // Evaluation date (used as InfluxDB point timestamp)
}

// PortfolioStateRecord represents a portfolio snapshot to persist in the "portfolio_state" measurement.
// Pre-computed equity curve point with risk metrics for visualization.
type PortfolioStateRecord struct {
	Ticker           string
	PortfolioValue   float64
	Cash             float64
	Position         float64
	PositionValue    float64
	CurrentPrice     float64
	CumulativeReturn float64
	Drawdown         float64
	StepIndex        int
	SnapshotDate     string    // ISO date string (YYYYMMDD)
	Timestamp        time.Time // Evaluation date (used as InfluxDB point timestamp)
}

// ScenarioMetadata tracks the identity of the strategy being tested
type ScenarioMetadata struct {
	ID          string `yaml:"id" json:"id"`
	Version     string `yaml:"version" json:"version"`
	Description string `yaml:"description" json:"description"`
	Author      string `yaml:"author" json:"author"`
	Created     string `yaml:"created" json:"created"`
}

// BacktestScenario represents the full configuration file
type BacktestScenario struct {
	Metadata ScenarioMetadata `yaml:"metadata" json:"metadata"`

	Evaluation struct {
		Ticker         string `yaml:"ticker" json:"ticker"`
		FetchStartDate string `yaml:"fetch_start_date" json:"fetch_start_date"`
		StartDate      string `yaml:"start_date" json:"start_date"` //YYYYMMDD
		EndDate        string `yaml:"end_date" json:"end_date"`     //YYYYMMDD
	} `yaml:"evaluation" json:"evaluation"`

	Forecast struct {
		Model       string    `yaml:"model" json:"model"`
		ContextSize int       `yaml:"context_size" json:"context_size"`
		HorizonSize int       `yaml:"horizon_size" json:"horizon_size"`
		ComputeMode string    `yaml:"compute_mode" json:"compute_mode"` // "legacy" (default) or "unified"
		Quantiles   []float64 `yaml:"quantiles" json:"quantiles"`       // Optional quantiles (e.g., [0.1, 0.5, 0.9])
	} `yaml:"forecast" json:"forecast"`

	Trading struct {
		InitialCapital  float64                `yaml:"initial_capital" json:"initial_capital"`
		InitialPosition float64                `yaml:"initial_position" json:"initial_position"`
		InitialCash     float64                `yaml:"initial_cash" json:"initial_cash"`
		StrategyType    string                 `yaml:"strategy_type" json:"strategy_type"`
		Params          map[string]interface{} `yaml:"params" json:"params"`
	} `yaml:"trading" json:"trading"`
}

// DataCoverageInfo holds information about available data for a ticker in InfluxDB
type DataCoverageInfo struct {
	Ticker     string
	OldestDate time.Time
	NewestDate time.Time
	PointCount int
	HasData    bool
}
