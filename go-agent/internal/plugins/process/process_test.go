package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessArtifactInstallerRequiresDigestAndNonExecutableCache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache.bin")
	payload := []byte("verified plugin process")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	path, err := (Installer{RuntimeRoot: filepath.Join(root, "runtime")}).Install("instance-1", Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("runtime artifact mode = %v", info.Mode())
	}
	if _, err := (Installer{RuntimeRoot: filepath.Join(root, "runtime")}).Install("instance-2", Artifact{CachePath: cache, SHA256: strings.Repeat("0", 64), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}); err == nil {
		t.Fatal("digest mismatch was accepted")
	}
}

func TestProcessArtifactInstallerRejectsTraversalAndRuntimeAlias(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	payload := []byte("artifact")
	if err := os.WriteFile(cache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	artifact := Artifact{CachePath: cache, SHA256: hex.EncodeToString(digest[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	installer := Installer{RuntimeRoot: filepath.Join(root, "runtime")}
	for _, id := range []string{"../escape", `..\\escape`, filepath.Join(root, "absolute"), "a/b", `a\\b`} {
		if _, err := installer.Install(id, artifact); err == nil {
			t.Fatalf("unsafe instance id %q accepted", id)
		}
	}
	managedCache := filepath.Join(root, "runtime", "cached")
	if err := os.MkdirAll(filepath.Dir(managedCache), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedCache, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact.CachePath = managedCache
	if _, err := installer.Install("alias", artifact); err == nil {
		t.Fatal("runtime-root cache alias accepted")
	}
}

type fakeSandbox struct {
	available   bool
	validateErr error
}

func (s fakeSandbox) Available() bool         { return s.available }
func (fakeSandbox) Provider() string          { return "fake" }
func (s fakeSandbox) Validate(Security) error { return s.validateErr }
func (fakeSandbox) Configure(*exec.Cmd, Security) (func() error, func() error, func(int) error, error) {
	return func() error { return nil }, func() error { return nil }, func(int) error { return nil }, nil
}

func TestSandboxUnsupportedBudgetFailsClosed(t *testing.T) {
	sandbox := fakeSandbox{available: true, validateErr: errors.New("files unsupported")}
	security := Security{Requirement: testSandboxRequirement(Budget{Files: 10}, false, true)}
	if _, err := DecideSandbox(sandbox, security); err == nil {
		t.Fatal("unsupported budget accepted")
	}
	security.Grants = []string{UnsandboxedGrant}
	decision, err := DecideSandbox(sandbox, security)
	if err != nil || decision.Sandboxed {
		t.Fatalf("explicit unsandboxed decision = %+v, %v", decision, err)
	}
}
func (fakeSandbox) Attach(int, Security) (func() error, error) {
	return func() error { return nil }, nil
}

func TestSandboxRejectsHighRiskWithoutExplicitGrant(t *testing.T) {
	security := Security{Requirement: testSandboxRequirement(Budget{Processes: 1}, true, true)}
	if _, err := DecideSandbox(fakeSandbox{}, security); err == nil {
		t.Fatal("high-risk unsandboxed process was accepted")
	}
	security.Grants = []string{UnsandboxedGrant}
	decision, err := DecideSandbox(fakeSandbox{}, security)
	if err != nil || decision.Sandboxed {
		t.Fatalf("explicit decision = %+v, %v", decision, err)
	}
}

func TestProcessLogsAreRedacted(t *testing.T) {
	var output bytes.Buffer
	w := newRedactingWriter(&output, []string{"super-secret"})
	if _, err := io.WriteString(w, "hello super-secret\nauthorization: bearer nope\n"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "super-secret") || strings.Contains(output.String(), "bearer nope") {
		t.Fatalf("sensitive log leaked: %q", output.String())
	}
}

func TestProcessLogsRedactAcrossWriteBoundariesAndDropOversizedLines(t *testing.T) {
	var output bytes.Buffer
	w := newRedactingWriter(&output, []string{"split-secret"})
	for _, chunk := range []string{"value=split-", "secret\nauthor", "ization: bearer nope\n", strings.Repeat("x", maxPluginLogLine+1), "\npartial token=bad"} {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, leaked := range []string{"split-secret", "bearer nope", "token=bad", strings.Repeat("x", 128)} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sensitive or oversized log leaked: %q", leaked)
		}
	}
	if !strings.Contains(got, "oversized") {
		t.Fatalf("oversized line was not fail-closed: %q", got)
	}
}

func TestProcessEnvironmentRejectsPlatformMinimumOverrides(t *testing.T) {
	reserved := []string{"PATH", "LANG", "HOME", "TMPDIR"}
	if runtime.GOOS == "windows" {
		reserved = []string{"SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "PATH"}
	}
	for _, key := range reserved {
		t.Run(key, func(t *testing.T) {
			if _, err := buildProcessEnvironment([]string{key + "=candidate"}, nil); err == nil {
				t.Fatalf("platform minimum environment override %q was accepted", key)
			}
		})
	}
}

func TestProcessEnvironmentDoesNotInheritHostSecrets(t *testing.T) {
	t.Setenv("NRE_PARENT_SECRET", "must-not-leak")
	environment, err := buildProcessEnvironment([]string{"PLUGIN_MODE=test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "NRE_PARENT_SECRET") || strings.Contains(joined, "must-not-leak") {
		t.Fatalf("host secret inherited by plugin process: %q", joined)
	}
	if !strings.Contains(joined, "PLUGIN_MODE=test") {
		t.Fatalf("explicit non-sensitive environment missing: %q", joined)
	}
}

func TestProcessLocationRejectsDirectoryMismatch(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "runtime", "plugin")
	if err := validateProcessLocation(InstanceSpec{Executable: executable, Directory: filepath.Join(t.TempDir(), "other")}); err == nil {
		t.Fatal("plugin process directory outside its instance runtime directory was accepted")
	}
}

type immediateProcess struct{ done chan error }

func (p *immediateProcess) PID() int               { return 42 }
func (p *immediateProcess) Wait() error            { return <-p.done }
func (p *immediateProcess) Signal(os.Signal) error { return nil }
func (p *immediateProcess) Kill() error            { return nil }

type crashRunner struct{}

func (crashRunner) Start(context.Context, InstanceSpec, Sandbox, io.Writer) (ManagedProcess, func() error, error) {
	p := &immediateProcess{done: make(chan error, 1)}
	p.done <- io.EOF
	return p, func() error { return nil }, nil
}

func TestProcessCrashLoopOpensCircuit(t *testing.T) {
	s := NewSupervisor(crashRunner{}, fakeSandbox{available: true}, io.Discard)
	h, err := s.Start(t.Context(), InstanceSpec{ID: "crash", Executable: "unused", RestartLimit: 1, RestartWindow: time.Second, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.Status().CircuitOpen {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("circuit did not open: %+v", h.Status())
}
