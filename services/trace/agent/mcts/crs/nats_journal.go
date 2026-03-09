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
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// CRS-27a: Prometheus metrics for NATS journal operations.
var (
	natsJournalAppendTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_nats_journal_append_total",
		Help: "Total NATS journal append operations by delta type and status",
	}, []string{"delta_type", "status"})

	natsJournalAppendDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crs_nats_journal_append_duration_seconds",
		Help:    "NATS journal append duration in seconds",
		Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
	}, []string{"status"})

	natsJournalReplayTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_nats_journal_replay_total",
		Help: "Total NATS journal replay operations by status",
	}, []string{"status"})

	natsJournalReplayDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crs_nats_journal_replay_duration_seconds",
		Help:    "NATS journal replay duration in seconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
	}, []string{"status"})

	natsJournalReplayDeltas = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "crs_nats_journal_replay_deltas",
		Help:    "Number of deltas returned per replay operation",
		Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})

	natsJournalCheckpointTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_nats_journal_checkpoint_total",
		Help: "Total NATS journal checkpoint operations by status",
	}, []string{"status"})

	natsJournalCorruptedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_nats_journal_corrupted_total",
		Help: "Total corrupted deltas encountered by operation",
	}, []string{"operation"})

	natsJournalDegraded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "crs_nats_journal_degraded",
		Help: "Whether NATS journal is in degraded mode (1=degraded, 0=healthy)",
	})

	natsJournalSizeBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "crs_nats_journal_size_bytes",
		Help: "Current total size of NATS journal in bytes",
	})
)

// NATSJournalConfig configures the NATS JetStream journal.
//
// Description:
//
//	Contains settings for NATS-backed CRS delta journaling. Uses JetStream
//	for durable, observable storage with built-in sequence numbering.
//
// Inputs:
//
//	SessionID - Unique session identifier for subject scoping.
//	JS - JetStream context obtained from a NATS client.
//	StreamName - Name of the JetStream stream (e.g., "CRS_DELTAS").
//
// Limitations:
//
//   - Requires an active NATS connection with JetStream enabled.
//
// Assumptions:
//
//   - The stream has already been created (via storage/nats.Client.EnsureStream).
type NATSJournalConfig struct {
	// SessionID scopes this journal to a specific session.
	// Required. Used in NATS subject names for isolation.
	SessionID string

	// JS is the JetStream context for publishing and subscribing.
	// Required. Must not be nil.
	JS nats.JetStreamContext

	// StreamName is the JetStream stream name.
	// Required. Must match the stream created by EnsureStream.
	StreamName string

	// MaxJournalBytes limits total journal size. 0 means no limit.
	MaxJournalBytes int64

	// AllowDegraded allows journal to continue in degraded mode
	// if NATS becomes unavailable after initial connection.
	AllowDegraded bool

	// SkipCorruptedDeltas skips corrupted deltas during replay
	// instead of returning an error.
	SkipCorruptedDeltas bool

	// Logger is the structured logger.
	// If nil, slog.Default() is used.
	Logger *slog.Logger
}

// NATSJournal implements the Journal interface using NATS JetStream.
//
// Description:
//
//	Replaces BadgerJournal with NATS JetStream for CRS delta persistence.
//	Each delta is published to a subject scoped by session ID, encoded as
//	[4-byte CRC32][gob-encoded Delta]. NATS message sequence numbers
//	replace manual seqNum counters. Checkpoints are stored as separate
//	messages containing the sequence number up to which deltas can be purged.
//
// Thread Safety: Safe for concurrent use from multiple goroutines.
type NATSJournal struct {
	js     nats.JetStreamContext
	config NATSJournalConfig
	logger *slog.Logger

	seqNum         atomic.Uint64
	totalBytes     atomic.Int64
	corruptedCount atomic.Int64
	lastCheckpoint atomic.Int64 // timestamp (Unix ms) of last checkpoint, NOT the seq number
	degraded       atomic.Bool
	closed         atomic.Bool

	mu sync.RWMutex
}

