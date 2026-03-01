// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package file provides file operation tools for the Aleutian Trace CLI.
//
// This package implements five core file tools:
//   - Read: Read file contents with line numbers and pagination
//   - Write: Create new files with atomic writes
//   - Edit: Make surgical edits via old_string → new_string replacement
//   - Glob: Find files by glob pattern
//   - Grep: Search file contents with regex
//
// All tools integrate with the tool registry and support the 2-LLM architecture
// where the micro LLM (Granite4) routes queries to appropriate tools.
//
// Thread Safety: All tools are safe for concurrent use.
package file

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// Constants
// ============================================================================

const (
	// Read tool limits
	DefaultReadLimit   = 2000
	MaxReadLimit       = 10000
	MaxFileSizeBytes   = 10 * 1024 * 1024 // 10MB max file size for read
	MaxLineLengthChars = 2000

	// Write tool limits
	MaxWriteContentSize = 5 * 1024 * 1024 // 5MB max write content

	// Edit tool limits
	MaxEditFileSize = 10 * 1024 * 1024 // 10MB max file size for edit

	// Glob tool limits
	DefaultGlobLimit = 100
	MaxGlobLimit     = 1000
	MaxPatternLength = 256

	// Grep tool limits
	DefaultGrepLimit = 100
	MaxGrepLimit     = 500
	MaxContextLines  = 10
	MaxGrepPattern   = 1024
)

// Default exclusion patterns for glob and grep operations.
var DefaultExclusions = []string{
	".git",
	"node_modules",
	"vendor",
	"__pycache__",
	".venv",
	"venv",
	".idea",
	".vscode",
	"dist",
	"build",
	".next",
	"target",
}

// ============================================================================
// Read Types
// ============================================================================

// ReadParams defines parameters for the Read tool.
type ReadParams struct {
	// FilePath is the absolute path to the file to read.
	FilePath string `json:"file_path"`

	// Offset is the line number to start reading from (1-indexed).
	// If 0 or not provided, reads from the beginning.
	Offset int `json:"offset,omitempty"`

	// Limit is the maximum number of lines to read.
	// Defaults to DefaultReadLimit if not provided.
	Limit int `json:"limit,omitempty"`
}

// Validate checks that ReadParams are valid.
// Note: Relative paths are allowed and should be resolved by the tool against the working directory.
func (p *ReadParams) Validate() error {
	if p.FilePath == "" {
		return errors.New("file_path is required")
	}
	if strings.Contains(p.FilePath, "..") {
		return errors.New("file_path must not contain '..'")
	}
	if p.Offset < 0 {
		return errors.New("offset must be >= 0")
	}
	if p.Limit < 0 || p.Limit > MaxReadLimit {
		return fmt.Errorf("limit must be between 0 and %d", MaxReadLimit)
	}
	return nil
}

// ReadResult contains the outcome of a Read operation.
type ReadResult struct {
	// Content is the file content with line numbers.
	Content string `json:"content"`

	// TotalLines is the total number of lines in the file.
	TotalLines int `json:"total_lines"`

	// LinesRead is the number of lines actually read.
	LinesRead int `json:"lines_read"`

	// Truncated indicates if output was truncated due to limits.
	Truncated bool `json:"truncated"`

	// TruncatedAt is the line number where truncation occurred.
	TruncatedAt int `json:"truncated_at,omitempty"`

	// BytesRead is the number of bytes read from the file.
	BytesRead int64 `json:"bytes_read"`

	// FileType indicates the detected file type.
	FileType string `json:"file_type,omitempty"`
}

// ============================================================================
// Write Types
// ============================================================================

// WriteParams defines parameters for the Write tool.
type WriteParams struct {
	// FilePath is the absolute path for the new file.
	FilePath string `json:"file_path"`

	// Content is the file content to write.
	Content string `json:"content"`
}

