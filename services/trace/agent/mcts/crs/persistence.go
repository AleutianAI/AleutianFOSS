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
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Logging Helpers
// -----------------------------------------------------------------------------

// loggerWithTrace returns a logger with trace context attached.
// Per CLAUDE.md Section 9.5, all logs should include trace_id and span_id.
func loggerWithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return logger
	}
	return logger.With(
		slog.String("trace_id", spanCtx.TraceID().String()),
		slog.String("span_id", spanCtx.SpanID().String()),
	)
}

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	// ErrBackupCorrupted indicates backup data failed integrity check.
	ErrBackupCorrupted = errors.New("backup corrupted: content hash mismatch")

	// ErrBackupVersionMismatch indicates BadgerDB version incompatibility.
	ErrBackupVersionMismatch = errors.New("backup BadgerDB version mismatch")

	// ErrBackupNotFound indicates no backup exists for the project.
	ErrBackupNotFound = errors.New("backup not found")

	// ErrBackupLockFailed indicates file lock acquisition failed.
	ErrBackupLockFailed = errors.New("failed to acquire backup lock")

	// ErrPersistenceManagerClosed indicates the manager has been closed.
	ErrPersistenceManagerClosed = errors.New("persistence manager is closed")

	// ErrRestoreInProgress indicates a restore is already in progress.
	ErrRestoreInProgress = errors.New("restore already in progress")
)

// -----------------------------------------------------------------------------
// Metrics
// -----------------------------------------------------------------------------

var (
	// Note: Removed project_hash from histograms to prevent cardinality explosion.
	// For project-specific metrics, use the gauges which are bounded by active projects.
	backupDurationHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crs_backup_duration_seconds",
		Help:    "Time to create CRS backup",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"status", "compression_level"})

	restoreDurationHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crs_restore_duration_seconds",
		Help:    "Time to restore CRS from backup",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"status"})

	backupSizeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crs_backup_size_bytes",
		Help: "Size of most recent backup in bytes",
	}, []string{"project_hash"})

	backupOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_backup_operations_total",
		Help: "Total backup operations by type and status",
	}, []string{"operation", "status"})

	backupAgeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crs_backup_age_seconds",
		Help: "Age of most recent backup in seconds",
	}, []string{"project_hash"})

	backupRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_backup_retries_total",
		Help: "Total backup retry attempts",
	}, []string{"operation", "reason"})

	// CRS-21: Checkpoint fallback counter.
	checkpointFallbackTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trace_crs_checkpoint_fallback_total",
		Help: "Total times LoadBackup fell back to the previous generation checkpoint",
	})
)

// -----------------------------------------------------------------------------
// Tracer
// -----------------------------------------------------------------------------

var persistenceTracer = otel.Tracer("crs.persistence")

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

// PersistenceConfig configures the PersistenceManager.
type PersistenceConfig struct {
	// BaseDir is the root directory for all CRS persistence data.
	// Default: ~/.aleutian/crs/
	BaseDir string

	// CompressionLevel is the gzip compression level (1-9).
	// Higher = smaller files, slower. Default: 6.
	CompressionLevel int

	// LockTimeoutSec is how long to wait for file lock.
	// Default: 30 seconds.
	LockTimeoutSec int

	// MaxBackupRetries is the number of retry attempts on transient failures.
	// Default: 3.
	MaxBackupRetries int

	// ValidateOnRestore enables integrity checks during restore.
	// Default: true.
	ValidateOnRestore bool

	// Logger for persistence operations.
	Logger *slog.Logger
}

// DefaultPersistenceConfig returns production defaults.
func DefaultPersistenceConfig() PersistenceConfig {
	homeDir, _ := os.UserHomeDir()
	return PersistenceConfig{
		BaseDir:           filepath.Join(homeDir, ".aleutian", "crs"),
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
		Logger:            slog.Default(),
	}
}

// Validate checks if the configuration is valid.
func (c *PersistenceConfig) Validate() error {
	if c.BaseDir == "" {
		return errors.New("base_dir must not be empty")
	}
	if c.CompressionLevel < 1 || c.CompressionLevel > 9 {
		return fmt.Errorf("compression_level must be 1-9, got %d", c.CompressionLevel)
	}
	if c.LockTimeoutSec <= 0 {
		return errors.New("lock_timeout_sec must be positive")
	}
	return nil
}

