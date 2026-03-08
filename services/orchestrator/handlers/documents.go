// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/ttl"
	"github.com/gin-gonic/gin"
	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/textsplitter"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var (
	CHUNK_SIZE        = 1000
	CHUNK_OVERLAP     = int(float64(CHUNK_SIZE) * 0.10) // Chunk_overlap is 10% of the CHUNK_SIZE
	defaultSeparators = []string{"\n\n", "\n", " ", ""}
	pythonSeparators  = []string{"\nclass ", "\ndef ", "\n\t", "\n", " "}
	cStyleSeparators  = []string{
		"\nfunction ", "\nclass ", "\ninterface ",
		"\npublic ", "\nprivate ", "\nprotected ",
		"\nfunc", "\ntype",
		"\n\n", "\n", " ", "",
	}

	markdownSeparators = []string{
		"\n# ", "\n## ", "\n### ", "\n#### ", "\n##### ", "\n###### ",
		"\n\n", "\n", " ", "",
	}
)

// IngestDocumentRequest represents a request to ingest a document into Weaviate.
//
// # Description
//
// Contains the document content and metadata required for ingestion. The TTL
// field is optional - if not provided or empty, the document will not expire.
//
// # Fields
//
//   - Content: The document content to be chunked and embedded.
//   - Source: The source path/identifier for this document.
//   - DataSpace: Logical namespace for document segmentation.
//   - VersionTag: Version label for this ingestion (auto-generated if empty).
//   - SessionUUID: If set, links document chunks to a session via cross-reference.
//   - TTL: Optional TTL duration string (e.g., "90d", "P30D"). Empty = no expiration.
//   - KeepVersions: Number of versions to retain (0 = keep all). Deletes oldest after ingestion.
type IngestDocumentRequest struct {
	Content      string `json:"content"`
	Source       string `json:"source"`
	DataSpace    string `json:"data_space"`
	VersionTag   string `json:"version_tag"`
	SessionUUID  string `json:"session_id"`
	TTL          string `json:"ttl,omitempty"`
	KeepVersions int    `json:"keep_versions,omitempty"`
}

type IngestedDocument struct {
	ParentSource string `json:"parent_source"`
	ChunkCount   int    `json:"chunk_count"`
	DataSpace    string `json:"data_space"`
	VersionTag   string `json:"version_tag"`
	IngestedAt   int64  `json:"ingested_at"`
}

// OllamaEmbedRequest is the request format for Ollama's /api/embed endpoint.
type OllamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// OllamaEmbedResponse is the response format from Ollama's /api/embed endpoint.
type OllamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// NomicDocumentPrefix is prepended to documents for nomic-embed-text models.
// This helps the model distinguish between queries and documents.
const NomicDocumentPrefix = "search_document: "

type gqlAggregateResponse struct {
	Aggregate struct {
		Document []struct {
			GroupedBy struct {
				Value string `json:"value"`
			} `json:"groupedBy"`
			Meta struct {
				Count float64 `json:"count"`
			} `json:"meta"`
			DataSpace struct {
				TopOccurrences []struct {
					Value string `json:"value"`
				} `json:"topOccurrences"`
			} `json:"data_space"`
			VersionTag struct {
				TopOccurrences []struct {
					Value string `json:"value"`
				} `json:"topOccurrences"`
			} `json:"version_tag"`
			IngestedAt struct {
				Maximum float64 `json:"maximum"`
			} `json:"ingested_at"`
		} `json:"Document"`
	} `json:"Aggregate"`
}

