package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/sanitize"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type PluginRuntimeRepository interface {
	StagePluginRuntime(context.Context, storage.PluginRuntimeInstanceRow) error
	PromotePluginRuntime(context.Context, string, string, int, string) error
	FailPluginRuntimeCandidate(context.Context, string, string, error) error
	GetPluginRuntime(context.Context, string) (storage.PluginRuntimeInstanceRow, bool, error)
	UpdatePluginRuntimeHealth(context.Context, string, string, string, int, int, bool, string) error
	StopPluginRuntime(context.Context, string, string) error
}

type PluginRuntimeBatchRepository interface {
	StagePluginRuntimeBatch(context.Context, []storage.PluginRuntimeInstanceRow) error
	PromotePluginRuntimeBatch(context.Context, []storage.PluginRuntimePromotion) error
	FailPluginRuntimeCandidateBatch(context.Context, []storage.PluginRuntimeCandidateFailure) error
}

type PluginRuntimeBatch struct {
	mu            sync.Mutex
	state         string
	entries       []pluginRuntimeBatchEntry
	operationDone func()
	releaseOnce   sync.Once
}

func (b *PluginRuntimeBatch) release() {
	if b == nil || b.operationDone == nil {
		return
	}
	b.releaseOnce.Do(b.operationDone)
}

type pluginRuntimeBatchEntry struct {
	candidate          pluginhost.Candidate
	instance           *pluginhost.Instance
	previousGeneration string
}

func (b *PluginRuntimeBatch) State() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

const pluginRuntimeHealthWriteTimeout = 5 * time.Second

type PluginRuntimeHost struct {
	host       *pluginhost.Host
	repository PluginRuntimeRepository
	locks      sync.Map
	stateMu    sync.Mutex
	closeMu    sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	operations sync.WaitGroup
	closed     bool
	terminalMu sync.Mutex
	terminals  map[terminalResultKey]pluginhost.TerminalResult
	pendingMu  sync.Mutex
	pending    map[*pluginhost.Instance]pendingRuntimeCleanup
	revoker    PluginCapabilityGenerationRevoker
	logQueue   chan pluginRuntimeHostLog
}

type pluginRuntimeHostLog struct {
	candidate pluginhost.Candidate
	message   string
}

type PluginCapabilityGenerationRevoker interface {
	RevokeGeneration(string, string)
}

type terminalResultKey struct {
	instanceID string
	generation string
}

type pendingRuntimeCleanup struct {
	candidate pluginhost.Candidate
	promoted  bool
}

func NewPluginRuntimeHost(host *pluginhost.Host, repository PluginRuntimeRepository) (*PluginRuntimeHost, error) {
	if host == nil || repository == nil {
		return nil, errors.New("plugin runtime host and repository are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &PluginRuntimeHost{host: host, repository: repository, ctx: ctx, cancel: cancel, terminals: make(map[terminalResultKey]pluginhost.TerminalResult), pending: make(map[*pluginhost.Instance]pendingRuntimeCleanup), logQueue: make(chan pluginRuntimeHostLog, 128)}
	host.SetStatusObserver(func(status pluginhost.RuntimeStatus) error {
		observerCtx, cancel := context.WithTimeout(service.ctx, pluginRuntimeHealthWriteTimeout)
		defer cancel()
		return repository.UpdatePluginRuntimeHealth(observerCtx, status.InstanceID, status.Generation, status.State, status.PID, status.RestartCount, status.CircuitOpen, status.LastError)
	})
	if sink, ok := repository.(interface {
		AppendControlPlanePluginRuntimeLog(context.Context, string, string, string, string) error
	}); ok {
		host.SetLogObserver(func(candidate pluginhost.Candidate, message string) {
			select {
			case service.logQueue <- pluginRuntimeHostLog{candidate: candidate, message: message}:
			default:
			}
		})
		service.operations.Add(1)
		go func() {
			defer service.operations.Done()
			for {
				select {
				case <-service.ctx.Done():
					return
				case entry := <-service.logQueue:
					writeCtx, cancel := context.WithTimeout(service.ctx, pluginRuntimeHealthWriteTimeout)
					_ = sink.AppendControlPlanePluginRuntimeLog(writeCtx, entry.candidate.InstanceID, entry.candidate.Identity.PluginID, entry.candidate.Identity.Generation, entry.message)
					cancel()
				}
			}
		}()
	}
	return service, nil
}

func (s *PluginRuntimeHost) SetCapabilityRevoker(revoker PluginCapabilityGenerationRevoker) {
	s.stateMu.Lock()
	s.revoker = revoker
	s.stateMu.Unlock()
}

func (s *PluginRuntimeHost) ActiveGeneration(instanceID string) (string, bool) {
	instance, ok := s.host.Active(instanceID)
	if !ok || instance == nil || instance.Generation == "" {
		return "", false
	}
	return instance.Generation, true
}

func (s *PluginRuntimeHost) InvokeAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionRequest) error {
	return s.host.InvokeAction(ctx, instanceID, generation, request)
}

func (s *PluginRuntimeHost) PlanAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	return s.host.PlanAction(ctx, instanceID, generation, request)
}

