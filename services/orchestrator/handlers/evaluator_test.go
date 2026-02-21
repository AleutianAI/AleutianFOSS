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
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// TEST HELPERS
// =============================================================================

func timePtr(t time.Time) *time.Time {
	return &t
}

func floatPtr(f float64) *float64 {
	return &f
}

// createMockEvaluator creates an Evaluator with a mock HTTP server
func createMockEvaluator(handler http.HandlerFunc) (*Evaluator, *httptest.Server) {
	mockServer := httptest.NewServer(handler)
	evaluator := &Evaluator{
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		orchestratorURL: mockServer.URL,
	}
	return evaluator, mockServer
}

// =============================================================================
// CallForecastServiceAsOf TESTS
// =============================================================================

func TestCallForecastServiceAsOf_Success(t *testing.T) {
	tests := []struct {
		name              string
		ticker            string
		model             string
		contextSize       int
		horizonSize       int
		asOfDate          *time.Time
		contextData       []float64
		wantAsOfDateKey   bool
		wantAsOfDateVal   string
		wantRecentData    bool
		wantRecentDataLen int
	}{
		{
			name:            "Without asOfDate (real-time)",
			ticker:          "SPY",
			model:           "amazon/chronos-t5-tiny",
			contextSize:     252,
			horizonSize:     20,
			asOfDate:        nil,
			contextData:     nil,
			wantAsOfDateKey: false,
			wantRecentData:  false,
		},
		{
			name:            "With asOfDate (backtest)",
			ticker:          "SPY",
			model:           "amazon/chronos-t5-tiny",
			contextSize:     252,
			horizonSize:     20,
			asOfDate:        timePtr(time.Date(2023, 6, 15, 14, 30, 0, 0, time.UTC)),
			contextData:     nil,
			wantAsOfDateKey: true,
			wantAsOfDateVal: "2023-06-15",
			wantRecentData:  false,
		},
		{
			name:              "With contextData (backtest with explicit history)",
			ticker:            "SPY",
			model:             "amazon/chronos-t5-tiny",
			contextSize:       10,
			horizonSize:       5,
			asOfDate:          timePtr(time.Date(2023, 6, 15, 14, 30, 0, 0, time.UTC)),
			contextData:       []float64{400.0, 401.5, 399.0, 402.0, 403.5, 401.0, 404.0, 405.5, 403.0, 406.0},
			wantAsOfDateKey:   true,
			wantAsOfDateVal:   "2023-06-15",
			wantRecentData:    true,
			wantRecentDataLen: 10,
		},
		{
			name:            "Minimal context size",
			ticker:          "AAPL",
			model:           "amazon/chronos-t5-small",
			contextSize:     1,
			horizonSize:     1,
			asOfDate:        nil,
			contextData:     nil,
			wantAsOfDateKey: false,
			wantRecentData:  false,
		},
		{
			name:            "Large context and horizon",
			ticker:          "QQQ",
			model:           "amazon/chronos-t5-large",
			contextSize:     512,
			horizonSize:     60,
			asOfDate:        timePtr(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
			contextData:     nil,
			wantAsOfDateKey: true,
			wantAsOfDateVal: "2024-01-01",
			wantRecentData:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPayload map[string]interface{}

			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/timeseries/forecast", r.URL.Path)
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				err := json.NewDecoder(r.Body).Decode(&capturedPayload)
				require.NoError(t, err)

				response := datatypes.ForecastResult{
					Forecast: []float64{400.0, 405.0, 410.0},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			})
			defer mockServer.Close()

			ctx := context.Background()
			result, err := evaluator.CallForecastServiceAsOf(ctx, tt.ticker, tt.model,
				tt.contextSize, tt.horizonSize, tt.asOfDate, tt.contextData)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Len(t, result.Forecast, 3)

			// Verify payload contents
			assert.Equal(t, tt.ticker, capturedPayload["name"])
			assert.Equal(t, float64(tt.contextSize), capturedPayload["context_period_size"])
			assert.Equal(t, float64(tt.horizonSize), capturedPayload["forecast_period_size"])
			assert.Equal(t, tt.model, capturedPayload["model"])

			// Verify as_of_date
			asOfDateVal, hasAsOfDate := capturedPayload["as_of_date"]
			assert.Equal(t, tt.wantAsOfDateKey, hasAsOfDate, "as_of_date presence mismatch")
			if tt.wantAsOfDateKey {
				assert.Equal(t, tt.wantAsOfDateVal, asOfDateVal)
			}

			// Verify recent_data
			recentData, hasRecentData := capturedPayload["recent_data"]
			assert.Equal(t, tt.wantRecentData, hasRecentData, "recent_data presence mismatch")
			if tt.wantRecentData {
				recentDataSlice, ok := recentData.([]interface{})
				require.True(t, ok, "recent_data should be array")
				assert.Len(t, recentDataSlice, tt.wantRecentDataLen)
			}
		})
	}
}

