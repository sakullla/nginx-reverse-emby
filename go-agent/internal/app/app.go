package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	stdruntime "runtime"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	platformlinux "github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform/linux"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
	agentupdate "github.com/sakullla/nginx-reverse-emby/go-agent/internal/update"
)

type Config = config.Config
type Snapshot = store.Snapshot
type SyncRequest = agentsync.SyncRequest

type SyncClient interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
}

type CertificateApplier interface {
	Apply(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
}

type HTTPApplier interface {
	Apply(context.Context, []model.HTTPRule) error
	Close() error
}

type Updater interface {
	Stage(context.Context, model.VersionPackage) (string, error)
	Activate(stagedPath string, desiredVersion string) error
}

type App struct {
	cfg          Config
	syncClient   SyncClient
	store        store.Store
	httpApplier  HTTPApplier
	certApplier  CertificateApplier
	l4Applier    L4Applier
	relayApplier RelayApplier
	updater      Updater
	runtime      *agentruntime.Runtime
}

func New(cfg Config) (*App, error) {
	st, err := store.NewFilesystem(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	client := agentsync.NewClient(agentsync.ClientConfig{
		MasterURL:      cfg.MasterURL,
		AgentToken:     cfg.AgentToken,
		AgentID:        cfg.AgentID,
		AgentName:      cfg.AgentName,
		CurrentVersion: cfg.CurrentVersion,
		Platform:       stdruntime.GOOS + "-" + stdruntime.GOARCH,
	}, nil)
	certManager, err := certs.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return newAppWithAllDeps(
		cfg,
		st,
		client,
		newHTTPRuntimeManager(),
		certManager,
		newL4RuntimeManager(),
		newRelayRuntimeManager(certManager),
		agentupdate.NewManager(
			cfg.DataDir,
			executablePath,
			os.Args,
			os.Environ(),
			platformlinux.ExecReplacement,
			nil,
		),
	), nil
}

func newAppWithDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
) *App {
	return newAppWithAllDeps(cfg, st, client, nil, certApplier, l4Applier, relayApplier, nil)
}

func newAppWithHTTPDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	httpApplier HTTPApplier,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
) *App {
	return newAppWithAllDeps(cfg, st, client, httpApplier, certApplier, l4Applier, relayApplier, nil)
}

func newAppWithAllDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	httpApplier HTTPApplier,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
	updater Updater,
) *App {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = config.Default().HeartbeatInterval
	}
	app := &App{
		cfg:          cfg,
		store:        st,
		syncClient:   client,
		httpApplier:  httpApplier,
		certApplier:  certApplier,
		l4Applier:    l4Applier,
		relayApplier: relayApplier,
		updater:      updater,
	}
	app.runtime = agentruntime.NewWithActivator(app.activateSnapshot)
	return app
}

func (a *App) Run(ctx context.Context) error {
	defer a.closeLocalRuntimes()

	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	if err := a.runtime.Apply(ctx, Snapshot{}, applied); err != nil {
		return a.recordRuntimeError(err)
	}

	if err := a.performSync(ctx); err != nil {
		if errors.Is(err, agentupdate.ErrRestartRequested) {
			return nil
		}
		if applied.DesiredVersion == "" {
			return err
		}
	}

	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.performSync(ctx); errors.Is(err, agentupdate.ErrRestartRequested) {
				return nil
			}
		}
	}
}

func (a *App) performSync(ctx context.Context) error {
	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	req := SyncRequest{CurrentRevision: int(applied.Revision)}
	return a.syncOnce(ctx, req)
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest) error {
	snapshot, err := a.syncClient.Sync(ctx, req)
	if err != nil {
		return a.recordRuntimeError(err)
	}
	existingDesired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return a.recordRuntimeError(err)
	}
	persistedSnapshot := mergeSnapshotPayload(snapshot, existingDesired)
	if err := a.store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.handlePendingUpdate(ctx, snapshot); err != nil {
		return err
	}
	previousApplied := a.runtime.ActiveSnapshot()
	candidateApplied := mergeSnapshotPayload(snapshot, previousApplied)
	if err := a.runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.store.SaveAppliedSnapshot(candidateApplied); err != nil {
		a.rollbackRuntime(ctx, previousApplied)
		return a.recordPersistedRuntimeError(err)
	}
	if err := a.persistRuntimeState(true); err != nil {
		a.rollbackRuntime(ctx, previousApplied)
		_ = a.store.SaveAppliedSnapshot(previousApplied)
		return a.recordPersistedRuntimeError(err)
	}
	return nil
}

