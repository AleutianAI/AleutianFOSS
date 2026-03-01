// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package initializer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StorageWriter defines the interface for writing index data.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type StorageWriter interface {
	// WriteIndex atomically writes the index to disk.
	WriteIndex(ctx context.Context, symbols []Symbol, edges []Edge, manifest *ManifestFile, config *ProjectConfig) (*WriteResult, error)
}

// StorageReader defines the interface for reading index data.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type StorageReader interface {
	// Exists checks if the .aleutian directory exists with valid files.
	Exists() bool

	// LoadManifest loads the manifest file from disk.
	LoadManifest(validateChecksums bool) (*ManifestFile, error)

	// LoadIndex loads the full index into memory.
	LoadIndex(ctx context.Context) (*MemoryIndex, error)
}

// MemoryIndex holds the in-memory index for fast queries.
//
// # Description
//
// MemoryIndex provides O(1) lookups by symbol ID and efficient
// filtering by name, kind, and file path. Loaded from JSON files
// and kept in memory for the duration of operations.
//
// # Thread Safety
//
// MemoryIndex is safe for concurrent read access. Write operations
// (Add*) must be synchronized externally or done during initialization.
type MemoryIndex struct {
	Symbols []Symbol `json:"symbols"`
	Edges   []Edge   `json:"edges"`

	// In-memory indexes (built on load)
	byID   map[string]*Symbol
	byName map[string][]*Symbol
	byFile map[string][]*Symbol

	// Edge indexes
	callers map[string][]Edge // to_id -> edges
	callees map[string][]Edge // from_id -> edges

	mu sync.RWMutex
}

// NewMemoryIndex creates an empty MemoryIndex.
//
// # Description
//
// Creates an initialized MemoryIndex ready for adding symbols and edges.
//
// # Outputs
//
//   - *MemoryIndex: Empty index with initialized maps.
//
// # Thread Safety
//
// The returned MemoryIndex is safe for concurrent use after initialization.
func NewMemoryIndex() *MemoryIndex {
	return &MemoryIndex{
		Symbols: make([]Symbol, 0),
		Edges:   make([]Edge, 0),
		byID:    make(map[string]*Symbol),
		byName:  make(map[string][]*Symbol),
		byFile:  make(map[string][]*Symbol),
		callers: make(map[string][]Edge),
		callees: make(map[string][]Edge),
	}
}

// BuildIndexes builds the in-memory lookup indexes from Symbols and Edges.
//
// # Description
//
// Call this after loading symbols/edges from JSON to build the fast
// lookup maps. Must be called before query methods are used.
//
// # Thread Safety
//
// NOT safe for concurrent use. Call during initialization only.
func (m *MemoryIndex) BuildIndexes() {
	m.byID = make(map[string]*Symbol, len(m.Symbols))
	m.byName = make(map[string][]*Symbol)
	m.byFile = make(map[string][]*Symbol)
	m.callers = make(map[string][]Edge)
	m.callees = make(map[string][]Edge)

	for i := range m.Symbols {
		sym := &m.Symbols[i]
		m.byID[sym.ID] = sym
		m.byName[sym.Name] = append(m.byName[sym.Name], sym)
		m.byFile[sym.FilePath] = append(m.byFile[sym.FilePath], sym)
	}

	for _, edge := range m.Edges {
		m.callers[edge.ToID] = append(m.callers[edge.ToID], edge)
		m.callees[edge.FromID] = append(m.callees[edge.FromID], edge)
	}
}

// GetByID retrieves a symbol by its ID.
//
// # Inputs
//
//   - id: Symbol ID to look up.
//
// # Outputs
//
//   - *Symbol: The symbol, or nil if not found.
//
// # Thread Safety
//
// Safe for concurrent use.
func (m *MemoryIndex) GetByID(id string) *Symbol {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id]
}

// GetByName retrieves symbols by name.
//
// # Inputs
//
//   - name: Symbol name to look up.
//
// # Outputs
//
//   - []*Symbol: Matching symbols, or empty slice if none found.
//
// # Thread Safety
//
// Safe for concurrent use.
func (m *MemoryIndex) GetByName(name string) []*Symbol {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byName[name]
}