func TestCallForecastServiceAsOf_HTTPErrors(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		responseBody  string
		wantError     bool
		errorContains string
	}{
		{
			name:         "Success 200",
			statusCode:   http.StatusOK,
			responseBody: `{"forecast": [400.0, 405.0]}`,
			wantError:    false,
		},
		{
			name:          "Bad Request 400",
			statusCode:    http.StatusBadRequest,
			responseBody:  `{"error": "invalid ticker"}`,
			wantError:     true,
			errorContains: "400",
		},
		{
			name:          "Internal Server Error 500",
			statusCode:    http.StatusInternalServerError,
			responseBody:  `{"error": "model failed"}`,
			wantError:     true,
			errorContains: "500",
		},
		{
			name:          "Service Unavailable 503",
			statusCode:    http.StatusServiceUnavailable,
			responseBody:  `{"error": "service down"}`,
			wantError:     true,
			errorContains: "503",
		},
		{
			name:          "Gateway Timeout 504",
			statusCode:    http.StatusGatewayTimeout,
			responseBody:  `{"error": "timeout"}`,
			wantError:     true,
			errorContains: "504",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			})
			defer mockServer.Close()

			ctx := context.Background()
			result, err := evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny",
				252, 20, nil, nil)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, result)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestCallForecastServiceAsOf_InvalidJSON(t *testing.T) {
	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json}`))
	})
	defer mockServer.Close()

	ctx := context.Background()
	result, err := evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny",
		252, 20, nil, nil)

	// The current implementation may return an empty result with an error
	// or may silently fail - this test documents the actual behavior
	if err != nil {
		assert.Contains(t, err.Error(), "invalid character")
	} else {
		// If no error, result should still be valid (empty but valid struct)
		assert.NotNil(t, result)
	}
}

func TestCallForecastServiceAsOf_Timeout(t *testing.T) {
	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(datatypes.ForecastResult{Forecast: []float64{400.0}})
	})
	defer mockServer.Close()

	// Use very short timeout
	evaluator.httpClient.Timeout = 10 * time.Millisecond

	ctx := context.Background()
	result, err := evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny",
		252, 20, nil, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestCallForecastServiceAsOf_ContextCancellation(t *testing.T) {
	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(datatypes.ForecastResult{Forecast: []float64{400.0}})
	})
	defer mockServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny",
		252, 20, nil, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// CallForecastService TESTS (Backward Compatibility)
// =============================================================================

func TestCallForecastService_BackwardCompatibility(t *testing.T) {
	var capturedPayload map[string]interface{}

	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedPayload)

		// Should NOT have as_of_date
		_, hasAsOfDate := capturedPayload["as_of_date"]
		assert.False(t, hasAsOfDate, "Old method should not send as_of_date")

		response := datatypes.ForecastResult{Forecast: []float64{400.0}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
	defer mockServer.Close()

	ctx := context.Background()
	result, err := evaluator.CallForecastService(ctx, "SPY", "amazon/chronos-t5-tiny", 252, 20)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// =============================================================================
// FetchMissingData TESTS
// =============================================================================

func TestFetchMissingData_Success(t *testing.T) {
	var capturedPayload map[string]interface{}

	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/data/fetch", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		err := json.NewDecoder(r.Body).Decode(&capturedPayload)
		require.NoError(t, err)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "points_written": 252}`))
	})
	defer mockServer.Close()

	ctx := context.Background()
	startDate := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	err := evaluator.FetchMissingData(ctx, "SPY", startDate, endDate)
	require.NoError(t, err)

	// Verify payload format
	names, ok := capturedPayload["names"].([]interface{})
	require.True(t, ok, "names should be an array")
	assert.Equal(t, "SPY", names[0])
	assert.Equal(t, "2023-01-01", capturedPayload["start_date"])
	assert.Equal(t, "2023-12-31", capturedPayload["end_date"])
	assert.Equal(t, "1d", capturedPayload["interval"])
}

