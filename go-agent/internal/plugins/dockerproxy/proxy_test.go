package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type recordingRunner struct {
	dir    string
	args   []string
	output []byte
	err    error
}

func (runner *recordingRunner) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	runner.dir = dir
	runner.args = append([]string(nil), args...)
	return runner.output, runner.err
}

func TestEligibleRequiresScopedRevocableHandleGrant(t *testing.T) {
	if !Eligible([]model.PluginGrantProjection{{Name: "event.emit"}, {Name: RequiredGrant, ResourceKind: RequiredResourceKind, ResourceID: RequiredResourceID}}) {
		t.Fatal("scoped Docker Compose grant was rejected")
	}
	if Eligible([]model.PluginGrantProjection{{Name: RequiredGrant}}) || Eligible([]model.PluginGrantProjection{{Name: RequiredGrant, ResourceKind: RequiredResourceKind, ResourceID: "other"}}) {
		t.Fatal("Docker command proxy accepted an unscoped or unrelated grant")
	}
}

func TestWorkspaceDirectoryBindingIsWritableWorkspaceRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instance")
	binding := WorkspaceDirectoryBinding(root)
	if binding.HostPath != filepath.Clean(root) || binding.GuestPath != filepath.Clean(root) || binding.ReadOnly {
		t.Fatalf("workspace binding = %+v", binding)
	}
}

func TestGuestEnvironmentIncludesDockerAppWorkDir(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	env := guestEnvironment(root)
	want := []string{
		CLIEnv + "=" + guestCLIPath,
		EndpointEnv + "=unix:" + guestEndpointPath,
		WorkDirEnv + "=" + root,
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("guest environment = %q want %q", env, want)
	}
}

func TestValidateArgsAllowsOnlyManagedComposeCommandShapes(t *testing.T) {
	allowed := [][]string{
		{"version", "--format", "{{.Server.Version}}"},
		{"compose", "up", "-d"},
		{"compose", "logs", "--no-color", "web"},
		{"workspace", "remove"},
		{"image", "inspect", "--format", "{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}", "nginx:latest"},
		{"buildx", "imagetools", "inspect", "--format", "{{.Manifest.Digest}}", "nginx:latest"},
		{"manifest", "inspect", "--verbose", "nginx:latest"},
	}
	for _, args := range allowed {
		if err := validateArgs(args); err != nil {
			t.Fatalf("validateArgs(%q) = %v", args, err)
		}
	}
	denied := [][]string{
		{"run", "--privileged", "alpine"},
		{"compose", "-f", "/etc/passwd", "up"},
		{"image", "rm", "nginx"},
		{"version", "--format", "{{json .}}"},
		{"manifest", "inspect", "--verbose", "-bad"},
	}
	for _, args := range denied {
		if err := validateArgs(args); err == nil {
			t.Fatalf("validateArgs(%q) succeeded", args)
		}
	}
}

func TestHandlerRemovesOnlyManagedWorkspace(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "media")
	other := filepath.Join(root, "other")
	for _, path := range []string{managed, other} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler := &handler{workspaceRoot: root, runner: &recordingRunner{}}
	request := Request{Args: []string{"workspace", "remove"}, AppID: "media"}
	if err := handler.removeWorkspace(request); err != nil {
		t.Fatalf("remove workspace: %v", err)
	}
	if _, err := os.Stat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed workspace still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("sibling workspace was removed: %v", err)
	}
	if err := handler.removeWorkspace(request); err != nil {
		t.Fatalf("idempotent remove workspace: %v", err)
	}
	if err := handler.removeWorkspace(Request{Args: request.Args, AppID: "other", Compose: "services: {}"}); err == nil {
		t.Fatal("workspace remove accepted compose content")
	}
}

func TestHandlerRejectsSymlinkWorkspaceRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "media")); err != nil {
		t.Fatal(err)
	}
	handler := &handler{workspaceRoot: root, runner: &recordingRunner{}}
	if err := handler.removeWorkspace(Request{Args: []string{"workspace", "remove"}, AppID: "media"}); err == nil {
		t.Fatal("workspace remove followed a symlink")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory changed: %v", err)
	}
}

func TestHandlerServesWorkspaceRemovalWithoutDocker(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "media")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	handler := &handler{cookie: "secret", workspaceRoot: root, runner: runner}
	payload, err := json.Marshal(Request{Args: []string{"workspace", "remove"}, AppID: "media"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, proxyPath, bytes.NewReader(payload))
	request.Header.Set(credentialHeader, "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("workspace remove status=%d body=%s", response.Code, response.Body.String())
	}
	if runner.args != nil {
		t.Fatalf("workspace remove invoked Docker: %q", runner.args)
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace still exists: %v", err)
	}
}

func TestCleanupWorkspaceTombstonesKeepsApplications(t *testing.T) {
	root := t.TempDir()
	tombstone := filepath.Join(root, workspaceTombstonePrefix+"abandoned")
	application := filepath.Join(root, "media")
	for _, path := range []string{tombstone, application} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupWorkspaceTombstones(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstone still exists: %v", err)
	}
	if _, err := os.Stat(application); err != nil {
		t.Fatalf("application workspace was removed: %v", err)
	}
}

func TestHandlerPreparesManagedComposeWorkspace(t *testing.T) {
	root := t.TempDir()
	runner := &recordingRunner{output: []byte("ok")}
	handler := &handler{workspaceRoot: root, runner: runner}
	dir, err := handler.prepare(Request{Args: []string{"compose", "up", "-d"}, AppID: "media", Compose: "services:\n  web:\n    image: nginx\n", Env: "DATABASE_PASSWORD=test-only-secret\n"})
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "media") {
		t.Fatalf("workspace = %q", dir)
	}
	payload, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil || !strings.Contains(string(payload), "image: nginx") {
		t.Fatalf("compose payload = %q error=%v", payload, err)
	}
	environment, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil || string(environment) != "DATABASE_PASSWORD=test-only-secret\n" {
		t.Fatalf("environment=%q error=%v", environment, err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".env")); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("environment mode=%v error=%v", info, err)
	}
	if _, err := handler.prepare(Request{Args: []string{"compose", "start"}, AppID: "../escape"}); err == nil {
		t.Fatal("workspace escape was accepted")
	}
}

func TestHandlerRunsValidatedCommandAndBoundsErrors(t *testing.T) {
	runner := &recordingRunner{output: []byte("29.7.2\n")}
	handler := &handler{workspaceRoot: t.TempDir(), runner: runner}
	dir, err := handler.prepare(Request{Args: []string{"version", "--format", "{{.Server.Version}}"}})
	if err != nil || dir != "" {
		t.Fatalf("prepare version = (%q, %v)", dir, err)
	}
	output, err := runner.Run(t.Context(), dir, "version", "--format", "{{.Server.Version}}")
	if err != nil || string(output) != "29.7.2\n" || !reflect.DeepEqual(runner.args, []string{"version", "--format", "{{.Server.Version}}"}) {
		t.Fatalf("runner = output %q args %q error %v", output, runner.args, err)
	}
	long := errors.New(strings.Repeat("x", 1024))
	if len(boundedError(long)) != 512 {
		t.Fatal("boundedError did not cap host error text")
	}
}
