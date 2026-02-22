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
	"log/slog"
	"os"
	"path/filepath"
	"sync"

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
	mu       sync.RWMutex
	managers map[string]*crs.PersistenceManager
	journals map[string]*crs.BadgerJournal
}{
	managers: make(map[string]*crs.PersistenceManager),
	journals: make(map[string]*crs.BadgerJournal),
}

// journalsByProject tracks journals by project key (checkpoint key).
// GR-36: Ensures only one journal is open per project at a time.
// This prevents BadgerDB lock conflicts when multiple sessions work on the same project.
var journalsByProject = struct {
	mu       sync.RWMutex
	journals map[string]*crs.BadgerJournal // key is checkpoint key (project hash)
	sessions map[string]string             // maps project key -> session ID that owns it
}{
	journals: make(map[string]*crs.BadgerJournal),
	sessions: make(map[string]string),
}

// registerCoordinator stores a coordinator for later cleanup.
func registerCoordinator(sessionID string, coord *integration.Coordinator) {
	coordinatorRegistry.mu.Lock()
	defer coordinatorRegistry.mu.Unlock()
	coordinatorRegistry.coordinators[sessionID] = coord
}

// registerPersistence stores persistence components for later cleanup.
// GR-36: Called when session restore infrastructure is created.
func registerPersistence(sessionID string, pm *crs.PersistenceManager, journal *crs.BadgerJournal) {
	persistenceRegistry.mu.Lock()
	defer persistenceRegistry.mu.Unlock()
	if pm != nil {
		persistenceRegistry.managers[sessionID] = pm
	}
	if journal != nil {
		persistenceRegistry.journals[sessionID] = journal
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
		}
		persistenceRegistry.mu.Unlock()

		return true
	}
	return false
}

// registerJournalForProject tracks a journal by its project key.
// GR-36: Enables proper cleanup when multiple sessions work on the same project.
func registerJournalForProject(projectKey, sessionID string, journal *crs.BadgerJournal) {
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
func cleanupPersistence(sessionID string) {
	persistenceRegistry.mu.Lock()
	defer persistenceRegistry.mu.Unlock()

	// Close journal first (flushes pending writes)
	if journal, ok := persistenceRegistry.journals[sessionID]; ok {
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
	if pm, ok := persistenceRegistry.managers[sessionID]; ok {
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
					}
				}

				// Create ToolRegistry if enabled
				if f.enableTools && cached.Graph != nil && cached.Index != nil {
					registry := tools.NewRegistry()

					// Register all CB-20/CB-31b exploration tools (graph-based)
					// Use the centralized registration function
					tools.RegisterExploreTools(registry, cached.Graph, cached.Index)

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

		// GR-36: Set up session restore infrastructure if enabled
		var restoreResult *crs.RestoreResult
		if f.enableSessionRestore {
			projectRoot := session.GetProjectRoot()
			if projectRoot != "" {
				restoreResult = f.trySessionRestore(session.ID, projectRoot, sessionCRS, deps)
			}
		}

		// Create Bridge connecting activities to CRS
		bridge := integration.NewBridge(sessionCRS, nil)

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

	// Create BadgerJournal for this session
	// GR-36: First close any existing journal for this project to prevent lock conflicts
	projectKey := sessionIdentifier.CheckpointKey()
	closeExistingJournalForProject(projectKey)

	journalPath := filepath.Join(baseDir, projectKey, "journal")
	journalConfig := crs.JournalConfig{
		SessionID:  sessionID,
		Path:       journalPath,
		SyncWrites: false,
	}

	journal, err := crs.NewBadgerJournal(journalConfig)
	if err != nil {
		slog.Warn("GR-36: Failed to create BadgerJournal",
			slog.String("error", err.Error()),
		)
		return nil
	}
	deps.BadgerJournal = journal

	// Register for cleanup when session ends (both session-based and project-based)
	registerPersistence(sessionID, pm, journal)
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

// Ensure DefaultDependenciesFactory implements agent.DependenciesFactory.
var _ agent.DependenciesFactory = (*DefaultDependenciesFactory)(nil)
