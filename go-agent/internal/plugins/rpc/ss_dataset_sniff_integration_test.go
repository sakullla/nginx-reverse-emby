//go:build linux && integration && ssplugin

package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const (
	ssPinnedGeoSiteName = "dlc.dat"
	ssPinnedGeoSiteSHA  = "f82f26c015f9726c763d96a5f658e5b31b285dc094a985e718051e421f350ed6"
	ssPinnedGeoIPName   = "loyalsoldier-geoip.dat"
	ssPinnedGeoIPSHA    = "4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721"
)

func TestIntegrationRealShadowsocksDatasetSniffAndIPPolicy(t *testing.T) {
	pluginRoot := requiredSSPath(t, "NRE_SS_PLUGIN_ROOT")
	ipArtifact := requiredSSPath(t, "NRE_IP_POLICY_WASM")
	dataCache := requiredSSPath(t, "NRE_DATASET_SOURCE_CACHE")
	work := t.TempDir()
	pluginBinary := filepath.Join(work, "shadowsocks-server")
	runGo(t, pluginRoot, "build", "-o", pluginBinary, "./plugins/shadowsocks-server/cmd/shadowsocks-server")
	if err := os.Chmod(pluginBinary, 0o600); err != nil {
		t.Fatal(err)
	}
	clientBinary := buildSSProcessClient(t, work, pluginRoot)
	pluginVersion := readSSPluginVersion(t, pluginRoot)
	artifactBytes, err := os.ReadFile(pluginBinary)
	if err != nil {
		t.Fatal(err)
	}
	artifactSum := sha256.Sum256(artifactBytes)
	artifactDigest := hex.EncodeToString(artifactSum[:])

	view, datasetProvider, evaluator, closePolicy := prepareSSRealPolicyGeneration(t, work, ipArtifact, dataCache, "ss-primary")
	defer closePolicy()
	warmSSPolicyEvaluator(t, evaluator)

	upstreamPassword := "controlled-upstream-password"
	inboundPassword := "primary-inbound-password"
	secrets := ssProcessSecrets{"primary-user": inboundPassword, "upstream-credential": upstreamPassword}
	upstreamSecrets := ssProcessSecrets{"upstream-user": upstreamPassword}
	primaryHost := newSSProcessHost(t, filepath.Join(work, "primary-host"), secrets)
	upstreamHost := newSSProcessHost(t, filepath.Join(work, "upstream-host"), upstreamSecrets)

	upstreamAdmission := &ssProcessAdmission{}
	upstreamCandidate := newSSProcessCandidate(t, pluginBinary, artifactDigest, pluginVersion, "ss-upstream", "ss-upstream-generation", &runtimeServices{evaluator: upstreamAdmission, policy: &model.PolicyRef{ID: "allow"}})
	upstreamInstance, err := upstreamHost.Activate(t.Context(), upstreamCandidate)
	if err != nil {
		t.Fatalf("activate controlled upstream SS: %v", err)
	}
	upstreamPort := freeSSDualPort(t)
	upstreamApply, _ := json.Marshal(map[string]any{"agent_id": "edge", "listens": []map[string]any{{
		"id": "upstream", "port": upstreamPort, "method": "aes-256-gcm",
		"users": []map[string]any{{"id": "upstream-user", "enabled": true, "secret_ref": "upstream-user", "secret_version": ssSecretVersion}},
	}}})
	if _, err := upstreamHost.Call(t.Context(), upstreamCandidate.PluginID, "listen.apply", upstreamApply); err != nil {
		t.Fatalf("apply controlled upstream SS: %v", err)
	}

	primaryCandidate := newSSProcessCandidate(t, pluginBinary, artifactDigest, pluginVersion, "ss-primary", view.ID(), &runtimeServices{datasets: datasetProvider, evaluator: evaluator, policy: &model.PolicyRef{ID: "effective"}})
	primaryInstance, err := primaryHost.Activate(t.Context(), primaryCandidate)
	if err != nil {
		t.Fatalf("activate routed SS: %v", err)
	}
	primaryPort := freeSSDualPort(t)
	routing := map[string]any{
		"upstreams":      []map[string]any{{"id": "ai-upstream", "host": "127.0.0.1", "port": upstreamPort, "method": "aes-256-gcm", "secret_ref": "upstream-credential", "secret_version": ssSecretVersion, "enabled": true, "tcp": true, "udp": false}},
		"rules":          []map[string]any{{"id": "ai", "source_id": "geosite", "classification": map[string]any{"name": "category-ai-!cn", "kind": "domain"}, "action": "upstream", "upstream_id": "ai-upstream"}},
		"default_action": "direct",
	}
	primaryApply := map[string]any{"agent_id": "edge", "listens": []map[string]any{{
		"id": "primary", "port": primaryPort, "method": "aes-256-gcm",
		"users": []map[string]any{{"id": "primary-user", "enabled": true, "secret_ref": "primary-user", "secret_version": ssSecretVersion}},
	}}, "routing": routing}
	primaryApplyWire, _ := json.Marshal(primaryApply)
	if _, err := primaryHost.Call(t.Context(), primaryCandidate.PluginID, "listen.apply", primaryApplyWire); err != nil {
		t.Fatalf("apply routed SS: %v", err)
	}

	finalTarget, finalPayloads := startSSTCPReceiver(t)
	httpAI := "GET /v1/models HTTP/1.1\r\nHost: api.openai.com\r\nConnection: close\r\n\r\n"
	runSSClient(t, clientBinary, "tcp", "127.0.0.1", primaryPort, finalTarget, "aes-256-gcm", inboundPassword, httpAI)
	if got := waitSSPayload(t, finalPayloads); got != httpAI {
		t.Fatalf("AI HTTP payload changed across upstream: %q", got)
	}
	tlsHello := ssTLSClientHello(t, "api.openai.com")
	runSSClient(t, clientBinary, "tcpb64", "127.0.0.1", primaryPort, finalTarget, "aes-256-gcm", inboundPassword, base64.RawStdEncoding.EncodeToString(tlsHello))
	if got := []byte(waitSSPayload(t, finalPayloads)); !bytes.Equal(got, tlsHello) {
		t.Fatal("TLS ClientHello changed across upstream")
	}
	if upstreamAdmission.allowed.Load() < 2 {
		t.Fatalf("HTTP/TLS did not traverse observable upstream admission: %d", upstreamAdmission.allowed.Load())
	}

	runSSClient(t, clientBinary, "tcp", "127.0.0.2", primaryPort, finalTarget, "aes-256-gcm", inboundPassword, httpAI)
	assertSSNoPayload(t, finalPayloads, "IP policy denied source reached upstream/final receiver")

	if os.Getenv("NRE_SS_PERF") == "1" {
		runSSRoutePerformance(t, clientBinary, primaryPort, finalTarget, inboundPassword, httpAI, finalPayloads, primaryInstance.Status().PID, primaryCandidate.InstanceID+"@"+primaryCandidate.Generation, upstreamCandidate.InstanceID+"@"+upstreamCandidate.Generation)
	}
	beforeDirect := upstreamAdmission.allowed.Load()
	directPayload := "GET / HTTP/1.1\r\nHost: unrecognized.invalid\r\nConnection: close\r\n\r\n"
	runSSClient(t, clientBinary, "tcp", "127.0.0.1", primaryPort, finalTarget, "aes-256-gcm", inboundPassword, directPayload)
	if got := waitSSPayload(t, finalPayloads); got != directPayload {
		t.Fatalf("default direct payload = %q", got)
	}
	if upstreamAdmission.allowed.Load() != beforeDirect {
		t.Fatalf("default direct flow traversed upstream: %d -> %d", beforeDirect, upstreamAdmission.allowed.Load())
	}

	upstreamRevoke := model.PluginGenerationRevokeRequest{InstanceID: upstreamCandidate.InstanceID, PluginID: upstreamCandidate.PluginID, GenerationID: upstreamCandidate.Generation, ProviderGenerationID: upstreamCandidate.ProviderGenerationID, Revision: upstreamCandidate.Revision, FenceID: "upstream-fence"}
	if err := upstreamHost.RevokeGeneration(t.Context(), upstreamRevoke); err != nil || !upstreamInstance.terminated() {
		t.Fatalf("revoke controlled upstream: %v", err)
	}
	runSSClient(t, clientBinary, "tcp", "127.0.0.1", primaryPort, finalTarget, "aes-256-gcm", inboundPassword, httpAI)
	assertSSNoPayload(t, finalPayloads, "failed upstream fell back to direct")

	primaryRevoke := model.PluginGenerationRevokeRequest{InstanceID: primaryCandidate.InstanceID, PluginID: primaryCandidate.PluginID, GenerationID: primaryCandidate.Generation, ProviderGenerationID: primaryCandidate.ProviderGenerationID, Revision: primaryCandidate.Revision, FenceID: "primary-fence"}
	if err := primaryHost.RevokeGeneration(t.Context(), primaryRevoke); err != nil || !primaryInstance.terminated() {
		t.Fatalf("revoke routed SS: %v", err)
	}
}

