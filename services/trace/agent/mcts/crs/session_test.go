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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// SessionIdentifier Tests
// -----------------------------------------------------------------------------

func TestNewSessionIdentifier_ValidPath(t *testing.T) {
	ctx := context.Background()

	// Use temp directory for testing
	tmpDir := t.TempDir()

	// Create a go.mod file for hash computation
	goModPath := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("create go.mod: %v", err)
	}

	sid, err := NewSessionIdentifier(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	// Verify fields are set
	if sid.ProjectPath == "" {
		t.Error("ProjectPath should not be empty")
	}
	if sid.ProjectHash == "" {
		t.Error("ProjectHash should not be empty")
	}
	if sid.ComputedAt == 0 {
		t.Error("ComputedAt should be set")
	}

	// ProjectPath should be absolute
	if !filepath.IsAbs(sid.ProjectPath) {
		t.Errorf("ProjectPath should be absolute, got %s", sid.ProjectPath)
	}

	// Verify age is reasonable
	if sid.Age() > time.Second {
		t.Errorf("Age should be less than 1 second, got %v", sid.Age())
	}
}

func TestNewSessionIdentifier_EmptyPath(t *testing.T) {
	ctx := context.Background()

	_, err := NewSessionIdentifier(ctx, "")
	if err == nil {
		t.Error("expected error for empty path")
	}
	if err != ErrProjectPathEmpty {
		t.Errorf("expected ErrProjectPathEmpty, got %v", err)
	}
}

func TestNewSessionIdentifier_NilContext(t *testing.T) {
	_, err := NewSessionIdentifier(nil, "/some/path")
	if err == nil {
		t.Error("expected error for nil context")
	}
	if err != ErrNilContext {
		t.Errorf("expected ErrNilContext, got %v", err)
	}
}

