package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	appVersion = "dev"
	buildTime  = "dev"
	goVersion  = "dev"
)

type localAgentRuntime interface {
	Start(context.Context) error
	ConfigureTunnelPKI(localagent.TunnelPKIService) error
	ApplyRevision(context.Context, storage.Snapshot) error
	ApplyRevisionWithDrainTimeout(context.Context, storage.Snapshot, time.Duration) error
	DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error)
}

type contextRuntimeCloser interface {
	Close(context.Context) error
}

func closeRuntimeWithRetry(runtime contextRuntimeCloser) error {
	if runtime == nil {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = runtime.Close(ctx)
		cancel()
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runMain(args []string) error {
	migrateCommand, err := parseMigrateStorageCommand(args)
	if err != nil {
		return err
	}
	if migrateCommand != nil {
		return runMigrateStorageCommand(context.Background(), *migrateCommand)
	}
	return runControlPlaneFromEnv()
}

var runControlPlaneFromEnv = func() error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	cfg.AppVersion = appVersion
	cfg.BuildTime = buildTime
	cfg.GoVersion = goVersion
	logPanelTokenWarning(log.Default(), cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := initializeControlPlane(ctx, cfg); err != nil {
		return err
	}
	service.ManagedCertificateDispatcher().SetBaseContext(ctx)
	startTrafficCleanupLoop(ctx, cfg, nil)
	startRevisionRetentionLoop(ctx, cfg, nil)

	var dnsTokenResolver *service.PluginDNSTokenResolver
	application, err := newControlPlaneApp(cfg, nil, func(resolver *service.PluginDNSTokenResolver) {
		dnsTokenResolver = resolver
	})
	if err != nil {
		return err
	}
	startManagedCertificateAutoRenewLoop(ctx, cfg, nil, dnsTokenResolver)
	// Wire the background signer before startup recovery so re-dispatched "issuing" certificates
	// have a real sign function (otherwise Submit is a safe no-op). Each issuance opens a fresh
	// store, decoupled from the HTTP request or renewal-loop store lifecycles.
	service.ManagedCertificateDispatcher().SetSignFunc(service.ManagedCertificateBackgroundSignerWithDNSTokenResolver(cfg, func() (storage.Store, error) {
		return openConfiguredStore(cfg)
	}, nil, dnsTokenResolver.Resolve))
	startManagedCertificateIssuanceRecovery(ctx, cfg, nil)
	if err := application.Run(ctx); err != nil {
		return err
	}
	// Graceful shutdown: application.Run returns once the shutdown context is cancelled,
	// but background issuance goroutines may still be finishing. Give them a bounded window
	// to observe cancellation and persist their outcome instead of leaving them to race
	// process exit; log if any are still outstanding when the window closes.
	if !service.ManagedCertificateDispatcher().WaitWithTimeout(managedCertificateIssuanceShutdownTimeout) {
		log.Println("[cert] shutdown: timed out waiting for in-flight certificate issuance to finish")
	}
	return nil
}

type migrateStorageCommand struct {
	FromDriver   string
	FromDSN      string
	FromDataRoot string
	ToDriver     string
	ToDSN        string
	ToDataRoot   string
}

func parseMigrateStorageCommand(args []string) (*migrateStorageCommand, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if args[0] != "migrate-storage" {
		return nil, fmt.Errorf("unknown command %q", args[0])
	}

	fs := flag.NewFlagSet("migrate-storage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cmd := migrateStorageCommand{}
	fs.StringVar(&cmd.FromDriver, "from-driver", "", "source database driver")
	fs.StringVar(&cmd.FromDSN, "from-dsn", "", "source database DSN")
	fs.StringVar(&cmd.FromDataRoot, "from-data-root", "", "source panel data root for managed certificate material")
	fs.StringVar(&cmd.ToDriver, "to-driver", "", "target database driver")
	fs.StringVar(&cmd.ToDSN, "to-dsn", "", "target database DSN")
	fs.StringVar(&cmd.ToDataRoot, "to-data-root", "", "target panel data root for managed certificate material")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cmd.FromDriver) == "" {
		return nil, fmt.Errorf("missing --from-driver")
	}
	if strings.TrimSpace(cmd.FromDSN) == "" {
		return nil, fmt.Errorf("missing --from-dsn")
	}
	if strings.TrimSpace(cmd.ToDriver) == "" {
		return nil, fmt.Errorf("missing --to-driver")
	}
	if strings.TrimSpace(cmd.ToDSN) == "" {
		return nil, fmt.Errorf("missing --to-dsn")
	}
	cmd.FromDriver = normalizeStorageDriver(cmd.FromDriver)
	cmd.ToDriver = normalizeStorageDriver(cmd.ToDriver)
	cmd.FromDSN = strings.TrimSpace(cmd.FromDSN)
	cmd.ToDSN = strings.TrimSpace(cmd.ToDSN)
	cmd.FromDataRoot = strings.TrimSpace(cmd.FromDataRoot)
	cmd.ToDataRoot = strings.TrimSpace(cmd.ToDataRoot)
	if cmd.FromDriver == cmd.ToDriver && cmd.FromDSN == cmd.ToDSN {
		return nil, fmt.Errorf("source and target storage must be different")
	}
	return &cmd, nil
}

func logPanelTokenWarning(logger *log.Logger, cfg config.Config) {
	if strings.TrimSpace(cfg.PanelToken) != "" {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	logger.Println("[security] panel token is empty; panel API authentication is disabled")
}

var newHandler = func(cfg config.Config) (http.Handler, error) {
	return httpapi.NewRouter(httpapi.Dependencies{Config: cfg})
}

var newHandlerWithDependencies = func(cfg config.Config, deps httpapi.Dependencies) (http.Handler, error) {
	deps.Config = cfg
	return httpapi.NewRouter(deps)
}

var newLocalAgentRuntime = func(cfg config.Config, store localagent.Store) (localAgentRuntime, error) {
	return localagent.NewRuntime(cfg, store)
}

var openConfiguredStore = storage.NewConfiguredStore

var openStore = storage.NewStore

var runMigrateStorageCommand = func(ctx context.Context, cmd migrateStorageCommand) error {
	sourceDataRoot := migrationStoreDataRoot(cmd.FromDriver, cmd.FromDSN, cmd.FromDataRoot)
	targetDataRoot := migrationStoreDataRoot(cmd.ToDriver, cmd.ToDSN, cmd.ToDataRoot)
	if targetDataRoot == "" && strings.TrimSpace(cmd.ToDataRoot) == "" {
		targetDataRoot = sourceDataRoot
	}

	source, err := openStore(storage.StoreConfig{
		Driver:              cmd.FromDriver,
		DSN:                 cmd.FromDSN,
		DataRoot:            sourceDataRoot,
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: false,
	})
	if err != nil {
		return fmt.Errorf("open source storage: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()

	target, err := openStore(storage.StoreConfig{
		Driver:              cmd.ToDriver,
		DSN:                 cmd.ToDSN,
		DataRoot:            targetDataRoot,
		TrafficStatsEnabled: true,
	})
	if err != nil {
		return fmt.Errorf("open target storage: %w", err)
	}
	defer func() {
		_ = target.Close()
	}()

	return storage.CopyDefaultMigrationRows(ctx, source, target)
}

func migrationStoreDataRoot(driver, dsn, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if !strings.EqualFold(strings.TrimSpace(driver), "sqlite") {
		return ""
	}
	path := strings.TrimSpace(dsn)
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return ""
	}
	dir := filepath.Dir(path)
	if dir == "." {
		return ""
	}
	return dir
}

func normalizeStorageDriver(driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		return "sqlite"
	}
	return driver
}

func guardLegacyNonSQLiteState(dataDir string) error {
	dbPath := filepath.Join(dataDir, "panel.db")
	if _, err := os.Stat(dbPath); err == nil {
		return nil
	}

	if v := strings.TrimSpace(os.Getenv("PANEL_STORAGE_BACKEND")); v != "" && !strings.EqualFold(v, "sqlite") {
		return fmt.Errorf("detected legacy storage backend %q in PANEL_STORAGE_BACKEND; migrate data to SQLite before starting the pure-Go control plane", v)
	}

	legacyMarkers := []string{
		filepath.Join(dataDir, "state.json"),
		filepath.Join(dataDir, "agents.json"),
		filepath.Join(dataDir, "prisma", "schema.prisma"),
	}
	for _, p := range legacyMarkers {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("detected legacy state file %q; migrate data to panel.db before starting the pure-Go control plane", p)
		}
	}

	entries, err := os.ReadDir(dataDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".db") && !strings.EqualFold(name, "panel.db") {
				return fmt.Errorf("detected legacy database file %q; migrate data to panel.db before starting the pure-Go control plane", name)
			}
		}
	}

	return nil
}

