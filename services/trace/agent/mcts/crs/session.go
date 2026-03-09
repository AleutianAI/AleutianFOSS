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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Session Constants (GR-36 Code Review Fix: S2 - Named Constants)
// -----------------------------------------------------------------------------

const (
	// DefaultCheckpointMaxAge is the maximum age of a checkpoint before invalidation.
	// GR-36 Code Review Fix: S2 - No magic numbers.
	DefaultCheckpointMaxAge = 7 * 24 * time.Hour

	// DefaultMaxFilesToRefresh is the maximum files to mark dirty after restore.
	// Beyond this threshold, a full rebuild is more efficient.
	DefaultMaxFilesToRefresh = 1000

	// CheckpointKeyHashBytes is the number of bytes used for checkpoint key hash.
	// GR-36 Code Review Fix: R1 - Use 16 bytes (128 bits) to match ProjectHashLength.
	CheckpointKeyHashBytes = 16

	// DefaultSessionRestoreRetries is the number of retry attempts on transient failures.
	// GR-36 Code Review Fix: R4 - Add retry logic.
	DefaultSessionRestoreRetries = 3

	// sessionRestoreBaseBackoff is the initial backoff duration for retries.
	sessionRestoreBaseBackoff = 100 * time.Millisecond
)

// -----------------------------------------------------------------------------
// Session Errors
// -----------------------------------------------------------------------------

var (
	// ErrSessionIdentifierNil is returned when session identifier is nil.
	ErrSessionIdentifierNil = errors.New("session identifier must not be nil")

	// ErrProjectPathEmpty is returned when project path is empty.
	ErrProjectPathEmpty = errors.New("project path must not be empty")

	// ErrCheckpointTooOld is returned when checkpoint exceeds max age.
	ErrCheckpointTooOld = errors.New("checkpoint too old")

	// ErrProjectHashMismatch is returned when project hash doesn't match checkpoint.
	ErrProjectHashMismatch = errors.New("project hash mismatch")

	// ErrSchemaVersionMismatch is returned when schema versions don't match.
	ErrSchemaVersionMismatch = errors.New("schema version mismatch")

	// ErrTooManyModifiedFiles is returned when modified file count exceeds threshold.
	ErrTooManyModifiedFiles = errors.New("too many modified files, full rebuild recommended")
)

// -----------------------------------------------------------------------------
// Session Metrics (GR-36 Code Review Fix: L3 - Missing Metrics)
// -----------------------------------------------------------------------------

var (
	sessionRestoreTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crs_session_restore_total",
		Help: "Total session restore attempts by status",
	}, []string{"status"})

	sessionRestoreDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "crs_session_restore_duration_seconds",
		Help:    "Session restore duration in seconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
	})

	sessionCheckpointAgeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "crs_session_checkpoint_age_seconds",
		Help: "Age of restored checkpoint in seconds",
	})

	sessionFilesModifiedGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "crs_session_files_modified",
		Help: "Number of files modified since last checkpoint",
	})

	sessionIdentifyDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "crs_session_identify_duration_seconds",
		Help:    "Session identification duration in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
	})
)

// -----------------------------------------------------------------------------
// Session Tracer
// -----------------------------------------------------------------------------

var sessionTracer = otel.Tracer("crs.session")

// -----------------------------------------------------------------------------
// SessionIdentifier
// -----------------------------------------------------------------------------

// SessionIdentifier uniquely identifies a project/workspace for checkpoint lookup.
//
// Description:
//
//	Combines the canonical project path with a content hash of lock files
//	to detect when dependencies change. The checkpoint key is a hash of
//	the project path for filesystem-safe storage.
//
// Thread Safety: Immutable after creation; safe for concurrent use.
type SessionIdentifier struct {
	// ProjectPath is the canonical absolute path to the project root.
	ProjectPath string `json:"project_path"`

	// ProjectHash is the SHA256 hash of lock files (go.mod, go.sum, etc.).
	// Used to detect dependency changes between sessions.
	ProjectHash string `json:"project_hash"`

	// GitCommitHash is the git commit hash if available.
	// Provides additional change detection beyond lock files.
	GitCommitHash string `json:"git_commit_hash,omitempty"`

	// ComputedAt is when this identifier was computed (Unix milliseconds UTC).
	ComputedAt int64 `json:"computed_at"`
}

