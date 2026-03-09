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

	"github.com/AleutianAI/AleutianFOSS/services/trace/storage/badger"
	dgbadger "github.com/dgraph-io/badger/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Journal Errors
// -----------------------------------------------------------------------------

var (
	// ErrJournalClosed is returned when operations are called on a closed journal.
	ErrJournalClosed = errors.New("journal is closed")

	// ErrJournalCorrupted is returned when journal data fails integrity check.
	ErrJournalCorrupted = errors.New("journal entry corrupted (CRC mismatch)")

	// ErrJournalFull is returned when journal exceeds MaxJournalBytes.
	ErrJournalFull = errors.New("journal size limit exceeded")

	// ErrJournalDegraded is returned when journal is operating in degraded mode.
	ErrJournalDegraded = errors.New("journal operating in degraded mode")

	// ErrJournalSequenceGap is returned when replay detects sequence number gaps.
	ErrJournalSequenceGap = errors.New("journal sequence number gap detected")

	// ErrNilDeltaJournal is returned when attempting to append nil delta.
	ErrNilDeltaJournal = errors.New("delta must not be nil")
)

// -----------------------------------------------------------------------------
// Journal Interface
// -----------------------------------------------------------------------------

// JournalConfig configures journal behavior.
//
// Description:
//
//	Contains all settings for journal operation including durability,
//	size limits, and degradation behavior.
type JournalConfig struct {
	// Path is the directory for BadgerDB files.
	// Required for persistent mode.
	Path string

	// SessionID scopes this journal to a specific session.
	// Required. Used as key prefix for isolation.
	SessionID string

	// SyncWrites enables synchronous writes for durability.
	// MUST be true for WAL correctness. Default: true.
	SyncWrites bool

	// MaxJournalBytes triggers checkpoint when exceeded.
	// Default: 1GB. Set to 0 to disable limit.
	MaxJournalBytes int64

	// AllowDegraded allows startup even if BadgerDB unavailable.
	// When true, journal operates in memory-only mode with reduced durability.
	// Default: false (strict mode).
	AllowDegraded bool

	// SkipCorruptedDeltas continues replay past corrupted entries.
	// Corrupted entries are logged and skipped.
	// Default: false (fail fast).
	SkipCorruptedDeltas bool

	// InMemory uses in-memory BadgerDB (for testing).
	// Default: false.
	InMemory bool

	// Logger for journal operations.
	// Default: slog.Default().
	Logger *slog.Logger
}

// DefaultJournalConfig returns sensible defaults for production use.
//
// Outputs:
//
//	JournalConfig - Ready-to-use production configuration.
func DefaultJournalConfig() JournalConfig {
	return JournalConfig{
		SyncWrites:          true,    // WAL requires sync writes
		MaxJournalBytes:     1 << 30, // 1GB
		AllowDegraded:       false,
		SkipCorruptedDeltas: false,
		InMemory:            false,
		Logger:              slog.Default(),
	}
}

// Validate checks if the configuration is valid.
func (c *JournalConfig) Validate() error {
	if c.SessionID == "" {
		return errors.New("session_id must not be empty")
	}
	if !c.InMemory && c.Path == "" {
		return errors.New("path is required for persistent journal")
	}
	if c.MaxJournalBytes < 0 {
		return errors.New("max_journal_bytes must be non-negative")
	}
	return nil
}