var initializeControlPlane = func(ctx context.Context, cfg config.Config) error {
	if databaseDriverUsesSQLite(cfg.DatabaseDriver) {
		if err := guardLegacyNonSQLiteState(cfg.DataDir); err != nil {
			return err
		}
	}
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	// PKI bootstrap is intentionally owned by newControlPlaneApp. A damaged or
	// unavailable internal vault must not prevent the existing token-authenticated
	// control listener from starting; the app installs PKI dependencies only when
	// bootstrap succeeds and otherwise exposes PKI routes as unavailable.
	return nil
}

func databaseDriverUsesSQLite(driver string) bool {
	driver = strings.ToLower(strings.TrimSpace(driver))
	return driver == "" || driver == "sqlite"
}

var runManagedCertificateRenewalPass = func(ctx context.Context, cfg config.Config, resolver *service.PluginDNSTokenResolver) error {
	ctx = service.WithSystemMutationPrincipal(ctx, "system:managed-certificate-renewal")
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	return service.NewCertificateServiceWithDNSTokenResolver(cfg, store, resolver.Resolve).RunRenewalPass(ctx)
}

var managedCertificateAutoRenewInitialDelay = 10 * time.Second
var managedCertificateIssuanceShutdownTimeout = 30 * time.Second
var trafficCleanupInitialDelay = 30 * time.Second
var revisionRetentionInterval = 24 * time.Hour
var revisionRetentionStartupRetryInterval = 5 * time.Second
var revisionRetentionStartupMaxAttempts = 6
var pluginRuntimeRetentionAge = 24 * time.Hour