// CreateDocument receives data from the CLI and adds it to Weaviate
// This is now a thin wrapper around RunIngestion
func CreateDocument(client *weaviate.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req IngestDocumentRequest
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		chunksCreated, err := RunIngestion(c.Request.Context(), client, req)
		if err != nil {
			slog.Error("Ingestion failed", "source", req.Source, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		slog.Info("Successfully processed document via API", "source", req.Source, "chunks_processed", chunksCreated)
		c.JSON(http.StatusCreated, gin.H{
			"status":           "success",
			"source":           req.Source,
			"chunks_processed": chunksCreated,
		})
	}
}

// ListDocuments gets a unique list of all ingested 'parent_source' files
func ListDocuments(client *weaviate.Client) gin.HandlerFunc {
	tracer := otel.Tracer("aleutian.orchestrator.handlers")
	return func(c *gin.Context) {
		ctx, span := tracer.Start(c.Request.Context(), "ListDocuments.handler")
		defer span.End()
		slog.Info("Received request to list ingested documents")
		globalDocsFilter := filters.Where().
			WithPath([]string{"inSession"}).
			WithOperator(filters.IsNull).
			WithValueBoolean(true)

		resp, err := client.GraphQL().Aggregate().
			WithClassName("Document").
			WithWhere(globalDocsFilter).
			WithGroupBy("parent_source").
			WithFields(
				graphql.Field{
					Name: "meta",
					Fields: []graphql.Field{
						{Name: "count"},
					},
				},
				graphql.Field{
					Name: "data_space",
					Fields: []graphql.Field{
						{
							Name:   "topOccurrences(limit: 1)", // <-- Correct v5 syntax
							Fields: []graphql.Field{{Name: "value"}},
						},
					},
				},
				graphql.Field{
					Name: "version_tag",
					Fields: []graphql.Field{
						{
							Name:   "topOccurrences(limit: 1)", // <-- Correct v5 syntax
							Fields: []graphql.Field{{Name: "value"}},
						},
					},
				},
				graphql.Field{
					Name: "ingested_at",
					Fields: []graphql.Field{
						{
							Name: "maximum",
						},
					},
				},
			).
			Do(ctx)
		if err != nil {
			slog.Error("Failed to aggregate documents from Weaviate", "error", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query documents"})
			return
		}

		// Marshal the raw response data back to JSON
		rawRespData, err := json.Marshal(resp.Data)
		if err != nil {
			slog.Error("Failed to re-marshal weaviate response", "error", err)
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response data"})
			return
		}

		// Unmarshal the JSON bytes into our strongly-typed struct
		var parsedResp gqlAggregateResponse
		if err := json.Unmarshal(rawRespData, &parsedResp); err != nil {
			slog.Error("Failed to unmarshal weaviate response into struct", "error", err, "raw_data", string(rawRespData))
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmarshal response"})
			return
		}

		// Build the final docList from the struct
		docList := make([]IngestedDocument, 0)
		getStringValue := func(occurrences []struct {
			Value string `json:"value"`
		}) string {
			if len(occurrences) > 0 {
				return occurrences[0].Value
			}
			return ""
		}
		for _, group := range parsedResp.Aggregate.Document {
			// Filter out the RAG memory chunks from the UI list
			versionTag := getStringValue(group.VersionTag.TopOccurrences)
			if versionTag != "chat_memory" {
				docList = append(docList, IngestedDocument{
					ParentSource: group.GroupedBy.Value,
					ChunkCount:   int(group.Meta.Count),
					DataSpace:    getStringValue(group.DataSpace.TopOccurrences),
					VersionTag:   versionTag,
					IngestedAt:   int64(group.IngestedAt.Maximum),
				})
			}
		}
		slog.Info("Successfully fetched document list", "count", len(docList))
		c.JSON(http.StatusOK, gin.H{"documents": docList})
	}
}

// DocVersionInfo represents version metadata for a single document version.
type DocVersionInfo struct {
	VersionTag    string `json:"version_tag"`
	VersionNumber int    `json:"version_number"`
	ChunkCount    int    `json:"chunk_count"`
	IngestedAt    int64  `json:"ingested_at"`
	IsCurrent     bool   `json:"is_current"`
}

// GetDocumentVersions returns the version history for a specific document.
//
// # Description
//
// Queries Weaviate for all versions of a document identified by parent_source.
// Returns version metadata including version tags, timestamps, and current status.
//
// # Inputs
//
//   - source: Query parameter specifying the parent_source to look up
//
// # Outputs
//
//   - 200 OK: JSON with parent_source and array of version info
//   - 400 Bad Request: Missing source parameter
//   - 404 Not Found: No versions found for the document
//
// # Example
//
//	GET /v1/document/versions?source=report.md
func GetDocumentVersions(client *weaviate.Client) gin.HandlerFunc {
	tracer := otel.Tracer("aleutian.orchestrator.handlers")
	return func(c *gin.Context) {
		ctx, span := tracer.Start(c.Request.Context(), "GetDocumentVersions.handler")
		defer span.End()

		source := c.Query("source")
		if source == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source parameter is required"})
			return
		}

		slog.Info("Fetching document versions", "source", source)

		// Query for all versions of this document grouped by version_number
		where := filters.Where().
			WithPath([]string{"parent_source"}).
			WithOperator(filters.Equal).
			WithValueText(source)

		resp, err := client.GraphQL().Aggregate().
			WithClassName("Document").
			WithWhere(where).
			WithGroupBy("version_number").
			WithFields(
				graphql.Field{
					Name: "meta",
					Fields: []graphql.Field{
						{Name: "count"},
					},
				},
				graphql.Field{
					Name: "version_tag",
					Fields: []graphql.Field{
						{Name: "topOccurrences", Fields: []graphql.Field{{Name: "value"}}},
					},
				},
				graphql.Field{
					Name: "is_current",
					Fields: []graphql.Field{
						{Name: "totalTrue"},
					},
				},
				graphql.Field{
					Name: "ingested_at",
					Fields: []graphql.Field{
						{Name: "maximum"},
					},
				},
			).
			Do(ctx)

		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			slog.Error("Failed to query document versions", "source", source, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Parse aggregation response
		type aggregateResponse struct {
			Aggregate struct {
				Document []struct {
					GroupedBy struct {
						Value int `json:"value"`
					} `json:"groupedBy"`
					Meta struct {
						Count float64 `json:"count"`
					} `json:"meta"`
					VersionTag struct {
						TopOccurrences []struct {
							Value string `json:"value"`
						} `json:"topOccurrences"`
					} `json:"version_tag"`
					IsCurrent struct {
						TotalTrue float64 `json:"totalTrue"`
					} `json:"is_current"`
					IngestedAt struct {
						Maximum float64 `json:"maximum"`
					} `json:"ingested_at"`
				} `json:"Document"`
			} `json:"Aggregate"`
		}

		jsonBytes, err := json.Marshal(resp.Data)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse response"})
			return
		}

		var aggResp aggregateResponse
		if err := json.Unmarshal(jsonBytes, &aggResp); err != nil {
			span.SetStatus(codes.Error, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode response"})
			return
		}

		if len(aggResp.Aggregate.Document) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "no versions found for document"})
			return
		}

		// Build version list
		versions := make([]DocVersionInfo, 0, len(aggResp.Aggregate.Document))
		for _, doc := range aggResp.Aggregate.Document {
			versionTag := ""
			if len(doc.VersionTag.TopOccurrences) > 0 {
				versionTag = doc.VersionTag.TopOccurrences[0].Value
			}
			versions = append(versions, DocVersionInfo{
				VersionTag:    versionTag,
				VersionNumber: doc.GroupedBy.Value,
				ChunkCount:    int(doc.Meta.Count),
				IngestedAt:    int64(doc.IngestedAt.Maximum),
				IsCurrent:     doc.IsCurrent.TotalTrue > 0,
			})
		}

		slog.Info("Found document versions", "source", source, "version_count", len(versions))
		c.JSON(http.StatusOK, gin.H{
			"parent_source": source,
			"versions":      versions,
		})
	}
}

