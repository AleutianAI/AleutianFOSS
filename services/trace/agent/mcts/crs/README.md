# CRS - Constraint Reasoning System

The Constraint Reasoning System (CRS) provides persistent, queryable state for agent sessions. It maintains six synchronized indexes and supports cross-session persistence via BadgerDB backup/restore.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CRS ARCHITECTURE                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                          CRS Instance                                  │  │
│  │                                                                        │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐  │  │
│  │  │ Proof Index  │ │ Constraint   │ │ Similarity   │ │ Dependency   │  │  │
│  │  │              │ │ Index        │ │ Index        │ │ Index        │  │  │
│  │  └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘  │  │
│  │                                                                        │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────────┐   │  │
│  │  │ History      │ │ Streaming    │ │ Clause Index (CDCL)          │   │  │
│  │  │ Index        │ │ Index        │ │                              │   │  │
│  │  └──────────────┘ └──────────────┘ └──────────────────────────────┘   │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                     │                                        │
│                                     ▼                                        │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                        BadgerJournal (WAL)                            │  │
│  │                                                                        │  │
│  │  • CRC32 checksums for integrity                                      │  │
│  │  • Streaming replay for recovery                                      │  │
│  │  • Degraded mode support                                              │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                     │                                        │
│                                     ▼                                        │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                      PersistenceManager (GR-33)                       │  │
│  │                                                                        │  │
│  │  • SaveBackup() - Compressed, integrity-verified backups              │  │
│  │  • LoadBackup() - Restore with version compatibility check            │  │
│  │  • flock-based file locking                                           │  │
│  │  • Atomic file operations                                             │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Disk Persistence

CRS state can be persisted to disk and restored across sessions using the `PersistenceManager`.

### File Layout

```
~/.aleutian/crs/
├── {project_hash}/                     # Per-project isolation
│   ├── badger/                         # Live BadgerDB directory
│   │   ├── MANIFEST
│   │   └── *.sst
│   ├── backups/                        # Backup files
│   │   ├── latest.backup.gz            # Gzipped BadgerDB backup
│   │   └── latest.backup.gz.lock       # flock advisory lock
│   ├── metadata.json                   # Backup metadata with hash
│   └── export.json                     # JSON export (portable)
│
└── index.json                          # All projects index
```

### Session Initialization (Load Backup)

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "path/filepath"
    "time"

    "github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
)