func (s *PluginRuntimeHost) QueryAction(ctx context.Context, instanceID, generation string, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	return s.host.QueryAction(ctx, instanceID, generation, request)
}

func (s *PluginRuntimeHost) revokeGeneration(instanceID, generation string) {
	if generation == "" {
		return
	}
	s.stateMu.Lock()
	revoker := s.revoker
	s.stateMu.Unlock()
	if revoker != nil {
		revoker.RevokeGeneration(instanceID, generation)
	}
}

func (s *PluginRuntimeHost) Activate(ctx context.Context, candidate pluginhost.Candidate) (*pluginhost.Instance, error) {
	operationCtx, done, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	lockValue, _ := s.locks.LoadOrStore(candidate.InstanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := operationCtx.Err(); err != nil {
		return nil, errors.Join(errors.New("plugin runtime activation canceled"), err)
	}
	previousGeneration, _ := s.ActiveGeneration(candidate.InstanceID)
	row := storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, HostScope: "control-plane", CandidateGeneration: candidate.Identity.Generation, CandidatePackageDigest: candidate.Identity.PackageDigest, CandidateArtifactDigest: candidate.Artifact.SHA256}
	if err := s.repository.StagePluginRuntime(operationCtx, row); err != nil {
		return nil, fmt.Errorf("stage plugin runtime state: %w", err)
	}
	instance, err := s.host.PrepareCandidate(operationCtx, candidate)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if reportErr := s.repository.FailPluginRuntimeCandidate(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, err); reportErr != nil {
			return nil, errors.Join(err, reportErr)
		}
		return nil, err
	}
	state, _ := instance.Status()
	sandbox := instance.SandboxProvider
	if err := s.repository.PromotePluginRuntime(operationCtx, candidate.InstanceID, candidate.Identity.Generation, instance.PID, sandbox); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopErr := s.stopPreparedCandidate(cleanupCtx, instance, candidate, false)
		reportErr := s.repository.FailPluginRuntimeCandidate(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, err)
		return nil, errors.Join(fmt.Errorf("promote plugin runtime state after %s: %w", state, err), stopErr, reportErr)
	}
	if err := operationCtx.Err(); err != nil {
		return nil, s.rollbackPromotedCandidate(instance, candidate, fmt.Errorf("publish canceled promoted plugin runtime: %w", err))
	}
	if err := s.host.Publish(instance); err != nil {
		return nil, s.rollbackPromotedCandidate(instance, candidate, fmt.Errorf("publish promoted plugin runtime: %w", err))
	}
	if previousGeneration != "" && previousGeneration != candidate.Identity.Generation {
		s.revokeGeneration(candidate.InstanceID, previousGeneration)
	}
	return instance, nil
}

