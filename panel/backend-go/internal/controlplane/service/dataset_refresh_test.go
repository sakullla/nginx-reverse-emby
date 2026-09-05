//go:build !fast && !integration

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func rollingDatasetFixture(t *testing.T) *DatasetService {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewSQLiteStore(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	service := NewDatasetService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	t.Cleanup(func() { _ = service.Close(); _ = store.Close() })
	// Seed an existing enabled policy through the same durable owner tables as
	// package installation. Refresh itself uses the production snapshot builder,
	// revision executor and artifact ledger, with no injected snapshot/index.
	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "panel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	wasm := []byte("fixture policy artifact; this test executes the dataset index")
	wasmSum := sha256.Sum256(wasm)
	wasmDigest := hex.EncodeToString(wasmSum[:])
	digest := strings.Repeat("a", 64)
	identity := strings.Repeat("b", 64)
	fingerprint := strings.Repeat("c", 64)
	cacheRoot := filepath.Join(root, "plugins", "packages")
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cachePath, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "artifacts", "policy.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := plugins.Manifest{SchemaVersion: 1, ID: "rolling-policy", Version: "1.0.0", Name: "Rolling policy", Runtime: plugins.Runtime{Kind: sdk.RuntimeWASMPolicy, ABI: sdk.PolicyABIV1, HostScope: sdk.HostScopeAgent, Entry: "artifacts/policy.wasm", PolicyKind: "ip"}, Artifacts: []plugins.Artifact{{Path: "artifacts/policy.wasm", SHA256: wasmDigest, Size: int64(len(wasm)), Mode: "wasm"}}, ExtensionPoints: []string{"http.request"}, Permissions: []plugins.Permission{{Name: string(sdk.CapabilityDatasetQuery)}}, ResourceBudget: plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096}, FailurePolicy: plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}, Signature: plugins.Signature{Algorithm: "ed25519", KeyID: "fixture"}}
	manifestJSON, _ := json.Marshal(manifest)
	packageRow, artifacts, err := storage.ProjectPluginPackage(storage.PluginPackageRow{Identity: identity, Digest: digest, PluginID: manifest.ID, Version: manifest.Version, SignatureKeyID: "fixture", SignaturePublicKey: "pub", SignatureFingerprint: fingerprint, SignatureVerdict: "verified", SourceID: "fixture", SourceKind: "custom", CachePath: cachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	packageRow.CachePath = cachePath
	if err := db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: digest, ActivePackageIdentity: identity, RuntimeKind: packageRow.RuntimeKind, RuntimeABI: packageRow.RuntimeABI, HostScope: packageRow.HostScope, ActiveSourceID: "fixture", ActiveSourceKind: "custom", ActiveSignatureKeyID: "fixture", ActiveSignaturePublicKey: "pub", ActiveSignatureFingerprint: fingerprint, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, StateVersion: 1, InstalledAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	instance := storage.PluginInstanceRow{ID: "rolling-instance", PluginID: manifest.ID, ResourceGroupID: "default", TargetJSON: `["local"]`, PolicyChainsJSON: `["rolling-instance"]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 1, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: time.Now().UTC()}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	return service
}

func TestDatasetRollingRefreshActivatesNewBoundVersionWithoutRepinning(t *testing.T) {
	service := rollingDatasetFixture(t)
	auth := DatasetAuthorization{ActorID: "administrator", ResourceGroupID: "default", Administrator: true, Manage: true}
	first := datasetServiceCIDR("cn-44")
	second := []byte(strings.Replace(string(first), "192.0.2.0/24", "198.51.100.0/24", 1))
	removed := datasetServiceCIDR("cn-32")
	var mu sync.Mutex
	body := first
	checksum := datasetServiceDigest(first)[7:] + "  regions.json\n"
	metadataReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/regions.json.sha256sum" {
			metadataReads++
			_, _ = w.Write([]byte(checksum))
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	source := sdk.DatasetSource{ID: "rolling", Name: "Rolling regions", URL: server.URL + "/regions.json", Format: sdk.DatasetFormatCIDR, RefreshIntervalSeconds: 3600}
	if err := service.PutSource(t.Context(), auth, source, DatasetRetrieval{Mode: datasetRetrievalRolling, ChecksumURL: server.URL + "/regions.json.sha256sum", AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		if err := service.RefreshDue(t.Context(), time.Now().Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	row, err := service.store.GetDatasetSource(t.Context(), source.ID)
	if err != nil || row.CurrentDigest == "" {
		t.Fatal("first scheduled release was not activated", err)
	}
	firstVersion := row.CurrentDigest
	if err := service.Bind(t.Context(), auth, DatasetBindingRequest{SourceID: source.ID, VersionDigest: firstVersion, AgentID: "local", InstanceID: "rolling-instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}); err != nil {
		t.Fatal(err)
	}
	checkMatching := func(want bool) string {
		t.Helper()
		bindings, err := service.store.DatasetBindings(t.Context(), source.ID)
		if err != nil || len(bindings) != 1 {
			t.Fatal("missing persisted binding", err)
		}
		pointer, found, err := service.store.GetAgentRevisionPointer(t.Context(), "local")
		if err != nil || !found || pointer.DesiredRevision != bindings[0].Revision {
			t.Fatal("binding did not advance through actual revision", err)
		}
		snapshot, err := service.store.LoadLocalSnapshot(t.Context(), "local")
		if err != nil || len(snapshot.Datasets) != 1 || snapshot.Datasets[0].Version.Digest != bindings[0].VersionDigest {
			t.Fatalf("live snapshot omitted refreshed dataset: %+v %v", snapshot.Datasets, err)
		}
		index, err := service.loadIndex(t.Context(), source.ID, bindings[0].VersionDigest)
		if err != nil {
			t.Fatal(err)
		}
		request := sdk.DatasetQueryRequest{Reference: sdk.DatasetReference{Handle: strings.Repeat("x", 32), InstanceID: "rolling-instance", Generation: "test-generation", SourceID: source.ID, VersionDigest: bindings[0].VersionDigest}, Address: "192.0.2.1", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: 2000, MaxResponseBytes: 32768}}
		response, err := index.Query(t.Context(), request)
		if err != nil || response.Status != sdk.DatasetQueryOK || response.Matches[0].Matched != want {
			t.Fatalf("bound match did not change: %+v %v", response, err)
		}
		return bindings[0].VersionDigest
	}
	if got := checkMatching(true); got != firstVersion {
		t.Fatal("initial binding differs")
	}
	// A deleted consumer must not remain in the source's future rollout targets.
	if err := service.store.SaveAgent(t.Context(), storage.AgentRow{ID: "deleted-edge", AgentToken: "retired-token"}); err != nil {
		t.Fatal(err)
	}
	if err := service.store.PutDatasetBinding(t.Context(), storage.DatasetBindingRow{AgentID: "deleted-edge", InstanceID: "rolling-instance", SourceID: source.ID, VersionDigest: firstVersion, ClassificationsJSON: `[{"name":"cn-44","kind":"region"}]`, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	live, _ := service.store.LoadLocalSnapshot(t.Context(), "local")
	retained := storage.Snapshot{Revision: 1, Datasets: live.Datasets}
	retainedJSON, _ := json.Marshal(retained)
	retainedSum := sha256.Sum256(retainedJSON)
	retainedDigest := hex.EncodeToString(retainedSum[:])
	if _, err := service.store.EnsureAgentHeartbeatRevision(t.Context(), "deleted-edge", retained, retainedJSON, retainedDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := service.store.DeleteAgent(t.Context(), "deleted-edge"); err != nil {
		t.Fatal(err)
	}
	remainingBindings, err := service.store.DatasetBindings(t.Context(), source.ID)
	if err != nil || len(remainingBindings) != 1 || remainingBindings[0].AgentID != "local" {
		t.Fatal("deleted Agent retained desired dataset references", err)
	}
	if _, err := service.store.ResolveAgentRevisionDatasetArtifact(t.Context(), "deleted-edge", 1, retainedDigest, retained.Datasets[0].Artifact.ID); err != nil {
		t.Fatal("Agent deletion removed retained historical dataset artifact", err)
	}
	mu.Lock()
	body = second
	checksum = datasetServiceDigest(second)[7:] + " *regions.json\n"
	mu.Unlock()
	refresh()
	secondVersion := checkMatching(false)
	if secondVersion == firstVersion {
		t.Fatal("scheduled refresh kept permanently pinned data")
	}
	row, _ = service.store.GetDatasetSource(t.Context(), source.ID)
	if row.CurrentDigest != secondVersion {
		t.Fatal("source and consumer binding activated different versions")
	}
	stored, err := service.store.GetDatasetVersion(t.Context(), source.ID, secondVersion)
	if err != nil {
		t.Fatal(err)
	}
	var version sdk.DatasetVersion
	_ = json.Unmarshal([]byte(stored.VersionJSON), &version)
	if version.Revision != "checksum-"+datasetServiceDigest(second) || version.RawDigest != datasetServiceDigest(second) || stored.VerificationJSON == "" {
		t.Fatal("rolling version did not retain immutable checksum provenance")
	}
	before, _, _ := service.store.GetAgentRevisionPointer(t.Context(), "local")
	refresh()
	after, _, _ := service.store.GetAgentRevisionPointer(t.Context(), "local")
	unchanged, _ := service.store.GetDatasetVersion(t.Context(), source.ID, secondVersion)
	if before.DesiredRevision != after.DesiredRevision || unchanged.VerificationJSON != stored.VerificationJSON {
		t.Fatal("unchanged rolling refresh changed immutable evidence or issued a new revision")
	}
	for _, variant := range []string{"malformed", "ambiguous", "oversized", "racing pair", "corrupt body", "missing class"} {
		t.Run(variant, func(t *testing.T) {
			mu.Lock()
			body = second
			checksum = datasetServiceDigest(second)[7:] + "  regions.json\n"
			switch variant {
			case "malformed":
				checksum = "not a checksum"
			case "ambiguous":
				checksum += checksum
			case "oversized":
				checksum = strings.Repeat("x", int(datasetChecksumMaxBytes)+1)
			case "racing pair":
				checksum = datasetServiceDigest(first)[7:]
			case "corrupt body":
				body = []byte("corrupt")
			case "missing class":
				body = removed
				checksum = datasetServiceDigest(removed)[7:] + "  regions.json\n"
			}
			mu.Unlock()
			if err := service.RefreshDue(t.Context(), time.Now().Add(24*time.Hour)); err == nil {
				t.Fatal("invalid rolling candidate activated")
			}
			if got := checkMatching(false); got != secondVersion {
				t.Fatal("failed refresh replaced active bound version")
			}
			row, _ := service.store.GetDatasetSource(t.Context(), source.ID)
			if row.CurrentDigest != secondVersion || row.LastFailure == "" {
				t.Fatal("candidate failure lost previous source state")
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if metadataReads != 9 {
		t.Fatalf("each refresh must resolve a fresh checksum, got %d", metadataReads)
	}
}

func TestDatasetChecksumMetadataIsStrictAndSeparatelyBounded(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, good := range []string{hash, hash + "  geoip.dat\n", hash + " *geoip.dat\r\n", hash + "  ./geoip.dat\n"} {
		if digest, err := parseDatasetChecksum([]byte(good), "https://example.com/releases/latest/download/geoip.dat"); err != nil || digest != "sha256:"+hash {
			t.Fatalf("compatible sidecar rejected: %q %v", good, err)
		}
	}
	for _, bad := range []string{"", hash + "  other.dat", hash + "  ../geoip.dat", hash + "\n" + hash, hash + "  geoip.dat extra", strings.Repeat("g", 64), "\x00" + hash, strings.Repeat(" ", int(datasetChecksumMaxBytes)) + hash} {
		if _, err := parseDatasetChecksum([]byte(bad), "https://example.com/geoip.dat"); err == nil {
			t.Fatalf("ambiguous/malformed metadata accepted: %q", bad)
		}
	}
	source := sdk.DatasetSource{ID: "geoip", Name: "GeoIP", URL: "https://example.com/geoip.dat", Format: sdk.DatasetFormatGeoIP, RefreshIntervalSeconds: 3600}
	for _, bad := range []DatasetRetrieval{{Mode: "unknown"}, {Mode: datasetRetrievalRolling}, {Mode: datasetRetrievalRolling, ChecksumURL: "https://example.com/hash", ExpectedDigest: "sha256:" + hash}, {ChecksumURL: "https://example.com/hash"}} {
		if bad.Validate(source) == nil {
			t.Fatal("invalid rolling authority accepted")
		}
	}
	if err := (DatasetRetrieval{Mode: datasetRetrievalPinned, Revision: "fixed", ExpectedDigest: "sha256:" + hash}).Validate(source); err != nil {
		t.Fatal(fmt.Errorf("pinned mode regressed: %w", err))
	}
}