// getMaxVersionForParentSource queries Weaviate for the highest version_number
// of documents with the given parent_source and data_space.
//
// # Description
//
// Used during ingestion to determine the next version number for a document.
// Returns 0 if no existing versions are found.
//
// # Inputs
//
//   - ctx: Context for the query (supports cancellation/timeout)
//   - client: Weaviate client instance
//   - parentSource: The parent_source field to filter on
//   - dataSpace: The data_space field to filter on
//
// # Outputs
//
//   - int: The maximum version_number found, or 0 if none exist
//   - error: Any error encountered during the query
//
// # Assumptions
//
//   - The Document class exists in Weaviate with version_number property
//   - version_number is indexed for efficient aggregation
func getMaxVersionForParentSource(ctx context.Context, client *weaviate.Client, parentSource, dataSpace string) (int, error) {
	// Build filter for parent_source AND data_space
	where := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{
			filters.Where().
				WithPath([]string{"parent_source"}).
				WithOperator(filters.Equal).
				WithValueText(parentSource),
			filters.Where().
				WithPath([]string{"data_space"}).
				WithOperator(filters.Equal).
				WithValueText(dataSpace),
		})

	// Query for max version_number using aggregate
	result, err := client.GraphQL().Aggregate().
		WithClassName("Document").
		WithWhere(where).
		WithFields(graphql.Field{
			Name: "version_number",
			Fields: []graphql.Field{
				{Name: "maximum"},
			},
		}).
		Do(ctx)

	if err != nil {
		return 0, fmt.Errorf("failed to query max version: %w", err)
	}

	// Parse the aggregate result
	if result.Data == nil {
		return 0, nil
	}

	aggregate, ok := result.Data["Aggregate"].(map[string]interface{})
	if !ok {
		return 0, nil
	}

	docs, ok := aggregate["Document"].([]interface{})
	if !ok || len(docs) == 0 {
		return 0, nil
	}

	doc, ok := docs[0].(map[string]interface{})
	if !ok {
		return 0, nil
	}

	versionField, ok := doc["version_number"].(map[string]interface{})
	if !ok {
		return 0, nil
	}

	maxVersion, ok := versionField["maximum"].(float64)
	if !ok {
		return 0, nil
	}

	return int(maxVersion), nil
}