// -----------------------------------------------------------------------------
// BackupMetadata
// -----------------------------------------------------------------------------

// BackupMetadata contains information about a backup for verification.
//
// Description:
//
//	Stored alongside the backup file to enable integrity verification
//	and version compatibility checking. Uses int64 timestamps per
//	CLAUDE.md standards.
//
// Thread Safety: Immutable after creation.
type BackupMetadata struct {
	// ProjectHash identifies the project this backup belongs to.
	ProjectHash string `json:"project_hash"`

	// CreatedAt is when this backup was created (Unix milliseconds UTC).
	CreatedAt int64 `json:"created_at"`

	// BadgerVersion is the BadgerDB version used to create the backup.
	BadgerVersion string `json:"badger_version"`

	// ContentHash is the SHA256 hash of the compressed backup file.
	ContentHash string `json:"content_hash"`

	// UncompressedSize is the original size before compression.
	UncompressedSize int64 `json:"uncompressed_size"`

	// CompressedSize is the size of the compressed backup file.
	CompressedSize int64 `json:"compressed_size"`

	// Generation is the CRS generation at backup time.
	Generation int64 `json:"generation"`

	// SessionID is the session that created this backup.
	SessionID string `json:"session_id,omitempty"`

	// DeltaCount is the number of deltas in the journal.
	DeltaCount int64 `json:"delta_count"`

	// SchemaVersion is the backup format version for future compatibility.
	SchemaVersion string `json:"schema_version"`

	// ExportPath is the path to the companion JSON export (if created).
	ExportPath string `json:"export_path,omitempty"`

	// MetadataHash is the SHA256 hash of this metadata (excluding this field).
	// Used to detect metadata file corruption (P2 fix: I2).
	MetadataHash string `json:"metadata_hash,omitempty"`
}

// CurrentSchemaVersion is the backup schema version.
const CurrentSchemaVersion = "1.0"

// BadgerDBVersion is the version of BadgerDB used for backup compatibility.
//
// IMPORTANT: This MUST match the version in go.mod.
// When upgrading BadgerDB:
//  1. Update go.mod: go get github.com/dgraph-io/badger/v4@vX.Y.Z
//  2. Update this constant to match
//  3. Test backup/restore with existing backups
//
// Reference: go.mod -> github.com/dgraph-io/badger/v4
// Build verification: go generate ./... should fail if mismatch (TODO: add check)
const BadgerDBVersion = "v4.9.1"

// Age returns the age of the backup.
func (m *BackupMetadata) Age() time.Duration {
	return time.Since(time.UnixMilli(m.CreatedAt))
}

// CompressionRatio returns the compression ratio.
func (m *BackupMetadata) CompressionRatio() float64 {
	if m.UncompressedSize == 0 {
		return 0
	}
	return float64(m.CompressedSize) / float64(m.UncompressedSize)
}

// -----------------------------------------------------------------------------
// GraphRefreshCoordinator Interface
// -----------------------------------------------------------------------------

// GraphRefreshCoordinator is implemented by components that need to pause
// during CRS restore operations.
//
// Description:
//
//	During restore, the graph must not be refreshed as this could
//	introduce inconsistent state. Components implementing this
//	interface will be paused during restore and resumed after.
type GraphRefreshCoordinator interface {
	// Pause stops graph refresh operations.
	Pause(ctx context.Context) error

	// Resume allows graph refresh operations to continue.
	Resume(ctx context.Context) error

	// IsPaused returns true if currently paused.
	IsPaused() bool
}

// -----------------------------------------------------------------------------
// PersistenceManager
// -----------------------------------------------------------------------------

// PersistenceManager handles CRS state persistence across sessions.
//
// Description:
//
//	Provides backup and restore functionality for CRS state using
//	BadgerDB's native backup mechanism. Features include:
//	  - Gzip compression for space efficiency
//	  - SHA256 content hashing for integrity verification
//	  - flock-based file locking for concurrent access safety
//	  - Atomic file operations to prevent corruption
//	  - BadgerDB version compatibility checking
//	  - Optional JSON export for portability
//
// Thread Safety: Safe for concurrent use.
type PersistenceManager struct {
	config PersistenceConfig
	logger *slog.Logger

	// State
	mu            sync.RWMutex
	closed        bool
	restoreInProg bool
	restoreCount  int64

	// Coordination with graph refresh (set via SetRefreshCoordinator)
	refreshCoordinator GraphRefreshCoordinator
}

