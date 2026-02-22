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
	"log/slog"
)

// CloudLifecycleAdapter is a no-op lifecycle manager for cloud providers.
//
// Description:
//
//	Cloud providers (Anthropic, OpenAI, Gemini) don't need explicit model
//	loading/unloading. WarmModel logs a confirmation, UnloadModel is a no-op.
//	IsLocal returns false so callers skip the re-warm dance.
//
// Thread Safety: CloudLifecycleAdapter is safe for concurrent use.
type CloudLifecycleAdapter struct {
	provider string
}

// NewCloudLifecycleAdapter creates a new CloudLifecycleAdapter.
//
// Inputs:
//   - provider: The provider name (for logging).
//
// Outputs:
//   - *CloudLifecycleAdapter: The configured adapter.
func NewCloudLifecycleAdapter(provider string) *CloudLifecycleAdapter {
	return &CloudLifecycleAdapter{provider: provider}
}

// WarmModel is a no-op for cloud providers. Logs the action for visibility.
func (a *CloudLifecycleAdapter) WarmModel(ctx context.Context, model string, opts WarmupOptions) error {
	slog.Info("Cloud provider warmup (no-op)",
		slog.String("provider", a.provider),
		slog.String("model", model),
	)
	return nil
}

// UnloadModel is a no-op for cloud providers.
func (a *CloudLifecycleAdapter) UnloadModel(ctx context.Context, model string) error {
	return nil
}

// IsLocal returns false because cloud providers don't manage local GPU resources.
func (a *CloudLifecycleAdapter) IsLocal() bool {
	return false
}
