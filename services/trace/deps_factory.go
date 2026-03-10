// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	agentcontext "github.com/AleutianAI/AleutianFOSS/services/trace/agent/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/events"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/grounding"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/activities"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/integration"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/phases"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/safety"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools/file"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
	"github.com/nats-io/nats.go"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// coordinatorRegistry tracks coordinators by session ID for cleanup.
// CR-2 fix: Prevent memory leaks by enabling session-based cleanup.
var coordinatorRegistry = struct {
	mu           sync.RWMutex
	coordinators map[string]*integration.Coordinator
}{
	coordinators: make(map[string]*integration.Coordinator),
}

// persistenceRegistry tracks persistence components by session ID for cleanup.
// GR-36: Prevent resource leaks by enabling session-based cleanup.
var persistenceRegistry = struct {
	mu          sync.RWMutex
	managers    map[string]*crs.PersistenceManager
	journals    map[string]crs.Journal
	projectKeys map[string]string // CRS-17: maps session ID -> checkpoint key (path hash)
}{
	managers:    make(map[string]*crs.PersistenceManager),
	journals:    make(map[string]crs.Journal),
	projectKeys: make(map[string]string),
}

// journalsByProject tracks journals by project key (checkpoint key).
// GR-36: Ensures only one journal is open per project at a time.
// This prevents BadgerDB lock conflicts when multiple sessions work on the same project.
var journalsByProject = struct {
	mu       sync.RWMutex
	journals map[string]crs.Journal // key is checkpoint key (project hash)
	sessions map[string]string      // maps project key -> session ID that owns it
}{
	journals: make(map[string]crs.Journal),
	sessions: make(map[string]string),
}

// computeGraphContentHash builds a content-aware hash from the graph ID and graph statistics.
//
// Description:
//
//	graphID alone is sha256(projectRoot) — stable per project path, not per content.
//	This function combines graphID with node count and edge count to produce a hash
//	that changes when the graph is rebuilt with different content.
//	NOTE: BuiltAtMilli is intentionally excluded — it changes on every rebuild even
//	when the source is identical, causing unnecessary 7-minute re-indexing cycles.
//
// Inputs:
//
//	graphID - The project-level graph identifier.
//	cached - The cached graph containing statistics. Must not be nil.
//
// Outputs:
//
//	string - Hex-encoded SHA-256 hash incorporating graph content metadata.
//
// Thread Safety: Safe for concurrent use (read-only on cached).
func computeGraphContentHash(graphID string, cached *CachedGraph) string {
	nodeCount := 0
	edgeCount := 0
	if cached.Graph != nil {
		nodeCount = cached.Graph.NodeCount()
		edgeCount = cached.Graph.EdgeCount()
	}

	// CRS-26n: Include a hash of sorted file paths from the index to prevent
	// collisions when two different projects have the same node+edge count.
	// graphID = sha256("/projects") is always the same across project switches
	// (container mount point is constant), so file identity is needed.
	fileHash := ""
	if cached.Index != nil {
		h := sha256.New()
		files := cached.Index.GetUniqueFilePaths()
		sort.Strings(files)
		for _, f := range files {
			h.Write([]byte(f))
		}
		fileHash = fmt.Sprintf(":%x", h.Sum(nil)[:8])
	}

	composite := fmt.Sprintf("%s:n%d:e%d%s", graphID, nodeCount, edgeCount, fileHash)
	hash := sha256.Sum256([]byte(composite))
	return fmt.Sprintf("%x", hash)
}

// registerCoordinator stores a coordinator for later cleanup.
func registerCoordinator(sessionID string, coord *integration.Coordinator) {
	coordinatorRegistry.mu.Lock()
	defer coordinatorRegistry.mu.Unlock()
	coordinatorRegistry.coordinators[sessionID] = coord
}

// registerPersistence stores persistence components for later cleanup.
// GR-36: Called when session restore infrastructure is created.
// CRS-17: projectKey is the checkpoint key (path hash) used by SaveBackup for directory
// addressing and metadata identity. Must not be empty when pm is non-nil.
func registerPersistence(sessionID string, pm *crs.PersistenceManager, journal crs.Journal, projectKey string) {
	persistenceRegistry.mu.Lock()
	defer persistenceRegistry.mu.Unlock()
	if pm != nil {
		persistenceRegistry.managers[sessionID] = pm
	}
	if journal != nil {
		persistenceRegistry.journals[sessionID] = journal
	}
	if projectKey != "" {
		persistenceRegistry.projectKeys[sessionID] = projectKey
	}
}