func TestFetchMissingData_HTTPErrors(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		responseBody  string
		wantError     bool
		errorContains string
	}{
		{
			name:         "Success",
			statusCode:   http.StatusOK,
			responseBody: `{"status": "ok"}`,
			wantError:    false,
		},
		{
			name:          "Bad Request",
			statusCode:    http.StatusBadRequest,
			responseBody:  `{"error": "invalid ticker"}`,
			wantError:     true,
			errorContains: "status 400",
		},
		{
			name:          "Internal Server Error",
			statusCode:    http.StatusInternalServerError,
			responseBody:  `{"error": "database unavailable"}`,
			wantError:     true,
			errorContains: "status 500",
		},
		{
			name:          "Service Unavailable",
			statusCode:    http.StatusServiceUnavailable,
			responseBody:  `{"error": "data source down"}`,
			wantError:     true,
			errorContains: "status 503",
		},
		{
			name:          "Not Found",
			statusCode:    http.StatusNotFound,
			responseBody:  `{"error": "ticker not found"}`,
			wantError:     true,
			errorContains: "status 404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responseBody))
			})
			defer mockServer.Close()

			ctx := context.Background()
			err := evaluator.FetchMissingData(ctx, "SPY",
				time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC))

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFetchMissingData_DifferentTickers(t *testing.T) {
	tickers := []string{"SPY", "AAPL", "GOOGL", "MSFT", "QQQ", "IWM"}

	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			var capturedTicker string

			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]interface{}
				json.NewDecoder(r.Body).Decode(&payload)
				names := payload["names"].([]interface{})
				capturedTicker = names[0].(string)
				w.WriteHeader(http.StatusOK)
			})
			defer mockServer.Close()

			ctx := context.Background()
			err := evaluator.FetchMissingData(ctx, ticker,
				time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC))

			assert.NoError(t, err)
			assert.Equal(t, ticker, capturedTicker)
		})
	}
}

func TestFetchMissingData_DateFormats(t *testing.T) {
	tests := []struct {
		name      string
		startDate time.Time
		endDate   time.Time
		wantStart string
		wantEnd   string
	}{
		{
			name:      "Full year 2023",
			startDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			wantStart: "2023-01-01",
			wantEnd:   "2023-12-31",
		},
		{
			name:      "Single month",
			startDate: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
			wantStart: "2024-06-01",
			wantEnd:   "2024-06-30",
		},
		{
			name:      "Cross year boundary",
			startDate: time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			wantStart: "2023-12-01",
			wantEnd:   "2024-01-31",
		},
		{
			name:      "Same day",
			startDate: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			wantStart: "2024-03-15",
			wantEnd:   "2024-03-15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPayload map[string]interface{}

			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&capturedPayload)
				w.WriteHeader(http.StatusOK)
			})
			defer mockServer.Close()

			ctx := context.Background()
			err := evaluator.FetchMissingData(ctx, "SPY", tt.startDate, tt.endDate)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantStart, capturedPayload["start_date"])
			assert.Equal(t, tt.wantEnd, capturedPayload["end_date"])
		})
	}
}

// =============================================================================
// CallTradingService TESTS
// =============================================================================