// NewNATSJournal creates a new NATS JetStream journal.
//
// Description:
//
//	Initializes a journal backed by NATS JetStream. The stream must already
//	exist (created by storage/nats.Client.EnsureStream). The journal
//	initializes its sequence number from the latest message in the stream.
//
// Inputs:
//
//   - config: Journal configuration. SessionID and JS are required.
//
// Outputs:
//
//   - *NATSJournal: Configured journal ready for use.
//   - error: Non-nil if configuration is invalid or stream is inaccessible.
//
// Thread Safety: Safe for concurrent use after creation.
func NewNATSJournal(config NATSJournalConfig) (*NATSJournal, error) {
	if config.SessionID == "" {
		return nil, errors.New("nats journal: SessionID is required")
	}
	if config.JS == nil {
		return nil, errors.New("nats journal: JS context is required")
	}
	if config.StreamName == "" {
		return nil, errors.New("nats journal: StreamName is required")
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	j := &NATSJournal{
		js:     config.JS,
		config: config,
		logger: config.Logger.With(
			slog.String("component", "nats_journal"),
			slog.String("session_id", config.SessionID),
		),
	}

	// Initialize sequence number from existing messages
	if err := j.initSeqNum(); err != nil {
		if config.AllowDegraded {
			j.logger.Warn("CRS-27: NATS stream unavailable, operating in degraded mode",
				slog.String("error", err.Error()),
			)
			j.degraded.Store(true)
			natsJournalDegraded.Set(1)
			return j, nil
		}
		return nil, fmt.Errorf("nats journal: init seq num: %w", err)
	}

	j.logger.Info("CRS-27: NATS journal opened",
		slog.String("stream", config.StreamName),
		slog.Uint64("last_seq_num", j.seqNum.Load()),
	)

	return j, nil
}

// deltaSubject returns the NATS subject for delta messages.
func (j *NATSJournal) deltaSubject() string {
	return fmt.Sprintf("crs.%s.delta", j.config.SessionID)
}

// checkpointSubject returns the NATS subject for checkpoint messages.
func (j *NATSJournal) checkpointSubject() string {
	return fmt.Sprintf("crs.%s.checkpoint", j.config.SessionID)
}

// initSeqNum reads the latest message sequence from the stream.
func (j *NATSJournal) initSeqNum() error {
	subject := j.deltaSubject()

	// Try to get the last message on this subject
	msg, err := j.js.GetLastMsg(j.config.StreamName, subject)
	if err != nil {
		if errors.Is(err, nats.ErrMsgNotFound) {
			// No messages yet — start at 0
			j.seqNum.Store(0)
			return nil
		}
		return fmt.Errorf("getting last message: %w", err)
	}

	j.seqNum.Store(msg.Sequence)
	return nil
}

// encodeEntry encodes a delta with CRC32 checksum.
// Same wire format as BadgerJournal: [4-byte CRC32][gob-encoded Delta].
func (j *NATSJournal) encodeEntry(delta Delta) ([]byte, error) {
	registerDeltaTypes()

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&delta); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}

	crc := crc32.ChecksumIEEE(buf.Bytes())

	result := make([]byte, 4+buf.Len())
	binary.BigEndian.PutUint32(result[:4], crc)
	copy(result[4:], buf.Bytes())

	return result, nil
}

// decodeEntry decodes a delta and validates CRC32 checksum.
func (j *NATSJournal) decodeEntry(data []byte) (Delta, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("%w: entry too short", ErrJournalCorrupted)
	}

	storedCRC := binary.BigEndian.Uint32(data[:4])
	gobData := data[4:]
	computedCRC := crc32.ChecksumIEEE(gobData)

	if storedCRC != computedCRC {
		return nil, fmt.Errorf("%w: stored=%08x computed=%08x", ErrJournalCorrupted, storedCRC, computedCRC)
	}

	registerDeltaTypes()
	var delta Delta
	dec := gob.NewDecoder(bytes.NewReader(gobData))
	if err := dec.Decode(&delta); err != nil {
		return nil, fmt.Errorf("gob decode: %w", err)
	}

	return delta, nil
}