func startManagedCertificateAutoRenewLoop(ctx context.Context, cfg config.Config, logger *log.Logger, resolver *service.PluginDNSTokenResolver) {
	if !cfg.ManagedDNSCertificatesEnabled || cfg.ManagedCertificateRenewInterval <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}

	go func() {
		initialTimer := time.NewTimer(managedCertificateAutoRenewInitialDelay)
		defer initialTimer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-initialTimer.C:
			if err := runManagedCertificateRenewalPass(ctx, cfg, resolver); err != nil {
				logger.Printf("[cert] initial auto renew cycle failed: %v", err)
			}
		}

		ticker := time.NewTicker(cfg.ManagedCertificateRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runManagedCertificateRenewalPass(ctx, cfg, resolver); err != nil {
					logger.Printf("[cert] managed certificate auto renew cycle failed: %v", err)
				}
			}
		}
	}()
}

var runManagedCertificateIssuanceRecovery = func(ctx context.Context, cfg config.Config) (int, error) {
	ctx = service.WithSystemMutationPrincipal(ctx, "system:managed-certificate-recovery")
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = store.Close()
	}()
	return service.ManagedCertificateDispatcher().Recover(ctx, store)
}

func startManagedCertificateIssuanceRecovery(ctx context.Context, cfg config.Config, logger *log.Logger) {
	if !cfg.ManagedDNSCertificatesEnabled {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	dispatched, err := runManagedCertificateIssuanceRecovery(ctx, cfg)
	if err != nil {
		logger.Printf("[cert] startup issuance recovery failed: %v", err)
		return
	}
	if dispatched > 0 {
		logger.Printf("[cert] startup issuance recovery re-dispatched %d in-flight certificate(s)", dispatched)
	}
}

var runTrafficCleanupPass = func(ctx context.Context, cfg config.Config) error {
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	trafficCfg, err := service.NewTrafficServiceConfig(cfg.TrafficStatsEnabled, cfg.Timezone)
	if err != nil {
		return err
	}
	_, err = service.NewTrafficService(trafficCfg, store).CleanupAll(ctx)
	return err
}

func startTrafficCleanupLoop(ctx context.Context, cfg config.Config, logger *log.Logger) {
	if !cfg.TrafficStatsEnabled || cfg.TrafficCleanupInterval <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}

	go func() {
		initialTimer := time.NewTimer(trafficCleanupInitialDelay)
		defer initialTimer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-initialTimer.C:
			if err := runTrafficCleanupPass(ctx, cfg); err != nil {
				logger.Printf("[traffic] initial cleanup cycle failed: %v", err)
			}
		}

		ticker := time.NewTicker(cfg.TrafficCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runTrafficCleanupPass(ctx, cfg); err != nil {
					logger.Printf("[traffic] cleanup cycle failed: %v", err)
				}
			}
		}
	}()
}

type revisionRetentionStore interface {
	PruneRevisionHistory(context.Context, storage.RevisionRetentionPolicy) (storage.RevisionPruneResult, error)
	ListPluginRuntimeDirectoryReferences(context.Context) ([]storage.PluginRuntimeDirectoryReference, error)
	Close() error
}

type orphanedPluginOperationRetentionStore interface {
	FailOrphanedPluginOperations(context.Context, time.Time, time.Time) (int64, error)
}

var openRevisionRetentionStore = func(cfg config.Config) (revisionRetentionStore, error) {
	return openConfiguredStore(cfg)
}