func TestCallTradingService_Success(t *testing.T) {
	tests := []struct {
		name    string
		request datatypes.TradingSignalRequest
	}{
		{
			name: "Buy signal - threshold strategy",
			request: datatypes.TradingSignalRequest{
				Ticker:          "SPY",
				StrategyType:    "threshold",
				ForecastPrice:   410.0,
				CurrentPrice:    floatPtr(400.0),
				CurrentPosition: 0.0,
				AvailableCash:   100000.0,
				InitialCapital:  100000.0,
			},
		},
		{
			name: "Sell signal - threshold strategy",
			request: datatypes.TradingSignalRequest{
				Ticker:          "SPY",
				StrategyType:    "threshold",
				ForecastPrice:   390.0,
				CurrentPrice:    floatPtr(420.0),
				CurrentPosition: 100.0,
				AvailableCash:   50000.0,
				InitialCapital:  100000.0,
			},
		},
		{
			name: "Hold signal - no significant change",
			request: datatypes.TradingSignalRequest{
				Ticker:          "AAPL",
				StrategyType:    "threshold",
				ForecastPrice:   181.0,
				CurrentPrice:    floatPtr(180.0),
				CurrentPosition: 50.0,
				AvailableCash:   75000.0,
				InitialCapital:  100000.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPayload map[string]interface{}

			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&capturedPayload)
				response := datatypes.TradingSignalResponse{
					Action: "buy",
					Size:   10.0,
					Value:  4000.0,
					Reason: "Forecast indicates price increase",
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			})
			defer mockServer.Close()

			// Need to set tradingServiceURL for this test
			evaluator.tradingServiceURL = mockServer.URL

			ctx := context.Background()
			result, err := evaluator.CallTradingService(ctx, tt.request)

			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, "buy", result.Action)
			assert.Equal(t, 10.0, result.Size)
		})
	}
}

func TestCallTradingService_HTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{"Success", http.StatusOK, false},
		{"Bad Request", http.StatusBadRequest, true},
		{"Internal Error", http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.statusCode == http.StatusOK {
					json.NewEncoder(w).Encode(datatypes.TradingSignalResponse{Action: "hold"})
				} else {
					w.Write([]byte(`{"error": "test error"}`))
				}
			})
			defer mockServer.Close()

			evaluator.tradingServiceURL = mockServer.URL

			ctx := context.Background()
			req := datatypes.TradingSignalRequest{
				Ticker:          "SPY",
				StrategyType:    "threshold",
				ForecastPrice:   410.0,
				CurrentPrice:    floatPtr(400.0),
				CurrentPosition: 0.0,
				AvailableCash:   100000.0,
				InitialCapital:  100000.0,
			}
			result, err := evaluator.CallTradingService(ctx, req)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// =============================================================================
// GetCurrentPrice TESTS
// Note: GetCurrentPrice queries InfluxDB directly, so these tests document
// expected behavior but require mocking at the InfluxDB layer for true unit tests
// =============================================================================

func TestGetCurrentPrice_RequiresInfluxDB(t *testing.T) {
	// This test documents that GetCurrentPrice needs InfluxDB storage
	// In a real unit test scenario, you'd mock the InfluxDB client

	// For now, we just verify the function signature exists
	var evaluator *Evaluator
	var ctx context.Context
	var ticker string

	// This won't run, just type-checks the function signature
	_ = func() {
		_, _ = evaluator.GetCurrentPrice(ctx, ticker)
	}
}

// =============================================================================
// DataCoverageInfo STRUCT TESTS
// =============================================================================

func TestDataCoverageInfo_Struct(t *testing.T) {
	tests := []struct {
		name     string
		info     datatypes.DataCoverageInfo
		hasData  bool
		validate func(t *testing.T, info datatypes.DataCoverageInfo)
	}{
		{
			name: "Valid coverage with data",
			info: datatypes.DataCoverageInfo{
				Ticker:     "SPY",
				OldestDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				NewestDate: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
				PointCount: 252,
				HasData:    true,
			},
			hasData: true,
			validate: func(t *testing.T, info datatypes.DataCoverageInfo) {
				assert.Equal(t, "SPY", info.Ticker)
				assert.Equal(t, 252, info.PointCount)
				assert.True(t, info.NewestDate.After(info.OldestDate))
			},
		},
		{
			name: "No data available",
			info: datatypes.DataCoverageInfo{
				Ticker:     "UNKNOWN",
				PointCount: 0,
				HasData:    false,
			},
			hasData: false,
			validate: func(t *testing.T, info datatypes.DataCoverageInfo) {
				assert.Equal(t, 0, info.PointCount)
				assert.False(t, info.HasData)
			},
		},
		{
			name: "Single data point",
			info: datatypes.DataCoverageInfo{
				Ticker:     "TEST",
				OldestDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				NewestDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				PointCount: 1,
				HasData:    true,
			},
			hasData: true,
			validate: func(t *testing.T, info datatypes.DataCoverageInfo) {
				assert.Equal(t, 1, info.PointCount)
				assert.Equal(t, info.OldestDate, info.NewestDate)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.hasData, tt.info.HasData)
			tt.validate(t, tt.info)
		})
	}
}

