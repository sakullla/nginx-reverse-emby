package localagent

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	embedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type localScopedFixture struct {
	store   *storage.GormStore
	db      *gorm.DB
	service *service.PluginService
	manager *service.PluginCapabilityManager
	source  *SyncSource
	tasks   *service.TaskService
	root    string
	agentID string
}

func newLocalScopedFixture(t *testing.T) localScopedFixture {
	t.Helper()
	root, agentID := t.TempDir(), "local-scoped"
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "panel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	plugins := service.NewPluginService(store, filepath.Join(root, "packages"))
	plugins.SetSecretVault(vault)
	host, err := pluginhost.New(filepath.Join(root, "controller-runtime"), nil, pluginhost.GRPCDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.NewPluginRuntimeHost(host, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	manager, err := service.NewPluginCapabilityManager(store, authz.NewManager(store, authz.Options{}), runtime, plugins)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetCoreResourceVault(vault)
	tasks := service.NewTaskService(service.TaskServiceConfig{TaskTTL: 5 * time.Second})
	t.Cleanup(func() { _ = tasks.Close() })
	manager.SetTaskService(tasks)
	source := NewSyncSource(store, agentID)
	source.SetPluginSecretSource(plugins)
	return localScopedFixture{store: store, db: db, service: plugins, manager: manager, source: source, tasks: tasks, root: root, agentID: agentID}
}

func (f localScopedFixture) startedLease(t *testing.T, revision int64) service.RemoteRevisionLease {
	t.Helper()
	now := time.Now().UTC()
	if err := f.store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{ID: "scoped-revision", Kind: "test", Status: storage.OperationStatusPending, PrimaryAgentID: f.agentID, CreatedAt: now, UpdatedAt: now},
		Revisions: []storage.AgentRevisionRow{{AgentID: f.agentID, Revision: revision, OperationID: "scoped-revision", State: storage.AgentRevisionStatePending, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 5, CreatedAt: now, UpdatedAt: now}},
		Pointers:  []storage.AgentRevisionPointerRow{{AgentID: f.agentID, DesiredRevision: revision, UpdatedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := f.store.ClaimLatestAgentRevision(t.Context(), storage.CoordinatorClaimRequest{AgentID: f.agentID, LeaseID: "scoped-lease", Now: now})
	if err != nil || claim.Lease == nil {
		t.Fatal("claim", err)
	}
	lease := service.RemoteRevisionLease{AgentID: f.agentID, Revision: revision, RetryCycle: claim.Lease.RetryCycle, Attempt: claim.Lease.Attempt, LeaseID: claim.Lease.LeaseID, DeadlineAt: claim.Lease.DeadlineAt, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 5}
	started, err := f.store.StartAgentRevisionAttempt(t.Context(), storage.CoordinatorStartRequest{Lease: *claim.Lease, GenerationID: embeddedGenerationID(lease), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	lease.DeadlineAt = started.Attempt.DeadlineAt
	return lease
}

func (f localScopedFixture) startRuntime(t *testing.T, artifacts localPluginArtifactResolver) (*Runtime, *embedded.Runtime) {
	t.Helper()
	inner, err := embedded.New(embedded.Config{AgentID: f.agentID, AgentName: f.agentID, DataDir: filepath.Join(f.root, "agent"), HeartbeatInterval: time.Hour}, syncSourceAdapter{source: f.source}, datasetRuntimeSink{})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { finished <- inner.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("embedded runtime did not stop")
		}
		_ = inner.Close()
	})
	return &Runtime{runtime: inner, source: f.source, agentID: f.agentID, pluginArtifacts: artifacts}, inner
}

func TestLocalActualCoreBindsStartedLeaseBeforePublication(t *testing.T) {
	f := newLocalScopedFixture(t)
	lease := f.startedLease(t, 7)
	outer, inner := f.startRuntime(t, nil)
	snapshot := storage.Snapshot{Revision: 7}
	wrong := lease
	wrong.LeaseID = "wrong-lease"
	if err := applyRevisionWithinLease(t.Context(), outer, snapshot, wrong); err == nil {
		t.Fatal("wrong lease reached runtime publication")
	}
	drain := inner.GenerationDrainSnapshot()
	for _, status := range drain.Generations {
		if status.GenerationID == drain.ActiveGenerationID && status.Revision == 7 {
			t.Fatal("rejected binder published the candidate generation")
		}
	}
	if err := applyRevisionWithinLease(t.Context(), outer, snapshot, lease); err != nil {
		t.Fatal(err)
	}
	row, found, err := f.store.GetCoordinatorRevision(t.Context(), f.agentID, 7)
	if err != nil || !found {
		t.Fatal(err)
	}
	active := inner.GenerationDrainSnapshot().ActiveGenerationID
	if row.GenerationID != embeddedGenerationID(lease) || row.RuntimeGenerationID != active || row.RuntimeSnapshotHash == "" || active == row.GenerationID {
		t.Fatal("actual core identity was not bound independently from attempt")
	}
}
