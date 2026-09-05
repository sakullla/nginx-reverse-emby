package pluginhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const loggingCreateValue = "arbitrary-first-material-46852"
const loggingRotateValue = "arbitrary-replaced-material-86421"
const loggingReadValue = "arbitrary-delivered-material-52976"

func TestScopedHostRuntimeLoggingChild(t *testing.T) {
	if os.Getenv("NRE_SCOPED_LOG_TEST_CHILD") != "1" {
		return
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	material, _ := sdk.NewManagedSecretMaterial([]byte(loggingCreateValue))
	defer material.Close()
	request := sdk.ScopedSecretRequest{Action: sdk.ScopedSecretCreate, Binding: sdk.ManagedBinding{InstanceID: "log-instance", EntryID: "log-instance", Generation: "log-generation"}, Reference: sdk.ScopedSecretReference{InstanceID: "log-instance", ID: "credential", Scope: "purpose"}, Material: material}
	created, err := client.ScopedSecret(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Print(loggingCreateValue[:9])
	fmt.Println(loggingCreateValue[9:])
	request.Action, request.Reference, request.Material = sdk.ScopedSecretRead, created.Reference, nil
	read, err := client.ScopedSecret(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Material.WithBytes(func(value []byte) error { fmt.Print(string(value[:10])); fmt.Println(string(value[10:])); return nil }); err != nil {
		t.Fatal(err)
	}
	read.Material.Close()
	request.Action = sdk.ScopedSecretRotate
	request.Material, _ = sdk.NewManagedSecretMaterial([]byte(loggingRotateValue))
	defer request.Material.Close()
	if _, err := client.ScopedSecret(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	fmt.Println(loggingRotateValue)
}

type scopedLoggingDispatcher struct{ writer *redactor }

func (d scopedLoggingDispatcher) DispatchPluginHostResource(ctx context.Context, candidate Candidate, call sdk.HostRuntimeCall) sdk.HostRuntimeResponse {
	request, err := sdk.DecodeScopedSecretRequest(call.Payload)
	if err != nil {
		return sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "invalid fixture request"}}
	}
	defer request.Material.Close()
	response := sdk.ScopedSecretResponse{Reference: request.Reference}
	if request.Material != nil {
		_ = request.Material.WithBytes(func(value []byte) error {
			_, err := d.writer.Write(append(append([]byte(nil), value...), '\n'))
			return err
		})
	}
	switch request.Action {
	case sdk.ScopedSecretCreate:
		response.Reference.Version = strings.Repeat("a", 32)
	case sdk.ScopedSecretRotate:
		response.Reference.Version = strings.Repeat("b", 32)
	case sdk.ScopedSecretRead:
		response.Material, _ = sdk.NewManagedSecretMaterial([]byte(loggingReadValue))
		defer response.Material.Close()
	}
	payload, err := sdk.EncodeScopedSecretResponse(request, response)
	if err != nil {
		return sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: sdk.ErrorInternal, Message: "invalid fixture result"}}
	}
	return sdk.HostRuntimeResponse{Payload: payload}
}

