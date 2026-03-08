// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEmbeddedNATSForJournal starts an embedded NATS server with JetStream for testing.
func startEmbeddedNATSForJournal(t *testing.T) nats.JetStreamContext {
	t.Helper()

	opts := &natsserver.Options{
		Host:               "127.0.0.1",
		Port:               -1,
		NoLog:              true,
		NoSigs:             true,
		JetStream:          true,
		StoreDir:           t.TempDir(),
		JetStreamMaxMemory: 64 * 1024 * 1024,
	}

	srv, err := natsserver.NewServer(opts)
	require.NoError(t, err)

	srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not start in time")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})

	nc, err := nats.Connect(srv.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create the CRS_DELTAS stream
	_, err = js.AddStream(&nats.StreamConfig{
		Name:              "CRS_DELTAS",
		Subjects:          []string{"crs.*.delta", "crs.*.checkpoint"},
		Storage:           nats.FileStorage,
		MaxMsgsPerSubject: 100_000,
		MaxAge:            7 * 24 * time.Hour,
		Replicas:          1,
	})
	require.NoError(t, err)

	return js
}

// makeTestDelta creates a test delta of the given type.
func makeTestDelta(t *testing.T, deltaType DeltaType) Delta {
	t.Helper()
	switch deltaType {
	case DeltaTypeProof:
		return NewProofDelta(SignalSourceSoft, map[string]ProofNumber{
			"test-symbol-1": {Status: ProofStatusUnknown, Proof: 1, UpdatedAt: time.Now().UnixMilli()},
		})
	case DeltaTypeConstraint:
		d := NewConstraintDelta(SignalSourceSoft)
		d.Add = []Constraint{{
			ID:   "test-constraint-1",
			Type: ConstraintTypeMutualExclusion,
		}}
		return d
	case DeltaTypeSimilarity:
		d := NewSimilarityDelta(SignalSourceSoft)
		d.Updates[[2]string{"sym-a", "sym-b"}] = 0.85
		return d
	default:
		return NewProofDelta(SignalSourceSoft, map[string]ProofNumber{
			"default-symbol": {Status: ProofStatusUnknown, Proof: 1, UpdatedAt: time.Now().UnixMilli()},
		})
	}
}

func TestNATSJournal_NewNATSJournal_Success(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-session",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	assert.True(t, journal.IsAvailable())
	assert.False(t, journal.IsDegraded())
}

func TestNATSJournal_NewNATSJournal_MissingSessionID(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)
	_, err := NewNATSJournal(NATSJournalConfig{
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SessionID")
}

func TestNATSJournal_NewNATSJournal_MissingJS(t *testing.T) {
	_, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-session",
		StreamName: "CRS_DELTAS",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JS context")
}

func TestNATSJournal_AppendReplay_Roundtrip(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-roundtrip",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Append deltas of different types
	deltaTypes := []DeltaType{DeltaTypeProof, DeltaTypeConstraint, DeltaTypeSimilarity}
	for _, dt := range deltaTypes {
		delta := makeTestDelta(t, dt)
		err := journal.Append(ctx, delta)
		require.NoError(t, err, "failed to append delta type %s", dt)
	}

	// Replay and verify
	deltas, err := journal.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, 3)
	assert.Equal(t, DeltaTypeProof, deltas[0].Type())
	assert.Equal(t, DeltaTypeConstraint, deltas[1].Type())
	assert.Equal(t, DeltaTypeSimilarity, deltas[2].Type())
}

func TestNATSJournal_AppendBatch_Ordering(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-batch",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Create batch of deltas
	batch := []Delta{
		makeTestDelta(t, DeltaTypeProof),
		makeTestDelta(t, DeltaTypeConstraint),
		makeTestDelta(t, DeltaTypeSimilarity),
	}

	err = journal.AppendBatch(ctx, batch)
	require.NoError(t, err)

	// Replay and verify ordering
	deltas, err := journal.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, 3)
	assert.Equal(t, DeltaTypeProof, deltas[0].Type())
	assert.Equal(t, DeltaTypeConstraint, deltas[1].Type())
	assert.Equal(t, DeltaTypeSimilarity, deltas[2].Type())
}

func TestNATSJournal_ReplayStream(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-stream",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Append deltas
	for i := 0; i < 5; i++ {
		err := journal.Append(ctx, makeTestDelta(t, DeltaTypeProof))
		require.NoError(t, err)
	}

	// Stream replay
	ch, err := journal.ReplayStream(ctx)
	require.NoError(t, err)

	var count int
	for doe := range ch {
		require.NoError(t, doe.Err)
		assert.NotNil(t, doe.Delta)
		count++
	}
	assert.Equal(t, 5, count)
}

func TestNATSJournal_Checkpoint_PurgeVerification(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-checkpoint",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Append 5 deltas
	for i := 0; i < 5; i++ {
		err := journal.Append(ctx, makeTestDelta(t, DeltaTypeProof))
		require.NoError(t, err)
	}

	// Checkpoint (purges all 5)
	err = journal.Checkpoint(ctx)
	require.NoError(t, err)

	// Append 3 more after checkpoint
	for i := 0; i < 3; i++ {
		err := journal.Append(ctx, makeTestDelta(t, DeltaTypeConstraint))
		require.NoError(t, err)
	}

	// Replay should only return the 3 post-checkpoint deltas
	deltas, err := journal.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, 3)
	for _, d := range deltas {
		assert.Equal(t, DeltaTypeConstraint, d.Type())
	}
}

