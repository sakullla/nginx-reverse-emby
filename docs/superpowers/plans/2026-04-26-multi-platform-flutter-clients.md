# Multi-platform Flutter Clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitHub-distributed Windows, macOS, Android Flutter client support plus Cloudflare Worker deployment metadata and panel guidance, without embedding client artifacts in the control-plane container.

**Architecture:** The control plane stores release metadata for Flutter GUI packages, Go agent packages, and Worker scripts. The Vue panel manages those GitHub-hosted artifacts and renders a Worker deployment wizard. A new Flutter app under `clients/flutter/` provides shared Windows/macOS/Android GUI screens; desktop builds supervise the local Go agent while Android runs in light-management mode.

**Tech Stack:** Go 1.26, GORM/SQLite, Vue 3/Vite/Vitest, Flutter/Dart, GitHub Actions.

---

## File Structure Map

Backend release packages:

- Create `panel/backend-go/internal/controlplane/service/client_packages.go`: domain model, validation, CRUD service, latest-compatible selection.
- Create `panel/backend-go/internal/controlplane/service/client_packages_test.go`: service and selection tests.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add `ClientPackageRow`.
- Modify `panel/backend-go/internal/controlplane/storage/schema.go`: migrate `client_packages`.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: add list/save methods.
- Create `panel/backend-go/internal/controlplane/storage/client_packages_test.go`: SQLite persistence tests.
- Create `panel/backend-go/internal/controlplane/http/handlers_client_packages.go`: REST handlers.
- Modify `panel/backend-go/internal/controlplane/http/router.go`: dependency interface and routes.
- Modify `panel/backend-go/internal/controlplane/http/router_test.go`: fake service and API route coverage.
- Modify `panel/backend-go/cmd/nre-control-plane/main.go` only if dependency wiring requires explicit service construction.

Panel release package UI and Worker wizard:

- Modify `panel/frontend/src/api/index.js`, `runtime.js`, `devRuntime.js`, `devMocks/index.js`, and `devMocks/data.js`: client package API functions and dev mocks.
- Create `panel/frontend/src/hooks/useClientPackages.js`: Vue Query hooks.
- Create `panel/frontend/src/utils/workerDeploy.js`: deterministic Worker deployment command/script reference generation.
- Create `panel/frontend/src/utils/workerDeploy.test.mjs`: validation tests.
- Create `panel/frontend/src/pages/ClientPackagesPage.vue`: package management page and Worker wizard entry.
- Modify `panel/frontend/src/router/index.js`: add `/client-packages`.
- Modify `panel/frontend/src/components/layout/Sidebar.vue`: add navigation link.

Flutter client:

- Create `clients/flutter/`: Flutter project.
- Create `clients/flutter/lib/core/platform_capabilities.dart`: per-platform feature flags.
- Create `clients/flutter/lib/core/client_state.dart`: local state enum and reducer.
- Create `clients/flutter/lib/services/master_api.dart`: Master API client.
- Create `clients/flutter/lib/services/local_agent_controller.dart`: desktop runtime abstraction.
- Create `clients/flutter/lib/services/local_agent_controller_stub.dart`: Android no-runtime implementation.
- Create `clients/flutter/lib/screens/register_screen.dart`, `overview_screen.dart`, `runtime_screen.dart`, `logs_screen.dart`, `updates_screen.dart`, `settings_screen.dart`, `about_screen.dart`.
- Create `clients/flutter/test/client_state_test.dart`, `platform_capabilities_test.dart`, `register_screen_test.dart`.

GitHub distribution:

- Create `.github/workflows/client-release.yml`: builds and uploads Flutter clients, Go agent desktop packages, and Worker script assets to GitHub Release.
- Create `workers/cloudflare/nre-worker.js`: first Worker script asset.
- Create `workers/cloudflare/README.md`: deployment variables and GitHub distribution notes.
- Modify `README.md`: document GitHub Release distribution and no-container-artifact policy.

---

## Task 1: Backend Client Package Storage

**Files:**

- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Create: `panel/backend-go/internal/controlplane/storage/client_packages_test.go`

- [ ] **Step 1: Write the SQLite persistence test**

Create `panel/backend-go/internal/controlplane/storage/client_packages_test.go`:

```go
package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteStoreClientPackagesRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	rows := []ClientPackageRow{
		{
			ID:          "pkg-windows-amd64",
			Version:     "1.1.0",
			Platform:    "windows",
			Arch:        "amd64",
			Kind:        "flutter_gui",
			DownloadURL: "https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip",
			SHA256:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Notes:       "desktop gui",
			CreatedAt:   "2026-04-26T00:00:00Z",
		},
		{
			ID:          "pkg-worker-script",
			Version:     "1.1.0",
			Platform:    "cloudflare_worker",
			Arch:        "script",
			Kind:        "worker_script",
			DownloadURL: "https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js",
			SHA256:      "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
			Notes:       "worker script",
			CreatedAt:   "2026-04-26T00:01:00Z",
		},
	}

	if err := store.SaveClientPackages(ctx, rows); err != nil {
		t.Fatalf("SaveClientPackages() error = %v", err)
	}
	got, err := store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "pkg-worker-script" && got[1].ID != "pkg-worker-script" {
		t.Fatalf("worker package missing: %+v", got)
	}

	if err := store.SaveClientPackages(ctx, rows[:1]); err != nil {
		t.Fatalf("SaveClientPackages(replace) error = %v", err)
	}
	got, err = store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() after replace error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "pkg-windows-amd64" {
		t.Fatalf("replace got %+v", got)
	}
}
```

- [ ] **Step 2: Run the storage test and verify it fails**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/storage -run TestSQLiteStoreClientPackagesRoundTrip -count=1
```

Expected: FAIL because `ClientPackageRow`, `SaveClientPackages`, and `ListClientPackages` are not defined.

- [ ] **Step 3: Add the storage row model**

In `panel/backend-go/internal/controlplane/storage/sqlite_models.go`, add:

```go
type ClientPackageRow struct {
	ID          string `gorm:"column:id;primaryKey"`
	Version     string `gorm:"column:version;index:idx_client_packages_match"`
	Platform    string `gorm:"column:platform;index:idx_client_packages_match"`
	Arch        string `gorm:"column:arch;index:idx_client_packages_match"`
	Kind        string `gorm:"column:kind;index:idx_client_packages_match"`
	DownloadURL string `gorm:"column:download_url"`
	SHA256      string `gorm:"column:sha256"`
	Notes       string `gorm:"column:notes"`
	CreatedAt   string `gorm:"column:created_at"`
}
```

Add the table name method near the other `TableName()` methods:

```go
func (ClientPackageRow) TableName() string {
	return "client_packages"
}
```

- [ ] **Step 4: Migrate the table**

In `panel/backend-go/internal/controlplane/storage/schema.go`, add `&ClientPackageRow{},` to the `tx.AutoMigrate(...)` list after `&VersionPolicyRow{},`.

- [ ] **Step 5: Extend the store interface and methods**

In `panel/backend-go/internal/controlplane/storage/sqlite_store.go`, add to `Store`:

```go
ListClientPackages(context.Context) ([]ClientPackageRow, error)
SaveClientPackages(context.Context, []ClientPackageRow) error
```

Add methods near the version policy methods:

```go
func (s *SQLiteStore) ListClientPackages(ctx context.Context) ([]ClientPackageRow, error) {
	var rows []ClientPackageRow
	if err := s.db.WithContext(ctx).Order("kind, platform, arch, version, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		normalizeClientPackageRow(&rows[i])
	}
	return rows, nil
}

func (s *SQLiteStore) SaveClientPackages(ctx context.Context, packages []ClientPackageRow) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ClientPackageRow{}).Error; err != nil {
			return err
		}
		if len(packages) == 0 {
			return nil
		}
		rows := make([]ClientPackageRow, 0, len(packages))
		for _, row := range packages {
			normalizeClientPackageRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}
```

Add the normalizer near `normalizeVersionPolicyRow`:

```go
func normalizeClientPackageRow(row *ClientPackageRow) {
	row.ID = defaultString(row.ID, "")
	row.Version = defaultString(row.Version, "")
	row.Platform = defaultString(row.Platform, "")
	row.Arch = defaultString(row.Arch, "")
	row.Kind = defaultString(row.Kind, "")
	row.DownloadURL = defaultString(row.DownloadURL, "")
	row.SHA256 = defaultString(row.SHA256, "")
	row.Notes = defaultString(row.Notes, "")
	row.CreatedAt = defaultString(row.CreatedAt, "")
}
```

- [ ] **Step 6: Run the storage test and verify it passes**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/storage -run TestSQLiteStoreClientPackagesRoundTrip -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit storage work**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/storage/client_packages_test.go
git commit -m "feat(backend): store client release packages"
```

---

## Task 2: Backend Client Package Service

**Files:**