// Append writes a delta to NATS JetStream with CRC checksum.
//
// Description:
//
//	Publishes a CRC32+gob encoded delta to the session's delta subject.
//	NATS assigns a monotonically increasing sequence number automatically.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//   - delta: The CRS delta to persist. Must not be nil.
//
// Outputs:
//
//   - error: Non-nil if publish fails or context is cancelled.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Append(ctx context.Context, delta Delta) error {
	if ctx == nil {
		return ErrNilContext
	}
	if delta == nil {
		return ErrNilDeltaJournal
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	start := time.Now()
	deltaType := delta.Type().String()

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.Append",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
			attribute.String("delta_type", deltaType),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		span.SetStatus(codes.Error, "degraded mode")
		natsJournalAppendTotal.WithLabelValues(deltaType, "degraded").Inc()
		return ErrJournalDegraded
	}

	if j.config.MaxJournalBytes > 0 && j.totalBytes.Load() >= j.config.MaxJournalBytes {
		span.SetStatus(codes.Error, "journal full")
		natsJournalAppendTotal.WithLabelValues(deltaType, "full").Inc()
		return ErrJournalFull
	}

	data, err := j.encodeEntry(delta)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "encode failed")
		natsJournalAppendTotal.WithLabelValues(deltaType, "error").Inc()
		natsJournalAppendDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return fmt.Errorf("encode entry: %w", err)
	}

	ack, err := j.js.Publish(j.deltaSubject(), data)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish failed")
		natsJournalAppendTotal.WithLabelValues(deltaType, "error").Inc()
		natsJournalAppendDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return fmt.Errorf("publish delta: %w", err)
	}

	j.seqNum.Store(ack.Sequence)
	j.totalBytes.Add(int64(len(data)))

	// CRS-27a: Record metrics
	natsJournalAppendTotal.WithLabelValues(deltaType, "success").Inc()
	natsJournalAppendDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	natsJournalSizeBytes.Set(float64(j.totalBytes.Load()))

	span.SetAttributes(
		attribute.Int64("seq_num", int64(ack.Sequence)),
		attribute.Int("entry_bytes", len(data)),
	)

	j.logger.Debug("delta appended",
		slog.Uint64("seq_num", ack.Sequence),
		slog.String("type", deltaType),
		slog.Int("bytes", len(data)),
	)

	return nil
}

// AppendBatch writes multiple deltas sequentially to NATS JetStream.
//
// Description:
//
//	Publishes deltas one at a time under a mutex for ordering guarantees.
//	NATS JetStream does not support atomic multi-message transactions,
//	but deltas are idempotent on replay so partial batches are acceptable.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//   - deltas: Deltas to persist. Must not be nil or empty.
//
// Outputs:
//
//   - error: Non-nil if any publish fails.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) AppendBatch(ctx context.Context, deltas []Delta) error {
	if ctx == nil {
		return ErrNilContext
	}
	if len(deltas) == 0 {
		return errors.New("deltas must not be empty")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.AppendBatch",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
			attribute.Int("batch_size", len(deltas)),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		span.SetStatus(codes.Error, "degraded mode")
		return ErrJournalDegraded
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	var totalSize int64
	var lastSeq uint64

	for i, delta := range deltas {
		if delta == nil {
			return fmt.Errorf("delta at index %d is nil", i)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		data, err := j.encodeEntry(delta)
		if err != nil {
			return fmt.Errorf("encode delta %d: %w", i, err)
		}

		if j.config.MaxJournalBytes > 0 && j.totalBytes.Load()+totalSize+int64(len(data)) >= j.config.MaxJournalBytes {
			span.SetStatus(codes.Error, "journal full")
			return ErrJournalFull
		}

		ack, err := j.js.Publish(j.deltaSubject(), data)
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("publish delta %d: %w", i, err)
		}

		totalSize += int64(len(data))
		lastSeq = ack.Sequence
	}

	j.seqNum.Store(lastSeq)
	j.totalBytes.Add(totalSize)

	span.SetAttributes(
		attribute.Int64("last_seq", int64(lastSeq)),
		attribute.Int64("total_bytes", totalSize),
	)

	j.logger.Debug("batch appended",
		slog.Int("count", len(deltas)),
		slog.Uint64("last_seq", lastSeq),
		slog.Int64("bytes", totalSize),
	)

	return nil
}