// GetByFile retrieves symbols in a file.
//
// # Inputs
//
//   - filePath: File path to look up.
//
// # Outputs
//
//   - []*Symbol: Symbols in the file, or empty slice if none.
//
// # Thread Safety
//
// Safe for concurrent use.
func (m *MemoryIndex) GetByFile(filePath string) []*Symbol {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byFile[filePath]
}

// GetCallers retrieves edges pointing TO the given symbol.
//
// # Inputs
//
//   - symbolID: Symbol ID to find callers for.
//   - limit: Maximum edges to return (0 = unlimited).
//
// # Outputs
//
//   - []Edge: Edges where ToID matches symbolID.
//
// # Thread Safety
//
// Safe for concurrent use.
func (m *MemoryIndex) GetCallers(symbolID string, limit int) []Edge {
	m.mu.RLock()
	defer m.mu.RUnlock()
	edges := m.callers[symbolID]
	if limit > 0 && len(edges) > limit {
		return edges[:limit]
	}
	return edges
}

// GetCallees retrieves edges originating FROM the given symbol.
//
// # Inputs
//
//   - symbolID: Symbol ID to find callees for.
//   - limit: Maximum edges to return (0 = unlimited).
//
// # Outputs
//
//   - []Edge: Edges where FromID matches symbolID.
//
// # Thread Safety
//
// Safe for concurrent use.
func (m *MemoryIndex) GetCallees(symbolID string, limit int) []Edge {
	m.mu.RLock()
	defer m.mu.RUnlock()
	edges := m.callees[symbolID]
	if limit > 0 && len(edges) > limit {
		return edges[:limit]
	}
	return edges
}

// SymbolCount returns the number of symbols in the index.
func (m *MemoryIndex) SymbolCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.Symbols)
}

// EdgeCount returns the number of edges in the index.
func (m *MemoryIndex) EdgeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.Edges)
}

// Storage provides persistent storage for the index using JSON files.
//
// # Description
//
// Storage implements both StorageWriter and StorageReader interfaces,
// providing JSON-based persistence for symbols and call graph edges.
// Data is loaded into memory for fast queries.
//
// # File Structure
//
//	.aleutian/
//	├── manifest.json   # File hashes and metadata
//	├── index.json      # Symbols and edges
//	└── config.yaml     # Project settings
//
// # Thread Safety
//
// Storage is safe for concurrent use. Write operations are serialized
// through atomic directory swaps.
type Storage struct {
	projectRoot string
}

// Compile-time interface verification.
var (
	_ StorageWriter = (*Storage)(nil)
	_ StorageReader = (*Storage)(nil)
)

// NewStorage creates a new Storage for the given project root.
//
// # Description
//
// Creates a Storage instance for managing the .aleutian/ directory.
// Does not create any files or directories until WriteIndex is called.
//
// # Inputs
//
//   - projectRoot: Absolute path to project root. Must not be empty.
//
// # Outputs
//
//   - *Storage: The storage instance.
//
// # Thread Safety
//
// The returned Storage is safe for concurrent use.
//
// # Assumptions
//
//   - projectRoot is an absolute path
//   - Caller has validated projectRoot exists and is a directory
func NewStorage(projectRoot string) *Storage {
	return &Storage{projectRoot: projectRoot}
}

// AleutianPath returns the path to the .aleutian directory.
//
// # Description
//
// Returns the full path to the .aleutian directory within the project.
//
// # Outputs
//
//   - string: Absolute path to .aleutian directory.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (s *Storage) AleutianPath() string {
	return filepath.Join(s.projectRoot, AleutianDir)
}

// Exists checks if the .aleutian directory exists with valid files.
//
// # Description
//
// Checks for the existence of the .aleutian directory and the manifest file.
// Does not validate checksums or file contents.
//
// # Outputs
//
//   - bool: True if .aleutian directory exists with manifest file.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (s *Storage) Exists() bool {
	path := s.AleutianPath()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}

	// Check for manifest file
	manifestPath := filepath.Join(path, ManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		return false
	}

	return true
}