func TestScopedHostRuntimeRegistersBeforeConsumptionAndDeliveryWithDurableLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite and actual SDK subprocess run in the full tier")
	}
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	candidate := Candidate{InstanceID: "log-instance", OperationID: "log-operation", ResourceGroupID: "default", Revision: 1, Identity: Identity{PluginID: "log-plugin", Generation: "log-generation", PackageDigest: strings.Repeat("a", 64)}, Artifact: Artifact{SHA256: strings.Repeat("b", 64)}}
	if err := store.StagePluginRuntime(t.Context(), storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, HostScope: "control-plane", CandidateGeneration: candidate.Identity.Generation, CandidateOperationID: candidate.OperationID, CandidateResourceGroupID: candidate.ResourceGroupID, CandidateRevision: 1, CandidatePackageDigest: candidate.Identity.PackageDigest, CandidateArtifactDigest: candidate.Artifact.SHA256}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	host := &Host{logs: &output}
	var outboxErr error
	serial := 0
	host.logObserver = func(c Candidate, line string) {
		serial++
		err := store.EnqueueControlPlanePluginRuntimeLog(context.Background(), storage.PluginControlPlaneLogOutboxRow{EventID: fmt.Sprintf("log-%d", serial), InstanceID: c.InstanceID, PluginID: c.Identity.PluginID, OperationID: c.OperationID, GenerationID: c.Identity.Generation, ResourceGroupID: c.ResourceGroupID, Revision: c.Revision, PackageDigest: c.Identity.PackageDigest, ArtifactDigest: c.Artifact.SHA256, Level: "info", Message: line, CreatedAt: time.Now().UTC()})
		if err != nil {
			outboxErr = err
		}
	}
	writer := newRedactor(&candidateLogTarget{host: host, candidate: candidate}, nil)
	candidate.logRedactor = writer
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	cookie := hex.EncodeToString(random)
	socket := filepath.Join(os.TempDir(), "nre-log-"+cookie[:12]+".sock")
	defer os.Remove(socket)
	candidate.hostEndpoint = Endpoint{Network: "unix", Address: socket, Cookie: cookie}
	cleanup, err := startHostResourceServer(t.Context(), candidate, scopedLoggingDispatcher{writer})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cookieFile := filepath.Join(t.TempDir(), "cookie")
	if err := os.WriteFile(cookieFile, []byte(cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestScopedHostRuntimeLoggingChild$")
	command.Env = append(os.Environ(), "NRE_SCOPED_LOG_TEST_CHILD=1", sdk.EnvPluginHostEndpoint+"=unix:"+socket, "NRE_PLUGIN_COOKIE_FILE="+cookieFile)
	command.Stdout, command.Stderr = writer, writer
	if err := command.Run(); err != nil {
		t.Fatal("actual SDK HostRuntime child", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if outboxErr != nil {
		t.Fatal(outboxErr)
	}
	rows, err := store.ListControlPlanePluginRuntimeLogOutbox(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 5 {
		t.Fatal("child and dispatch log frames did not reach durable outbox")
	}
	all := output.String()
	for _, row := range rows {
		all += "\n" + row.Message
	}
	for _, value := range []string{loggingCreateValue, loggingReadValue, loggingRotateValue} {
		if strings.Contains(all, value) {
			t.Fatal("scoped exact material leaked into output/outbox")
		}
	}
	if strings.Count(all, "[REDACTED]") < 10 {
		t.Fatal("expected exact redaction evidence was absent")
	}
}

func TestAttemptLogRegistrationConcurrentBoundedAndOwnsStaticCopy(t *testing.T) {
	var output bytes.Buffer
	initial := []string{"initial-exact-value"}
	writer := newRedactor(&output, initial)
	clear(initial)
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			if err := writer.addSecrets(fmt.Sprintf("dynamic-value-%03d", i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	group.Wait()
	_, _ = writer.Write([]byte("initial-exact-value\n"))
	_, _ = writer.Write([]byte("dynamic-value-"))
	_, _ = writer.Write([]byte("007\n"))
	if err := writer.addSecrets("first-sensitive-line\nsecond-sensitive-line"); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("first-sensitive-line\nsecond-sensitive-line\n"))
	if strings.Contains(output.String(), "sensitive-line") {
		t.Fatal("multiline material leaked through line boundaries")
	}
	if strings.Contains(output.String(), "initial-exact-value") || strings.Contains(output.String(), "dynamic-value-007") {
		t.Fatal("registered static/dynamic value leaked")
	}
	if err := writer.addSecrets(strings.Repeat("x", maxAttemptLogSecretBytes)); err == nil {
		t.Fatal("unbounded registration succeeded")
	}
	_, _ = writer.Write([]byte("unknown-post-overflow-material\n"))
	_ = writer.Close()
	if strings.Contains(output.String(), "unknown-post-overflow-material") {
		t.Fatal("overflow did not seal log output")
	}
}