// Journal provides crash recovery for CRS via Write-Ahead Logging.
//
// Description:
//
//	Appends deltas synchronously to BadgerDB with CRC checksums.
//	On restart, replays all deltas since last checkpoint to reconstruct state.
//
// Thread Safety: Safe for concurrent use from multiple goroutines.
type Journal interface {
	// Append writes a delta with CRC checksum.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - delta: The CRS delta to persist. Must not be nil.
	//
	// Outputs:
	//   - error: Non-nil if write fails or context cancelled.
	//
	// Performance: ~100-200µs per append (BadgerDB sync write + CRC).
	Append(ctx context.Context, delta Delta) error

	// AppendBatch writes multiple deltas atomically in a single transaction.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - deltas: Deltas to persist. Must not be nil or empty.
	//
	// Outputs:
	//   - error: Non-nil if write fails or context cancelled.
	//
	// Performance: More efficient than individual Append calls.
	AppendBatch(ctx context.Context, deltas []Delta) error

	// Replay returns all deltas since last checkpoint with validation.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//
	// Outputs:
	//   - []Delta: Deltas in order. Empty if no journal exists.
	//   - error: Non-nil if read fails or validation errors (unless SkipCorrupted).
	//
	// Usage: Called once at session start to recover state.
	Replay(ctx context.Context) ([]Delta, error)

	// ReplayStream returns a channel for streaming replay (low memory).
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//
	// Outputs:
	//   - <-chan DeltaOrError: Channel yielding deltas or errors.
	//   - error: Non-nil if replay cannot start.
	//
	// Usage: For large journals where loading all into memory is prohibitive.
	ReplayStream(ctx context.Context) (<-chan DeltaOrError, error)

	// Checkpoint marks current position, enabling journal truncation.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//
	// Outputs:
	//   - error: Non-nil if checkpoint fails.
	//
	// Usage: Called after successful state persistence to Weaviate.
	Checkpoint(ctx context.Context) error

	// IsAvailable returns false if journal is in degraded mode.
	IsAvailable() bool

	// IsDegraded returns true if journal is operating with reduced durability.
	IsDegraded() bool

	// Sync flushes pending writes to disk.
	//
	// Outputs:
	//   - error: Non-nil if sync fails.
	Sync() error

	// Close syncs and releases resources.
	//
	// Outputs:
	//   - error: Non-nil if close fails.
	Close() error

	// Stats returns journal statistics.
	Stats() JournalStats

	// Backup creates a portable backup of the journal.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - w: Writer to receive backup data. Must not be nil.
	//
	// Outputs:
	//   - error: Non-nil if backup fails.
	//
	// Thread Safety: Safe for concurrent use.
	Backup(ctx context.Context, w io.Writer) error

	// Restore loads state from a backup.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - r: Reader containing backup data. Must not be nil.
	//
	// Outputs:
	//   - error: Non-nil if restore fails.
	//
	// Limitations:
	//   - Restore is all-or-nothing: failure leaves journal in undefined state.
	//
	// Thread Safety: NOT safe for concurrent use. Caller must ensure exclusivity.
	Restore(ctx context.Context, r io.Reader) error
}

// DeltaOrError is used for streaming replay.
//
// Description:
//
//	Yields either a successfully decoded delta or an error.
//	Skipped indicates the delta was corrupted but skipped per config.
type DeltaOrError struct {
	// Delta is the decoded delta (nil if error).
	Delta Delta

	// SeqNum is the sequence number of this entry.
	SeqNum uint64

	// Err is set if decoding failed.
	Err error

	// Skipped is true if the delta was corrupted and skipped.
	Skipped bool
}

// JournalStats contains journal metrics.
type JournalStats struct {
	// TotalDeltas is the count of deltas in the journal.
	TotalDeltas int64

	// TotalBytes is approximate size of journal data.
	TotalBytes int64

	// LastSeqNum is the most recent sequence number.
	LastSeqNum uint64

	// LastCheckpoint is when the last checkpoint occurred.
	LastCheckpoint time.Time

	// CorruptedCount is the number of corrupted entries encountered.
	CorruptedCount int64

	// Degraded indicates if running in degraded mode.
	Degraded bool
}

// -----------------------------------------------------------------------------
// BadgerJournal Implementation
// -----------------------------------------------------------------------------

// BadgerJournal implements Journal using BadgerDB.
//
// Description:
//
//	Provides persistent WAL storage using BadgerDB. Each delta is stored
//	with a CRC32 checksum for integrity verification.
//
// Key format: "delta:{session_id}:{seq_num:016d}"
// Value format: [4-byte CRC32][gob-encoded delta]
//
// Thread Safety: Safe for concurrent use.
type BadgerJournal struct {
	db     *badger.DB
	config JournalConfig
	logger *slog.Logger

	// State
	seqNum         atomic.Uint64
	totalBytes     atomic.Int64
	corruptedCount atomic.Int64
	lastCheckpoint atomic.Int64 // Unix timestamp
	degraded       atomic.Bool
	closed         atomic.Bool

	// Synchronization
	mu sync.RWMutex
}