// closeExistingJournalForProject closes any existing journal for a project.
// GR-36: Called before creating a new journal to prevent BadgerDB lock conflicts.
// Returns true if a journal was closed.
func closeExistingJournalForProject(projectKey string) bool {
	journalsByProject.mu.Lock()
	defer journalsByProject.mu.Unlock()

	if existingJournal, ok := journalsByProject.journals[projectKey]; ok {
		oldSessionID := journalsByProject.sessions[projectKey]
		slog.Debug("GR-36: Closing existing journal for project before opening new one",
			slog.String("project_key", projectKey),
			slog.String("old_session_id", oldSessionID),
		)
		if err := existingJournal.Close(); err != nil {
			slog.Warn("GR-36: Failed to close existing journal for project",
				slog.String("project_key", projectKey),
				slog.String("error", err.Error()),
			)
		}
		delete(journalsByProject.journals, projectKey)
		delete(journalsByProject.sessions, projectKey)

		// Also remove from session-based registry if it exists
		persistenceRegistry.mu.Lock()
		if oldSessionID != "" {
			delete(persistenceRegistry.journals, oldSessionID)
			delete(persistenceRegistry.projectKeys, oldSessionID)
		}
		persistenceRegistry.mu.Unlock()

		return true
	}
	return false
}

// registerJournalForProject tracks a journal by its project key.
// GR-36: Enables proper cleanup when multiple sessions work on the same project.
func registerJournalForProject(projectKey, sessionID string, journal crs.Journal) {
	journalsByProject.mu.Lock()
	defer journalsByProject.mu.Unlock()
	journalsByProject.journals[projectKey] = journal
	journalsByProject.sessions[projectKey] = sessionID
}

// cleanupCoordinator removes and closes the coordinator for a session.
func cleanupCoordinator(sessionID string) {
	coordinatorRegistry.mu.Lock()
	defer coordinatorRegistry.mu.Unlock()

	if coord, ok := coordinatorRegistry.coordinators[sessionID]; ok {
		_ = coord.Close()
		delete(coordinatorRegistry.coordinators, sessionID)
		slog.Debug("CRS-06: Coordinator cleaned up",
			slog.String("session_id", sessionID),
		)
	}
}

// cleanupPersistence removes and closes persistence components for a session.
// GR-36: Called when session ends to save checkpoint and close resources.
// cleanupPersistence saves a checkpoint and closes journal/manager for a session.
//
// Lock ordering: persistenceRegistry.mu → journalsByProject.mu
// (must always acquire in this order to avoid deadlocks).
func cleanupPersistence(sessionID string) {
	var projectKey string

	// Phase 1: Under persistenceRegistry lock — save, close, and remove entries.
	func() {
		persistenceRegistry.mu.Lock()
		defer persistenceRegistry.mu.Unlock()

		// CRS-PERSIST-01: Save checkpoint before closing.
		// CRS-17: Use projectKey (checkpoint key / path hash) instead of sessionID (UUID)
		// so metadata.ProjectHash matches what TryRestore validates against.
		pm := persistenceRegistry.managers[sessionID]
		journal := persistenceRegistry.journals[sessionID]
		projectKey = persistenceRegistry.projectKeys[sessionID]
		if pm != nil && journal != nil && projectKey != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := pm.SaveBackup(ctx, projectKey, journal, nil); err != nil {
				slog.Warn("CRS-PERSIST-01: Failed to save session checkpoint",
					slog.String("session_id", sessionID),
					slog.String("project_key", projectKey),
					slog.String("error", err.Error()),
				)
			} else {
				slog.Info("CRS-PERSIST-01: Session checkpoint saved",
					slog.String("session_id", sessionID),
					slog.String("project_key", projectKey),
				)
			}
		}

		// Close journal first (flushes pending writes)
		if journal != nil {
			if err := journal.Close(); err != nil {
				slog.Warn("GR-36: Failed to close journal",
					slog.String("session_id", sessionID),
					slog.String("error", err.Error()),
				)
			} else {
				slog.Debug("GR-36: Journal closed",
					slog.String("session_id", sessionID),
				)
			}
			delete(persistenceRegistry.journals, sessionID)
		}

		// Then close persistence manager
		if pm != nil {
			if err := pm.Close(); err != nil {
				slog.Warn("GR-36: Failed to close persistence manager",
					slog.String("session_id", sessionID),
					slog.String("error", err.Error()),
				)
			} else {
				slog.Debug("GR-36: Persistence manager closed",
					slog.String("session_id", sessionID),
				)
			}
			delete(persistenceRegistry.managers, sessionID)
		}
		delete(persistenceRegistry.projectKeys, sessionID)
	}()

	// Phase 2: Under journalsByProject lock — remove stale project entry.
	// CRS-17 CR2: Without this cleanup, closeExistingJournalForProject would
	// attempt to Close() the already-closed journal on next session start,
	// risking a double-close panic.
	if projectKey != "" {
		journalsByProject.mu.Lock()
		if existingSession, ok := journalsByProject.sessions[projectKey]; ok && existingSession == sessionID {
			delete(journalsByProject.journals, projectKey)
			delete(journalsByProject.sessions, projectKey)
		}
		journalsByProject.mu.Unlock()
	}
}

// init registers cleanup hooks.
func init() {
	agent.RegisterSessionCleanupHook("coordinator", cleanupCoordinator)
	agent.RegisterSessionCleanupHook("persistence", cleanupPersistence)
}