// NewPersistenceManager creates a new persistence manager.
//
// Description:
//
//	Creates a manager rooted at the configured base directory.
//	Creates the directory structure if it doesn't exist.
//
// Inputs:
//   - config: Configuration. If nil, uses DefaultPersistenceConfig().
//
// Outputs:
//   - *PersistenceManager: The new manager.
//   - error: Non-nil if configuration is invalid or directory creation fails.
//
// Example:
//
//	cfg := DefaultPersistenceConfig()
//	pm, err := NewPersistenceManager(&cfg)
//	if err != nil {
//	    return fmt.Errorf("create persistence manager: %w", err)
//	}
//	defer pm.Close()
//
// Thread Safety: Safe for concurrent use.
func NewPersistenceManager(config *PersistenceConfig) (*PersistenceManager, error) {
	if config == nil {
		cfg := DefaultPersistenceConfig()
		config = &cfg
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Create base directory
	if err := os.MkdirAll(config.BaseDir, 0750); err != nil {
		return nil, fmt.Errorf("create base dir %s: %w", config.BaseDir, err)
	}

	return &PersistenceManager{
		config: *config,
		logger: config.Logger.With(slog.String("component", "persistence_manager")),
	}, nil
}

// SetRefreshCoordinator registers the graph refresh coordinator.
//
// Description:
//
//	The coordinator will be paused during restore operations to
//	prevent graph refresh from interfering with state restoration.
//
// Inputs:
//   - coordinator: The coordinator to pause/resume. May be nil.
//
// Thread Safety: Safe for concurrent use.
func (pm *PersistenceManager) SetRefreshCoordinator(coordinator GraphRefreshCoordinator) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.refreshCoordinator = coordinator
	pm.logger.Info("graph refresh coordinator registered",
		slog.Bool("has_coordinator", coordinator != nil),
	)
}

// ProjectDir returns the directory for a specific project.
func (pm *PersistenceManager) ProjectDir(projectHash string) string {
	return filepath.Join(pm.config.BaseDir, projectHash)
}

// BackupPath returns the path to the backup file.
func (pm *PersistenceManager) BackupPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "backups", "latest.backup.gz")
}

// PrevBackupPath returns the path to the previous generation backup.
// CRS-21: Two-generation checkpoint rotation for crash safety.
func (pm *PersistenceManager) PrevBackupPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "backups", "latest.prev.backup.gz")
}

// MetadataPath returns the path to the metadata file.
func (pm *PersistenceManager) MetadataPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "metadata.json")
}

// PrevMetadataPath returns the path to the previous generation metadata.
// CRS-21: Two-generation checkpoint rotation for crash safety.
func (pm *PersistenceManager) PrevMetadataPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "metadata.prev.json")
}

// LockPath returns the path to the lock file.
func (pm *PersistenceManager) LockPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "backups", "latest.backup.gz.lock")
}

// ExportPath returns the path to the JSON export file.
func (pm *PersistenceManager) ExportPath(projectHash string) string {
	return filepath.Join(pm.ProjectDir(projectHash), "export.json")
}

// BackupOptions configures backup behavior.
type BackupOptions struct {
	// CreateJSONExport also creates a portable JSON export.
	CreateJSONExport bool

	// SessionID to record in metadata.
	SessionID string
}