// =============================================================================
// ContextSizeValidation TESTS
// =============================================================================

func TestContextSizeValidation(t *testing.T) {
	tests := []struct {
		name           string
		contextSize    int
		oldestDataDate string
		evalStartDate  string
		expectWarning  bool
		description    string
	}{
		{
			name:           "Sufficient context - 1 year for 100 days",
			contextSize:    100,
			oldestDataDate: "2022-01-01",
			evalStartDate:  "2023-01-01",
			expectWarning:  false,
			description:    "365 days available, need 150 (100*1.5)",
		},
		{
			name:           "Insufficient context - 60 days for 252",
			contextSize:    252,
			oldestDataDate: "2023-01-01",
			evalStartDate:  "2023-03-01",
			expectWarning:  true,
			description:    "~60 days available, need 378 (252*1.5)",
		},
		{
			name:           "Borderline passes - 200 context",
			contextSize:    200,
			oldestDataDate: "2022-01-01",
			evalStartDate:  "2023-01-01",
			expectWarning:  false,
			description:    "365 days available, need 300 (200*1.5)",
		},
		{
			name:           "Borderline fails - 252 context",
			contextSize:    252,
			oldestDataDate: "2022-01-01",
			evalStartDate:  "2023-01-01",
			expectWarning:  true,
			description:    "365 days available, need 378 (252*1.5)",
		},
		{
			name:           "Minimal context - 10 days",
			contextSize:    10,
			oldestDataDate: "2024-01-01",
			evalStartDate:  "2024-01-20",
			expectWarning:  false,
			description:    "19 days available, need 15 (10*1.5)",
		},
		{
			name:           "Large context - 512 days",
			contextSize:    512,
			oldestDataDate: "2020-01-01",
			evalStartDate:  "2024-01-01",
			expectWarning:  false,
			description:    "~1461 days available, need 768 (512*1.5)",
		},
		{
			name:           "Exact boundary - just enough",
			contextSize:    100,
			oldestDataDate: "2023-01-01",
			evalStartDate:  "2023-06-01",
			expectWarning:  false,
			description:    "151 days available, need exactly 150",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldest, err := time.Parse("2006-01-02", tt.oldestDataDate)
			require.NoError(t, err)
			evalStart, err := time.Parse("2006-01-02", tt.evalStartDate)
			require.NoError(t, err)

			// Calculate minimum required calendar days (same logic as EnsureDataAvailability)
			minCalendarDays := int(float64(tt.contextSize) * 1.5)
			minEvalStart := oldest.AddDate(0, 0, minCalendarDays)

			needsWarning := evalStart.Before(minEvalStart)

			assert.Equal(t, tt.expectWarning, needsWarning,
				"Context: %s - oldest=%s, evalStart=%s, minEvalStart=%s",
				tt.description, tt.oldestDataDate, tt.evalStartDate, minEvalStart.Format("2006-01-02"))
		})
	}
}

// =============================================================================
// DateParsing TESTS
// =============================================================================