// NewBadgerJournal creates a journal at the specified path.
//
// Inputs:
//
//	config - Journal configuration. Must pass Validate().
//
// Outputs:
//
//	*BadgerJournal - Ready-to-use journal.
//	error - Non-nil if BadgerDB initialization fails and AllowDegraded is false.
//
// Thread Safety: Safe for concurrent use.
func NewBadgerJournal(config JournalConfig) (*BadgerJournal, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	j := &BadgerJournal{
		config: config,
		logger: config.Logger.With(slog.String("component", "journal"), slog.String("session_id", config.SessionID)),
	}

	// Build BadgerDB config
	dbConfig := badger.Config{
		Path:              config.Path,
		InMemory:          config.InMemory,
		SyncWrites:        config.SyncWrites,
		NumVersionsToKeep: 1,
		GCInterval:        5 * time.Minute,
		GCDiscardRatio:    0.5,
		Logger:            config.Logger,
	}

	// Open BadgerDB
	db, err := badger.OpenDB(dbConfig)
	if err != nil {
		if config.AllowDegraded {
			j.logger.Warn("BadgerDB unavailable, operating in degraded mode",
				slog.String("path", config.Path),
				slog.String("error", err.Error()))
			j.degraded.Store(true)
			return j, nil
		}
		return nil, fmt.Errorf("open badger: %w", err)
	}

	j.db = db

	// Initialize sequence number from existing entries
	if err := j.initSeqNum(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init sequence number: %w", err)
	}

	j.logger.Info("journal opened",
		slog.String("path", config.Path),
		slog.Bool("sync_writes", config.SyncWrites),
		slog.Uint64("last_seq_num", j.seqNum.Load()))

	return j, nil
}

