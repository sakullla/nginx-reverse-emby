package hotrestart

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestSupervisorReadinessActivationAndAuthorityOrdering(t *testing.T) {
	requireProcessHandoff(t)
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(t.Context(), helperLaunch("ready"))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatalf("idempotent Activate() error = %v", err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatalf("idempotent TransferAuthority() error = %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("child Wait() error = %v", err)
	}
}

func TestSupervisorRejectsChildCrashBeforeReadiness(t *testing.T) {
	requireProcessHandoff(t)
	var stderr bytes.Buffer
	launch := helperLaunch("crash")
	launch.Stderr = &stderr
	_, err := (Supervisor{ReadyTimeout: 5 * time.Second}).Start(t.Context(), launch)
	if err == nil || !strings.Contains(err.Error(), "readiness") {
		t.Fatalf("Start() error = %v, want readiness failure", err)
	}
}

func TestSupervisorKillsChildOnReadinessTimeout(t *testing.T) {
	requireProcessHandoff(t)
	started := time.Now()
	_, err := (Supervisor{ReadyTimeout: 100 * time.Millisecond}).Start(t.Context(), helperLaunch("hang"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timeout cleanup took %s", time.Since(started))
	}
}

func TestValidateMessageBindsVersionTypeAndIdentity(t *testing.T) {
	identity := testIdentity()
	for _, tc := range []struct {
		name string
		msg  message
	}{
		{name: "version", msg: message{Version: ProtocolVersion + 1, Type: messageReady, Identity: identity}},
		{name: "type", msg: message{Version: ProtocolVersion, Type: messageActivated, Identity: identity}},
		{name: "identity", msg: message{Version: ProtocolVersion, Type: messageReady, Identity: Identity{Revision: identity.Revision + 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMessage(tc.msg, messageReady, identity); err == nil {
				t.Fatal("validateMessage() succeeded, want rejection")
			}
		})
	}
}

func TestHotRestartHelperProcess(t *testing.T) {
	mode := os.Getenv("NRE_HOT_RESTART_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "crash" {
		os.Exit(23)
	}
	session, child, err := OpenChildSessionFromEnvironment()
	if err != nil || !child {
		os.Exit(24)
	}
	defer session.Close()
	if mode == "hang" {
		select {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Ready(); err != nil {
		os.Exit(25)
	}
	if err := session.AwaitActivation(ctx, nil); err != nil {
		os.Exit(26)
	}
	if err := session.AwaitAuthority(ctx, nil); err != nil {
		os.Exit(27)
	}
}

func helperLaunch(mode string) Launch {
	return Launch{
		Binary:   os.Args[0],
		Argv:     []string{os.Args[0], "-test.run=^TestHotRestartHelperProcess$"},
		Env:      setEnv(os.Environ(), "NRE_HOT_RESTART_TEST_MODE", mode),
		Identity: testIdentity(),
	}
}

func testIdentity() Identity {
	return Identity{Revision: 17, SnapshotDigest: strings.Repeat("a", 64), GenerationID: "generation-17", LeaseID: "lease-17"}
}

func requireProcessHandoff(t *testing.T) {
	t.Helper()
	if !platform.SupportsHotRestart() {
		t.Skipf("process FD handoff is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