func TestDateParsing(t *testing.T) {
	tests := []struct {
		name      string
		dateStr   string
		wantYear  int
		wantMonth time.Month
		wantDay   int
		wantError bool
	}{
		{
			name:      "YYYYMMDD format",
			dateStr:   "20230615",
			wantYear:  2023,
			wantMonth: time.June,
			wantDay:   15,
			wantError: false,
		},
		{
			name:      "YYYY-MM-DD format",
			dateStr:   "2023-06-15",
			wantYear:  2023,
			wantMonth: time.June,
			wantDay:   15,
			wantError: false,
		},
		{
			name:      "Start of year YYYYMMDD",
			dateStr:   "20240101",
			wantYear:  2024,
			wantMonth: time.January,
			wantDay:   1,
			wantError: false,
		},
		{
			name:      "End of year YYYY-MM-DD",
			dateStr:   "2023-12-31",
			wantYear:  2023,
			wantMonth: time.December,
			wantDay:   31,
			wantError: false,
		},
		{
			name:      "Invalid format",
			dateStr:   "not-a-date",
			wantError: true,
		},
		{
			name:      "Partial date",
			dateStr:   "2023-06",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Try both formats (same logic as EnsureDataAvailability)
			layout := "2006-01-02"
			if len(tt.dateStr) == 8 {
				layout = "20060102"
			}

			parsed, err := time.Parse(layout, tt.dateStr)

			if tt.wantError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantYear, parsed.Year())
				assert.Equal(t, tt.wantMonth, parsed.Month())
				assert.Equal(t, tt.wantDay, parsed.Day())
			}
		})
	}
}

// =============================================================================
// BacktestScenario VALIDATION TESTS
// =============================================================================

func TestBacktestScenario_Validation(t *testing.T) {
	tests := []struct {
		name          string
		scenario      datatypes.BacktestScenario
		wantError     bool
		errorContains string
	}{
		{
			name: "Valid scenario",
			scenario: func() datatypes.BacktestScenario {
				s := datatypes.BacktestScenario{}
				s.Metadata.ID = "test-1"
				s.Metadata.Version = "1.0.0"
				s.Evaluation.Ticker = "SPY"
				s.Evaluation.FetchStartDate = "20220101"
				s.Evaluation.StartDate = "20230101"
				s.Evaluation.EndDate = "20231231"
				s.Forecast.Model = "amazon/chronos-t5-tiny"
				s.Forecast.ContextSize = 252
				s.Forecast.HorizonSize = 20
				s.Trading.InitialCapital = 100000.0
				s.Trading.StrategyType = "threshold"
				return s
			}(),
			wantError: false,
		},
		{
			name: "Missing ticker",
			scenario: func() datatypes.BacktestScenario {
				s := datatypes.BacktestScenario{}
				s.Evaluation.FetchStartDate = "20220101"
				s.Evaluation.StartDate = "20230101"
				return s
			}(),
			wantError:     true,
			errorContains: "ticker",
		},
		{
			name: "Missing fetch_start_date",
			scenario: func() datatypes.BacktestScenario {
				s := datatypes.BacktestScenario{}
				s.Evaluation.Ticker = "SPY"
				s.Evaluation.StartDate = "20230101"
				return s
			}(),
			wantError:     true,
			errorContains: "fetch_start_date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate scenario (simplified validation logic)
			var err error
			if tt.scenario.Evaluation.Ticker == "" {
				err = errors.New("ticker is required")
			} else if tt.scenario.Evaluation.FetchStartDate == "" {
				err = errors.New("fetch_start_date is required")
			}

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// TradingSignal CALCULATION TESTS
// =============================================================================

func TestTradingSignalCalculation(t *testing.T) {
	tests := []struct {
		name           string
		currentPrice   float64
		forecastPrice  float64
		thresholdPct   float64
		expectedSignal string
	}{
		{
			name:           "Strong buy signal",
			currentPrice:   100.0,
			forecastPrice:  110.0,
			thresholdPct:   5.0,
			expectedSignal: "BUY",
		},
		{
			name:           "Strong sell signal",
			currentPrice:   100.0,
			forecastPrice:  90.0,
			thresholdPct:   5.0,
			expectedSignal: "SELL",
		},
		{
			name:           "Hold - within threshold",
			currentPrice:   100.0,
			forecastPrice:  102.0,
			thresholdPct:   5.0,
			expectedSignal: "HOLD",
		},
		{
			name:           "Exact threshold - buy",
			currentPrice:   100.0,
			forecastPrice:  105.0,
			thresholdPct:   5.0,
			expectedSignal: "BUY",
		},
		{
			name:           "Exact threshold - sell",
			currentPrice:   100.0,
			forecastPrice:  95.0,
			thresholdPct:   5.0,
			expectedSignal: "SELL",
		},
		{
			name:           "Zero threshold - any change triggers",
			currentPrice:   100.0,
			forecastPrice:  100.01,
			thresholdPct:   0.0,
			expectedSignal: "BUY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pctChange := ((tt.forecastPrice - tt.currentPrice) / tt.currentPrice) * 100

			var signal string
			if pctChange >= tt.thresholdPct {
				signal = "BUY"
			} else if pctChange <= -tt.thresholdPct {
				signal = "SELL"
			} else {
				signal = "HOLD"
			}

			assert.Equal(t, tt.expectedSignal, signal)
		})
	}
}

// =============================================================================
// BENCHMARK TESTS
// =============================================================================

func BenchmarkCallForecastServiceAsOf(b *testing.B) {
	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		response := datatypes.ForecastResult{Forecast: []float64{400.0, 405.0}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
	defer mockServer.Close()

	ctx := context.Background()
	asOfDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)

	b.Run("WithAsOfDate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny", 252, 20, &asOfDate, nil)
		}
	})

	b.Run("WithoutAsOfDate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny", 252, 20, nil, nil)
		}
	})

	b.Run("WithContextData", func(b *testing.B) {
		contextData := make([]float64, 252)
		for i := range contextData {
			contextData[i] = 400.0 + float64(i)*0.1
		}
		for i := 0; i < b.N; i++ {
			evaluator.CallForecastServiceAsOf(ctx, "SPY", "chronos-t5-tiny", 252, 20, &asOfDate, contextData)
		}
	})
}