- Create: `panel/backend-go/internal/controlplane/service/client_packages.go`
- Create: `panel/backend-go/internal/controlplane/service/client_packages_test.go`

- [ ] **Step 1: Write service tests**

Create `panel/backend-go/internal/controlplane/service/client_packages_test.go`:

```go
package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestClientPackageServiceCRUDAndValidation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	svc := NewClientPackageService(store)
	svc.now = func() time.Time { return time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	created, err := svc.Create(ctx, ClientPackageInput{
		Version:     strPtr("1.1.0"),
		Platform:    strPtr(" windows "),
		Arch:        strPtr("amd64"),
		Kind:        strPtr("flutter_gui"),
		DownloadURL: strPtr("https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip"),
		SHA256:      strPtr("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		Notes:       strPtr(" desktop gui "),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" || created.Platform != "windows" || created.CreatedAt != "2026-04-26T00:00:00Z" {
		t.Fatalf("created = %+v", created)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	updated, err := svc.Update(ctx, created.ID, ClientPackageInput{Notes: strPtr("updated notes")})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Notes != "updated notes" || updated.DownloadURL != created.DownloadURL {
		t.Fatalf("updated = %+v", updated)
	}

	if _, err := svc.Create(ctx, ClientPackageInput{
		Version:     strPtr("1.1.0"),
		Platform:    strPtr("cloudflare_worker"),
		Arch:        strPtr("script"),
		Kind:        strPtr("worker_script"),
		DownloadURL: strPtr("https://example.com/nre-worker.js"),
		SHA256:      strPtr("not-a-sha"),
	}); err == nil {
		t.Fatal("Create() with invalid sha should fail")
	}

	deleted, err := svc.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("deleted = %+v", deleted)
	}
}

func TestClientPackageServiceLatestCompatible(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	svc := NewClientPackageService(store)
	svc.now = func() time.Time { return time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	for _, input := range []ClientPackageInput{
		{Version: strPtr("1.0.0"), Platform: strPtr("windows"), Arch: strPtr("amd64"), Kind: strPtr("flutter_gui"), DownloadURL: strPtr("https://example.com/old.zip"), SHA256: strPtr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		{Version: strPtr("1.2.0"), Platform: strPtr("windows"), Arch: strPtr("amd64"), Kind: strPtr("flutter_gui"), DownloadURL: strPtr("https://example.com/new.zip"), SHA256: strPtr("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")},
		{Version: strPtr("1.3.0"), Platform: strPtr("macos"), Arch: strPtr("arm64"), Kind: strPtr("flutter_gui"), DownloadURL: strPtr("https://example.com/mac.zip"), SHA256: strPtr("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")},
	} {
		if _, err := svc.Create(ctx, input); err != nil {
			t.Fatalf("Create(%+v) error = %v", input, err)
		}
	}

	latest, err := svc.Latest(ctx, ClientPackageQuery{Platform: "windows", Arch: "amd64", Kind: "flutter_gui"})
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.Version != "1.2.0" {
		t.Fatalf("latest.Version = %q", latest.Version)
	}
}

func strPtr(value string) *string {
	return &value
}
```

- [ ] **Step 2: Run the service tests and verify they fail**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/service -run ClientPackage -count=1
```

Expected: FAIL because the service does not exist.

- [ ] **Step 3: Implement the service**

Create `panel/backend-go/internal/controlplane/service/client_packages.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrClientPackageNotFound = errors.New("client package not found")

type ClientPackage struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Kind        string `json:"kind"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes"`
	CreatedAt   string `json:"created_at"`
}