// Validate checks that WriteParams are valid.
// Note: Relative paths are allowed and should be resolved by the tool against the working directory.
func (p *WriteParams) Validate() error {
	if p.FilePath == "" {
		return errors.New("file_path is required")
	}
	if strings.Contains(p.FilePath, "..") {
		return errors.New("file_path must not contain '..'")
	}
	if len(p.Content) > MaxWriteContentSize {
		return fmt.Errorf("content exceeds max size of %d bytes", MaxWriteContentSize)
	}
	return nil
}

// WriteResult contains the outcome of a Write operation.
type WriteResult struct {
	// Success indicates if the write succeeded.
	Success bool `json:"success"`

	// BytesWritten is the number of bytes written.
	BytesWritten int64 `json:"bytes_written"`

	// Path is the absolute path of the written file.
	Path string `json:"path"`

	// Created indicates if a new file was created (vs overwritten).
	Created bool `json:"created"`
}

// ============================================================================
// Edit Types
// ============================================================================

// EditParams defines parameters for the Edit tool.
type EditParams struct {
	// FilePath is the absolute path to the file to edit.
	FilePath string `json:"file_path"`

	// OldString is the exact text to replace.
	OldString string `json:"old_string"`

	// NewString is the replacement text.
	NewString string `json:"new_string"`

	// ReplaceAll replaces all occurrences if true.
	ReplaceAll bool `json:"replace_all,omitempty"`
}

// Validate checks that EditParams are valid.
// Note: Relative paths are allowed and should be resolved by the tool against the working directory.
func (p *EditParams) Validate() error {
	if p.FilePath == "" {
		return errors.New("file_path is required")
	}
	if strings.Contains(p.FilePath, "..") {
		return errors.New("file_path must not contain '..'")
	}
	if p.OldString == "" {
		return errors.New("old_string is required")
	}
	if p.OldString == p.NewString {
		return errors.New("old_string and new_string are identical")
	}
	return nil
}

// EditResult contains the outcome of an Edit operation.
type EditResult struct {
	// Success indicates if the edit succeeded.
	Success bool `json:"success"`

	// Replacements is the number of replacements made.
	Replacements int `json:"replacements"`

	// Diff is the unified diff of the changes.
	Diff string `json:"diff"`

	// Path is the absolute path of the edited file.
	Path string `json:"path"`
}

// Edit errors for specific failure modes.
var (
	ErrNoMatch       = errors.New("old_string not found in file")
	ErrMultipleMatch = errors.New("old_string matches multiple times; use replace_all=true or provide more context")
	ErrFileNotRead   = errors.New("file must be read before editing")
	ErrConflict      = errors.New("file was modified externally since last read; re-read and retry")
)

// EditError provides detailed error information for edit failures.
type EditError struct {
	// Err is the underlying error.
	Err error

	// MatchCount is the number of matches found.
	MatchCount int

	// Suggestion provides guidance for fixing the error.
	Suggestion string
}

