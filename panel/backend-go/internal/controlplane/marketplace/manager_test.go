package marketplace

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
}

func TestRefreshValidationFailureKeepsCurrentSnapshotAndCache(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{"community": {ID: "stable", SourceID: "community", Commit: "old"}}}
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	cacheRoot := filepath.Join(t.TempDir(), "packages")
	cache, err := NewVerifiedCache(cacheRoot, validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	invalid := marketplaceFixture(t, false)
	if err := os.WriteFile(filepath.Join(invalid, "plugins", "example.plugin", "1.0.0", plugins.PackageDigestFile), []byte(strings.Repeat("0", 64)), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: invalid}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx, source); err == nil {
		t.Fatal("refresh with invalid package unexpectedly succeeded")
	}
	current, ok, err := repository.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != "stable" {
		t.Fatalf("failed refresh replaced current snapshot: %+v, %v, %v", current, ok, err)
	}
	if len(repository.operations) != 1 || repository.operations[0].Status != "failed" || repository.operations[0].ErrorClass != "validation" {
		t.Fatalf("refresh failure was not persisted: %+v", repository.operations)
	}
}

func TestRefreshPromotesOnlyAfterValidationAndKeepsDigestCache(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{current: map[string]Snapshot{}}
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	cacheRoot := filepath.Join(t.TempDir(), "packages")
	cache, err := NewVerifiedCache(cacheRoot, validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	fixture := marketplaceFixture(t, false)
	manager, err := NewManager(filepath.Join(t.TempDir(), "marketplace"), copyFetcher{source: fixture}, validator, cache, repository)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	snapshot, err := manager.Refresh(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if repository.current[source.ID].ID != snapshot.ID || repository.operations[0].Status != "succeeded" {
		t.Fatalf("snapshot or refresh operation was not promoted: %+v %+v", repository.current, repository.operations)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, snapshot.Entries[0].PackageSHA256)); err != nil {
		t.Fatalf("verified digest cache missing: %v", err)
	}
	delete(repository.current, source.ID) // deleting a source does not remove package bytes.
	if _, err := os.Stat(filepath.Join(cacheRoot, snapshot.Entries[0].PackageSHA256)); err != nil {
		t.Fatalf("source deletion removed installed-cache candidate: %v", err)
	}
}

func TestSnapshotDiffReportsAddedChangedAndRemovedEntries(t *testing.T) {
	current := Snapshot{ID: "old", Entries: []plugins.MarketEntry{{ID: "removed", Version: "1.0.0"}, {ID: "changed", Version: "1.0.0"}}}
	next := Snapshot{ID: "new", Entries: []plugins.MarketEntry{{ID: "added", Version: "1.0.0"}, {ID: "changed", Version: "2.0.0"}}}
	diff := snapshotDiff(current, true, next)
	var decoded map[string]string
	if err := json.Unmarshal([]byte(diff), &decoded); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"added": "added:1.0.0", "changed": "1.0.0->2.0.0", "removed": "removed:1.0.0"}
	for id, value := range expected {
		if decoded[id] != value {
			t.Fatalf("snapshot diff %s has %s=%q, want %q", diff, id, decoded[id], value)
		}
	}
}

type copyFetcher struct{ source string }

func (f copyFetcher) Fetch(_ context.Context, _ Source, destination string) (string, error) {
	return "0123456789abcdef", copyFixtureTree(f.source, destination)
}

type memoryRepository struct {
	mu         sync.Mutex
	current    map[string]Snapshot
	operations []RefreshOperation
	referenced map[string]bool
}

func (r *memoryRepository) SaveRefreshOperation(_ context.Context, operation RefreshOperation) error {
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

func (r *memoryRepository) PromoteSnapshot(_ context.Context, source Source, snapshot Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current[source.ID] = snapshot
	return nil
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
	writeMarketFixture(t, packageRoot, plugins.PackageManifestFile, `schema_version: 1
id: example.plugin
version: 1.0.0
name: Example
compatibility: {host: "*", agent: "*"}
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`)
	writeMarketFixture(t, packageRoot, plugins.ConfigSchemaFile, `{"type":"object"}`)
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeMarketFixture(t, packageRoot, plugins.PackageDigestFile, digest)
	writeMarketFixture(t, root, plugins.MarketManifestFile, `schema_version: 1
name: Test
plugins:
  - id: example.plugin
    version: 1.0.0
    compatibility: {host: "*", agent: "*"}
    package: plugins/example.plugin/1.0.0
    sha256: `+digest+`
    official: `+map[bool]string{true: "true", false: "false"}[official]+`
`)
	return root
}

func writeMarketFixture(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
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