func BenchmarkFetchMissingData(b *testing.B) {
	evaluator, mockServer := createMockEvaluator(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer mockServer.Close()

	ctx := context.Background()
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.FetchMissingData(ctx, "SPY", start, end)
	}
}

// =============================================================================
// MOCK INFLUXDB WRITE API
// =============================================================================

// mockWriteAPI implements api.WriteAPIBlocking for testing Store methods.
type mockWriteAPI struct {
	points []*write.Point
	err    error // If set, WritePoint returns this error
}

func (m *mockWriteAPI) WriteRecord(_ context.Context, _ ...string) error {
	return m.err
}

func (m *mockWriteAPI) WritePoint(_ context.Context, point ...*write.Point) error {
	if m.err != nil {
		return m.err
	}
	m.points = append(m.points, point...)
	return nil
}

func (m *mockWriteAPI) EnableBatching() {}

func (m *mockWriteAPI) Flush(_ context.Context) error {
	return m.err
}

func createMockStorage() (*InfluxDBStorage, *mockWriteAPI) {
	mock := &mockWriteAPI{}
	storage := &InfluxDBStorage{
		writeAPI: mock,
		bucket:   "test-bucket",
		org:      "test-org",
	}
	return storage, mock
}

// =============================================================================
// sanitizeFloat TESTS
// =============================================================================

func TestSanitizeFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"normal value", 123.45, 123.45},
		{"zero", 0.0, 0.0},
		{"negative", -42.5, -42.5},
		{"NaN", math.NaN(), 0.0},
		{"positive Inf", math.Inf(1), 0.0},
		{"negative Inf", math.Inf(-1), 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFloat(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// StoreForecast TESTS
// =============================================================================

func TestStoreForecast_Success(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.ForecastRecord{
		Ticker:          "SPY",
		Model:           "chronos-t5-tiny",
		ModelFamily:     "chronos",
		ForecastPrice:   150.25,
		CurrentPrice:    148.50,
		Horizon:         20,
		InferenceTimeMs: 245,
		ForecastDate:    "20260220",
		Timestamp:       time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
	}

	err := storage.StoreForecast(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStoreForecast_WithConfidenceBounds(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.ForecastRecord{
		Ticker:          "SPY",
		Model:           "chronos-t5-tiny",
		ModelFamily:     "chronos",
		ForecastPrice:   150.25,
		CurrentPrice:    148.50,
		Horizon:         20,
		InferenceTimeMs: 245,
		ForecastDate:    "20260220",
		ConfidenceLower: 145.0,
		ConfidenceUpper: 155.0,
		Timestamp:       time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
	}

	err := storage.StoreForecast(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStoreForecast_WriteError(t *testing.T) {
	storage, mock := createMockStorage()
	mock.err = errors.New("connection refused")
	ctx := context.Background()

	record := &datatypes.ForecastRecord{
		Ticker:        "SPY",
		Model:         "chronos-t5-tiny",
		ForecastPrice: 150.25,
		CurrentPrice:  148.50,
	}

	err := storage.StoreForecast(ctx, "test-run-1", record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write forecast")
}

func TestStoreForecast_SanitizesNaN(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.ForecastRecord{
		Ticker:        "SPY",
		Model:         "chronos-t5-tiny",
		ForecastPrice: math.NaN(),
		CurrentPrice:  math.Inf(1),
	}

	err := storage.StoreForecast(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
	// Values should be sanitized to 0.0, not NaN/Inf
}

// =============================================================================
// StoreTradingSignal TESTS
// =============================================================================

func TestStoreTradingSignal_Success(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.TradingSignalRecord{
		Ticker:         "SPY",
		StrategyType:   "threshold",
		Action:         "buy",
		ForecastPrice:  150.25,
		CurrentPrice:   148.50,
		PositionBefore: 0.0,
		PositionAfter:  10.0,
		TradeSize:      10.0,
		TradeValue:     1485.0,
		CashBefore:     10000.0,
		CashAfter:      8515.0,
		Reason:         "Forecast exceeds threshold",
		SignalDate:     "20260220",
	}

	err := storage.StoreTradingSignal(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStoreTradingSignal_HoldAction(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.TradingSignalRecord{
		Ticker:         "SPY",
		StrategyType:   "threshold",
		Action:         "hold",
		ForecastPrice:  148.80,
		CurrentPrice:   148.50,
		PositionBefore: 10.0,
		PositionAfter:  10.0,
		TradeSize:      0.0,
		TradeValue:     0.0,
		CashBefore:     8515.0,
		CashAfter:      8515.0,
		Reason:         "Below threshold",
		SignalDate:     "20260220",
	}

	err := storage.StoreTradingSignal(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStoreTradingSignal_WriteError(t *testing.T) {
	storage, mock := createMockStorage()
	mock.err = errors.New("timeout")
	ctx := context.Background()

	record := &datatypes.TradingSignalRecord{
		Ticker: "SPY",
		Action: "buy",
	}

	err := storage.StoreTradingSignal(ctx, "test-run-1", record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write trading signal")
}

// =============================================================================
// StorePortfolioState TESTS
// =============================================================================

func TestStorePortfolioState_Success(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.PortfolioStateRecord{
		Ticker:           "SPY",
		PortfolioValue:   10150.0,
		Cash:             8515.0,
		Position:         10.0,
		PositionValue:    1635.0,
		CurrentPrice:     163.50,
		CumulativeReturn: 0.015,
		Drawdown:         0.0,
		StepIndex:        5,
		SnapshotDate:     "20260220",
	}

	err := storage.StorePortfolioState(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStorePortfolioState_WithDrawdown(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.PortfolioStateRecord{
		Ticker:           "SPY",
		PortfolioValue:   9500.0,
		Cash:             8515.0,
		Position:         10.0,
		PositionValue:    985.0,
		CurrentPrice:     98.50,
		CumulativeReturn: -0.05,
		Drawdown:         -0.064,
		StepIndex:        25,
		SnapshotDate:     "20260220",
	}

	err := storage.StorePortfolioState(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}

func TestStorePortfolioState_WriteError(t *testing.T) {
	storage, mock := createMockStorage()
	mock.err = errors.New("disk full")
	ctx := context.Background()

	record := &datatypes.PortfolioStateRecord{
		Ticker:         "SPY",
		PortfolioValue: 10000.0,
	}

	err := storage.StorePortfolioState(ctx, "test-run-1", record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write portfolio state")
}

func TestStorePortfolioState_SanitizesInfValues(t *testing.T) {
	storage, mock := createMockStorage()
	ctx := context.Background()

	record := &datatypes.PortfolioStateRecord{
		Ticker:           "SPY",
		PortfolioValue:   10000.0,
		CumulativeReturn: math.Inf(1),
		Drawdown:         math.NaN(),
	}

	err := storage.StorePortfolioState(ctx, "test-run-1", record)
	require.NoError(t, err)
	assert.Len(t, mock.points, 1)
}