// cleanupOldVersions deletes document chunks from versions beyond the retention limit.
//
// # Description
//
// After a new version is ingested, this function removes old versions to enforce
// the keep_versions retention policy. Versions are deleted from oldest to newest
// until only keepVersions remain.
//
// # Inputs
//
//   - ctx: Context for the operation (supports cancellation/timeout)
//   - client: Weaviate client instance
//   - parentSource: The parent_source field to filter on
//   - dataSpace: The data_space field to filter on
//   - keepVersions: Number of versions to retain (must be > 0)
//   - currentVersion: The version number just ingested
//
// # Outputs
//
//   - int: Number of chunks deleted across all removed versions
//   - error: Any error encountered during deletion
//
// # Audit
//
// Logs deletion events with operation type "delete_old_version" for compliance.
//
// # Limitations
//
//   - Uses batch delete which may be slow for documents with many chunks
//   - Does not verify deletion success for individual chunks
func cleanupOldVersions(ctx context.Context, client *weaviate.Client, parentSource, dataSpace string, keepVersions, currentVersion int) (int, error) {
	// Calculate which versions to delete
	// If current is v5 and keep=3, delete versions 1 and 2 (keep 3, 4, 5)
	oldestToKeep := currentVersion - keepVersions + 1
	if oldestToKeep <= 0 {
		return 0, nil // Nothing to delete
	}

	slog.Info("Version cleanup starting",
		"parent_source", parentSource,
		"data_space", dataSpace,
		"current_version", currentVersion,
		"keep_versions", keepVersions,
		"deleting_versions_below", oldestToKeep,
	)

	// Build filter: parent_source AND data_space AND version_number < oldestToKeep
	where := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{
			filters.Where().
				WithPath([]string{"parent_source"}).
				WithOperator(filters.Equal).
				WithValueText(parentSource),
			filters.Where().
				WithPath([]string{"data_space"}).
				WithOperator(filters.Equal).
				WithValueText(dataSpace),
			filters.Where().
				WithPath([]string{"version_number"}).
				WithOperator(filters.LessThan).
				WithValueInt(int64(oldestToKeep)),
		})

	// First, get all document IDs to delete
	result, err := client.GraphQL().Get().
		WithClassName("Document").
		WithWhere(where).
		WithFields(
			graphql.Field{Name: "_additional { id }"},
			graphql.Field{Name: "version_number"},
		).
		Do(ctx)

	if err != nil {
		return 0, fmt.Errorf("failed to query old versions: %w", err)
	}

	if result.Data == nil {
		return 0, nil
	}

	getResult, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return 0, nil
	}

	docs, ok := getResult["Document"].([]interface{})
	if !ok || len(docs) == 0 {
		return 0, nil
	}

	// Collect IDs to delete
	var idsToDelete []string
	versionsDeleted := make(map[int]bool)
	for _, doc := range docs {
		docMap, ok := doc.(map[string]interface{})
		if !ok {
			continue
		}
		additional, ok := docMap["_additional"].(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := additional["id"].(string)
		if !ok {
			continue
		}
		idsToDelete = append(idsToDelete, id)

		// Track which versions are being deleted for audit
		if versionNum, ok := docMap["version_number"].(float64); ok {
			versionsDeleted[int(versionNum)] = true
		}
	}

	if len(idsToDelete) == 0 {
		return 0, nil
	}

	// Delete the chunks
	deletedCount := 0
	for _, id := range idsToDelete {
		err := client.Data().Deleter().
			WithClassName("Document").
			WithID(id).
			Do(ctx)

		if err != nil {
			slog.Warn("Failed to delete old version chunk", "id", id, "error", err)
			continue
		}
		deletedCount++
	}

	// Audit log for compliance
	versionsList := make([]int, 0, len(versionsDeleted))
	for v := range versionsDeleted {
		versionsList = append(versionsList, v)
	}
	slog.Info("version.cleanup.complete",
		"operation", "delete_old_version",
		"parent_source", parentSource,
		"data_space", dataSpace,
		"versions_removed", versionsList,
		"chunks_deleted", deletedCount,
		"keep_versions", keepVersions,
	)

	return deletedCount, nil
}

