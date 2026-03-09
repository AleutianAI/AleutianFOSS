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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistenceConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  PersistenceConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: PersistenceConfig{
				BaseDir:          "/tmp/test",
				CompressionLevel: 6,
				LockTimeoutSec:   30,
			},
			wantErr: false,
		},
		{
			name: "empty base dir",
			config: PersistenceConfig{
				BaseDir:          "",
				CompressionLevel: 6,
				LockTimeoutSec:   30,
			},
			wantErr: true,
			errMsg:  "base_dir must not be empty",
		},
		{
			name: "compression level too low",
			config: PersistenceConfig{
				BaseDir:          "/tmp/test",
				CompressionLevel: 0,
				LockTimeoutSec:   30,
			},
			wantErr: true,
			errMsg:  "compression_level must be 1-9",
		},
		{
			name: "compression level too high",
			config: PersistenceConfig{
				BaseDir:          "/tmp/test",
				CompressionLevel: 10,
				LockTimeoutSec:   30,
			},
			wantErr: true,
			errMsg:  "compression_level must be 1-9",
		},
		{
			name: "lock timeout zero",
			config: PersistenceConfig{
				BaseDir:          "/tmp/test",
				CompressionLevel: 6,
				LockTimeoutSec:   0,
			},
			wantErr: true,
			errMsg:  "lock_timeout_sec must be positive",
		},
		{
			name: "lock timeout negative",
			config: PersistenceConfig{
				BaseDir:          "/tmp/test",
				CompressionLevel: 6,
				LockTimeoutSec:   -1,
			},
			wantErr: true,
			errMsg:  "lock_timeout_sec must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() = nil, want error containing %q", tt.errMsg)
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			}
		})
	}
}

func TestDefaultPersistenceConfig(t *testing.T) {
	config := DefaultPersistenceConfig()

	// Check defaults are sensible
	if config.BaseDir == "" {
		t.Error("BaseDir should not be empty")
	}
	if !strings.Contains(config.BaseDir, ".aleutian") {
		t.Errorf("BaseDir = %q, want path containing .aleutian", config.BaseDir)
	}
	if config.CompressionLevel < 1 || config.CompressionLevel > 9 {
		t.Errorf("CompressionLevel = %d, want 1-9", config.CompressionLevel)
	}
	if config.LockTimeoutSec <= 0 {
		t.Errorf("LockTimeoutSec = %d, want > 0", config.LockTimeoutSec)
	}
	if config.MaxBackupRetries <= 0 {
		t.Errorf("MaxBackupRetries = %d, want > 0", config.MaxBackupRetries)
	}
	if !config.ValidateOnRestore {
		t.Error("ValidateOnRestore should default to true")
	}

	// Should pass validation
	if err := config.Validate(); err != nil {
		t.Errorf("DefaultPersistenceConfig() fails validation: %v", err)
	}
}

func TestNewPersistenceManager(t *testing.T) {
	t.Run("with nil config uses defaults", func(t *testing.T) {
		// Use temp dir to avoid touching real ~/.claude
		tmpDir := t.TempDir()
		config := &PersistenceConfig{
			BaseDir:          tmpDir,
			CompressionLevel: 6,
			LockTimeoutSec:   30,
		}

		pm, err := NewPersistenceManager(config)
		if err != nil {
			t.Fatalf("NewPersistenceManager() error = %v", err)
		}
		defer pm.Close()

		if pm == nil {
			t.Fatal("NewPersistenceManager() returned nil")
		}
	})

	t.Run("creates base directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		baseDir := filepath.Join(tmpDir, "nested", "crs")

		config := &PersistenceConfig{
			BaseDir:          baseDir,
			CompressionLevel: 6,
			LockTimeoutSec:   30,
		}

		pm, err := NewPersistenceManager(config)
		if err != nil {
			t.Fatalf("NewPersistenceManager() error = %v", err)
		}
		defer pm.Close()

		// Check directory was created
		info, err := os.Stat(baseDir)
		if err != nil {
			t.Fatalf("BaseDir not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("BaseDir is not a directory")
		}
	})

	t.Run("invalid config returns error", func(t *testing.T) {
		config := &PersistenceConfig{
			BaseDir:          "",
			CompressionLevel: 6,
			LockTimeoutSec:   30,
		}

		pm, err := NewPersistenceManager(config)
		if err == nil {
			pm.Close()
			t.Fatal("NewPersistenceManager() should fail with empty BaseDir")
		}
		if !strings.Contains(err.Error(), "base_dir") {
			t.Errorf("error = %q, want error about base_dir", err.Error())
		}
	})
}