// SaveBackup creates a backup of the journal state.
//
// Description:
//
//	Creates a compressed, integrity-verified backup of the BadgerDB
//	journal. Uses atomic file operations to ensure backup is either
//	completely written or not at all. Optionally creates a JSON
//	export for portability. Implements retry logic for transient failures.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - projectHash: Project identifier (8-64 hex chars). Must not be empty.
//   - journal: The BadgerJournal to backup. Must not be nil.
//   - opts: Optional backup options (nil for defaults).
//
// Outputs:
//   - *BackupMetadata: Metadata about the created backup.
//   - error: Non-nil if backup fails after all retries.
//
// Example:
//
//	meta, err := pm.SaveBackup(ctx, "abc123def456", journal, nil)
//	if err != nil {
//	    return fmt.Errorf("backup: %w", err)
//	}
//	log.Info("backup created", "size", meta.CompressedSize)
//
// Thread Safety: Safe for concurrent use. Uses file locking for safety.
func (pm *PersistenceManager) SaveBackup(ctx context.Context, projectHash string, journal Journal, opts *BackupOptions) (*BackupMetadata, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	pm.mu.RLock()
	if pm.closed {
		pm.mu.RUnlock()
		return nil, ErrPersistenceManagerClosed
	}
	maxRetries := pm.config.MaxBackupRetries
	pm.mu.RUnlock()

	// Validate inputs
	if err := ValidateProjectHash(projectHash); err != nil {
		return nil, fmt.Errorf("validate project hash: %w", err)
	}
	if journal == nil {
		return nil, fmt.Errorf("journal must not be nil")
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		metadata, err := pm.saveBackupOnce(ctx, projectHash, journal, opts, attempt)
		if err == nil {
			return metadata, nil
		}

		lastErr = err

		// Don't retry on non-transient errors
		if errors.Is(err, ErrPersistenceManagerClosed) ||
			errors.Is(err, ErrNilContext) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		// Log retry attempt
		if attempt < maxRetries {
			backupRetriesTotal.WithLabelValues("save", "transient_error").Inc()
			pm.logger.Warn("backup attempt failed, retrying",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", maxRetries),
				slog.String("error", err.Error()),
			)
			// Exponential backoff: 100ms, 200ms, 400ms...
			time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
		}
	}

	return nil, fmt.Errorf("backup failed after %d attempts: %w", maxRetries+1, lastErr)
}

// saveBackupOnce performs a single backup attempt.
func (pm *PersistenceManager) saveBackupOnce(ctx context.Context, projectHash string, journal Journal, opts *BackupOptions, attempt int) (*BackupMetadata, error) {
	start := time.Now()
	ctx, span := persistenceTracer.Start(ctx, "crs.Persistence.SaveBackup",
		trace.WithAttributes(
			attribute.String("project_hash", projectHash),
			attribute.Int("attempt", attempt),
		),
	)
	defer span.End()

	// Use trace-aware logger per CLAUDE.md Section 9.5
	logger := loggerWithTrace(ctx, pm.logger).With(
		slog.String("project_hash", projectHash),
		slog.String("operation", "save_backup"),
		slog.Int("attempt", attempt),
	)
	logger.Info("starting backup")

	// Ensure directories exist
	backupDir := filepath.Join(pm.ProjectDir(projectHash), "backups")
	if err := os.MkdirAll(backupDir, 0750); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create backup dir failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	// Acquire exclusive lock
	lockFile, err := pm.acquireLock(ctx, projectHash, true)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "acquire lock failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer pm.releaseLock(lockFile)

	// Create temp file for atomic write
	backupPath := pm.BackupPath(projectHash)
	tmpPath := backupPath + ".tmp"

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create temp file failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("create temp file: %w", err)
	}

	// Cleanup on any error
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	// Create counting and hashing writers
	hasher := sha256.New()
	countWriter := &countingWriter{w: tmpFile}
	multiWriter := io.MultiWriter(countWriter, hasher)

	// Create gzip writer
	gzipWriter, err := gzip.NewWriterLevel(multiWriter, pm.config.CompressionLevel)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create gzip writer failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}

	// Create uncompressed size counter
	uncompressedCounter := &countingWriter{w: gzipWriter}

	// Perform backup
	if err := journal.Backup(ctx, uncompressedCounter); err != nil {
		gzipWriter.Close()
		span.RecordError(err)
		span.SetStatus(codes.Error, "journal backup failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("journal backup: %w", err)
	}

	// Close gzip writer (flushes remaining data)
	if err := gzipWriter.Close(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "close gzip failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("close gzip: %w", err)
	}

	// Sync to disk for durability
	if err := tmpFile.Sync(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "sync failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("sync: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "close file failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("close file: %w", err)
	}

	// CRS-21: Two-generation rotation. Rename current → prev before writing new.
	// This preserves the previous checkpoint as a fallback if the process crashes
	// between this rename and the final rename below.
	prevPath := pm.PrevBackupPath(projectHash)
	if _, statErr := os.Stat(backupPath); statErr == nil {
		if renameErr := os.Rename(backupPath, prevPath); renameErr != nil {
			// Log but don't fail — losing the prev backup is acceptable.
			logger.Warn("CRS-21: failed to rotate current checkpoint to prev",
				slog.String("error", renameErr.Error()),
			)
		}
	}

	// Atomic rename: tmp → current
	if err := os.Rename(tmpPath, backupPath); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rename failed")
		backupOperationsTotal.WithLabelValues("save", "error").Inc()
		return nil, fmt.Errorf("atomic rename: %w", err)
	}

	cleanupTmp = false // Rename succeeded, don't cleanup

	// Sync directory for full durability on all filesystems (P3 fix)
	if err := syncDir(filepath.Dir(backupPath)); err != nil {
		// Log but don't fail - the backup was written successfully
		logger.Warn("directory sync failed (backup still valid)",
			slog.String("error", err.Error()),
		)
	}

	// Build metadata
	stats := journal.Stats()
	metadata := &BackupMetadata{
		ProjectHash:      projectHash,
		CreatedAt:        time.Now().UnixMilli(),
		BadgerVersion:    BadgerDBVersion,
		ContentHash:      hex.EncodeToString(hasher.Sum(nil)),
		UncompressedSize: uncompressedCounter.count,
		CompressedSize:   countWriter.count,
		Generation:       int64(stats.LastSeqNum),
		DeltaCount:       stats.TotalDeltas,
		SchemaVersion:    CurrentSchemaVersion,
	}

	// Add session ID if provided
	if opts != nil && opts.SessionID != "" {
		metadata.SessionID = opts.SessionID
	}

	// Write metadata
	if err := pm.writeMetadata(projectHash, metadata); err != nil {
		logger.Warn("failed to write metadata",
			slog.String("error", err.Error()),
		)
		// Don't fail backup if metadata write fails
	}

	// Update metrics
	duration := time.Since(start)
	compressionLevel := strconv.Itoa(pm.config.CompressionLevel)
	backupDurationHistogram.WithLabelValues("success", compressionLevel).Observe(duration.Seconds())
	backupSizeGauge.WithLabelValues(projectHash).Set(float64(metadata.CompressedSize))
	backupOperationsTotal.WithLabelValues("save", "success").Inc()
	backupAgeGauge.WithLabelValues(projectHash).Set(0)

	span.SetAttributes(
		attribute.Int64("compressed_size", metadata.CompressedSize),
		attribute.Int64("uncompressed_size", metadata.UncompressedSize),
		attribute.Float64("compression_ratio", metadata.CompressionRatio()),
		attribute.Int64("generation", metadata.Generation),
		attribute.Int64("delta_count", metadata.DeltaCount),
		attribute.Int("compression_level", pm.config.CompressionLevel),
	)

	logger.Info("backup completed",
		slog.Duration("duration", duration),
		slog.Int64("compressed_bytes", metadata.CompressedSize),
		slog.Int64("uncompressed_bytes", metadata.UncompressedSize),
		slog.Float64("compression_ratio", metadata.CompressionRatio()),
		slog.Int64("generation", metadata.Generation),
		slog.Int("compression_level", pm.config.CompressionLevel),
	)

	return metadata, nil
}