// DefaultDependenciesFactory creates phase Dependencies for agent sessions.
//
// Description:
//
//	DefaultDependenciesFactory holds shared components (LLM client, tool registry,
//	etc.) and creates per-session Dependencies structs when Create is called.
//	When enableContext or enableTools are set, it creates ContextManager and
//	ToolRegistry dynamically using the graph from the Service.
//
// Thread Safety: DefaultDependenciesFactory is safe for concurrent use.
type DefaultDependenciesFactory struct {
	llmClient        llm.Client
	graphProvider    phases.GraphProvider
	toolRegistry     *tools.Registry
	toolExecutor     *tools.Executor
	safetyGate       safety.Gate
	eventEmitter     *events.Emitter
	responseGrounder grounding.Grounder

	// service provides access to cached graphs for context/tools
	service *Service

	// enableContext enables ContextManager creation when graph is available
	enableContext bool

	// enableTools enables ToolRegistry creation when graph is available
	enableTools bool

	// enableCoordinator enables MCTS activity coordination
	enableCoordinator bool

	// enableSessionRestore enables CRS session restore from checkpoints
	// GR-36: When enabled, attempts to restore CRS state from previous session
	enableSessionRestore bool

	// persistenceBaseDir is the base directory for CRS persistence
	// GR-36: Defaults to ~/.aleutian/crs if not set
	persistenceBaseDir string

	// weaviateClient is the Weaviate client for semantic resolution.
	// CRS-25: Optional - if nil, RAG uses structural-only resolution.
	weaviateClient *weaviate.Client

	// weaviateDataSpace is the project isolation key for Weaviate.
	weaviateDataSpace string

	// embedClient is the embedding client for pre-computing vectors.
	// CRS-26i: Routes embedding requests through the orchestrator.
	embedClient *rag.EmbedClient

	// indexingCoord is the shared symbol indexing coordinator.
	// CRS-26l: When set, delegates symbol indexing to the coordinator
	// instead of running an inline goroutine. Enables eager indexing
	// at init time.
	indexingCoord *SymbolIndexingCoordinator

	// indexingMu guards indexingHash to prevent concurrent indexing goroutines
	// from racing (delete-all + re-index interleaving).
	// CRS-26l: Only used when indexingCoord is nil (legacy fallback).
	indexingMu   sync.Mutex
	indexingHash string // graph hash currently being indexed, empty when idle

	// natsJS is the JetStream context for creating NATSJournals.
	// CRS-27: When set, sessions use NATS JetStream for CRS delta persistence
	// instead of embedded BadgerDB. Falls back to in-memory BadgerJournal when nil.
	natsJS     nats.JetStreamContext
	natsStream string // JetStream stream name (default: "CRS_DELTAS")
}

// DependenciesFactoryOption configures a DefaultDependenciesFactory.
type DependenciesFactoryOption func(*DefaultDependenciesFactory)

// NewDependenciesFactory creates a new dependencies factory.
//
// Description:
//
//	Creates a factory with the provided options. Use the With* functions
//	to configure the shared components.
//
// Inputs:
//
//	opts - Configuration options.
//
// Outputs:
//
//	*DefaultDependenciesFactory - The configured factory.
func NewDependenciesFactory(opts ...DependenciesFactoryOption) *DefaultDependenciesFactory {
	f := &DefaultDependenciesFactory{}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

// WithLLMClient sets the LLM client.
func WithLLMClient(client llm.Client) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.llmClient = client
	}
}

// WithGraphProvider sets the graph provider.
func WithGraphProvider(provider phases.GraphProvider) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.graphProvider = provider
	}
}

// WithToolRegistry sets the tool registry.
func WithToolRegistry(registry *tools.Registry) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.toolRegistry = registry
	}
}

// WithToolExecutor sets the tool executor.
func WithToolExecutor(executor *tools.Executor) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.toolExecutor = executor
	}
}

// WithSafetyGate sets the safety gate.
func WithSafetyGate(gate safety.Gate) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.safetyGate = gate
	}
}

// WithEventEmitter sets the event emitter.
func WithEventEmitter(emitter *events.Emitter) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.eventEmitter = emitter
	}
}

// WithService sets the service for accessing cached graphs.
func WithService(svc *Service) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.service = svc
	}
}

// WithContextEnabled enables ContextManager creation.
func WithContextEnabled(enabled bool) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.enableContext = enabled
	}
}

// WithToolsEnabled enables ToolRegistry creation.
func WithToolsEnabled(enabled bool) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.enableTools = enabled
	}
}

// WithResponseGrounder sets the response grounding validator.
func WithResponseGrounder(grounder grounding.Grounder) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.responseGrounder = grounder
	}
}