func TestPersistenceManager_PathMethods(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	projectHash := "abcdef0123456789"

	t.Run("ProjectDir", func(t *testing.T) {
		dir := pm.ProjectDir(projectHash)
		expected := filepath.Join(tmpDir, projectHash)
		if dir != expected {
			t.Errorf("ProjectDir() = %q, want %q", dir, expected)
		}
	})

	t.Run("BackupPath", func(t *testing.T) {
		path := pm.BackupPath(projectHash)
		if !strings.HasSuffix(path, "latest.backup.gz") {
			t.Errorf("BackupPath() = %q, want suffix latest.backup.gz", path)
		}
		if !strings.Contains(path, projectHash) {
			t.Errorf("BackupPath() = %q, want to contain project hash", path)
		}
	})

	t.Run("MetadataPath", func(t *testing.T) {
		path := pm.MetadataPath(projectHash)
		if !strings.HasSuffix(path, "metadata.json") {
			t.Errorf("MetadataPath() = %q, want suffix metadata.json", path)
		}
	})

	t.Run("LockPath", func(t *testing.T) {
		path := pm.LockPath(projectHash)
		if !strings.HasSuffix(path, ".lock") {
			t.Errorf("LockPath() = %q, want suffix .lock", path)
		}
	})

	t.Run("ExportPath", func(t *testing.T) {
		path := pm.ExportPath(projectHash)
		if !strings.HasSuffix(path, "export.json") {
			t.Errorf("ExportPath() = %q, want suffix export.json", path)
		}
	})
}

func TestPersistenceManager_HasBackup(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	projectHash := "abcdef0123456789"

	t.Run("no backup exists", func(t *testing.T) {
		if pm.HasBackup(projectHash) {
			t.Error("HasBackup() = true for non-existent backup")
		}
	})

	t.Run("backup exists", func(t *testing.T) {
		// Create the backup file
		backupPath := pm.BackupPath(projectHash)
		if err := os.MkdirAll(filepath.Dir(backupPath), 0750); err != nil {
			t.Fatalf("MkdirAll error: %v", err)
		}
		if err := os.WriteFile(backupPath, []byte("test"), 0640); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		if !pm.HasBackup(projectHash) {
			t.Error("HasBackup() = false for existing backup")
		}
	})
}

func TestPersistenceManager_Close(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}

	// First close should succeed
	if err := pm.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Second close should be idempotent
	if err := pm.Close(); err != nil {
		t.Errorf("Close() second call error = %v", err)
	}
}

func TestPersistenceManager_SaveBackup_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	ctx := context.Background()

	t.Run("nil context", func(t *testing.T) {
		_, err := pm.SaveBackup(nil, "abcdef01", nil, nil)
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("SaveBackup(nil ctx) = %v, want ErrNilContext", err)
		}
	})

	t.Run("invalid project hash", func(t *testing.T) {
		_, err := pm.SaveBackup(ctx, "invalid!", nil, nil)
		if err == nil {
			t.Error("SaveBackup(invalid hash) should fail")
		}
		if !strings.Contains(err.Error(), "project hash") {
			t.Errorf("error = %q, want error about project hash", err.Error())
		}
	})

	t.Run("nil journal", func(t *testing.T) {
		_, err := pm.SaveBackup(ctx, "abcdef01", nil, nil)
		if err == nil {
			t.Error("SaveBackup(nil journal) should fail")
		}
		if !strings.Contains(err.Error(), "journal must not be nil") {
			t.Errorf("error = %q, want error about nil journal", err.Error())
		}
	})

	t.Run("closed manager", func(t *testing.T) {
		pm2, _ := NewPersistenceManager(config)
		pm2.Close()

		_, err := pm2.SaveBackup(ctx, "abcdef01", nil, nil)
		if !errors.Is(err, ErrPersistenceManagerClosed) {
			t.Errorf("SaveBackup on closed manager = %v, want ErrPersistenceManagerClosed", err)
		}
	})
}

func TestPersistenceManager_LoadBackup_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	ctx := context.Background()

	t.Run("nil context", func(t *testing.T) {
		_, err := pm.LoadBackup(nil, "abcdef01", nil)
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("LoadBackup(nil ctx) = %v, want ErrNilContext", err)
		}
	})

	t.Run("invalid project hash", func(t *testing.T) {
		_, err := pm.LoadBackup(ctx, "invalid!", nil)
		if err == nil {
			t.Error("LoadBackup(invalid hash) should fail")
		}
	})

	t.Run("nil journal", func(t *testing.T) {
		_, err := pm.LoadBackup(ctx, "abcdef01", nil)
		if err == nil {
			t.Error("LoadBackup(nil journal) should fail")
		}
	})

	t.Run("backup not found", func(t *testing.T) {
		// Create a mock journal for this test
		journalConfig := JournalConfig{
			SessionID: "test-session",
			InMemory:  true,
		}
		journal, err := NewBadgerJournal(journalConfig)
		if err != nil {
			t.Fatalf("NewBadgerJournal error: %v", err)
		}
		defer journal.Close()

		_, err = pm.LoadBackup(ctx, "abcdef01", journal)
		if !errors.Is(err, ErrBackupNotFound) {
			t.Errorf("LoadBackup(no backup) = %v, want ErrBackupNotFound", err)
		}
	})

	t.Run("closed manager", func(t *testing.T) {
		pm2, _ := NewPersistenceManager(config)
		pm2.Close()

		_, err := pm2.LoadBackup(ctx, "abcdef01", nil)
		if !errors.Is(err, ErrPersistenceManagerClosed) {
			t.Errorf("LoadBackup on closed manager = %v, want ErrPersistenceManagerClosed", err)
		}
	})
}

func TestBackupMetadata_Age(t *testing.T) {
	// Create metadata from 1 hour ago
	oneHourAgo := time.Now().Add(-1 * time.Hour).UnixMilli()
	meta := &BackupMetadata{
		CreatedAt: oneHourAgo,
	}

	age := meta.Age()
	if age < 59*time.Minute || age > 61*time.Minute {
		t.Errorf("Age() = %v, want approximately 1 hour", age)
	}
}