var runRevisionRetentionPass = func(ctx context.Context, cfg config.Config) error {
	store, err := openRevisionRetentionStore(cfg)
	if err != nil {
		return fmt.Errorf("open revision retention store: %w", err)
	}
	now := time.Now().UTC()
	_, pruneErr := store.PruneRevisionHistory(ctx, storage.RevisionRetentionPolicy{Now: now})
	var orphanedOperationErr error
	if orphanedStore, ok := store.(orphanedPluginOperationRetentionStore); ok {
		_, orphanedOperationErr = orphanedStore.FailOrphanedPluginOperations(ctx, now.Add(-storage.DefaultOrphanedPluginOperationGrace), now)
		if orphanedOperationErr != nil {
			orphanedOperationErr = fmt.Errorf("fail orphaned plugin operations: %w", orphanedOperationErr)
		}
	}
	references, referenceErr := store.ListPluginRuntimeDirectoryReferences(ctx)
	closeErr := store.Close()
	if pruneErr != nil {
		pruneErr = fmt.Errorf("prune revision history: %w", pruneErr)
	}
	if referenceErr != nil {
		referenceErr = fmt.Errorf("list protected plugin runtimes: %w", referenceErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close revision retention store: %w", closeErr)
	}
	var runtimePruneErr error
	if referenceErr == nil {
		protected := make([]pluginhost.RuntimeDirectoryReference, 0, len(references))
		for _, reference := range references {
			protected = append(protected, pluginhost.RuntimeDirectoryReference{InstanceID: reference.InstanceID, Generation: reference.Generation})
		}
		age := pluginRuntimeRetentionAge
		if age <= 0 {
			age = 24 * time.Hour
		}
		_, runtimePruneErr = pluginhost.PruneRuntimeDirectories(filepath.Join(cfg.DataDir, "plugins", "rpc-runtime"), protected, now.Add(-age))
		if runtimePruneErr != nil {
			runtimePruneErr = fmt.Errorf("prune plugin runtime directories: %w", runtimePruneErr)
		}
	}
	return errors.Join(pruneErr, orphanedOperationErr, referenceErr, runtimePruneErr, closeErr)
}

func startRevisionRetentionLoop(ctx context.Context, cfg config.Config, logger *log.Logger) <-chan struct{} {
	done := make(chan struct{})
	if logger == nil {
		logger = log.Default()
	}
	interval := revisionRetentionInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := runRevisionRetentionStartupPass(ctx, cfg); err != nil && ctx.Err() == nil {
			logger.Printf("[revision-retention] startup pass failed: %v", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runRevisionRetentionPass(ctx, cfg); err != nil && ctx.Err() == nil {
					logger.Printf("[revision-retention] periodic pass failed: %v", err)
				}
			}
		}
	}()
	return done
}