// PrepareBatch stages and starts every candidate without publishing any of
// them. A failure aborts all prepared candidates and leaves every old active
// generation untouched.
func (s *PluginRuntimeHost) PrepareBatch(ctx context.Context, candidates []pluginhost.Candidate) (*PluginRuntimeBatch, error) {
	operationCtx, done, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			done()
		}
	}()
	repository, ok := s.repository.(PluginRuntimeBatchRepository)
	if !ok {
		return nil, errors.New("plugin runtime repository does not support atomic batches")
	}
	if len(candidates) == 0 {
		return nil, errors.New("plugin runtime candidate batch is empty")
	}
	ordered := append([]pluginhost.Candidate(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].InstanceID < ordered[j].InstanceID })
	rows := make([]storage.PluginRuntimeInstanceRow, 0, len(ordered))
	batch := &PluginRuntimeBatch{state: "preparing", entries: make([]pluginRuntimeBatchEntry, 0, len(ordered)), operationDone: done}
	for index, candidate := range ordered {
		if candidate.InstanceID == "" || candidate.Identity.Generation == "" || (index > 0 && ordered[index-1].InstanceID == candidate.InstanceID) {
			return nil, errors.New("plugin runtime batch candidate identities must be unique and complete")
		}
		previous, _ := s.ActiveGeneration(candidate.InstanceID)
		batch.entries = append(batch.entries, pluginRuntimeBatchEntry{candidate: candidate, previousGeneration: previous})
		rows = append(rows, storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, HostScope: "control-plane", CandidateGeneration: candidate.Identity.Generation, CandidatePackageDigest: candidate.Identity.PackageDigest, CandidateArtifactDigest: candidate.Artifact.SHA256})
	}
	if err := repository.StagePluginRuntimeBatch(operationCtx, rows); err != nil {
		return nil, fmt.Errorf("stage plugin runtime batch: %w", err)
	}
	for index := range batch.entries {
		instance, prepareErr := s.host.PrepareCandidate(operationCtx, batch.entries[index].candidate)
		if prepareErr != nil {
			abortErr := s.abortPreparedBatch(context.Background(), batch, prepareErr, repository)
			return nil, errors.Join(prepareErr, abortErr)
		}
		batch.entries[index].instance = instance
	}
	batch.mu.Lock()
	batch.state = "prepared"
	batch.mu.Unlock()
	owned = true
	return batch, nil
}

// CommitBatch atomically promotes the durable candidate pointers, then
// publishes the already-ready instances. Instance locks fence concurrent
// single-generation activation and stop operations.
func (s *PluginRuntimeHost) CommitBatch(ctx context.Context, batch *PluginRuntimeBatch) error {
	if batch == nil || ctx == nil {
		return errors.New("plugin runtime batch is required")
	}
	repository, ok := s.repository.(PluginRuntimeBatchRepository)
	if !ok {
		return errors.New("plugin runtime repository does not support atomic batches")
	}
	batch.mu.Lock()
	if batch.state != "prepared" {
		state := batch.state
		batch.mu.Unlock()
		return fmt.Errorf("plugin runtime batch is %s, not prepared", state)
	}
	batch.state = "committing"
	batch.mu.Unlock()
	defer batch.release()
	if err := ctx.Err(); err != nil {
		cause := errors.Join(errors.New("plugin runtime batch commit canceled"), err)
		return errors.Join(cause, s.abortPreparedBatch(context.Background(), batch, cause, repository))
	}
	locks := make([]*sync.Mutex, 0, len(batch.entries))
	for _, entry := range batch.entries {
		value, _ := s.locks.LoadOrStore(entry.candidate.InstanceID, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		locks = append(locks, lock)
	}
	defer func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}()
	for _, entry := range batch.entries {
		active, _ := s.ActiveGeneration(entry.candidate.InstanceID)
		if active != entry.previousGeneration {
			cause := errors.New("plugin runtime active generation changed after batch prepare")
			return errors.Join(cause, s.abortPreparedBatch(context.Background(), batch, cause, repository))
		}
	}
	promotions := make([]storage.PluginRuntimePromotion, 0, len(batch.entries))
	instances := make([]*pluginhost.Instance, 0, len(batch.entries))
	for _, entry := range batch.entries {
		promotions = append(promotions, storage.PluginRuntimePromotion{InstanceID: entry.candidate.InstanceID, Generation: entry.candidate.Identity.Generation, PID: entry.instance.ProcessID(), SandboxProvider: entry.instance.SandboxProvider})
		instances = append(instances, entry.instance)
	}
	publication, err := s.host.PreparePublication(instances)
	if err != nil {
		return errors.Join(err, s.abortPreparedBatch(context.Background(), batch, err, repository))
	}
	if err := repository.PromotePluginRuntimeBatch(ctx, promotions); err != nil {
		publication.Abort()
		return errors.Join(err, s.abortPreparedBatch(context.Background(), batch, err, repository))
	}
	publication.Publish()
	for _, entry := range batch.entries {
		if entry.previousGeneration != "" && entry.previousGeneration != entry.candidate.Identity.Generation {
			s.revokeGeneration(entry.candidate.InstanceID, entry.previousGeneration)
		}
	}
	batch.mu.Lock()
	batch.state = "committed"
	batch.mu.Unlock()
	return nil
}