// LoadManifest loads the manifest file from disk.
//
// # Description
//
// Reads and parses the manifest.json file. Optionally validates checksums
// of the index.json file against stored values.
//
// # Inputs
//
//   - validateChecksums: If true, verify index checksum matches stored value.
//
// # Outputs
//
//   - *ManifestFile: The loaded manifest. Never nil on success.
//   - error: Non-nil on failure. Returns ErrIndexCorrupted if file is invalid,
//     ErrVersionMismatch if format version differs, ErrChecksumMismatch if
//     checksums don't match.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (s *Storage) LoadManifest(validateChecksums bool) (*ManifestFile, error) {
	path := filepath.Join(s.AleutianPath(), ManifestFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var manifest ManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%w: parsing manifest: %v", ErrIndexCorrupted, err)
	}

	// Validate format version
	if manifest.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("%w: expected %s, got %s",
			ErrVersionMismatch, FormatVersion, manifest.FormatVersion)
	}

	// Validate checksums if requested
	if validateChecksums {
		if err := s.validateChecksums(&manifest); err != nil {
			return nil, err
		}
	}

	return &manifest, nil
}

// validateChecksums verifies the index file checksum.
func (s *Storage) validateChecksums(manifest *ManifestFile) error {
	indexPath := filepath.Join(s.AleutianPath(), IndexFileName)
	indexHash, err := hashFile(indexPath)
	if err != nil {
		return fmt.Errorf("hashing index.json: %w", err)
	}
	if indexHash != manifest.IndexChecksum {
		return fmt.Errorf("%w: index.json checksum mismatch", ErrChecksumMismatch)
	}

	return nil
}

// hashFile computes the SHA256 hash of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// LoadIndex loads the full index into memory.
//
// # Description
//
// Reads and parses the index.json file, then builds in-memory
// lookup indexes for fast queries.
//
// # Inputs
//
//   - ctx: Context for cancellation. Must not be nil.
//
// # Outputs
//
//   - *MemoryIndex: The loaded index with built lookup maps.
//   - error: Non-nil on failure.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (s *Storage) LoadIndex(ctx context.Context) (*MemoryIndex, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	path := filepath.Join(s.AleutianPath(), IndexFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}

	var index MemoryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("%w: parsing index: %v", ErrIndexCorrupted, err)
	}

	// Build lookup indexes
	index.BuildIndexes()

	return &index, nil
}

// WriteResult holds the result of writing the index.
type WriteResult struct {
	IndexPath     string
	IndexChecksum string
}

