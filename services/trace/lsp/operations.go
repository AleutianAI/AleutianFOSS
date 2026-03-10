// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// =============================================================================
// OPERATIONS
// =============================================================================

// Operations provides high-level LSP operations.
//
// Description:
//
//	Wraps the Manager to provide convenient methods for common LSP
//	operations like go-to-definition, find-references, hover, and rename.
//	Automatically determines the language from file extensions and
//	handles server startup as needed.
//
// Thread Safety:
//
//	Safe for concurrent use.
type Operations struct {
	manager *Manager
}

// NewOperations creates an Operations instance.
//
// Inputs:
//
//	manager - The LSP manager to use for server management
//
// Outputs:
//
//	*Operations - The operations wrapper
func NewOperations(manager *Manager) *Operations {
	return &Operations{manager: manager}
}

// Manager returns the underlying manager.
func (o *Operations) Manager() *Manager {
	return o.manager
}

// =============================================================================
// RETRY CONFIGURATION
// =============================================================================

const (
	// maxRetries is the maximum number of retry attempts for transient failures.
	maxRetries = 1

	// retryDelay is the delay between retry attempts.
	retryDelay = 100 * time.Millisecond
)

// isRetryableError returns true if the error is transient and worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Retry on server crash or connection issues
	if errors.Is(err, ErrServerCrashed) || errors.Is(err, ErrServerNotRunning) {
		return true
	}
	// Retry on LSP errors that indicate server issues
	var lspErr *LSPError
	if errors.As(err, &lspErr) {
		// -32099 to -32000 are server errors
		return lspErr.Code >= -32099 && lspErr.Code <= -32000
	}
	return false
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// languageFromPath determines the language from a file path.
func (o *Operations) languageFromPath(path string) string {
	ext := filepath.Ext(path)
	lang, ok := o.manager.Configs().LanguageForExtension(ext)
	if !ok {
		return ""
	}
	return lang
}

// pathToURI converts an absolute file path to a file:// URI.
//
// Description:
//
//	Properly encodes the path for use in a file:// URI, handling special
//	characters like spaces, unicode, and other reserved characters.
func pathToURI(path string) string {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}

	// Use url.URL to properly encode the path
	u := &url.URL{
		Scheme: "file",
		Path:   path,
	}
	return u.String()
}

// URIToPath converts a file:// URI to an absolute file path.
//
// Description:
//
//	Properly decodes URL-encoded characters in the URI path.
//	Exported for use by the graph package's LSP enrichment phase.
func URIToPath(uri string) string {
	// Try to parse as URL first for proper decoding
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		return u.Path
	}
	// Fallback for simple URIs
	return strings.TrimPrefix(uri, "file://")
}

// parseLocationResponse parses a location or array of locations response.
func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}

	// Check if it's an array by looking at the first character
	if len(data) > 0 && data[0] == '[' {
		// Try array of LocationLinks first (has targetUri field)
		var links []LocationLink
		if err := json.Unmarshal(data, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
			locations := make([]Location, len(links))
			for i, link := range links {
				locations[i] = Location{
					URI:   link.TargetURI,
					Range: link.TargetSelectionRange,
				}
			}
			return locations, nil
		}

		// Try array of Locations
		var locations []Location
		if err := json.Unmarshal(data, &locations); err == nil {
			return locations, nil
		}
	}

	// Try single location
	var single Location
	if err := json.Unmarshal(data, &single); err == nil && single.URI != "" {
		return []Location{single}, nil
	}

	// Try single LocationLink
	var link LocationLink
	if err := json.Unmarshal(data, &link); err == nil && link.TargetURI != "" {
		return []Location{{URI: link.TargetURI, Range: link.TargetSelectionRange}}, nil
	}

	return nil, ErrInvalidResponse
}