// WithCoordinatorEnabled enables MCTS activity coordination.
//
// Description:
//
//	When enabled, Creates a Coordinator for each session that orchestrates
//	MCTS activities (Search, Learning, Constraint, Planning, Awareness,
//	Similarity, Streaming, Memory) in response to agent events.
//
// Inputs:
//
//	enabled - Whether to enable the coordinator.
//
// Outputs:
//
//	DependenciesFactoryOption - The configuration function.
func WithCoordinatorEnabled(enabled bool) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.enableCoordinator = enabled
	}
}

// WithSessionRestoreEnabled enables CRS session restore from checkpoints.
//
// Description:
//
//	When enabled, attempts to restore CRS state from a previous session
//	checkpoint. This preserves learned clauses, proof numbers, and other
//	CRS state across agent sessions.
//
// Inputs:
//
//	enabled - Whether to enable session restore.
//
// Outputs:
//
//	DependenciesFactoryOption - The configuration function.
//
// GR-36: Added for session restore integration.
func WithSessionRestoreEnabled(enabled bool) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.enableSessionRestore = enabled
	}
}

// WithPersistenceBaseDir sets the base directory for CRS persistence.
//
// Description:
//
//	Sets the directory where CRS checkpoints and journals are stored.
//	If not set, defaults to ~/.aleutian/crs.
//
// Inputs:
//
//	dir - The base directory path.
//
// Outputs:
//
//	DependenciesFactoryOption - The configuration function.
//
// GR-36: Added for session restore integration.
func WithPersistenceBaseDir(dir string) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.persistenceBaseDir = dir
	}
}

// WithWeaviateClient sets the Weaviate client for semantic RAG resolution.
//
// Description:
//
//	CRS-25: When set, the factory creates a CombinedResolver (structural + semantic)
//	instead of a StructuralResolver-only. The dataSpace is used for project isolation.
//
// Inputs:
//
//	client - Weaviate client. May be nil (degrades to structural-only).
//	dataSpace - Project isolation key.
func WithWeaviateClient(client *weaviate.Client, dataSpace string) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.weaviateClient = client
		f.weaviateDataSpace = dataSpace
	}
}

// WithEmbedClient sets the embedding client for pre-computing vectors.
//
// Description:
//
//	CRS-26i: When set, SymbolStore pre-computes vectors via the orchestrator's
//	/v1/embed endpoint before inserting into Weaviate. SemanticResolver uses
//	the same client to embed query tokens for nearVector search.
//
// Inputs:
//
//	client - Embedding client. May be nil (graceful degradation — no vectors).
func WithEmbedClient(client *rag.EmbedClient) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.embedClient = client
	}
}

// WithIndexingCoordinator sets the shared symbol indexing coordinator.
//
// Description:
//
//	CRS-26l: When set, the factory delegates symbol indexing to the coordinator
//	instead of running an inline goroutine. This enables eager indexing at
//	graph init time via HandleInit.
//
// Inputs:
//
//	coord - The indexing coordinator. May be nil (falls back to inline goroutine).
func WithIndexingCoordinator(coord *SymbolIndexingCoordinator) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.indexingCoord = coord
	}
}

// WithNATSJetStream sets the NATS JetStream context for CRS delta persistence.
//
// Description:
//
//	CRS-27: When set, sessions use NATS JetStream for CRS delta persistence
//	instead of embedded BadgerDB. Falls back to in-memory BadgerJournal when
//	NATS is unavailable at session creation time.
//
// Inputs:
//
//	js - JetStream context from a connected NATS client.
//	streamName - JetStream stream name (e.g., "CRS_DELTAS").
//
// Thread Safety: Safe for concurrent use.
func WithNATSJetStream(js nats.JetStreamContext, streamName string) DependenciesFactoryOption {
	return func(f *DefaultDependenciesFactory) {
		f.natsJS = js
		f.natsStream = streamName
	}
}