// Replay returns all deltas since last checkpoint with validation.
//
// Description:
//
//	Creates an ordered consumer to fetch all deltas from the checkpoint
//	sequence + 1 to the latest message. Validates CRC on each delta.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//
//   - []Delta: Ordered deltas since last checkpoint. Empty if no journal.
//   - error: Non-nil if read fails or CRC validation errors.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Replay(ctx context.Context) ([]Delta, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if j.closed.Load() {
		return nil, ErrJournalClosed
	}

	start := time.Now()

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.Replay",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		span.SetAttributes(attribute.Bool("degraded", true))
		natsJournalReplayTotal.WithLabelValues("degraded").Inc()
		return []Delta{}, nil
	}

	// Get checkpoint sequence to know where to start
	checkpointSeq := j.getCheckpointSeq()

	subject := j.deltaSubject()
	var deltas []Delta
	corrupted := 0

	// Subscribe to fetch all messages on this subject
	sub, err := j.js.SubscribeSync(subject, nats.OrderedConsumer())
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("subscribe for replay: %w", err)
	}
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		msg, err := sub.NextMsg(1 * time.Second)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break // No more messages
			}
			return nil, fmt.Errorf("fetching message: %w", err)
		}

		// Get message metadata for sequence number
		meta, err := msg.Metadata()
		if err != nil {
			return nil, fmt.Errorf("getting message metadata: %w", err)
		}

		// Skip messages before checkpoint
		if checkpointSeq > 0 && meta.Sequence.Stream <= checkpointSeq {
			continue
		}

		delta, err := j.decodeEntry(msg.Data)
		if err != nil {
			corrupted++
			j.corruptedCount.Add(1)
			natsJournalCorruptedTotal.WithLabelValues("replay").Inc()
			if j.config.SkipCorruptedDeltas {
				j.logger.Warn("CRS-27: Skipping corrupted delta during replay",
					slog.Uint64("seq", meta.Sequence.Stream),
					slog.String("error", err.Error()),
				)
				continue
			}
			span.RecordError(err)
			return nil, fmt.Errorf("decode delta at seq %d: %w", meta.Sequence.Stream, err)
		}

		deltas = append(deltas, delta)
	}

	span.SetAttributes(
		attribute.Int("delta_count", len(deltas)),
		attribute.Int("corrupted_count", corrupted),
	)

	// CRS-27a: Record metrics
	natsJournalReplayTotal.WithLabelValues("success").Inc()
	natsJournalReplayDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	natsJournalReplayDeltas.Observe(float64(len(deltas)))

	j.logger.Info("CRS-27: Replay complete",
		slog.Int("delta_count", len(deltas)),
		slog.Int("corrupted_count", corrupted),
	)

	return deltas, nil
}

// ReplayStream returns a channel for streaming replay.
//
// Description:
//
//	Same as Replay but yields deltas through a channel for low-memory
//	consumption on large journals.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//
//   - <-chan DeltaOrError: Channel yielding deltas or errors.
//   - error: Non-nil if replay cannot start.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) ReplayStream(ctx context.Context) (<-chan DeltaOrError, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	if j.closed.Load() {
		return nil, ErrJournalClosed
	}

	if j.degraded.Load() {
		ch := make(chan DeltaOrError)
		close(ch)
		return ch, nil
	}

	checkpointSeq := j.getCheckpointSeq()
	subject := j.deltaSubject()

	sub, err := j.js.SubscribeSync(subject, nats.OrderedConsumer())
	if err != nil {
		return nil, fmt.Errorf("subscribe for replay stream: %w", err)
	}

	ch := make(chan DeltaOrError, 64)

	go func() {
		defer close(ch)
		defer sub.Unsubscribe()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			msg, err := sub.NextMsg(1 * time.Second)
			if err != nil {
				if errors.Is(err, nats.ErrTimeout) {
					return // No more messages
				}
				ch <- DeltaOrError{Err: fmt.Errorf("fetching message: %w", err)}
				return
			}

			meta, err := msg.Metadata()
			if err != nil {
				ch <- DeltaOrError{Err: fmt.Errorf("getting metadata: %w", err)}
				return
			}

			if checkpointSeq > 0 && meta.Sequence.Stream <= checkpointSeq {
				continue
			}

			delta, err := j.decodeEntry(msg.Data)
			if err != nil {
				j.corruptedCount.Add(1)
				if j.config.SkipCorruptedDeltas {
					ch <- DeltaOrError{Skipped: true, Err: err}
					continue
				}
				ch <- DeltaOrError{Err: err}
				return
			}

			ch <- DeltaOrError{Delta: delta}
		}
	}()

	return ch, nil
}

