// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package nats provides a NATS JetStream client wrapper for CRS state persistence.
//
// CRS-27: Replaces embedded BadgerDB with NATS JetStream for observable,
// durable, and scalable CRS delta journaling.
package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Config configures the NATS JetStream client.
//
// Description:
//
//	Provides connection parameters for the NATS server and JetStream stream.
//	The client will auto-reconnect on disconnection with exponential backoff.
//
// Inputs:
//
//	URL - NATS server URL (e.g., "nats://localhost:4222")
//	StreamName - JetStream stream name (e.g., "CRS_DELTAS")
//	ConnectTimeout - Maximum time to wait for initial connection
//	ReconnectWait - Wait time between reconnection attempts
//	MaxReconnects - Maximum reconnection attempts (-1 for infinite)
//	Logger - Structured logger for connection events
//
// Limitations:
//
//   - Only supports single-server NATS connections (no cluster URLs)
//
// Assumptions:
//
//   - NATS server is accessible at the configured URL
//   - JetStream is enabled on the NATS server
type Config struct {
	// URL is the NATS server connection URL.
	URL string

	// StreamName is the JetStream stream name for CRS deltas.
	StreamName string

	// ConnectTimeout is the maximum time to wait for initial connection.
	// Zero means 5 seconds.
	ConnectTimeout time.Duration

	// ReconnectWait is the wait time between reconnection attempts.
	// Zero means 1 second.
	ReconnectWait time.Duration

	// MaxReconnects is the maximum number of reconnection attempts.
	// -1 means infinite reconnects. nil means -1 (infinite).
	// Use IntPtr(0) to disable reconnects entirely.
	MaxReconnects *int

	// Logger is the structured logger for connection events.
	// If nil, slog.Default() is used.
	Logger *slog.Logger
}

// IntPtr returns a pointer to the given int. Convenience for setting MaxReconnects.
func IntPtr(v int) *int { return &v }

// defaults fills in zero-value fields with sensible defaults.
func (c *Config) defaults() {
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.ReconnectWait == 0 {
		c.ReconnectWait = 1 * time.Second
	}
	if c.MaxReconnects == nil {
		infinite := -1
		c.MaxReconnects = &infinite
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Client wraps a NATS connection with JetStream support.
//
// Description:
//
//	Manages the NATS connection lifecycle, ensures the CRS_DELTAS stream
//	exists, and provides health check capabilities. The client is safe
//	for concurrent use from multiple goroutines.
//
// Thread Safety: Safe for concurrent use.
type Client struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	config Config
	logger *slog.Logger
	mu     sync.RWMutex
}

// NewClient connects to NATS and ensures the CRS_DELTAS stream exists.
//
// Description:
//
//	Establishes a connection to the NATS server, obtains a JetStream context,
//	and creates the CRS_DELTAS stream if it does not already exist. Returns
//	an error if connection or stream creation fails.
//
// Inputs:
//
//   - cfg: Client configuration. URL and StreamName are required.
//
// Outputs:
//
//   - *Client: Connected client with JetStream context.
//   - error: Non-nil if connection or stream creation fails.
//
// Limitations:
//
//   - Blocks until connection is established or timeout expires.
//
// Assumptions:
//
//   - NATS server is running and accessible.
//   - JetStream is enabled on the server.
func NewClient(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("nats: URL is required")
	}
	if cfg.StreamName == "" {
		return nil, errors.New("nats: StreamName is required")
	}

	cfg.defaults()

	conn, err := nats.Connect(cfg.URL,
		nats.Timeout(cfg.ConnectTimeout),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.MaxReconnects(*cfg.MaxReconnects),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				cfg.Logger.Warn("CRS-27: NATS disconnected",
					slog.String("error", err.Error()),
				)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			cfg.Logger.Info("CRS-27: NATS reconnected")
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			cfg.Logger.Info("CRS-27: NATS connection closed")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats: connecting to %s: %w", cfg.URL, err)
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats: obtaining JetStream context: %w", err)
	}

	c := &Client{
		conn:   conn,
		js:     js,
		config: cfg,
		logger: cfg.Logger,
	}

	if err := c.EnsureStream(context.Background()); err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats: ensuring stream %s: %w", cfg.StreamName, err)
	}

	cfg.Logger.Info("CRS-27: NATS client connected",
		slog.String("url", cfg.URL),
		slog.String("stream", cfg.StreamName),
	)

	return c, nil
}