// Error implements the error interface.
func (e *EditError) Error() string {
	if e.Suggestion != "" {
		return fmt.Sprintf("%v (suggestion: %s)", e.Err, e.Suggestion)
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *EditError) Unwrap() error {
	return e.Err
}

// ============================================================================
// Glob Types
// ============================================================================

// GlobParams defines parameters for the Glob tool.
type GlobParams struct {
	// Pattern is the glob pattern to match (e.g., "**/*.go").
	Pattern string `json:"pattern"`

	// Path is the directory to search in. Defaults to current working directory.
	Path string `json:"path,omitempty"`

	// Limit is the maximum number of results. Defaults to DefaultGlobLimit.
	Limit int `json:"limit,omitempty"`
}

// Validate checks that GlobParams are valid.
func (p *GlobParams) Validate() error {
	if p.Pattern == "" {
		return errors.New("pattern is required")
	}
	if len(p.Pattern) > MaxPatternLength {
		return fmt.Errorf("pattern exceeds max length of %d", MaxPatternLength)
	}
	// Reject patterns that could cause excessive traversal
	if strings.Count(p.Pattern, "**") > 3 {
		return errors.New("pattern contains too many ** wildcards")
	}
	if p.Limit < 0 || p.Limit > MaxGlobLimit {
		return fmt.Errorf("limit must be between 0 and %d", MaxGlobLimit)
	}
	if p.Path != "" && !filepath.IsAbs(p.Path) {
		return errors.New("path must be absolute if provided")
	}
	return nil
}

// GlobResult contains the outcome of a Glob operation.
type GlobResult struct {
	// Files is the list of matching files.
	Files []FileInfo `json:"files"`

	// Count is the total number of matches found.
	Count int `json:"count"`

	// Truncated indicates if results were truncated due to limit.
	Truncated bool `json:"truncated"`

	// SearchPath is the directory that was searched.
	SearchPath string `json:"search_path"`
}

// FileInfo contains metadata about a file.
type FileInfo struct {
	// Path is the absolute path to the file.
	Path string `json:"path"`

	// RelPath is the path relative to the search root.
	RelPath string `json:"rel_path,omitempty"`

	// Size is the file size in bytes.
	Size int64 `json:"size"`

	// ModTime is the last modification time (Unix milliseconds UTC).
	ModTime int64 `json:"mod_time"`

	// IsDir indicates if this is a directory.
	IsDir bool `json:"is_dir,omitempty"`
}

// ============================================================================
// Grep Types
// ============================================================================

// GrepParams defines parameters for the Grep tool.
type GrepParams struct {
	// Pattern is the regex pattern to search for.
	Pattern string `json:"pattern"`

	// Path is the file or directory to search in.
	Path string `json:"path,omitempty"`

	// Glob is an optional file pattern filter (e.g., "*.go").
	Glob string `json:"glob,omitempty"`

	// ContextLines is the number of lines to show before and after each match.
	ContextLines int `json:"context_lines,omitempty"`

	// CaseInsensitive enables case-insensitive matching.
	CaseInsensitive bool `json:"case_insensitive,omitempty"`

	// Limit is the maximum number of matches. Defaults to DefaultGrepLimit.
	Limit int `json:"limit,omitempty"`

	// Fuzzy enables fzf-style fuzzy matching where characters must appear
	// in order but not necessarily adjacent. Example: "prsfil" matches "parseFile".
	Fuzzy bool `json:"fuzzy,omitempty"`

	// Approximate enables agrep-style approximate matching using Levenshtein
	// distance. Example: "functon" matches "function" with MaxErrors=1.
	Approximate bool `json:"approximate,omitempty"`

	// MaxErrors is the maximum edit distance for approximate matching.
	// Only used when Approximate=true. Default: 2, Max: 5.
	MaxErrors int `json:"max_errors,omitempty"`
}

// MaxFuzzyErrors is the maximum allowed edit distance for approximate matching.
const MaxFuzzyErrors = 5

// Validate checks that GrepParams are valid.
func (p *GrepParams) Validate() error {
	if p.Pattern == "" {
		return errors.New("pattern is required")
	}
	if len(p.Pattern) > MaxGrepPattern {
		return fmt.Errorf("pattern exceeds max length of %d", MaxGrepPattern)
	}
	if p.ContextLines < 0 || p.ContextLines > MaxContextLines {
		return fmt.Errorf("context_lines must be between 0 and %d", MaxContextLines)
	}
	// GR-59 Group D: Clamp limit instead of rejecting out-of-range values.
	// LLMs sometimes submit large limits; clamping is more resilient than erroring.
	if p.Limit < 0 {
		p.Limit = DefaultGrepLimit
	}
	if p.Limit > MaxGrepLimit {
		p.Limit = MaxGrepLimit
	}
	if p.Path != "" && !filepath.IsAbs(p.Path) {
		return errors.New("path must be absolute if provided")
	}
	if p.Fuzzy && p.Approximate {
		return errors.New("cannot use both fuzzy and approximate matching")
	}
	if p.MaxErrors < 0 || p.MaxErrors > MaxFuzzyErrors {
		return fmt.Errorf("max_errors must be between 0 and %d", MaxFuzzyErrors)
	}
	return nil
}

// GrepResult contains the outcome of a Grep operation.
type GrepResult struct {
	// Matches is the list of matching lines.
	Matches []GrepMatch `json:"matches"`

	// Count is the total number of matches found.
	Count int `json:"count"`

	// Truncated indicates if results were truncated due to limit.
	Truncated bool `json:"truncated"`

	// FilesSearched is the number of files searched.
	FilesSearched int `json:"files_searched"`

	// SearchPath is the directory or file that was searched.
	SearchPath string `json:"search_path"`
}

// GrepMatch represents a single match from a grep operation.
type GrepMatch struct {
	// File is the absolute path to the file containing the match.
	File string `json:"file"`

	// Line is the 1-indexed line number of the match.
	Line int `json:"line"`

	// Content is the matching line content.
	Content string `json:"content"`

	// ContextBefore contains lines before the match.
	ContextBefore []string `json:"context_before,omitempty"`

	// ContextAfter contains lines after the match.
	ContextAfter []string `json:"context_after,omitempty"`
}

// ============================================================================
// Path Safety
// ============================================================================

// LSPReleaser provides methods to coordinate file access with LSP servers.
//
// Description:
//
//	On Windows, LSP servers (like gopls) hold file handles open, which
//	prevents atomic file operations (os.Rename). This interface allows
//	the WriteTool to notify LSP servers to release file handles before
//	writing, and reopen them afterward.
//
// Thread Safety:
//
//	Implementations must be safe for concurrent use.
type LSPReleaser interface {
	// ReleaseFile tells LSP servers to close the file.
	// Called before atomic writes on Windows.
	ReleaseFile(ctx context.Context, filePath string) error

	// ReopenFile tells LSP servers to reopen the file with new content.
	// Called after atomic writes on Windows.
	ReopenFile(ctx context.Context, filePath string, content string, languageID string) error
}

// GraphRefresher provides synchronous graph updates after file writes.
//
// Description:
//
//	When the agent writes a file, the code graph becomes stale. Instead of
//	waiting for fsnotify to detect the change and trigger an asynchronous
//	refresh (which can cause event storms and stale queries), this interface
//	allows the WriteTool to refresh the graph synchronously BEFORE returning.
//
//	This prevents the "event storm loop" where:
//	1. Agent writes file
//	2. fsnotify fires, graph refresher starts parsing
//	3. Agent writes again (before refresh completes)
//	4. Agent queries graph - gets STALE version
//	5. Agent thinks fix didn't work, writes again → infinite loop
//
// Thread Safety:
//
//	Implementations must be safe for concurrent use.
type GraphRefresher interface {
	// RefreshFiles synchronously refreshes the graph for the given file paths.
	// Returns after the graph is updated. If an error occurs, the graph may
	// be partially updated but should not corrupt existing data.
	RefreshFiles(ctx context.Context, paths []string) error
}

// Config holds configuration for file tools.
type Config struct {
	// AllowedPaths is a list of paths that file operations are allowed in.
	// If empty, only the working directory and its subdirectories are allowed.
	AllowedPaths []string

	// WorkingDir is the current working directory.
	WorkingDir string

	// ReadTracking tracks which files have been read (for Edit validation).
	// Maps file path to read time (Unix milliseconds UTC).
	ReadTracking map[string]int64

	// ContentHashes stores content hashes for optimistic locking.
	// When a file is read, its hash is stored. During edit, we verify
	// the hash matches to detect external modifications.
	ContentHashes map[string]string

	// LSPReleaser coordinates file access with LSP servers.
	// Optional. If nil, no LSP coordination is performed.
	// Required for Windows to avoid "Access is denied" errors during atomic writes.
	LSPReleaser LSPReleaser

	// GraphRefresher provides synchronous graph updates after file writes.
	// Optional. If nil, no synchronous refresh is performed.
	// Recommended to prevent event storms and stale graph queries.
	GraphRefresher GraphRefresher
}

// NewConfig creates a new Config with the given working directory.
// The working directory is resolved to its real path (symlinks followed).
func NewConfig(workingDir string) *Config {
	// Resolve symlinks to get real path (handles macOS /var -> /private/var)
	realDir, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		realDir = workingDir // Fallback if resolution fails
	}
	return &Config{
		WorkingDir:    realDir,
		AllowedPaths:  []string{realDir},
		ReadTracking:  make(map[string]int64),
		ContentHashes: make(map[string]string),
	}
}

