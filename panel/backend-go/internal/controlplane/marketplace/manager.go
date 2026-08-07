package marketplace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type Manager struct {
	root       string
	fetcher    Fetcher
	validator  *plugins.Validator
	cache      *VerifiedCache
	repository Repository
	now        func() time.Time
	leaseTTL   time.Duration
	locks      sync.Map
}

func NewManager(root string, fetcher Fetcher, validator *plugins.Validator, cache *VerifiedCache, repository Repository) (*Manager, error) {
	if fetcher == nil || validator == nil || cache == nil || repository == nil {
		return nil, errors.New("fetcher, validator, cache, and repository are required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "staging"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0o755); err != nil {
		return nil, err
	}
	return &Manager{root: root, fetcher: fetcher, validator: validator, cache: cache, repository: repository, now: func() time.Time { return time.Now().UTC() }, leaseTTL: 10 * time.Minute}, nil
}

func (m *Manager) Refresh(ctx context.Context, source Source, actor OperationActor) (Snapshot, error) {
	if err := ValidateSource(source); err != nil {
		return Snapshot{}, err
	}
	lockValue, _ := m.locks.LoadOrStore(source.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	id := randomID("refresh")
	started := m.now()
	if actor.ActorID == "" {
		actor.ActorID = "system.marketplace"
	}
	if actor.CorrelationID == "" {
		actor.CorrelationID = id
	}
	operation := RefreshOperation{ID: id, SourceID: source.ID, Status: "running", StartedAt: started, LeaseToken: randomID("lease"), LeaseExpiresAt: started.Add(m.leaseTTL)}
	operation.Actor = actor
	if err := m.repository.AcquireRefreshLease(ctx, operation); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		errorClass := "lease_acquire"
		if errors.Is(err, ErrRefreshLeaseHeld) {
			errorClass = "lease_contention"
		}
		if auditErr := m.repository.RecordRefreshRejection(cleanupCtx, source.ID, actor, errorClass); auditErr != nil {
			return Snapshot{}, errors.Join(err, auditErr)
		}
		return Snapshot{}, err
	}
	staging := filepath.Join(m.root, "staging", source.ID, id)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "staging", err)
	}
	defer os.RemoveAll(staging)
	commit, err := m.fetcher.Fetch(ctx, source, staging)
	if err != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "fetch", err)
	}
	operation.Commit = commit
	validated, err := m.validator.ValidateMarket(staging, source.Kind == SourceKindOfficial)
	if err != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "validation", err)
	}
	for _, candidate := range validated.Packages {
		if _, err := m.cache.Store(candidate); err != nil {
			return Snapshot{}, m.failRefresh(ctx, operation, "cache", err)
		}
	}
	previous, hadPrevious, previousErr := m.repository.CurrentSnapshot(ctx, source.ID)
	if previousErr != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "snapshot_read", previousErr)
	}
	snapshotID := randomID("snapshot")
	snapshotPath := filepath.Join(m.root, "snapshots", source.ID, snapshotID)
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "snapshot", err)
	}
	if err := os.Rename(staging, snapshotPath); err != nil {
		return Snapshot{}, m.failRefresh(ctx, operation, "snapshot", err)
	}
	snapshot := Snapshot{ID: snapshotID, SourceID: source.ID, Commit: commit, Path: snapshotPath, ValidatedAt: m.now(), Entries: validated.Manifest.Entries}
	finished := m.now()
	operation.Status = "succeeded"
	operation.FinishedAt = &finished
	operation.DiffJSON = snapshotDiff(previous, hadPrevious, snapshot)
	if err := m.repository.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, operation); err != nil {
		_ = os.RemoveAll(snapshotPath)
		return Snapshot{}, m.failRefresh(ctx, operation, "promotion", err)
	}
	return snapshot, nil
}

func (m *Manager) failRefresh(ctx context.Context, operation RefreshOperation, class string, cause error) error {
	finished := m.now()
	operation.Status = "failed"
	operation.ErrorClass = class
	operation.Error = cause.Error()
	operation.FinishedAt = &finished
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.repository.SaveRefreshOperation(cleanupCtx, operation); err != nil {
		return fmt.Errorf("%v (record refresh failure: %w)", cause, err)
	}
	return cause
}

func snapshotDiff(current Snapshot, exists bool, next Snapshot) string {
	if !exists || current.ID == next.ID {
		return "{}"
	}
	oldEntries := make(map[string][]string, len(current.Entries))
	for _, entry := range current.Entries {
		oldEntries[entry.ID] = append(oldEntries[entry.ID], entry.Version)
	}
	newEntries := make(map[string][]string, len(next.Entries))
	for _, entry := range next.Entries {
		newEntries[entry.ID] = append(newEntries[entry.ID], entry.Version)
	}
	diff := map[string]string{}
	for id, versions := range newEntries {
		sort.Strings(versions)
		previous := oldEntries[id]
		sort.Strings(previous)
		currentVersions, nextVersions := strings.Join(previous, ","), strings.Join(versions, ",")
		if currentVersions == "" {
			diff[id] = "added:" + nextVersions
		} else if currentVersions != nextVersions {
			diff[id] = currentVersions + "->" + nextVersions
		}
		delete(oldEntries, id)
	}
	for id, versions := range oldEntries {
		sort.Strings(versions)
		diff[id] = "removed:" + strings.Join(versions, ",")
	}
	encoded, _ := json.Marshal(diff)
	return string(encoded)
}

func randomID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}