// Create implements agent.DependenciesFactory.
//
// Description:
//
//	Creates a Dependencies struct for the given session and query.
//	Uses the pre-configured shared components. Retrieves existing
//	context from the session if available (for cross-phase context sharing).
//	When enableContext or enableTools are set, creates ContextManager and
//	ToolRegistry using the graph from the Service.
//
// Inputs:
//
//	session - The current session.
//	query - The user's query.
//
// Outputs:
//
//	any - The Dependencies struct (as *phases.Dependencies).
//	error - Non-nil if creation failed.
//
// Thread Safety: This method is safe for concurrent use.
func (f *DefaultDependenciesFactory) Create(session *agent.Session, query string) (any, error) {
	deps := &phases.Dependencies{
		Session:          session,
		Query:            query,
		LLMClient:        f.llmClient,
		GraphProvider:    f.graphProvider,
		ToolRegistry:     f.toolRegistry,
		ToolExecutor:     f.toolExecutor,
		SafetyGate:       f.safetyGate,
		EventEmitter:     f.eventEmitter,
		ResponseGrounder: f.responseGrounder,
		// Retrieve existing context from session (persisted by PlanPhase)
		Context: session.GetCurrentContext(),
	}

	// IT-08b: Wire ParamExtractor from session into dependencies
	if pe := session.GetParamExtractor(); pe != nil {
		deps.ParamExtractor = pe
	}

	// Try to get the cached graph if we need context or tools
	if (f.enableContext || f.enableTools) && f.service != nil {
		graphID := session.GetGraphID()
		if graphID != "" {
			cached, err := f.service.GetGraph(graphID)
			if err == nil && cached != nil {
				slog.Info("Creating dependencies with graph",
					slog.String("session_id", session.ID),
					slog.String("graph_id", graphID),
					slog.Bool("with_context", f.enableContext),
					slog.Bool("with_tools", f.enableTools),
				)

				// Create ContextManager if enabled
				if f.enableContext && cached.Graph != nil && cached.Index != nil {
					mgr, err := agentcontext.NewManager(cached.Graph, cached.Index, nil)
					if err != nil {
						slog.Warn("Failed to create ContextManager",
							slog.String("error", err.Error()),
						)
					} else {
						deps.ContextManager = mgr
						slog.Info("ContextManager created",
							slog.String("session_id", session.ID),
						)
					}
				}

				// CB-31d: Populate GraphAnalytics and SymbolIndex for symbol resolution
				if cached.Graph != nil && cached.Index != nil {
					// Wrap graph as hierarchical for analytics
					hg, err := graph.WrapGraph(cached.Graph)
					if err != nil {
						slog.Warn("CB-31d: Failed to wrap graph for analytics",
							slog.String("error", err.Error()),
						)
					} else {
						// Create GraphAnalytics for symbol resolution
						deps.GraphAnalytics = graph.NewGraphAnalytics(hg)
						deps.SymbolIndex = cached.Index
						slog.Debug("CB-31d: Symbol resolution enabled",
							slog.String("session_id", session.ID),
						)

						// CRS-25: Create RAG resolver for entity grounding.
						// Layer 1 (structural) always available. Layer 2 (semantic) when Weaviate connected.
						structural := rag.NewStructuralResolver(cached.Index)
						if f.weaviateClient != nil && f.embedClient != nil {
							semantic, sErr := rag.NewSemanticResolver(f.weaviateClient, f.weaviateDataSpace, f.embedClient)
							if sErr != nil {
								slog.Warn("CRS-26j: Semantic resolver init failed, using structural-only",
									slog.String("error", sErr.Error()),
								)
								deps.RAGResolver = structural
							} else {
								deps.RAGResolver = rag.NewCombinedResolver(structural, semantic)
								slog.Debug("CRS-25: Combined RAG resolver enabled (structural + semantic)",
									slog.String("session_id", session.ID),
									slog.Int("packages", len(structural.PackageNames())),
								)

								// CRS-26l: Delegate symbol indexing to coordinator if available.
								if f.indexingCoord != nil {
									f.indexingCoord.TriggerIndexing(graphID, cached)
									if store := f.indexingCoord.GetSymbolStore(); store != nil {
										deps.SymbolStore = store
									}
								} else {
									// CRS-25: Legacy fallback — inline goroutine for indexing.
									preHash := computeGraphContentHash(graphID, cached)
									go func() {
										defer func() {
											if r := recover(); r != nil {
												slog.Error("CRS-25: Panic in symbol indexing goroutine",
													slog.Any("panic", r),
												)
											}
										}()
										f.indexingMu.Lock()
										if f.indexingHash == preHash {
											f.indexingMu.Unlock()
											slog.Debug("CRS-25: Indexing already in progress for this graph", slog.String("graph_hash", preHash))
											return
										}
										f.indexingHash = preHash
										f.indexingMu.Unlock()
										defer func() {
											f.indexingMu.Lock()
											f.indexingHash = ""
											f.indexingMu.Unlock()
										}()
										indexCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
										defer cancel()
										store, storeErr := rag.NewSymbolStore(f.weaviateClient, f.weaviateDataSpace, f.embedClient)
										if storeErr != nil {
											slog.Warn("CRS-25: Failed to create symbol store", slog.String("error", storeErr.Error()))
											return
										}
										deps.SymbolStore = store
										hasHash, hashErr := store.HasGraphHash(indexCtx, preHash)
										if hashErr != nil {
											slog.Warn("CRS-25: Failed to check graph hash", slog.String("error", hashErr.Error()))
											return
										}
										if hasHash {
											slog.Debug("CRS-25: Symbols already indexed for this graph", slog.String("graph_hash", preHash))
											return
										}
										if delErr := store.DeleteAll(indexCtx); delErr != nil {
											slog.Warn("CRS-25: Failed to delete stale symbols", slog.String("error", delErr.Error()))
										}
										count, idxErr := store.IndexSymbols(indexCtx, cached.Index, preHash, nil)
										if idxErr != nil {
											slog.Warn("CRS-25: Symbol indexing failed", slog.String("error", idxErr.Error()))
										} else {
											slog.Info("CRS-25: Symbols indexed into Weaviate",
												slog.Int("count", count),
												slog.String("graph_hash", preHash),
											)
										}
									}()
								}
							}
						} else {
							deps.RAGResolver = structural
							slog.Debug("CRS-25: Structural RAG resolver enabled (no Weaviate)",
								slog.String("session_id", session.ID),
								slog.Int("packages", len(structural.PackageNames())),
							)
						}
					}
				}

				// CRS-19: Create staleness checker from graph's recorded file mtimes.
				if cached.Graph != nil && len(cached.Graph.FileMtimes) > 0 {
					projectRoot := session.GetProjectRoot()
					if projectRoot != "" {
						deps.StalenessChecker = graph.NewStalenessChecker(projectRoot, cached.Graph.FileMtimes)
						slog.Debug("CRS-19: Staleness checker enabled",
							slog.String("session_id", session.ID),
							slog.Int("tracked_files", len(cached.Graph.FileMtimes)),
						)
					}
				}

				// Create ToolRegistry if enabled
				if f.enableTools && cached.Graph != nil && cached.Index != nil {
					registry := tools.NewRegistry()

					// Register all CB-20/CB-31b exploration tools (graph-based)
					// Use the centralized registration function
					tools.RegisterExploreTools(registry, cached.Graph, cached.Index)

					// CRS-26m: Register semantic search tools (vector-based)
					if f.weaviateClient != nil && f.embedClient != nil {
						tools.RegisterSemanticTools(registry, f.weaviateClient, f.weaviateDataSpace, f.embedClient, cached.Index)
						slog.Info("Semantic tools registered",
							slog.String("session_id", session.ID),
						)
					}

					// Register CB-30 file operation tools (Read, Write, Edit, Glob, Grep, Diff, Tree, JSON)
					projectRoot := session.GetProjectRoot()
					if projectRoot != "" {
						fileConfig := file.NewConfig(projectRoot)
						file.RegisterFileTools(registry, fileConfig)
						slog.Info("File tools registered",
							slog.String("session_id", session.ID),
							slog.String("project_root", projectRoot),
						)
					}

					deps.ToolRegistry = registry
					deps.ToolExecutor = tools.NewExecutor(registry, nil)

					// Mark graph_initialized requirement as satisfied since we have a valid graph
					deps.ToolExecutor.SatisfyRequirement("graph_initialized")

					slog.Info("ToolRegistry created",
						slog.String("session_id", session.ID),
						slog.Int("tool_count", registry.Count()),
					)
				}
			}
		}
	}

	// Create Coordinator if enabled
	if f.enableCoordinator {
		// Create CRS for this session
		sessionCRS := crs.New(nil)
		deps.CRS = sessionCRS
		// CRS-00 Rev 2: Wire CRS to session so HasCRS() returns true
		// and CRS-02 through CRS-06 features activate in execute.go.
		session.SetCRS(sessionCRS)

		// GR-36: Set up session restore infrastructure if enabled
		var restoreResult *crs.RestoreResult
		if f.enableSessionRestore {
			projectRoot := session.GetProjectRoot()
			if projectRoot != "" {
				restoreResult = f.trySessionRestore(session.ID, projectRoot, sessionCRS, deps)
			}
		}

		// Create Bridge connecting activities to CRS.
		// CRS-WIRE-01: Wire journal to Bridge so coordinator-driven deltas
		// (learned constraints, proof updates) are persisted.
		var bridgeOpts []integration.BridgeOption
		if deps.Journal != nil {
			// Session restore path: persistent journal already created
			bridgeOpts = append(bridgeOpts, integration.WithJournal(deps.Journal))
			slog.Info("CRS-WIRE-01: Persistent journal wired to Bridge",
				slog.String("session_id", session.ID),
			)
		} else {
			// No session restore: create in-memory journal for within-session learning.
			// Deltas won't survive restart but the coordinator's learning loop
			// (CDCL clauses, constraint propagation) still works within this session.
			memJournal, memErr := crs.NewBadgerJournal(crs.JournalConfig{
				SessionID:     session.ID,
				InMemory:      true,
				SyncWrites:    false,
				AllowDegraded: true,
			})
			if memErr == nil {
				deps.Journal = memJournal
				bridgeOpts = append(bridgeOpts, integration.WithJournal(memJournal))
				registerPersistence(session.ID, nil, memJournal, "")
				slog.Info("CRS-WIRE-01: In-memory journal wired to Bridge",
					slog.String("session_id", session.ID),
				)
			} else {
				slog.Debug("CRS-WIRE-01: In-memory journal creation failed, bridge runs without journal",
					slog.String("error", memErr.Error()),
				)
			}
		}
		bridge := integration.NewBridge(sessionCRS, nil, bridgeOpts...)

		// Create Coordinator with default configuration
		coordConfig := integration.DefaultCoordinatorConfig()
		coordConfig.EnableTracing = true
		coordConfig.EnableMetrics = true
		coordConfig.ActivityConfigs = integration.DefaultActivityConfigs()

		coordinator := integration.NewCoordinator(bridge, coordConfig)

		// CR-1 fix: Register all 8 MCTS activities with the Coordinator
		coordinator.Register(activities.NewSearchActivity(nil))
		coordinator.Register(activities.NewLearningActivity(nil))
		coordinator.Register(activities.NewConstraintActivity(nil))
		coordinator.Register(activities.NewPlanningActivity(nil))
		coordinator.Register(activities.NewAwarenessActivity(nil))
		coordinator.Register(activities.NewSimilarityActivity(nil))
		coordinator.Register(activities.NewStreamingActivity(nil))
		coordinator.Register(activities.NewMemoryActivity(nil))

		// CR-2 fix: Register for cleanup to prevent memory leaks
		registerCoordinator(session.ID, coordinator)

		deps.Coordinator = coordinator

		// GR-36: Emit EventSessionRestored if session was restored
		if restoreResult != nil && restoreResult.Restored {
			coordinator.HandleEvent(context.Background(), integration.EventSessionRestored, &integration.EventData{
				SessionID:         session.ID,
				Generation:        restoreResult.Generation,
				CheckpointAgeMs:   restoreResult.CheckpointAge.Milliseconds(),
				ModifiedFileCount: restoreResult.ModifiedFileCount,
			})
		}

		slog.Info("Coordinator created for session",
			slog.String("session_id", session.ID),
			slog.Int("activity_count", 8),
			slog.Bool("session_restored", restoreResult != nil && restoreResult.Restored),
		)
	}

	return deps, nil
}