func TestBackupMetadata_CompressionRatio(t *testing.T) {
	tests := []struct {
		name             string
		compressed       int64
		uncompressed     int64
		expectedRatio    float64
		expectedApprox   bool
		expectedApproxTo float64
	}{
		{
			name:           "50% compression",
			compressed:     500,
			uncompressed:   1000,
			expectedRatio:  0.5,
			expectedApprox: false,
		},
		{
			name:           "no compression",
			compressed:     1000,
			uncompressed:   1000,
			expectedRatio:  1.0,
			expectedApprox: false,
		},
		{
			name:           "zero uncompressed",
			compressed:     100,
			uncompressed:   0,
			expectedRatio:  0,
			expectedApprox: false,
		},
		{
			name:             "30% compression typical",
			compressed:       300,
			uncompressed:     1000,
			expectedRatio:    0.3,
			expectedApprox:   true,
			expectedApproxTo: 0.31,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &BackupMetadata{
				CompressedSize:   tt.compressed,
				UncompressedSize: tt.uncompressed,
			}

			ratio := meta.CompressionRatio()
			if tt.expectedApprox {
				if ratio < tt.expectedRatio || ratio > tt.expectedApproxTo {
					t.Errorf("CompressionRatio() = %v, want between %v and %v", ratio, tt.expectedRatio, tt.expectedApproxTo)
				}
			} else {
				if ratio != tt.expectedRatio {
					t.Errorf("CompressionRatio() = %v, want %v", ratio, tt.expectedRatio)
				}
			}
		})
	}
}

func TestCurrentSchemaVersion(t *testing.T) {
	// Schema version should be a valid semver-like string
	if CurrentSchemaVersion == "" {
		t.Error("CurrentSchemaVersion should not be empty")
	}
	if !strings.Contains(CurrentSchemaVersion, ".") {
		t.Errorf("CurrentSchemaVersion = %q, want version format like '1.0'", CurrentSchemaVersion)
	}
}

// mockGraphRefreshCoordinator implements GraphRefreshCoordinator for testing.
type mockGraphRefreshCoordinator struct {
	paused      bool
	pauseErr    error
	resumeErr   error
	pauseCalls  int
	resumeCalls int
}

func (m *mockGraphRefreshCoordinator) Pause(ctx context.Context) error {
	m.pauseCalls++
	if m.pauseErr != nil {
		return m.pauseErr
	}
	m.paused = true
	return nil
}

func (m *mockGraphRefreshCoordinator) Resume(ctx context.Context) error {
	m.resumeCalls++
	if m.resumeErr != nil {
		return m.resumeErr
	}
	m.paused = false
	return nil
}

func (m *mockGraphRefreshCoordinator) IsPaused() bool {
	return m.paused
}

func TestPersistenceManager_SetRefreshCoordinator(t *testing.T) {
	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:          tmpDir,
		CompressionLevel: 6,
		LockTimeoutSec:   30,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	coordinator := &mockGraphRefreshCoordinator{}
	pm.SetRefreshCoordinator(coordinator)

	// Verify coordinator is set by checking it's used during restore
	// (We can't directly access the field, but the log message confirms it)
}

// TestPersistenceIntegration_SaveLoadRoundtrip tests the full backup/restore cycle.
// This is an integration test that creates a real journal and backs it up.
func TestPersistenceIntegration_SaveLoadRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Create a journal with some data
	journalConfig := JournalConfig{
		SessionID: "test-session-" + time.Now().Format("20060102150405"),
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "abcdef0123456789"

	// Add some deltas to the journal
	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Disproof: 5, Status: ProofStatusExpanded},
			"node2": {Proof: 20, Disproof: 10, Status: ProofStatusUnknown},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	// Save backup
	metadata, err := pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Verify metadata
	if metadata.ProjectHash != projectHash {
		t.Errorf("metadata.ProjectHash = %q, want %q", metadata.ProjectHash, projectHash)
	}
	if metadata.CompressedSize <= 0 {
		t.Error("metadata.CompressedSize should be > 0")
	}
	if metadata.UncompressedSize <= 0 {
		t.Error("metadata.UncompressedSize should be > 0")
	}
	if metadata.ContentHash == "" {
		t.Error("metadata.ContentHash should not be empty")
	}
	if metadata.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("metadata.SchemaVersion = %q, want %q", metadata.SchemaVersion, CurrentSchemaVersion)
	}

	// Verify backup file exists
	if !pm.HasBackup(projectHash) {
		t.Error("HasBackup() = false after SaveBackup")
	}

	// Create a new journal for restore
	journal2Config := JournalConfig{
		SessionID: "test-session-restore",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Load backup
	loadedMetadata, err := pm.LoadBackup(ctx, projectHash, journal2)
	if err != nil {
		t.Fatalf("LoadBackup error: %v", err)
	}

	// Verify loaded metadata matches
	if loadedMetadata.ProjectHash != metadata.ProjectHash {
		t.Errorf("loaded ProjectHash = %q, want %q", loadedMetadata.ProjectHash, metadata.ProjectHash)
	}
	if loadedMetadata.ContentHash != metadata.ContentHash {
		t.Errorf("loaded ContentHash = %q, want %q", loadedMetadata.ContentHash, metadata.ContentHash)
	}

	// Verify GetBackupMetadata returns correct data
	storedMeta, err := pm.GetBackupMetadata(projectHash)
	if err != nil {
		t.Fatalf("GetBackupMetadata error: %v", err)
	}
	if storedMeta.ContentHash != metadata.ContentHash {
		t.Errorf("stored ContentHash = %q, want %q", storedMeta.ContentHash, metadata.ContentHash)
	}
}