type ClientPackageInput struct {
	ID          *string `json:"id,omitempty"`
	Version     *string `json:"version,omitempty"`
	Platform    *string `json:"platform,omitempty"`
	Arch        *string `json:"arch,omitempty"`
	Kind        *string `json:"kind,omitempty"`
	DownloadURL *string `json:"download_url,omitempty"`
	SHA256      *string `json:"sha256,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

type ClientPackageQuery struct {
	Platform string
	Arch     string
	Kind     string
}

type clientPackageService struct {
	store storage.Store
	now   func() time.Time
}

func NewClientPackageService(store storage.Store) *clientPackageService {
	return &clientPackageService{store: store, now: time.Now}
}

func (s *clientPackageService) List(ctx context.Context) ([]ClientPackage, error) {
	rows, err := s.store.ListClientPackages(ctx)
	if err != nil {
		return nil, err
	}
	packages := make([]ClientPackage, 0, len(rows))
	for _, row := range rows {
		packages = append(packages, clientPackageFromRow(row))
	}
	return packages, nil
}

func (s *clientPackageService) Create(ctx context.Context, input ClientPackageInput) (ClientPackage, error) {
	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}
	pkg, err := normalizeClientPackageInput(input, ClientPackage{}, s.now)
	if err != nil {
		return ClientPackage{}, err
	}
	for _, existing := range current {
		if existing.ID == pkg.ID {
			return ClientPackage{}, fmt.Errorf("%w: client package id already exists: %s", ErrInvalidArgument, pkg.ID)
		}
	}
	current = append(current, pkg)
	return pkg, s.saveAll(ctx, current)
}

func (s *clientPackageService) Update(ctx context.Context, id string, input ClientPackageInput) (ClientPackage, error) {
	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}
	for i, existing := range current {
		if existing.ID != strings.TrimSpace(id) {
			continue
		}
		next, err := normalizeClientPackageInput(input, existing, s.now)
		if err != nil {
			return ClientPackage{}, err
		}
		next.ID = existing.ID
		next.CreatedAt = existing.CreatedAt
		current[i] = next
		return next, s.saveAll(ctx, current)
	}
	return ClientPackage{}, ErrClientPackageNotFound
}

func (s *clientPackageService) Delete(ctx context.Context, id string) (ClientPackage, error) {
	current, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}
	for i, existing := range current {
		if existing.ID != strings.TrimSpace(id) {
			continue
		}
		next := append([]ClientPackage(nil), current[:i]...)
		next = append(next, current[i+1:]...)
		return existing, s.saveAll(ctx, next)
	}
	return ClientPackage{}, ErrClientPackageNotFound
}

func (s *clientPackageService) Latest(ctx context.Context, query ClientPackageQuery) (ClientPackage, error) {
	packages, err := s.List(ctx)
	if err != nil {
		return ClientPackage{}, err
	}
	platform := strings.ToLower(strings.TrimSpace(query.Platform))
	arch := strings.ToLower(strings.TrimSpace(query.Arch))
	kind := strings.ToLower(strings.TrimSpace(query.Kind))
	matches := make([]ClientPackage, 0)
	for _, pkg := range packages {
		if pkg.Platform == platform && pkg.Arch == arch && pkg.Kind == kind {
			matches = append(matches, pkg)
		}
	}
	if len(matches) == 0 {
		return ClientPackage{}, ErrClientPackageNotFound
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return compareVersionStrings(matches[i].Version, matches[j].Version) > 0
	})
	return matches[0], nil
}

func (s *clientPackageService) saveAll(ctx context.Context, packages []ClientPackage) error {
	rows := make([]storage.ClientPackageRow, 0, len(packages))
	for _, pkg := range packages {
		rows = append(rows, clientPackageToRow(pkg))
	}
	return s.store.SaveClientPackages(ctx, rows)
}

var clientPackageSHA256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func normalizeClientPackageInput(input ClientPackageInput, fallback ClientPackage, now func() time.Time) (ClientPackage, error) {
	pkg := fallback
	if input.ID != nil {
		pkg.ID = strings.TrimSpace(*input.ID)
	}
	if input.Version != nil {
		pkg.Version = strings.TrimSpace(*input.Version)
	}
	if input.Platform != nil {
		pkg.Platform = strings.ToLower(strings.TrimSpace(*input.Platform))
	}
	if input.Arch != nil {
		pkg.Arch = strings.ToLower(strings.TrimSpace(*input.Arch))
	}
	if input.Kind != nil {
		pkg.Kind = strings.ToLower(strings.TrimSpace(*input.Kind))
	}
	if input.DownloadURL != nil {
		pkg.DownloadURL = strings.TrimSpace(*input.DownloadURL)
	}
	if input.SHA256 != nil {
		pkg.SHA256 = strings.ToLower(strings.TrimSpace(*input.SHA256))
	}
	if input.Notes != nil {
		pkg.Notes = strings.TrimSpace(*input.Notes)
	}
	if pkg.CreatedAt == "" {
		pkg.CreatedAt = now().UTC().Format(time.RFC3339)
	}
	if pkg.ID == "" {
		pkg.ID = generatedClientPackageID(pkg)
	}
	if err := validateClientPackage(pkg); err != nil {
		return ClientPackage{}, err
	}
	return pkg, nil
}

func validateClientPackage(pkg ClientPackage) error {
	if pkg.Version == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidArgument)
	}
	if !isAllowedClientPackagePlatform(pkg.Platform) {
		return fmt.Errorf("%w: platform must be windows, macos, android, or cloudflare_worker", ErrInvalidArgument)
	}
	if !isAllowedClientPackageArch(pkg.Arch) {
		return fmt.Errorf("%w: arch must be amd64, arm64, universal, or script", ErrInvalidArgument)
	}
	if !isAllowedClientPackageKind(pkg.Kind) {
		return fmt.Errorf("%w: kind must be flutter_gui, go_agent, or worker_script", ErrInvalidArgument)
	}
	parsed, err := url.Parse(pkg.DownloadURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%w: download_url must be an absolute https URL", ErrInvalidArgument)
	}
	if !clientPackageSHA256Pattern.MatchString(pkg.SHA256) {
		return fmt.Errorf("%w: sha256 must be a 64-character hex string", ErrInvalidArgument)
	}
	if pkg.Platform == "cloudflare_worker" && (pkg.Arch != "script" || pkg.Kind != "worker_script") {
		return fmt.Errorf("%w: cloudflare_worker packages must use arch=script and kind=worker_script", ErrInvalidArgument)
	}
	return nil
}

func isAllowedClientPackagePlatform(value string) bool {
	switch value {
	case "windows", "macos", "android", "cloudflare_worker":
		return true
	default:
		return false
	}
}

func isAllowedClientPackageArch(value string) bool {
	switch value {
	case "amd64", "arm64", "universal", "script":
		return true
	default:
		return false
	}
}

func isAllowedClientPackageKind(value string) bool {
	switch value {
	case "flutter_gui", "go_agent", "worker_script":
		return true
	default:
		return false
	}
}

func generatedClientPackageID(pkg ClientPackage) string {
	parts := []string{pkg.Kind, pkg.Platform, pkg.Arch, pkg.Version}
	return strings.NewReplacer(".", "-", "+", "-", "/", "-").Replace(strings.Join(parts, "-"))
}

func clientPackageFromRow(row storage.ClientPackageRow) ClientPackage {
	return ClientPackage{
		ID:          row.ID,
		Version:     row.Version,
		Platform:    row.Platform,
		Arch:        row.Arch,
		Kind:        row.Kind,
		DownloadURL: row.DownloadURL,
		SHA256:      row.SHA256,
		Notes:       row.Notes,
		CreatedAt:   row.CreatedAt,
	}
}

func clientPackageToRow(pkg ClientPackage) storage.ClientPackageRow {
	return storage.ClientPackageRow{
		ID:          pkg.ID,
		Version:     pkg.Version,
		Platform:    pkg.Platform,
		Arch:        pkg.Arch,
		Kind:        pkg.Kind,
		DownloadURL: pkg.DownloadURL,
		SHA256:      pkg.SHA256,
		Notes:       pkg.Notes,
		CreatedAt:   pkg.CreatedAt,
	}
}

func compareVersionStrings(left string, right string) int {
	if left == right {
		return 0
	}
	leftParts := strings.FieldsFunc(left, func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' })
	rightParts := strings.FieldsFunc(right, func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' })
	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}
	for i := 0; i < maxLen; i++ {
		l, r := "", ""
		if i < len(leftParts) {
			l = leftParts[i]
		}
		if i < len(rightParts) {
			r = rightParts[i]
		}
		if l == r {
			continue
		}
		if l > r {
			return 1
		}
		return -1
	}
	return 0
}
```

- [ ] **Step 4: Run service tests**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/service -run ClientPackage -count=1
```

Expected: PASS.

- [ ] **Step 5: Run targeted backend packages**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/storage ./internal/controlplane/service
```

Expected: PASS.

- [ ] **Step 6: Commit service work**

```bash
git add panel/backend-go/internal/controlplane/service/client_packages.go panel/backend-go/internal/controlplane/service/client_packages_test.go
git commit -m "feat(backend): manage client release packages"
```

---

## Task 3: Backend HTTP API

**Files:**

- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_client_packages.go`
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Add router tests for package API**

In `panel/backend-go/internal/controlplane/http/router_test.go`, add this fake near `fakeVersionPolicyService`:

```go
type fakeClientPackageService struct {
	packages       []service.ClientPackage
	createdPackage service.ClientPackage
	updatedPackage service.ClientPackage
	deletedPackage service.ClientPackage
	latestPackage  service.ClientPackage
	err            error
}

func (f fakeClientPackageService) List(context.Context) ([]service.ClientPackage, error) {
	return f.packages, f.err
}

func (f fakeClientPackageService) Create(context.Context, service.ClientPackageInput) (service.ClientPackage, error) {
	return f.createdPackage, f.err
}

func (f fakeClientPackageService) Update(context.Context, string, service.ClientPackageInput) (service.ClientPackage, error) {
	return f.updatedPackage, f.err
}

func (f fakeClientPackageService) Delete(context.Context, string) (service.ClientPackage, error) {
	return f.deletedPackage, f.err
}

func (f fakeClientPackageService) Latest(context.Context, service.ClientPackageQuery) (service.ClientPackage, error) {
	return f.latestPackage, f.err
}
```

Add this test near the version policy router tests:

```go
func TestRouterClientPackageRoutes(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{info: service.SystemInfo{Role: "master", LocalApplyRuntime: "go-agent", DefaultAgentID: "local", LocalAgentEnabled: true}},
		AgentService: fakeAgentService{},
		RuleService:  fakeRuleService{},
		L4RuleService: fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		ClientPackageService: fakeClientPackageService{
			packages: []service.ClientPackage{{
				ID: "flutter_gui-windows-amd64-1-1-0", Version: "1.1.0", Platform: "windows", Arch: "amd64", Kind: "flutter_gui", DownloadURL: "https://example.com/client.zip", SHA256: strings.Repeat("a", 64),
			}},
			createdPackage: service.ClientPackage{ID: "created", Version: "1.1.0", Platform: "android", Arch: "universal", Kind: "flutter_gui", DownloadURL: "https://example.com/app.apk", SHA256: strings.Repeat("b", 64)},
			updatedPackage: service.ClientPackage{ID: "created", Version: "1.1.1", Platform: "android", Arch: "universal", Kind: "flutter_gui", DownloadURL: "https://example.com/app.apk", SHA256: strings.Repeat("c", 64)},
			deletedPackage: service.ClientPackage{ID: "created", Version: "1.1.1", Platform: "android", Arch: "universal", Kind: "flutter_gui", DownloadURL: "https://example.com/app.apk", SHA256: strings.Repeat("c", 64)},
			latestPackage:  service.ClientPackage{ID: "latest", Version: "1.2.0", Platform: "windows", Arch: "amd64", Kind: "flutter_gui", DownloadURL: "https://example.com/latest.zip", SHA256: strings.Repeat("d", 64)},
		},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
		status int
		field  string
	}{
		{http.MethodGet, "/panel-api/client-packages", "", http.StatusOK, "packages"},
		{http.MethodPost, "/panel-api/client-packages", `{"version":"1.1.0","platform":"android","arch":"universal","kind":"flutter_gui","download_url":"https://example.com/app.apk","sha256":"` + strings.Repeat("b", 64) + `"}`, http.StatusCreated, "package"},
		{http.MethodPut, "/panel-api/client-packages/created", `{"version":"1.1.1"}`, http.StatusOK, "package"},
		{http.MethodDelete, "/panel-api/client-packages/created", "", http.StatusOK, "package"},
		{http.MethodGet, "/panel-api/client-packages/latest?platform=windows&arch=amd64&kind=flutter_gui", "", http.StatusOK, "package"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("X-Panel-Token", "secret")
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != tc.status {
			t.Fatalf("%s %s = %d body=%s", tc.method, tc.path, resp.Code, resp.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if _, ok := payload[tc.field]; !ok {
			t.Fatalf("%s %s missing %s: %+v", tc.method, tc.path, tc.field, payload)
		}
	}
}
```

- [ ] **Step 2: Run router test and verify it fails**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/http -run TestRouterClientPackageRoutes -count=1
```

Expected: FAIL because `ClientPackageService` dependency and routes do not exist.

- [ ] **Step 3: Add router dependency and defaults**

In `panel/backend-go/internal/controlplane/http/router.go`, add:

```go
type ClientPackageService interface {
	List(context.Context) ([]service.ClientPackage, error)
	Create(context.Context, service.ClientPackageInput) (service.ClientPackage, error)
	Update(context.Context, string, service.ClientPackageInput) (service.ClientPackage, error)
	Delete(context.Context, string) (service.ClientPackage, error)
	Latest(context.Context, service.ClientPackageQuery) (service.ClientPackage, error)
}
```

Add to `Dependencies`:

```go
ClientPackageService ClientPackageService
```

In `NewRouter`, add routes after `version-policies`:

```go
mux.Handle(prefix+"/client-packages", resolved.requirePanelToken(http.HandlerFunc(resolved.handleClientPackages)))
mux.Handle(prefix+"/client-packages/latest", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLatestClientPackage)))
mux.Handle(prefix+"/client-packages/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleClientPackage)))
```

In `withDefaults`, after `VersionPolicyService` defaulting:

```go
if d.ClientPackageService == nil {
	d.ClientPackageService = service.NewClientPackageService(store)
}
```

Update `hasCoreServices()` to include `d.ClientPackageService != nil`.

In `mapServiceError`, add:

```go
case errors.Is(err, service.ErrClientPackageNotFound):
	return http.StatusNotFound, errorPayload("client package not found")
```

- [ ] **Step 4: Add HTTP handlers**

Create `panel/backend-go/internal/controlplane/http/handlers_client_packages.go`:

```go
package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleClientPackages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		packages, err := d.ClientPackageService.List(r.Context())
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "packages": packages})
	case http.MethodPost:
		var payload service.ClientPackageInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		pkg, err := d.ClientPackageService.Create(r.Context(), payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "package": pkg})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleClientPackage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid client package id"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var payload service.ClientPackageInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		pkg, err := d.ClientPackageService.Update(r.Context(), id, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "package": pkg})
	case http.MethodDelete:
		pkg, err := d.ClientPackageService.Delete(r.Context(), id)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "package": pkg})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleLatestClientPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	query := service.ClientPackageQuery{
		Platform: r.URL.Query().Get("platform"),
		Arch:     r.URL.Query().Get("arch"),
		Kind:     r.URL.Query().Get("kind"),
	}
	pkg, err := d.ClientPackageService.Latest(r.Context(), query)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "package": pkg})
}
```

- [ ] **Step 5: Run router tests**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/http -run 'TestRouterClientPackageRoutes|TestRouterServesPanelAuthAndInfoEndpoints' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run backend suite**

Run:

```bash
cd panel/backend-go && go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit HTTP API**

```bash
git add panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/handlers_client_packages.go panel/backend-go/internal/controlplane/http/router_test.go
git commit -m "feat(backend): expose client package api"
```

---

## Task 4: Frontend API, Hooks, And Worker Deploy Utility

**Files:**

- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devRuntime.js`
- Modify: `panel/frontend/src/api/devMocks/index.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Create: `panel/frontend/src/hooks/useClientPackages.js`
- Create: `panel/frontend/src/utils/workerDeploy.js`
- Create: `panel/frontend/src/utils/workerDeploy.test.mjs`

- [ ] **Step 1: Write Worker utility tests**

Create `panel/frontend/src/utils/workerDeploy.test.mjs`:

```js
import { describe, expect, it } from 'vitest'
import { buildWorkerDeployModel, validateWorkerDeployInput } from './workerDeploy.js'

describe('workerDeploy', () => {
  it('validates required Worker deployment fields', () => {
    expect(validateWorkerDeployInput({
      workerName: '',
      masterUrl: 'not-a-url',
      token: '',
      packageRecord: null
    })).toEqual({
      workerName: '请输入 Worker 名称',
      masterUrl: '请输入有效的 https Master URL',
      token: '请输入 Worker 访问令牌',
      packageRecord: '请选择 Cloudflare Worker 脚本包'
    })
  })

  it('builds GitHub-hosted script deployment output', () => {
    const model = buildWorkerDeployModel({
      workerName: 'nre-edge',
      masterUrl: 'https://panel.example.com',
      token: 'secret',
      packageRecord: {
        version: '1.1.0',
        download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
        sha256: 'a'.repeat(64)
      }
    })
    expect(model.env.NRE_MASTER_URL).toBe('https://panel.example.com')
    expect(model.command).toContain('wrangler deploy')
    expect(model.command).toContain('nre-edge')
    expect(model.scriptUrl).toContain('raw.githubusercontent.com')
    expect(model.sha256).toBe('a'.repeat(64))
  })
})
```

- [ ] **Step 2: Run utility test and verify it fails**

Run:

```bash
cd panel/frontend && npm run test -- src/utils/workerDeploy.test.mjs
```

Expected: FAIL because `workerDeploy.js` does not exist.

- [ ] **Step 3: Implement Worker deploy utility**

Create `panel/frontend/src/utils/workerDeploy.js`:

```js
function isHttpsUrl(value) {
  try {
    const parsed = new URL(String(value || '').trim())
    return parsed.protocol === 'https:' && !!parsed.host
  } catch {
    return false
  }
}

export function validateWorkerDeployInput(input = {}) {
  const errors = {}
  const workerName = String(input.workerName || '').trim()
  const masterUrl = String(input.masterUrl || '').trim()
  const token = String(input.token || '').trim()
  const packageRecord = input.packageRecord
  if (!workerName) errors.workerName = '请输入 Worker 名称'
  if (!isHttpsUrl(masterUrl)) errors.masterUrl = '请输入有效的 https Master URL'
  if (!token) errors.token = '请输入 Worker 访问令牌'
  if (!packageRecord || packageRecord.platform !== 'cloudflare_worker' || packageRecord.kind !== 'worker_script') {
    errors.packageRecord = '请选择 Cloudflare Worker 脚本包'
  }
  return errors
}

export function buildWorkerDeployModel(input = {}) {
  const errors = validateWorkerDeployInput(input)
  if (Object.keys(errors).length) {
    const err = new Error('invalid worker deploy input')
    err.errors = errors
    throw err
  }
  const workerName = String(input.workerName || '').trim()
  const masterUrl = String(input.masterUrl || '').trim().replace(/\/+$/, '')
  const token = String(input.token || '').trim()
  const pkg = input.packageRecord
  return {
    workerName,
    scriptUrl: pkg.download_url,
    sha256: pkg.sha256,
    env: {
      NRE_MASTER_URL: masterUrl,
      NRE_WORKER_TOKEN: token
    },
    command: [
      'wrangler deploy',
      '--name', workerName,
      '--compatibility-date 2026-04-26',
      pkg.download_url
    ].join(' ')
  }
}
```

- [ ] **Step 4: Add frontend API functions**

In `panel/frontend/src/api/runtime.js`, add:

```js
export async function fetchClientPackages() {
  const { data } = await api.get('/client-packages')
  return data.packages || []
}

export async function createClientPackage(payload) {
  const { data } = await api.post('/client-packages', payload, longRunningRequest)
  return data.package
}

export async function updateClientPackage(id, payload) {
  const { data } = await api.put(`/client-packages/${encodeURIComponent(id)}`, payload, longRunningRequest)
  return data.package
}

export async function deleteClientPackage(id) {
  const { data } = await api.delete(`/client-packages/${encodeURIComponent(id)}`, longRunningRequest)
  return data.package
}

export async function fetchLatestClientPackage(params) {
  const search = new URLSearchParams(params)
  const { data } = await api.get(`/client-packages/latest?${search.toString()}`)
  return data.package
}
```

Export the same names from `panel/frontend/src/api/index.js`, `devRuntime.js`, and `devMocks/index.js`.

- [ ] **Step 5: Add dev mock data**

In `panel/frontend/src/api/devMocks/data.js`, add a `mockClientPackages` array near `mockVersionPolicies`:

```js
const mockClientPackages = [
  {
    id: 'flutter_gui-windows-amd64-1-1-0',
    version: '1.1.0',
    platform: 'windows',
    arch: 'amd64',
    kind: 'flutter_gui',
    download_url: 'https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip',
    sha256: 'a'.repeat(64),
    notes: 'Windows Flutter GUI',
    created_at: new Date().toISOString()
  },
  {
    id: 'worker_script-cloudflare_worker-script-1-1-0',
    version: '1.1.0',
    platform: 'cloudflare_worker',
    arch: 'script',
    kind: 'worker_script',
    download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
    sha256: 'b'.repeat(64),
    notes: 'Cloudflare Worker script',
    created_at: new Date().toISOString()
  }
]
```

Add mock functions:

```js
export async function fetchClientPackages() {
  if (isDev) {
    await sleep()
    return [...mockClientPackages]
  }
  const { data } = await api.get('/client-packages')
  return data.packages || []
}

export async function createClientPackage(payload) {
  if (isDev) {
    await sleep()
    const item = { id: `${payload.kind}-${payload.platform}-${payload.arch}-${payload.version}`.replaceAll('.', '-'), created_at: new Date().toISOString(), ...payload }
    mockClientPackages.push(item)
    return item
  }
  const { data } = await api.post('/client-packages', payload, longRunningRequest)
  return data.package
}

export async function updateClientPackage(id, payload) {
  if (isDev) {
    await sleep()
    const index = mockClientPackages.findIndex((item) => item.id === id)
    if (index < 0) return null
    mockClientPackages[index] = { ...mockClientPackages[index], ...payload }
    return mockClientPackages[index]
  }
  const { data } = await api.put(`/client-packages/${encodeURIComponent(id)}`, payload, longRunningRequest)
  return data.package
}

export async function deleteClientPackage(id) {
  if (isDev) {
    await sleep()
    const index = mockClientPackages.findIndex((item) => item.id === id)
    if (index < 0) return null
    return mockClientPackages.splice(index, 1)[0]
  }
  const { data } = await api.delete(`/client-packages/${encodeURIComponent(id)}`, longRunningRequest)
  return data.package
}

export async function fetchLatestClientPackage(params) {
  if (isDev) {
    await sleep()
    return mockClientPackages.find((item) =>
      item.platform === params.platform && item.arch === params.arch && item.kind === params.kind
    ) || null
  }
  const search = new URLSearchParams(params)
  const { data } = await api.get(`/client-packages/latest?${search.toString()}`)
  return data.package
}
```

- [ ] **Step 6: Add Vue Query hooks**

Create `panel/frontend/src/hooks/useClientPackages.js`:

```js
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { messageStore } from '../stores/messages'

export function useClientPackages() {
  return useQuery({
    queryKey: ['clientPackages'],
    queryFn: () => api.fetchClientPackages()
  })
}

export function useCreateClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createClientPackage(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包创建成功')
    },
    onError: (error) => messageStore.error(error, '创建客户端发布包失败')
  })
}

