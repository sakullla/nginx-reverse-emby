//go:build linux && integration && ssplugin

package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const ssSecretVersion = "secret-version-000000000001"

type ssProcessAdmission struct {
	allowed atomic.Int32
	denied  atomic.Int32
}

func (a *ssProcessAdmission) Evaluate(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
	if input.Metadata().Source().Addr().String() == "127.0.0.2" {
		a.denied.Add(1)
		return policy.Decision{Action: policy.ActionDeny, Reason: "fixture-deny"}
	}
	a.allowed.Add(1)
	return policy.Decision{Action: policy.ActionAllow}
}

type ssProcessSecrets map[string]string

func (ssProcessSecrets) RedeemPluginSecrets(context.Context, model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error) {
	return nil, nil
}

func (s ssProcessSecrets) RedeemScopedPluginSecret(_ context.Context, request model.PluginSecretRedemptionRequest) (json.RawMessage, error) {
	decoded, err := sdk.DecodeScopedSecretRequest(request.Scoped)
	if err != nil || decoded.Action != sdk.ScopedSecretRead || decoded.Reference.Scope != "shadowsocks-inbound" {
		return nil, errors.New("unexpected scoped secret request")
	}
	value, ok := s[decoded.Reference.ID]
	if !ok || decoded.Reference.Version != ssSecretVersion {
		return nil, errors.New("unknown scoped secret")
	}
	material, err := sdk.NewManagedSecretMaterial([]byte(value))
	if err != nil {
		return nil, err
	}
	defer material.Close()
	return sdk.EncodeScopedSecretResponse(decoded, sdk.ScopedSecretResponse{Reference: decoded.Reference, Material: material})
}