// markOldVersionsNotCurrent updates all existing document chunks with the given
// parent_source and data_space to have is_current = false.
//
// # Description
//
// Called before inserting a new version to mark previous versions as non-current.
// This ensures only the latest version is returned in default queries.
//
// # Inputs
//
//   - ctx: Context for the operation (supports cancellation/timeout)
//   - client: Weaviate client instance
//   - parentSource: The parent_source field to filter on
//   - dataSpace: The data_space field to filter on
//
// # Outputs
//
//   - int: Number of objects updated
//   - error: Any error encountered during the update
//
// # Limitations
//
//   - Uses batch update which may be slow for documents with many chunks
func markOldVersionsNotCurrent(ctx context.Context, client *weaviate.Client, parentSource, dataSpace string) (int, error) {
	// First, find all current document IDs
	where := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{
			filters.Where().
				WithPath([]string{"parent_source"}).
				WithOperator(filters.Equal).
				WithValueText(parentSource),
			filters.Where().
				WithPath([]string{"data_space"}).
				WithOperator(filters.Equal).
				WithValueText(dataSpace),
			filters.Where().
				WithPath([]string{"is_current"}).
				WithOperator(filters.Equal).
				WithValueBoolean(true),
		})

	result, err := client.GraphQL().Get().
		WithClassName("Document").
		WithWhere(where).
		WithFields(graphql.Field{Name: "_additional { id }"},
			graphql.Field{Name: "source"}).
		Do(ctx)

	if err != nil {
		return 0, fmt.Errorf("failed to query existing documents: %w", err)
	}

	if result.Data == nil {
		return 0, nil
	}

	getResult, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return 0, nil
	}

	docs, ok := getResult["Document"].([]interface{})
	if !ok || len(docs) == 0 {
		return 0, nil
	}

	// Update each document to set is_current = false
	updatedCount := 0
	for _, doc := range docs {
		docMap, ok := doc.(map[string]interface{})
		if !ok {
			continue
		}
		additional, ok := docMap["_additional"].(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := additional["id"].(string)
		if !ok {
			continue
		}

		err := client.Data().Updater().
			WithClassName("Document").
			WithID(id).
			WithMerge().
			WithProperties(map[string]interface{}{
				"is_current": false,
			}).
			Do(ctx)

		if err != nil {
			slog.Warn("Failed to update is_current for document", "id", id, "error", err)
			continue
		}
		updatedCount++
	}

	slog.Info("Marked old versions as not current", "parent_source", parentSource, "count", updatedCount)
	return updatedCount, nil
}