export function useUpdateClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateClientPackage(id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包更新成功')
    },
    onError: (error) => messageStore.error(error, '更新客户端发布包失败')
  })
}

export function useDeleteClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteClientPackage(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包已删除')
    },
    onError: (error) => messageStore.error(error, '删除客户端发布包失败')
  })
}
```

- [ ] **Step 7: Run frontend tests**

Run:

```bash
cd panel/frontend && npm run test -- src/utils/workerDeploy.test.mjs
```

Expected: PASS.

- [ ] **Step 8: Commit API and utility work**

```bash
git add panel/frontend/src/api/index.js panel/frontend/src/api/runtime.js panel/frontend/src/api/devRuntime.js panel/frontend/src/api/devMocks/index.js panel/frontend/src/api/devMocks/data.js panel/frontend/src/hooks/useClientPackages.js panel/frontend/src/utils/workerDeploy.js panel/frontend/src/utils/workerDeploy.test.mjs
git commit -m "feat(panel): add client package api hooks"
```

---

## Task 5: Panel Client Packages Page And Worker Wizard

**Files:**

- Create: `panel/frontend/src/pages/ClientPackagesPage.vue`
- Modify: `panel/frontend/src/router/index.js`
- Modify: `panel/frontend/src/components/layout/Sidebar.vue`

- [ ] **Step 1: Create the client packages page**

Create `panel/frontend/src/pages/ClientPackagesPage.vue` with:

```vue
<template>
  <div class="client-packages-page">
    <header class="client-packages-page__header">
      <div>
        <h1 class="client-packages-page__title">客户端发布</h1>
        <p class="client-packages-page__subtitle">管理 GitHub 分发的 GUI 客户端、Agent 包和 Worker 脚本</p>
      </div>
      <button class="btn btn-primary" @click="openCreate">新增发布包</button>
    </header>

    <section class="worker-panel">
      <div>
        <h2>Cloudflare Worker 部署向导</h2>
        <p>选择 GitHub 托管的 Worker 脚本，生成环境变量和 wrangler 部署命令。</p>
      </div>
      <div class="worker-form">
        <input v-model="workerForm.workerName" class="input" placeholder="Worker 名称">
        <input v-model="workerForm.masterUrl" class="input" placeholder="https://panel.example.com">
        <input v-model="workerForm.token" class="input" placeholder="Worker token">
        <select v-model="workerForm.packageId" class="input">
          <option value="">选择 Worker 脚本包</option>
          <option v-for="pkg in workerPackages" :key="pkg.id" :value="pkg.id">
            {{ pkg.version }} · {{ pkg.download_url }}
          </option>
        </select>
        <button class="btn btn-secondary" @click="buildWorkerDeploy">生成部署命令</button>
      </div>
      <p v-if="workerError" class="form-error">{{ workerError }}</p>
      <pre v-if="workerDeploy" class="deploy-output">{{ workerDeployText }}</pre>
    </section>

    <div v-if="isLoading" class="client-packages-page__empty">加载中...</div>
    <div v-else-if="!packages.length" class="client-packages-page__empty">暂无客户端发布包</div>
    <div v-else class="packages-grid">
      <article v-for="pkg in packages" :key="pkg.id" class="package-card">
        <div class="package-card__header">
          <div>
            <h3>{{ pkg.kind }} · {{ pkg.platform }}</h3>
            <p>{{ pkg.version }} / {{ pkg.arch }}</p>
          </div>
          <div class="package-card__actions">
            <button class="icon-btn" @click="openEdit(pkg)">编辑</button>
            <button class="icon-btn icon-btn--danger" @click="deletePackage.mutate(pkg.id)">删除</button>
          </div>
        </div>
        <a :href="pkg.download_url" target="_blank" rel="noreferrer">{{ pkg.download_url }}</a>
        <p class="package-card__sha">SHA256: {{ pkg.sha256 }}</p>
        <p v-if="pkg.notes" class="package-card__notes">{{ pkg.notes }}</p>
      </article>
    </div>

    <Teleport to="body">
      <div v-if="showForm" class="modal-overlay">
        <div class="modal modal--large">
          <div class="modal__header">
            <span>{{ editingPackage ? '编辑发布包' : '新增发布包' }}</span>
            <button class="modal__close" @click="closeForm">x</button>
          </div>
          <form class="modal__body package-form" @submit.prevent="submitPackage">
            <div class="form-row">
              <input v-model="form.version" class="input" placeholder="版本，例如 1.1.0">
              <select v-model="form.platform" class="input">
                <option value="">平台</option>
                <option value="windows">Windows</option>
                <option value="macos">macOS</option>
                <option value="android">Android</option>
                <option value="cloudflare_worker">Cloudflare Worker</option>
              </select>
              <select v-model="form.arch" class="input">
                <option value="">架构</option>
                <option value="amd64">amd64</option>
                <option value="arm64">arm64</option>
                <option value="universal">universal</option>
                <option value="script">script</option>
              </select>
              <select v-model="form.kind" class="input">
                <option value="">类型</option>
                <option value="flutter_gui">Flutter GUI</option>
                <option value="go_agent">Go Agent</option>
                <option value="worker_script">Worker Script</option>
              </select>
            </div>
            <input v-model="form.download_url" class="input" placeholder="GitHub Release 或 raw.githubusercontent.com URL">
            <input v-model="form.sha256" class="input" placeholder="64 位 SHA256">
            <textarea v-model="form.notes" class="input" rows="3" placeholder="说明"></textarea>
            <p v-if="formError" class="form-error">{{ formError }}</p>
            <div class="modal__footer">
              <button type="button" class="btn btn-secondary" @click="closeForm">取消</button>
              <button type="submit" class="btn btn-primary" :disabled="isMutating">保存</button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import {
  useClientPackages,
  useCreateClientPackage,
  useUpdateClientPackage,
  useDeleteClientPackage
} from '../hooks/useClientPackages'
import { buildWorkerDeployModel } from '../utils/workerDeploy'