// initSeqNum scans for the highest existing sequence number.
func (j *BadgerJournal) initSeqNum() error {
	prefix := j.deltaKeyPrefix()
	var maxSeq uint64

	err := j.db.WithReadTxn(context.Background(), func(txn *dgbadger.Txn) error {
		opts := dgbadger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Reverse = true // Start from highest key

		it := txn.NewIterator(opts)
		defer it.Close()

		// Seek to the last key with our prefix
		seekKey := append([]byte(prefix), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
		it.Seek(seekKey)

		if it.ValidForPrefix([]byte(prefix)) {
			key := it.Item().Key()
			seqStr := string(key[len(prefix):])
			var seq uint64
			if _, err := fmt.Sscanf(seqStr, "%016d", &seq); err == nil {
				maxSeq = seq
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	j.seqNum.Store(maxSeq)
	return nil
}

// deltaKeyPrefix returns the key prefix for this session's deltas.
func (j *BadgerJournal) deltaKeyPrefix() string {
	return fmt.Sprintf("delta:%s:", j.config.SessionID)
}

// deltaKey generates a key for a specific sequence number.
func (j *BadgerJournal) deltaKey(seqNum uint64) []byte {
	return []byte(fmt.Sprintf("%s%016d", j.deltaKeyPrefix(), seqNum))
}

// checkpointKey returns the key for the checkpoint marker.
func (j *BadgerJournal) checkpointKey() []byte {
	return []byte(fmt.Sprintf("checkpoint:latest:%s", j.config.SessionID))
}

// encodeEntry encodes a delta with CRC32 checksum.
func (j *BadgerJournal) encodeEntry(delta Delta) ([]byte, error) {
	// Register delta types for gob
	registerDeltaTypes()

	// Encode delta with gob
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&delta); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}

	// Compute CRC32 of encoded data
	crc := crc32.ChecksumIEEE(buf.Bytes())

	// Prepend CRC to data: [4-byte CRC][gob data]
	result := make([]byte, 4+buf.Len())
	binary.BigEndian.PutUint32(result[:4], crc)
	copy(result[4:], buf.Bytes())

	return result, nil
}

// decodeEntry decodes a delta and validates CRC32 checksum.
func (j *BadgerJournal) decodeEntry(data []byte) (Delta, error) {
	if len(data) < 5 { // 4-byte CRC + at least 1 byte data
		return nil, fmt.Errorf("%w: entry too short", ErrJournalCorrupted)
	}

	// Extract and verify CRC
	storedCRC := binary.BigEndian.Uint32(data[:4])
	gobData := data[4:]
	computedCRC := crc32.ChecksumIEEE(gobData)

	if storedCRC != computedCRC {
		return nil, fmt.Errorf("%w: stored=%08x computed=%08x", ErrJournalCorrupted, storedCRC, computedCRC)
	}

	// Decode gob data
	registerDeltaTypes()
	var delta Delta
	dec := gob.NewDecoder(bytes.NewReader(gobData))
	if err := dec.Decode(&delta); err != nil {
		return nil, fmt.Errorf("gob decode: %w", err)
	}

	return delta, nil
}

// registerDeltaTypes registers all delta types for gob encoding.
var deltaTypesRegistered sync.Once

// RegisterDeltaTypesForSSE exposes gob type registration for SSE decoding.
// CRS-27: Called by the SSE handler to ensure delta types are registered
// before decoding NATS messages.
//
// Thread Safety: Safe for concurrent use (uses sync.Once internally).
func RegisterDeltaTypesForSSE() {
	registerDeltaTypes()
}

func registerDeltaTypes() {
	deltaTypesRegistered.Do(func() {
		gob.Register(&ProofDelta{})
		gob.Register(&ConstraintDelta{})
		gob.Register(&SimilarityDelta{})
		gob.Register(&DependencyDelta{})
		gob.Register(&HistoryDelta{})
		gob.Register(&StreamingDelta{})
		gob.Register(&CompositeDelta{})
	})
}

// -----------------------------------------------------------------------------
// Journal Interface Implementation
// -----------------------------------------------------------------------------

// Append writes a delta with CRC checksum.
func (j *BadgerJournal) Append(ctx context.Context, delta Delta) error {
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

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "journal.Append",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
			attribute.String("delta_type", delta.Type().String()),
		),
	)
	defer span.End()

	// Check degraded mode
	if j.degraded.Load() {
		span.SetStatus(codes.Error, "degraded mode")
		return ErrJournalDegraded
	}

	// Check size limit
	if j.config.MaxJournalBytes > 0 && j.totalBytes.Load() >= j.config.MaxJournalBytes {
		span.SetStatus(codes.Error, "journal full")
		return ErrJournalFull
	}

	// Encode entry
	data, err := j.encodeEntry(delta)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "encode failed")
		return fmt.Errorf("encode entry: %w", err)
	}

	// Acquire next sequence number atomically
	seqNum := j.seqNum.Add(1)

	// Write to BadgerDB
	key := j.deltaKey(seqNum)
	err = j.db.WithTxn(ctx, func(txn *dgbadger.Txn) error {
		return txn.Set(key, data)
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "write failed")
		return fmt.Errorf("write entry: %w", err)
	}

	j.totalBytes.Add(int64(len(data)))

	span.SetAttributes(
		attribute.Int64("seq_num", int64(seqNum)),
		attribute.Int("entry_bytes", len(data)),
	)

	j.logger.Debug("delta appended",
		slog.Uint64("seq_num", seqNum),
		slog.String("type", delta.Type().String()),
		slog.Int("bytes", len(data)))

	return nil
}