// LoadBackup restores journal state from a backup.
//
// Description:
//
//	Restores the journal from a compressed backup file. Performs
//	integrity verification (SHA256 hash check) and version
//	compatibility check before restoring. Coordinates with the
//	graph refresh coordinator if set.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - projectHash: Project identifier. Must not be empty.
//   - journal: The BadgerJournal to restore into. Must not be nil.
//
// Outputs:
//   - *BackupMetadata: Metadata about the restored backup.
//   - error: Non-nil if restore fails. ErrBackupNotFound if no backup exists.
//
// Example:
//
//	meta, err := pm.LoadBackup(ctx, "abc123def456", journal)
//	if errors.Is(err, ErrBackupNotFound) {
//	    // First run, no backup exists
//	    return nil
//	}
//	if err != nil {
//	    return fmt.Errorf("restore: %w", err)
//	}
//
// Thread Safety: Safe for concurrent use. Uses file locking.
func (pm *PersistenceManager) LoadBackup(ctx context.Context, projectHash string, journal Journal) (*BackupMetadata, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	pm.mu.Lock()
	if pm.closed {
		pm.mu.Unlock()
		return nil, ErrPersistenceManagerClosed
	}
	if pm.restoreInProg {
		pm.mu.Unlock()
		return nil, ErrRestoreInProgress
	}
	pm.restoreInProg = true
	pm.restoreCount++
	coordinator := pm.refreshCoordinator
	pm.mu.Unlock()

	defer func() {
		pm.mu.Lock()
		pm.restoreInProg = false
		pm.mu.Unlock()
	}()

	// Validate inputs
	if err := ValidateProjectHash(projectHash); err != nil {
		return nil, fmt.Errorf("validate project hash: %w", err)
	}
	if journal == nil {
		return nil, fmt.Errorf("journal must not be nil")
	}

	start := time.Now()
	ctx, span := persistenceTracer.Start(ctx, "crs.Persistence.LoadBackup",
		trace.WithAttributes(
			attribute.String("project_hash", projectHash),
		),
	)
	defer span.End()

	// Use trace-aware logger per CLAUDE.md Section 9.5
	logger := loggerWithTrace(ctx, pm.logger).With(
		slog.String("project_hash", projectHash),
		slog.String("operation", "load_backup"),
	)

	// Pause graph refresh during restore with panic-safe resume (P2 fix)
	coordinatorPaused := false
	if coordinator != nil {
		logger.Debug("pausing graph refresh coordinator")
		if err := coordinator.Pause(ctx); err != nil {
			span.RecordError(err)
			logger.Warn("failed to pause coordinator, continuing anyway",
				slog.String("error", err.Error()),
			)
		} else {
			coordinatorPaused = true
		}
	}
	// Always attempt resume if we paused, even on panic
	defer func() {
		if coordinatorPaused && coordinator != nil {
			logger.Debug("resuming graph refresh coordinator")
			if err := coordinator.Resume(ctx); err != nil {
				logger.Warn("failed to resume coordinator",
					slog.String("error", err.Error()),
				)
			}
		}
	}()

	// Acquire shared lock (allows concurrent reads but blocks writes)
	lockFile, err := pm.acquireLock(ctx, projectHash, false)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "acquire lock failed")
		backupOperationsTotal.WithLabelValues("load", "error").Inc()
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer pm.releaseLock(lockFile)

	// Try current checkpoint first.
	backupPath := pm.BackupPath(projectHash)
	metadataPath := pm.MetadataPath(projectHash)
	metadata, loadErr := pm.loadBackupFromPath(ctx, projectHash, backupPath, metadataPath, journal, logger, span)
	if loadErr == nil {
		// Current checkpoint loaded successfully.
		duration := time.Since(start)
		restoreDurationHistogram.WithLabelValues("success").Observe(duration.Seconds())
		backupOperationsTotal.WithLabelValues("load", "success").Inc()
		backupAgeGauge.WithLabelValues(projectHash).Set(metadata.Age().Seconds())
		span.SetAttributes(
			attribute.Int64("restored_generation", metadata.Generation),
			attribute.Int64("restored_delta_count", metadata.DeltaCount),
		)
		logger.Info("restore completed",
			slog.Duration("duration", duration),
			slog.Int64("generation", metadata.Generation),
			slog.Int64("delta_count", metadata.DeltaCount),
			slog.Duration("backup_age", metadata.Age()),
		)
		return metadata, nil
	}

	// CRS-21: Current checkpoint failed. Try previous generation fallback.
	prevBackupPath := pm.PrevBackupPath(projectHash)
	prevMetadataPath := pm.PrevMetadataPath(projectHash)
	if _, statErr := os.Stat(prevBackupPath); os.IsNotExist(statErr) {
		// No previous checkpoint exists — return the original error.
		backupOperationsTotal.WithLabelValues("load", "error").Inc()
		return nil, loadErr
	}

	logger.Warn("CRS-PERSIST-02: Current checkpoint failed, falling back to previous",
		slog.String("error", loadErr.Error()),
	)

	metadata, fallbackErr := pm.loadBackupFromPath(ctx, projectHash, prevBackupPath, prevMetadataPath, journal, logger, span)
	if fallbackErr != nil {
		// Both checkpoints failed — return both errors for diagnostics.
		backupOperationsTotal.WithLabelValues("load", "error").Inc()
		return nil, fmt.Errorf("current checkpoint: %w; previous checkpoint: %v", loadErr, fallbackErr)
	}

	// Fallback succeeded.
	checkpointFallbackTotal.Inc()
	duration := time.Since(start)
	restoreDurationHistogram.WithLabelValues("fallback").Observe(duration.Seconds())
	backupOperationsTotal.WithLabelValues("load", "fallback").Inc()
	backupAgeGauge.WithLabelValues(projectHash).Set(metadata.Age().Seconds())
	span.SetAttributes(
		attribute.Int64("restored_generation", metadata.Generation),
		attribute.Int64("restored_delta_count", metadata.DeltaCount),
		attribute.Bool("fallback_used", true),
	)
	logger.Info("CRS-PERSIST-02: Fell back to previous checkpoint",
		slog.Duration("duration", duration),
		slog.Int64("generation", metadata.Generation),
		slog.Int64("delta_count", metadata.DeltaCount),
		slog.Duration("backup_age", metadata.Age()),
	)

	return metadata, nil
}