// requestWithRetry performs an LSP request with retry on transient failures.
//
// Description:
//
//	Executes the request function and retries once if a transient error occurs.
//	Only idempotent operations (definition, references, hover) should use this.
func (o *Operations) requestWithRetry(
	ctx context.Context,
	language string,
	requestFn func(server *Server) (*Response, error),
) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		server, err := o.manager.GetOrSpawn(ctx, language)
		if err != nil {
			if isRetryableError(err) && attempt < maxRetries {
				slog.Debug("Retrying LSP request after server error",
					slog.String("language", language),
					slog.Int("attempt", attempt+1),
					slog.String("error", err.Error()),
				)
				time.Sleep(retryDelay)
				continue
			}
			return nil, err
		}

		resp, err := requestFn(server)
		if err != nil {
			lastErr = err
			if isRetryableError(err) && attempt < maxRetries {
				slog.Debug("Retrying LSP request after transient error",
					slog.String("language", language),
					slog.Int("attempt", attempt+1),
					slog.String("error", err.Error()),
				)
				time.Sleep(retryDelay)
				continue
			}
			return nil, err
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// =============================================================================
// DEFINITION OPERATION
// =============================================================================

// Definition returns the definition location(s) for a symbol.
//
// Description:
//
//	Sends a textDocument/definition request to the appropriate language
//	server and returns the location(s) of the symbol's definition.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number (character offset)
//
// Outputs:
//
//	[]Location - Definition location(s), may be empty if not found
//	error - Non-nil on failure
//
// Errors:
//
//	ErrUnsupportedLanguage - No LSP configuration for file extension
//	ErrServerNotInstalled - Server binary not found
//	ErrRequestTimeout - Request timed out
//
// Example:
//
//	locs, err := ops.Definition(ctx, "/project/main.go", 10, 5)
//	if err != nil {
//	    return err
//	}
//	for _, loc := range locs {
//	    fmt.Printf("Definition at %s:%d\n", URIToPath(loc.URI), loc.Range.Start.Line+1)
//	}
func (o *Operations) Definition(ctx context.Context, filePath string, line, col int) ([]Location, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return nil, fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	// Start tracing span
	ctx, span := startOperationSpan(ctx, "Definition", language, filePath)
	defer span.End()
	start := time.Now()

	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
		Position:     Position{Line: line - 1, Character: col}, // Convert to 0-indexed
	}

	// Use retry for this idempotent operation
	resp, err := o.requestWithRetry(ctx, language, func(server *Server) (*Response, error) {
		return server.Request(ctx, "textDocument/definition", params)
	})
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "definition", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("definition request: %w", err)
	}

	locations, err := parseLocationResponse(resp.Result)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "definition", language, time.Since(start), 0, false)
		return nil, err
	}

	setOperationSpanResult(span, len(locations), true)
	recordOperationMetrics(ctx, "definition", language, time.Since(start), len(locations), true)
	return locations, nil
}

// =============================================================================
// REFERENCES OPERATION
// =============================================================================

// References returns all references to a symbol.
//
// Description:
//
//	Sends a textDocument/references request to find all locations that
//	reference the symbol at the given position.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//	includeDecl - Whether to include the declaration in results
//
// Outputs:
//
//	[]Location - Reference locations, may be empty
//	error - Non-nil on failure
//
// Example:
//
//	refs, err := ops.References(ctx, "/project/main.go", 10, 5, true)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Found %d references\n", len(refs))
func (o *Operations) References(ctx context.Context, filePath string, line, col int, includeDecl bool) ([]Location, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return nil, fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	// Start tracing span
	ctx, span := startOperationSpan(ctx, "References", language, filePath)
	defer span.End()
	start := time.Now()

	params := ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
			Position:     Position{Line: line - 1, Character: col},
		},
		Context: ReferenceContext{IncludeDeclaration: includeDecl},
	}

	// Use retry for this idempotent operation
	resp, err := o.requestWithRetry(ctx, language, func(server *Server) (*Response, error) {
		return server.Request(ctx, "textDocument/references", params)
	})
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "references", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("references request: %w", err)
	}

	locations, err := parseLocationResponse(resp.Result)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "references", language, time.Since(start), 0, false)
		return nil, err
	}

	setOperationSpanResult(span, len(locations), true)
	recordOperationMetrics(ctx, "references", language, time.Since(start), len(locations), true)
	return locations, nil
}

// =============================================================================
// HOVER OPERATION
// =============================================================================

// HoverInfo contains parsed hover information.
type HoverInfo struct {
	// Content is the hover text (documentation, type info, etc.)
	Content string `json:"content"`

	// Kind is the content format ("plaintext" or "markdown").
	Kind string `json:"kind"`

	// Range is the range this hover applies to (optional).
	Range *Range `json:"range,omitempty"`
}

