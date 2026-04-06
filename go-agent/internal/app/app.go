package app

import (
	"context"
	"runtime"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
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

type App struct {
	cfg          Config
	syncClient   SyncClient
	store        store.Store
	httpApplier  HTTPApplier
	certApplier  CertificateApplier
	l4Applier    L4Applier
	relayApplier RelayApplier
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
		Platform:       runtime.GOOS + "-" + runtime.GOARCH,
	}, nil)
	certManager, err := certs.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return newAppWithHTTPDeps(
		cfg,
		st,
		client,
		newHTTPRuntimeManager(),
		certManager,
		newL4RuntimeManager(),
		newRelayRuntimeManager(certManager),
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
	return newAppWithHTTPDeps(cfg, st, client, nil, certApplier, l4Applier, relayApplier)
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
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = config.Default().HeartbeatInterval
	}
	return &App{
		cfg:          cfg,
		store:        st,
		syncClient:   client,
		httpApplier:  httpApplier,
		certApplier:  certApplier,
		l4Applier:    l4Applier,
		relayApplier: relayApplier,
	}
}

func (a *App) Run(ctx context.Context) error {
	defer a.closeLocalRuntimes()

	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	runtimeState, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	desired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return err
	}
	if err := a.applyManagedCertificates(ctx, desired); err != nil {
		return a.recordRuntimeError(&runtimeState, err)
	}
	if err := a.applyHTTPRules(ctx, desired); err != nil {
		return a.recordRuntimeError(&runtimeState, err)
	}
	if err := a.applyRelayListeners(ctx, desired); err != nil {
		return a.recordRuntimeError(&runtimeState, err)
	}
	if err := a.applyL4Rules(ctx, desired); err != nil {
		return a.recordRuntimeError(&runtimeState, err)
	}

	if err := a.performSync(ctx); err != nil {
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
			a.performSync(ctx)
		}
	}
}

func (a *App) performSync(ctx context.Context) error {
	runtimeState, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	req := SyncRequest{CurrentRevision: currentRevisionFromMetadata(runtimeState.Metadata)}
	return a.syncOnce(ctx, req, &runtimeState)
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest, runtimeState *store.RuntimeState) error {
	snapshot, err := a.syncClient.Sync(ctx, req)
	if err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	existingDesired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	persistedSnapshot := mergeSnapshotPayload(snapshot, existingDesired)
	if err := a.store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	if err := a.applyManagedCertificates(ctx, snapshot); err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	if err := a.applyHTTPRules(ctx, snapshot); err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	if err := a.applyRelayListeners(ctx, snapshot); err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	if err := a.applyL4Rules(ctx, snapshot); err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	return a.clearRuntimeError(runtimeState)
}

func (a *App) recordRuntimeError(state *store.RuntimeState, syncErr error) error {
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	if err := a.store.SaveRuntimeState(*state); err != nil {
		return syncErr
	}
	return syncErr
}

func (a *App) clearRuntimeError(state *store.RuntimeState) error {
	state.Metadata = ensureMetadata(state.Metadata)
	delete(state.Metadata, "last_sync_error")
	if err := a.store.SaveRuntimeState(*state); err != nil {
		return err
	}
	return nil
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func currentRevisionFromMetadata(meta map[string]string) int {
	if meta == nil {
		return 0
	}
	if raw, ok := meta["current_revision"]; ok {
		if val, err := strconv.Atoi(raw); err == nil {
			return val
		}
	}
	return 0
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