// loadBackupFromPath loads and validates a backup from a specific path.
//
// Description:
//
//	Core restore logic factored out of LoadBackup to support CRS-21
//	two-generation checkpoint fallback. Reads backup file, validates
//	metadata and SHA256 integrity, and restores journal state.
//
// Inputs:
//
//   - ctx: Context for cancellation and tracing.
//   - projectHash: Project identifier for metadata validation.
//   - backupPath: Path to the backup .gz file.
//   - metadataPath: Path to the corresponding metadata .json file.
//   - journal: BadgerDB journal to restore into.
//   - logger: Logger with trace context.
//   - span: OpenTelemetry span for tracing.
//
// Outputs:
//
//   - *BackupMetadata: Loaded metadata on success.
//   - error: Non-nil if any step fails.
//
// Thread Safety: Caller must hold the shared lock.
func (pm *PersistenceManager) loadBackupFromPath(ctx context.Context, projectHash string, backupPath string, metadataPath string, journal Journal, logger *slog.Logger, span trace.Span) (*BackupMetadata, error) {
	// Check if backup exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return nil, ErrBackupNotFound
	}

	// Read and validate metadata
	metadata, err := pm.readMetadataFromPath(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	// Version compatibility check
	if metadata.BadgerVersion != BadgerDBVersion {
		return nil, fmt.Errorf("%w: backup=%s, current=%s",
			ErrBackupVersionMismatch, metadata.BadgerVersion, BadgerDBVersion)
	}

	// Open backup file
	file, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()

	// Single-pass hash verification and restore (P3 optimization: O1)
	var reader io.Reader = file
	var validateHash bool

	hasher := sha256.New()
	if pm.config.ValidateOnRestore {
		reader = io.TeeReader(file, hasher)
		validateHash = true
	}

	// Decompress
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Perform restore
	if err := journal.Restore(ctx, gzipReader); err != nil {
		return nil, fmt.Errorf("journal restore: %w", err)
	}

	// Verify hash after restore completes
	if validateHash {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if actualHash != metadata.ContentHash {
			return nil, fmt.Errorf("%w: expected=%s, actual=%s",
				ErrBackupCorrupted, metadata.ContentHash, actualHash)
		}
		logger.Debug("backup integrity verified", slog.String("path", backupPath))
	}

	return metadata, nil
}