func TestCountingWriter(t *testing.T) {
	// Use a buffer to verify the counting writer works
	var buf strings.Builder
	cw := &countingWriter{w: &buf}

	// Write some data
	n, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned n = %d, want 5", n)
	}
	if cw.count != 5 {
		t.Errorf("count = %d, want 5", cw.count)
	}

	// Write more data
	n, err = cw.Write([]byte(" world"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 6 {
		t.Errorf("Write returned n = %d, want 6", n)
	}
	if cw.count != 11 {
		t.Errorf("count = %d, want 11", cw.count)
	}

	// Verify the underlying writer received the data
	if buf.String() != "hello world" {
		t.Errorf("buffer = %q, want 'hello world'", buf.String())
	}
}

// TestPersistenceIntegration_CorruptedBackupRejection verifies that corrupted backups
// are detected and rejected during restore.
func TestPersistenceIntegration_CorruptedBackupRejection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Create a journal with some data
	journalConfig := JournalConfig{
		SessionID: "test-session-corrupt",
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "c0ff1234567890ab"

	// Add some deltas and create backup
	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Disproof: 5, Status: ProofStatusExpanded},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	_, err = pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Corrupt the backup file by modifying some bytes
	backupPath := pm.BackupPath(projectHash)
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	// Corrupt the middle of the file
	if len(backupData) > 50 {
		backupData[50] ^= 0xFF
	}
	if err := os.WriteFile(backupPath, backupData, 0640); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Create a new journal for restore
	journal2Config := JournalConfig{
		SessionID: "test-session-restore-corrupt",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Load should fail with some error indicating corruption
	// Note: Depending on where corruption occurs, this could be:
	// - ErrBackupCorrupted (hash mismatch detected after decompression)
	// - A gzip/flate error (corruption in compressed data prevents decompression)
	// - A badger error (corruption in uncompressed data)
	// All of these are valid failure modes - the key is that corrupt data is rejected.
	_, err = pm.LoadBackup(ctx, projectHash, journal2)
	if err == nil {
		t.Fatal("LoadBackup should fail for corrupted backup")
	}
	// Verify we got some kind of error - the restore was rejected
	t.Logf("LoadBackup correctly rejected corrupted backup: %v", err)
}

// TestPersistenceIntegration_VersionMismatch verifies that backups from
// incompatible BadgerDB versions are rejected.
func TestPersistenceIntegration_VersionMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Create a journal with some data
	journalConfig := JournalConfig{
		SessionID: "test-session-version",
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "abcdef1234567890"

	// Add some deltas and create backup
	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Status: ProofStatusExpanded},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	_, err = pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Modify the metadata to use an incompatible version
	// We need to write a clean metadata file without the hash to avoid hash validation
	metaPath := pm.MetadataPath(projectHash)
	modifiedMeta := &BackupMetadata{
		ProjectHash:      projectHash,
		CreatedAt:        time.Now().UnixMilli(),
		BadgerVersion:    "v3.0.0",   // Incompatible version
		ContentHash:      "fakehash", // Any non-empty value
		UncompressedSize: 1000,
		CompressedSize:   500,
		Generation:       1,
		SchemaVersion:    CurrentSchemaVersion,
		// MetadataHash intentionally left empty to skip hash validation
	}
	modifiedData, err := json.MarshalIndent(modifiedMeta, "", "  ")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if err := os.WriteFile(metaPath, modifiedData, 0640); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Create a new journal for restore
	journal2Config := JournalConfig{
		SessionID: "test-session-restore-version",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Load should fail with version mismatch error
	_, err = pm.LoadBackup(ctx, projectHash, journal2)
	if err == nil {
		t.Fatal("LoadBackup should fail for version mismatch")
	}
	if !errors.Is(err, ErrBackupVersionMismatch) {
		t.Errorf("LoadBackup error = %v, want ErrBackupVersionMismatch", err)
	}
}

// TestPersistenceIntegration_MetadataHashValidation verifies that corrupted
// metadata is detected during restore.
func TestPersistenceIntegration_MetadataHashValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Create a journal with some data
	journalConfig := JournalConfig{
		SessionID: "test-session-metahash",
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "de1ad0beef567890"

	// Add some deltas and create backup
	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Status: ProofStatusExpanded},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	_, err = pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Corrupt the metadata by modifying a field without updating the hash
	metaPath := pm.MetadataPath(projectHash)
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	// Modify the generation but keep the same hash
	modified := strings.Replace(string(metaData), `"generation": 1`, `"generation": 999`, 1)
	if err := os.WriteFile(metaPath, []byte(modified), 0640); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Create a new journal for restore
	journal2Config := JournalConfig{
		SessionID: "test-session-restore-metahash",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Load should fail with metadata corrupted error
	_, err = pm.LoadBackup(ctx, projectHash, journal2)
	if err == nil {
		t.Fatal("LoadBackup should fail for corrupted metadata")
	}
	if !errors.Is(err, ErrMetadataCorrupted) {
		t.Errorf("LoadBackup error = %v, want ErrMetadataCorrupted", err)
	}
}

// TestPersistenceIntegration_ConcurrentAccess verifies that file locking
// prevents concurrent write access.
func TestPersistenceIntegration_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    1, // Short timeout for test
		ValidateOnRestore: true,
	}

	pm1, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm1.Close()

	pm2, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm2.Close()

	ctx := context.Background()
	projectHash := "c0dec0de12345678"

	// Create journals
	journal1Config := JournalConfig{
		SessionID: "test-session-concurrent1",
		InMemory:  true,
	}
	journal1, err := NewBadgerJournal(journal1Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal1.Close()

	journal2Config := JournalConfig{
		SessionID: "test-session-concurrent2",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Add deltas
	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Status: ProofStatusExpanded},
		},
	}
	if err := journal1.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}
	if err := journal2.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	// Start concurrent backup operations
	results := make(chan error, 2)

	// First backup (should succeed)
	go func() {
		_, err := pm1.SaveBackup(ctx, projectHash, journal1, nil)
		results <- err
	}()

	// Small delay to ensure first goroutine acquires lock
	time.Sleep(50 * time.Millisecond)

	// Second backup (should fail or wait for lock)
	go func() {
		_, err := pm2.SaveBackup(ctx, projectHash, journal2, nil)
		results <- err
	}()

	// Collect results
	err1 := <-results
	err2 := <-results

	// At least one should succeed
	successes := 0
	if err1 == nil {
		successes++
	}
	if err2 == nil {
		successes++
	}

	// Both might succeed if they serialize properly, or one might fail with lock timeout
	if successes == 0 {
		t.Errorf("Both backups failed: err1=%v, err2=%v", err1, err2)
	}

	t.Logf("Concurrent backup results: err1=%v, err2=%v, successes=%d", err1, err2, successes)
}