func (a *App) recordRuntimeError(syncErr error) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func (a *App) persistRuntimeState(clearLastSyncError bool) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	if clearLastSyncError {
		delete(state.Metadata, "last_sync_error")
	}
	if err := a.store.SaveRuntimeState(state); err != nil {
		return err
	}
	return nil
}

func (a *App) recordPersistedRuntimeError(syncErr error) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func (a *App) runtimeStateForPersistence() (store.RuntimeState, error) {
	existing, err := a.store.LoadRuntimeState()
	if err != nil {
		return store.RuntimeState{}, err
	}

	current := a.runtime.State()
	state := existing
	state.Status = current.Status
	state.CurrentRevision = current.CurrentRevision
	state.Metadata = ensureMetadata(existing.Metadata)
	for key, value := range current.Metadata {
		state.Metadata[key] = value
	}
	return state, nil
}

func (a *App) applyManagedCertificates(ctx context.Context, snapshot Snapshot) error {
	if a.certApplier == nil {
		return nil
	}
	if snapshot.Certificates == nil && snapshot.CertificatePolicies == nil {
		return nil
	}
	return a.certApplier.Apply(ctx, snapshot.Certificates, snapshot.CertificatePolicies)
}

func (a *App) applyHTTPRules(ctx context.Context, snapshot Snapshot) error {
	if a.httpApplier == nil || snapshot.Rules == nil {
		return nil
	}
	return a.httpApplier.Apply(ctx, snapshot.Rules)
}

func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	merged := next
	if next.Rules == nil {
		merged.Rules = previous.Rules
	}
	if next.L4Rules == nil {
		merged.L4Rules = previous.L4Rules
	}
	if next.RelayListeners == nil {
		merged.RelayListeners = previous.RelayListeners
	}
	if next.Certificates == nil {
		merged.Certificates = previous.Certificates
	}
	if next.CertificatePolicies == nil {
		merged.CertificatePolicies = previous.CertificatePolicies
	}
	return merged
}

func (a *App) activateSnapshot(ctx context.Context, previous, next Snapshot) error {
	if certificatesChanged(previous, next) {
		if err := a.applyManagedCertificates(ctx, next); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(previous.Rules, next.Rules) {
		if err := a.applyHTTPRules(ctx, next); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(previous.RelayListeners, next.RelayListeners) {
		if err := a.applyRelayListeners(ctx, next); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(previous.L4Rules, next.L4Rules) {
		if err := a.applyL4Rules(ctx, next); err != nil {
			return err
		}
	}
	return nil
}

func certificatesChanged(previous, next Snapshot) bool {
	return !reflect.DeepEqual(previous.Certificates, next.Certificates) ||
		!reflect.DeepEqual(previous.CertificatePolicies, next.CertificatePolicies)
}

func (a *App) rollbackRuntime(ctx context.Context, previousApplied Snapshot) {
	currentApplied := a.runtime.ActiveSnapshot()
	if reflect.DeepEqual(currentApplied, previousApplied) {
		return
	}
	_ = a.runtime.Apply(ctx, currentApplied, previousApplied)
}

func (a *App) applyL4Rules(ctx context.Context, snapshot Snapshot) error {
	if a.l4Applier == nil || snapshot.L4Rules == nil {
		return nil
	}
	return a.l4Applier.Apply(ctx, snapshot.L4Rules)
}

func (a *App) applyRelayListeners(ctx context.Context, snapshot Snapshot) error {
	if a.relayApplier == nil || snapshot.RelayListeners == nil {
		return nil
	}
	return a.relayApplier.Apply(ctx, snapshot.RelayListeners)
}

func (a *App) closeLocalRuntimes() {
	if a.httpApplier != nil {
		_ = a.httpApplier.Close()
	}
	if a.relayApplier != nil {
		_ = a.relayApplier.Close()
	}
	if a.l4Applier != nil {
		_ = a.l4Applier.Close()
	}
}

func (a *App) handlePendingUpdate(ctx context.Context, snapshot Snapshot) error {
	if !agentupdate.NeedsUpdate(a.cfg.CurrentVersion, snapshot.DesiredVersion) {
		return nil
	}
	if !agentupdate.HasValidPackage(snapshot.VersionPackage) {
		return nil
	}
	if a.updater == nil {
		return a.recordRuntimeError(errors.New("updater unavailable"))
	}

	stagedPath, err := a.updater.Stage(ctx, *snapshot.VersionPackage)
	if err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.updater.Activate(stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, agentupdate.ErrRestartRequested) {
			return err
		}
		return a.recordRuntimeError(err)
	}
	return agentupdate.ErrRestartRequested
}
