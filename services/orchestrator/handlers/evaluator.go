// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/pkg/validation"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

// Retry configuration
const (
	maxRetries     = 3
	baseRetryDelay = 500 * time.Millisecond
	maxRetryDelay  = 10 * time.Second
)

// schemaVersion is the InfluxDB measurement schema version tag for migration support.
const schemaVersion = "1"

// forecastServiceRequest is the typed request for the legacy forecast service.
// This replaces map[string]interface{} to avoid runtime type errors.
type forecastServiceRequest struct {
	Name               string    `json:"name"`
	ContextPeriodSize  int       `json:"context_period_size"`
	ForecastPeriodSize int       `json:"forecast_period_size"`
	Model              string    `json:"model"`
	AsOfDate           string    `json:"as_of_date,omitempty"`
	RecentData         []float64 `json:"recent_data,omitempty"`
}

// dataFetchRequest is the typed request for the data fetcher service.
type dataFetchRequest struct {
	Names     []string `json:"names"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	Interval  string   `json:"interval"`
}

// metricsRequest is the request payload for the metrics service.
type metricsRequest struct {
	Returns        []float64 `json:"returns"`
	Metric         string    `json:"metric"`
	RiskFreeRate   float64   `json:"risk_free_rate"`
	PeriodsPerYear int       `json:"periods_per_year"`
}

// Evaluator handles the logic of running forecasts and checking trading signals
type Evaluator struct {
	httpClient        *http.Client
	orchestratorURL   string
	tradingServiceURL string
	metricsServiceURL string
	storage           *InfluxDBStorage
}

// NewEvaluator creates a new evaluator instance.
// Note: We default to localhost ports because this is usually run from the CLI on the Host.
func NewEvaluator() (*Evaluator, error) {
	orchestratorURL := os.Getenv("ORCHESTRATOR_URL")
	if orchestratorURL == "" {
		orchestratorURL = "http://localhost:12210"
	}

	tradingURL := os.Getenv("SAPHENEIA_TRADING_SERVICE_URL")
	if tradingURL == "" {
		// Default to the external port mapped in podman-compose (12132->9000)
		tradingURL = "http://localhost:12132"
	}

	metricsURL := os.Getenv("SAPHENEIA_METRICS_SERVICE_URL")
	if metricsURL == "" {
		// Default to the external port mapped in podman-compose (12702->8000)
		metricsURL = "http://localhost:12702"
	}

	storage, err := NewInfluxDBStorage()
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	return &Evaluator{
		httpClient:        &http.Client{Timeout: 5 * time.Minute},
		orchestratorURL:   orchestratorURL,
		tradingServiceURL: tradingURL,
		metricsServiceURL: metricsURL,
		storage:           storage,
	}, nil
}

// forecastJob represents a single forecast task in the worker pool.
//
// Description:
//
//	forecastJob encapsulates all data needed to generate a forecast for a
//	specific date in the backtest. Jobs are sent to worker goroutines for
//	parallel processing.
//
// Fields:
//   - index: Position in the full history array
//   - date: The evaluation date for this forecast
//   - price: The actual price on this date (for comparison)
//   - contextData: Historical values to use as model context
//
// Limitations:
//   - contextData must be populated before sending to workers
//
// Assumptions:
//   - index corresponds to a valid position in fullHistory
type forecastJob struct {
	index       int
	date        time.Time
	price       float64
	contextData []float64
}

// forecastResult holds the output from a forecast worker.
//
// Description:
//
//	forecastResult captures the result of processing a forecastJob, including
//	the forecast output or any error that occurred. Results are collected
//	and processed sequentially after all workers complete.
//
// Fields:
//   - index: Matches the job index for ordering
//   - date: The evaluation date
//   - price: The actual price on this date
//   - output: The forecast output (unified structure for both APIs)
//   - err: Non-nil if the forecast failed
//
// Assumptions:
//   - Either output is populated OR err is non-nil, never both
type forecastResult struct {
	index  int
	date   time.Time
	price  float64
	output ForecastOutput
	err    error
}

// ForecastOutput normalizes results from both legacy and unified APIs.
//
// Description:
//
//	ForecastOutput provides a common structure for processing forecast results
//	regardless of which API was used to generate them. This enables the trading
//	loop to remain unchanged while supporting both compute modes.
//
// Fields:
//   - Values: The forecast values (required, always populated)
//   - RequestID: Request tracing ID (empty for legacy API)
//   - ResponseID: Response tracing ID (empty for legacy API)
//   - Metadata: Execution metadata (nil for legacy API)
//   - Quantiles: Optional quantile forecasts (nil for legacy API or if not requested)
//
// Example:
//
//	// From legacy API
//	output := ForecastOutput{Values: legacyResult.Forecast}
//
//	// From unified API
//	output := ForecastOutput{
//	    Values:     unifiedResult.Forecast.Values,
//	    RequestID:  unifiedResult.RequestID,
//	    ResponseID: unifiedResult.ResponseID,
//	    Metadata:   &unifiedResult.Metadata,
//	    Quantiles:  unifiedResult.Quantiles,
//	}
//
// Limitations:
//   - Quantiles are not currently used in trading logic
//
// Assumptions:
//   - Values is never nil after successful API call
type ForecastOutput struct {
	Values     []float64
	RequestID  string
	ResponseID string
	Metadata   *datatypes.InferenceMetadata
	Quantiles  []datatypes.QuantileForecast
}

// buildInferenceRequestFromJob converts a forecastJob to an InferenceRequest.
//
// Description:
//
//	buildInferenceRequestFromJob constructs a fully-populated InferenceRequest
//	from the job data and scenario configuration. It generates a unique
//	RequestID for tracing and populates all required metadata fields.
//
// Inputs:
//   - job: The forecast job containing date, price, and context slice
//   - scenario: The backtest scenario with model and ticker info
//   - runID: The current run ID (used as prefix for request ID)
//
// Outputs:
//   - *datatypes.InferenceRequest: Ready to send to CallInferenceService
//
// Example:
//
//	req := buildInferenceRequestFromJob(job, scenario, "run-2026-01-20")
//	resp, err := e.CallInferenceService(ctx, req)
//
// Limitations:
//   - Assumes daily period (Period1d)
//   - Assumes InfluxDB source
//   - Assumes close price field
//
// Assumptions:
//   - job.contextData is non-empty
//   - scenario.Forecast.Model contains "/" (validated elsewhere)
//   - runID is non-empty
func buildInferenceRequestFromJob(
	job forecastJob,
	scenario *datatypes.BacktestScenario,
	runID string,
) *datatypes.InferenceRequest {
	// Generate unique request ID: {runID}-{ticker}-{dateStr}-{random}
	requestID := fmt.Sprintf("%s-%s-%s-%d",
		runID,
		scenario.Evaluation.Ticker,
		job.date.Format("20060102"),
		time.Now().UnixNano()%1000000)

	// Calculate context date range
	contextDays := len(job.contextData)
	startDate := job.date.AddDate(0, 0, -(contextDays - 1))

	// Build optional params (only if quantiles specified)
	var params *datatypes.ModelParams
	if len(scenario.Forecast.Quantiles) > 0 {
		params = &datatypes.ModelParams{
			Quantiles: scenario.Forecast.Quantiles,
		}
	}

	return &datatypes.InferenceRequest{
		RequestID: requestID,
		Timestamp: time.Now().UTC(),
		Ticker:    scenario.Evaluation.Ticker,
		Model:     scenario.Forecast.Model,
		Context: datatypes.ContextData{
			Values:    job.contextData,
			Period:    datatypes.Period1d,
			Source:    datatypes.SourceInfluxDB,
			StartDate: startDate.Format("2006-01-02"),
			EndDate:   job.date.Format("2006-01-02"),
			Field:     datatypes.FieldClose,
		},
		Horizon: datatypes.HorizonSpec{
			Length: scenario.Forecast.HorizonSize,
			Period: datatypes.Period1d,
		},
		Params: params,
	}
}

// RunScenario executes a backtest based on the YAML scenario file.
// Returns computed metrics on success, or nil metrics with error on failure.
func (e *Evaluator) RunScenario(ctx context.Context, scenario *datatypes.BacktestScenario, runID string) (*datatypes.MetricsResponse, error) {
	ticker := scenario.Evaluation.Ticker
	slog.Info("Starting backtest loop", "run_id", runID, "ticker", ticker)

	// Precheck and autofill data
	adjustedFetchStart, err := e.EnsureDataAvailability(ctx, scenario)
	if err != nil {
		return nil, fmt.Errorf("data availability check failed: %w", err)
	}
	fetchStartDate := adjustedFetchStart

	// Parse end_date (use from config or default to now)
	var fetchEndDate time.Time
	endDateStr := scenario.Evaluation.EndDate
	if endDateStr == "" {
		fetchEndDate = time.Now()
	} else {
		endLayout := "2006-01-02"
		if len(endDateStr) == 8 {
			endLayout = "20060102"
		}
		fetchEndDate, err = time.Parse(endLayout, endDateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid end_date format: %w", err)
		}
	}

	slog.Info("Fetching data from absolute date range", "fetch_start", fetchStartDate.Format("2006-01-02"), "fetch_end", fetchEndDate.Format("2006-01-02"))

	// Fetch OHLC data using absolute date range
	fullHistory, _, err := fetchOHLCFromInfluxByDateRange(ctx, ticker, fetchStartDate, fetchEndDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch history: %w", err)
	}
	if len(fullHistory.Close) == 0 {
		return nil, fmt.Errorf("no historical data found for %s", ticker)
	}

	slog.Info("Data fetch complete",
		"total_points", len(fullHistory.Close),
		"date_range", fmt.Sprintf("%s to %s", fullHistory.Time[0].Format("2006-01-02"), fullHistory.Time[len(fullHistory.Time)-1].Format("2006-01-02")))

	// 2. Determine Start Index
	// (Parse EvaluationStartDate)
	evalStartLayout := "2006-01-02"
	if len(scenario.Evaluation.StartDate) == 8 {
		evalStartLayout = "20060102"
	}
	evalStart, err := time.Parse(evalStartLayout, scenario.Evaluation.StartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format: %w", err)
	}

	startIndex := -1
	for i, t := range fullHistory.Time {
		// Compare dates (ignoring time)
		if !t.Before(evalStart) {
			startIndex = i
			break
		}
	}

	if startIndex == -1 {
		return nil, fmt.Errorf("start date %s not found in loaded history (oldest data: %s, latest data: %s)",
			scenario.Evaluation.StartDate, fullHistory.Time[0].Format("2006-01-02"), fullHistory.Time[len(fullHistory.Time)-1].Format("2006-01-02"))
	}

	// Ensure we have enough context *before* the start index
	if startIndex < scenario.Forecast.ContextSize {
		return nil, fmt.Errorf("insufficient history before start date. Need %d days context, but only have %d days available (start_date=%s is at index %d, oldest_data=%s)",
			scenario.Forecast.ContextSize, startIndex, scenario.Evaluation.StartDate, startIndex, fullHistory.Time[0].Format("2006-01-02"))
	}

	slog.Info("Evaluation date range validated",
		"start_date", scenario.Evaluation.StartDate,
		"start_index", startIndex,
		"context_days_available", startIndex,
		"context_days_required", scenario.Forecast.ContextSize)

	endIndex := len(fullHistory.Close) - 1
	totalDays := endIndex - startIndex + 1
	slog.Info("Backtest range found", "start_date", fullHistory.Time[startIndex], "start_index", startIndex, "days_to_test", totalDays)

	// 3. Pre-fetch all forecasts in parallel
	// (forecastJob and forecastResult types defined at package level)

	// Log compute mode
	computeMode := scenario.Forecast.ComputeMode
	if computeMode == "" {
		computeMode = "legacy"
	}
	slog.Info("Compute mode configured", "mode", computeMode)

	// Build job list
	jobs := make([]forecastJob, 0, totalDays)
	for i := startIndex; i <= endIndex; i++ {
		sliceStart := i - scenario.Forecast.ContextSize + 1
		if sliceStart < 0 {
			sliceStart = 0
		}
		contextSlice := make([]float64, i+1-sliceStart)
		copy(contextSlice, fullHistory.Close[sliceStart:i+1])

		jobs = append(jobs, forecastJob{
			index:       i,
			date:        fullHistory.Time[i],
			price:       fullHistory.Close[i],
			contextData: contextSlice,
		})
	}

	// Parallel forecast fetching with worker pool
	numWorkers := 4 // Configurable concurrency
	if envWorkers := os.Getenv("ALEUTIAN_FORECAST_WORKERS"); envWorkers != "" {
		if n, err := fmt.Sscanf(envWorkers, "%d", &numWorkers); n == 1 && err == nil && numWorkers > 0 {
			// Use env value
		}
	}

	slog.Info("Fetching forecasts in parallel", "total_days", totalDays, "workers", numWorkers)
	startTime := time.Now()

	jobChan := make(chan forecastJob, len(jobs))
	resultChan := make(chan forecastResult, len(jobs))

	// Start workers
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				var output ForecastOutput
				var err error

				if computeMode == "unified" {
					// Use new unified API
					req := buildInferenceRequestFromJob(job, scenario, runID)

					// Log request (comprehensive logging)
					slog.Info("Sending inference request",
						"request_id", req.RequestID,
						"date", job.date.Format("2006-01-02"),
						"ticker", req.Ticker,
						"model", req.Model,
						"context_size", len(req.Context.Values),
						"horizon", req.Horizon.Length)

					resp, callErr := e.CallInferenceService(ctx, req)
					if callErr != nil {
						err = callErr
						slog.Error("Inference request failed",
							"request_id", req.RequestID,
							"error", callErr)
					} else {
						// Log response (comprehensive logging)
						slog.Info("Received inference response",
							"request_id", resp.RequestID,
							"response_id", resp.ResponseID,
							"inference_time_ms", resp.Metadata.InferenceTimeMs,
							"device", resp.Metadata.Device,
							"model_family", resp.Metadata.ModelFamily,
							"forecast_length", len(resp.Forecast.Values))

						output = ForecastOutput{
							Values:     resp.Forecast.Values,
							RequestID:  resp.RequestID,
							ResponseID: resp.ResponseID,
							Metadata:   &resp.Metadata,
							Quantiles:  resp.Quantiles,
						}
					}
				} else {
					// Use legacy API (default)
					result, callErr := e.CallForecastServiceAsOf(
						ctx, ticker, scenario.Forecast.Model,
						scenario.Forecast.ContextSize, scenario.Forecast.HorizonSize,
						&job.date, job.contextData)
					if callErr != nil {
						err = callErr
					} else {
						output = ForecastOutput{
							Values: result.Forecast,
						}
					}
				}

				resultChan <- forecastResult{
					index:  job.index,
					date:   job.date,
					price:  job.price,
					output: output,
					err:    err,
				}
			}
		}()
	}

	// Send jobs
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	// Wait for all workers and close results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results into a map
	forecasts := make(map[int]forecastResult)
	successCount := 0
	failCount := 0
	for result := range resultChan {
		forecasts[result.index] = result
		if result.err != nil {
			failCount++
		} else {
			successCount++
		}
	}

	fetchDuration := time.Since(startTime)
	slog.Info("Forecast fetching complete",
		"duration", fetchDuration,
		"success", successCount,
		"failed", failCount,
		"avg_per_forecast", fetchDuration/time.Duration(totalDays))

	// 4. Initialize Portfolio
	currentPosition := scenario.Trading.InitialPosition
	currentCash := scenario.Trading.InitialCash

	// Track portfolio values for metrics calculation
	// Pre-allocate slice with capacity for all iterations + initial value
	portfolioValues := make([]float64, 0, endIndex-startIndex+2)

	// Record initial portfolio value BEFORE trading begins
	initialValue := currentCash + currentPosition*fullHistory.Close[startIndex]
	portfolioValues = append(portfolioValues, initialValue)

	// Track peak portfolio value for drawdown calculation
	peakValue := initialValue

	// 5. Sequential trading loop using pre-fetched forecasts
	for i := startIndex; i <= endIndex; i++ {
		fr, ok := forecasts[i]
		if !ok {
			slog.Error("Missing forecast for index", "index", i)
			continue
		}

		currentSimulatedPrice := fr.price
		currentDate := fr.date

		if fr.err != nil {
			slog.Error("Forecast failed", "date", currentDate.Format("2006-01-02"), "error", fr.err)
			continue
		}

		// Use the first forecast value (1-day ahead prediction)
		var predictedPrice float64
		if len(fr.output.Values) > 0 {
			predictedPrice = fr.output.Values[0]
		} else {
			slog.Warn("Empty forecast returned, skipping", "date", currentDate.Format("2006-01-02"))
			continue
		}

		// --- Store Forecast ---
		forecastRecord := &datatypes.ForecastRecord{
			Ticker:        ticker,
			Model:         scenario.Forecast.Model,
			ForecastPrice: predictedPrice,
			CurrentPrice:  currentSimulatedPrice,
			Horizon:       scenario.Forecast.HorizonSize,
			ForecastDate:  currentDate.Format("20060102"),
			Timestamp:     currentDate,
		}
		if fr.output.Metadata != nil {
			forecastRecord.ModelFamily = fr.output.Metadata.ModelFamily
			forecastRecord.InferenceTimeMs = fr.output.Metadata.InferenceTimeMs
		}
		// Extract confidence bounds from quantiles if available
		for _, q := range fr.output.Quantiles {
			if q.Quantile <= 0.1 && len(q.Values) > 0 {
				forecastRecord.ConfidenceLower = q.Values[0]
			}
			if q.Quantile >= 0.9 && len(q.Values) > 0 {
				forecastRecord.ConfidenceUpper = q.Values[0]
			}
		}
		if err := e.storage.StoreForecast(ctx, runID, forecastRecord); err != nil {
			slog.Warn("Failed to store forecast", "error", err)
		}

		// --- Execute Trade ---
		tradingReq := datatypes.TradingSignalRequest{
			Ticker:          ticker,
			StrategyType:    scenario.Trading.StrategyType,
			ForecastPrice:   predictedPrice,
			CurrentPrice:    &currentSimulatedPrice,
			CurrentPosition: currentPosition,
			AvailableCash:   currentCash,
			InitialCapital:  scenario.Trading.InitialCapital,
			StrategyParams:  scenario.Trading.Params,
		}

		signal, err := e.CallTradingService(ctx, tradingReq)
		if err != nil {
			slog.Error("Trade signal failed", "date", currentDate, "error", err)
			continue
		}

		// --- Store Trading Signal (before state update to capture "before" values) ---
		if err := e.storage.StoreTradingSignal(ctx, runID, &datatypes.TradingSignalRecord{
			Ticker:         ticker,
			StrategyType:   scenario.Trading.StrategyType,
			Action:         signal.Action,
			ForecastPrice:  predictedPrice,
			CurrentPrice:   currentSimulatedPrice,
			PositionBefore: currentPosition,
			PositionAfter:  signal.PositionAfter,
			TradeSize:      signal.Size,
			TradeValue:     signal.Value,
			CashBefore:     currentCash,
			CashAfter:      signal.AvailableCash,
			Reason:         signal.Reason,
			SignalDate:     currentDate.Format("20060102"),
			Timestamp:      currentDate,
		}); err != nil {
			slog.Warn("Failed to store trading signal", "error", err)
		}

		// Update State
		currentPosition = signal.PositionAfter
		currentCash = signal.AvailableCash

		// Track portfolio value AFTER trade execution
		portfolioValue := currentCash + currentPosition*currentSimulatedPrice
		portfolioValues = append(portfolioValues, portfolioValue)

		// --- Store Portfolio State ---
		if portfolioValue > peakValue {
			peakValue = portfolioValue
		}
		var drawdown float64
		if peakValue > 0 {
			drawdown = (portfolioValue - peakValue) / peakValue
		}
		var cumulativeReturn float64
		if initialValue > 0 {
			cumulativeReturn = (portfolioValue - initialValue) / initialValue
		}
		if err := e.storage.StorePortfolioState(ctx, runID, &datatypes.PortfolioStateRecord{
			Ticker:           ticker,
			PortfolioValue:   portfolioValue,
			Cash:             currentCash,
			Position:         currentPosition,
			PositionValue:    currentPosition * currentSimulatedPrice,
			CurrentPrice:     currentSimulatedPrice,
			CumulativeReturn: cumulativeReturn,
			Drawdown:         drawdown,
			StepIndex:        i - startIndex,
			SnapshotDate:     currentDate.Format("20060102"),
			Timestamp:        currentDate,
		}); err != nil {
			slog.Warn("Failed to store portfolio state", "error", err)
		}

		// Store Result with metadata (populated only in unified mode)
		result := datatypes.EvaluationResult{
			Ticker:          ticker,
			Model:           scenario.Forecast.Model,
			EvaluationDate:  currentDate.Format("20060102"),
			RunID:           runID,
			ForecastHorizon: scenario.Forecast.HorizonSize,
			StrategyType:    scenario.Trading.StrategyType,
			ForecastPrice:   predictedPrice,
			CurrentPrice:    currentSimulatedPrice,
			Action:          signal.Action,
			Size:            signal.Size,
			Value:           signal.Value,
			Reason:          signal.Reason,
			AvailableCash:   signal.AvailableCash,
			PositionAfter:   signal.PositionAfter,
			Timestamp:       currentDate,
			// Inference metadata (from unified mode)
			RequestID:  fr.output.RequestID,
			ResponseID: fr.output.ResponseID,
		}

		// Populate metadata fields if available (unified mode only)
		if fr.output.Metadata != nil {
			result.InferenceTimeMs = fr.output.Metadata.InferenceTimeMs
			result.Device = fr.output.Metadata.Device
			result.ModelFamily = fr.output.Metadata.ModelFamily
		}

		if err := e.storage.StoreResult(ctx, &result); err != nil {
			slog.Error("Failed to store result", "error", err)
		}

		if i%20 == 0 {
			slog.Info("Progress", "date", currentDate.Format("20060102"), "cash", currentCash)
		}
	}

	// --- Compute and Store Performance Metrics ---
	slog.Info("Computing performance metrics", "portfolio_values", len(portfolioValues))

	returns := portfolioValuesToReturns(portfolioValues)

	metrics, err := e.CallMetricsService(ctx, returns, runID)
	if err != nil {
		// Non-blocking: log warning but don't fail the backtest
		slog.Warn("Metrics calculation failed", "error", err)
		// Return zero metrics instead of nil
		metrics = &datatypes.MetricsResponse{}
	} else {
		// Store metrics to InfluxDB
		if err := e.storage.StoreMetrics(ctx, runID, ticker, scenario.Forecast.Model, metrics); err != nil {
			slog.Error("Failed to store metrics to InfluxDB", "error", err)
			// Continue - metrics are still returned to caller
		}
	}

	return metrics, nil
}

func (e *Evaluator) EvaluateTickerModel(
	ctx context.Context,
	ticker string,
	model string,
	config *datatypes.EvaluationConfig,
	currentPrice float64,
) error {
	slog.Info("Evaluating", "ticker", ticker, "model", model)

	// 1. Generate forecast via Orchestrator
	forecast, err := e.CallForecastService(ctx, ticker, model, config.ContextSize, config.HorizonSize)
	if err != nil {
		return fmt.Errorf("forecast failed: %w", err)
	}

	if len(forecast.Forecast) == 0 {
		return fmt.Errorf("empty forecast received")
	}

	// 2. Track portfolio state
	currentPosition := config.InitialPosition
	availableCash := config.InitialCash

	// 3. For each forecast horizon (1 to N days ahead)
	for horizon := 1; horizon <= len(forecast.Forecast); horizon++ {
		forecastPrice := forecast.Forecast[horizon-1]

		// Build trading signal request
		tradingReq := datatypes.TradingSignalRequest{
			Ticker:          ticker,
			StrategyType:    config.StrategyType,
			ForecastPrice:   forecastPrice,
			CurrentPrice:    &currentPrice, // We use the initial price as the "current" for simulation
			CurrentPosition: currentPosition,
			AvailableCash:   availableCash,
			InitialCapital:  config.InitialCapital,
			StrategyParams:  config.StrategyParams,
		}

		// Generate trading signal via Sapheneia
		signal, err := e.CallTradingService(ctx, tradingReq)
		if err != nil {
			slog.Error("Trading signal failed", "horizon", horizon, "error", err)
			continue
		}

		// Update portfolio state for next iteration
		availableCash = signal.AvailableCash
		currentPosition = signal.PositionAfter

		// Prepare Result for Storage
		thresholdVal := 0.0
		executionSize := 0.0
		if v, ok := config.StrategyParams["threshold_value"].(float64); ok {
			thresholdVal = v
		}
		if v, ok := config.StrategyParams["execution_size"].(float64); ok {
			executionSize = v
		}

		result := datatypes.EvaluationResult{
			Ticker:          ticker,
			Model:           model,
			EvaluationDate:  config.EvaluationDate,
			RunID:           config.RunID,
			ForecastHorizon: horizon,
			StrategyType:    config.StrategyType,
			ForecastPrice:   forecastPrice,
			CurrentPrice:    currentPrice,
			Action:          signal.Action,
			Size:            signal.Size,
			Value:           signal.Value,
			Reason:          signal.Reason,
			AvailableCash:   signal.AvailableCash,
			PositionAfter:   signal.PositionAfter,
			Stopped:         signal.Stopped,
			ThresholdValue:  thresholdVal,
			ExecutionSize:   executionSize,
			Timestamp:       time.Now(),
		}

		if err := e.storage.StoreResult(ctx, &result); err != nil {
			slog.Error("Failed to store result", "error", err)
		}
	}
	return nil
}

// --- HTTP Helper Methods ---

// retryWithBackoff executes a function with exponential backoff retry logic
func retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled: %w", err)
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt < maxRetries-1 {
			// Calculate delay with jitter
			delay := baseRetryDelay * time.Duration(1<<uint(attempt))
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			// Add jitter (±25%)
			jitter := time.Duration(rand.Int63n(int64(delay / 2)))
			delay = delay + jitter - (delay / 4)

			slog.Warn("Retrying operation",
				"operation", operation,
				"attempt", attempt+1,
				"max_attempts", maxRetries,
				"delay", delay,
				"error", lastErr)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("operation %s failed after %d attempts: %w", operation, maxRetries, lastErr)
}

// portfolioValuesToReturns converts a series of portfolio values to period returns.
// Returns are capped to [-1.0, 10.0] to handle extreme values.
func portfolioValuesToReturns(values []float64) []float64 {
	if len(values) < 2 {
		return []float64{}
	}

	returns := make([]float64, 0, len(values)-1)
	for i := 1; i < len(values); i++ {
		if values[i-1] <= 0 {
			returns = append(returns, 0.0)
			continue
		}
		ret := (values[i] - values[i-1]) / values[i-1]
		// Cap extreme returns (same bounds as Python MetricsClient)
		if ret < -1.0 {
			ret = -1.0
		} else if ret > 10.0 {
			ret = 10.0
		}
		returns = append(returns, ret)
	}
	return returns
}

// CallMetricsService sends portfolio returns to the metrics service and returns computed metrics.
// Uses the existing retryWithBackoff helper for consistency.
// This is a non-blocking operation - errors are logged but don't fail the backtest.
func (e *Evaluator) CallMetricsService(ctx context.Context, returns []float64, runID string) (*datatypes.MetricsResponse, error) {
	if len(returns) < 2 {
		slog.Warn("Insufficient returns data for metrics", "count", len(returns))
		return &datatypes.MetricsResponse{}, nil
	}

	url := fmt.Sprintf("%s/metrics/v1/compute/", e.metricsServiceURL)

	reqBody := metricsRequest{
		Returns:        returns,
		Metric:         "all",
		RiskFreeRate:   0.0,
		PeriodsPerYear: 252,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metrics request: %w", err)
	}

	var result datatypes.MetricsResponse

	err = retryWithBackoff(ctx, "metrics_service", func() error {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
		if err != nil {
			return fmt.Errorf("failed to create metrics request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Run-ID", runID)

		resp, err := e.httpClient.Do(httpReq)
		if err != nil {
			return err
		}

		// Read and close body immediately to avoid leaks on retry
		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			slog.Debug("Failed to close response body", "error", closeErr)
		}

		if readErr != nil {
			return fmt.Errorf("failed to read response: %w", readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("metrics service returned %d: %s", resp.StatusCode, string(respBody))
		}

		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("failed to decode metrics response: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	slog.Info("Metrics computed successfully",
		"sharpe_ratio", fmt.Sprintf("%.3f", result.SharpeRatio),
		"max_drawdown", fmt.Sprintf("%.2f%%", result.MaxDrawdown*100),
		"cagr", fmt.Sprintf("%.2f%%", result.CAGR*100),
		"win_rate", fmt.Sprintf("%.1f%%", result.WinRate*100))

	return &result, nil
}

func (e *Evaluator) CallForecastService(ctx context.Context, ticker, model string, contextSize, horizonSize int) (*datatypes.ForecastResult, error) {
	// Pass nil for asOfDate (implies "now")
	// Pass nil for contextData (implies "fetch from DB")
	return e.CallForecastServiceAsOf(ctx, ticker, model, contextSize, horizonSize, nil, nil)
}

func (e *Evaluator) CallForecastServiceAsOf(
	ctx context.Context,
	ticker, model string,
	contextSize, horizonSize int,
	asOfDate *time.Time,
	contextData []float64,
) (*datatypes.ForecastResult, error) {
	var result *datatypes.ForecastResult

	err := retryWithBackoff(ctx, "forecast", func() error {
		url := fmt.Sprintf("%s/v1/timeseries/forecast", e.orchestratorURL)

		payload := forecastServiceRequest{
			Name:               ticker,
			ContextPeriodSize:  contextSize,
			ForecastPeriodSize: horizonSize,
			Model:              model,
		}

		// Add as_of_date for metadata/logging
		if asOfDate != nil {
			payload.AsOfDate = asOfDate.Format("2006-01-02")
		}

		// Add the explicit historical data
		if len(contextData) > 0 {
			payload.RecentData = contextData
		}

		reqBody, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal forecast request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("Failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				body = []byte("(failed to read body)")
			}
			// Don't retry on 4xx errors (client errors)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return fmt.Errorf("forecast error status %d: %s (not retryable)", resp.StatusCode, string(body))
			}
			return fmt.Errorf("forecast error status %d: %s", resp.StatusCode, string(body))
		}

		result = &datatypes.ForecastResult{}
		return json.NewDecoder(resp.Body).Decode(result)
	})

	return result, err
}

// CallInferenceService sends a request to Sapheneia's unified predict endpoint.
//
// Description:
//
//	CallInferenceService constructs and sends an InferenceRequest to the
//	Sapheneia orchestration gateway's /orchestration/v1/predict endpoint.
//	It handles retries with exponential backoff for transient failures.
//	This is the new unified API that provides request/response tracing
//	and richer metadata compared to the legacy CallForecastService.
//
// Inputs:
//   - ctx: Context for cancellation and timeout (required)
//   - req: The inference request to send (required, must pass Validate())
//
// Outputs:
//   - *datatypes.InferenceResponse: The forecast response on success
//   - error: Non-nil on failure (validation, network, HTTP error, or invalid response)
//
// Example:
//
//	req := &datatypes.InferenceRequest{
//	    RequestID: uuid.New().String(),
//	    Timestamp: time.Now().UTC(),
//	    Ticker:    "SPY",
//	    Model:     "amazon/chronos-t5-tiny",
//	    Context: datatypes.ContextData{
//	        Values:    closeHistory,
//	        Period:    datatypes.Period1d,
//	        Source:    datatypes.SourceInfluxDB,
//	        StartDate: "2025-01-01",
//	        EndDate:   "2025-12-31",
//	        Field:     datatypes.FieldClose,
//	    },
//	    Horizon: datatypes.HorizonSpec{
//	        Length: 10,
//	        Period: datatypes.Period1d,
//	    },
//	}
//
//	resp, err := evaluator.CallInferenceService(ctx, req)
//	if err != nil {
//	    log.Fatalf("Inference failed: %v", err)
//	}
//	fmt.Printf("Forecast: %v\n", resp.Forecast.Values)
//
// Limitations:
//   - Timeout is inherited from the http.Client (5 minutes)
//   - Does not retry on 4xx errors (considered non-retryable)
//   - Requires Sapheneia /orchestration/v1/predict endpoint to be available
//
// Assumptions:
//   - Sapheneia gateway is running at e.orchestratorURL or SAPHENEIA_GATEWAY_URL
//   - SAPHENEIA_API_KEY env var is set for authentication (optional)
//   - Request passes Validate() before network call
func (e *Evaluator) CallInferenceService(
	ctx context.Context,
	req *datatypes.InferenceRequest,
) (*datatypes.InferenceResponse, error) {
	// Validate request before making network call
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid inference request: %w", err)
	}

	var result *datatypes.InferenceResponse

	err := retryWithBackoff(ctx, "inference", func() error {
		url := buildInferenceURL(e.orchestratorURL)

		reqBody, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		// Add API key if configured
		apiKey := os.Getenv("SAPHENEIA_API_KEY")
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := e.httpClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("Failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				body = []byte("(failed to read body)")
			}
			// Don't retry on 4xx errors (client errors)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return fmt.Errorf("inference error status %d: %s (not retryable)", resp.StatusCode, string(body))
			}
			return fmt.Errorf("inference error status %d: %s", resp.StatusCode, string(body))
		}

		result = &datatypes.InferenceResponse{}
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})

	return result, err
}

// buildInferenceURL constructs the endpoint URL for the unified inference API.
//
// Description:
//
//	buildInferenceURL is an internal helper that constructs the full URL
//	for the inference endpoint from the base orchestrator URL.
//
// Inputs:
//   - baseURL: The orchestrator base URL (e.g., "http://localhost:12700")
//
// Outputs:
//   - string: The full endpoint URL
//
// Example:
//
//	url := buildInferenceURL("http://localhost:12700")
//	// Returns: "http://localhost:12700/orchestration/v1/predict"
//
// Limitations:
//   - Does not validate the URL format
//
// Assumptions:
//   - baseURL does not have a trailing slash
func buildInferenceURL(baseURL string) string {
	return baseURL + "/orchestration/v1/predict"
}

func (e *Evaluator) CallTradingService(ctx context.Context, req datatypes.TradingSignalRequest) (*datatypes.TradingSignalResponse, error) {
	url := fmt.Sprintf("%s/trading/execute", e.tradingServiceURL)
	flatReq := map[string]interface{}{
		"ticker":           req.Ticker,
		"strategy_type":    req.StrategyType,
		"forecast_price":   req.ForecastPrice,
		"current_price":    req.CurrentPrice,
		"current_position": req.CurrentPosition,
		"available_cash":   req.AvailableCash,
		"initial_capital":  req.InitialCapital,
	}
	for key, value := range req.StrategyParams {
		flatReq[key] = value
	}
	reqBody, err := json.Marshal(flatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trading request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create trading request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	apiKey := os.Getenv("SAPHENEIA_TRADING_API_KEY")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("Failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			b = []byte("(failed to read body)")
		}
		return nil, fmt.Errorf("trading error: %s", string(b))
	}

	var result datatypes.TradingSignalResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	return &result, err
}

// CallTradingServiceV2 sends a V2 trading signal request with inference tracing.
//
// Description:
//
//	CallTradingServiceV2 accepts the new V2 request format which includes
//	inference traceability (request/response IDs linking to the forecast).
//	Since Sapheneia's trading service does not yet support V2 format natively,
//	this method converts the V2 request to V1 format before making the call.
//
//	The V2 format provides:
//	  - InferenceRef: Links to the forecast that informed this decision
//	  - Structured PriceInfo: Current and forecast prices with metadata
//	  - Structured PortfolioState: Position, cash, and initial capital
//	  - RequestID and Timestamp: For logging and audit trails
//
// Inputs:
//   - ctx: Context for cancellation and timeout (required)
//   - req: V2 trading signal request (required, must pass Validate())
//
// Outputs:
//   - *datatypes.TradingSignalResponse: Trading decision (buy/sell/hold)
//   - error: Non-nil on validation failure or service error
//
// Example:
//
//	// After receiving inference response
//	v2Req := datatypes.NewTradingSignalRequestV2(
//	    "SPY", "threshold",
//	    datatypes.PriceInfo{Current: 450.0, Forecast: 455.0, Period: datatypes.Period1d},
//	    datatypes.PortfolioState{Position: 0, Cash: 100000, InitialCapital: 100000},
//	    map[string]interface{}{"threshold_value": 0.02},
//	)
//	v2Req.InferenceRef = datatypes.InferenceRef{
//	    RequestID:  inferenceReq.RequestID,
//	    ResponseID: inferenceResp.ResponseID,
//	}
//
//	signal, err := evaluator.CallTradingServiceV2(ctx, v2Req)
//	if err != nil {
//	    log.Fatalf("Trading failed: %v", err)
//	}
//	fmt.Printf("Action: %s, Size: %f\n", signal.Action, signal.Size)
//
// Limitations:
//   - Currently converts to V1 format for compatibility with Sapheneia
//   - InferenceRef is logged but not sent to Sapheneia (V2 support planned)
//   - Will be updated to use native V2 when Sapheneia adds support
//
// Assumptions:
//   - Sapheneia trading service is available at e.tradingServiceURL
//   - V1 format is sufficient for trading logic (V2 adds tracing, not functionality)
//   - InferenceRef contains valid IDs from a prior inference call
func (e *Evaluator) CallTradingServiceV2(
	ctx context.Context,
	req *datatypes.TradingSignalRequestV2,
) (*datatypes.TradingSignalResponse, error) {
	// Validate V2 request before processing
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid V2 trading request: %w", err)
	}

	// Log V2 request with tracing info for audit trail
	slog.Info("Processing V2 trading request",
		"request_id", req.RequestID,
		"ticker", req.Ticker,
		"strategy_type", req.StrategyType,
		"current_price", req.Prices.Current,
		"forecast_price", req.Prices.Forecast,
		"inference_linked", req.InferenceRef.IsSet())

	if req.InferenceRef.IsSet() {
		slog.Debug("Trading request linked to inference",
			"trading_request_id", req.RequestID,
			"inference_request_id", req.InferenceRef.RequestID,
			"inference_response_id", req.InferenceRef.ResponseID)
	}

	// Convert V2 to V1 for Sapheneia compatibility
	// TODO: Remove this conversion when Sapheneia adds native V2 support
	v1Req := req.ToV1()

	// Call the existing V1 trading service
	response, err := e.CallTradingService(ctx, v1Req)
	if err != nil {
		slog.Error("V2 trading request failed",
			"request_id", req.RequestID,
			"error", err)
		return nil, err
	}

	// Log successful response
	slog.Info("V2 trading request completed",
		"request_id", req.RequestID,
		"action", response.Action,
		"size", response.Size,
		"position_after", response.PositionAfter)

	return response, nil
}

func (e *Evaluator) GetCurrentPrice(ctx context.Context, ticker string) (float64, error) {
	// Validate ticker to prevent Flux injection
	if err := validation.ValidateTicker(ticker); err != nil {
		return 0, fmt.Errorf("invalid ticker: %w", err)
	}

	// Simple Influx query to get the last known close price
	query := fmt.Sprintf(`
		from(bucket: "%s")
		  |> range(start: -7d)
		  |> filter(fn: (r) => r._measurement == "stock_prices")
		  |> filter(fn: (r) => r.ticker == "%s")
		  |> filter(fn: (r) => r._field == "close")
		  |> last()
	`, e.storage.bucket, ticker)

	// Note: We use the storage client's QueryAPI
	queryAPI := e.storage.client.QueryAPI(e.storage.org)
	result, err := queryAPI.Query(ctx, query)
	if err != nil {
		return 0, err
	}
	if result.Next() {
		if val, ok := result.Record().Value().(float64); ok {
			return val, nil
		}
	}
	return 0, fmt.Errorf("no price data found for %s", ticker)
}

func (e *Evaluator) Close() error {
	e.storage.Close()
	return nil
}

func (e *Evaluator) CheckDataCoverage(ctx context.Context,
	ticker string) (*datatypes.DataCoverageInfo, error) {

	// Validate ticker to prevent Flux injection
	if err := validation.ValidateTicker(ticker); err != nil {
		return nil, fmt.Errorf("invalid ticker: %w", err)
	}

	query := fmt.Sprintf(`
		from(bucket: "%s")
            |> range(start: -20y)
            |> filter(fn: (r) => r._measurement == "stock_prices")
            |> filter(fn: (r) => r.ticker == "%s")
            |> filter(fn: (r) => r._field == "close")
            |> group()
            |> reduce(
                identity: {count: 0, first: time(v: "2100-01-01T00:00:00Z"), last: time(v: "1900-01-01T00:00:00Z")},
                fn: (r, accumulator) => ({
                  count: accumulator.count + 1,
                  first: if r._time < accumulator.first then r._time else accumulator.first,
                  last: if r._time > accumulator.last then r._time else accumulator.last
                })
            )
	`, e.storage.bucket, ticker)

	queryAPI := e.storage.client.QueryAPI(e.storage.org)
	result, err := queryAPI.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query data coverage: %w", err)
	}
	info := &datatypes.DataCoverageInfo{Ticker: ticker, HasData: false}
	if result.Next() {
		record := result.Record()
		if count, ok := record.ValueByKey("count").(int64); ok && count > 0 {
			info.PointCount = int(count)
			info.HasData = true
			if first, ok := record.ValueByKey("first").(time.Time); ok {
				info.OldestDate = first
			}
			if last, ok := record.ValueByKey("last").(time.Time); ok {
				info.NewestDate = last
			}
		}
	}
	return info, result.Err()
}

// --- Internal Storage Implementation ---

type InfluxDBStorage struct {
	client   influxdb2.Client
	writeAPI api.WriteAPIBlocking
	bucket   string
	org      string
}

func NewInfluxDBStorage() (*InfluxDBStorage, error) {
	// Default to external port 12130 if not set
	url := os.Getenv("INFLUXDB_URL")
	if url == "" {
		url = "http://localhost:12130"
	}

	token := os.Getenv("INFLUXDB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("INFLUXDB_TOKEN environment variable is required")
	}

	org := os.Getenv("INFLUXDB_ORG")
	if org == "" {
		org = "aleutian-finance"
	}

	bucket := os.Getenv("INFLUXDB_BUCKET")
	if bucket == "" {
		bucket = "financial-data"
	}

	client := influxdb2.NewClient(url, token)
	writeAPI := client.WriteAPIBlocking(org, bucket)

	return &InfluxDBStorage{
		client:   client,
		writeAPI: writeAPI,
		bucket:   bucket,
		org:      org,
	}, nil
}

// FetchMissingData calls the data fetcher service to populate InfluxDB
func (e *Evaluator) FetchMissingData(ctx context.Context, ticker string, startDate,
	endDate time.Time) error {

	url := fmt.Sprintf("%s/v1/data/fetch", e.orchestratorURL)

	payload := dataFetchRequest{
		Names:     []string{ticker},
		StartDate: startDate.Format("2006-01-02"),
		EndDate:   endDate.Format("2006-01-02"),
		Interval:  "1d",
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal data fetch request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create data fetch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info("Fetching missing data from external source",
		"ticker", ticker,
		"start", startDate.Format("2006-01-02"),
		"end", endDate.Format("2006-01-02"))

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("data fetch request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("Failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			body = []byte("(failed to read body)")
		}
		return fmt.Errorf("data fetch failed with status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("Data fetch completed successfully", "ticker", ticker)
	return nil
}

// EnsureDataAvailability checks data coverage and fetches missing data if needed.
// Returns the adjusted fetch_start_date if data isn't available as far back as requested.
func (e *Evaluator) EnsureDataAvailability(ctx context.Context, scenario *datatypes.BacktestScenario) (adjustedFetchStart time.Time, err error) {
	ticker := scenario.Evaluation.Ticker
	contextSize := scenario.Forecast.ContextSize

	// Parse the requested dates
	layout := "2006-01-02"
	if len(scenario.Evaluation.FetchStartDate) == 8 {
		layout = "20060102"
	}
	requestedFetchStart, err := time.Parse(layout, scenario.Evaluation.FetchStartDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid fetch_start_date: %w", err)
	}

	endLayout := "2006-01-02"
	if len(scenario.Evaluation.EndDate) == 8 {
		endLayout = "20060102"
	}
	var requestedEnd time.Time
	if scenario.Evaluation.EndDate == "" {
		requestedEnd = time.Now()
	} else {
		requestedEnd, err = time.Parse(endLayout, scenario.Evaluation.EndDate)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid end_date: %w", err)
		}
	}

	// Step 1: Check current data coverage
	slog.Info("Checking data availability in InfluxDB", "ticker", ticker)
	coverage, err := e.CheckDataCoverage(ctx, ticker)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to check data coverage: %w", err)
	}

	if coverage.HasData {
		slog.Info("Current data coverage",
			"ticker", ticker,
			"points", coverage.PointCount,
			"oldest", coverage.OldestDate.Format("2006-01-02"),
			"newest", coverage.NewestDate.Format("2006-01-02"))
	} else {
		slog.Info("No existing data found for ticker", "ticker", ticker)
	}

	// Step 2: Determine what data we need to fetch
	needsFetch := false
	fetchStart := requestedFetchStart
	fetchEnd := requestedEnd

	if !coverage.HasData {
		// No data at all - fetch everything
		needsFetch = true
		slog.Warn("No data exists for ticker, will attempt to fetch",
			"ticker", ticker,
			"requested_start", requestedFetchStart.Format("2006-01-02"),
			"requested_end", requestedEnd.Format("2006-01-02"))
	} else {
		// Check if we need earlier data
		if requestedFetchStart.Before(coverage.OldestDate) {
			needsFetch = true
			fetchEnd = coverage.OldestDate.AddDate(0, 0, -1) // Fetch up to day before existing data
			slog.Info("Need earlier data",
				"requested_start", requestedFetchStart.Format("2006-01-02"),
				"oldest_available", coverage.OldestDate.Format("2006-01-02"))
		}
		// Check if we need later data
		if requestedEnd.After(coverage.NewestDate) {
			if needsFetch {
				// Need both earlier and later - fetch the whole range
				fetchStart = requestedFetchStart
				fetchEnd = requestedEnd
			} else {
				needsFetch = true
				fetchStart = coverage.NewestDate.AddDate(0, 0, 1) // Fetch from day after existing data
				fetchEnd = requestedEnd
			}
			slog.Info("Need later data",
				"requested_end", requestedEnd.Format("2006-01-02"),
				"newest_available", coverage.NewestDate.Format("2006-01-02"))
		}
	}

	// Step 3: Fetch missing data if needed
	if needsFetch {
		err = e.FetchMissingData(ctx, ticker, fetchStart, fetchEnd)
		if err != nil {
			slog.Warn("Failed to fetch missing data", "error", err)
			// Don't fail yet - we might still have enough data
		} else {
			// Re-check coverage after fetch
			coverage, err = e.CheckDataCoverage(ctx, ticker)
			if err != nil {
				return time.Time{}, fmt.Errorf("failed to re-check data coverage: %w", err)
			}
			slog.Info("Updated data coverage after fetch",
				"ticker", ticker,
				"points", coverage.PointCount,
				"oldest", coverage.OldestDate.Format("2006-01-02"),
				"newest", coverage.NewestDate.Format("2006-01-02"))
		}
	}

	// Step 4: Validate we have enough data and adjust if needed
	if !coverage.HasData {
		return time.Time{}, fmt.Errorf("no data available for ticker %s after fetch attempt", ticker)
	}

	adjustedFetchStart = requestedFetchStart

	// Check if oldest available is after what we requested
	if coverage.OldestDate.After(requestedFetchStart) {
		adjustedFetchStart = coverage.OldestDate
		slog.Warn("DATA AVAILABILITY ALERT: Requested start date is earlier than available data",
			"ticker", ticker,
			"requested_start", requestedFetchStart.Format("2006-01-02"),
			"oldest_available", coverage.OldestDate.Format("2006-01-02"),
			"adjusted_fetch_start", adjustedFetchStart.Format("2006-01-02"))

		fmt.Printf("\n⚠️  DATA AVAILABILITY WARNING\n")
		fmt.Printf("   Requested fetch_start_date: %s\n", requestedFetchStart.Format("2006-01-02"))
		fmt.Printf("   Oldest available data:      %s\n", coverage.OldestDate.Format("2006-01-02"))
		fmt.Printf("   → Using oldest available as fetch_start_date\n\n")
	}

	// Calculate if evaluation start_date needs adjustment based on context requirement
	evalLayout := "2006-01-02"
	if len(scenario.Evaluation.StartDate) == 8 {
		evalLayout = "20060102"
	}
	evalStart, _ := time.Parse(evalLayout, scenario.Evaluation.StartDate)

	// Minimum evaluation start = oldest data + context_size trading days
	// Approximate: context_size * 1.5 calendar days to account for weekends
	minCalendarDays := int(float64(contextSize) * 1.5)
	minEvalStart := coverage.OldestDate.AddDate(0, 0, minCalendarDays)

	if evalStart.Before(minEvalStart) {
		fmt.Printf("\n⚠️  CONTEXT REQUIREMENT WARNING\n")
		fmt.Printf("   Evaluation start_date: %s\n", evalStart.Format("2006-01-02"))
		fmt.Printf("   Required context:      %d trading days\n", contextSize)
		fmt.Printf("   Oldest available data: %s\n", coverage.OldestDate.Format("2006-01-02"))
		fmt.Printf("   Minimum eval start:    ~%s (oldest + %d calendar days)\n", minEvalStart.Format("2006-01-02"), minCalendarDays)
		fmt.Printf("   → The evaluation will skip dates until sufficient context is available\n\n")
	}

	return adjustedFetchStart, nil
}

func (s *InfluxDBStorage) StoreResult(ctx context.Context, result *datatypes.EvaluationResult) error {
	p := influxdb2.NewPointWithMeasurement("forecast_evaluations").
		AddTag("ticker", result.Ticker).
		AddTag("model", result.Model).
		AddTag("evaluation_date", result.EvaluationDate).
		AddTag("run_id", result.RunID).
		AddTag("forecast_horizon", fmt.Sprintf("%d", result.ForecastHorizon)).
		AddTag("strategy_type", result.StrategyType).
		AddField("forecast_price", result.ForecastPrice).
		AddField("current_price", result.CurrentPrice).
		AddField("action", result.Action).
		AddField("size", result.Size).
		AddField("value", result.Value).
		AddField("reason", result.Reason).
		AddField("available_cash", result.AvailableCash).
		AddField("position_after", result.PositionAfter).
		AddField("stopped", result.Stopped).
		AddField("threshold_value", result.ThresholdValue).
		AddField("execution_size", result.ExecutionSize).
		SetTime(result.Timestamp)

	return s.writeAPI.WritePoint(ctx, p)
}

// StoreMetrics writes backtest performance metrics to InfluxDB.
func (s *InfluxDBStorage) StoreMetrics(ctx context.Context, runID, ticker, model string, metrics *datatypes.MetricsResponse) error {
	if metrics == nil {
		return nil
	}

	p := influxdb2.NewPointWithMeasurement("backtest_metrics").
		AddTag("run_id", runID).
		AddTag("ticker", ticker).
		AddTag("model", model).
		AddField("sharpe_ratio", metrics.SharpeRatio).
		AddField("max_drawdown", metrics.MaxDrawdown).
		AddField("cagr", metrics.CAGR).
		AddField("calmar_ratio", metrics.CalmarRatio).
		AddField("win_rate", metrics.WinRate).
		SetTime(time.Now())

	if err := s.writeAPI.WritePoint(ctx, p); err != nil {
		return fmt.Errorf("failed to write metrics to InfluxDB: %w", err)
	}

	slog.Info("Metrics stored to InfluxDB",
		"run_id", runID,
		"measurement", "backtest_metrics")

	return nil
}

// sanitizeFloat replaces NaN and Inf with 0.0 for safe InfluxDB writes.
func sanitizeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0.0
	}
	return v
}

// StoreForecast writes a forecast record to the "forecasts" InfluxDB measurement.
func (s *InfluxDBStorage) StoreForecast(ctx context.Context, runID string, forecast *datatypes.ForecastRecord) error {
	p := influxdb2.NewPointWithMeasurement("forecasts").
		AddTag("run_id", runID).
		AddTag("ticker", forecast.Ticker).
		AddTag("model", forecast.Model).
		AddTag("model_family", forecast.ModelFamily).
		AddTag("schema_version", schemaVersion).
		AddField("forecast_price", sanitizeFloat(forecast.ForecastPrice)).
		AddField("current_price", sanitizeFloat(forecast.CurrentPrice)).
		AddField("forecast_horizon", forecast.Horizon).
		AddField("inference_time_ms", forecast.InferenceTimeMs).
		AddField("forecast_date", forecast.ForecastDate).
		SetTime(forecast.Timestamp)

	if forecast.ConfidenceLower != 0 || forecast.ConfidenceUpper != 0 {
		p.AddField("confidence_lower", sanitizeFloat(forecast.ConfidenceLower))
		p.AddField("confidence_upper", sanitizeFloat(forecast.ConfidenceUpper))
	}

	if err := s.writeAPI.WritePoint(ctx, p); err != nil {
		return fmt.Errorf("failed to write forecast to InfluxDB: %w", err)
	}

	return nil
}

// StoreTradingSignal writes a trading signal record to the "trading_signals" InfluxDB measurement.
func (s *InfluxDBStorage) StoreTradingSignal(ctx context.Context, runID string, signal *datatypes.TradingSignalRecord) error {
	p := influxdb2.NewPointWithMeasurement("trading_signals").
		AddTag("run_id", runID).
		AddTag("ticker", signal.Ticker).
		AddTag("strategy_type", signal.StrategyType).
		AddTag("action", signal.Action).
		AddTag("schema_version", schemaVersion).
		AddField("forecast_price", sanitizeFloat(signal.ForecastPrice)).
		AddField("current_price", sanitizeFloat(signal.CurrentPrice)).
		AddField("position_before", sanitizeFloat(signal.PositionBefore)).
		AddField("position_after", sanitizeFloat(signal.PositionAfter)).
		AddField("trade_size", sanitizeFloat(signal.TradeSize)).
		AddField("trade_value", sanitizeFloat(signal.TradeValue)).
		AddField("available_cash_before", sanitizeFloat(signal.CashBefore)).
		AddField("available_cash_after", sanitizeFloat(signal.CashAfter)).
		AddField("reason", signal.Reason).
		AddField("signal_date", signal.SignalDate).
		SetTime(signal.Timestamp)

	if err := s.writeAPI.WritePoint(ctx, p); err != nil {
		return fmt.Errorf("failed to write trading signal to InfluxDB: %w", err)
	}

	return nil
}

// StorePortfolioState writes a portfolio snapshot to the "portfolio_state" InfluxDB measurement.
func (s *InfluxDBStorage) StorePortfolioState(ctx context.Context, runID string, state *datatypes.PortfolioStateRecord) error {
	p := influxdb2.NewPointWithMeasurement("portfolio_state").
		AddTag("run_id", runID).
		AddTag("ticker", state.Ticker).
		AddTag("schema_version", schemaVersion).
		AddField("portfolio_value", sanitizeFloat(state.PortfolioValue)).
		AddField("cash", sanitizeFloat(state.Cash)).
		AddField("position", sanitizeFloat(state.Position)).
		AddField("position_value", sanitizeFloat(state.PositionValue)).
		AddField("current_price", sanitizeFloat(state.CurrentPrice)).
		AddField("cumulative_return", sanitizeFloat(state.CumulativeReturn)).
		AddField("drawdown", sanitizeFloat(state.Drawdown)).
		AddField("step_index", state.StepIndex).
		AddField("snapshot_date", state.SnapshotDate).
		SetTime(state.Timestamp)

	if err := s.writeAPI.WritePoint(ctx, p); err != nil {
		return fmt.Errorf("failed to write portfolio state to InfluxDB: %w", err)
	}

	return nil
}

func (s *InfluxDBStorage) Close() {
	s.client.Close()
}