// NewSessionIdentifier creates a session identifier for a project.
//
// Description:
//
//	Computes a stable project identifier from the project path and its
//	dependency lock files. The identifier includes a hash of go.mod,
//	go.sum, package.json, etc. to detect when the project changes.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - projectPath: Path to the project root. Must not be empty.
//
// Outputs:
//   - *SessionIdentifier: The computed identifier. Never nil on success.
//   - error: Non-nil if path resolution fails or context is nil.
//
// Example:
//
//	sid, err := crs.NewSessionIdentifier(ctx, "/path/to/project")
//	if err != nil {
//	    return fmt.Errorf("compute session ID: %w", err)
//	}
//	key := sid.CheckpointKey()
//
// Thread Safety: Safe for concurrent use.
func NewSessionIdentifier(ctx context.Context, projectPath string) (*SessionIdentifier, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	// GR-36 Code Review Fix: S5 - Input validation at boundaries
	if projectPath == "" {
		return nil, ErrProjectPathEmpty
	}

	// Start tracing (GR-36 Code Review Fix: L1 - Add span)
	ctx, span := sessionTracer.Start(ctx, "crs.NewSessionIdentifier",
		trace.WithAttributes(
			attribute.String("project_path", projectPath),
		),
	)
	defer span.End()

	start := time.Now()
	defer func() {
		sessionIdentifyDuration.Observe(time.Since(start).Seconds())
	}()

	// Resolve to absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "resolve path failed")
		return nil, fmt.Errorf("resolving project path: %w", err)
	}

	// Validate path exists
	info, err := os.Stat(absPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "stat path failed")
		return nil, fmt.Errorf("stat project path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project path is not a directory: %s", absPath)
	}

	// Compute project hash from lock files
	projectHash, err := computeProjectHashStreaming(ctx, absPath)
	if err != nil {
		// GR-36 Code Review Fix: R5 - Log but don't fail on missing lock files
		// Use path hash as fallback (GR-36 Code Review Fix: R6)
		projectHash = ComputeProjectHash(absPath)
		span.AddEvent("lock_file_hash_failed_using_path_hash",
			trace.WithAttributes(
				attribute.String("error", err.Error()),
				attribute.String("fallback_hash", projectHash),
			),
		)
	}

	// Try to get git commit hash for additional change detection
	gitHash := getGitCommitHash(absPath)

	span.SetAttributes(
		attribute.String("project_hash", projectHash),
		attribute.String("git_commit_hash", gitHash),
	)

	return &SessionIdentifier{
		ProjectPath:   absPath,
		ProjectHash:   projectHash,
		GitCommitHash: gitHash,
		ComputedAt:    time.Now().UnixMilli(),
	}, nil
}

// CheckpointKey returns a filesystem-safe key for checkpoint storage.
//
// Description:
//
//	Computes a SHA256 hash of the project path and returns the first
//	16 bytes (128 bits) as a hex string. This provides a stable key
//	for checkpoint storage that avoids filesystem issues with long paths.
//
// Outputs:
//   - string: 32-character hex string (16 bytes = 128 bits).
//
// Thread Safety: Safe for concurrent use.
func (s *SessionIdentifier) CheckpointKey() string {
	// GR-36 Code Review Fix: R1 - Use 16 bytes instead of 8 for lower collision risk
	// Returns just the hex hash to be compatible with PersistenceManager's
	// ValidateProjectHash (8-64 hex characters expected).
	h := sha256.Sum256([]byte(s.ProjectPath))
	return hex.EncodeToString(h[:CheckpointKeyHashBytes])
}