// verifyContentHash validates the file content against expected hash.
func (pm *PersistenceManager) verifyContentHash(file *os.File, expectedHash string) error {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("compute hash: %w", err)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("%w: expected=%s, actual=%s",
			ErrBackupCorrupted, expectedHash, actualHash)
	}

	return nil
}

// acquireLock obtains a file lock for backup operations.
func (pm *PersistenceManager) acquireLock(ctx context.Context, projectHash string, exclusive bool) (*os.File, error) {
	lockPath := pm.LockPath(projectHash)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0640)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	lockType := syscall.LOCK_SH
	if exclusive {
		lockType = syscall.LOCK_EX
	}

	// Try non-blocking first
	err = syscall.Flock(int(file.Fd()), lockType|syscall.LOCK_NB)
	if err == nil {
		return file, nil
	}

	// If would block, wait with timeout
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		file.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}

	// Create timeout context
	timeout := time.Duration(pm.config.LockTimeoutSec) * time.Second
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Poll for lock with exponential backoff (P2 fix: S1)
	// Start at 100ms, double each time, cap at 2s
	const (
		minBackoff = 100 * time.Millisecond
		maxBackoff = 2 * time.Second
	)
	backoff := minBackoff

	for {
		select {
		case <-lockCtx.Done():
			file.Close()
			return nil, fmt.Errorf("%w after %v: %w", ErrBackupLockFailed, timeout, lockCtx.Err())
		case <-time.After(backoff):
			err = syscall.Flock(int(file.Fd()), lockType|syscall.LOCK_NB)
			if err == nil {
				return file, nil
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				file.Close()
				return nil, fmt.Errorf("flock: %w", err)
			}
			// Exponential backoff with cap
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// releaseLock releases a file lock.
func (pm *PersistenceManager) releaseLock(file *os.File) {
	if file == nil {
		return
	}
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	file.Close()
}

// writeMetadata saves backup metadata to disk with integrity hash.
func (pm *PersistenceManager) writeMetadata(projectHash string, metadata *BackupMetadata) error {
	metaPath := pm.MetadataPath(projectHash)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(metaPath), 0750); err != nil {
		return fmt.Errorf("create metadata dir: %w", err)
	}

	// Compute metadata hash (excluding the MetadataHash field itself)
	metadata.MetadataHash = "" // Clear before computing
	hashData, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal for hash: %w", err)
	}
	hash := sha256.Sum256(hashData)
	metadata.MetadataHash = hex.EncodeToString(hash[:])

	// Marshal with the hash included
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	// CRS-21: Rotate current metadata → prev before writing new.
	prevMetaPath := pm.PrevMetadataPath(projectHash)
	if _, statErr := os.Stat(metaPath); statErr == nil {
		if renameErr := os.Rename(metaPath, prevMetaPath); renameErr != nil {
			// Log but don't fail — losing the prev metadata is acceptable.
			pm.logger.Warn("CRS-21: failed to rotate metadata to prev",
				slog.String("error", renameErr.Error()),
			)
		}
	}

	// Atomic write via temp file
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0640); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, metaPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// ErrMetadataCorrupted indicates the metadata file failed integrity check.
var ErrMetadataCorrupted = errors.New("metadata corrupted: hash mismatch")

