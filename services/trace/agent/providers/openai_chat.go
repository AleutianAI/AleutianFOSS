// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// OpenAIChatAdapter wraps OpenAIClient to implement ChatClient.
//
// Description:
//
//	Delegates chat requests to the OpenAI API via the existing OpenAIClient.
//	Ollama-specific options (KeepAlive, NumCtx) are ignored.
//
// Thread Safety: OpenAIChatAdapter is safe for concurrent use.
type OpenAIChatAdapter struct {
	client *llm.OpenAIClient
}

// NewOpenAIChatAdapter creates a new OpenAIChatAdapter.
//
// Inputs:
//   - client: The OpenAIClient to wrap. Must not be nil.
//
// Outputs:
//   - *OpenAIChatAdapter: The configured adapter.
func NewOpenAIChatAdapter(client *llm.OpenAIClient) *OpenAIChatAdapter {
	return &OpenAIChatAdapter{client: client}
}

// Chat implements ChatClient by delegating to OpenAIClient.Chat.
func (a *OpenAIChatAdapter) Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error) {
	if a.client == nil {
		return "", fmt.Errorf("OpenAI client is nil")
	}

	// Create OTel span
	ctx, span := otel.Tracer(chatTracerName).Start(ctx, "providers.OpenAIChatAdapter.Chat",
		trace.WithAttributes(
			attribute.String("provider", "openai"),
			attribute.Int("message_count", len(messages)),
			attribute.Float64("temperature", opts.Temperature),
		),
	)
	defer span.End()

	params := llm.GenerationParams{}
	if opts.Temperature >= 0 {
		temp := float32(opts.Temperature)
		params.Temperature = &temp
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = &opts.MaxTokens
	}

	startTime := time.Now()
	result, err := a.client.Chat(ctx, messages, params)
	duration := time.Since(startTime)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recordChatMetrics("openai", duration, err)
		return "", err
	}

	recordChatMetrics("openai", duration, nil)
	return result, nil
}