// trySessionRestore attempts to restore CRS state from a previous session.
//
// GR-36: Integrates session restore with dependencies factory.
func (f *DefaultDependenciesFactory) trySessionRestore(
	sessionID string,
	projectRoot string,
	sessionCRS crs.CRS,
	deps *phases.Dependencies,
) *crs.RestoreResult {
	ctx := context.Background()

	// Determine persistence base directory
	baseDir := f.persistenceBaseDir
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			slog.Warn("GR-36: Failed to get home directory for persistence",
				slog.String("error", err.Error()),
			)
			return nil
		}
		baseDir = filepath.Join(homeDir, ".aleutian", "crs")
	}

	// Create persistence manager
	pmConfig := &crs.PersistenceConfig{
		BaseDir:           baseDir,
		CompressionLevel:  6,
		LockTimeoutSec:    30,
		MaxBackupRetries:  3,
		ValidateOnRestore: true,
	}

	pm, err := crs.NewPersistenceManager(pmConfig)
	if err != nil {
		slog.Warn("GR-36: Failed to create persistence manager",
			slog.String("error", err.Error()),
		)
		return nil
	}
	deps.PersistenceManager = pm

	// Create session identifier
	sessionIdentifier, err := crs.NewSessionIdentifier(ctx, projectRoot)
	if err != nil {
		slog.Warn("GR-36: Failed to create session identifier",
			slog.String("error", err.Error()),
		)
		return nil
	}

	// Create journal for this session.
	// CRS-27: Prefer NATS JetStream when available, fall back to BadgerDB.
	// GR-36: First close any existing journal for this project to prevent lock conflicts.
	projectKey := sessionIdentifier.CheckpointKey()
	closeExistingJournalForProject(projectKey)

	var journal crs.Journal
	if f.natsJS != nil {
		// CRS-27: Use NATS JetStream for CRS delta persistence
		streamName := f.natsStream
		if streamName == "" {
			streamName = "CRS_DELTAS"
		}
		natsJournal, natsErr := crs.NewNATSJournal(crs.NATSJournalConfig{
			SessionID:  sessionID,
			JS:         f.natsJS,
			StreamName: streamName,
			Logger:     slog.Default(),
		})
		if natsErr != nil {
			slog.Warn("CRS-27: Failed to create NATSJournal, falling back to BadgerDB",
				slog.String("error", natsErr.Error()),
			)
		} else {
			journal = natsJournal
			slog.Info("CRS-27: Using NATS JetStream journal",
				slog.String("session_id", sessionID),
				slog.String("stream", streamName),
			)
		}
	}

	if journal == nil {
		// Fall back to BadgerDB journal
		journalPath := filepath.Join(baseDir, projectKey, "journal")
		journalConfig := crs.JournalConfig{
			SessionID:  sessionID,
			Path:       journalPath,
			SyncWrites: false,
		}

		badgerJournal, badgerErr := crs.NewBadgerJournal(journalConfig)
		if badgerErr != nil {
			slog.Warn("GR-36: Failed to create BadgerJournal",
				slog.String("error", badgerErr.Error()),
			)
			return nil
		}
		journal = badgerJournal
	}
	deps.Journal = journal

	// CRS-27: Attempt one-time migration from BadgerDB to NATS if applicable.
	if natsJournal, ok := journal.(*crs.NATSJournal); ok {
		if migErr := migrateFromBadgerIfNeeded(ctx, baseDir, projectKey, sessionID, natsJournal, pm); migErr != nil {
			slog.Warn("CRS-27: Migration from BadgerDB failed (non-fatal)",
				slog.String("error", migErr.Error()),
			)
		}
	}

	// Register for cleanup when session ends (both session-based and project-based)
	registerPersistence(sessionID, pm, journal, projectKey)
	registerJournalForProject(projectKey, sessionID, journal)

	// Create restorer and attempt restore
	restorer, err := crs.NewSessionRestorer(pm, nil)
	if err != nil {
		slog.Warn("GR-36: Failed to create session restorer",
			slog.String("error", err.Error()),
		)
		return nil
	}

	result, err := restorer.TryRestore(ctx, sessionCRS, journal, sessionIdentifier)
	if err != nil {
		slog.Warn("GR-36: Session restore failed",
			slog.String("error", err.Error()),
		)
		return nil
	}

	if result.Restored {
		slog.Info("GR-36: Session restored from checkpoint",
			slog.String("session_id", sessionID),
			slog.String("project_root", projectRoot),
			slog.Int64("generation", result.Generation),
			slog.Duration("checkpoint_age", result.CheckpointAge),
			slog.Int64("duration_ms", result.DurationMs),
		)
	} else {
		slog.Debug("GR-36: No checkpoint to restore",
			slog.String("session_id", sessionID),
			slog.String("reason", result.Reason),
		)
	}

	return result
}