const { data, isLoading } = useClientPackages()
const createPackage = useCreateClientPackage()
const updatePackage = useUpdateClientPackage()
const deletePackage = useDeleteClientPackage()

const packages = computed(() => data.value ?? [])
const workerPackages = computed(() => packages.value.filter((pkg) => pkg.platform === 'cloudflare_worker' && pkg.kind === 'worker_script'))
const isMutating = computed(() => createPackage.isPending.value || updatePackage.isPending.value)

const showForm = ref(false)
const editingPackage = ref(null)
const formError = ref('')
const workerError = ref('')
const workerDeploy = ref(null)
const workerForm = ref({ workerName: '', masterUrl: '', token: '', packageId: '' })
const form = ref(defaultForm())

const workerDeployText = computed(() => {
  if (!workerDeploy.value) return ''
  return [
    `Script: ${workerDeploy.value.scriptUrl}`,
    `SHA256: ${workerDeploy.value.sha256}`,
    `NRE_MASTER_URL=${workerDeploy.value.env.NRE_MASTER_URL}`,
    `NRE_WORKER_TOKEN=${workerDeploy.value.env.NRE_WORKER_TOKEN}`,
    '',
    workerDeploy.value.command
  ].join('\n')
})

function defaultForm() {
  return { version: '', platform: '', arch: '', kind: '', download_url: '', sha256: '', notes: '' }
}

function openCreate() {
  editingPackage.value = null
  form.value = defaultForm()
  formError.value = ''
  showForm.value = true
}

function openEdit(pkg) {
  editingPackage.value = pkg
  form.value = { ...pkg }
  formError.value = ''
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingPackage.value = null
}