func InitializeSession(ctx context.Context, projectPath string) (*crs.CRS, *crs.BadgerJournal, *crs.PersistenceManager, error) {
    // 1. Compute project hash from path (consistent across sessions)
    projectHash := crs.ComputeProjectHash(projectPath)

    // 2. Create persistence manager
    pmConfig := crs.DefaultPersistenceConfig()
    pm, err := crs.NewPersistenceManager(&pmConfig)
    if err != nil {
        return nil, nil, nil, fmt.Errorf("create persistence manager: %w", err)
    }

    // 3. Create journal for this session
    journalConfig := crs.JournalConfig{
        SessionID:  "session-" + time.Now().Format("20060102-150405"),
        Path:       filepath.Join(pm.ProjectDir(projectHash), "badger"),
        SyncWrites: true,
    }
    journal, err := crs.NewBadgerJournal(journalConfig)
    if err != nil {
        pm.Close()
        return nil, nil, nil, fmt.Errorf("create journal: %w", err)
    }

    // 4. Create CRS instance
    crsInstance := crs.New(nil)

    // 5. LOAD EXISTING BACKUP (if one exists)
    metadata, err := crsInstance.LoadCheckpointFromDisk(ctx, pm, projectHash, journal)
    if err != nil {
        slog.Error("failed to restore from backup, starting fresh",
            slog.String("project_hash", projectHash),
            slog.String("error", err.Error()),
        )
    } else if metadata != nil {
        slog.Info("restored CRS state from backup",
            slog.String("project_hash", projectHash),
            slog.Int64("generation", metadata.Generation),
            slog.Duration("backup_age", metadata.Age()),
        )
    } else {
        slog.Info("no previous backup found, starting fresh",
            slog.String("project_hash", projectHash),
        )
    }

    return crsInstance, journal, pm, nil
}
```

### Session Shutdown (Save Backup)

```go
func ShutdownSession(ctx context.Context, crsInstance *crs.CRS, journal *crs.BadgerJournal, pm *crs.PersistenceManager, projectHash string) error {
    defer pm.Close()
    defer journal.Close()

    // Save checkpoint to disk
    metadata, err := crsInstance.SaveCheckpointToDisk(ctx, pm, projectHash, journal)
    if err != nil {
        return fmt.Errorf("save checkpoint: %w", err)
    }

    slog.Info("session state saved",
        slog.String("project_hash", projectHash),
        slog.Int64("generation", metadata.Generation),
        slog.Int64("compressed_bytes", metadata.CompressedSize),
        slog.Float64("compression_ratio", metadata.CompressionRatio()),
    )

    return nil
}
```

### Complete Session Lifecycle

```go
func main() {
    ctx := context.Background()
    projectPath := "/path/to/my/project"

    // Initialize session and restore previous state
    crsInstance, journal, pm, err := InitializeSession(ctx, projectPath)
    if err != nil {
        log.Fatalf("Failed to initialize: %v", err)
    }

    projectHash := crs.ComputeProjectHash(projectPath)

    // ... do work with crsInstance ...

    // Option A: Direct Apply + manual journal append
    delta := &crs.ProofDelta{
        Updates: map[string]crs.ProofNumber{
            "node1": {Proof: 10, Status: crs.ProofStatusExpanded},
        },
    }
    if _, err := crsInstance.Apply(ctx, delta); err != nil {
        log.Printf("Apply failed: %v", err)
    }
    if err := journal.Append(ctx, delta); err != nil {
        log.Printf("Journal append failed: %v", err)
    }

    // Option B: Via Bridge (recommended — handles Apply + journal automatically)
    // bridge := integration.NewBridge(crsInstance, nil, integration.WithJournal(journal))
    // bridge.RunActivity(ctx, activity, input)
    //   → activity.Execute() → delta
    //   → crs.Apply(delta)        (automatic)
    //   → journal.Append(delta)   (automatic)

    // Shutdown and save state
    if err := ShutdownSession(ctx, crsInstance, journal, pm, projectHash); err != nil {
        log.Fatalf("Failed to save state: %v", err)
    }
}
```

### Checking Backup Status

```go
func CheckBackupStatus(projectPath string) error {
    projectHash := crs.ComputeProjectHash(projectPath)

    pm, err := crs.NewPersistenceManager(nil)
    if err != nil {
        return err
    }
    defer pm.Close()

    if !pm.HasBackup(projectHash) {
        fmt.Printf("No backup exists for project %s\n", projectHash)
        return nil
    }

    metadata, err := pm.GetBackupMetadata(projectHash)
    if err != nil {
        return fmt.Errorf("read metadata: %w", err)
    }

    fmt.Printf("Backup found:\n")
    fmt.Printf("  Project Hash: %s\n", metadata.ProjectHash)
    fmt.Printf("  Created At:   %s\n", time.UnixMilli(metadata.CreatedAt).Format(time.RFC3339))
    fmt.Printf("  Age:          %s\n", metadata.Age().Round(time.Second))
    fmt.Printf("  Generation:   %d\n", metadata.Generation)
    fmt.Printf("  Delta Count:  %d\n", metadata.DeltaCount)
    fmt.Printf("  Compressed:   %d bytes\n", metadata.CompressedSize)
    fmt.Printf("  Uncompressed: %d bytes\n", metadata.UncompressedSize)
    fmt.Printf("  Ratio:        %.1f%%\n", metadata.CompressionRatio()*100)

    return nil
}
```

### Error Handling

```go
import "errors"

