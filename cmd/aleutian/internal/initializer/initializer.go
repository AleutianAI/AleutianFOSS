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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// IndexBuilder defines the interface for building the code index.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type IndexBuilder interface {
	// Init initializes the index for a project.
	Init(ctx context.Context, cfg Config, progress ProgressCallback) (*Result, error)
}

// Initializer builds the code index for a project.
//
// # Description
//
// Initializer orchestrates the parsing and indexing of source files.
// It uses buffered channels for work distribution to achieve
// parallel file processing.
//
// # Architecture
//
//	┌─────────┐     ┌───────────────────┐     ┌───────────────────┐
//	│ Scanner │────▶│ fileChan (buffer) │────▶│ Worker Pool (N)   │
//	└─────────┘     └───────────────────┘     └───────────────────┘
//	                                                   │
//	                                                   ▼
//	┌─────────┐     ┌───────────────────┐     ┌───────────────────┐
//	│ Collector│◀────│ resultChan (buf) │◀────│ Parse Results     │
//	└─────────┘     └───────────────────┘     └───────────────────┘
//
// # Thread Safety
//
// Initializer is safe for concurrent use.
type Initializer struct {
	storage StorageWriter
}

// Compile-time interface verification.
var _ IndexBuilder = (*Initializer)(nil)

// NewInitializer creates a new Initializer.
//
// # Description
//
// Creates an Initializer with the given storage backend.
//
// # Inputs
//
//   - storage: Storage backend for persisting the index. Must not be nil.
//
// # Outputs
//
//   - *Initializer: The initializer instance.
//
// # Thread Safety
//
// The returned Initializer is safe for concurrent use.
func NewInitializer(storage StorageWriter) *Initializer {
	return &Initializer{storage: storage}
}