// AppendBatch writes multiple deltas atomically.
func (j *BadgerJournal) AppendBatch(ctx context.Context, deltas []Delta) error {
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

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "journal.AppendBatch",
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

	// Pre-encode all entries
	type encodedEntry struct {
		key  []byte
		data []byte
	}
	entries := make([]encodedEntry, 0, len(deltas))
	totalSize := int64(0)

	// Reserve sequence numbers atomically
	baseSeq := j.seqNum.Add(uint64(len(deltas))) - uint64(len(deltas)) + 1

	for i, delta := range deltas {
		if delta == nil {
			return fmt.Errorf("delta at index %d is nil", i)
		}

		data, err := j.encodeEntry(delta)
		if err != nil {
			return fmt.Errorf("encode delta %d: %w", i, err)
		}

		entries = append(entries, encodedEntry{
			key:  j.deltaKey(baseSeq + uint64(i)),
			data: data,
		})
		totalSize += int64(len(data))
	}

	// Check size limit
	if j.config.MaxJournalBytes > 0 && j.totalBytes.Load()+totalSize >= j.config.MaxJournalBytes {
		span.SetStatus(codes.Error, "journal full")
		return ErrJournalFull
	}

	// Write all entries in single transaction
	err := j.db.WithTxn(ctx, func(txn *dgbadger.Txn) error {
		for _, entry := range entries {
			if err := txn.Set(entry.key, entry.data); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "write failed")
		return fmt.Errorf("write batch: %w", err)
	}

	j.totalBytes.Add(totalSize)

	span.SetAttributes(
		attribute.Int64("first_seq", int64(baseSeq)),
		attribute.Int64("last_seq", int64(baseSeq)+int64(len(deltas))-1),
		attribute.Int64("total_bytes", totalSize),
	)

	j.logger.Debug("batch appended",
		slog.Int("count", len(deltas)),
		slog.Uint64("first_seq", baseSeq),
		slog.Int64("bytes", totalSize))

	return nil
}

// Replay returns all deltas since last checkpoint with validation.
func (j *BadgerJournal) Replay(ctx context.Context) ([]Delta, error) {
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

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "journal.Replay",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		// In degraded mode, return empty - no persisted state
		span.SetAttributes(attribute.Bool("degraded", true))
		return []Delta{}, nil
	}

	// Get checkpoint sequence number
	checkpointSeq, err := j.getCheckpointSeq()
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}

	var deltas []Delta
	var lastSeq uint64
	corrupted := 0

	prefix := []byte(j.deltaKeyPrefix())
	err = j.db.WithReadTxn(ctx, func(txn *dgbadger.Txn) error {
		opts := dgbadger.DefaultIteratorOptions
		opts.PrefetchValues = true

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			// Check context
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			item := it.Item()
			key := item.Key()

			// Parse sequence number from key
			seqStr := string(key[len(prefix):])
			var seqNum uint64
			if _, err := fmt.Sscanf(seqStr, "%016d", &seqNum); err != nil {
				continue // Skip malformed keys
			}

			// Skip entries before checkpoint
			if seqNum <= checkpointSeq {
				continue
			}

			// Validate sequence is increasing
			if lastSeq > 0 && seqNum != lastSeq+1 {
				if !j.config.SkipCorruptedDeltas {
					return fmt.Errorf("%w: expected %d, got %d", ErrJournalSequenceGap, lastSeq+1, seqNum)
				}
				j.logger.Warn("sequence gap detected",
					slog.Uint64("expected", lastSeq+1),
					slog.Uint64("got", seqNum))
			}
			lastSeq = seqNum

			// Decode entry
			err := item.Value(func(val []byte) error {
				delta, err := j.decodeEntry(val)
				if err != nil {
					if errors.Is(err, ErrJournalCorrupted) {
						corrupted++
						j.corruptedCount.Add(1)
						if j.config.SkipCorruptedDeltas {
							j.logger.Warn("skipping corrupted entry",
								slog.Uint64("seq_num", seqNum),
								slog.String("error", err.Error()))
							return nil
						}
					}
					return err
				}
				deltas = append(deltas, delta)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "replay failed")
		return nil, fmt.Errorf("replay: %w", err)
	}

	span.SetAttributes(
		attribute.Int("delta_count", len(deltas)),
		attribute.Int("corrupted_count", corrupted),
		attribute.Int64("checkpoint_seq", int64(checkpointSeq)),
	)

	j.logger.Info("replay completed",
		slog.Int("delta_count", len(deltas)),
		slog.Int("corrupted", corrupted),
		slog.Uint64("checkpoint_seq", checkpointSeq))

	return deltas, nil
}

// ReplayStream returns a channel for streaming replay.
func (j *BadgerJournal) ReplayStream(ctx context.Context) (<-chan DeltaOrError, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	if j.closed.Load() {
		return nil, ErrJournalClosed
	}

	if j.degraded.Load() {
		// Return closed channel for degraded mode
		ch := make(chan DeltaOrError)
		close(ch)
		return ch, nil
	}

	ch := make(chan DeltaOrError, 100) // Buffer for efficiency

	go func() {
		defer close(ch)

		_, span := otel.Tracer("crs").Start(ctx, "journal.ReplayStream",
			trace.WithAttributes(
				attribute.String("session_id", j.config.SessionID),
			),
		)
		defer span.End()

		checkpointSeq, err := j.getCheckpointSeq()
		if err != nil {
			span.RecordError(err)
			ch <- DeltaOrError{Err: fmt.Errorf("get checkpoint: %w", err)}
			return
		}

		var lastSeq uint64
		count := 0
		gapCount := 0

		prefix := []byte(j.deltaKeyPrefix())
		err = j.db.WithReadTxn(ctx, func(txn *dgbadger.Txn) error {
			opts := dgbadger.DefaultIteratorOptions
			opts.PrefetchValues = true

			it := txn.NewIterator(opts)
			defer it.Close()

			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				item := it.Item()
				key := item.Key()

				seqStr := string(key[len(prefix):])
				var seqNum uint64
				if _, err := fmt.Sscanf(seqStr, "%016d", &seqNum); err != nil {
					continue
				}

				if seqNum <= checkpointSeq {
					continue
				}

				// Validate sequence is increasing (matching Replay behavior)
				if lastSeq > 0 && seqNum != lastSeq+1 {
					gapCount++
					if !j.config.SkipCorruptedDeltas {
						err := fmt.Errorf("%w: expected %d, got %d", ErrJournalSequenceGap, lastSeq+1, seqNum)
						ch <- DeltaOrError{SeqNum: seqNum, Err: err}
						return err
					}
					j.logger.Warn("sequence gap detected in stream",
						slog.Uint64("expected", lastSeq+1),
						slog.Uint64("got", seqNum))
				}
				lastSeq = seqNum

				err := item.Value(func(val []byte) error {
					delta, err := j.decodeEntry(val)
					if err != nil {
						if errors.Is(err, ErrJournalCorrupted) && j.config.SkipCorruptedDeltas {
							j.corruptedCount.Add(1)
							ch <- DeltaOrError{SeqNum: seqNum, Err: err, Skipped: true}
							return nil
						}
						ch <- DeltaOrError{SeqNum: seqNum, Err: err}
						return nil
					}
					ch <- DeltaOrError{Delta: delta, SeqNum: seqNum}
					count++
					return nil
				})
				if err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			span.RecordError(err)
			ch <- DeltaOrError{Err: err}
		}

		span.SetAttributes(
			attribute.Int("delta_count", count),
			attribute.Int64("last_seq", int64(lastSeq)),
			attribute.Int("gap_count", gapCount),
		)
	}()

	return ch, nil
}

// Checkpoint marks current position and truncates old entries.
func (j *BadgerJournal) Checkpoint(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "journal.Checkpoint",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	if j.degraded.Load() {
		span.SetAttributes(attribute.Bool("degraded", true))
		return nil // No-op in degraded mode
	}

	currentSeq := j.seqNum.Load()
	checkpointData := make([]byte, 8)
	binary.BigEndian.PutUint64(checkpointData, currentSeq)

	// Write checkpoint marker
	err := j.db.WithTxn(ctx, func(txn *dgbadger.Txn) error {
		return txn.Set(j.checkpointKey(), checkpointData)
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "checkpoint failed")
		return fmt.Errorf("write checkpoint: %w", err)
	}

	j.lastCheckpoint.Store(time.Now().Unix())

	// Delete old entries (before checkpoint)
	deletedCount := 0
	prefix := []byte(j.deltaKeyPrefix())
	err = j.db.WithTxn(ctx, func(txn *dgbadger.Txn) error {
		opts := dgbadger.DefaultIteratorOptions
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := it.Item().Key()
			seqStr := string(key[len(prefix):])
			var seqNum uint64
			if _, err := fmt.Sscanf(seqStr, "%016d", &seqNum); err != nil {
				continue
			}

			if seqNum <= currentSeq {
				if err := txn.Delete(key); err != nil {
					return err
				}
				deletedCount++
			}
		}
		return nil
	})

	if err != nil {
		span.RecordError(err)
		j.logger.Warn("checkpoint truncation failed", slog.String("error", err.Error()))
		// Don't fail checkpoint if truncation fails - marker is saved
	}

	// Reset byte counter after truncation
	j.totalBytes.Store(0)

	span.SetAttributes(
		attribute.Int64("checkpoint_seq", int64(currentSeq)),
		attribute.Int("deleted_entries", deletedCount),
	)

	j.logger.Info("checkpoint created",
		slog.Uint64("seq_num", currentSeq),
		slog.Int("deleted", deletedCount))

	return nil
}

// getCheckpointSeq returns the last checkpoint sequence number.
func (j *BadgerJournal) getCheckpointSeq() (uint64, error) {
	var checkpointSeq uint64

	err := j.db.WithReadTxn(context.Background(), func(txn *dgbadger.Txn) error {
		item, err := txn.Get(j.checkpointKey())
		if err == dgbadger.ErrKeyNotFound {
			return nil // No checkpoint yet
		}
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			if len(val) >= 8 {
				checkpointSeq = binary.BigEndian.Uint64(val)
			}
			return nil
		})
	})

	return checkpointSeq, err
}