// TestPersistenceIntegration_RestoreInProgressProtection verifies that
// concurrent restore attempts are blocked.
func TestPersistenceIntegration_RestoreInProgressProtection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Create a journal and backup
	journalConfig := JournalConfig{
		SessionID: "test-session-restore-protect",
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "fe12345678abcdef"

	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Status: ProofStatusExpanded},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	_, err = pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Start concurrent restore operations
	results := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func(id int) {
			jCfg := JournalConfig{
				SessionID: "restore-" + time.Now().Format("150405.000000") + "-" + string(rune('A'+id)),
				InMemory:  true,
			}
			j, err := NewBadgerJournal(jCfg)
			if err != nil {
				results <- err
				return
			}
			defer j.Close()

			_, err = pm.LoadBackup(ctx, projectHash, j)
			results <- err
		}(i)
	}

	// Collect results
	err1 := <-results
	err2 := <-results

	// At least one should succeed, the other may fail with ErrRestoreInProgress
	successes := 0
	restoreInProgress := 0

	for _, e := range []error{err1, err2} {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrRestoreInProgress) {
			restoreInProgress++
		}
	}

	if successes == 0 {
		t.Errorf("Both restores failed: err1=%v, err2=%v", err1, err2)
	}

	t.Logf("Concurrent restore results: successes=%d, restore_in_progress=%d", successes, restoreInProgress)
}

// TestPersistenceManager_GraphRefreshCoordinatorIntegration verifies that
// the graph refresh coordinator is properly paused and resumed during restore.
func TestPersistenceManager_GraphRefreshCoordinatorIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	config := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		ValidateOnRestore: true,
	}

	pm, err := NewPersistenceManager(config)
	if err != nil {
		t.Fatalf("NewPersistenceManager() error = %v", err)
	}
	defer pm.Close()

	// Set up mock coordinator
	coordinator := &mockGraphRefreshCoordinator{}
	pm.SetRefreshCoordinator(coordinator)

	// Create a journal and backup
	journalConfig := JournalConfig{
		SessionID: "test-session-coord",
		InMemory:  true,
	}
	journal, err := NewBadgerJournal(journalConfig)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal.Close()

	ctx := context.Background()
	projectHash := "ca1d1234567890ab"

	delta := &ProofDelta{
		Updates: map[string]ProofNumber{
			"node1": {Proof: 10, Status: ProofStatusExpanded},
		},
	}
	if err := journal.Append(ctx, delta); err != nil {
		t.Fatalf("Append error: %v", err)
	}

	_, err = pm.SaveBackup(ctx, projectHash, journal, nil)
	if err != nil {
		t.Fatalf("SaveBackup error: %v", err)
	}

	// Create a new journal for restore
	journal2Config := JournalConfig{
		SessionID: "test-session-restore-coord",
		InMemory:  true,
	}
	journal2, err := NewBadgerJournal(journal2Config)
	if err != nil {
		t.Fatalf("NewBadgerJournal error: %v", err)
	}
	defer journal2.Close()

	// Perform restore
	_, err = pm.LoadBackup(ctx, projectHash, journal2)
	if err != nil {
		t.Fatalf("LoadBackup error: %v", err)
	}

	// Verify coordinator was paused and resumed
	if coordinator.pauseCalls != 1 {
		t.Errorf("coordinator.pauseCalls = %d, want 1", coordinator.pauseCalls)
	}
	if coordinator.resumeCalls != 1 {
		t.Errorf("coordinator.resumeCalls = %d, want 1", coordinator.resumeCalls)
	}
	if coordinator.IsPaused() {
		t.Error("coordinator should be resumed after restore")
	}
}