func warmSSPolicyEvaluator(t *testing.T, evaluator policy.Evaluator) {
	t.Helper()
	metadata, err := policy.NewDirectMetadata(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	body, err := policy.NewBodyWindow(nil, true, policy.BodyNotSkipped)
	if err != nil {
		t.Fatal(err)
	}
	input, err := policy.NewInput(policy.ExtensionL4, "ss-policy-warmup", metadata, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithEntryID("managed-entry")
	var decision policy.Decision
	for attempt := 0; attempt < 3; attempt++ {
		decision = evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: "effective"}, input)
		if decision.Action == policy.ActionAllow && !decision.Degraded {
			return
		}
	}
	t.Fatalf("real IP policy did not become ready within warmup bound: %+v", decision)
}

func requiredSSPath(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the ssplugin integration suite", name)
	}
	return value
}

func newSSProcessHost(t *testing.T, root string, secrets ssProcessSecrets) *Host {
	t.Helper()
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: root}, pluginprocess.NewSupervisor(nil, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.SetRevocationPath(filepath.Join(root, "revocations.json")); err != nil {
		t.Fatal(err)
	}
	host.SetSecretRedeemer(secrets)
	t.Cleanup(func() { host.Close(context.Background()) })
	return host
}

func newSSProcessCandidate(t *testing.T, binary, artifactDigest, version, instanceID, generation string, services *runtimeServices) HostCandidate {
	t.Helper()
	scopes := []string{"storage.read", "storage.write", "event.emit", "service.revocable-resource-handle", "agent.read", string(sdk.CapabilityRuntimeIdentity), sdk.PermissionManagedNetworkListen, sdk.PermissionManagedNetworkDial, sdk.PermissionScopedSecretRead, sdk.PermissionScopedSecretWrite, string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityDatasetResolve)}
	permissions := make([]pluginprocess.SandboxPermission, len(scopes))
	grants := make([]model.PluginGrantProjection, 0, len(scopes))
	for index, scope := range scopes {
		permissions[index] = pluginprocess.SandboxPermission(scope)
		grant := model.PluginGrantProjection{Name: scope}
		if scope == sdk.PermissionScopedSecretRead || scope == sdk.PermissionScopedSecretWrite {
			grant.ResourceKind, grant.ResourceID = "secret-scope", "shadowsocks-inbound"
		}
		grants = append(grants, grant)
	}
	packageDigest := strings.Repeat("d", 64)
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{PackageDigest: packageDigest, Permissions: permissions, ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 30000, MemoryBytes: 256 << 20, Concurrency: 8, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 3}})
	if err != nil {
		t.Fatal(err)
	}
	features, err := sdk.RequiredRPCFeaturesForExecutionScope(scopes, nil, sdk.HostScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	return HostCandidate{InstanceID: instanceID, PluginID: "shadowsocks-server", PluginVersion: version, PackageDigest: packageDigest, Generation: generation, ProviderGenerationID: "provider-" + instanceID, OperationID: "operation-" + instanceID, Revision: 1, AgentID: "edge", Artifact: pluginprocess.Artifact{CachePath: binary, SHA256: artifactDigest, GOOS: "linux", GOARCH: "amd64"}, Requirement: requirement, Scopes: scopes, RequiredFeatures: features, Grants: grants, Config: json.RawMessage(`{"listeners":[]}`), Process: pluginprocess.InstanceSpec{GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 10 * time.Second}, services: services}
}