// IsPathAllowed checks if a path is within allowed directories.
// The path is resolved through symlinks before checking.
func (c *Config) IsPathAllowed(path string) bool {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// Try to resolve symlinks (handles macOS /var -> /private/var)
	realPath := resolvePathWithAncestors(absPath)

	// Check against allowed paths
	for _, allowed := range c.AllowedPaths {
		// AllowedPaths are already resolved in NewConfig
		if strings.HasPrefix(realPath, allowed) {
			return true
		}
	}

	return false
}

// resolvePathWithAncestors resolves symlinks by finding the nearest existing ancestor.
// This handles cases where the target path doesn't exist yet (e.g., creating new files).
func resolvePathWithAncestors(path string) string {
	// First try to resolve the full path
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		return realPath
	}

	// Walk up to find an existing ancestor
	current := path
	var nonExistent []string

	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Reached root
			break
		}

		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			// Found an existing ancestor, reconstruct path
			for i := len(nonExistent) - 1; i >= 0; i-- {
				realParent = filepath.Join(realParent, nonExistent[i])
			}
			return realParent
		}

		nonExistent = append(nonExistent, filepath.Base(current))
		current = parent
	}

	// Couldn't resolve, return original
	return path
}

// MarkFileRead records that a file has been read.
func (c *Config) MarkFileRead(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	c.ReadTracking[absPath] = time.Now().UnixMilli()
}