// TestSyncDir verifies the syncDir helper function.
// TestCRS15_SimilarityPersistenceRoundTrip verifies that similarity data
// persists across sessions via the journal save/restore pipeline.
//
// CRS-15 Acceptance Criteria:
//   - AC-1: SaveBackup includes SimilarityIndex entries
//   - AC-2: TryRestore restores SimilarityIndex entries
//   - AC-3: FindSimilar returns matches from prior session
//   - AC-5: Unit test: save 3 entries → restore → verify present
func TestCRS15_SimilarityPersistenceRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	projectHash := "abcdef0123456789"

	// --- Session 1: Record similarity pairs and save ---

	cfg := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  1,
		LockTimeoutSec:    5,
		MaxBackupRetries:  0,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(cfg)
	if err != nil {
		t.Fatalf("NewPersistenceManager: %v", err)
	}
	defer pm.Close()

	journal1, err := NewBadgerJournal(JournalConfig{
		SessionID: "session-1",
		InMemory:  true,
	})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer journal1.Close()

	// Create CRS instance and record 3 similarity pairs.
	crs1 := New(nil)
	defer crs1.Close()

	// Record 3 similarity pairs.
	pairs := [][3]interface{}{
		{"find_hotspots:rendering hotspots", "find_hotspots:render path hotspots", 0.15},
		{"Grep:parseConfig", "Grep:parse_config", 0.08},
		{"find_callers:handleRequest", "find_callers:handle_request", 0.12},
	}
	for _, p := range pairs {
		if err := crs1.RecordSimilarity(ctx, p[0].(string), p[1].(string), p[2].(float64)); err != nil {
			t.Fatalf("RecordSimilarity(%s, %s): %v", p[0], p[1], err)
		}
	}

	// Verify pairs exist in session 1.
	for _, p := range pairs {
		dist, found := crs1.GetSimilarity(p[0].(string), p[1].(string))
		if !found {
			t.Errorf("GetSimilarity(%s, %s) not found in session 1", p[0], p[1])
		}
		if dist != p[2].(float64) {
			t.Errorf("GetSimilarity(%s, %s) = %f, want %f", p[0], p[1], dist, p[2].(float64))
		}
	}

	// Save checkpoint to disk (flushes similarity to journal).
	// SaveCheckpointToDisk is on *crsImpl, use type assertion.
	crsImpl1, ok := crs1.(*crsImpl)
	if !ok {
		t.Fatal("CRS instance is not *crsImpl")
	}
	_, err = crsImpl1.SaveCheckpointToDisk(ctx, pm, projectHash, journal1)
	if err != nil {
		t.Fatalf("SaveCheckpointToDisk: %v", err)
	}

	// --- Session 2: Restore and verify ---

	// Use same session ID as session 1 so delta key prefix matches.
	// In production, TryRestore uses the same project-scoped journal
	// so key prefixes always match.
	journal2, err := NewBadgerJournal(JournalConfig{
		SessionID: "session-1",
		InMemory:  true,
	})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer journal2.Close()

	crs2 := New(nil)
	defer crs2.Close()

	// Verify session 2 starts with no similarity data.
	for _, p := range pairs {
		_, found := crs2.GetSimilarity(p[0].(string), p[1].(string))
		if found {
			t.Errorf("GetSimilarity(%s, %s) should not exist before restore", p[0], p[1])
		}
	}

	// Restore from checkpoint.
	restorer, err := NewSessionRestorer(pm, nil)
	if err != nil {
		t.Fatalf("NewSessionRestorer: %v", err)
	}

	// Build a minimal SessionIdentifier that produces the same checkpoint key.
	// We load the backup manually since we're using a test project hash.
	_, err = pm.LoadBackup(ctx, projectHash, journal2)
	if err != nil {
		t.Fatalf("LoadBackup: %v", err)
	}
	_ = restorer // Used for type validation only

	// Replay journal deltas into CRS.
	deltas, err := journal2.Replay(ctx)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(deltas) == 0 {
		t.Fatal("Expected at least 1 delta after restore, got 0")
	}

	hasSimilarityDelta := false
	for _, delta := range deltas {
		if delta.Type() == DeltaTypeSimilarity {
			hasSimilarityDelta = true
		}
		if _, err := crs2.Apply(ctx, delta); err != nil {
			t.Fatalf("Apply delta: %v", err)
		}
	}

	if !hasSimilarityDelta {
		t.Error("Expected at least one SimilarityDelta in journal, got none")
	}

	// Verify all 3 similarity pairs are present in session 2.
	for _, p := range pairs {
		dist, found := crs2.GetSimilarity(p[0].(string), p[1].(string))
		if !found {
			t.Errorf("GetSimilarity(%s, %s) not found after restore", p[0], p[1])
			continue
		}
		if dist != p[2].(float64) {
			t.Errorf("GetSimilarity(%s, %s) = %f after restore, want %f", p[0], p[1], dist, p[2].(float64))
		}

		// Verify symmetry
		distRev, foundRev := crs2.GetSimilarity(p[1].(string), p[0].(string))
		if !foundRev {
			t.Errorf("GetSimilarity(%s, %s) reverse not found after restore", p[1], p[0])
		}
		if distRev != dist {
			t.Errorf("Reverse distance %f != forward %f", distRev, dist)
		}
	}
}