func prepareSSRealPolicyGeneration(t *testing.T, work, wasmPath, cache, ssInstance string) (*module.GenerationView, *policy.DatasetGeneration, policy.Evaluator, func()) {
	t.Helper()
	geoSite := compileSSPinnedDataset(t, work, cache, ssPinnedGeoSiteName, ssPinnedGeoSiteSHA, sdk.DatasetSource{ID: "geosite", Name: "V2Fly GeoSite", Format: sdk.DatasetFormatGeoSite}, []model.DatasetInstanceBinding{{InstanceID: ssInstance, Classifications: []sdk.DatasetClassification{{Name: "category-ai-!cn", Kind: sdk.DatasetClassificationDomain}}}})
	geoIP := compileSSPinnedDataset(t, work, cache, ssPinnedGeoIPName, ssPinnedGeoIPSHA, sdk.DatasetSource{ID: "geoip", Name: "Loyalsoldier GeoIP", Format: sdk.DatasetFormatGeoIP}, []model.DatasetInstanceBinding{{InstanceID: "ip-instance", Classifications: []sdk.DatasetClassification{{Name: "cn", Kind: sdk.DatasetClassificationCountry}}}})
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	wasmSum := sha256.Sum256(wasmBytes)
	wasmDigest := hex.EncodeToString(wasmSum[:])
	mode := sdk.PolicyModeEnforce
	settings := sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 1, InstanceVersion: 1}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingRaw, DefaultMode: &mode}}
	grants := []string{string(sdk.CapabilityDatasetResolve), string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityPolicyTrustedSource), "l4.inspect", "event.emit"}
	config := json.RawMessage(`{"schema":"sakullla.ip-policy/v1","default_action":"allow","datasets":[{"id":"geoip","source_id":"geoip","classifications":[{"id":"china","name":"cn","kind":"country"}]}],"province_whitelist":[],"rules":[{"id":"deny-cn","action":"deny","selector":{"type":"classification","dataset_id":"geoip","classification_id":"china"}},{"id":"deny-loopback-two","action":"deny","selector":{"type":"cidr","value":"127.0.0.2/32"}}]}`)
	stage := model.PolicyStage{PolicySettings: &settings, Kind: model.PolicyKindIP, PolicyID: "ip-instance", PluginID: "ip-policy", PluginVersion: "1.0.0", InstanceID: "ip-instance", PackageDigest: wasmDigest, ArtifactPath: wasmPath, ArtifactDigest: wasmDigest, SignatureVerified: true, SignerKeyID: "integration", SignerFingerprint: strings.Repeat("e", 64), ABI: model.PolicyABIV1, ExtensionPoints: []string{policy.ExtensionL4}, DeclaredScopes: grants, GrantedScopes: grants, ResourceGroupID: "default", Config: config, ResourceBudget: model.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 16 << 20, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096}, FailurePolicy: model.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
	ref := &model.PolicyRef{ID: "effective", StageModes: []model.PolicyModeBinding{{Stage: sdk.PolicyStageIdentity{Kind: "ip", PolicyID: "ip-instance"}, Snapshot: settings}}}
	snapshot := model.Snapshot{Revision: 1, Datasets: []model.DatasetSnapshot{geoSite, geoIP}, PluginPolicies: []model.PluginPolicy{{ID: "effective", Revision: 1, Stages: []model.PolicyStage{stage}}}, PluginGenerations: []model.PluginGeneration{{ID: "ss-definition", InstanceID: ssInstance, ManagedNetworkPolicies: map[string]*model.PolicyRef{"tcp": ref, "udp": ref}}}}
	wasmRuntime, err := wasm.NewRuntime(t.Context(), wasm.RuntimeOptions{MaxMemoryPages: 1024})
	if err != nil {
		t.Fatal(err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(policy.NewModule(wasm.GenerationFactory{Runtime: wasmRuntime}, nil)); err != nil {
		t.Fatal(err)
	}
	identity, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := registry.PrepareGeneration(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, _ := prepared.Publish()
	datasetValue, ok := view.Resolve(policy.ProviderDatasets)
	if !ok {
		t.Fatal("real dataset provider missing")
	}
	evaluatorValue, ok := view.Resolve(policy.ProviderEvaluator)
	if !ok {
		t.Fatal("real IP evaluator missing")
	}
	closeFn := func() { view.Destroy(context.Background()); wasmRuntime.Close(context.Background()) }
	return view, datasetValue.(*policy.DatasetGeneration), evaluatorValue.(policy.Evaluator), closeFn
}

func compileSSPinnedDataset(t *testing.T, work, cache, name, expectedSHA string, source sdk.DatasetSource, bindings []model.DatasetInstanceBinding) model.DatasetSnapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cache, name))
	if err != nil {
		t.Fatalf("required pinned dataset %s unavailable: %v", name, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != expectedSHA {
		t.Fatalf("pinned dataset %s digest = %s, want %s", name, got, expectedSHA)
	}
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: source, Revision: expectedSHA[:16], FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + expectedSHA, Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatalf("compile pinned dataset %s: %v", name, err)
	}
	encoded, err := index.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	indexSum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(indexSum[:])
	path := filepath.Join(work, "index-"+source.ID)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return model.DatasetSnapshot{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + digest, Kind: model.DatasetArtifactKind, SHA256: digest, SizeBytes: int64(len(encoded)), LocalPath: path}, Bindings: bindings}
}

func ssTLSClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = tls.Client(clientSide, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}).Handshake()
	}()
	_ = serverSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	buffer := make([]byte, 64<<10)
	n, err := serverSide.Read(buffer)
	serverSide.Close()
	clientSide.Close()
	<-done
	if err != nil || n == 0 {
		t.Fatalf("build TLS ClientHello: %v", err)
	}
	return append([]byte(nil), buffer[:n]...)
}

func assertSSNoPayload(t *testing.T, values <-chan string, message string) {
	t.Helper()
	select {
	case value := <-values:
		t.Fatalf("%s: %q", message, value)
	case <-time.After(700 * time.Millisecond):
	}
}

func runSSRoutePerformance(t *testing.T, client string, port int, target, password, payload string, received <-chan string, pid int, primaryRef, upstreamRef string) {
	t.Helper()
	lateResponseCount := 0
	waitForSample := func(want string, timeout time.Duration) bool {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case got := <-received:
				if got == want {
					return true
				}
				lateResponseCount++
			case <-timer.C:
				return false
			}
		}
	}
	latencies := make([]time.Duration, 20)
	sampleIDs := make([]string, len(latencies))
	retryCount := 0
	started := time.Now()
	for index := range latencies {
		sampleIDs[index] = fmt.Sprintf("sample-%02d", index)
		samplePayload := ssRouteSamplePayload(payload, sampleIDs[index])
		begin := time.Now()
		for {
			runSSClient(t, client, "tcp", "127.0.0.1", port, target, "aes-256-gcm", password, samplePayload)
			if waitForSample(samplePayload, 200*time.Millisecond) {
				break
			}
			retryCount++
			if retryCount > 2 {
				t.Fatal("route performance exceeded two bounded retries")
			}
		}
		latencies[index] = time.Since(begin)
	}
	elapsed := time.Since(started)
	ordered := append([]time.Duration(nil), latencies...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	rss := ssProcessRSS(t, pid)
	throughput := float64(len(latencies)) / elapsed.Seconds()
	if rss <= 0 || rss > 256<<20 {
		t.Fatalf("plugin process RSS is outside 256 MiB bound: %d", rss)
	}
	if ordered[19] > 500*time.Millisecond || throughput < 2 {
		t.Fatalf("route performance budget exceeded: p99=%s throughput=%.2f", ordered[19], throughput)
	}
	raw := make([]int64, len(latencies))
	for index, latency := range latencies {
		raw[index] = latency.Nanoseconds()
	}
	evidence := map[string]any{
		"raw_latency_ns": raw, "throughput_per_sec": throughput,
		"p95_ns": ordered[18].Nanoseconds(), "p99_ns": ordered[19].Nanoseconds(), "rss_bytes": rss,
		"retry_count": retryCount, "late_response_count": lateResponseCount, "sample_ids": sampleIDs,
		"dataset_count": 2, "dataset_digests": map[string]string{ssPinnedGeoSiteName: ssPinnedGeoSiteSHA, ssPinnedGeoIPName: ssPinnedGeoIPSHA},
		"candidate_refs": []string{primaryRef, upstreamRef},
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("NRE_SS_PERF_JSON=%s\n", encoded)
}

func ssRouteSamplePayload(base, sampleID string) string {
	marker := "X-NRE-Perf-Sample: " + sampleID + "\r\n"
	if index := strings.Index(base, "\r\n\r\n"); index >= 0 {
		return base[:index+2] + marker + base[index+2:]
	}
	return base + "\r\n" + marker
}

func ssProcessRSS(t *testing.T, pid int) int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return value * 1024
	}
	t.Fatal("plugin process VmRSS is unavailable")
	return 0
}