// Hover returns type and documentation info for a symbol.
//
// Description:
//
//	Sends a textDocument/hover request to get documentation and type
//	information for the symbol at the given position.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//
// Outputs:
//
//	*HoverInfo - Hover information, nil if no hover available
//	error - Non-nil on failure
//
// Example:
//
//	info, err := ops.Hover(ctx, "/project/main.go", 10, 5)
//	if err != nil {
//	    return err
//	}
//	if info != nil {
//	    fmt.Println(info.Content)
//	}
func (o *Operations) Hover(ctx context.Context, filePath string, line, col int) (*HoverInfo, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return nil, fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	// Start tracing span
	ctx, span := startOperationSpan(ctx, "Hover", language, filePath)
	defer span.End()
	start := time.Now()

	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
		Position:     Position{Line: line - 1, Character: col},
	}

	// Use retry for this idempotent operation
	resp, err := o.requestWithRetry(ctx, language, func(server *Server) (*Response, error) {
		return server.Request(ctx, "textDocument/hover", params)
	})
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "hover", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("hover request: %w", err)
	}

	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		setOperationSpanResult(span, 0, true)
		recordOperationMetrics(ctx, "hover", language, time.Since(start), 0, true)
		return nil, nil
	}

	var result HoverResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "hover", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("parse hover result: %w", err)
	}

	setOperationSpanResult(span, 1, true)
	recordOperationMetrics(ctx, "hover", language, time.Since(start), 1, true)
	return &HoverInfo{
		Content: result.Contents.Value,
		Kind:    result.Contents.Kind,
		Range:   result.Range,
	}, nil
}

// =============================================================================
// RENAME OPERATION
// =============================================================================

// Rename computes edits for renaming a symbol.
//
// Description:
//
//	Sends a textDocument/rename request to compute all edits needed to
//	rename the symbol at the given position. Returns the edits but does
//	NOT apply them - the caller is responsible for applying the edits.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//	newName - The new name for the symbol
//
// Outputs:
//
//	*WorkspaceEdit - Edits to apply for the rename
//	error - Non-nil on failure
//
// Example:
//
//	edit, err := ops.Rename(ctx, "/project/main.go", 10, 5, "newName")
//	if err != nil {
//	    return err
//	}
//	for uri, edits := range edit.Changes {
//	    fmt.Printf("File %s: %d edits\n", URIToPath(uri), len(edits))
//	}
func (o *Operations) Rename(ctx context.Context, filePath string, line, col int, newName string) (*WorkspaceEdit, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if newName == "" {
		return nil, fmt.Errorf("newName must not be empty")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return nil, fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	// Start tracing span
	ctx, span := startOperationSpan(ctx, "Rename", language, filePath)
	defer span.End()
	start := time.Now()

	server, err := o.manager.GetOrSpawn(ctx, language)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "rename", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("get server: %w", err)
	}

	params := RenameParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
			Position:     Position{Line: line - 1, Character: col},
		},
		NewName: newName,
	}

	resp, err := server.Request(ctx, "textDocument/rename", params)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "rename", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("rename request: %w", err)
	}

	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "rename", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("rename not supported at position")
	}

	var edit WorkspaceEdit
	if err := json.Unmarshal(resp.Result, &edit); err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "rename", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("parse rename result: %w", err)
	}

	editCount := len(edit.Changes)
	setOperationSpanResult(span, editCount, true)
	recordOperationMetrics(ctx, "rename", language, time.Since(start), editCount, true)
	return &edit, nil
}

// PrepareRename checks if rename is valid at the given position.
//
// Description:
//
//	Sends a textDocument/prepareRename request to check if the symbol
//	at the given position can be renamed and returns the range and
//	placeholder text.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//
// Outputs:
//
//	*PrepareRenameResult - The range and placeholder, nil if not renameable
//	error - Non-nil on failure
func (o *Operations) PrepareRename(ctx context.Context, filePath string, line, col int) (*PrepareRenameResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return nil, fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	server, err := o.manager.GetOrSpawn(ctx, language)
	if err != nil {
		return nil, fmt.Errorf("get server: %w", err)
	}

	params := PrepareRenameParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
			Position:     Position{Line: line - 1, Character: col},
		},
	}

	resp, err := server.Request(ctx, "textDocument/prepareRename", params)
	if err != nil {
		return nil, fmt.Errorf("prepareRename request: %w", err)
	}

	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return nil, nil
	}

	// Try parsing as PrepareRenameResult first
	var result PrepareRenameResult
	if err := json.Unmarshal(resp.Result, &result); err == nil && result.Placeholder != "" {
		return &result, nil
	}

	// Some servers return just a Range
	var r Range
	if err := json.Unmarshal(resp.Result, &r); err == nil {
		return &PrepareRenameResult{Range: r}, nil
	}

	return nil, nil
}