// Init initializes the index for a project.
//
// # Description
//
// Performs full initialization:
//  1. Acquires exclusive lock to prevent concurrent inits
//  2. Detects languages in project (if not specified)
//  3. Scans for source files matching language extensions
//  4. Parses files in parallel using buffered channels
//  5. Collects symbols and builds call graph
//  6. Writes index atomically to .aleutian/
//
// # Inputs
//
//   - ctx: Context for cancellation. Must not be nil.
//   - cfg: Configuration for initialization. Must be valid (cfg.Validate() == nil).
//   - progress: Callback for progress updates. May be nil.
//
// # Outputs
//
//   - *Result: Initialization results. Never nil on success.
//   - error: Non-nil on failure. See errors.go for possible errors.
//
// # Limitations
//
//   - Only one init can run per project at a time (file lock)
//   - Files larger than cfg.MaxFileSize are skipped
//   - Unparseable files are skipped with warnings (not errors)
//
// # Assumptions
//
//   - cfg.ProjectRoot is an absolute path to an existing directory
//   - Caller has write permission on the project root
func (i *Initializer) Init(ctx context.Context, cfg Config, progress ProgressCallback) (*Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	start := time.Now()
	result := NewResult()
	result.ProjectRoot = cfg.ProjectRoot

	// Validate project root exists
	info, err := os.Stat(cfg.ProjectRoot)
	if os.IsNotExist(err) {
		return nil, ErrPathNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("stat project root: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrPathNotDirectory
	}

	// Acquire lock
	lock, err := NewFileLock(cfg.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("creating lock: %w", err)
	}

	if err := lock.Acquire(); err != nil {
		if lock.IsStale() {
			// Try to force release stale lock
			if err := lock.ForceRelease(); err == nil {
				err = lock.Acquire()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("acquiring lock: %w", err)
		}
	}
	defer lock.Release()

	// Report progress: starting
	if progress != nil {
		progress(Progress{
			Phase:   "detecting",
			Percent: 0,
		})
	}

	// Detect languages if not specified
	languages := cfg.Languages
	if len(languages) == 0 {
		languages = detectLanguages(cfg.ProjectRoot)
	}
	if len(languages) == 0 {
		return nil, ErrNoLanguages
	}
	result.Languages = languages

	// Check for dry run
	if cfg.DryRun {
		files, err := scanFiles(ctx, cfg.ProjectRoot, languages, cfg.ExcludePatterns, cfg.MaxFileSize)
		if err != nil {
			return nil, fmt.Errorf("scanning files: %w", err)
		}
		result.FilesIndexed = len(files)
		result.DurationMs = time.Since(start).Milliseconds()
		result.IndexPath = filepath.Join(cfg.ProjectRoot, AleutianDir)
		return result, nil
	}

	// Report progress: scanning
	if progress != nil {
		progress(Progress{
			Phase:   "scanning",
			Percent: 5,
		})
	}

	// Scan for files
	files, err := scanFiles(ctx, cfg.ProjectRoot, languages, cfg.ExcludePatterns, cfg.MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("scanning files: %w", err)
	}
	if len(files) == 0 {
		return nil, ErrNoSupportedFiles
	}

	// Report progress: parsing
	if progress != nil {
		progress(Progress{
			Phase:      "parsing",
			FilesTotal: len(files),
			Percent:    10,
		})
	}

	// Parse files in parallel using buffered channels
	symbols, edges, warnings, err := i.parseFilesParallel(ctx, cfg, files, progress)
	if err != nil {
		return nil, fmt.Errorf("parsing files: %w", err)
	}
	result.Warnings = warnings

	// Report progress: writing
	if progress != nil {
		progress(Progress{
			Phase:   "writing",
			Percent: 90,
		})
	}

	// Create manifest
	manifest := &ManifestFile{
		FormatVersion:  FormatVersion,
		ProjectRoot:    cfg.ProjectRoot,
		Files:          make(map[string]FileEntry),
		CreatedAtMilli: time.Now().UnixMilli(),
		UpdatedAtMilli: time.Now().UnixMilli(),
	}

	// Add files to manifest
	for _, f := range files {
		relPath, _ := filepath.Rel(cfg.ProjectRoot, f)
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		hash := hashFilePath(f)
		manifest.Files[relPath] = FileEntry{
			Path:  relPath,
			Hash:  hash,
			Mtime: info.ModTime().UnixNano(),
			Size:  info.Size(),
		}
	}

	// Create project config
	now := time.Now().Format(time.RFC3339)
	projectConfig := &ProjectConfig{
		FormatVersion:   FormatVersion,
		Languages:       languages,
		ExcludePatterns: cfg.ExcludePatterns,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Write index
	writeResult, err := i.storage.WriteIndex(ctx, symbols, edges, manifest, projectConfig)
	if err != nil {
		return nil, fmt.Errorf("writing index: %w", err)
	}

	// Suggest adding .aleutian to .gitignore
	gitignorePath := filepath.Join(cfg.ProjectRoot, ".gitignore")
	if shouldSuggestGitignore(gitignorePath) {
		result.Warnings = append(result.Warnings,
			"Consider adding .aleutian/ to your .gitignore file")
	}

	// Populate result
	result.FilesIndexed = len(files)
	result.SymbolsFound = len(symbols)
	result.EdgesBuilt = len(edges)
	result.DurationMs = time.Since(start).Milliseconds()
	result.IndexPath = writeResult.IndexPath

	// Report progress: complete
	if progress != nil {
		progress(Progress{
			Phase:   "complete",
			Percent: 100,
		})
	}

	return result, nil
}

// parseFilesParallel parses files using buffered channels for concurrency.
//
// # Description
//
// Uses a worker pool pattern with buffered channels:
//   - fileChan: buffered channel for distributing files to workers
//   - resultChan: buffered channel for collecting parse results
//
// Workers read from fileChan until it's closed, then exit.
// The collector reads from resultChan until all workers are done.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - cfg: Configuration with MaxWorkers and FileTimeout.
//   - files: List of file paths to parse.
//   - progress: Optional progress callback.
//
// # Outputs
//
//   - []Symbol: All extracted symbols.
//   - []Edge: All extracted edges.
//   - []string: Warnings for files that failed to parse.
//   - error: Non-nil only on fatal errors or context cancellation.
func (i *Initializer) parseFilesParallel(
	ctx context.Context,
	cfg Config,
	files []string,
	progress ProgressCallback,
) ([]Symbol, []Edge, []string, error) {
	numWorkers := cfg.MaxWorkers
	if numWorkers > len(files) {
		numWorkers = len(files)
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}

	// Buffered channels for work distribution
	fileChan := make(chan string, DefaultChannelBuffer)
	resultChan := make(chan ParseResult, DefaultChannelBuffer)

	// WaitGroup for workers
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Start workers
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for filePath := range fileChan {
				// Check for cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Parse with timeout
				result := parseFileWithTimeout(ctx, filePath, cfg.FileTimeout)
				resultChan <- result
			}
		}()
	}

	// Goroutine to close resultChan when workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Send files to workers
	go func() {
		defer close(fileChan)
		for _, f := range files {
			select {
			case <-ctx.Done():
				return
			case fileChan <- f:
			}
		}
	}()

	// Collect results
	var (
		symbols   []Symbol
		edges     []Edge
		warnings  []string
		processed int
	)

	for result := range resultChan {
		processed++

		// Update progress
		if progress != nil && processed%DefaultProgressBatch == 0 {
			percent := 10 + (processed*80)/len(files)
			progress(Progress{
				Phase:        "parsing",
				FilesTotal:   len(files),
				FilesScanned: processed,
				FilesCurrent: result.FilePath,
				Percent:      percent,
			})
		}

		// Handle errors as warnings (continue processing)
		if result.Error != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", result.FilePath, result.Error))
			continue
		}

		symbols = append(symbols, result.Symbols...)
		edges = append(edges, result.Edges...)
	}

	return symbols, edges, warnings, nil
}