// IsAvailable returns false if journal is in degraded mode or closed.
func (j *BadgerJournal) IsAvailable() bool {
	return !j.degraded.Load() && !j.closed.Load()
}

// IsDegraded returns true if operating with reduced durability.
func (j *BadgerJournal) IsDegraded() bool {
	return j.degraded.Load()
}

// Sync flushes pending writes.
func (j *BadgerJournal) Sync() error {
	if j.closed.Load() {
		return ErrJournalClosed
	}
	if j.degraded.Load() || j.db == nil {
		return nil
	}

	return j.db.Sync()
}

// Close syncs and releases resources.
func (j *BadgerJournal) Close() error {
	if j.closed.Swap(true) {
		return nil // Already closed
	}

	j.logger.Info("closing journal")

	if j.db != nil {
		if err := j.db.Sync(); err != nil {
			j.logger.Warn("sync before close failed", slog.String("error", err.Error()))
		}
		return j.db.Close()
	}

	return nil
}

// Stats returns journal statistics.
func (j *BadgerJournal) Stats() JournalStats {
	lastCP := j.lastCheckpoint.Load()
	var lastCPTime time.Time
	if lastCP > 0 {
		lastCPTime = time.Unix(lastCP, 0)
	}

	return JournalStats{
		TotalDeltas:    int64(j.seqNum.Load()),
		TotalBytes:     j.totalBytes.Load(),
		LastSeqNum:     j.seqNum.Load(),
		LastCheckpoint: lastCPTime,
		CorruptedCount: j.corruptedCount.Load(),
		Degraded:       j.degraded.Load(),
	}
}