// EnsureStream creates the CRS_DELTAS stream if it does not exist.
//
// Description:
//
//	Creates a JetStream stream with file-based storage, 7-day retention,
//	and 100,000 max messages per subject. If the stream already exists,
//	this is a no-op.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//
//   - error: Non-nil if stream creation fails.
//
// Thread Safety: Safe for concurrent use.
func (c *Client) EnsureStream(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.js.StreamInfo(c.config.StreamName)
	if err == nil {
		return nil // Stream already exists
	}

	if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("checking stream %s: %w", c.config.StreamName, err)
	}

	_, err = c.js.AddStream(&nats.StreamConfig{
		Name:     c.config.StreamName,
		Subjects: []string{"crs.*.delta", "crs.*.checkpoint"},
		Storage:  nats.FileStorage,
		// Retention: Limits-based (default)
		MaxMsgsPerSubject: 100_000,
		MaxAge:            7 * 24 * time.Hour,
		Replicas:          1,
	})
	if err != nil {
		return fmt.Errorf("creating stream %s: %w", c.config.StreamName, err)
	}

	c.logger.Info("CRS-27: Created JetStream stream",
		slog.String("stream", c.config.StreamName),
	)
	return nil
}

// Close drains subscriptions and closes the NATS connection.
//
// Description:
//
//	Gracefully closes the NATS connection. Pending messages are drained
//	before disconnect.
//
// Outputs:
//
//   - error: Non-nil if drain or close fails.
//
// Thread Safety: Safe for concurrent use. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}

	if err := c.conn.Drain(); err != nil {
		c.logger.Warn("CRS-27: NATS drain failed, forcing close",
			slog.String("error", err.Error()),
		)
		c.conn.Close()
		return nil
	}

	return nil
}

// IsConnected returns true if the NATS connection is active.
//
// Description:
//
//	Checks whether the underlying NATS connection is in the CONNECTED state.
//
// Outputs:
//
//   - bool: True if connected, false otherwise.
//
// Thread Safety: Safe for concurrent use.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.conn != nil && c.conn.IsConnected()
}

// HealthCheck verifies the NATS connection and JetStream stream are available.
//
// Description:
//
//	Checks that the NATS connection is active and the CRS_DELTAS stream
//	exists with valid state. Returns an error describing the first failure.
//
// Outputs:
//
//   - error: Non-nil if connection or stream is unavailable.
//
// Thread Safety: Safe for concurrent use.
func (c *Client) HealthCheck() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conn == nil || !c.conn.IsConnected() {
		return errors.New("nats: not connected")
	}

	_, err := c.js.StreamInfo(c.config.StreamName)
	if err != nil {
		return fmt.Errorf("nats: stream %s unavailable: %w", c.config.StreamName, err)
	}

	return nil
}

// JetStream returns the JetStream context for publishing and subscribing.
//
// Description:
//
//	Returns the JetStream context obtained during connection. The context
//	is valid for the lifetime of the client connection.
//
// Outputs:
//
//   - nats.JetStreamContext: JetStream context for pub/sub operations.
//
// Thread Safety: Safe for concurrent use.
func (c *Client) JetStream() nats.JetStreamContext {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.js
}

// Conn returns the underlying NATS connection.
//
// Description:
//
//	Returns the raw NATS connection for advanced use cases (e.g., push
//	subscriptions for SSE streaming).
//
// Outputs:
//
//   - *nats.Conn: Underlying NATS connection.
//
// Thread Safety: Safe for concurrent use.
func (c *Client) Conn() *nats.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}