func runRevisionRetentionStartupPass(ctx context.Context, cfg config.Config) error {
	attempts := revisionRetentionStartupMaxAttempts
	if attempts <= 0 {
		attempts = 6
	}
	retryInterval := revisionRetentionStartupRetryInterval
	if retryInterval <= 0 {
		retryInterval = 5 * time.Second
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := runRevisionRetentionPass(ctx, cfg); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts-1 {
			break
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func newControlPlaneApp(cfg config.Config, logger *log.Logger, bindDNSTokenResolver func(*service.PluginDNSTokenResolver)) (*app.App, error) {
	if logger == nil {
		logger = log.Default()
	}
	serviceStore, err := openConfiguredStore(cfg)
	if err != nil {
		return nil, err
	}
	rpcProcessHost, err := pluginhost.New(filepath.Join(cfg.DataDir, "plugins", "rpc-runtime"), nil, pluginhost.GRPCDialer{}, logger.Writer())
	if err != nil {
		_ = serviceStore.Close()
		return nil, err
	}
	rpcRuntimeHost, err := service.NewPluginRuntimeHost(rpcProcessHost, serviceStore)
	if err != nil {
		_ = rpcProcessHost.Close(context.Background())
		_ = serviceStore.Close()
		return nil, err
	}
	dnsTokenResolver := service.NewPluginDNSTokenResolver(rpcProcessHost, cfg.DDNS.Token)
	if bindDNSTokenResolver != nil {
		bindDNSTokenResolver(dnsTokenResolver)
	}

	systemSvc := service.NewSystemService(cfg, serviceStore)
	agentSvc := service.NewAgentService(cfg, serviceStore)
	trafficCfg, err := service.NewTrafficServiceConfig(cfg.TrafficStatsEnabled, cfg.Timezone)
	if err != nil {
		_ = serviceStore.Close()
		return nil, err
	}
	trafficSvc := service.NewTrafficService(trafficCfg, serviceStore)
	agentSvc.SetTrafficService(trafficSvc)
	ddnsSvc := service.NewDDNSService(cfg, serviceStore, nil, nil)
	ddnsSvc.SetTokenResolver(dnsTokenResolver.Resolve, dnsTokenResolver.Ready)
	agentSvc.SetDDNSReconciler(ddnsSvc)
	revisionReconciler := service.NewRevisionReconciler(agentSvc.RevisionAPI(), logger)
	ruleSvc := service.NewRuleService(cfg, serviceStore)
	ruleSvc.SetDNSTokenProviderReady(func() bool {
		return cfg.ManagedDNSCertificatesEnabled && dnsTokenResolver.Ready()
	})
	l4Svc := service.NewL4RuleService(cfg, serviceStore)
	versionSvc := service.NewVersionPolicyService(serviceStore)
	egressSvc := service.NewEgressProfileServiceWithConfig(cfg, serviceStore)
	relaySvc := service.NewRelayListenerService(cfg, serviceStore)
	certSvc := service.NewCertificateServiceWithDNSTokenResolver(cfg, serviceStore, dnsTokenResolver.Resolve)
	taskSvc := service.NewTaskService(service.TaskServiceConfig{})
	pkiProxy := service.NewDegradedPKIService(service.ErrPKIRuntimeUnavailable)
	pkiSupervisor := newControlPlanePKISupervisor(cfg, serviceStore, taskSvc, relaySvc, pkiProxy, logger)
	startupPKICtx, cancelStartupPKI := context.WithTimeout(context.Background(), controlPlanePKIStartupWait)
	pkiErr := pkiSupervisor.Bootstrap(startupPKICtx)
	cancelStartupPKI()
	if pkiErr != nil {
		logger.Printf("[pki] runtime unavailable; existing token control protocol remains online and PKI mutations are disabled: %v", pkiErr)
	}
	agentSvc.SetPKIController(pkiProxy)
	agentSvc.SetPKIAgentRevoker(pkiProxy.RevokeAgentForDeletion)
	relaySvc.SetPKIListenerRevoker(pkiProxy.RevokeListenerForDeletion)
	var pkiHTTPService httpapi.PKIService = pkiProxy

	var runtimeStore *storage.GormStore
	closeServices := func() error {
		revisionReconciler.Close()
		ddnsSvc.Close()
		pkiErr := pkiSupervisor.Close()
		taskErr := taskSvc.Close()
		var runtimeErr error
		if runtimeStore != nil {
			runtimeErr = runtimeStore.Close()
		}
		rpcErr := closeRuntimeWithRetry(rpcRuntimeHost)
		storeErr := serviceStore.Close()
		return errors.Join(pkiErr, taskErr, runtimeErr, rpcErr, storeErr)
	}

	var runLocalAgent func(context.Context) error
	if cfg.EnableLocalAgent {
		runtimeStore, err = openConfiguredStore(cfg)
		if err != nil {
			_ = closeServices()
			return nil, err
		}
		runtime, runtimeErr := newLocalAgentRuntime(cfg, runtimeStore)
		if runtimeErr != nil {
			_ = closeServices()
			return nil, runtimeErr
		}
		if runtimeErr := runtime.ConfigureTunnelPKI(pkiProxy); runtimeErr != nil {
			_ = closeServices()
			return nil, runtimeErr
		}
		if runtimeWithSource, ok := runtime.(interface{ SyncSource() *localagent.SyncSource }); ok {
			runtimeWithSource.SyncSource().SetTrafficService(cfg.TrafficStatsEnabled, trafficSvc)
			runtimeWithSource.SyncSource().SetDDNSReconciler(ddnsSvc.ReconcileAfterHeartbeat)
		}
		revisionWorker, workerErr := localagent.NewRevisionWorker(cfg.LocalAgentID, agentSvc.RevisionAPI(), serviceStore, runtime)
		if workerErr != nil {
			_ = closeServices()
			return nil, workerErr
		}
		localTaskSession := localagent.NewLocalTaskSessionWithDiagnostics(cfg.LocalAgentID, taskSvc, serviceStore, runtime)
		if registerErr := localTaskSession.Register(); registerErr != nil {
			log.Printf("[local-agent] failed to register local task session: %v", registerErr)
		}
		runLocalAgent = func(ctx context.Context) error {
			return localagent.RunRevisionRuntime(ctx, runtime, revisionWorker)
		}
	}

	handler, err := newHandlerWithDependencies(cfg, httpapi.Dependencies{
		SystemService:        systemSvc,
		AgentService:         agentSvc,
		RuleService:          ruleSvc,
		L4RuleService:        l4Svc,
		VersionPolicyService: versionSvc,
		EgressProfileService: egressSvc,
		RelayListenerService: relaySvc,
		CertificateService:   certSvc,
		BackupService:        service.NewBackupService(cfg, serviceStore),
		PKIService:           pkiHTTPService,
		TaskService:          taskSvc,
		TrafficService:       trafficSvc,
		PluginRuntimeHost:    rpcRuntimeHost,
	})
	if err != nil {
		_ = closeServices()
		return nil, err
	}
	closeApp := closeServices
	if cleanup, ok := handler.(interface{ Close() error }); ok {
		nextCloseApp := closeApp
		closeApp = func() error {
			handlerErr := cleanup.Close()
			restErr := nextCloseApp()
			return errors.Join(handlerErr, restErr)
		}
	}
	// Both background services are required in remote-only deployments too:
	// remote heartbeats feed DDNS and coordinator deadlines must advance even
	// when an agent disappears before its next pull.
	ddnsSvc.Start()
	revisionReconciler.Start()

	controlPlaneApp := app.New(cfg, handler, logger, runLocalAgent)
	controlPlaneApp.SetPKIMaintainer(pkiSupervisor.Run)
	controlPlaneApp.SetCleanup(closeApp)
	return controlPlaneApp, nil
}

type controlPlanePKIRuntime struct {
	service *service.InternalPKIService
	lease   *service.PKILeaseService
	vault   *service.PKIVault
}

type controlPlanePKIRuntimeFactory func(
	context.Context,
	config.Config,
	*storage.GormStore,
	*service.TaskService,
	service.PKIActivationFinalizer,
	*log.Logger,
	string,
) (controlPlanePKIRuntime, error)

var (
	newControlPlanePKIRuntimeFactory controlPlanePKIRuntimeFactory = newControlPlanePKIRuntime
	controlPlanePKIStartupWait                                     = 5 * time.Second
	controlPlanePKIAttemptTimeout                                  = 10 * time.Second
	controlPlanePKIRetryInterval                                   = time.Second
)

type controlPlanePKIAttemptResult struct {
	runtime controlPlanePKIRuntime
	err     error
}

type controlPlanePKIAttempt struct {
	result <-chan controlPlanePKIAttemptResult
	cancel context.CancelFunc
}

// controlPlanePKISupervisor owns a stable proxy and exactly one bootstrap
// attempt at a time. Lease contention and bounded-startup timeouts therefore
// degrade only tunnel PKI; the existing HTTP listener can start immediately,
// while a follower keeps retrying and promotes in place after takeover.
type controlPlanePKISupervisor struct {
	cfg        config.Config
	store      *storage.GormStore
	tasks      *service.TaskService
	activation service.PKIActivationFinalizer
	proxy      *service.DegradedPKIService
	logger     *log.Logger
	instanceID string

	mu      sync.Mutex
	runtime *controlPlanePKIRuntime
	attempt *controlPlanePKIAttempt
	closed  bool
}

func newControlPlanePKISupervisor(
	cfg config.Config,
	store *storage.GormStore,
	tasks *service.TaskService,
	activation service.PKIActivationFinalizer,
	proxy *service.DegradedPKIService,
	logger *log.Logger,
) *controlPlanePKISupervisor {
	return &controlPlanePKISupervisor{
		cfg: cfg, store: store, tasks: tasks, activation: activation, proxy: proxy,
		logger: logger, instanceID: controlPlanePKIInstanceID(),
	}
}

func (s *controlPlanePKISupervisor) Bootstrap(ctx context.Context) error {
	attempt := s.startAttempt(ctx)
	if attempt == nil {
		return service.ErrPKIRuntimeUnavailable
	}
	select {
	case result := <-attempt.result:
		return s.completeAttempt(attempt, result)
	case <-ctx.Done():
		s.proxy.SetUnavailable(ctx.Err())
		return ctx.Err()
	}
}

func (s *controlPlanePKISupervisor) Run(ctx context.Context) error {
	for {
		s.mu.Lock()
		current := s.runtime
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return context.Canceled
		}
		if current != nil {
			return maintainControlPlanePKILease(ctx, current.lease, current.service, s.logger)
		}
		attempt := s.startAttempt(ctx)
		if attempt == nil {
			return context.Canceled
		}
		select {
		case result := <-attempt.result:
			if err := s.completeAttempt(attempt, result); err == nil {
				continue
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		timer := time.NewTimer(controlPlanePKIRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *controlPlanePKISupervisor) startAttempt(parent context.Context) *controlPlanePKIAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.attempt != nil {
		return s.attempt
	}
	attemptCtx, cancel := context.WithTimeout(parent, controlPlanePKIAttemptTimeout)
	results := make(chan controlPlanePKIAttemptResult, 1)
	attempt := &controlPlanePKIAttempt{result: results, cancel: cancel}
	s.attempt = attempt
	go func() {
		runtime, err := newControlPlanePKIRuntimeFactory(
			attemptCtx, s.cfg, s.store, s.tasks, s.activation, s.logger, s.instanceID,
		)
		cancel()
		results <- controlPlanePKIAttemptResult{runtime: runtime, err: err}
	}()
	return attempt
}

func (s *controlPlanePKISupervisor) completeAttempt(attempt *controlPlanePKIAttempt, result controlPlanePKIAttemptResult) error {
	s.mu.Lock()
	if s.attempt != attempt {
		s.mu.Unlock()
		_ = closeControlPlanePKIRuntime(result.runtime)
		return result.err
	}
	s.attempt = nil
	closed := s.closed
	if result.err == nil && !closed {
		runtime := result.runtime
		s.runtime = &runtime
	}
	s.mu.Unlock()
	if result.err != nil {
		s.proxy.SetUnavailable(result.err)
		if s.logger != nil {
			s.logger.Printf("[pki] bootstrap attempt failed; retrying in background: %v", result.err)
		}
		return result.err
	}
	if closed {
		return closeControlPlanePKIRuntime(result.runtime)
	}
	s.proxy.Promote(result.runtime.service)
	if s.logger != nil {
		s.logger.Printf("[pki] tunnel PKI runtime is ready")
	}
	return nil
}

func (s *controlPlanePKISupervisor) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	current := s.runtime
	s.runtime = nil
	attempt := s.attempt
	s.attempt = nil
	s.mu.Unlock()
	var attemptErr error
	if attempt != nil {
		attempt.cancel()
		select {
		case result := <-attempt.result:
			attemptErr = errors.Join(result.err, closeControlPlanePKIRuntime(result.runtime))
		case <-time.After(5 * time.Second):
			attemptErr = errors.New("PKI bootstrap attempt did not stop before cleanup")
		}
	}
	var runtimeErr error
	if current != nil {
		runtimeErr = closeControlPlanePKIRuntime(*current)
	}
	return errors.Join(attemptErr, runtimeErr)
}

func closeControlPlanePKIRuntime(runtime controlPlanePKIRuntime) error {
	var leaseErr error
	if runtime.lease != nil {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		leaseErr = runtime.lease.Relinquish(releaseCtx)
		cancel()
		if errors.Is(leaseErr, service.ErrPKILeaseNotHeld) {
			leaseErr = nil
		}
	}
	if runtime.vault != nil {
		runtime.vault.Close()
	}
	return leaseErr
}

func newControlPlanePKIRuntime(
	ctx context.Context,
	cfg config.Config,
	store *storage.GormStore,
	tasks *service.TaskService,
	activation service.PKIActivationFinalizer,
	logger *log.Logger,
	instanceID string,
) (controlPlanePKIRuntime, error) {
	_ = logger
	pkiClock := controlPlanePKIRuntimeClock
	vault, err := service.OpenPKIVault(service.PKIVaultConfig{DataRoot: cfg.DataDir, MasterKeyFile: cfg.PKIMasterKeyFile})
	if err != nil {
		return controlPlanePKIRuntime{}, err
	}
	var lease *service.PKILeaseService
	fail := func(err error) (controlPlanePKIRuntime, error) {
		if lease != nil {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = lease.Relinquish(releaseCtx)
			cancel()
		}
		vault.Close()
		return controlPlanePKIRuntime{}, err
	}
	leaseRepository, err := service.NewGormPKILeaseRepository(store)
	if err != nil {
		return fail(err)
	}
	lease, err = service.NewPKILeaseService(service.PKILeaseServiceOptions{
		// The integration lifecycle clock may jump by hours. Lease fencing stays
		// on real/database time so those jumps cannot manufacture split-brain or
		// invalidate an otherwise live production lease.
		Repository: leaseRepository, InstanceID: instanceID, Clock: time.Now,
	})
	if err != nil {
		return fail(err)
	}
	vaultSigner, err := service.NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		return fail(err)
	}
	leaseSigner, err := service.NewPKILeaseAuthoritySigner(lease, vaultSigner)
	if err != nil {
		return fail(err)
	}
	snapshotSigner, err := service.NewPKIVaultSecuritySnapshotSigner(service.PKIVaultSecuritySnapshotSignerOptions{
		StateSource: store, Signer: leaseSigner,
	})
	if err != nil {
		return fail(err)
	}
	if _, err := service.BootstrapInternalPKI(ctx, service.InternalPKIBootstrapOptions{
		Store: store, Vault: vault, Lease: lease, SnapshotSigner: snapshotSigner, Clock: pkiClock,
	}); err != nil {
		return fail(err)
	}
	tokens, err := service.NewPKITokenService(service.PKITokenServiceOptions{Store: store, LocalAgentID: cfg.LocalAgentID, Clock: pkiClock})
	if err != nil {
		return fail(err)
	}
	enrollment, err := service.NewPKIEnrollmentService(service.PKIEnrollmentServiceOptions{
		Store: store, Lease: lease, AuthoritySigner: leaseSigner, LocalAgentID: cfg.LocalAgentID, Clock: pkiClock,
	})
	if err != nil {
		return fail(err)
	}
	revocationRepository, err := service.NewGormPKIRevocationRepository(service.GormPKIRevocationRepositoryOptions{Store: store, Clock: pkiClock})
	if err != nil {
		return fail(err)
	}
	publisher, err := service.NewPKISecurityTaskPublisher(store, tasks)
	if err != nil {
		return fail(err)
	}
	closer, err := service.NewPKITaskSessionCloser(tasks)
	if err != nil {
		return fail(err)
	}
	revocation, err := service.NewPKIRevocationService(service.PKIRevocationServiceOptions{
		Repository: revocationRepository, Signer: snapshotSigner, Publisher: publisher, Closer: closer, Lease: lease, Clock: pkiClock,
	})
	if err != nil {
		return fail(err)
	}
	backupKeySource, err := service.NewPKIVaultBackupKeySource(vault)
	if err != nil {
		return fail(err)
	}
	restoreTarget, err := service.NewProductionPKIBackupRestoreTarget(service.PKIBackupRestoreTargetOptions{
		Store: store, Vault: vault, DataRoot: cfg.DataDir, MasterKeyFile: cfg.PKIMasterKeyFile,
		Clock: pkiClock, ActivationHooks: controlPlanePKIRestoreHooks(),
	})
	if err != nil {
		return fail(err)
	}
	canonicalPKI, err := store.LoadPKICanonicalState(ctx)
	if err != nil || canonicalPKI.Settings == nil {
		if err == nil {
			err = fmt.Errorf("%w: canonical PKI settings are unavailable", service.ErrPKILifecycleInvalid)
		}
		return fail(err)
	}
	authorityGenerator, err := service.NewPKIVaultAuthorityGenerator(service.PKIVaultAuthorityGeneratorOptions{
		Vault: vault, PKIDomainID: canonicalPKI.Settings.PKIDomainID,
		Clock: pkiClock, Lifetime: time.Duration(canonicalPKI.Settings.CALifetimeSeconds) * time.Second,
	})
	if err != nil {
		return fail(err)
	}
	relayRevisionController, ok := activation.(service.PKIEmergencyRelayRevisionController)
	if !ok {
		return fail(fmt.Errorf("%w: relay revision controller is unavailable", service.ErrPKILifecycleInvalid))
	}
	emergencyRelayGate, err := service.NewPKIEmergencyRevisionRelayGate(relayRevisionController)
	if err != nil {
		return fail(err)
	}
	authorityRuntime, err := service.NewPKIAuthorityRuntime(service.PKIAuthorityRuntimeOptions{
		Store: store, Lease: lease, Generator: authorityGenerator,
		SnapshotSigner: snapshotSigner, SnapshotPublisher: publisher,
		Tasks: tasks, KeyDestroyer: vault, RelayGate: emergencyRelayGate,
		Clock: pkiClock, HeartbeatInterval: controlPlanePKIAuthorityHeartbeatInterval(),
	})
	if err != nil {
		return fail(err)
	}
	backupService, err := service.NewPKIBackupService(service.PKIBackupServiceOptions{
		LeaseGate: lease, SnapshotSource: store, AuthorityKeySource: backupKeySource, RestoreTarget: restoreTarget,
		Clock: pkiClock,
	})
	if err != nil {
		return fail(err)
	}
	pkiService, err := service.NewInternalPKIService(service.InternalPKIServiceOptions{
		Store: store, Lease: lease, Tokens: tokens, Enrollment: enrollment,
		Revocation: revocation, SnapshotSigner: snapshotSigner, Tasks: tasks, Backup: backupService,
		Activation: activation, Authority: authorityRuntime, Clock: pkiClock,
	})
	if err != nil {
		return fail(err)
	}
	return controlPlanePKIRuntime{service: pkiService, lease: lease, vault: vault}, nil
}

func fenceExistingPKIListenerDeletion(
	store *storage.GormStore,
	relay interface {
		SetPKIListenerRevoker(func(context.Context, *storage.GormStore, string, int) (func(), error))
	},
	logger *log.Logger,
) {
	state, err := store.LoadPKICanonicalState(context.Background())
	if err == nil && state.Settings == nil {
		return
	}
	if err != nil && logger != nil {
		logger.Printf("[pki] canonical state cannot be inspected while runtime is unavailable; listener deletion is fenced: %v", err)
	}
	relay.SetPKIListenerRevoker(func(context.Context, *storage.GormStore, string, int) (func(), error) {
		return nil, fmt.Errorf("%w: internal PKI runtime is unavailable", service.ErrPKIEnrollmentAuthorityUnavailable)
	})
}

func controlPlanePKIInstanceID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "control-plane"
	}
	return fmt.Sprintf("%s-%d-%d", hostname, os.Getpid(), time.Now().UTC().UnixNano())
}

func maintainControlPlanePKILease(
	ctx context.Context,
	lease *service.PKILeaseService,
	pkiService *service.InternalPKIService,
	logger *log.Logger,
) error {
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	if pkiService != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-reconcileCtx.Done():
					return
				case <-ticker.C:
					retryCtx, cancel := context.WithTimeout(reconcileCtx, 5*time.Second)
					err := pkiService.ReconcilePendingConvergence(retryCtx)
					cancel()
					if err != nil && !errors.Is(err, service.ErrPKILeaseNotHeld) && logger != nil {
						logger.Printf("[pki] durable convergence retry failed: %v", err)
					}
				}
			}
		}()
	}
	for {
		err := lease.Maintain(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if logger != nil {
			logger.Printf("[pki] lease unavailable; retrying while control protocol remains online: %v", err)
		}
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
