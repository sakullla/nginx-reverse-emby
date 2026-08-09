package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestCustomSourceCannotImpersonateOfficialAndAlwaysCarriesRisk(t *testing.T) {
	for _, test := range []struct{ id, name string }{{"official", "mirror"}, {"mirror", "Official"}, {"mirror", "Sakullla Official Mirror"}, {"mirror", "官方市场"}} {
		if _, err := NewCustomSource(test.id, test.name, "https://example.com/plugins.git", "main", "", 0); err == nil {
			t.Fatalf("expected custom identity %q/%q to be rejected", test.id, test.name)
		}
	}
	source, err := NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "secret-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != SourceKindCustom || source.RiskLabel != UntrustedRiskLabel {
		t.Fatalf("custom source lost non-official risk identity: %+v", source)
	}
	source.RiskLabel = ""
	if err := ValidateSource(source); err == nil {
		t.Fatal("custom source without risk label was accepted")
	}
	official := OfficialSource()
	official.RefName, official.ConfigRevision = "release", 2
	if err := ValidateSource(official); err != nil {
		t.Fatalf("official source branch switch was rejected: %v", err)
	}
	official.RefKind = GitRefKindTag
	if err := ValidateSource(official); err == nil {
		t.Fatal("official source accepted a version-pinned tag")
	}
}

func TestCustomSourceURLRejectsCredentialQueryFragmentAndSSH(t *testing.T) {
	for _, remote := range []string{
		"https://user:token@example.com/plugins.git",
		"https://example.com/plugins.git?token=secret",
		"https://example.com/plugins.git#secret",
		"ssh://git@example.com/plugins.git",
	} {
		if _, err := NewCustomSource("community", "Community", remote, "main", "", 0); err == nil {
			t.Fatalf("unsafe or unpinned source URL was accepted: %s", remote)
		}
	}
}

func TestRefreshSourceGenerationChangeStopsBeforeFetchAndRejectionAudit(t *testing.T) {
	repository := &memoryRepository{current: map[string]Snapshot{}, acquireError: ErrSourceGenerationChanged}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: marketplaceFixture(t, false)}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, err := marketplaceSignedTestSource()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(t.Context(), source, OperationActor{ActorID: "admin"}); !errors.Is(err, ErrSourceGenerationChanged) {
		t.Fatalf("refresh generation error = %v", err)
	}
	if len(repository.operations) != 0 {
		t.Fatalf("stale generation created operation or rejection audit: %+v", repository.operations)
	}
}