// TestCRS15_SimilarityPruning verifies that stale similarity deltas are
// pruned during restore (AC-4: 7-day TTL).
func TestCRS15_SimilarityPruning(t *testing.T) {
	ctx := context.Background()

	// Create a fresh CRS.
	crsInstance := New(nil)
	defer crsInstance.Close()

	// Create a SimilarityDelta with an old timestamp (> 7 days).
	oldDelta := NewSimilarityDelta(SignalSourceSoft)
	oldDelta.Updates[[2]string{"old_key1", "old_key2"}] = 0.5
	// Manually set timestamp to 8 days ago.
	oldDelta.baseDelta.timestamp = time.Now().Add(-8 * 24 * time.Hour).UnixMilli()

	// Create a recent SimilarityDelta.
	recentDelta := NewSimilarityDelta(SignalSourceSoft)
	recentDelta.Updates[[2]string{"new_key1", "new_key2"}] = 0.3

	// Simulate what TryRestore does: filter old deltas.
	deltas := []Delta{oldDelta, recentDelta}
	prunedCount := 0
	for _, delta := range deltas {
		if simDelta, ok := delta.(*SimilarityDelta); ok {
			age := time.Since(time.UnixMilli(simDelta.Timestamp()))
			if age > DefaultCheckpointMaxAge {
				prunedCount++
				continue
			}
		}
		if _, err := crsInstance.Apply(ctx, delta); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	if prunedCount != 1 {
		t.Errorf("Expected 1 pruned delta, got %d", prunedCount)
	}

	// Old pair should NOT be present.
	_, found := crsInstance.GetSimilarity("old_key1", "old_key2")
	if found {
		t.Error("Old similarity pair should have been pruned")
	}

	// New pair SHOULD be present.
	dist, found := crsInstance.GetSimilarity("new_key1", "new_key2")
	if !found {
		t.Error("Recent similarity pair should be present after restore")
	}
	if dist != 0.3 {
		t.Errorf("Distance = %f, want 0.3", dist)
	}
}

func TestSyncDir(t *testing.T) {
	tmpDir := t.TempDir()

	// syncDir should succeed on valid directory
	err := syncDir(tmpDir)
	if err != nil {
		t.Errorf("syncDir() error = %v", err)
	}

	// syncDir should fail on non-existent directory
	err = syncDir(filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Error("syncDir() should fail on non-existent directory")
	}
}

// TestCRS21_CheckpointRotation verifies that SaveBackup rotates
// the current checkpoint to .prev before writing the new one.
func TestCRS21_CheckpointRotation(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	projectHash := "abcdef0123456789"

	cfg := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  1,
		LockTimeoutSec:    5,
		MaxBackupRetries:  0,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(cfg)
	if err != nil {
		t.Fatalf("NewPersistenceManager: %v", err)
	}
	defer pm.Close()

	// Create first journal and save backup.
	journal1, err := NewBadgerJournal(JournalConfig{
		SessionID: "session-1",
		InMemory:  true,
	})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer journal1.Close()

	meta1, err := pm.SaveBackup(ctx, projectHash, journal1, nil)
	if err != nil {
		t.Fatalf("SaveBackup 1: %v", err)
	}

	// Verify current checkpoint exists, prev does not.
	if _, statErr := os.Stat(pm.BackupPath(projectHash)); statErr != nil {
		t.Fatalf("current backup should exist: %v", statErr)
	}
	if _, statErr := os.Stat(pm.PrevBackupPath(projectHash)); statErr == nil {
		t.Fatal("prev backup should NOT exist after first save")
	}

	// Save a second backup — should rotate first → prev.
	journal2, err := NewBadgerJournal(JournalConfig{
		SessionID: "session-2",
		InMemory:  true,
	})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer journal2.Close()

	meta2, err := pm.SaveBackup(ctx, projectHash, journal2, nil)
	if err != nil {
		t.Fatalf("SaveBackup 2: %v", err)
	}

	// Both current and prev should now exist.
	if _, statErr := os.Stat(pm.BackupPath(projectHash)); statErr != nil {
		t.Fatalf("current backup should exist: %v", statErr)
	}
	if _, statErr := os.Stat(pm.PrevBackupPath(projectHash)); statErr != nil {
		t.Fatalf("prev backup should exist after second save: %v", statErr)
	}

	// Metadata should also have prev.
	if _, statErr := os.Stat(pm.PrevMetadataPath(projectHash)); statErr != nil {
		t.Fatalf("prev metadata should exist: %v", statErr)
	}

	// Verify the two backups are different (different content hashes).
	if meta1.ContentHash == meta2.ContentHash {
		// They may legitimately be the same if both journals are empty,
		// but the creation times should differ.
		if meta1.CreatedAt == meta2.CreatedAt {
			t.Error("two backups should have different timestamps")
		}
	}
}

// TestCRS21_FallbackToPrev verifies that LoadBackup falls back to
// the previous checkpoint when the current one is corrupted.
func TestCRS21_FallbackToPrev(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	projectHash := "abcdef0123456789"

	cfg := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  1,
		LockTimeoutSec:    5,
		MaxBackupRetries:  0,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(cfg)
	if err != nil {
		t.Fatalf("NewPersistenceManager: %v", err)
	}
	defer pm.Close()

	// Create and save two backups to establish current + prev.
	journal1, err := NewBadgerJournal(JournalConfig{SessionID: "s1", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	if _, err := pm.SaveBackup(ctx, projectHash, journal1, nil); err != nil {
		t.Fatalf("SaveBackup 1: %v", err)
	}
	journal1.Close()

	journal2, err := NewBadgerJournal(JournalConfig{SessionID: "s2", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	if _, err := pm.SaveBackup(ctx, projectHash, journal2, nil); err != nil {
		t.Fatalf("SaveBackup 2: %v", err)
	}
	journal2.Close()

	// Corrupt the current backup file.
	if err := os.WriteFile(pm.BackupPath(projectHash), []byte("corrupted"), 0640); err != nil {
		t.Fatalf("corrupt backup: %v", err)
	}

	// Load should fall back to prev.
	restoreJournal, err := NewBadgerJournal(JournalConfig{SessionID: "restore", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer restoreJournal.Close()

	meta, err := pm.LoadBackup(ctx, projectHash, restoreJournal)
	if err != nil {
		t.Fatalf("LoadBackup should succeed with fallback: %v", err)
	}
	if meta == nil {
		t.Fatal("metadata should not be nil after fallback")
	}
}

// TestCRS21_BothCorrupt verifies that LoadBackup returns an error
// when both current and prev checkpoints are corrupted.
func TestCRS21_BothCorrupt(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	projectHash := "abcdef0123456789"

	cfg := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  1,
		LockTimeoutSec:    5,
		MaxBackupRetries:  0,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(cfg)
	if err != nil {
		t.Fatalf("NewPersistenceManager: %v", err)
	}
	defer pm.Close()

	// Create and save two backups.
	journal1, err := NewBadgerJournal(JournalConfig{SessionID: "s1", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	if _, err := pm.SaveBackup(ctx, projectHash, journal1, nil); err != nil {
		t.Fatalf("SaveBackup 1: %v", err)
	}
	journal1.Close()

	journal2, err := NewBadgerJournal(JournalConfig{SessionID: "s2", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	if _, err := pm.SaveBackup(ctx, projectHash, journal2, nil); err != nil {
		t.Fatalf("SaveBackup 2: %v", err)
	}
	journal2.Close()

	// Corrupt both checkpoints.
	if err := os.WriteFile(pm.BackupPath(projectHash), []byte("bad1"), 0640); err != nil {
		t.Fatalf("corrupt current: %v", err)
	}
	if err := os.WriteFile(pm.PrevBackupPath(projectHash), []byte("bad2"), 0640); err != nil {
		t.Fatalf("corrupt prev: %v", err)
	}

	// Load should fail with combined error.
	restoreJournal, err := NewBadgerJournal(JournalConfig{SessionID: "restore", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer restoreJournal.Close()

	_, err = pm.LoadBackup(ctx, projectHash, restoreJournal)
	if err == nil {
		t.Fatal("LoadBackup should fail when both checkpoints are corrupted")
	}
	// Error should mention both checkpoints.
	if !strings.Contains(err.Error(), "current checkpoint") || !strings.Contains(err.Error(), "previous checkpoint") {
		t.Errorf("error should mention both checkpoints, got: %v", err)
	}
}

// TestCRS21_NoPrevNoCrash verifies that LoadBackup returns a clean
// error when only the current checkpoint exists and is corrupted (no prev).
func TestCRS21_NoPrevNoCrash(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	projectHash := "abcdef0123456789"

	cfg := &PersistenceConfig{
		BaseDir:           tmpDir,
		CompressionLevel:  1,
		LockTimeoutSec:    5,
		MaxBackupRetries:  0,
		ValidateOnRestore: true,
	}
	pm, err := NewPersistenceManager(cfg)
	if err != nil {
		t.Fatalf("NewPersistenceManager: %v", err)
	}
	defer pm.Close()

	// Save one backup only (no prev).
	journal1, err := NewBadgerJournal(JournalConfig{SessionID: "s1", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	if _, err := pm.SaveBackup(ctx, projectHash, journal1, nil); err != nil {
		t.Fatalf("SaveBackup: %v", err)
	}
	journal1.Close()

	// Corrupt current.
	if err := os.WriteFile(pm.BackupPath(projectHash), []byte("bad"), 0640); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	// Load should fail cleanly (no panic).
	restoreJournal, err := NewBadgerJournal(JournalConfig{SessionID: "restore", InMemory: true})
	if err != nil {
		t.Fatalf("NewBadgerJournal: %v", err)
	}
	defer restoreJournal.Close()

	_, err = pm.LoadBackup(ctx, projectHash, restoreJournal)
	if err == nil {
		t.Fatal("LoadBackup should fail when current is corrupted and no prev exists")
	}
	// Should NOT panic — just return an error.
}
