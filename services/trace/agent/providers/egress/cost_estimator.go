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
	"strings"
	"sync"
)

// ModelPricing holds per-model token pricing in dollars per million tokens.
//
// Thread Safety: ModelPricing is a value type, safe to copy.
type ModelPricing struct {
	// InputCostPerMillion is the cost in USD per million input tokens.
	InputCostPerMillion float64

	// OutputCostPerMillion is the cost in USD per million output tokens.
	OutputCostPerMillion float64
}

// defaultPricing contains pricing for known models.
// Prices are approximate and based on published rates as of 2025.
var defaultPricing = map[string]ModelPricing{
	// Anthropic
	"claude-sonnet-4-20250514":  {InputCostPerMillion: 3.0, OutputCostPerMillion: 15.0},
	"claude-haiku-4-5-20251001": {InputCostPerMillion: 1.0, OutputCostPerMillion: 5.0},

	// OpenAI
	"gpt-4o":      {InputCostPerMillion: 2.50, OutputCostPerMillion: 10.0},
	"gpt-4o-mini": {InputCostPerMillion: 0.15, OutputCostPerMillion: 0.60},

	// Gemini
	"gemini-1.5-flash": {InputCostPerMillion: 0.075, OutputCostPerMillion: 0.30},
	"gemini-1.5-pro":   {InputCostPerMillion: 1.25, OutputCostPerMillion: 5.0},
	"gemini-2.0-flash": {InputCostPerMillion: 0.10, OutputCostPerMillion: 0.40},
}

// CostEstimator tracks cumulative cost and enforces a cost ceiling.
//
// Description:
//
//	Estimates the cost of LLM API calls based on per-model pricing and
//	enforces a configurable cost limit (in US cents). A limit of 0 means
//	unlimited (no enforcement).
//
// Thread Safety: Safe for concurrent use via sync.Mutex.
type CostEstimator struct {
	mu             sync.Mutex
	pricing        map[string]ModelPricing
	totalCostCents float64
	limitCents     float64
}

// NewCostEstimator creates a new cost estimator.
//
// Inputs:
//   - limitCents: Maximum cost in US cents. 0 means unlimited.
//
// Outputs:
//   - *CostEstimator: Configured estimator with default pricing.
func NewCostEstimator(limitCents float64) *CostEstimator {
	pricing := make(map[string]ModelPricing, len(defaultPricing))
	for k, v := range defaultPricing {
		pricing[k] = v
	}
	return &CostEstimator{
		pricing:    pricing,
		limitCents: limitCents,
	}
}

// CanAfford checks whether a request with the estimated tokens fits within
// the cost limit.
//
// Inputs:
//   - model: The model name.
//   - estimatedInputTokens: Estimated input token count.
//   - estimatedOutputTokens: Estimated output token count.
//
// Outputs:
//   - bool: True if the request is within budget.
//   - float64: Estimated cost of this request in US cents.
func (c *CostEstimator) CanAfford(model string, estimatedInputTokens, estimatedOutputTokens int) (bool, float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	estimatedCents := c.estimateCostCentsLocked(model, estimatedInputTokens, estimatedOutputTokens)

	if c.limitCents == 0 {
		return true, estimatedCents // unlimited
	}

	if c.totalCostCents+estimatedCents > c.limitCents {
		return false, estimatedCents
	}

	return true, estimatedCents
}

// Record records actual token usage and updates the cumulative cost.
//
// Inputs:
//   - model: The model name.
//   - inputTokens: Actual input token count.
//   - outputTokens: Actual output token count.
//
// Outputs:
//   - float64: The cost of this call in US cents.
func (c *CostEstimator) Record(model string, inputTokens, outputTokens int) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	costCents := c.estimateCostCentsLocked(model, inputTokens, outputTokens)
	c.totalCostCents += costCents
	return costCents
}

// TotalCostCents returns the cumulative cost in US cents.
func (c *CostEstimator) TotalCostCents() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalCostCents
}

// Summary returns a human-readable cost summary.
func (c *CostEstimator) Summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.limitCents == 0 {
		return fmt.Sprintf("total cost: $%.4f (unlimited)", c.totalCostCents/100)
	}
	return fmt.Sprintf("total cost: $%.4f / $%.4f limit", c.totalCostCents/100, c.limitCents/100)
}

// estimateCostCentsLocked estimates cost in US cents. Caller must hold mu.
func (c *CostEstimator) estimateCostCentsLocked(model string, inputTokens, outputTokens int) float64 {
	pricing := c.lookupPricingLocked(model)

	inputCost := float64(inputTokens) * pricing.InputCostPerMillion / 1_000_000
	outputCost := float64(outputTokens) * pricing.OutputCostPerMillion / 1_000_000

	// Convert dollars to cents
	return (inputCost + outputCost) * 100
}

// lookupPricingLocked finds pricing for a model. Caller must hold mu.
// Falls back to prefix matching for versioned model names.
func (c *CostEstimator) lookupPricingLocked(model string) ModelPricing {
	// Exact match first
	if p, ok := c.pricing[model]; ok {
		return p
	}

	// Prefix match for versioned names (e.g., "claude-sonnet-4-20250514" matches "claude-sonnet")
	for name, p := range c.pricing {
		if strings.HasPrefix(model, name) || strings.HasPrefix(name, model) {
			return p
		}
	}

	// Unknown model — use conservative default pricing
	return ModelPricing{
		InputCostPerMillion:  5.0,
		OutputCostPerMillion: 15.0,
	}
}