// WriteIndex atomically writes the index to disk as JSON files.
//
// # Description
//
// Uses the temp-directory-swap pattern for atomic writes:
//  1. Write to temp directory (.aleutian.tmp.{timestamp})
//  2. Compute SHA256 checksum of index.json
//  3. Backup existing .aleutian (if any) to .aleutian.backup.{timestamp}
//  4. Atomic rename temp to .aleutian
//  5. Remove backup on success, restore on failure
//
// # Inputs
//
//   - ctx: Context for cancellation. Must not be nil.
//   - symbols: Symbols to write to index.json. May be empty.
//   - edges: Edges to write to index.json. May be empty.
//   - manifest: Manifest with file hashes. Must not be nil.
//   - config: Project configuration. Must not be nil.
//
// # Outputs
//
//   - *WriteResult: Path and checksum of written index. Never nil on success.
//   - error: Non-nil on failure. Returns ErrAtomicSwapFailed if rename fails,
//     context.Canceled or context.DeadlineExceeded if cancelled.
//
// # Thread Safety
//
// This method is safe for concurrent use. Uses atomic operations.
//
// # Limitations
//
//   - Requires write permission on project root directory
//   - Temp directory must be on same filesystem for atomic rename
func (s *Storage) WriteIndex(
	ctx context.Context,
	symbols []Symbol,
	edges []Edge,
	manifest *ManifestFile,
	config *ProjectConfig,
) (*WriteResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if manifest == nil {
		return nil, fmt.Errorf("manifest must not be nil")
	}
	if config == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	// Generate unique temp directory name
	tempDir := filepath.Join(s.projectRoot, fmt.Sprintf(".aleutian.tmp.%d", time.Now().UnixNano()))

	// Ensure cleanup on error
	var cleanupTemp = true
	defer func() {
		if cleanupTemp {
			os.RemoveAll(tempDir)
		}
	}()

	// Create temp directory
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, &StorageError{Op: "create_temp_dir", Err: err}
	}

	// Check for cancellation
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("write index cancelled: %w", ctx.Err())
	default:
	}

	// Write index.json
	indexPath := filepath.Join(tempDir, IndexFileName)
	if err := s.writeIndexJSON(indexPath, symbols, edges); err != nil {
		return nil, fmt.Errorf("writing index.json: %w", err)
	}

	// Compute checksum
	indexChecksum, err := hashFile(indexPath)
	if err != nil {
		return nil, &StorageError{Op: "hash_index", Err: err}
	}

	// Update manifest with checksum
	manifest.IndexChecksum = indexChecksum
	manifest.GraphChecksum = "" // Not used in JSON mode
	manifest.UpdatedAtMilli = time.Now().UnixMilli()

	// Write manifest
	manifestPath := filepath.Join(tempDir, ManifestFileName)
	if err := s.writeManifest(manifestPath, manifest); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}

	// Write config
	configPath := filepath.Join(tempDir, ConfigFileName)
	if err := s.writeConfig(configPath, config); err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}

	// Check for cancellation before atomic swap
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("write index cancelled before swap: %w", ctx.Err())
	default:
	}

	// Perform atomic swap
	targetDir := s.AleutianPath()
	backupDir := targetDir + fmt.Sprintf(".backup.%d", time.Now().UnixNano())

	// Backup existing directory if it exists
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.Rename(targetDir, backupDir); err != nil {
			return nil, fmt.Errorf("%w: backup existing: %v", ErrAtomicSwapFailed, err)
		}
		defer func() {
			if cleanupTemp {
				// Restore backup on failure
				os.Rename(backupDir, targetDir)
			} else {
				// Remove backup on success
				os.RemoveAll(backupDir)
			}
		}()
	}

	// Atomic rename
	if err := os.Rename(tempDir, targetDir); err != nil {
		return nil, fmt.Errorf("%w: rename: %v", ErrAtomicSwapFailed, err)
	}

	// Success - don't cleanup temp (it's now the target)
	cleanupTemp = false

	return &WriteResult{
		IndexPath:     targetDir,
		IndexChecksum: indexChecksum,
	}, nil
}

// writeIndexJSON writes symbols and edges to a JSON file.
func (s *Storage) writeIndexJSON(path string, symbols []Symbol, edges []Edge) error {
	index := MemoryIndex{
		Symbols: symbols,
		Edges:   edges,
	}

	data, err := json.MarshalIndent(&index, "", "  ")
	if err != nil {
		return &StorageError{Op: "marshal_index", Err: err}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return &StorageError{Op: "write_index", Err: err}
	}

	return nil
}

// writeManifest writes the manifest to a JSON file.
func (s *Storage) writeManifest(path string, manifest *ManifestFile) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return &StorageError{Op: "marshal_manifest", Err: err}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return &StorageError{Op: "write_manifest", Err: err}
	}

	return nil
}

// writeConfig writes the project config to a YAML file.
func (s *Storage) writeConfig(path string, config *ProjectConfig) error {
	// Simple YAML serialization (avoiding external dependency)
	content := fmt.Sprintf(`# Aleutian Trace Configuration
# Auto-generated - do not edit manually

format_version: %q
created_at: %q
updated_at: %q

languages:
`, config.FormatVersion, config.CreatedAt, config.UpdatedAt)

	for _, lang := range config.Languages {
		content += fmt.Sprintf("  - %q\n", lang)
	}

	content += "\nexclude_patterns:\n"
	for _, pattern := range config.ExcludePatterns {
		content += fmt.Sprintf("  - %q\n", pattern)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &StorageError{Op: "write_config", Err: err}
	}

	return nil
}