func (s *PluginRuntimeHost) AbortBatch(ctx context.Context, batch *PluginRuntimeBatch, cause error) error {
	if ctx == nil {
		return errors.New("plugin runtime batch abort context is required")
	}
	repository, ok := s.repository.(PluginRuntimeBatchRepository)
	if !ok {
		return errors.New("plugin runtime repository does not support atomic batches")
	}
	defer batch.release()
	return s.abortPreparedBatch(ctx, batch, cause, repository)
}

func (s *PluginRuntimeHost) ActivateBatch(ctx context.Context, candidates []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	batch, err := s.PrepareBatch(ctx, candidates)
	if err != nil {
		return nil, err
	}
	if err := s.CommitBatch(ctx, batch); err != nil {
		return nil, err
	}
	instances := make([]*pluginhost.Instance, 0, len(batch.entries))
	for _, entry := range batch.entries {
		instances = append(instances, entry.instance)
	}
	return instances, nil
}

func (s *PluginRuntimeHost) abortPreparedBatch(ctx context.Context, batch *PluginRuntimeBatch, cause error, repository PluginRuntimeBatchRepository) error {
	if batch == nil {
		return errors.New("plugin runtime batch is required")
	}
	batch.mu.Lock()
	if batch.state == "committed" || batch.state == "aborted" {
		state := batch.state
		batch.mu.Unlock()
		if state == "aborted" {
			return nil
		}
		return errors.New("committed plugin runtime batch cannot be aborted")
	}
	batch.state = "aborting"
	batch.mu.Unlock()
	cleanupCtx := ctx
	cancel := func() {}
	if cleanupCtx == nil || cleanupCtx.Err() != nil {
		cleanupCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancel()
	var errs []error
	failures := make([]storage.PluginRuntimeCandidateFailure, 0, len(batch.entries))
	for _, entry := range batch.entries {
		failures = append(failures, storage.PluginRuntimeCandidateFailure{InstanceID: entry.candidate.InstanceID, Generation: entry.candidate.Identity.Generation, Failure: cause})
		if entry.instance != nil {
			if err := s.stopPreparedCandidate(cleanupCtx, entry.instance, entry.candidate, false); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := repository.FailPluginRuntimeCandidateBatch(cleanupCtx, failures); err != nil {
		errs = append(errs, err)
	}
	batch.mu.Lock()
	batch.state = "aborted"
	batch.mu.Unlock()
	return errors.Join(errs...)
}

func (s *PluginRuntimeHost) beginOperation(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("plugin runtime operation context is required")
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return nil, nil, errors.New("plugin runtime service is closed")
	}
	s.operations.Add(1)
	serviceCtx := s.ctx
	s.stateMu.Unlock()
	operationCtx, cancel := context.WithCancel(ctx)
	stopServiceCancellation := context.AfterFunc(serviceCtx, cancel)
	done := func() {
		stopServiceCancellation()
		cancel()
		s.operations.Done()
	}
	return operationCtx, done, nil
}

func (s *PluginRuntimeHost) rollbackPromotedCandidate(instance *pluginhost.Instance, candidate pluginhost.Candidate, publishErr error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := s.stopPreparedCandidate(cleanupCtx, instance, candidate, true)
	if !instance.Terminated() {
		healthErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", instance.ProcessID(), 0, false, safeRuntimeError(errors.Join(publishErr, stopErr)))
		return errors.Join(publishErr, stopErr, healthErr)
	}
	terminalErr := retryRuntimeStopPersistence(cleanupCtx, s.repository, candidate.InstanceID, candidate.Identity.Generation)
	if terminalErr != nil {
		fallbackErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", 0, 0, false, safeRuntimeError(errors.Join(publishErr, terminalErr)))
		terminalErr = errors.Join(fmt.Errorf("terminalize promoted plugin runtime: %w", terminalErr), fallbackErr)
	}
	return errors.Join(publishErr, stopErr, terminalErr)
}

func (s *PluginRuntimeHost) stopPreparedCandidate(ctx context.Context, instance *pluginhost.Instance, candidate pluginhost.Candidate, promoted bool) error {
	stopErr := s.host.StopCandidate(ctx, instance)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if !instance.Terminated() {
		s.pending[instance] = pendingRuntimeCleanup{candidate: candidate, promoted: promoted}
	} else {
		delete(s.pending, instance)
	}
	return stopErr
}

func safeRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	value := sanitize.Text(err.Error(), nil)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func (s *PluginRuntimeHost) Stop(ctx context.Context, instanceID string) error {
	operationCtx, done, err := s.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer done()
	lockValue, _ := s.locks.LoadOrStore(instanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := operationCtx.Err(); err != nil {
		return errors.Join(errors.New("plugin runtime stop canceled"), err)
	}
	row, found, readErr := s.repository.GetPluginRuntime(operationCtx, instanceID)
	if readErr != nil {
		return fmt.Errorf("read plugin runtime before stop: %w", readErr)
	}
	if found && row.ActiveGeneration != "" {
		if result, ok := s.terminalResult(instanceID, row.ActiveGeneration); ok && result.Terminated {
			return s.persistTerminalResult(operationCtx, result, false)
		}
	}
	results, stopErr := s.host.StopWithResults(operationCtx, instanceID)
	s.rememberTerminalResults(results)
	for _, result := range results {
		if result.Terminated {
			s.revokeGeneration(result.InstanceID, result.Generation)
		}
	}
	if !found || row.ActiveGeneration == "" {
		return stopErr
	}
	result, resultFound := s.terminalResult(instanceID, row.ActiveGeneration)
	if !resultFound {
		return errors.Join(stopErr, fmt.Errorf("plugin runtime %s generation %s has no ownership-fenced terminal result", instanceID, row.ActiveGeneration))
	}
	return errors.Join(stopErr, s.persistTerminalResult(operationCtx, result, false))
}

func (s *PluginRuntimeHost) rememberTerminalResults(results []pluginhost.TerminalResult) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	for _, result := range results {
		if !result.Published || result.InstanceID == "" || result.Generation == "" {
			continue
		}
		key := terminalResultKey{instanceID: result.InstanceID, generation: result.Generation}
		current, exists := s.terminals[key]
		if !exists || result.Terminated || !current.Terminated {
			s.terminals[key] = result
		}
	}
}

func (s *PluginRuntimeHost) terminalResult(instanceID, generation string) (pluginhost.TerminalResult, bool) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	result, ok := s.terminals[terminalResultKey{instanceID: instanceID, generation: generation}]
	return result, ok
}

func (s *PluginRuntimeHost) terminalResults() []pluginhost.TerminalResult {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	results := make([]pluginhost.TerminalResult, 0, len(s.terminals))
	for _, result := range s.terminals {
		results = append(results, result)
	}
	return results
}

func (s *PluginRuntimeHost) forgetTerminalResult(result pluginhost.TerminalResult) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	key := terminalResultKey{instanceID: result.InstanceID, generation: result.Generation}
	if current, ok := s.terminals[key]; ok && current.Instance == result.Instance {
		delete(s.terminals, key)
	}
}