func TestNATSJournal_BackupRestore_Roundtrip(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	// Create source journal and append data
	src, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-backup-src",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer src.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		err := src.Append(ctx, makeTestDelta(t, DeltaTypeProof))
		require.NoError(t, err)
	}

	// Backup to buffer
	var buf bytes.Buffer
	err = src.Backup(ctx, &buf)
	require.NoError(t, err)
	assert.Greater(t, buf.Len(), 0)

	// Create destination journal and restore
	dst, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-backup-dst",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer dst.Close()

	err = dst.Restore(ctx, &buf)
	require.NoError(t, err)

	// Verify restored data
	deltas, err := dst.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, 5)
}

func TestNATSJournal_CRCCorruption(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:           "test-corruption",
		JS:                  js,
		StreamName:          "CRS_DELTAS",
		SkipCorruptedDeltas: false,
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Publish a corrupted message directly
	_, err = js.Publish("crs.test-corruption.delta", []byte{0x00, 0x01, 0x02, 0x03, 0xFF})
	require.NoError(t, err)

	// Replay should fail with corruption error
	_, err = journal.Replay(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupted")
}

func TestNATSJournal_CRCCorruption_SkipEnabled(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:           "test-skip-corrupt",
		JS:                  js,
		StreamName:          "CRS_DELTAS",
		SkipCorruptedDeltas: true,
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Add valid delta, then corrupted, then valid
	err = journal.Append(ctx, makeTestDelta(t, DeltaTypeProof))
	require.NoError(t, err)

	_, err = js.Publish("crs.test-skip-corrupt.delta", []byte{0x00, 0x01, 0x02, 0x03, 0xFF})
	require.NoError(t, err)

	err = journal.Append(ctx, makeTestDelta(t, DeltaTypeConstraint))
	require.NoError(t, err)

	// Should skip corrupted and return 2 valid deltas
	deltas, err := journal.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, 2)
}

func TestNATSJournal_ConcurrentAccess(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-concurrent",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()
	numGoroutines := 10
	deltasPerGoroutine := 5

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*deltasPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < deltasPerGoroutine; i++ {
				if err := journal.Append(ctx, makeTestDelta(t, DeltaTypeProof)); err != nil {
					errCh <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent append error: %v", err)
	}

	// Verify all deltas were persisted
	deltas, err := journal.Replay(ctx)
	require.NoError(t, err)
	assert.Len(t, deltas, numGoroutines*deltasPerGoroutine)
}

func TestNATSJournal_Stats(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-stats",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	ctx := context.Background()

	// Initial stats
	stats := journal.Stats()
	assert.Equal(t, uint64(0), stats.LastSeqNum)
	assert.False(t, stats.Degraded)

	// Append and check stats
	err = journal.Append(ctx, makeTestDelta(t, DeltaTypeProof))
	require.NoError(t, err)

	stats = journal.Stats()
	assert.Greater(t, stats.LastSeqNum, uint64(0))
	assert.Greater(t, stats.TotalBytes, int64(0))
}

func TestNATSJournal_ClosedJournal(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-closed",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)

	err = journal.Close()
	require.NoError(t, err)

	ctx := context.Background()

	// All operations should return ErrJournalClosed
	err = journal.Append(ctx, makeTestDelta(t, DeltaTypeProof))
	assert.ErrorIs(t, err, ErrJournalClosed)

	err = journal.AppendBatch(ctx, []Delta{makeTestDelta(t, DeltaTypeProof)})
	assert.ErrorIs(t, err, ErrJournalClosed)

	_, err = journal.Replay(ctx)
	assert.ErrorIs(t, err, ErrJournalClosed)

	err = journal.Checkpoint(ctx)
	assert.ErrorIs(t, err, ErrJournalClosed)
}

func TestNATSJournal_NilContext(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-nil-ctx",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	err = journal.Append(nil, makeTestDelta(t, DeltaTypeProof)) //nolint:staticcheck
	assert.ErrorIs(t, err, ErrNilContext)

	_, err = journal.Replay(nil) //nolint:staticcheck
	assert.ErrorIs(t, err, ErrNilContext)
}

func TestNATSJournal_NilDelta(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-nil-delta",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	err = journal.Append(context.Background(), nil)
	assert.ErrorIs(t, err, ErrNilDeltaJournal)
}

func TestNATSJournal_Sync_NoOp(t *testing.T) {
	js := startEmbeddedNATSForJournal(t)

	journal, err := NewNATSJournal(NATSJournalConfig{
		SessionID:  "test-sync",
		JS:         js,
		StreamName: "CRS_DELTAS",
	})
	require.NoError(t, err)
	defer journal.Close()

	err = journal.Sync()
	assert.NoError(t, err)
}