func TestIntegrationRealShadowsocksManagedHostProcess(t *testing.T) {
	pluginRoot := strings.TrimSpace(os.Getenv("NRE_SS_PLUGIN_ROOT"))
	if pluginRoot == "" {
		t.Fatal("NRE_SS_PLUGIN_ROOT is required for the ssplugin integration suite")
	}
	if info, err := os.Stat(filepath.Join(pluginRoot, "plugins", "shadowsocks-server")); err != nil || !info.IsDir() {
		t.Fatalf("NRE_SS_PLUGIN_ROOT does not contain the shadowsocks-server plugin: %v", err)
	}

	work := t.TempDir()
	pluginBinary := filepath.Join(work, "shadowsocks-server")
	runGo(t, pluginRoot, "build", "-o", pluginBinary, "./plugins/shadowsocks-server/cmd/shadowsocks-server")
	if err := os.Chmod(pluginBinary, 0o600); err != nil {
		t.Fatal(err)
	}
	clientBinary := buildSSProcessClient(t, work, pluginRoot)
	pluginVersion := readSSPluginVersion(t, pluginRoot)
	pluginBytes, err := os.ReadFile(pluginBinary)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(pluginBytes)

	legacyPassword := "legacy-integration-password"
	serverPSK := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 16))
	userPSK := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x52}, 16))
	secrets := ssProcessSecrets{"legacy-user": legacyPassword, "modern-server": serverPSK, "modern-user": userPSK}

	scopes := []string{
		"storage.read", "storage.write", "event.emit", "service.revocable-resource-handle", "agent.read",
		string(sdk.CapabilityRuntimeIdentity), sdk.PermissionManagedNetworkListen, sdk.PermissionManagedNetworkDial,
		sdk.PermissionScopedSecretRead, sdk.PermissionScopedSecretWrite,
	}
	permissions := make([]pluginprocess.SandboxPermission, len(scopes))
	grants := make([]model.PluginGrantProjection, 0, len(scopes))
	for i, scope := range scopes {
		permissions[i] = pluginprocess.SandboxPermission(scope)
		grant := model.PluginGrantProjection{Name: scope}
		if scope == sdk.PermissionScopedSecretRead || scope == sdk.PermissionScopedSecretWrite {
			grant.ResourceKind, grant.ResourceID = "secret-scope", "shadowsocks-inbound"
		}
		grants = append(grants, grant)
	}
	packageDigest := strings.Repeat("a", 64)
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{
		PackageDigest: packageDigest, Permissions: permissions,
		ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 30000, MemoryBytes: 256 << 20, Concurrency: 8, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(work, "runtime")}, pluginprocess.NewSupervisor(nil, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := host.SetRevocationPath(filepath.Join(work, "revocations.json")); err != nil {
		t.Fatal(err)
	}
	host.SetSecretRedeemer(secrets)
	admission := &ssProcessAdmission{}
	features, err := sdk.RequiredRPCFeaturesForExecutionScope(scopes, nil, sdk.HostScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	candidate := HostCandidate{
		InstanceID: "ss-integration", PluginID: "shadowsocks-server", PluginVersion: pluginVersion,
		PackageDigest: packageDigest, Generation: "ss-runtime-generation-1", ProviderGenerationID: "ss-provider-generation-1", OperationID: "ss-operation-1", Revision: 1, AgentID: "edge",
		Artifact:    pluginprocess.Artifact{CachePath: pluginBinary, SHA256: hex.EncodeToString(artifactDigest[:]), GOOS: "linux", GOARCH: "amd64"},
		Requirement: requirement, Scopes: scopes, RequiredFeatures: features, Grants: grants, Config: json.RawMessage(`{"listeners":[]}`),
		Process: pluginprocess.InstanceSpec{GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 10 * time.Second},
		services: &runtimeServices{evaluator: admission, policy: &model.PolicyRef{ID: "ss-ip-policy"}},
	}
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatalf("activate real shadowsocks-server: %v", err)
	}
	if instance.Status().PID <= 0 {
		t.Fatal("real shadowsocks-server process was not started")
	}
	if _, err := host.Call(t.Context(), candidate.PluginID, "listen.apply", json.RawMessage(`{"agent_id":"edge","listens":[]}`)); err != nil {
		t.Fatalf("empty real managed listen apply: %v", err)
	}

	legacyPort := freeSSDualPort(t)
	modernPort := freeSSDualPort(t)
	legacyTarget, legacyTCP := startSSTCPReceiver(t)
	modernTarget, modernUDP := startSSUDPReceiver(t)
	apply := map[string]any{"agent_id": "edge", "listens": []map[string]any{
		{"id": "legacy", "port": legacyPort, "method": "aes-256-gcm", "users": []map[string]any{{"id": "legacy-user", "enabled": true, "secret_ref": "legacy-user", "secret_version": ssSecretVersion}}},
		{"id": "modern", "port": modernPort, "method": "2022-blake3-aes-128-gcm", "server_secret_ref": "modern-server", "server_secret_version": ssSecretVersion, "users": []map[string]any{{"id": "modern-user", "enabled": true, "secret_ref": "modern-user", "secret_version": ssSecretVersion}}},
	}}
	applyPayload, _ := json.Marshal(apply)
	if _, err := host.Call(t.Context(), candidate.PluginID, "listen.apply", applyPayload); err != nil {
		t.Fatalf("apply real managed listeners: %v payload=%s", err, applyPayload)
	}

	runSSClient(t, clientBinary, "tcp", "127.0.0.1", legacyPort, legacyTarget, "aes-256-gcm", legacyPassword, "legacy-allowed")
	if got := waitSSPayload(t, legacyTCP); got != "legacy-allowed" {
		t.Fatalf("legacy TCP target payload = %q", got)
	}
	runSSClient(t, clientBinary, "udp", "127.0.0.1", modernPort, modernTarget, "2022-blake3-aes-128-gcm", serverPSK+":"+userPSK, "modern-allowed")
	if got := waitSSPayload(t, modernUDP); got != "modern-allowed" {
		t.Fatalf("2022 UDP target payload = %q", got)
	}

	runSSClient(t, clientBinary, "tcp", "127.0.0.2", legacyPort, legacyTarget, "aes-256-gcm", legacyPassword, "must-not-arrive")
	select {
	case payload := <-legacyTCP:
		t.Fatalf("denied source reached plugin target: %q", payload)
	case <-time.After(500 * time.Millisecond):
	}
	if admission.allowed.Load() < 2 || admission.denied.Load() < 1 {
		t.Fatalf("Host admission decisions = allowed:%d denied:%d", admission.allowed.Load(), admission.denied.Load())
	}

	conflictPort := freeSSDualPort(t)
	reservation, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(conflictPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	conflict := map[string]any{"id": "conflict", "port": conflictPort, "method": "aes-256-gcm", "users": []map[string]any{{"id": "legacy-user", "enabled": true, "secret_ref": "legacy-user", "secret_version": ssSecretVersion}}}
	apply["listens"] = append(apply["listens"].([]map[string]any), conflict)
	conflictPayload, _ := json.Marshal(apply)
	if _, err := host.Call(t.Context(), candidate.PluginID, "listen.apply", conflictPayload); err == nil {
		t.Fatal("port-conflict listener candidate succeeded")
	}
	runSSClient(t, clientBinary, "udp", "127.0.0.1", modernPort, modernTarget, "2022-blake3-aes-128-gcm", serverPSK+":"+userPSK, "old-kept")
	if got := waitSSPayload(t, modernUDP); got != "old-kept" {
		t.Fatalf("failed listener candidate displaced old listener: %q", got)
	}

	revoke := model.PluginGenerationRevokeRequest{InstanceID: candidate.InstanceID, PluginID: candidate.PluginID, GenerationID: candidate.Generation, ProviderGenerationID: candidate.ProviderGenerationID, Revision: candidate.Revision, FenceID: "ss-fence-1"}
	if err := host.RevokeGeneration(t.Context(), revoke); err != nil {
		t.Fatal(err)
	}
	if !instance.terminated() {
		t.Fatal("generation revoke acknowledged before real process termination")
	}
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(legacyPort)), 300*time.Millisecond); err == nil {
		connection.Close()
		t.Fatal("revoked generation retained managed listener")
	}
}