// =============================================================================
// WORKSPACE SYMBOL OPERATION
// =============================================================================

// WorkspaceSymbol finds symbols matching a query in the workspace.
//
// Description:
//
//	Sends a workspace/symbol request to find symbols matching the query
//	across the entire workspace.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	language - The language to query (determines which server to use)
//	query - The symbol query (empty string returns all symbols)
//
// Outputs:
//
//	[]SymbolInformation - Matching symbols
//	error - Non-nil on failure
//
// Example:
//
//	symbols, err := ops.WorkspaceSymbol(ctx, "go", "Handler")
//	if err != nil {
//	    return err
//	}
//	for _, sym := range symbols {
//	    fmt.Printf("%s (%s)\n", sym.Name, sym.Location.URI)
//	}
func (o *Operations) WorkspaceSymbol(ctx context.Context, language, query string) ([]SymbolInformation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	// Start tracing span
	ctx, span := startOperationSpan(ctx, "WorkspaceSymbol", language, "")
	defer span.End()
	start := time.Now()

	server, err := o.manager.GetOrSpawn(ctx, language)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "workspace_symbol", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("get server: %w", err)
	}

	params := WorkspaceSymbolParams{Query: query}

	resp, err := server.Request(ctx, "workspace/symbol", params)
	if err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "workspace_symbol", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("symbol request: %w", err)
	}

	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		setOperationSpanResult(span, 0, true)
		recordOperationMetrics(ctx, "workspace_symbol", language, time.Since(start), 0, true)
		return nil, nil
	}

	var symbols []SymbolInformation
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "workspace_symbol", language, time.Since(start), 0, false)
		return nil, fmt.Errorf("parse symbol result: %w", err)
	}

	setOperationSpanResult(span, len(symbols), true)
	recordOperationMetrics(ctx, "workspace_symbol", language, time.Since(start), len(symbols), true)
	return symbols, nil
}

// =============================================================================
// DOCUMENT NOTIFICATION OPERATIONS
// =============================================================================

// OpenDocument notifies the server that a document was opened.
//
// Description:
//
//	Sends a textDocument/didOpen notification. This is required before
//	most LSP operations will work on a file.
//
// Inputs:
//
//	filePath - Absolute path to the file
//	content - The file content
//
// Outputs:
//
//	error - Non-nil on failure
func (o *Operations) OpenDocument(ctx context.Context, filePath, content string) error {
	if ctx == nil {
		return fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	server, err := o.manager.GetOrSpawn(ctx, language)
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}

	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        pathToURI(filePath),
			LanguageID: language,
			Version:    1,
			Text:       content,
		},
	}

	return server.Notify("textDocument/didOpen", params)
}

// CloseDocument notifies the server that a document was closed.
//
// Description:
//
//	Sends a textDocument/didClose notification.
//
// Inputs:
//
//	filePath - Absolute path to the file
//
// Outputs:
//
//	error - Non-nil on failure
func (o *Operations) CloseDocument(ctx context.Context, filePath string) error {
	if ctx == nil {
		return fmt.Errorf("ctx must not be nil")
	}

	language := o.languageFromPath(filePath)
	if language == "" {
		return fmt.Errorf("%w: no language for %s", ErrUnsupportedLanguage, filepath.Ext(filePath))
	}

	server := o.manager.Get(language)
	if server == nil {
		// Server not running, nothing to close
		return nil
	}

	params := DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: pathToURI(filePath)},
	}

	return server.Notify("textDocument/didClose", params)
}

// =============================================================================
// UTILITY FUNCTIONS
// =============================================================================

// IsAvailable checks if LSP operations are available for a file.
//
// Description:
//
//	Checks if the file extension is supported and the server binary
//	is installed.
//
// Inputs:
//
//	filePath - Path to check (only extension is used)
//
// Outputs:
//
//	bool - True if LSP operations are available
func (o *Operations) IsAvailable(filePath string) bool {
	language := o.languageFromPath(filePath)
	if language == "" {
		return false
	}
	return o.manager.IsAvailable(language)
}

// URIToPath converts a file:// URI to a file path.
//
// Description:
//
//	Convenience method for converting LSP URIs to file paths.
func (o *Operations) URIToPath(uri string) string {
	return URIToPath(uri)
}