// Checkpoint marks the current position for journal truncation.
//
// Description:
//
//	Publishes the current sequence number to the checkpoint subject and
//	purges delta messages up to that sequence number.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//
//   - error: Non-nil if checkpoint fails.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Checkpoint(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.Checkpoint",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		span.SetStatus(codes.Error, "degraded mode")
		return ErrJournalDegraded
	}

	currentSeq := j.seqNum.Load()
	if currentSeq == 0 {
		return nil // Nothing to checkpoint
	}

	// Publish checkpoint: payload is the sequence number as 8-byte big-endian
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], currentSeq)

	_, err := j.js.Publish(j.checkpointSubject(), seqBuf[:])
	if err != nil {
		span.RecordError(err)
		natsJournalCheckpointTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("publish checkpoint: %w", err)
	}

	natsJournalCheckpointTotal.WithLabelValues("success").Inc()
	j.lastCheckpoint.Store(time.Now().UnixMilli())

	// Purge delta messages up to checkpoint sequence
	err = j.js.PurgeStream(j.config.StreamName, &nats.StreamPurgeRequest{
		Subject:  j.deltaSubject(),
		Sequence: currentSeq + 1, // Keep currentSeq+1 and above
	})
	if err != nil {
		j.logger.Warn("CRS-27: Failed to purge deltas after checkpoint",
			slog.Uint64("checkpoint_seq", currentSeq),
			slog.String("error", err.Error()),
		)
		// Non-fatal — checkpoint is still recorded
	}

	span.SetAttributes(
		attribute.Int64("checkpoint_seq", int64(currentSeq)),
	)

	j.logger.Info("CRS-27: Checkpoint created",
		slog.Uint64("seq", currentSeq),
	)

	return nil
}

// getCheckpointSeq reads the latest checkpoint sequence number.
func (j *NATSJournal) getCheckpointSeq() uint64 {
	msg, err := j.js.GetLastMsg(j.config.StreamName, j.checkpointSubject())
	if err != nil {
		return 0 // No checkpoint
	}

	if len(msg.Data) < 8 {
		return 0
	}

	return binary.BigEndian.Uint64(msg.Data)
}

// Backup creates a portable backup of the journal.
//
// Description:
//
//	Iterates all delta messages on this session's subject via an ordered
//	consumer and writes them as length-prefixed frames to the writer.
//	Frame format: [4-byte big-endian length][message data].
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//   - w: Writer to receive backup data. Must not be nil.
//
// Outputs:
//
//   - error: Non-nil if backup fails.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Backup(ctx context.Context, w io.Writer) error {
	if ctx == nil {
		return ErrNilContext
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	if j.degraded.Load() {
		return ErrJournalDegraded
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.Backup",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	subject := j.deltaSubject()
	sub, err := j.js.SubscribeSync(subject, nats.OrderedConsumer())
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("subscribe for backup: %w", err)
	}
	defer sub.Unsubscribe()

	var count int
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := sub.NextMsg(1 * time.Second)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break // No more messages
			}
			return fmt.Errorf("fetching message for backup: %w", err)
		}

		// Write length-prefixed frame
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(msg.Data)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			return fmt.Errorf("writing frame length: %w", err)
		}
		if _, err := w.Write(msg.Data); err != nil {
			return fmt.Errorf("writing frame data: %w", err)
		}
		count++
	}

	span.SetAttributes(attribute.Int("message_count", count))

	j.logger.Info("CRS-27: Backup complete",
		slog.Int("message_count", count),
	)

	return nil
}