func TestRefreshSignatureValidationFailureKeepsCurrentSnapshotAndCache(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	cache, err := newTestVerifiedCache(t, cacheRoot, validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	fixture := marketplaceFixture(t, false)
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, err := marketplaceSignedTestSource()
	if err != nil {
		t.Fatal(err)
	}
	stable, err := manager.Refresh(ctx, source, OperationActor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "plugins", "example.plugin", "1.0.0", plugins.PackageSignatureFile), []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, source, OperationActor{}); err == nil {
		t.Fatal("refresh with invalid signature unexpectedly succeeded")
	}
	current, ok, err := repository.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != stable.ID {
		t.Fatalf("failed refresh replaced current snapshot: %+v, %v, %v", current, ok, err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := SignerCachePath(cacheRoot, stable.Entries[0].PackageSHA256, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("failed refresh removed active cache: %v", err)
	}
	if len(repository.operations) != 2 || repository.operations[1].Status != "failed" || repository.operations[1].ErrorClass != "validation" {
		t.Fatalf("refresh failure was not persisted: %+v", repository.operations)
	}
}

func TestManagerUsesPersistedSourceBoundSigner(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{}}
	baseValidator := plugins.NewValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), baseValidator, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithSourceValidators(
		filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: marketplaceFixture(t, false)}, baseValidator, cache, repository,
		NewSourceValidatorFactory(plugins.ValidatorOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSignedCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0, SourceSigner{
		KeyID: "test-market", SecretRef: "vault-market-signer", PublicKey: base64.StdEncoding.EncodeToString(marketTestSigningKey().Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, source, OperationActor{}); err != nil {
		t.Fatalf("refresh with source-bound signer: %v", err)
	}
	wrongKey := ed25519.NewKeyFromSeed(bytesOf(0x5a, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	wrongSource, err := NewSignedCustomSource("other", "Other", "https://example.com/other.git", "main", "", 0, SourceSigner{
		KeyID: "test-market", SecretRef: "vault-other-signer", PublicKey: base64.StdEncoding.EncodeToString(wrongKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, wrongSource, OperationActor{}); err == nil {
		t.Fatal("market signed by another source's key was accepted")
	}
}

func TestProductionOfficialRefreshResolvesCurrentBranchAndInvalidPolicyKeepsCurrent(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{OfficialSourceID: {ID: "stable", SourceID: OfficialSourceID, Commit: "stable-commit"}}}
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	lockedRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockedRoot, plugins.MarketManifestFile), []byte("schema_version: 1\nname: Locked Official\nplugins: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := validOfficialLock()
	lockPath := filepath.Join(t.TempDir(), OfficialMarketLockFile)
	if err := os.WriteFile(lockPath, []byte("schema_version: 1\nrepository: "+OfficialSourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := &lockedCopyFetcher{lockedRoot: lockedRoot, commits: []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}}
	manager, err := NewManagerWithOfficialLock(filepath.Join(t.TempDir(), "marketplace"), fetcher, validator, cache, repository, NewSourceValidatorFactory(plugins.ValidatorOptions{}), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, OfficialSource(), OperationActor{}); err == nil {
		t.Fatal("pending official lock was accepted")
	}
	if fetcher.normalCalls != 0 || repository.current[OfficialSourceID].ID != "stable" {
		t.Fatalf("invalid policy changed official current: calls=%d current=%+v", fetcher.normalCalls, repository.current[OfficialSourceID])
	}
	lockYAML := func(branch string) string {
		return fmt.Sprintf("schema_version: 1\nrepository: %s\nref_kind: branch\nref_name: %s\nsdk_abis: [nre:policy/v1, nre:rpc/v1]\nsignature_key_id: %s\n", lock.Repository, branch, lock.SignatureKeyID)
	}
	if err := os.WriteFile(lockPath, []byte(lockYAML("main")), 0o644); err != nil {
		t.Fatal(err)
	}
	for index, wantCommit := range fetcher.commits {
		if index == 1 {
			if err := os.WriteFile(lockPath, []byte(lockYAML("release")), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		snapshot, err := manager.Refresh(ctx, OfficialSource(), OperationActor{})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Commit != wantCommit {
			t.Fatalf("refresh %d commit = %q, want %q", index, snapshot.Commit, wantCommit)
		}
		wantBranch := []string{"main", "release"}[index]
		if snapshot.RefName != wantBranch {
			t.Fatalf("refresh %d branch = %q, want %q", index, snapshot.RefName, wantBranch)
		}
	}
	if fetcher.normalCalls != len(fetcher.commits) || fetcher.lastOID != fetcher.commits[len(fetcher.commits)-1] || !slices.Equal(fetcher.refs, []string{"main", "release"}) {
		t.Fatalf("official refresh did not resolve each current branch head: fetcher=%+v", fetcher)
	}
}

func bytesOf(value byte, size int) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = value
	}
	return result
}

func TestRefreshPromotesOnlyAfterValidationAndKeepsDigestCache(t *testing.T) {
	ctx, identity := WithRefreshIdentityCapture(context.Background())
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	cache, err := newTestVerifiedCache(t, cacheRoot, validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	fixture := marketplaceFixture(t, false)
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, err := marketplaceSignedTestSource()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Refresh(ctx, source, OperationActor{})
	if err != nil {
		t.Fatal(err)
	}
	if repository.current[source.ID].ID != snapshot.ID || repository.operations[0].Status != "succeeded" {
		t.Fatalf("snapshot or refresh operation was not promoted: %+v %+v", repository.current, repository.operations)
	}
	if captured := identity.Load(); captured.OperationID != repository.operations[0].ID || captured.LeaseToken != repository.operations[0].LeaseToken || captured.OperationID == "" || captured.LeaseToken == "" {
		t.Fatalf("refresh identity capture = %+v, operation = %+v", captured, repository.operations[0])
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := SignerCachePath(cacheRoot, snapshot.Entries[0].PackageSHA256, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("verified digest cache missing: %v", err)
	}
	delete(repository.current, source.ID) // deleting a source does not remove package bytes.
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("source deletion removed installed-cache candidate: %v", err)
	}
}

func TestIncompatibleRefreshKeepsCurrentSnapshot(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{"community": {ID: "stable", SourceID: "community", Commit: "old"}}}
	validator := marketTestValidator(plugins.ValidatorOptions{HostVersion: "1.0.0", AgentVersion: "1.0.0"})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	fixture := marketplaceFixture(t, false)
	packageRoot := filepath.Join(fixture, "plugins", "example.plugin", "1.0.0")
	manifestPath := filepath.Join(packageRoot, plugins.PackageManifestFile)
	manifest, _ := os.ReadFile(manifestPath)
	writeMarketFixture(t, packageRoot, plugins.PackageManifestFile, strings.Replace(string(manifest), `compatibility: {host: "*", agent: "*"}`, `compatibility: {host: ">=9.0.0", agent: "*"}`, 1))
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeMarketFixture(t, packageRoot, plugins.PackageDigestFile, digest)
	marketPath := filepath.Join(fixture, plugins.MarketManifestFile)
	market, _ := os.ReadFile(marketPath)
	marketText := strings.Replace(string(market), `compatibility: {host: "*", agent: "*"}`, `compatibility: {host: ">=9.0.0", agent: "*"}`, 1)
	fields := strings.Fields(marketText)
	for index := range fields {
		if len(fields[index]) == 64 {
			marketText = strings.Replace(marketText, fields[index], digest, 1)
			break
		}
	}
	writeMarketFixture(t, fixture, plugins.MarketManifestFile, marketText)
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := marketplaceSignedTestSource()
	if _, err := manager.Refresh(ctx, source, OperationActor{}); err == nil {
		t.Fatal("runtime-incompatible market refresh succeeded")
	}
	current, ok, err := repository.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != "stable" {
		t.Fatalf("incompatible refresh changed current snapshot: %+v, %v, %v", current, ok, err)
	}
}

func TestSnapshotDiffReportsAddedChangedAndRemovedEntries(t *testing.T) {
	current := Snapshot{ID: "old", Entries: []plugins.MarketEntry{{ID: "removed", Version: "1.0.0"}, {ID: "changed", Version: "1.0.0"}, {ID: "multi", Version: "2.0.0"}, {ID: "multi", Version: "1.0.0"}}}
	next := Snapshot{ID: "new", Entries: []plugins.MarketEntry{{ID: "added", Version: "1.0.0"}, {ID: "changed", Version: "2.0.0"}, {ID: "multi", Version: "3.0.0"}, {ID: "multi", Version: "2.0.0"}}}
	diff := snapshotDiff(current, true, next)
	var decoded map[string]string
	if err := json.Unmarshal([]byte(diff), &decoded); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"added": "added:1.0.0", "changed": "1.0.0->2.0.0", "multi": "1.0.0,2.0.0->2.0.0,3.0.0", "removed": "removed:1.0.0"}
	for id, value := range expected {
		if decoded[id] != value {
			t.Fatalf("snapshot diff %s has %s=%q, want %q", diff, id, decoded[id], value)
		}
	}
}

func TestSnapshotDiffReportsSameVersionDigestReplacement(t *testing.T) {
	current := Snapshot{ID: "old", Entries: []plugins.MarketEntry{{ID: "changed", Version: "1.0.0", PackageSHA256: strings.Repeat("a", 64)}}}
	next := Snapshot{ID: "new", Entries: []plugins.MarketEntry{{ID: "changed", Version: "1.0.0", PackageSHA256: strings.Repeat("b", 64)}}}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(snapshotDiff(current, true, next)), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded["changed"], "digest_changed:1.0.0@") {
		t.Fatalf("same-version digest replacement was hidden: %v", decoded)
	}
}

func TestIndependentManagersShareRepositoryRefreshLease(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	fixture := marketplaceFixture(t, false)
	first, err := NewManager(filepath.Join(t.TempDir(), "first"), blockingFetcher{source: fixture, started: started, release: release}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(filepath.Join(t.TempDir(), "second"), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := marketplaceSignedTestSource()
	result := make(chan error, 1)
	go func() { _, refreshErr := first.Refresh(ctx, source, OperationActor{}); result <- refreshErr }()
	<-started
	if _, err := second.Refresh(ctx, source, OperationActor{}); !errors.Is(err, ErrRefreshLeaseHeld) {
		t.Fatalf("second manager refresh error = %v", err)
	}
	if len(repository.operations) < 2 || repository.operations[1].Status != "rejected" || repository.operations[1].ErrorClass != "lease_contention" {
		t.Fatalf("lease contention was not audited: %+v", repository.operations)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	rejection := repository.operations[1]
	if rejection.SourceRevision != source.ConfigRevision || rejection.RefKind != source.RefKind || rejection.RefName != source.RefName || rejection.SignerSourceKind != trust.SourceKind || rejection.SignerKeyID != trust.KeyID || rejection.SignerPublicKey != trust.PublicKey || rejection.SignerFingerprint != trust.Fingerprint {
		t.Fatalf("lease contention rejection lost immutable attempt provenance: %+v", rejection)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestSameManagerRefreshContentionIsImmediateAndCancelable(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), blockingFetcher{source: marketplaceFixture(t, false), started: started, release: release}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := marketplaceSignedTestSource()
	done := make(chan error, 1)
	go func() { _, refreshErr := manager.Refresh(ctx, source, OperationActor{}); done <- refreshErr }()
	<-started
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := manager.Refresh(canceled, source, OperationActor{}); !errors.Is(err, ErrRefreshLeaseHeld) {
		t.Fatalf("same-manager contention = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRefreshRenewsLeaseBeyondInitialTTL(t *testing.T) {
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), blockingFetcher{source: marketplaceFixture(t, false), started: started, release: release}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager.leaseTTL = 30 * time.Millisecond
	source, _ := marketplaceSignedTestSource()
	done := make(chan error, 1)
	go func() {
		_, refreshErr := manager.Refresh(context.Background(), source, OperationActor{})
		done <- refreshErr
	}()
	<-started
	time.Sleep(90 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("renewed refresh failed: %v", err)
	}
	if len(repository.operations) != 1 || repository.operations[0].Status != "succeeded" {
		t.Fatalf("renewed operations = %+v", repository.operations)
	}
}

func TestCanceledRefreshUsesIndependentContextForTerminalFailure(t *testing.T) {
	repository := &memoryRepository{current: map[string]Snapshot{}, rejectCanceled: true}
	validator := marketTestValidator(plugins.ValidatorOptions{})
	cache, err := newTestVerifiedCache(t, filepath.Join(t.TempDir(), "plugins", "packages"), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), canceledFetcher{}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := marketplaceSignedTestSource()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Refresh(ctx, source, OperationActor{ActorID: "admin", CorrelationID: "request"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh error = %v", err)
	}
	if len(repository.operations) != 1 || repository.operations[0].Status != "failed" || repository.operations[0].FinishedAt == nil || repository.operations[0].Actor.CorrelationID != "request" {
		t.Fatalf("canceled refresh was not terminally persisted: %+v", repository.operations)
	}
}

func TestLeaseRenewalFailureDuringFinalPromotionCannotPublishSnapshot(t *testing.T) {
	fixture := marketplaceFixture(t, false)
	repository := &memoryRepository{current: map[string]Snapshot{"community": {ID: "stable", SourceID: "community", Commit: "old"}}, renewError: errors.New("injected renewal failure"), promotionDelay: 100 * time.Millisecond}
	validator := marketTestValidator(plugins.ValidatorOptions{HostVersion: "1.0.0", AgentVersion: "1.0.0"})
	cache, err := newTestVerifiedCache(t, t.TempDir(), validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	manager.leaseTTL = 15 * time.Millisecond
	source, _ := marketplaceSignedTestSource()
	if _, err := manager.Refresh(context.Background(), source, OperationActor{ActorID: "admin"}); err == nil {
		t.Fatal("renewal failure during promotion was ignored")
	}
	current, _, _ := repository.CurrentSnapshot(context.Background(), source.ID)
	if current.ID != "stable" {
		t.Fatalf("renewal failure published snapshot: %+v", current)
	}
}

type copyFetcher struct{ source string }

func (f copyFetcher) Fetch(_ context.Context, _ Source, destination string) (string, error) {
	return "0123456789abcdef", copyFixtureTree(f.source, destination)
}

type lockedCopyFetcher struct {
	lockedRoot  string
	normalCalls int
	commits     []string
	lastOID     string
	refs        []string
}

func (f *lockedCopyFetcher) Fetch(_ context.Context, source Source, destination string) (string, error) {
	if source.ID != OfficialSourceID || source.Kind != SourceKindOfficial || source.URL != OfficialSourceURL || source.RefKind != GitRefKindBranch {
		return "", errors.New("unexpected source passed to official fetch")
	}
	if f.normalCalls >= len(f.commits) {
		return "", errors.New("official fetch called more times than configured")
	}
	commit := f.commits[f.normalCalls]
	f.normalCalls++
	f.lastOID = commit
	f.refs = append(f.refs, source.RefName)
	return commit, copyFixtureTree(f.lockedRoot, destination)
}

type blockingFetcher struct {
	source  string
	started chan struct{}
	release chan struct{}
}

type canceledFetcher struct{}

func (canceledFetcher) Fetch(ctx context.Context, _ Source, _ string) (string, error) {
	return "", ctx.Err()
}

func (f blockingFetcher) Fetch(ctx context.Context, _ Source, destination string) (string, error) {
	close(f.started)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-f.release:
	}
	return "0123456789abcdef", copyFixtureTree(f.source, destination)
}

type memoryRepository struct {
	mu             sync.Mutex
	current        map[string]Snapshot
	operations     []RefreshOperation
	referenced     map[string]bool
	rejectCanceled bool
	renewError     error
	promotionDelay time.Duration
	acquireError   error
	officialSource Source
}

func (r *memoryRepository) ReconcileOfficialMarketplaceSource(_ context.Context, desired Source) (Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.officialSource
	if current.ID == "" {
		current = OfficialSource()
	}
	if current.RefKind == desired.RefKind && current.RefName == desired.RefName {
		return current, nil
	}
	desired.ConfigRevision = current.ConfigRevision + 1
	if err := ValidateSource(desired); err != nil {
		return Source{}, err
	}
	r.officialSource = desired
	return desired, nil
}

func (r *memoryRepository) RecordRefreshRejection(_ context.Context, operation RefreshOperation, errorClass string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	operation.Status, operation.ErrorClass = "rejected", errorClass
	r.operations = append(r.operations, operation)
	return nil
}
func (r *memoryRepository) RenewRefreshLease(_ context.Context, operation RefreshOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.renewError != nil {
		return r.renewError
	}
	for index := range r.operations {
		if r.operations[index].ID == operation.ID && r.operations[index].LeaseToken == operation.LeaseToken && r.operations[index].Status == "running" {
			r.operations[index].LeaseExpiresAt = operation.LeaseExpiresAt
			return nil
		}
	}
	return errors.New("stale lease")
}
func (r *memoryRepository) StagePackageAcquisition(context.Context, string, string, string, SignatureTrust) error {
	return nil
}
func (r *memoryRepository) PublishPackageAcquisition(_ context.Context, _, _, _ string, _ SignatureTrust, publish func() error) error {
	return publish()
}
func (r *memoryRepository) CompletePackageAcquisitions(context.Context, string, string, bool) error {
	return nil
}

func (r *memoryRepository) AcquireRefreshLease(_ context.Context, operation RefreshOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.acquireError != nil {
		return r.acquireError
	}
	for index := range r.operations {
		current := &r.operations[index]
		if current.SourceID != operation.SourceID || current.Status != "running" {
			continue
		}
		if current.LeaseExpiresAt.After(operation.StartedAt) {
			return ErrRefreshLeaseHeld
		}
		finished := operation.StartedAt
		current.Status, current.ErrorClass, current.FinishedAt = "failed", "interrupted", &finished
	}
	r.operations = append(r.operations, operation)
	return nil
}

func (r *memoryRepository) SaveRefreshOperation(ctx context.Context, operation RefreshOperation) error {
	if r.rejectCanceled && ctx.Err() != nil {
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.operations {
		if r.operations[index].ID == operation.ID {
			r.operations[index] = operation
			return nil
		}
	}
	r.operations = append(r.operations, operation)
	return nil
}

func (r *memoryRepository) PromoteSnapshotAndCompleteRefresh(ctx context.Context, source Source, snapshot Snapshot, operation RefreshOperation) error {
	if r.promotionDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.promotionDelay):
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.operations {
		if r.operations[index].ID == operation.ID && r.operations[index].Status == "running" && r.operations[index].LeaseToken == operation.LeaseToken && !operation.FinishedAt.After(r.operations[index].LeaseExpiresAt) {
			r.current[source.ID] = snapshot
			r.operations[index] = operation
			return nil
		}
	}
	return errors.New("refresh operation missing")
}

func (r *memoryRepository) CurrentSnapshot(_ context.Context, sourceID string) (Snapshot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, ok := r.current[sourceID]
	return snapshot, ok, nil
}

func (r *memoryRepository) PackageReferenced(_ context.Context, digest string) (bool, error) {
	return r.referenced[digest], nil
}

func marketplaceFixture(t *testing.T, official bool) string {
	t.Helper()
	root := t.TempDir()
	packageRoot := filepath.Join(root, "plugins", "example.plugin", "1.0.0")
	artifact := marketPolicyWASMFixture(t)
	artifactDigest := sha256.Sum256(artifact)
	writeMarketFixture(t, packageRoot, plugins.PackageManifestFile, fmt.Sprintf(`schema_version: 1
id: example.plugin
version: 1.0.0
name: Example
compatibility: {host: "*", agent: "*"}
runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, entry: artifacts/policy.wasm, policy_kind: waf}
artifacts:
  - {path: artifacts/policy.wasm, sha256: %x, size: %d, mode: wasm}
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 8, input_bytes: 65536, output_bytes: 4096}
failure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: test-market, file: package.sig}
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`, artifactDigest, len(artifact)))
	writeMarketFixture(t, packageRoot, plugins.ConfigSchemaFile, `{"type":"object"}`)
	writeMarketFixtureBytes(t, packageRoot, "artifacts/policy.wasm", artifact)
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeMarketFixture(t, packageRoot, plugins.PackageDigestFile, digest)
	writeMarketFixture(t, packageRoot, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(marketTestSigningKey(), []byte(digest))))
	provenance := "custom"
	if official {
		provenance = "sakullla-plugins"
	}
	writeMarketFixture(t, root, plugins.MarketManifestFile, `schema_version: 1
name: Test
plugins:
  - id: example.plugin
    version: 1.0.0
    capabilities: [http.request]
    compatibility: {host: "*", agent: "*"}
    runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, policy_kind: waf}
    artifacts:
      - {sha256: `+fmt.Sprintf("%x", artifactDigest)+`, size: `+fmt.Sprintf("%d", len(artifact))+`}
    package: plugins/example.plugin/1.0.0
    sha256: `+digest+`
    signature_key_id: test-market
    provenance: `+provenance+`
    official: `+map[bool]string{true: "true", false: "false"}[official]+`
`)
	return root
}

func marketPolicyWASMFixture(t *testing.T) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func writeMarketFixture(t *testing.T, root, name, value string) {
	t.Helper()
	writeMarketFixtureBytes(t, root, name, []byte(value))
}

func writeMarketFixtureBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func marketTestSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-market-test-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func marketplaceSignedTestSource() (Source, error) {
	return NewSignedCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0, SourceSigner{KeyID: "test-market", SecretRef: "vault-test-market", PublicKey: base64.StdEncoding.EncodeToString(marketTestSigningKey().Public().(ed25519.PublicKey))})
}

func marketTestValidator(options plugins.ValidatorOptions) *plugins.Validator {
	if options.TrustedSigners == nil {
		options.TrustedSigners = map[string]ed25519.PublicKey{}
	}
	options.TrustedSigners["test-market"] = marketTestSigningKey().Public().(ed25519.PublicKey)
	return plugins.NewValidator(options)
}

func newTestVerifiedCache(t *testing.T, root string, validator *plugins.Validator, references PackageReferenceChecker) (*VerifiedCache, error) {
	t.Helper()
	if filepath.Base(root) != "packages" || filepath.Base(filepath.Dir(root)) != "plugins" {
		root = filepath.Join(root, "plugins", "packages")
	}
	cache, err := NewVerifiedCache(root, validator, references)
	if err == nil {
		t.Cleanup(func() { _ = DiscardVerifiedCacheRoot(root) })
	}
	return cache, err
}

func copyFixtureTree(source, destination string) error {
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(source, current)
		if rel == "." {
			return os.MkdirAll(destination, 0o755)
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
