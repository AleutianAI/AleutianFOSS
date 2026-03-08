// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package nats

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEmbeddedNATS starts an embedded NATS server with JetStream enabled for testing.
//
// Description:
//
//	Creates an in-memory NATS server with JetStream. The server is automatically
//	stopped when the test completes via t.Cleanup.
//
// Inputs:
//
//   - t: Test instance for cleanup registration.
//
// Outputs:
//
//   - string: NATS client URL (e.g., "nats://127.0.0.1:PORT").
func startEmbeddedNATS(t *testing.T) string {
	t.Helper()

	opts := &natsserver.Options{
		Host:               "127.0.0.1",
		Port:               -1, // Random available port
		NoLog:              true,
		NoSigs:             true,
		JetStream:          true,
		StoreDir:           t.TempDir(),
		JetStreamMaxMemory: 64 * 1024 * 1024, // 64MB
	}

	srv, err := natsserver.NewServer(opts)
	require.NoError(t, err, "failed to create embedded NATS server")

	srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not start in time")
	}

	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})

	return srv.ClientURL()
}

func TestNewClient_Success(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer client.Close()

	assert.True(t, client.IsConnected())
	assert.NotNil(t, client.JetStream())
}

func TestNewClient_MissingURL(t *testing.T) {
	_, err := NewClient(Config{
		StreamName: "CRS_DELTAS",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL is required")
}

func TestNewClient_MissingStreamName(t *testing.T) {
	_, err := NewClient(Config{
		URL: "nats://localhost:4222",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StreamName is required")
}

func TestNewClient_UnreachableServer(t *testing.T) {
	_, err := NewClient(Config{
		URL:            "nats://127.0.0.1:1", // unreachable
		StreamName:     "CRS_DELTAS",
		ConnectTimeout: 500 * time.Millisecond,
		MaxReconnects:  IntPtr(0),
	})
	require.Error(t, err)
}

func TestClient_EnsureStream_Idempotent(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer client.Close()

	// Second call should be a no-op
	err = client.EnsureStream(context.Background())
	require.NoError(t, err)
}

func TestClient_HealthCheck(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer client.Close()

	err = client.HealthCheck()
	require.NoError(t, err)
}

func TestClient_Close_Idempotent(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)

	// Wait briefly for drain to complete (drain is async)
	time.Sleep(100 * time.Millisecond)

	// Second close should be fine
	err = client.Close()
	require.NoError(t, err)
}

func TestClient_StreamExists(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer client.Close()

	// Verify stream was created with correct config
	info, err := client.JetStream().StreamInfo("CRS_DELTAS")
	require.NoError(t, err)
	assert.Equal(t, "CRS_DELTAS", info.Config.Name)
	assert.Equal(t, int64(100_000), info.Config.MaxMsgsPerSubject)
	assert.Equal(t, 7*24*time.Hour, info.Config.MaxAge)
}

func TestClient_Conn(t *testing.T) {
	url := startEmbeddedNATS(t)

	client, err := NewClient(Config{
		URL:        url,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer client.Close()

	conn := client.Conn()
	assert.NotNil(t, conn)
	assert.True(t, conn.IsConnected())
}