function validateForm() {
  const required = ['version', 'platform', 'arch', 'kind', 'download_url', 'sha256']
  const missing = required.find((key) => !String(form.value[key] || '').trim())
  if (missing) return '请完整填写版本、平台、架构、类型、URL 和 SHA256'
  if (!/^https:\/\//.test(form.value.download_url)) return '下载地址必须是 https URL'
  if (!/^[a-fA-F0-9]{64}$/.test(form.value.sha256)) return 'SHA256 必须是 64 位十六进制字符串'
  return ''
}

async function submitPackage() {
  formError.value = validateForm()
  if (formError.value) return
  const payload = { ...form.value }
  try {
    if (editingPackage.value?.id) {
      await updatePackage.mutateAsync({ id: editingPackage.value.id, ...payload })
    } else {
      await createPackage.mutateAsync(payload)
    }
    closeForm()
  } catch (err) {
    formError.value = err?.message || '保存发布包失败'
  }
}

function buildWorkerDeploy() {
  workerError.value = ''
  workerDeploy.value = null
  try {
    workerDeploy.value = buildWorkerDeployModel({
      ...workerForm.value,
      packageRecord: workerPackages.value.find((pkg) => pkg.id === workerForm.value.packageId)
    })
  } catch (err) {
    workerError.value = Object.values(err.errors || {}).join('；') || err.message
  }
}
</script>
```

Add this scoped CSS before `</style>` in `ClientPackagesPage.vue`:

```vue
<style scoped>
.client-packages-page {
  max-width: 1200px;
  margin: 0 auto;
}

.client-packages-page__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  align-items: center;
  margin-bottom: var(--space-6);
}

.client-packages-page__title {
  margin: 0;
  font-size: 1.5rem;
}

.client-packages-page__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.worker-panel {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  padding: var(--space-4);
  margin-bottom: var(--space-5);
}

.worker-panel h2 {
  margin: 0;
  font-size: var(--text-lg);
}

.worker-panel p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.worker-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
  margin-top: var(--space-4);
}

.deploy-output {
  margin: var(--space-3) 0 0;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: var(--text-xs);
}

.client-packages-page__empty {
  padding: var(--space-8);
  text-align: center;
  color: var(--color-text-muted);
}

.packages-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-4);
}

.package-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  padding: var(--space-4);
}

.package-card__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.package-card__header h3 {
  margin: 0;
  font-size: var(--text-base);
}

.package-card__header p,
.package-card__sha,
.package-card__notes {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.package-card a {
  display: block;
  color: var(--color-primary);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.package-card__actions {
  display: flex;
  gap: var(--space-1);
}

.package-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.form-error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-xs);
}

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  box-sizing: border-box;
}

.btn {
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
}

.btn-secondary {
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
}

.icon-btn {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  font-size: var(--text-xs);
  cursor: pointer;
}

.icon-btn--danger {
  color: var(--color-danger);
}

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}

.modal {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  width: min(480px, 90vw);
  max-height: calc(100vh - var(--space-8));
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.modal--large {
  width: min(900px, 95vw);
}

.modal__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
}

.modal__body {
  padding: var(--space-6);
  overflow: auto;
}

.modal__footer {
  padding-top: var(--space-3);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.modal__close {
  border: none;
  background: transparent;
  cursor: pointer;
}

@media (max-width: 900px) {
  .client-packages-page__header,
  .package-card__header {
    flex-direction: column;
    align-items: stretch;
  }

  .worker-form,
  .form-row {
    grid-template-columns: 1fr;
  }
}
</style>
```

- [ ] **Step 2: Add router entry**

In `panel/frontend/src/router/index.js`, add under the AppShell children near `versions`:

```js
{
  path: 'client-packages',
  name: 'client-packages',
  component: () => import('../pages/ClientPackagesPage.vue'),
  meta: { title: '客户端发布' }
}
```

- [ ] **Step 3: Add sidebar navigation**

In `panel/frontend/src/components/layout/Sidebar.vue`, add a full nav `RouterLink` before settings:

```vue
<RouterLink to="/client-packages" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <rect x="3" y="4" width="18" height="14" rx="2"/>
    <path d="M8 20h8"/>
    <path d="M12 18v2"/>
  </svg>
  <span>客户端发布</span>
</RouterLink>
```

Add a collapsed nav icon before settings:

```vue
<RouterLink to="/client-packages" class="sidebar__nav-icon" title="客户端发布" active-class="sidebar__nav-icon--active">
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <rect x="3" y="4" width="18" height="14" rx="2"/>
    <path d="M8 20h8"/>
    <path d="M12 18v2"/>
  </svg>
</RouterLink>
```

- [ ] **Step 4: Build the frontend**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit panel page**

```bash
git add panel/frontend/src/pages/ClientPackagesPage.vue panel/frontend/src/router/index.js panel/frontend/src/components/layout/Sidebar.vue
git commit -m "feat(panel): add client release management page"
```

---

## Task 6: Cloudflare Worker Script Asset

**Files:**

- Create: `workers/cloudflare/nre-worker.js`
- Create: `workers/cloudflare/README.md`

- [ ] **Step 1: Create Worker script**

Create `workers/cloudflare/nre-worker.js`:

```js
export default {
  async fetch(request, env) {
    const masterURL = normalizeMasterURL(env.NRE_MASTER_URL)
    const workerToken = String(env.NRE_WORKER_TOKEN || '').trim()
    if (!masterURL || !workerToken) {
      return new Response('NRE Worker is not configured', { status: 500 })
    }

    const url = new URL(request.url)
    if (url.pathname === '/health') {
      return Response.json({ ok: true, runtime: 'cloudflare_worker' })
    }

    const upstream = new URL(url.pathname + url.search, masterURL)
    const headers = new Headers(request.headers)
    headers.set('X-NRE-Worker-Token', workerToken)
    headers.set('X-Forwarded-Host', url.host)
    headers.set('X-Forwarded-Proto', url.protocol.replace(':', ''))

    return fetch(upstream.toString(), {
      method: request.method,
      headers,
      body: request.body,
      redirect: 'manual'
    })
  }
}

function normalizeMasterURL(value) {
  try {
    const parsed = new URL(String(value || '').trim())
    if (parsed.protocol !== 'https:') return ''
    parsed.pathname = parsed.pathname.replace(/\/+$/, '')
    parsed.search = ''
    parsed.hash = ''
    return parsed.toString().replace(/\/+$/, '/')
  } catch {
    return ''
  }
}
```

- [ ] **Step 2: Create Worker README**

Create `workers/cloudflare/README.md`:

```md
# NRE Cloudflare Worker

This Worker script is distributed through GitHub Release or a repository-hosted raw URL. It is not embedded in the control-plane Docker image.

Required variables:

- `NRE_MASTER_URL`: HTTPS URL for the NRE control plane.
- `NRE_WORKER_TOKEN`: shared secret sent to the control plane as `X-NRE-Worker-Token`.

Deploy with the command generated by the panel Client Packages page. The first release does not call the Cloudflare API from the panel.
```

- [ ] **Step 3: Run a syntax check**

Run:

```bash
node --check workers/cloudflare/nre-worker.js
```

Expected: PASS.

- [ ] **Step 4: Commit Worker asset**

```bash
git add workers/cloudflare/nre-worker.js workers/cloudflare/README.md
git commit -m "feat(worker): add cloudflare worker script asset"
```

---

## Task 7: Flutter Client Scaffold

**Files:**

- Create: `clients/flutter/`

- [ ] **Step 1: Scaffold Flutter project**

Run:

```bash
flutter create --platforms=windows,macos,android --project-name nre_client clients/flutter
```

Expected: PASS and creates `clients/flutter/pubspec.yaml`.

- [ ] **Step 2: Add core platform capabilities**

Create `clients/flutter/lib/core/platform_capabilities.dart`:

```dart
enum NrePlatform { windows, macos, android }

class PlatformCapabilities {
  const PlatformCapabilities({
    required this.platform,
    required this.canManageLocalAgent,
    required this.hasTrayOrMenuBar,
    required this.canViewRemoteAgents,
  });

  final NrePlatform platform;
  final bool canManageLocalAgent;
  final bool hasTrayOrMenuBar;
  final bool canViewRemoteAgents;

  static PlatformCapabilities forPlatform(NrePlatform platform) {
    switch (platform) {
      case NrePlatform.windows:
      case NrePlatform.macos:
        return PlatformCapabilities(
          platform: platform,
          canManageLocalAgent: true,
          hasTrayOrMenuBar: true,
          canViewRemoteAgents: true,
        );
      case NrePlatform.android:
        return const PlatformCapabilities(
          platform: NrePlatform.android,
          canManageLocalAgent: false,
          hasTrayOrMenuBar: false,
          canViewRemoteAgents: true,
        );
    }
  }
}
```

- [ ] **Step 3: Add local state model**

Create `clients/flutter/lib/core/client_state.dart`:

```dart
enum ClientRuntimeState {
  unconfigured,
  pendingRegistration,
  registeredOffline,
  agentNotRunning,
  agentRunning,
  updating,
  updateFailed,
}

class ClientProfile {
  const ClientProfile({
    required this.masterUrl,
    required this.displayName,
    this.agentId = '',
    this.token = '',
  });

  final String masterUrl;
  final String displayName;
  final String agentId;
  final String token;

  bool get isRegistered => agentId.isNotEmpty && token.isNotEmpty;
}
```

- [ ] **Step 4: Add service abstractions**

Create `clients/flutter/lib/services/local_agent_controller.dart`:

```dart
abstract class LocalAgentController {
  Future<bool> isInstalled();
  Future<void> start();
  Future<void> stop();
  Future<String> readRecentLogs();
}

class UnsupportedLocalAgentController implements LocalAgentController {
  @override
  Future<bool> isInstalled() async => false;

  @override
  Future<void> start() async {
    throw UnsupportedError('Local agent runtime is not available on this platform');
  }

  @override
  Future<void> stop() async {
    throw UnsupportedError('Local agent runtime is not available on this platform');
  }

  @override
  Future<String> readRecentLogs() async => '';
}
```

Create `clients/flutter/lib/services/master_api.dart`:

```dart
class MasterApiConfig {
  const MasterApiConfig({required this.masterUrl, required this.token});

  final String masterUrl;
  final String token;
}

class RegisterClientRequest {
  const RegisterClientRequest({required this.name, required this.tags});

  final String name;
  final List<String> tags;
}

class RegisterClientResult {
  const RegisterClientResult({required this.agentId, required this.agentToken});

  final String agentId;
  final String agentToken;
}

abstract class MasterApi {
  Future<RegisterClientResult> register(RegisterClientRequest request);
}
```

- [ ] **Step 5: Add tests**

Create `clients/flutter/test/platform_capabilities_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/platform_capabilities.dart';

void main() {
  test('desktop platforms can manage local agent', () {
    expect(PlatformCapabilities.forPlatform(NrePlatform.windows).canManageLocalAgent, isTrue);
    expect(PlatformCapabilities.forPlatform(NrePlatform.macos).canManageLocalAgent, isTrue);
  });

  test('android is light management mode', () {
    final capabilities = PlatformCapabilities.forPlatform(NrePlatform.android);
    expect(capabilities.canManageLocalAgent, isFalse);
    expect(capabilities.canViewRemoteAgents, isTrue);
  });
}
```

Create `clients/flutter/test/client_state_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';

void main() {
  test('profile is registered only when agent id and token exist', () {
    expect(const ClientProfile(masterUrl: 'https://panel.example.com', displayName: 'desktop').isRegistered, isFalse);
    expect(const ClientProfile(masterUrl: 'https://panel.example.com', displayName: 'desktop', agentId: 'agent-1', token: 'secret').isRegistered, isTrue);
  });
}
```

- [ ] **Step 6: Run Flutter tests**

Run:

```bash
cd clients/flutter && flutter test
```

Expected: PASS.

- [ ] **Step 7: Commit Flutter scaffold**

```bash
git add clients/flutter
git commit -m "feat(client): scaffold flutter gui client"
```

---

## Task 8: Flutter First Screens

**Files:**

- Modify: `clients/flutter/lib/main.dart`
- Create: `clients/flutter/lib/app.dart`
- Create: `clients/flutter/lib/screens/register_screen.dart`
- Create: `clients/flutter/lib/screens/overview_screen.dart`
- Create: `clients/flutter/lib/screens/runtime_screen.dart`
- Create: `clients/flutter/lib/screens/logs_screen.dart`
- Create: `clients/flutter/lib/screens/updates_screen.dart`
- Create: `clients/flutter/lib/screens/settings_screen.dart`
- Create: `clients/flutter/lib/screens/about_screen.dart`
- Create: `clients/flutter/test/register_screen_test.dart`

- [ ] **Step 1: Add registration widget test**

Create `clients/flutter/test/register_screen_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/screens/register_screen.dart';

void main() {
  testWidgets('register screen validates required fields', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: RegisterScreen()));
    await tester.tap(find.text('Register'));
    await tester.pump();
    expect(find.text('Master URL is required'), findsOneWidget);
    expect(find.text('Register token is required'), findsOneWidget);
  });
}
```

- [ ] **Step 2: Add Register screen**

Create `clients/flutter/lib/screens/register_screen.dart`:

```dart
import 'package:flutter/material.dart';