// readMetadata loads backup metadata from disk and validates its integrity.
func (pm *PersistenceManager) readMetadata(projectHash string) (*BackupMetadata, error) {
	return pm.readMetadataFromPath(pm.MetadataPath(projectHash))
}

// readMetadataFromPath loads metadata from a specific file path.
// CRS-21: Factored out of readMetadata to support loading prev metadata.
func (pm *PersistenceManager) readMetadataFromPath(metaPath string) (*BackupMetadata, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var metadata BackupMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	// Validate metadata hash if present (P3 fix: I2 integrity check)
	if metadata.MetadataHash != "" {
		savedHash := metadata.MetadataHash
		metadata.MetadataHash = "" // Clear for hash computation

		hashData, err := json.Marshal(&metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal for hash verification: %w", err)
		}

		computedHash := sha256.Sum256(hashData)
		computedHashStr := hex.EncodeToString(computedHash[:])

		if computedHashStr != savedHash {
			return nil, fmt.Errorf("%w: expected=%s, computed=%s",
				ErrMetadataCorrupted, savedHash, computedHashStr)
		}

		// Restore the hash for the returned metadata
		metadata.MetadataHash = savedHash
	}

	return &metadata, nil
}

// HasBackup checks if a backup exists for a project.
//
// Thread Safety: Safe for concurrent use.
func (pm *PersistenceManager) HasBackup(projectHash string) bool {
	_, err := os.Stat(pm.BackupPath(projectHash))
	return err == nil
}

// GetBackupMetadata returns metadata for an existing backup.
//
// Outputs:
//   - *BackupMetadata: Metadata, or nil if no backup exists.
//   - error: Non-nil on read error (not on missing backup).
//
// Thread Safety: Safe for concurrent use.
func (pm *PersistenceManager) GetBackupMetadata(projectHash string) (*BackupMetadata, error) {
	if !pm.HasBackup(projectHash) {
		return nil, nil
	}
	return pm.readMetadata(projectHash)
}

// Close releases resources.
func (pm *PersistenceManager) Close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.closed {
		return nil
	}

	pm.closed = true
	pm.logger.Info("persistence manager closed")
	return nil
}

// countingWriter wraps a writer and counts bytes written.
type countingWriter struct {
	w     io.Writer
	count int64
}

func (cw *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.w.Write(p)
	cw.count += int64(n)
	return n, err
}

// syncDir syncs a directory to ensure durability of file operations.
// This is needed after atomic rename on some filesystems.
func syncDir(dirPath string) error {
	dir, err := os.Open(dirPath)
	if err != nil {
		return fmt.Errorf("open dir for sync: %w", err)
	}
	defer dir.Close()

	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}

	return nil
}