// Age returns the age of this session identifier.
//
// Thread Safety: Safe for concurrent use.
func (s *SessionIdentifier) Age() time.Duration {
	return time.Since(time.UnixMilli(s.ComputedAt))
}

// computeProjectHashStreaming computes project hash using streaming I/O.
//
// GR-36 Code Review Fix: O3 - Use streaming hash for large lock files.
func computeProjectHashStreaming(ctx context.Context, projectPath string) (string, error) {
	files := []string{
		filepath.Join(projectPath, "go.mod"),
		filepath.Join(projectPath, "go.sum"),
		filepath.Join(projectPath, "package.json"),
		filepath.Join(projectPath, "package-lock.json"),
		filepath.Join(projectPath, "yarn.lock"),
		filepath.Join(projectPath, "pnpm-lock.yaml"),
		filepath.Join(projectPath, "Cargo.lock"),
		filepath.Join(projectPath, "requirements.txt"),
		filepath.Join(projectPath, "poetry.lock"),
	}

	h := sha256.New()
	foundAny := false

	for _, f := range files {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// NEW-1 Fix: Use helper function with defer to ensure file.Close() is always called
		if hashFile(f, h) {
			foundAny = true
		}
	}

	if !foundAny {
		return "", errors.New("no lock files found")
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile hashes a single file into the provided hash.Hash.
// Returns true if file was successfully hashed, false otherwise.
// NEW-1 Fix: Uses defer to ensure file is closed even if io.Copy panics.
func hashFile(path string, h io.Writer) bool {
	file, err := os.Open(path)
	if err != nil {
		return false // File doesn't exist or can't be opened
	}
	defer file.Close()

	if _, err := io.Copy(h, file); err != nil {
		return false // File can't be read
	}
	return true
}

// getGitCommitHash returns the current git commit hash if in a git repository.
//
// Description:
//
//	Executes `git rev-parse HEAD` to get the current commit hash.
//	Returns empty string if not in a git repo or git command fails.
//
// Inputs:
//   - projectPath: Path to the project directory.
//
// Outputs:
//   - string: 40-character hex commit hash, or empty string on failure.
//
// Thread Safety: Safe for concurrent use.
//
// NEW-8, NEW-14 Fix: Added GoDoc and debug logging.
func getGitCommitHash(projectPath string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = projectPath
	output, err := cmd.Output()
	if err != nil {
		slog.Debug("git commit hash unavailable",
			slog.String("project_path", projectPath),
			slog.String("error", err.Error()),
		)
		return ""
	}
	return strings.TrimSpace(string(output))
}

// -----------------------------------------------------------------------------
// SessionRestorerConfig
// -----------------------------------------------------------------------------

// SessionRestorerConfig configures session restore behavior.
//
// Description:
//
//	Provides configuration options for session restore including
//	checkpoint age limits, file refresh thresholds, and git integration.
//
// Thread Safety: Immutable after creation; safe for concurrent use.
type SessionRestorerConfig struct {
	// CheckpointMaxAge is how old a checkpoint can be before it's invalid.
	// Default: 7 days.
	CheckpointMaxAge time.Duration

	// MaxFilesToRefresh is the maximum files to mark dirty after restore.
	// If more files changed, skip restore and trigger full rebuild.
	// Default: 1000.
	MaxFilesToRefresh int

	// UseGitStatus uses `git status` to find modified files instead of mtime scan.
	// Much faster for git repositories.
	// Default: true.
	UseGitStatus bool

	// MaxRetries is the number of retry attempts on transient failures.
	// Default: 3.
	MaxRetries int

	// Logger for session restore operations.
	// If nil, uses slog.Default().
	Logger *slog.Logger
}

// DefaultSessionRestorerConfig returns production defaults.
func DefaultSessionRestorerConfig() SessionRestorerConfig {
	return SessionRestorerConfig{
		CheckpointMaxAge:  DefaultCheckpointMaxAge,
		MaxFilesToRefresh: DefaultMaxFilesToRefresh,
		UseGitStatus:      true,
		MaxRetries:        DefaultSessionRestoreRetries,
		Logger:            slog.Default(),
	}
}

// Validate checks if the configuration is valid.
//
// GR-36 Code Review Fix: S6 - Add config validation.
func (c *SessionRestorerConfig) Validate() error {
	if c.CheckpointMaxAge <= 0 {
		return errors.New("checkpoint_max_age must be positive")
	}
	if c.MaxFilesToRefresh <= 0 {
		return errors.New("max_files_to_refresh must be positive")
	}
	if c.MaxRetries < 0 {
		return errors.New("max_retries must be non-negative")
	}
	return nil
}

// -----------------------------------------------------------------------------
// RestoreResult
// -----------------------------------------------------------------------------

// RestoreResult describes what happened during session restore.
//
// Description:
//
//	Contains the outcome of a restore attempt including whether it
//	succeeded, checkpoint details, and any modified files detected.
//
// Thread Safety: Immutable after creation; safe for concurrent use.
type RestoreResult struct {
	// Restored is true if a checkpoint was successfully restored.
	Restored bool `json:"restored"`

	// CheckpointID is the ID of the restored checkpoint (empty if not restored).
	CheckpointID string `json:"checkpoint_id,omitempty"`

	// Generation is the CRS generation of the restored state.
	Generation int64 `json:"generation,omitempty"`

	// CheckpointTime is when the checkpoint was created (Unix milliseconds UTC).
	// GR-36 Code Review Fix: S3 - Use int64 instead of time.Time.
	CheckpointTime int64 `json:"checkpoint_time,omitempty"`

	// CheckpointAge is how old the checkpoint was at restore time.
	CheckpointAge time.Duration `json:"checkpoint_age,omitempty"`

	// Reason explains why restore succeeded or failed.
	Reason string `json:"reason"`

	// ModifiedFiles is the list of files modified since checkpoint.
	ModifiedFiles []string `json:"modified_files,omitempty"`

	// ModifiedFileCount is the total count (may exceed len(ModifiedFiles) if truncated).
	ModifiedFileCount int `json:"modified_file_count"`

	// DurationMs is how long the restore took in milliseconds.
	DurationMs int64 `json:"duration_ms"`
}

// -----------------------------------------------------------------------------
// SessionRestorer
// -----------------------------------------------------------------------------

// SessionRestorer handles checkpoint loading at session start.
//
// Description:
//
//	Integrates with GR-33's PersistenceManager to load checkpoints
//	and restore CRS state. Validates checkpoint compatibility using
//	project hash and age checks.
//
// Thread Safety: Safe for concurrent use.
type SessionRestorer struct {
	pm     *PersistenceManager
	config SessionRestorerConfig
	logger *slog.Logger
}

// NewSessionRestorer creates a new session restorer.
//
// Description:
//
//	Creates a restorer that uses the provided PersistenceManager
//	for checkpoint storage. Reuses GR-33 infrastructure instead
//	of creating a separate CheckpointStorage interface.
//
// Inputs:
//   - pm: Persistence manager from GR-33. Must not be nil.
//   - config: Configuration. If nil, uses DefaultSessionRestorerConfig().
//
// Outputs:
//   - *SessionRestorer: The new restorer. Never nil on success.
//   - error: Non-nil if pm is nil or config is invalid.
//
// Thread Safety: Safe for concurrent use.
func NewSessionRestorer(pm *PersistenceManager, config *SessionRestorerConfig) (*SessionRestorer, error) {
	if pm == nil {
		return nil, errors.New("persistence manager must not be nil")
	}

	if config == nil {
		cfg := DefaultSessionRestorerConfig()
		config = &cfg
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SessionRestorer{
		pm:     pm,
		config: *config,
		logger: logger.With(slog.String("component", "session_restorer")),
	}, nil
}

// TryRestore attempts to restore CRS state from a previous session.
//
// Description:
//
//	Loads a checkpoint from disk and restores it into the CRS instance
//	if it passes validation. Validation includes project hash matching
//	and age checks. Returns a RestoreResult describing the outcome.
//
//	This method uses GR-33's LoadBackup infrastructure rather than
//	creating a parallel path.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - crsi: The CRS instance to restore into. Must not be nil.
//   - journal: The Journal for replay. Must not be nil.
//   - sessionID: Session identifier for checkpoint lookup. Must not be nil.
//
// Outputs:
//   - *RestoreResult: Describes what happened during restore. Never nil on success.
//   - error: Non-nil only for fatal errors (restore failures return in result).
//
// Example:
//
//	result, err := restorer.TryRestore(ctx, crs, journal, sessionID)
//	if err != nil {
//	    return fmt.Errorf("restore: %w", err)
//	}
//	if result.Restored {
//	    log.Info("restored checkpoint", "generation", result.Generation)
//	}
//
// Thread Safety: Safe for concurrent use.
func (r *SessionRestorer) TryRestore(
	ctx context.Context,
	crsi CRS,
	journal Journal,
	sessionID *SessionIdentifier,
) (*RestoreResult, error) {
	// GR-36 Code Review Fix: S5 - Input validation
	if ctx == nil {
		return nil, ErrNilContext
	}
	if crsi == nil {
		return nil, errors.New("crs must not be nil")
	}
	if journal == nil {
		return nil, errors.New("journal must not be nil")
	}
	if sessionID == nil {
		return nil, ErrSessionIdentifierNil
	}

	// Start tracing (GR-36 Code Review Fix: L1 - Add OTel span)
	ctx, span := sessionTracer.Start(ctx, "crs.SessionRestorer.TryRestore",
		trace.WithAttributes(
			attribute.String("project_path", sessionID.ProjectPath),
			attribute.String("project_hash", sessionID.ProjectHash),
			attribute.String("checkpoint_key", sessionID.CheckpointKey()),
		),
	)
	defer span.End()

	start := time.Now()

	// Use trace-aware logger (GR-36 Code Review Fix: L2)
	logger := loggerWithTrace(ctx, r.logger).With(
		slog.String("project_hash", sessionID.ProjectHash),
		slog.String("checkpoint_key", sessionID.CheckpointKey()),
	)

	logger.Info("attempting session restore")

	// GR-36 Code Review Fix: R4 - Add retry logic
	var lastErr error
retryLoop:
	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		result, err := r.tryRestoreOnce(ctx, crsi, journal, sessionID, logger, span)
		if err == nil {
			// Update metrics on success
			duration := time.Since(start)
			result.DurationMs = duration.Milliseconds()
			sessionRestoreDuration.Observe(duration.Seconds())

			if result.Restored {
				sessionRestoreTotal.WithLabelValues("success").Inc()
				sessionCheckpointAgeGauge.Set(result.CheckpointAge.Seconds())
				sessionFilesModifiedGauge.Set(float64(result.ModifiedFileCount))
				// NEW-9 Fix: Set span status to Ok on successful restore
				span.SetStatus(codes.Ok, "restored")
			}

			return result, nil
		}

		lastErr = err

		// Don't retry on non-transient errors
		if errors.Is(err, ErrCheckpointTooOld) ||
			errors.Is(err, ErrProjectHashMismatch) ||
			errors.Is(err, ErrSchemaVersionMismatch) ||
			errors.Is(err, ErrBackupNotFound) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			break retryLoop
		}

		if attempt < r.config.MaxRetries {
			backoff := sessionRestoreBaseBackoff << attempt
			logger.Warn("restore attempt failed, retrying",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", r.config.MaxRetries),
				slog.String("error", err.Error()),
				slog.Duration("backoff", backoff),
			)
			// NEW-13 Fix: Respect context cancellation during backoff
			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
				break retryLoop
			case <-time.After(backoff):
			}
		}
	}

	// All retries exhausted
	sessionRestoreTotal.WithLabelValues("error").Inc()
	span.RecordError(lastErr)

	return &RestoreResult{
		Restored:   false,
		Reason:     fmt.Sprintf("restore failed after retries: %v", lastErr),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// tryRestoreOnce performs a single restore attempt.
func (r *SessionRestorer) tryRestoreOnce(
	ctx context.Context,
	crsi CRS,
	journal Journal,
	sessionID *SessionIdentifier,
	logger *slog.Logger,
	span trace.Span,
) (*RestoreResult, error) {
	checkpointKey := sessionID.CheckpointKey()

	// Check if backup exists
	if !r.pm.HasBackup(checkpointKey) {
		sessionRestoreTotal.WithLabelValues("no_checkpoint").Inc()
		return &RestoreResult{
			Restored: false,
			Reason:   "no checkpoint found",
		}, nil
	}

	// Read metadata for validation before loading
	metadata, err := r.pm.GetBackupMetadata(checkpointKey)
	if err != nil {
		sessionRestoreTotal.WithLabelValues("metadata_error").Inc()
		span.RecordError(err)
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	if metadata == nil {
		sessionRestoreTotal.WithLabelValues("no_metadata").Inc()
		return &RestoreResult{
			Restored: false,
			Reason:   "no metadata found",
		}, nil
	}

	// Validate checkpoint before loading
	if err := r.validateCheckpoint(ctx, metadata, sessionID); err != nil {
		sessionRestoreTotal.WithLabelValues("incompatible").Inc()
		logger.Warn("checkpoint incompatible",
			slog.String("error", err.Error()),
			slog.Int64("checkpoint_generation", metadata.Generation),
		)
		return &RestoreResult{
			Restored:       false,
			CheckpointTime: metadata.CreatedAt,
			CheckpointAge:  metadata.Age(),
			Reason:         fmt.Sprintf("incompatible: %v", err),
		}, nil
	}

	// Load backup using GR-33 infrastructure (GR-36 Code Review Fix: C6)
	loadedMeta, err := r.pm.LoadBackup(ctx, checkpointKey, journal)
	if err != nil {
		sessionRestoreTotal.WithLabelValues("load_error").Inc()
		span.RecordError(err)
		return nil, fmt.Errorf("load backup: %w", err)
	}

	// Replay journal to restore CRS state
	deltas, err := journal.Replay(ctx)
	if err != nil {
		sessionRestoreTotal.WithLabelValues("replay_error").Inc()
		span.RecordError(err)
		return nil, fmt.Errorf("replay journal: %w", err)
	}

	// Apply all deltas, pruning stale similarity entries (CRS-15).
	prunedSimilarityPairs := 0
	for i, delta := range deltas {
		// CRS-15: Skip similarity deltas older than 7 days to prevent
		// unbounded growth of cross-session similarity data.
		if simDelta, ok := delta.(*SimilarityDelta); ok {
			age := time.Since(time.UnixMilli(simDelta.Timestamp()))
			if age > DefaultCheckpointMaxAge {
				// CRS-15 Code Review Fix #4: Log pair count, not delta count.
				prunedSimilarityPairs += len(simDelta.Updates)
				logger.Debug("CRS-15: Pruning stale similarity delta",
					slog.Duration("age", age),
					slog.Int("pairs", len(simDelta.Updates)),
				)
				continue
			}
		}

		if _, err := crsi.Apply(ctx, delta); err != nil {
			sessionRestoreTotal.WithLabelValues("apply_error").Inc()
			span.RecordError(err)
			return nil, fmt.Errorf("apply delta %d: %w", i, err)
		}
	}

	if prunedSimilarityPairs > 0 {
		logger.Info("CRS-15: Pruned stale similarity pairs during restore",
			slog.Int("pruned_pairs", prunedSimilarityPairs),
		)
	}

	// NEW-12 Fix: Checkpoint journal after successful restore to prevent
	// replaying the same deltas in subsequent sessions.
	if err := journal.Checkpoint(ctx); err != nil {
		// Log but don't fail - restore succeeded, checkpoint is optimization
		logger.Warn("failed to checkpoint journal after restore",
			slog.String("error", err.Error()),
		)
	}

	// GR-36 Code Review Fix: I6 - Verify restored generation
	if crsi.Generation() < loadedMeta.Generation {
		logger.Warn("restored generation lower than expected",
			slog.Int64("expected", loadedMeta.Generation),
			slog.Int64("actual", crsi.Generation()),
		)
	}

	span.SetAttributes(
		attribute.Int64("restored_generation", loadedMeta.Generation),
		attribute.Int("deltas_replayed", len(deltas)),
		attribute.Float64("checkpoint_age_seconds", loadedMeta.Age().Seconds()),
	)

	logger.Info("session restored from checkpoint",
		slog.Int64("generation", loadedMeta.Generation),
		slog.Int("deltas_replayed", len(deltas)),
		slog.Duration("checkpoint_age", loadedMeta.Age()),
	)

	return &RestoreResult{
		Restored:       true,
		CheckpointID:   loadedMeta.SessionID,
		Generation:     loadedMeta.Generation,
		CheckpointTime: loadedMeta.CreatedAt,
		CheckpointAge:  loadedMeta.Age(),
		Reason:         "success",
	}, nil
}

// validateCheckpoint validates checkpoint compatibility.
//
// GR-36 Code Review Fix: R2 - Safe type assertion.
// GR-36 Code Review Fix: R3 - Proper timestamp handling.
// GR-36 Code Review Fix: I2 - Schema version check.
func (r *SessionRestorer) validateCheckpoint(
	ctx context.Context,
	metadata *BackupMetadata,
	sessionID *SessionIdentifier,
) error {
	// Check schema version (GR-36 Code Review Fix: I2)
	if metadata.SchemaVersion != "" && metadata.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("%w: backup=%s, current=%s",
			ErrSchemaVersionMismatch, metadata.SchemaVersion, CurrentSchemaVersion)
	}

	// Check BadgerDB version
	if metadata.BadgerVersion != BadgerDBVersion {
		return fmt.Errorf("%w: backup=%s, current=%s",
			ErrBackupVersionMismatch, metadata.BadgerVersion, BadgerDBVersion)
	}

	// Check checkpoint age (GR-36 Code Review Fix: R3)
	// metadata.CreatedAt is int64 Unix milliseconds
	age := metadata.Age()
	if age > r.config.CheckpointMaxAge {
		return fmt.Errorf("%w: age=%v, max=%v", ErrCheckpointTooOld, age, r.config.CheckpointMaxAge)
	}

	// CRS-17: Validate project identity to prevent cross-project checkpoint contamination.
	// cleanupPersistence() passes the checkpoint key (path hash) to SaveBackup(),
	// which stores it as metadata.ProjectHash. Compare against the current session's
	// CheckpointKey() to detect misconfigured checkpoint directories.
	if metadata.ProjectHash != "" && sessionID != nil {
		currentKey := sessionID.CheckpointKey()
		if metadata.ProjectHash != currentKey {
			return fmt.Errorf("%w: backup=%s, current=%s",
				ErrProjectHashMismatch, metadata.ProjectHash, currentKey)
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// Modified File Detection
// -----------------------------------------------------------------------------

// FindFilesModifiedSince finds files changed since a timestamp.
//
// Description:
//
//	Uses git status for git repositories (much faster) and falls back
//	to mtime scan for non-git directories. Returns an error if more
//	files than MaxFilesToRefresh are found.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - projectPath: Path to the project root.
//   - since: Find files modified after this time.
//   - config: Configuration with UseGitStatus and MaxFilesToRefresh.
//
// Outputs:
//   - []string: Paths of modified files relative to project root.
//   - error: Non-nil on failure or if too many files modified.
//
// Thread Safety: Safe for concurrent use.
func FindFilesModifiedSince(
	ctx context.Context,
	projectPath string,
	since time.Time,
	config *SessionRestorerConfig,
) ([]string, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	ctx, span := sessionTracer.Start(ctx, "crs.FindFilesModifiedSince",
		trace.WithAttributes(
			attribute.String("project_path", projectPath),
			attribute.String("since", since.Format(time.RFC3339)),
		),
	)
	defer span.End()

	if config == nil {
		cfg := DefaultSessionRestorerConfig()
		config = &cfg
	}

	// Try git first if enabled
	if config.UseGitStatus {
		files, err := findModifiedViaGit(ctx, projectPath, since, config.MaxFilesToRefresh)
		if err == nil {
			span.SetAttributes(
				attribute.String("method", "git"),
				attribute.Int("file_count", len(files)),
			)
			return files, nil
		}
		// Fall through to mtime scan
	}

	// Fallback to mtime scan
	files, err := findModifiedViaMtime(ctx, projectPath, since, config.MaxFilesToRefresh)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(
		attribute.String("method", "mtime"),
		attribute.Int("file_count", len(files)),
	)

	return files, nil
}

// findModifiedViaGit uses git to find modified files.
func findModifiedViaGit(ctx context.Context, projectPath string, since time.Time, maxFiles int) ([]string, error) {
	// git diff --name-only --diff-filter=ACMRT @{since}
	// Use ISO format for git date parsing
	sinceStr := since.Format("2006-01-02T15:04:05")

	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=ACMRT",
		fmt.Sprintf("@{%s}", sinceStr))
	cmd.Dir = projectPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// NEW-2 Fix: Handle empty output correctly
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []string{}, nil
	}

	lines := strings.Split(trimmed, "\n")

	// GR-36 Code Review Fix: R7 - Check max files limit
	if len(lines) > maxFiles {
		return nil, fmt.Errorf("%w: found %d files, max %d",
			ErrTooManyModifiedFiles, len(lines), maxFiles)
	}

	return lines, nil
}

// findModifiedViaMtime uses file mtime to find modified files.
func findModifiedViaMtime(ctx context.Context, projectPath string, since time.Time, maxFiles int) ([]string, error) {
	var files []string
	sinceUnix := since.Unix()

	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // Skip files we can't access
		}

		// Skip directories and hidden files
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if modified after since
		if info.ModTime().Unix() > sinceUnix {
			// NEW-6 Fix: Check max files limit BEFORE computing relPath
			if len(files) >= maxFiles {
				return fmt.Errorf("%w: found more than %d files",
					ErrTooManyModifiedFiles, maxFiles)
			}

			relPath, err := filepath.Rel(projectPath, path)
			if err != nil {
				return nil
			}
			files = append(files, relPath)
		}

		return nil
	})

	if err != nil && !errors.Is(err, ErrTooManyModifiedFiles) {
		return nil, fmt.Errorf("walk: %w", err)
	}

	return files, err
}

// -----------------------------------------------------------------------------
// Session Events (GR-36 Code Review Fix: L5 - Event Emission)
// -----------------------------------------------------------------------------

// SessionRestoredEvent is emitted when a session is successfully restored.
type SessionRestoredEvent struct {
	// ProjectPath is the project that was restored.
	ProjectPath string

	// ProjectHash is the hash used to identify the project.
	ProjectHash string

	// Generation is the CRS generation after restore.
	Generation int64

	// CheckpointAge is how old the restored checkpoint was.
	CheckpointAge time.Duration

	// ModifiedFileCount is the number of files modified since checkpoint.
	ModifiedFileCount int

	// RestoreDuration is how long the restore took.
	RestoreDuration time.Duration
}

// SessionRestoreFailedEvent is emitted when session restore fails.
type SessionRestoreFailedEvent struct {
	// ProjectPath is the project that failed to restore.
	ProjectPath string

	// Reason explains why restore failed.
	Reason string

	// Error is the underlying error if any.
	Error error
}