func (s *PluginRuntimeHost) persistTerminalResult(ctx context.Context, result pluginhost.TerminalResult, closing bool) error {
	if !result.Terminated {
		return errors.Join(result.CleanupError, fmt.Errorf("plugin runtime %s generation %s remains owned after failed termination", result.InstanceID, result.Generation))
	}
	persistCtx := ctx
	cancelPersist := func() {}
	if ctx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancelPersist()
	observerErr := s.host.StatusPersistenceError(result.InstanceID)
	if result.PendingExitError != nil {
		failure := errors.Join(result.PendingExitError, observerErr)
		persistErr := retryRuntimeFailurePersistence(persistCtx, s.repository, result.InstanceID, result.Generation, failure)
		if persistErr == nil {
			s.forgetTerminalResult(result)
		}
		return errors.Join(fmt.Errorf("plugin runtime %s generation %s stopped with unacknowledged exit: %w", result.InstanceID, result.Generation, failure), persistErr)
	}
	persistErr := retryRuntimeStopPersistence(persistCtx, s.repository, result.InstanceID, result.Generation)
	if persistErr == nil {
		s.forgetTerminalResult(result)
	} else {
		persistErr = fmt.Errorf("persist terminal state for plugin runtime %s generation %s: %w", result.InstanceID, result.Generation, persistErr)
	}
	if closing && errors.Is(observerErr, context.Canceled) && s.ctx.Err() != nil {
		observerErr = nil
	}
	return errors.Join(persistErr, observerErr)
}