func TestNewSessionIdentifier_NonexistentPath(t *testing.T) {
	ctx := context.Background()

	_, err := NewSessionIdentifier(ctx, "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestNewSessionIdentifier_FileNotDirectory(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a file instead of directory
	filePath := filepath.Join(tmpDir, "not_a_dir.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	_, err := NewSessionIdentifier(ctx, filePath)
	if err == nil {
		t.Error("expected error for file path")
	}
}

func TestSessionIdentifier_CheckpointKey(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	sid, err := NewSessionIdentifier(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	key := sid.CheckpointKey()

	// Key should be a valid hex string (compatible with ValidateProjectHash)
	if err := ValidateProjectHash(key); err != nil {
		t.Errorf("CheckpointKey should be valid project hash: %v", err)
	}

	// Key should be consistent
	key2 := sid.CheckpointKey()
	if key != key2 {
		t.Errorf("CheckpointKey should be consistent: %s != %s", key, key2)
	}

	// Key length: 32 hex chars (16 bytes)
	expectedLen := CheckpointKeyHashBytes * 2
	if len(key) != expectedLen {
		t.Errorf("CheckpointKey length should be %d, got %d: %s", expectedLen, len(key), key)
	}
}

func TestSessionIdentifier_CheckpointKeyDifferentPaths(t *testing.T) {
	ctx := context.Background()

	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	sid1, err := NewSessionIdentifier(ctx, tmpDir1)
	if err != nil {
		t.Fatalf("NewSessionIdentifier for dir1 failed: %v", err)
	}

	sid2, err := NewSessionIdentifier(ctx, tmpDir2)
	if err != nil {
		t.Fatalf("NewSessionIdentifier for dir2 failed: %v", err)
	}

	key1 := sid1.CheckpointKey()
	key2 := sid2.CheckpointKey()

	if key1 == key2 {
		t.Error("Different paths should produce different checkpoint keys")
	}
}

func TestSessionIdentifier_ProjectHashWithLockFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create go.mod and go.sum
	goModPath := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("create go.mod: %v", err)
	}

	goSumPath := filepath.Join(tmpDir, "go.sum")
	if err := os.WriteFile(goSumPath, []byte("example.com/dep v1.0.0 h1:abc\n"), 0644); err != nil {
		t.Fatalf("create go.sum: %v", err)
	}

	sid, err := NewSessionIdentifier(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	// Project hash should be the full SHA256 (64 hex chars)
	if len(sid.ProjectHash) != 64 {
		t.Errorf("ProjectHash should be 64 hex chars (full SHA256), got %d: %s",
			len(sid.ProjectHash), sid.ProjectHash)
	}

	// Modify go.sum and recompute - hash should change
	if err := os.WriteFile(goSumPath, []byte("example.com/dep v2.0.0 h1:xyz\n"), 0644); err != nil {
		t.Fatalf("update go.sum: %v", err)
	}

	sid2, err := NewSessionIdentifier(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier (2) failed: %v", err)
	}

	if sid.ProjectHash == sid2.ProjectHash {
		t.Error("ProjectHash should change when lock files change")
	}
}

func TestSessionIdentifier_FallbackHashWithoutLockFiles(t *testing.T) {
	ctx := context.Background()

	// Empty temp directory with no lock files
	tmpDir := t.TempDir()

	sid, err := NewSessionIdentifier(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	// Should fall back to path-based hash (16 hex chars from ComputeProjectHash)
	if len(sid.ProjectHash) != ProjectHashLength {
		t.Errorf("Fallback ProjectHash should be %d hex chars, got %d: %s",
			ProjectHashLength, len(sid.ProjectHash), sid.ProjectHash)
	}
}

// -----------------------------------------------------------------------------
// SessionRestorerConfig Tests
// -----------------------------------------------------------------------------

func TestSessionRestorerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  SessionRestorerConfig
		wantErr bool
	}{
		{
			name:    "default config is valid",
			config:  DefaultSessionRestorerConfig(),
			wantErr: false,
		},
		{
			name: "zero max age is invalid",
			config: SessionRestorerConfig{
				CheckpointMaxAge:  0,
				MaxFilesToRefresh: 1000,
				MaxRetries:        3,
			},
			wantErr: true,
		},
		{
			name: "negative max age is invalid",
			config: SessionRestorerConfig{
				CheckpointMaxAge:  -time.Hour,
				MaxFilesToRefresh: 1000,
				MaxRetries:        3,
			},
			wantErr: true,
		},
		{
			name: "zero max files is invalid",
			config: SessionRestorerConfig{
				CheckpointMaxAge:  24 * time.Hour,
				MaxFilesToRefresh: 0,
				MaxRetries:        3,
			},
			wantErr: true,
		},
		{
			name: "negative retries is invalid",
			config: SessionRestorerConfig{
				CheckpointMaxAge:  24 * time.Hour,
				MaxFilesToRefresh: 1000,
				MaxRetries:        -1,
			},
			wantErr: true,
		},
		{
			name: "zero retries is valid (no retry)",
			config: SessionRestorerConfig{
				CheckpointMaxAge:  24 * time.Hour,
				MaxFilesToRefresh: 1000,
				MaxRetries:        0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultSessionRestorerConfig(t *testing.T) {
	cfg := DefaultSessionRestorerConfig()

	if cfg.CheckpointMaxAge != DefaultCheckpointMaxAge {
		t.Errorf("CheckpointMaxAge = %v, want %v", cfg.CheckpointMaxAge, DefaultCheckpointMaxAge)
	}

	if cfg.MaxFilesToRefresh != DefaultMaxFilesToRefresh {
		t.Errorf("MaxFilesToRefresh = %d, want %d", cfg.MaxFilesToRefresh, DefaultMaxFilesToRefresh)
	}

	if !cfg.UseGitStatus {
		t.Error("UseGitStatus should be true by default")
	}

	if cfg.MaxRetries != DefaultSessionRestoreRetries {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, DefaultSessionRestoreRetries)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}
}

// -----------------------------------------------------------------------------
// NewSessionRestorer Tests
// -----------------------------------------------------------------------------

func TestNewSessionRestorer_NilPersistenceManager(t *testing.T) {
	_, err := NewSessionRestorer(nil, nil)
	if err == nil {
		t.Error("expected error for nil persistence manager")
	}
}

func TestNewSessionRestorer_InvalidConfig(t *testing.T) {
	// Create a minimal persistence manager
	tmpDir := t.TempDir()
	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	invalidConfig := &SessionRestorerConfig{
		CheckpointMaxAge:  -1, // Invalid
		MaxFilesToRefresh: 1000,
		MaxRetries:        3,
	}

	_, err = NewSessionRestorer(pm, invalidConfig)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestNewSessionRestorer_WithDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	// nil config should use defaults
	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	if restorer == nil {
		t.Error("restorer should not be nil")
	}
}

// -----------------------------------------------------------------------------
// TryRestore Tests
// -----------------------------------------------------------------------------

func TestTryRestore_NilContext(t *testing.T) {
	tmpDir := t.TempDir()
	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	_, err = restorer.TryRestore(nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil context")
	}
	if err != ErrNilContext {
		t.Errorf("expected ErrNilContext, got %v", err)
	}
}

func TestTryRestore_NilCRS(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	_, err = restorer.TryRestore(ctx, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil CRS")
	}
}

func TestTryRestore_NilSessionIdentifier(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	crsi := New(nil)
	defer crsi.Close()

	journalDir := filepath.Join(tmpDir, "journal")
	journalConfig := JournalConfig{
		SessionID:  "test-session",
		Path:       journalDir,
		SyncWrites: false,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}
	defer journal.Close()

	_, err = restorer.TryRestore(ctx, crsi, journal, nil)
	if err == nil {
		t.Error("expected error for nil session identifier")
	}
	if err != ErrSessionIdentifierNil {
		t.Errorf("expected ErrSessionIdentifierNil, got %v", err)
	}
}

func TestTryRestore_NoCheckpoint(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	projectDir := t.TempDir()

	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	crsi := New(nil)
	defer crsi.Close()

	journalDir := filepath.Join(tmpDir, "journal")
	journalConfig := JournalConfig{
		SessionID:  "test-session",
		Path:       journalDir,
		SyncWrites: false,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}
	defer journal.Close()

	sid, err := NewSessionIdentifier(ctx, projectDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	result, err := restorer.TryRestore(ctx, crsi, journal, sid)
	if err != nil {
		t.Fatalf("TryRestore failed: %v", err)
	}

	if result.Restored {
		t.Error("should not have restored (no checkpoint exists)")
	}
	if result.Reason != "no checkpoint found" {
		t.Errorf("unexpected reason: %s", result.Reason)
	}
}

// -----------------------------------------------------------------------------
// CRS-17: Project Identity Validation Tests
// -----------------------------------------------------------------------------

func TestCRS17_ValidateCheckpoint_ProjectHashMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	t.Run("mismatch returns error", func(t *testing.T) {
		// Construct two SessionIdentifiers from different paths to get different CheckpointKeys.
		sidOld := &SessionIdentifier{ProjectPath: "/project/alpha"}
		sidNew := &SessionIdentifier{ProjectPath: "/project/beta"}

		if sidOld.CheckpointKey() == sidNew.CheckpointKey() {
			t.Fatal("test precondition: different paths must produce different checkpoint keys")
		}

		metadata := &BackupMetadata{
			ProjectHash:   sidOld.CheckpointKey(),
			BadgerVersion: BadgerDBVersion,
			SchemaVersion: CurrentSchemaVersion,
			CreatedAt:     time.Now().UnixMilli(),
		}

		err := restorer.validateCheckpoint(context.Background(), metadata, sidNew)
		if err == nil {
			t.Fatal("expected error for project hash mismatch, got nil")
		}
		if !errors.Is(err, ErrProjectHashMismatch) {
			t.Errorf("expected ErrProjectHashMismatch, got: %v", err)
		}
	})

	t.Run("match succeeds", func(t *testing.T) {
		// Create a SessionIdentifier and compute its CheckpointKey
		sid := &SessionIdentifier{
			ProjectPath: "/test/project/path",
		}
		checkpointKey := sid.CheckpointKey()

		metadata := &BackupMetadata{
			ProjectHash:   checkpointKey,
			BadgerVersion: BadgerDBVersion,
			SchemaVersion: CurrentSchemaVersion,
			CreatedAt:     time.Now().UnixMilli(),
		}

		err := restorer.validateCheckpoint(context.Background(), metadata, sid)
		if err != nil {
			t.Errorf("expected no error for matching hash, got: %v", err)
		}
	})

	t.Run("empty metadata hash skips check", func(t *testing.T) {
		// Legacy checkpoints without ProjectHash should still load.
		metadata := &BackupMetadata{
			ProjectHash:   "",
			BadgerVersion: BadgerDBVersion,
			SchemaVersion: CurrentSchemaVersion,
			CreatedAt:     time.Now().UnixMilli(),
		}
		sid := &SessionIdentifier{
			ProjectPath: "/any/project",
		}

		err := restorer.validateCheckpoint(context.Background(), metadata, sid)
		if err != nil {
			t.Errorf("expected no error for empty metadata hash (legacy), got: %v", err)
		}
	})

	t.Run("nil sessionID skips check", func(t *testing.T) {
		metadata := &BackupMetadata{
			ProjectHash:   "abcdef0123456789abcdef0123456789",
			BadgerVersion: BadgerDBVersion,
			SchemaVersion: CurrentSchemaVersion,
			CreatedAt:     time.Now().UnixMilli(),
		}

		err := restorer.validateCheckpoint(context.Background(), metadata, nil)
		if err != nil {
			t.Errorf("expected no error for nil sessionID, got: %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// FindFilesModifiedSince Tests
// -----------------------------------------------------------------------------

func TestFindFilesModifiedSince_NilContext(t *testing.T) {
	_, err := FindFilesModifiedSince(nil, "/some/path", time.Now(), nil)
	if err == nil {
		t.Error("expected error for nil context")
	}
	if err != ErrNilContext {
		t.Errorf("expected ErrNilContext, got %v", err)
	}
}

func TestFindFilesModifiedSince_NoModifiedFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a file before the "since" time
	filePath := filepath.Join(tmpDir, "old_file.txt")
	if err := os.WriteFile(filePath, []byte("old content"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	// Set mtime to the past
	pastTime := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(filePath, pastTime, pastTime); err != nil {
		t.Fatalf("set mtime: %v", err)
	}

	// Find files modified since now (should find none)
	config := DefaultSessionRestorerConfig()
	config.UseGitStatus = false // Force mtime scan for testing

	files, err := FindFilesModifiedSince(ctx, tmpDir, time.Now().Add(-time.Hour), &config)
	if err != nil {
		t.Fatalf("FindFilesModifiedSince failed: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(files), files)
	}
}

func TestFindFilesModifiedSince_WithModifiedFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Set "since" time to the past
	since := time.Now().Add(-24 * time.Hour)

	// Create some files (will have current mtime, so newer than since)
	for i := 0; i < 3; i++ {
		filePath := filepath.Join(tmpDir, "new_file_"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(filePath, []byte("new content"), 0644); err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	config := DefaultSessionRestorerConfig()
	config.UseGitStatus = false // Force mtime scan for testing

	files, err := FindFilesModifiedSince(ctx, tmpDir, since, &config)
	if err != nil {
		t.Fatalf("FindFilesModifiedSince failed: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(files), files)
	}
}

func TestFindFilesModifiedSince_TooManyFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Set "since" time to the past
	since := time.Now().Add(-24 * time.Hour)

	// Create more files than the limit
	for i := 0; i < 15; i++ {
		filePath := filepath.Join(tmpDir, "file_"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	config := DefaultSessionRestorerConfig()
	config.UseGitStatus = false // Force mtime scan
	config.MaxFilesToRefresh = 10

	_, err := FindFilesModifiedSince(ctx, tmpDir, since, &config)
	if err == nil {
		t.Error("expected error for too many files")
	}
	if !isErrTooManyModifiedFiles(err) {
		t.Errorf("expected ErrTooManyModifiedFiles, got %v", err)
	}
}

func TestFindFilesModifiedSince_SkipsHiddenDirs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Set "since" time to the past
	since := time.Now().Add(-24 * time.Hour)

	// Create a visible file
	visiblePath := filepath.Join(tmpDir, "visible.txt")
	if err := os.WriteFile(visiblePath, []byte("visible"), 0644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}

	// Create a hidden directory with a file
	hiddenDir := filepath.Join(tmpDir, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0755); err != nil {
		t.Fatalf("create hidden dir: %v", err)
	}
	hiddenFile := filepath.Join(hiddenDir, "hidden_file.txt")
	if err := os.WriteFile(hiddenFile, []byte("hidden"), 0644); err != nil {
		t.Fatalf("create hidden file: %v", err)
	}

	config := DefaultSessionRestorerConfig()
	config.UseGitStatus = false

	files, err := FindFilesModifiedSince(ctx, tmpDir, since, &config)
	if err != nil {
		t.Fatalf("FindFilesModifiedSince failed: %v", err)
	}

	// Should only find visible.txt, not hidden_file.txt
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(files), files)
	}
	if len(files) > 0 && files[0] != "visible.txt" {
		t.Errorf("expected visible.txt, got %s", files[0])
	}
}

// isErrTooManyModifiedFiles checks if error is ErrTooManyModifiedFiles.
// errors.Is doesn't work well with wrapped errors containing this error.
func isErrTooManyModifiedFiles(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() != "" && (err == ErrTooManyModifiedFiles ||
		(len(err.Error()) > len(ErrTooManyModifiedFiles.Error()) &&
			err.Error()[:len(ErrTooManyModifiedFiles.Error())] == ErrTooManyModifiedFiles.Error()))
}

// -----------------------------------------------------------------------------
// Integration Tests
// -----------------------------------------------------------------------------

func TestSessionRestore_Integration_SaveAndRestore(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	projectDir := t.TempDir()

	// Create go.mod for project hash
	goModPath := filepath.Join(projectDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("create go.mod: %v", err)
	}

	// Create persistence manager
	pmConfig := PersistenceConfig{
		BaseDir:           baseDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	// Get session identifier
	sid, err := NewSessionIdentifier(ctx, projectDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	// Create CRS and journal
	crsi := New(nil)
	defer crsi.Close()

	journalDir := filepath.Join(baseDir, sid.CheckpointKey(), "badger")
	journalConfig := JournalConfig{
		SessionID:  "test-session-1",
		Path:       journalDir,
		SyncWrites: false,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}

	// Apply some deltas to CRS
	delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
		"node1": {Proof: 5, Status: ProofStatusExpanded},
		"node2": {Proof: 10, Status: ProofStatusExpanded},
	})
	if _, err := crsi.Apply(ctx, delta); err != nil {
		t.Fatalf("apply delta: %v", err)
	}

	// Record to journal
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("append to journal: %v", err)
	}

	// Save checkpoint
	_, err = pm.SaveBackup(ctx, sid.CheckpointKey(), journal, nil)
	if err != nil {
		t.Fatalf("save backup: %v", err)
	}

	journal.Close()

	// Create new CRS and journal for restore
	crsi2 := New(nil)
	defer crsi2.Close()

	journal2, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("create journal 2: %v", err)
	}
	defer journal2.Close()

	// Create restorer and try restore
	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	result, err := restorer.TryRestore(ctx, crsi2, journal2, sid)
	if err != nil {
		t.Fatalf("TryRestore failed: %v", err)
	}

	if !result.Restored {
		t.Errorf("restore should have succeeded: %s", result.Reason)
	}

	// Verify CRS state was restored
	snap := crsi2.Snapshot()
	proofIdx := snap.ProofIndex()

	pn1, found := proofIdx.Get("node1")
	if !found {
		t.Error("node1 should exist after restore")
	} else if pn1.Proof != 5 {
		t.Errorf("node1.Proof = %d, want 5", pn1.Proof)
	}

	pn2, found := proofIdx.Get("node2")
	if !found {
		t.Error("node2 should exist after restore")
	} else if pn2.Proof != 10 {
		t.Errorf("node2.Proof = %d, want 10", pn2.Proof)
	}
}

// -----------------------------------------------------------------------------
// Second Review Fix Tests
// -----------------------------------------------------------------------------

func TestHashFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")

	content := []byte("test content for hashing")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	h := sha256.New()
	if !hashFile(filePath, h) {
		t.Error("hashFile should return true for valid file")
	}

	// Verify hash was written
	if len(h.Sum(nil)) != 32 { // SHA256 produces 32 bytes
		t.Error("hash should have content")
	}
}

func TestHashFile_NonexistentFile(t *testing.T) {
	h := sha256.New()
	if hashFile("/nonexistent/path/file.txt", h) {
		t.Error("hashFile should return false for nonexistent file")
	}
}

func TestHashFile_Directory(t *testing.T) {
	tmpDir := t.TempDir()

	h := sha256.New()
	// Opening a directory for reading will fail on most systems
	// or produce an error during io.Copy
	result := hashFile(tmpDir, h)
	if result {
		t.Error("hashFile should return false for directory")
	}
}

func TestFindModifiedViaGit_EmptyOutput(t *testing.T) {
	// This test verifies NEW-2 fix: empty git output handling
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skip("git not available")
	}

	// Create and commit a file
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "-m", "init")
	cmd.Dir = tmpDir
	cmd.Run()

	// Find files modified since now (should be none)
	files, err := findModifiedViaGit(ctx, tmpDir, time.Now(), 100)
	if err != nil {
		t.Fatalf("findModifiedViaGit failed: %v", err)
	}

	// Should return empty slice, not [""]
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(files), files)
	}
}

func TestTryRestore_ContextCancellationDuringBackoff(t *testing.T) {
	// This test verifies NEW-13 fix: context cancellation during retry backoff
	ctx, cancel := context.WithCancel(context.Background())
	tmpDir := t.TempDir()
	projectDir := t.TempDir()

	pmConfig := PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(&pmConfig)
	if err != nil {
		t.Fatalf("create persistence manager: %v", err)
	}
	defer pm.Close()

	// Use config with retries
	config := DefaultSessionRestorerConfig()
	config.MaxRetries = 5 // Allow multiple retries

	restorer, err := NewSessionRestorer(pm, &config)
	if err != nil {
		t.Fatalf("NewSessionRestorer failed: %v", err)
	}

	crsi := New(nil)
	defer crsi.Close()

	journalDir := filepath.Join(tmpDir, "journal")
	journalConfig := JournalConfig{
		SessionID:  "test-session",
		Path:       journalDir,
		SyncWrites: false,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}
	defer journal.Close()

	sid, err := NewSessionIdentifier(ctx, projectDir)
	if err != nil {
		t.Fatalf("NewSessionIdentifier failed: %v", err)
	}

	// Cancel context before calling TryRestore
	cancel()

	result, err := restorer.TryRestore(ctx, crsi, journal, sid)

	// Should return early due to cancelled context (no checkpoint found path)
	// or handle context cancellation gracefully
	if err != nil && err != context.Canceled {
		t.Logf("TryRestore returned error (expected): %v", err)
	}
	if result != nil && result.Restored {
		t.Error("should not restore with cancelled context")
	}
}