// -----------------------------------------------------------------------------
// Backup and Restore Methods (GR-33)
// -----------------------------------------------------------------------------

// Backup creates a portable backup of the journal.
//
// Description:
//
//	Uses BadgerDB's built-in streaming backup to export all KV pairs.
//	The writer receives a binary stream suitable for later restore.
//	Caller is responsible for compression and storage.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - w: Writer to receive backup stream. Must not be nil.
//
// Outputs:
//   - error: Non-nil if backup fails.
//
// Thread Safety: Safe for concurrent use.
func (j *BadgerJournal) Backup(ctx context.Context, w io.Writer) error {
	if ctx == nil {
		return ErrNilContext
	}
	if w == nil {
		return errors.New("writer must not be nil")
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}
	if j.degraded.Load() {
		return ErrJournalDegraded
	}

	ctx, span := otel.Tracer("crs").Start(ctx, "journal.Backup",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	start := time.Now()

	// BadgerDB's Backup streams all KV pairs to writer
	// Version 0 means full backup (not incremental)
	since, err := j.db.DB.Backup(w, 0)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backup failed")
		return fmt.Errorf("badger backup: %w", err)
	}

	duration := time.Since(start)
	span.SetAttributes(
		attribute.Int64("since_version", int64(since)),
		attribute.Float64("duration_seconds", duration.Seconds()),
	)

	j.logger.Info("journal backup created",
		slog.Duration("duration", duration),
		slog.Uint64("since_version", since),
	)

	return nil
}