// migrateFromBadgerIfNeeded performs a one-time migration from BadgerDB to NATS JetStream.
//
// Description:
//
//	CRS-27: Checks if a BadgerDB backup exists for the project and the NATS stream
//	is empty. If so, creates a temporary in-memory BadgerJournal, loads the backup
//	into it, replays all deltas, and appends each to the NATS journal. The old
//	backup is renamed to .migrated to prevent re-migration.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	baseDir - CRS persistence base directory.
//	projectKey - The checkpoint key (project hash).
//	sessionID - Current session identifier.
//	natsJournal - The NATS journal to migrate into.
//	pm - Persistence manager for loading backups.
//
// Outputs:
//
//	error - Non-nil if migration fails (non-fatal, caller should log and continue).
//
// Thread Safety: Safe for concurrent use (operates on project-scoped paths).
func migrateFromBadgerIfNeeded(
	ctx context.Context,
	baseDir string,
	projectKey string,
	sessionID string,
	natsJournal *crs.NATSJournal,
	pm *crs.PersistenceManager,
) error {
	// Check if NATS stream already has data for this session
	stats := natsJournal.Stats()
	if stats.TotalDeltas > 0 {
		slog.Debug("CRS-27: NATS stream already has data, skipping migration",
			slog.String("session_id", sessionID),
			slog.Int64("total_deltas", int64(stats.TotalDeltas)),
		)
		return nil
	}

	// Check if a BadgerDB backup exists
	backupDir := filepath.Join(baseDir, projectKey, "backups")
	latestBackup := filepath.Join(backupDir, "latest.backup.gz")
	if _, err := os.Stat(latestBackup); os.IsNotExist(err) {
		slog.Debug("CRS-27: No BadgerDB backup found, nothing to migrate",
			slog.String("path", latestBackup),
		)
		return nil
	}

	slog.Info("CRS-27: Migrating BadgerDB backup to NATS JetStream",
		slog.String("project_key", projectKey),
		slog.String("backup_path", latestBackup),
	)

	// Create temporary in-memory BadgerJournal for loading the backup
	tmpJournal, err := crs.NewBadgerJournal(crs.JournalConfig{
		SessionID: sessionID + "-migration",
		InMemory:  true,
	})
	if err != nil {
		return fmt.Errorf("creating temp journal for migration: %w", err)
	}
	defer tmpJournal.Close()

	// Load backup into temp journal
	_, err = pm.LoadBackup(ctx, projectKey, tmpJournal)
	if err != nil {
		return fmt.Errorf("loading BadgerDB backup: %w", err)
	}

	// Replay all deltas from temp journal and append to NATS
	deltas, replayErr := tmpJournal.Replay(ctx)
	if replayErr != nil {
		return fmt.Errorf("replaying temp journal: %w", replayErr)
	}

	migratedCount := 0
	for _, delta := range deltas {
		if appendErr := natsJournal.Append(ctx, delta); appendErr != nil {
			return fmt.Errorf("appending delta %d to NATS: %w", migratedCount, appendErr)
		}
		migratedCount++
	}

	// Rename old backup to prevent re-migration
	migratedPath := latestBackup + ".migrated"
	if renameErr := os.Rename(latestBackup, migratedPath); renameErr != nil {
		slog.Warn("CRS-27: Failed to rename backup after migration (may re-migrate next time)",
			slog.String("error", renameErr.Error()),
		)
	}

	slog.Info("CRS-27: Migration complete",
		slog.String("project_key", projectKey),
		slog.Int("deltas_migrated", migratedCount),
	)

	return nil
}

// Ensure DefaultDependenciesFactory implements agent.DependenciesFactory.
var _ agent.DependenciesFactory = (*DefaultDependenciesFactory)(nil)