// PathToURI converts a file path to a file:// URI.
//
// Description:
//
//	Convenience method for converting file paths to LSP URIs.
func (o *Operations) PathToURI(path string) string {
	return pathToURI(path)
}

// =============================================================================
// WORKSPACE EDIT HELPERS
// =============================================================================

// WorkspaceEditSummary provides a human-readable summary of a workspace edit.
type WorkspaceEditSummary struct {
	// FileCount is the number of files affected by the edit.
	FileCount int

	// TotalEdits is the total number of text edits across all files.
	TotalEdits int

	// Files is a map from file path to the number of edits in that file.
	Files map[string]int
}

// SummarizeWorkspaceEdit creates a summary of a workspace edit.
//
// Description:
//
//	Analyzes a WorkspaceEdit returned by LSP Rename and produces a summary
//	showing which files are affected and how many edits each will receive.
//	This is useful for presenting rename previews to users.
//
// Inputs:
//
//	edit - The workspace edit to summarize
//
// Outputs:
//
//	WorkspaceEditSummary - Summary of the edit
//
// Example:
//
//	edit, err := ops.Rename(ctx, "/project/main.go", 10, 5, "newName")
//	if err != nil {
//	    return err
//	}
//	summary := ops.SummarizeWorkspaceEdit(edit)
//	fmt.Printf("Rename will affect %d files with %d total edits\n",
//	    summary.FileCount, summary.TotalEdits)
func (o *Operations) SummarizeWorkspaceEdit(edit *WorkspaceEdit) WorkspaceEditSummary {
	summary := WorkspaceEditSummary{
		Files: make(map[string]int),
	}

	if edit == nil {
		return summary
	}

	for uri, edits := range edit.Changes {
		filePath := URIToPath(uri)
		editCount := len(edits)
		summary.Files[filePath] = editCount
		summary.TotalEdits += editCount
	}

	// Also count document changes if present
	for _, docChange := range edit.DocumentChanges {
		filePath := URIToPath(docChange.TextDocument.URI)
		editCount := len(docChange.Edits)
		if _, exists := summary.Files[filePath]; !exists {
			summary.Files[filePath] = editCount
			summary.TotalEdits += editCount
		}
	}

	summary.FileCount = len(summary.Files)
	return summary
}

// ValidateWorkspaceEdit checks if a workspace edit can be safely applied.
//
// Description:
//
//	Performs basic validation on a workspace edit to ensure it references
//	valid files and has reasonable edit ranges. This does NOT check if the
//	files actually exist or are writable.
//
// Important:
//
//	The Rename operation returns edits but does NOT apply them. The caller
//	is responsible for:
//	  1. Reviewing the edits with the user
//	  2. Creating backups if needed
//	  3. Applying the edits atomically
//	  4. Handling any conflicts or errors
//
// Inputs:
//
//	edit - The workspace edit to validate
//
// Outputs:
//
//	error - Non-nil if the edit has obvious problems
func (o *Operations) ValidateWorkspaceEdit(edit *WorkspaceEdit) error {
	if edit == nil {
		return fmt.Errorf("workspace edit is nil")
	}

	if len(edit.Changes) == 0 && len(edit.DocumentChanges) == 0 {
		return fmt.Errorf("workspace edit has no changes")
	}

	// Validate Changes
	for uri, edits := range edit.Changes {
		if !strings.HasPrefix(uri, "file://") {
			return fmt.Errorf("invalid URI scheme: %s", uri)
		}
		for i, e := range edits {
			if e.Range.Start.Line < 0 || e.Range.Start.Character < 0 {
				return fmt.Errorf("invalid range in edit %d for %s: negative position", i, uri)
			}
			if e.Range.End.Line < e.Range.Start.Line {
				return fmt.Errorf("invalid range in edit %d for %s: end before start", i, uri)
			}
		}
	}

	// Validate DocumentChanges
	for _, docChange := range edit.DocumentChanges {
		uri := docChange.TextDocument.URI
		if !strings.HasPrefix(uri, "file://") {
			return fmt.Errorf("invalid URI scheme: %s", uri)
		}
		for i, e := range docChange.Edits {
			if e.Range.Start.Line < 0 || e.Range.Start.Character < 0 {
				return fmt.Errorf("invalid range in edit %d for %s: negative position", i, uri)
			}
		}
	}

	return nil
}