// WasFileRead checks if a file was previously read.
func (c *Config) WasFileRead(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	_, ok := c.ReadTracking[absPath]
	return ok
}

// StoreContentHash records the content hash of a file when read.
// This enables optimistic locking during edits.
func (c *Config) StoreContentHash(path string, hash string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	if c.ContentHashes == nil {
		c.ContentHashes = make(map[string]string)
	}
	c.ContentHashes[absPath] = hash
}

// GetContentHash retrieves the stored content hash for a file.
// Returns empty string if no hash was stored.
func (c *Config) GetContentHash(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return c.ContentHashes[absPath]
}

// ClearContentHash removes the stored hash for a file after successful write.
func (c *Config) ClearContentHash(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	delete(c.ContentHashes, absPath)
}

// SensitivePaths contains paths that should never be written to.
var SensitivePaths = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/hosts",
	"/.ssh/",
	"/.gnupg/",
	"/.aws/credentials",
	"/.env",
	"/id_rsa",
	"/id_ed25519",
}

// IsSensitivePath checks if a path is sensitive and should not be written.
func IsSensitivePath(path string) bool {
	lowerPath := strings.ToLower(path)
	for _, sensitive := range SensitivePaths {
		if strings.Contains(lowerPath, sensitive) {
			return true
		}
	}
	return false
}

// ResolveAndValidatePath resolves symlinks and validates the path is allowed.
func ResolveAndValidatePath(path string, config *Config) (string, error) {
	// Resolve symlinks to get real path
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If file doesn't exist yet, check parent directory
		if errors.Is(err, errors.ErrUnsupported) {
			dir := filepath.Dir(path)
			realDir, dirErr := filepath.EvalSymlinks(dir)
			if dirErr != nil {
				return "", fmt.Errorf("resolving path: %w", err)
			}
			realPath = filepath.Join(realDir, filepath.Base(path))
		} else {
			return "", fmt.Errorf("resolving path: %w", err)
		}
	}

	// Verify real path is within allowed directories
	if !config.IsPathAllowed(realPath) {
		return "", errors.New("path resolves outside allowed directories")
	}

	return realPath, nil
}

