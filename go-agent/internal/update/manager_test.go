package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestStageUpdateVerifiesHash(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "nre-agent")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mgr := NewManager(dir, filepath.Join(dir, "current-agent"), nil, nil, nil, nil)
	_, err := mgr.Stage(context.Background(), model.VersionPackage{
		URL:    fileURL(sourcePath),
		SHA256: "deadbeef",
	})
	if err == nil {
		t.Fatal("expected hash verification failure")
	}
}

func TestStageWritesExecutableFile(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "nre-agent")
	payload := []byte("payload")
	if err := os.WriteFile(sourcePath, payload, 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mgr := NewManager(dir, filepath.Join(dir, "current-agent"), nil, nil, nil, nil)
	stagedPath, err := mgr.Stage(context.Background(), model.VersionPackage{
		URL:      fileURL(sourcePath),
		SHA256:   sumSHA256(payload),
		Filename: "nre-agent-staged",
	})
	if err != nil {
		t.Fatalf("Stage returned error: %v", err)
	}

	got, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("unexpected staged payload: %q", string(got))
	}
	if filepath.Base(stagedPath) != "nre-agent-staged" {
		t.Fatalf("unexpected staged filename: %s", filepath.Base(stagedPath))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(stagedPath)
		if err != nil {
			t.Fatalf("Stat returned error: %v", err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("expected executable mode, got %v", info.Mode())
		}
	}
}

func TestActivatePromotesAndExecsReplacement(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "nre-agent")
	if err := os.WriteFile(targetPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	stagedPath := filepath.Join(dir, "updates", "nre-agent")
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var gotBinary string
	var gotArgv []string
	var gotEnv []string
	mgr := NewManager(
		dir,
		targetPath,
		[]string{"old-binary", "--flag"},
		[]string{"PATH=/bin"},
		func(binary string, argv []string, env []string) error {
			gotBinary = binary
			gotArgv = append([]string(nil), argv...)
			gotEnv = append([]string(nil), env...)
			return ErrRestartRequested
		},
		nil,
	)

	err := mgr.Activate(stagedPath, "2.0.0")
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("expected ErrRestartRequested, got %v", err)
	}
	if gotBinary != targetPath {
		t.Fatalf("unexpected binary path: %s", gotBinary)
	}
	if !reflect.DeepEqual(gotArgv, []string{targetPath, "--flag"}) {
		t.Fatalf("unexpected argv: %+v", gotArgv)
	}
	if !containsEnv(gotEnv, "NRE_AGENT_VERSION=2.0.0") {
		t.Fatalf("expected updated version env, got %+v", gotEnv)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("unexpected promoted payload: %q", string(got))
	}
}

func TestActivatePreservesExistingVersionWhenDesiredVersionEmpty(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "nre-agent")
	if err := os.WriteFile(targetPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	stagedPath := filepath.Join(dir, "updates", "nre-agent")
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var gotEnv []string
	mgr := NewManager(
		dir,
		targetPath,
		[]string{"old-binary"},
		[]string{"PATH=/bin", "NRE_AGENT_VERSION=1.0.0"},
		func(_ string, _ []string, env []string) error {
			gotEnv = append([]string(nil), env...)
			return ErrRestartRequested
		},
		nil,
	)

	err := mgr.Activate(stagedPath, "")
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("expected ErrRestartRequested, got %v", err)
	}
	if !containsEnv(gotEnv, "NRE_AGENT_VERSION=1.0.0") {
		t.Fatalf("expected preserved version env, got %+v", gotEnv)
	}
	if containsEnv(gotEnv, "NRE_AGENT_VERSION=") {
		t.Fatalf("expected empty version env to be absent, got %+v", gotEnv)
	}
}

func sumSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileURL(path string) string {
	return "file:///" + filepath.ToSlash(path)
}

func containsEnv(env []string, needle string) bool {
	for _, entry := range env {
		if entry == needle {
			return true
		}
	}
	return false
}