// Restore loads state from a backup.
//
// Description:
//
//	Reads length-prefixed frames from the reader and publishes each
//	as a delta message to NATS JetStream. This rebuilds the journal
//	state from a portable backup.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//   - r: Reader containing backup data. Must not be nil.
//
// Outputs:
//
//   - error: Non-nil if restore fails.
//
// Limitations:
//
//   - Restore is all-or-nothing: partial restores leave partial data.
//
// Thread Safety: NOT safe for concurrent use. Caller must ensure exclusivity.
func (j *NATSJournal) Restore(ctx context.Context, r io.Reader) error {
	if ctx == nil {
		return ErrNilContext
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	if j.degraded.Load() {
		return ErrJournalDegraded
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "nats_journal.Restore",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	subject := j.deltaSubject()
	var count int
	var lastSeq uint64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read frame length
		var lenBuf [4]byte
		_, err := io.ReadFull(r, lenBuf[:])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break // End of backup
			}
			return fmt.Errorf("reading frame length: %w", err)
		}

		frameLen := binary.BigEndian.Uint32(lenBuf[:])
		if frameLen > 10*1024*1024 { // 10MB sanity limit
			return fmt.Errorf("frame too large: %d bytes", frameLen)
		}

		// Read frame data
		data := make([]byte, frameLen)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("reading frame data: %w", err)
		}

		// Publish to NATS
		ack, err := j.js.Publish(subject, data)
		if err != nil {
			return fmt.Errorf("publishing restored delta %d: %w", count, err)
		}

		lastSeq = ack.Sequence
		count++
	}

	if lastSeq > 0 {
		j.seqNum.Store(lastSeq)
	}

	span.SetAttributes(
		attribute.Int("restored_count", count),
		attribute.Int64("last_seq", int64(lastSeq)),
	)

	j.logger.Info("CRS-27: Restore complete",
		slog.Int("restored_count", count),
		slog.Uint64("last_seq", lastSeq),
	)

	return nil
}

// IsAvailable returns true if the journal is operational.
//
// Description:
//
//	Returns false if the journal is in degraded mode (NATS unavailable).
//
// Outputs:
//
//   - bool: True if journal can accept writes.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) IsAvailable() bool {
	return !j.degraded.Load() && !j.closed.Load()
}

// IsDegraded returns true if the journal is in degraded mode.
//
// Description:
//
//	Degraded mode occurs when NATS becomes unavailable after initial
//	connection. In degraded mode, writes return ErrJournalDegraded
//	and replays return empty results.
//
// Outputs:
//
//   - bool: True if operating with reduced durability.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) IsDegraded() bool {
	return j.degraded.Load()
}

// Sync is a no-op for NATS JetStream (file storage is durable).
//
// Description:
//
//	JetStream with file storage is already durable on publish acknowledgment.
//	This method exists only to satisfy the Journal interface.
//
// Outputs:
//
//   - error: Always nil.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Sync() error {
	return nil
}

// Close marks the journal as closed.
//
// Description:
//
//	Marks the journal as closed so future operations return ErrJournalClosed.
//	Does not close the underlying NATS connection (that's managed by the Client).
//
// Outputs:
//
//   - error: Always nil.
//
// Thread Safety: Safe for concurrent use. Idempotent.
func (j *NATSJournal) Close() error {
	j.closed.Store(true)
	j.logger.Info("CRS-27: NATS journal closed")
	return nil
}

// Stats returns journal statistics.
//
// Description:
//
//	Returns current journal metrics including delta count, byte size,
//	sequence number, and degraded status. For NATS journals, attempts
//	to query JetStream stream info for accurate counts.
//
// Outputs:
//
//   - JournalStats: Current journal statistics.
//
// Thread Safety: Safe for concurrent use.
func (j *NATSJournal) Stats() JournalStats {
	stats := JournalStats{
		LastSeqNum:     j.seqNum.Load(),
		TotalBytes:     j.totalBytes.Load(),
		CorruptedCount: j.corruptedCount.Load(),
		Degraded:       j.degraded.Load(),
	}

	checkpoint := j.lastCheckpoint.Load()
	if checkpoint > 0 {
		stats.LastCheckpoint = time.UnixMilli(checkpoint)
	}

	// Try to get accurate count from stream info for this session's subject.
	// StreamInfo().State.Subjects contains per-subject counts when subject
	// count is below the server threshold (default 256).
	if !j.degraded.Load() {
		info, err := j.js.StreamInfo(j.config.StreamName)
		if err == nil {
			if count, ok := info.State.Subjects[j.deltaSubject()]; ok {
				stats.TotalDeltas = int64(count)
			} else if info.State.NumSubjects == 0 && info.State.Msgs == 0 {
				stats.TotalDeltas = 0
			} else {
				// Subjects map not populated (too many subjects) or
				// this subject not present — fall back to seq num estimate.
				stats.TotalDeltas = int64(j.seqNum.Load())
			}
		} else {
			stats.TotalDeltas = int64(j.seqNum.Load())
		}
	}

	return stats
}