class RegisterScreen extends StatefulWidget {
  const RegisterScreen({super.key});

  @override
  State<RegisterScreen> createState() => _RegisterScreenState();
}

class _RegisterScreenState extends State<RegisterScreen> {
  final _formKey = GlobalKey<FormState>();
  final _masterUrl = TextEditingController();
  final _token = TextEditingController();
  final _name = TextEditingController();

  @override
  void dispose() {
    _masterUrl.dispose();
    _token.dispose();
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Register')),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            TextFormField(
              controller: _masterUrl,
              decoration: const InputDecoration(labelText: 'Master URL'),
              validator: (value) => (value == null || value.trim().isEmpty) ? 'Master URL is required' : null,
            ),
            TextFormField(
              controller: _token,
              decoration: const InputDecoration(labelText: 'Register token'),
              obscureText: true,
              validator: (value) => (value == null || value.trim().isEmpty) ? 'Register token is required' : null,
            ),
            TextFormField(
              controller: _name,
              decoration: const InputDecoration(labelText: 'Client name'),
            ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: () => _formKey.currentState?.validate(),
              child: const Text('Register'),
            ),
          ],
        ),
      ),
    );
  }
}
```

- [ ] **Step 3: Add shell and simple screens**

Create `clients/flutter/lib/app.dart`:

```dart
import 'package:flutter/material.dart';
import 'screens/about_screen.dart';
import 'screens/logs_screen.dart';
import 'screens/overview_screen.dart';
import 'screens/register_screen.dart';
import 'screens/runtime_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/updates_screen.dart';

class NreClientApp extends StatelessWidget {
  const NreClientApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'NRE Client',
      theme: ThemeData(useMaterial3: true, colorSchemeSeed: Colors.teal),
      home: const NreClientHome(),
    );
  }
}

class NreClientHome extends StatefulWidget {
  const NreClientHome({super.key});

  @override
  State<NreClientHome> createState() => _NreClientHomeState();
}

class _NreClientHomeState extends State<NreClientHome> {
  int index = 0;

  static const screens = [
    OverviewScreen(),
    RegisterScreen(),
    RuntimeScreen(),
    LogsScreen(),
    UpdatesScreen(),
    SettingsScreen(),
    AboutScreen(),
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: screens[index],
      bottomNavigationBar: NavigationBar(
        selectedIndex: index,
        onDestinationSelected: (value) => setState(() => index = value),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.dashboard), label: 'Overview'),
          NavigationDestination(icon: Icon(Icons.login), label: 'Register'),
          NavigationDestination(icon: Icon(Icons.memory), label: 'Runtime'),
          NavigationDestination(icon: Icon(Icons.article), label: 'Logs'),
          NavigationDestination(icon: Icon(Icons.system_update), label: 'Updates'),
          NavigationDestination(icon: Icon(Icons.settings), label: 'Settings'),
          NavigationDestination(icon: Icon(Icons.info), label: 'About'),
        ],
      ),
    );
  }
}
```

Replace `clients/flutter/lib/main.dart` with:

```dart
import 'package:flutter/material.dart';
import 'app.dart';

void main() {
  runApp(const NreClientApp());
}
```

Create each simple screen with a `Scaffold` and domain-specific fields. Example `clients/flutter/lib/screens/overview_screen.dart`:

```dart
import 'package:flutter/material.dart';

class OverviewScreen extends StatelessWidget {
  const OverviewScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Overview')),
      body: const ListView(
        padding: EdgeInsets.all(16),
        children: [
          ListTile(title: Text('Master'), subtitle: Text('Not configured')),
          ListTile(title: Text('Runtime'), subtitle: Text('Agent not running')),
          ListTile(title: Text('Last sync'), subtitle: Text('-')),
        ],
      ),
    );
  }
}
```

Create `clients/flutter/lib/screens/runtime_screen.dart`:

```dart
import 'package:flutter/material.dart';

class RuntimeScreen extends StatelessWidget {
  const RuntimeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Runtime')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const ListTile(title: Text('Local agent'), subtitle: Text('Not installed')),
          const ListTile(title: Text('Startup'), subtitle: Text('Manual')),
          FilledButton(onPressed: () {}, child: const Text('Start Agent')),
          const SizedBox(height: 8),
          OutlinedButton(onPressed: () {}, child: const Text('Stop Agent')),
        ],
      ),
    );
  }
}
```

Create `clients/flutter/lib/screens/logs_screen.dart`:

```dart
import 'package:flutter/material.dart';