// RunIngestion is the refactored, reusable logic for ingesting a document.
func RunIngestion(ctx context.Context, client *weaviate.Client, req IngestDocumentRequest) (int, error) {
	embeddingServiceURL := os.Getenv("EMBEDDING_SERVICE_URL")
	if embeddingServiceURL == "" {
		slog.Error("EMBEDDING_SERVICE_URL not set for orchestrator")
		return 0, fmt.Errorf("Embedding service not configured")
	}
	embeddingModel := os.Getenv("EMBEDDING_MODEL")
	if embeddingModel == "" {
		embeddingModel = "nomic-embed-text-v2-moe"
	}
	slog.Info("Ingestion request received", "source", req.Source, "embedding_model", embeddingModel)

	// --- GET SPLITTER ---
	splitter := getSplitterForFile(req.Source)

	// --- SPLIT TEXT ---
	chunks, err := splitter.SplitText(req.Content)
	if err != nil {
		slog.Error("Failed to split text", "source", req.Source, "error", err)
		return 0, fmt.Errorf("Failed to split content: %w", err)
	}
	if len(chunks) == 0 {
		slog.Warn("No chunks produced after splitting", "source", req.Source)
		return 0, nil
	}
	slog.Info("Split document into chunks", "source", req.Source, "chunk_count", len(chunks))

	// --- VERSION MANAGEMENT ---
	// Get the current max version for this document
	maxVersion, err := getMaxVersionForParentSource(ctx, client, req.Source, req.DataSpace)
	if err != nil {
		slog.Warn("Failed to get max version, assuming first version", "error", err)
		maxVersion = 0
	}
	newVersion := maxVersion + 1
	versionTag := fmt.Sprintf("v%d", newVersion)
	slog.Info("Document versioning", "parent_source", req.Source, "previous_version", maxVersion, "new_version", newVersion)

	// Mark old versions as not current (if any exist)
	if maxVersion > 0 {
		updatedCount, err := markOldVersionsNotCurrent(ctx, client, req.Source, req.DataSpace)
		if err != nil {
			slog.Warn("Failed to mark old versions as not current", "error", err)
			// Continue anyway - this is not fatal
		} else {
			slog.Info("Updated old version chunks", "count", updatedCount)
		}
	}

	vectors, err := callOllamaEmbed(embeddingServiceURL, embeddingModel, chunks)
	if err != nil {
		slog.Error("Failed to get batch embeddings", "source", req.Source, "error", err)
		return 0, err
	}
	if len(vectors) != len(chunks) {
		slog.Error("Mismatch between chunk count and vector count", "chunks", len(chunks), "vectors", len(vectors))
		return 0, fmt.Errorf("embedding service returned mismatched vector count")
	}
	slog.Info("Successfully generated batch embeddings", "source", req.Source, "vector_count", len(vectors))

	// --- TTL EXPIRATION CALCULATION ---
	// Parse TTL string and calculate expiration timestamp. Zero = no expiration.
	var ttlExpiresAt int64
	if req.TTL != "" {
		ttlResult, ttlErr := ttl.ParseTTLDuration(req.TTL)
		if ttlErr != nil {
			slog.Error("Failed to parse TTL duration", "ttl", req.TTL, "error", ttlErr)
			return 0, fmt.Errorf("invalid TTL format '%s': %w", req.TTL, ttlErr)
		}
		ttlExpiresAt = ttlResult.ExpiresAt
		slog.Info("Document TTL configured",
			"source", req.Source,
			"ttl_input", req.TTL,
			"ttl_format", ttlResult.Format.String(),
			"ttl_description", ttlResult.Description,
			"expires_at", time.UnixMilli(ttlExpiresAt).Format(time.RFC3339),
		)
	}

	// --- Batch Weaviate Import in one request ---
	batcher := client.Batch().ObjectsBatcher()
	objects := make([]*models.Object, len(chunks))
	ingestedAt := time.Now().UnixMilli()

	// Build objects with version-aware UUIDs
	for i, chunk := range chunks {
		chunkSource := fmt.Sprintf("%s_part_%d", req.Source, i+1)
		// Include version in hash to ensure each version has unique UUIDs
		hashInput := fmt.Sprintf("%s:%s:%d", chunk, req.Source, newVersion)
		hash := sha256.Sum256([]byte(hashInput))
		docUUID, _ := uuid.FromBytes(hash[:16])
		docId := docUUID.String()
		properties := map[string]interface{}{
			"content":        chunk,
			"source":         chunkSource,
			"parent_source":  req.Source,
			"data_space":     req.DataSpace,
			"version_tag":    versionTag,
			"version_number": newVersion,
			"is_current":     true,
			"ingested_at":    ingestedAt,
			"ttl_expires_at": ttlExpiresAt,
		}
		if req.SessionUUID != "" {
			beacon := map[string]interface{}{
				"beacon": fmt.Sprintf("weaviate://localhost/Session/%s", req.SessionUUID),
			}
			properties["inSession"] = []map[string]interface{}{beacon}
		}

		objects[i] = &models.Object{
			Class:      "Document",
			ID:         strfmt.UUID(docId),
			Vector:     vectors[i],
			Properties: properties,
		}

	}

	batcher.WithObjects(objects...)

	resp, err := batcher.Do(ctx)
	if err != nil {
		slog.Error("Failed to perform batch import to Weaviate", "error", err)
		return 0, fmt.Errorf("failed to save objects to Weaviate: %w", err)
	}

	chunksCreated := 0
	hasErrors := false
	if resp != nil {
		for _, item := range resp {
			if item.Result != nil && item.Result.Status != nil && *item.Result.Status == "SUCCESS" {
				chunksCreated++
			} else {
				hasErrors = true
				if item.Result != nil && item.Result.Errors != nil && len(item.Result.Errors.Error) > 0 {
					for _, errItem := range item.Result.Errors.Error {
						slog.Warn("Error in Weaviate batch item", "source", req.Source, "error", errItem.Message)
					}
				} else {
					var status string
					if item.Result != nil && item.Result.Status != nil {
						status = *item.Result.Status
					} else {
						status = "UNKNOWN"
					}
					slog.Warn("Failed Weaviate batch item, no error provided", "source", req.Source, "status", status)
				}
			}
		}
	}

	if hasErrors {
		slog.Warn("Errors encountered during Weaviate batch import", "source", req.Source, "successful_chunks", chunksCreated)
	}

	slog.Info("Successfully processed document", "source", req.Source, "chunks_processed",
		chunksCreated)

	// --- VERSION CLEANUP ---
	// If KeepVersions is set, delete old versions beyond the retention limit
	if req.KeepVersions > 0 && newVersion > req.KeepVersions {
		deletedCount, deleteErr := cleanupOldVersions(ctx, client, req.Source, req.DataSpace, req.KeepVersions, newVersion)
		if deleteErr != nil {
			slog.Warn("Failed to cleanup old versions", "source", req.Source, "error", deleteErr)
			// Don't fail the request, just log the warning
		} else if deletedCount > 0 {
			slog.Info("Cleaned up old document versions",
				"source", req.Source,
				"versions_deleted", deletedCount,
				"keep_versions", req.KeepVersions,
			)
		}
	}

	return chunksCreated, nil
}