// parseFileWithTimeout parses a single file with optional timeout.
func parseFileWithTimeout(ctx context.Context, filePath string, timeout time.Duration) ParseResult {
	result := ParseResult{FilePath: filePath}

	// Create timeout context if specified
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Read file
	content, err := os.ReadFile(filePath)
	if err != nil {
		result.Error = fmt.Errorf("reading file: %w", err)
		return result
	}

	// Detect language from extension
	lang := languageFromExtension(filepath.Ext(filePath))

	// Parse using simple extraction (tree-sitter would be used in production)
	symbols, edges := extractSymbolsSimple(ctx, filePath, content, lang)
	result.Symbols = symbols
	result.Edges = edges

	return result
}

// extractSymbolsSimple is a simple symbol extractor for demonstration.
// In production, this would use tree-sitter parsers from services/trace/ast/.
func extractSymbolsSimple(ctx context.Context, filePath string, content []byte, lang string) ([]Symbol, []Edge) {
	symbols := make([]Symbol, 0)
	edges := make([]Edge, 0)

	// Generate a deterministic ID for the file
	fileID := generateSymbolID(filePath, "file", "")

	// Add file as a symbol
	symbols = append(symbols, Symbol{
		ID:        fileID,
		Name:      filepath.Base(filePath),
		Kind:      "file",
		FilePath:  filePath,
		StartLine: 1,
		EndLine:   len(strings.Split(string(content), "\n")),
		StartCol:  0,
		EndCol:    0,
	})

	// Simple function detection for Go files
	if lang == "go" {
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			select {
			case <-ctx.Done():
				return symbols, edges
			default:
			}

			// Simple function detection: func Name(
			if idx := strings.Index(line, "func "); idx >= 0 {
				rest := line[idx+5:]
				// Skip receiver if present
				if strings.HasPrefix(rest, "(") {
					closeIdx := strings.Index(rest, ")")
					if closeIdx >= 0 {
						rest = strings.TrimSpace(rest[closeIdx+1:])
					}
				}
				// Extract function name
				parenIdx := strings.Index(rest, "(")
				if parenIdx > 0 {
					name := strings.TrimSpace(rest[:parenIdx])
					if name != "" && isValidIdentifier(name) {
						symID := generateSymbolID(filePath, "function", name)
						symbols = append(symbols, Symbol{
							ID:        symID,
							Name:      name,
							Kind:      "function",
							FilePath:  filePath,
							StartLine: lineNum + 1,
							EndLine:   lineNum + 1, // Would need proper parsing
							StartCol:  idx,
							EndCol:    idx + len(name),
						})

						// Add edge from file to function
						edges = append(edges, Edge{
							FromID:   fileID,
							ToID:     symID,
							Kind:     "defines",
							FilePath: filePath,
							Line:     lineNum + 1,
						})
					}
				}
			}
		}
	}

	return symbols, edges
}