class LogsScreen extends StatelessWidget {
  const LogsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Logs')),
      body: const Padding(
        padding: EdgeInsets.all(16),
        child: SelectableText('No logs available.'),
      ),
    );
  }
}
```

Create `clients/flutter/lib/screens/updates_screen.dart`:

```dart
import 'package:flutter/material.dart';

class UpdatesScreen extends StatelessWidget {
  const UpdatesScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Updates')),
      body: const ListView(
        padding: EdgeInsets.all(16),
        children: [
          ListTile(title: Text('GUI client'), subtitle: Text('Current version unknown')),
          ListTile(title: Text('Managed agent'), subtitle: Text('No package selected')),
          ListTile(title: Text('Checksum'), subtitle: Text('-')),
        ],
      ),
    );
  }
}
```

Create `clients/flutter/lib/screens/settings_screen.dart`:

```dart
import 'package:flutter/material.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: const ListView(
        padding: EdgeInsets.all(16),
        children: [
          ListTile(title: Text('Master URL'), subtitle: Text('Not configured')),
          ListTile(title: Text('Data directory'), subtitle: Text('Default')),
          SwitchListTile(value: false, onChanged: null, title: Text('Start at login')),
        ],
      ),
    );
  }
}
```

Create `clients/flutter/lib/screens/about_screen.dart`:

```dart
import 'package:flutter/material.dart';

class AboutScreen extends StatelessWidget {
  const AboutScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('About')),
      body: const ListView(
        padding: EdgeInsets.all(16),
        children: [
          ListTile(title: Text('NRE Client'), subtitle: Text('Flutter GUI')),
          ListTile(title: Text('Distribution'), subtitle: Text('GitHub Release')),
          ListTile(title: Text('Container policy'), subtitle: Text('Client artifacts are not embedded in the control-plane image')),
        ],
      ),
    );
  }
}
```

- [ ] **Step 4: Run Flutter tests**

Run:

```bash
cd clients/flutter && flutter test
```

Expected: PASS.

- [ ] **Step 5: Commit Flutter screens**

```bash
git add clients/flutter
git commit -m "feat(client): add initial flutter gui screens"
```

---

## Task 9: GitHub Release Workflow

**Files:**

- Create: `.github/workflows/client-release.yml`
- Modify: `.github/workflows/docker-build.yml` only if needed to keep Docker build independent from client artifacts.

- [ ] **Step 1: Add client release workflow**

Create `.github/workflows/client-release.yml`:

```yaml
name: Client Release

on:
  push:
    tags:
      - 'v*'
  workflow_dispatch:

permissions:
  contents: write

jobs:
  flutter-android:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: subosito/flutter-action@v2
        with:
          channel: stable
      - run: flutter pub get
        working-directory: clients/flutter
      - run: flutter build apk --release
        working-directory: clients/flutter
      - run: sha256sum build/app/outputs/flutter-apk/app-release.apk > nre-client-android.apk.sha256
        working-directory: clients/flutter
      - uses: actions/upload-artifact@v4
        with:
          name: nre-client-android
          path: |
            clients/flutter/build/app/outputs/flutter-apk/app-release.apk
            clients/flutter/nre-client-android.apk.sha256

  flutter-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: subosito/flutter-action@v2
        with:
          channel: stable
      - run: flutter pub get
        working-directory: clients/flutter
      - run: flutter build windows --release
        working-directory: clients/flutter
      - shell: pwsh
        run: Compress-Archive -Path clients/flutter/build/windows/x64/runner/Release/* -DestinationPath nre-client-windows-amd64.zip
      - shell: pwsh
        run: Get-FileHash nre-client-windows-amd64.zip -Algorithm SHA256 | ForEach-Object { $_.Hash.ToLower() + "  nre-client-windows-amd64.zip" } | Set-Content nre-client-windows-amd64.zip.sha256
      - uses: actions/upload-artifact@v4
        with:
          name: nre-client-windows-amd64
          path: |
            nre-client-windows-amd64.zip
            nre-client-windows-amd64.zip.sha256

  flutter-macos:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: subosito/flutter-action@v2
        with:
          channel: stable
      - run: flutter pub get
        working-directory: clients/flutter
      - run: flutter build macos --release
        working-directory: clients/flutter
      - run: ditto -c -k --sequesterRsrc --keepParent clients/flutter/build/macos/Build/Products/Release/nre_client.app nre-client-macos-universal.zip
      - run: shasum -a 256 nre-client-macos-universal.zip > nre-client-macos-universal.zip.sha256
      - uses: actions/upload-artifact@v4
        with:
          name: nre-client-macos-universal
          path: |
            nre-client-macos-universal.zip
            nre-client-macos-universal.zip.sha256

  go-agent-desktop:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.x'
      - run: |
          mkdir -p dist
          GOOS=windows GOARCH=amd64 go build -o dist/nre-agent-windows-amd64.exe ./cmd/nre-agent
          GOOS=darwin GOARCH=amd64 go build -o dist/nre-agent-macos-amd64 ./cmd/nre-agent
          GOOS=darwin GOARCH=arm64 go build -o dist/nre-agent-macos-arm64 ./cmd/nre-agent
          cd dist
          sha256sum * > SHA256SUMS
        working-directory: go-agent
      - uses: actions/upload-artifact@v4
        with:
          name: nre-agent-desktop
          path: go-agent/dist/*

  worker-script:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: |
          mkdir -p dist
          cp workers/cloudflare/nre-worker.js dist/nre-worker.js
          cd dist
          sha256sum nre-worker.js > nre-worker.js.sha256
      - uses: actions/upload-artifact@v4
        with:
          name: nre-worker
          path: dist/*
```

- [ ] **Step 2: Verify Docker workflow stays client-free**

Run:

```bash
Select-String -Path .github/workflows/docker-build.yml,Dockerfile -Pattern 'clients/flutter|nre-client|workers/cloudflare'
```

Expected: no matches. If matches exist, remove the client build from Docker paths and keep it in `client-release.yml`.

- [ ] **Step 3: Commit workflow**

```bash
git add .github/workflows/client-release.yml
git commit -m "ci(clients): build client artifacts on github releases"
```

---

## Task 10: Documentation And Final Verification

**Files:**

- Modify: `README.md`

- [ ] **Step 1: Update README distribution notes**

In `README.md`, update the Windows client paragraph and add Android/macOS/Worker notes under Agent management:

```md
### GUI 客户端

Windows、macOS 和 Android 客户端通过 Flutter 构建，并通过 GitHub Release 分发。控制面容器不构建、不内置、不公开这些客户端安装包。

- Windows/macOS：GUI 客户端负责注册、配置、状态、日志、更新，并管理本机 `nre-agent`。
- Android：第一版是轻管理客户端，只连接控制面查看节点、版本和诊断，不在 Android 本机运行代理。
- Cloudflare Worker：不使用 Flutter 客户端。控制面提供部署向导，Worker 脚本通过 GitHub Release 或仓库 raw URL 分发。

发布包记录只保存 GitHub URL、版本、平台、架构、类型和 SHA256。实际二进制、APK、桌面安装包和 Worker 脚本不进入 Docker 镜像。
```

- [ ] **Step 2: Run full backend tests**

Run:

```bash
cd panel/backend-go && go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run Go agent tests**

Run:

```bash
cd go-agent && go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run frontend tests and build**

Run:

```bash
cd panel/frontend && npm run test
cd panel/frontend && npm run build
```

Expected: PASS.

- [ ] **Step 5: Run Flutter tests**

Run:

```bash
cd clients/flutter && flutter test
```

Expected: PASS.

- [ ] **Step 6: Verify Docker build does not require Flutter**

Run:

```bash
docker build -t nginx-reverse-emby .
```

Expected: PASS without installing Flutter or copying `clients/flutter` artifacts into the image.

- [ ] **Step 7: Commit docs**

```bash
git add README.md
git commit -m "docs(clients): document github distributed gui clients"
```

---

## Self-Review

Spec coverage:

- Windows/macOS Flutter GUI: Tasks 7 and 8 scaffold shared GUI and desktop runtime abstractions.
- Android light-management mode: Task 7 platform capabilities explicitly disable local runtime management for Android.
- Cloudflare Worker without Flutter: Tasks 5 and 6 add panel wizard output and Worker script asset.
- GitHub distribution, no container artifact embedding: Tasks 9 and 10 add GitHub workflow and Docker independence verification.
- Backend package metadata: Tasks 1 through 3 add storage, service, and HTTP API.
- Panel management: Tasks 4 and 5 add API hooks and UI.

Placeholder scan:

- The plan contains no `TBD`, `TODO`, or undefined future work.
- Each task has concrete files, commands, expected results, and commit points.

Type consistency:

- Backend JSON uses `download_url`, matching `ClientPackage.DownloadURL`.
- Package enum values are consistent across service, frontend, Worker wizard, and spec: `windows`, `macos`, `android`, `cloudflare_worker`; `amd64`, `arm64`, `universal`, `script`; `flutter_gui`, `go_agent`, `worker_script`.