// ============================================================================
// Diff Types
// ============================================================================

// DiffParams defines parameters for the Diff tool.
type DiffParams struct {
	// FileA is the first file to compare.
	FileA string `json:"file_a"`

	// FileB is the second file to compare.
	FileB string `json:"file_b"`

	// ContextLines is the number of context lines around changes.
	ContextLines int `json:"context_lines,omitempty"`
}

// Validate checks that DiffParams are valid.
// Note: Relative paths are allowed and should be resolved by the tool against the working directory.
func (p *DiffParams) Validate() error {
	if p.FileA == "" {
		return errors.New("file_a is required")
	}
	if p.FileB == "" {
		return errors.New("file_b is required")
	}
	if p.ContextLines < 0 || p.ContextLines > MaxContextLines {
		return fmt.Errorf("context_lines must be between 0 and %d", MaxContextLines)
	}
	return nil
}

// DiffResult contains the outcome of a Diff operation.
type DiffResult struct {
	// Diff is the unified diff output.
	Diff string `json:"diff"`

	// LinesAdded is the number of lines added.
	LinesAdded int `json:"lines_added"`

	// LinesRemoved is the number of lines removed.
	LinesRemoved int `json:"lines_removed"`

	// FilesIdentical indicates if the files are the same.
	FilesIdentical bool `json:"files_identical"`
}

// ============================================================================
// Tree Types
// ============================================================================

// TreeParams defines parameters for the Tree tool.
type TreeParams struct {
	// Path is the directory to visualize.
	Path string `json:"path,omitempty"`

	// Depth is the maximum depth to traverse (default: 3, max: 10).
	Depth int `json:"depth,omitempty"`

	// ShowHidden includes dotfiles/dotdirs.
	ShowHidden bool `json:"show_hidden,omitempty"`

	// DirsOnly shows only directories, not files.
	DirsOnly bool `json:"dirs_only,omitempty"`
}

// MaxTreeDepth is the maximum allowed tree depth.
const MaxTreeDepth = 10

// Validate checks that TreeParams are valid.
func (p *TreeParams) Validate() error {
	if p.Path != "" && !filepath.IsAbs(p.Path) {
		return errors.New("path must be absolute if provided")
	}
	if p.Depth < 0 || p.Depth > MaxTreeDepth {
		return fmt.Errorf("depth must be between 0 and %d", MaxTreeDepth)
	}
	return nil
}

// TreeResult contains the outcome of a Tree operation.
type TreeResult struct {
	// Tree is the ASCII tree output.
	Tree string `json:"tree"`

	// TotalDirs is the number of directories found.
	TotalDirs int `json:"total_dirs"`

	// TotalFiles is the number of files found.
	TotalFiles int `json:"total_files"`

	// Truncated indicates if depth limit was hit.
	Truncated bool `json:"truncated"`
}

// ============================================================================
// JSON Types
// ============================================================================

// JSONParams defines parameters for the JSON tool.
type JSONParams struct {
	// FilePath is the JSON file to query/validate.
	FilePath string `json:"file_path"`

	// Query is a jq-style path (e.g., ".users[0].name").
	Query string `json:"query,omitempty"`

	// Validate only validates JSON, doesn't query.
	Validate bool `json:"validate,omitempty"`
}

// JSONParamsValidate checks that JSONParams are valid.
// Note: Relative paths are allowed and should be resolved by the tool against the working directory.
func (p *JSONParams) ValidateParams() error {
	if p.FilePath == "" {
		return errors.New("file_path is required")
	}
	return nil
}

// JSONResult contains the outcome of a JSON operation.
type JSONResult struct {
	// Value is the query result.
	Value any `json:"value,omitempty"`

	// Valid indicates if the JSON is valid.
	Valid bool `json:"valid"`

	// Error contains the parse error if invalid.
	Error string `json:"error,omitempty"`

	// Path is the file path.
	Path string `json:"path"`
}