func (s *PluginRuntimeHost) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.stateMu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.stateMu.Unlock()
	s.operations.Wait()
	results, hostErr := s.host.CloseWithResults(ctx)
	s.rememberTerminalResults(results)
	for _, result := range results {
		if result.Terminated {
			s.revokeGeneration(result.InstanceID, result.Generation)
		}
	}
	errs := []error{hostErr}
	s.pendingMu.Lock()
	for instance, pending := range s.pending {
		if !instance.Terminated() {
			errs = append(errs, fmt.Errorf("plugin runtime candidate %s generation %s remains owned after failed termination", pending.candidate.InstanceID, pending.candidate.Identity.Generation))
			continue
		}
		if pending.promoted {
			if err := retryRuntimeStopPersistence(ctx, s.repository, pending.candidate.InstanceID, pending.candidate.Identity.Generation); err != nil {
				errs = append(errs, fmt.Errorf("persist stopped rollback candidate %s generation %s: %w", pending.candidate.InstanceID, pending.candidate.Identity.Generation, err))
				continue
			}
		}
		delete(s.pending, instance)
	}
	s.pendingMu.Unlock()
	for _, result := range s.terminalResults() {
		if err := s.persistTerminalResult(ctx, result, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func retryRuntimeFailurePersistence(ctx context.Context, repository PluginRuntimeRepository, instanceID, generation string, failure error) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = repository.UpdatePluginRuntimeHealth(ctx, instanceID, generation, "failed", 0, 0, false, safeRuntimeError(failure))
		if lastErr == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func retryRuntimeStopPersistence(ctx context.Context, repository PluginRuntimeRepository, instanceID, generation string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if lastErr = repository.StopPluginRuntime(ctx, instanceID, generation); lastErr == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}