// callOllamaEmbed calls Ollama's /api/embed endpoint with document prefixes.
// For nomic-embed-text models, documents must be prefixed with "search_document: "
// to distinguish them from queries (which use "search_query: ").
func callOllamaEmbed(embedURL string, model string, chunks []string) ([][]float32, error) {
	// Add document prefix for nomic models
	prefixedChunks := make([]string, len(chunks))
	for i, chunk := range chunks {
		prefixedChunks[i] = NomicDocumentPrefix + chunk
	}

	reqBody := OllamaEmbedRequest{
		Model: model,
		Input: prefixedChunks,
	}
	return callOllamaEmbedInternal(embedURL, reqBody)
}

// callOllamaEmbedInternal sends a pre-built OllamaEmbedRequest to the Ollama
// /api/embed endpoint and returns the resulting embedding vectors.
//
// Description:
//
//	Shared implementation used by both callOllamaEmbed (document ingestion)
//	and callOllamaEmbedRaw (HandleEmbed endpoint). The caller is responsible
//	for prefixing inputs before calling this function.
//
// Inputs:
//
//	embedURL - The Ollama /api/embed endpoint URL.
//	reqBody - The embed request with model and input texts.
//
// Outputs:
//
//	[][]float32 - One embedding vector per input.
//	error - Non-nil if the HTTP call or JSON parsing fails.
//
// Thread Safety: Safe for concurrent use.
func callOllamaEmbedInternal(embedURL string, reqBody OllamaEmbedRequest) ([][]float32, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ollama embed request: %w", err)
	}

	// Use a client with a longer timeout for batch processing
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(embedURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to call ollama /api/embed endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read ollama embed response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/embed returned status %d: %s", resp.StatusCode, string(body))
	}

	var embedResp OllamaEmbedResponse
	if err = json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to decode ollama embed response: %w", err)
	}

	slog.Debug("Ollama embeddings received", "model", embedResp.Model, "count", len(embedResp.Embeddings))
	return embedResp.Embeddings, nil
}

func getSplitterForFile(filename string) textsplitter.TextSplitter {
	ext := filepath.Ext(filename)
	switch ext {
	case ".md":
		return textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(CHUNK_SIZE),
			textsplitter.WithChunkOverlap(CHUNK_OVERLAP),
			textsplitter.WithSeparators(markdownSeparators),
		)

	case ".py":
		return textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(CHUNK_SIZE),
			textsplitter.WithChunkOverlap(CHUNK_OVERLAP),
			textsplitter.WithSeparators(pythonSeparators),
		)

	case ".js", ".ts", ".java", ".c", ".cpp", ".h", ".hpp", ".rs", ".go":
		return textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(CHUNK_SIZE),
			textsplitter.WithChunkOverlap(CHUNK_OVERLAP),
			textsplitter.WithSeparators(cStyleSeparators),
		)

	default:
		return textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(CHUNK_SIZE),
			textsplitter.WithChunkOverlap(CHUNK_OVERLAP),
			textsplitter.WithSeparators(defaultSeparators),
		)
	}
}