// detectLanguages detects programming languages in a project.
func detectLanguages(root string) []string {
	languages := make(map[string]bool)

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip common non-source directories
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == "vendor" || name == "node_modules" || name == ".git" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		ext := filepath.Ext(path)
		switch ext {
		case ".go":
			languages["go"] = true
		case ".py":
			languages["python"] = true
		case ".ts", ".tsx":
			languages["typescript"] = true
		case ".js", ".jsx":
			languages["javascript"] = true
		case ".java":
			languages["java"] = true
		case ".rs":
			languages["rust"] = true
		}

		return nil
	})

	result := make([]string, 0, len(languages))
	for lang := range languages {
		result = append(result, lang)
	}
	return result
}

// scanFiles finds all source files matching the given languages.
func scanFiles(ctx context.Context, root string, languages, excludes []string, maxSize int64) ([]string, error) {
	var files []string
	extensions := languageExtensions(languages)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // Skip errors
		}

		// Skip directories
		if info.IsDir() {
			name := info.Name()
			// Skip common non-source directories
			if name == "vendor" || name == "node_modules" || name == ".git" || name == ".aleutian" {
				return filepath.SkipDir
			}
			// Check exclude patterns
			for _, pattern := range excludes {
				if matched, _ := filepath.Match(pattern, name); matched {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip large files
		if info.Size() > maxSize {
			return nil
		}

		// Check extension
		ext := filepath.Ext(path)
		for _, e := range extensions {
			if ext == e {
				// Check exclude patterns
				relPath, _ := filepath.Rel(root, path)
				excluded := false
				for _, pattern := range excludes {
					if matched, _ := filepath.Match(pattern, relPath); matched {
						excluded = true
						break
					}
					if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
						excluded = true
						break
					}
				}
				if !excluded {
					files = append(files, path)
				}
				break
			}
		}

		return nil
	})

	return files, err
}

// languageExtensions returns file extensions for the given languages.
func languageExtensions(languages []string) []string {
	var exts []string
	for _, lang := range languages {
		switch lang {
		case "go":
			exts = append(exts, ".go")
		case "python":
			exts = append(exts, ".py")
		case "typescript":
			exts = append(exts, ".ts", ".tsx")
		case "javascript":
			exts = append(exts, ".js", ".jsx")
		case "java":
			exts = append(exts, ".java")
		case "rust":
			exts = append(exts, ".rs")
		}
	}
	return exts
}

// languageFromExtension returns the language for a file extension.
func languageFromExtension(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

// generateSymbolID creates a unique ID for a symbol.
func generateSymbolID(filePath, kind, name string) string {
	input := fmt.Sprintf("%s:%s:%s", filePath, kind, name)
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes (32 hex chars)
}

// hashFilePath computes a quick hash of a file path.
func hashFilePath(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:])
}

// isValidIdentifier checks if a string is a valid Go identifier.
func isValidIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return true
}

// shouldSuggestGitignore checks if we should suggest adding .aleutian to .gitignore.
func shouldSuggestGitignore(gitignorePath string) bool {
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		// No .gitignore exists, suggest creating one
		return true
	}

	// Check if .aleutian is already in .gitignore
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == ".aleutian" || line == ".aleutian/" || line == "/.aleutian" || line == "/.aleutian/" {
			return false
		}
	}

	return true
}

// OptimalWorkerCount returns the optimal number of workers for the system.
func OptimalWorkerCount() int {
	cpus := runtime.NumCPU()
	if cpus > DefaultMaxWorkers {
		return DefaultMaxWorkers
	}
	return cpus
}