func HandleRestoreError(err error) {
    switch {
    case errors.Is(err, crs.ErrBackupNotFound):
        // No backup exists - normal for first run
        log.Println("No previous backup found, starting fresh")

    case errors.Is(err, crs.ErrBackupCorrupted):
        // Backup failed integrity check
        log.Println("Backup corrupted, starting fresh")

    case errors.Is(err, crs.ErrBackupVersionMismatch):
        // BadgerDB version changed
        log.Println("Backup incompatible with current BadgerDB version")

    case errors.Is(err, crs.ErrBackupLockFailed):
        // Another process holds the lock
        log.Println("Could not acquire lock - another process may be using this project")

    default:
        log.Printf("Restore failed: %v", err)
    }
}
```

## Execute Loop Integration (CRS-WIRE-01)

CRS receives data from the execute phase through three paths:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                                                              │
│  Execute Loop                                                               │
│       │                                                                     │
│       ├──→ PATH 1: Execution Counting                                       │
│       │    recordTraceStep() → TraceStepToStepRecord() → CRS.RecordStep()  │
│       │    Feeds: CountToolExecutions() → circuit breaker count fallback    │
│       │                                                                     │
│       ├──→ PATH 2: Proof Numbers                                            │
│       │    updateProofNumber() → CRS.UpdateProofNumber()                   │
│       │    Feeds: CheckCircuitBreaker() → proof-based decisions            │
│       │                                                                     │
│       └──→ PATH 3: Learning Loop                                            │
│            emitCoordinatorEvent() → Coordinator.HandleEvent()              │
│              → Activities analyze outcome → produce Delta                  │
│              → Bridge.Apply(delta) → CRS indexes updated                   │
│              → Journal.Append(delta) → persisted for recovery              │
│            Feeds: Learned constraints, CDCL clauses, session restore       │
│                                                                              │
│  Events emitted: ToolSelected, ToolExecuted, ToolFailed,                   │
│                  CircuitBreaker, SemanticRepetition, CycleDetected,        │
│                  GraphRefreshed                                             │
│                                                                              │
│  Journal modes:                                                             │
│    enableSessionRestore=true  → persistent (disk, survives restart)        │
│    enableSessionRestore=false → in-memory (within-session learning only)   │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

For the full data flow documentation, see `docs/opensource/trace/mcts/02_crs_state_management.md`, section "Execute Loop Integration: How CRS Gets Its Data".

## Key Components

| File | Description |
|------|-------------|
| `crs.go` | Core CRS implementation with Apply/Snapshot |
| `journal.go` | BadgerJournal WAL with Backup/Restore |
| `trace_recorder.go` | TraceStep recording and TraceStepToStepRecord bridge |
| `persistence.go` | PersistenceManager for disk backup/restore |
| `hash.go` | Project hash utilities |
| `types.go` | Delta types, indexes, constraints |
| `serializer.go` | JSON export/import for portability |
| `history.go` | Delta history tracking (GR-35) |

## Observability

### Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `crs_backup_duration_seconds` | Histogram | Time to create backup |
| `crs_restore_duration_seconds` | Histogram | Time to restore from backup |
| `crs_backup_size_bytes` | Gauge | Compressed backup size |
| `crs_backup_operations_total` | Counter | Total backup operations |
| `crs_backup_age_seconds` | Gauge | Age of most recent backup |

### OpenTelemetry Spans

- `crs.Persistence.SaveBackup`
- `crs.Persistence.LoadBackup`
- `journal.Backup`
- `journal.Restore`
- `crs.SaveCheckpointToDisk`
- `crs.LoadCheckpointFromDisk`

## Testing

```bash
# Run persistence tests
go test ./services/trace/agent/mcts/crs/... -v -run "TestPersistence"

# Run hash tests
go test ./services/trace/agent/mcts/crs/... -v -run "TestProjectHash"

# Run integration test
go test ./services/trace/agent/mcts/crs/... -v -run "TestPersistenceIntegration"

# Run all CRS tests
go test ./services/trace/agent/mcts/crs/...
```