// Restore loads state from a backup.
//
// Description:
//
//	Uses BadgerDB's built-in Load function to restore from a backup
//	stream. After restore, re-initializes internal state (sequence
//	numbers, counters).
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - r: Reader with backup stream. Must not be nil.
//
// Outputs:
//   - error: Non-nil if restore fails.
//
// Limitations:
//   - Restore is all-or-nothing: failure leaves journal in undefined state.
//   - Caller should discard journal and create new one on restore failure.
//
// Thread Safety: NOT safe for concurrent use with Append/Replay.
// Caller must ensure exclusive access during restore.
func (j *BadgerJournal) Restore(ctx context.Context, r io.Reader) error {
	if ctx == nil {
		return ErrNilContext
	}
	if r == nil {
		return errors.New("reader must not be nil")
	}

	if j.closed.Load() {
		return ErrJournalClosed
	}
	// Allow restore even in degraded mode - might fix the degradation

	ctx, span := otel.Tracer("crs").Start(ctx, "journal.Restore",
		trace.WithAttributes(
			attribute.String("session_id", j.config.SessionID),
		),
	)
	defer span.End()

	start := time.Now()

	// MaxPendingWrites controls concurrency during restore
	// 256 is BadgerDB's recommended value for most workloads
	const maxPendingWrites = 256

	// BadgerDB's Load restores from backup stream
	err := j.db.DB.Load(r, maxPendingWrites)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "restore failed")
		return fmt.Errorf("badger load: %w", err)
	}

	// Re-initialize sequence number from restored data
	if err := j.initSeqNum(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reinit seq num failed")
		return fmt.Errorf("reinit seq num: %w", err)
	}

	// Clear degraded flag - restore may have fixed underlying issue
	j.degraded.Store(false)

	// Reset counters
	j.totalBytes.Store(0) // Will be recomputed on next append
	j.corruptedCount.Store(0)

	duration := time.Since(start)
	span.SetAttributes(
		attribute.Int64("restored_seq_num", int64(j.seqNum.Load())),
		attribute.Float64("duration_seconds", duration.Seconds()),
	)

	j.logger.Info("journal restored from backup",
		slog.Duration("duration", duration),
		slog.Uint64("last_seq_num", j.seqNum.Load()),
	)

	return nil
}

// DB returns the underlying BadgerDB wrapper.
//
// Description:
//
//	Provides access to the underlying database for backup operations.
//	Used by PersistenceManager.SaveBackup and LoadBackup.
//
// INTERNAL USE ONLY: This method is intended for use by the persistence
// package only. External callers should use the Journal interface methods
// (Append, Replay, Checkpoint) rather than accessing the database directly.
// Direct database access bypasses journal safety mechanisms including:
//   - CRC checksums on entries
//   - Sequence number tracking
//   - Degraded mode handling
//
// Outputs:
//   - *badger.DB: The database wrapper. May be nil in degraded mode.
//
// Thread Safety: Safe for concurrent use.
func (j *BadgerJournal) DB() *badger.DB {
	return j.db
}