func runGo(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func readSSPluginVersion(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "plugins", "shadowsocks-server", "model.go"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`PluginVersion\s*=\s*"([^"]+)"`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("shadowsocks-server PluginVersion is unavailable")
	}
	return string(match[1])
}

func buildSSProcessClient(t *testing.T, work, pluginRoot string) string {
	t.Helper()
	directory := filepath.Join(work, "client")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module nre-ss-process-client\n\ngo 1.27.0\n\nrequire github.com/sakullla/sakullla-plugins v0.0.0\nreplace github.com/sakullla/sakullla-plugins => %s\n", pluginRoot)
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	const source = `package main
import (
 "crypto/rand"
 "encoding/base64"
 "fmt"
 "net"
 "os"
 "strconv"
 "strings"
 "time"
 ss "github.com/sakullla/sakullla-plugins/plugins/shadowsocks-server"
)
func main() {
 if len(os.Args) != 8 { panic("protocol local proxy-port target method material payload") }
 protocol, localIP, target, method, material := os.Args[1], os.Args[2], os.Args[4], os.Args[5], os.Args[6]
 payload := []byte(os.Args[7])
 if protocol == "tcpb64" { decoded, decodeErr := base64.RawStdEncoding.DecodeString(os.Args[7]); if decodeErr != nil { panic(decodeErr) }; protocol, payload = "tcp", decoded }
 port, err := strconv.Atoi(os.Args[3]); if err != nil { panic(err) }
 engine, err := ss.NewProtocolEngine(method, []byte(material)); if err != nil { panic(err) }; defer engine.Destroy()
 proxy := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
 if protocol == "tcp" {
  salt := make([]byte, engine.SaltSize()); if _, err = rand.Read(salt); err != nil { panic(err) }
  wire, err := engine.SealTCPRequest(salt, target, payload, time.Now(), nil); if err != nil { panic(err) }
  conn, err := (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(localIP)}}).Dial("tcp", proxy); if err != nil { panic(err) }; defer conn.Close()
  _ = conn.SetDeadline(time.Now().Add(3*time.Second)); if _, err = conn.Write(wire); err != nil { panic(err) }; return
 }
 seed := make([]byte, engine.SaltSize()); packetID := uint64(0)
 if strings.HasPrefix(method, "2022-") { seed = []byte{1,2,3,4,5,6,7,8}; packetID = 1 } else if _, err = rand.Read(seed); err != nil { panic(err) }
 wire, err := engine.SealUDPPacket(seed, packetID, target, payload, time.Now(), nil); if err != nil { panic(err) }
 conn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.ParseIP(localIP)}, mustUDP(proxy)); if err != nil { panic(err) }; defer conn.Close()
 _ = conn.SetWriteDeadline(time.Now().Add(3*time.Second)); if _, err = conn.Write(wire); err != nil { panic(err) }
}
func mustUDP(value string) *net.UDPAddr { address, err := net.ResolveUDPAddr("udp", value); if err != nil { panic(fmt.Sprint(err)) }; return address }
`
	if err := os.WriteFile(filepath.Join(directory, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "ss-client")
	runGo(t, directory, "mod", "tidy")
	runGo(t, directory, "build", "-o", binary, ".")
	return binary
}

func runSSClient(t *testing.T, binary, protocol, localIP string, port int, target, method, material, payload string) {
	t.Helper()
	command := exec.Command(binary, protocol, localIP, strconv.Itoa(port), target, method, material, payload)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("SS %s client: %v\n%s", protocol, err, output)
	}
}

func freeSSDualPort(t *testing.T) int {
	t.Helper()
	for attempts := 0; attempts < 20; attempts++ {
		tcp, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := tcp.Addr().(*net.TCPAddr).Port
		udp, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		tcp.Close()
		if err == nil {
			udp.Close()
			return port
		}
	}
	t.Fatal("no free TCP/UDP port pair")
	return 0
}

func startSSTCPReceiver(t *testing.T) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	payloads := make(chan string, 8)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
				value := make([]byte, 64<<10)
				n, _ := connection.Read(value)
				if n > 0 {
					payloads <- string(value[:n])
				}
			}()
		}
	}()
	return listener.Addr().String(), payloads
}

func startSSUDPReceiver(t *testing.T) (string, <-chan string) {
	t.Helper()
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	payloads := make(chan string, 8)
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, _, err := connection.ReadFrom(buffer)
			if err != nil {
				return
			}
			payloads <- string(append([]byte(nil), buffer[:n]...))
		}
	}()
	return connection.LocalAddr().String(), payloads
}

func waitSSPayload(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Shadowsocks target payload")
		return ""
	}
}
