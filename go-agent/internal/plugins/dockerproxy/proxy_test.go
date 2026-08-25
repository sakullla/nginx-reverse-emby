package dockerproxy

import (
	"context"
	"errors"
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
