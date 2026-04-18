package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRouterServesJoinScriptAndHeartbeat(t *testing.T) {
	distDir := filepath.Join(t.TempDir(), "dist")
	assetDir := filepath.Join(t.TempDir(), "assets")
	if err := os.MkdirAll(filepath.Join(distDir, "assets"), 0o755); err != nil {
		t.Fatalf("MkdirAll(dist) error = %v", err)
	}
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html><body>control-plane</body></html>"), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "assets", "app.js"), []byte("console.log('panel');"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.js) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "nre-agent-linux-amd64"), []byte{0x7f, 0x45, 0x4c, 0x46}, 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

	state := &fakeAgentServiceState{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:           "secret",
			FrontendDistDir:      distDir,
			PublicAgentAssetsDir: assetDir,
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{
			heartbeatReply: service.HeartbeatReply{DesiredRevision: 12, Rules: []storage.HTTPRule{}},
			state:          state,
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	server := httptest.NewServer(router)
	defer server.Close()

	scriptResp, err := http.Get(server.URL + "/panel-api/public/join-agent.sh")
	if err != nil {
		t.Fatalf("GET join-agent.sh error = %v", err)
	}
	defer scriptResp.Body.Close()
	if scriptResp.StatusCode != http.StatusOK {
		t.Fatalf("GET join-agent.sh = %d", scriptResp.StatusCode)
	}
	scriptBytes := new(bytes.Buffer)
	if _, err := scriptBytes.ReadFrom(scriptResp.Body); err != nil {
		t.Fatalf("ReadFrom(join-agent.sh) error = %v", err)
	}
	script := scriptBytes.String()
	if !strings.Contains(script, `DEFAULT_ASSET_BASE_URL="`+server.URL+`/panel-api/public/agent-assets"`) {
		t.Fatalf("join-agent.sh missing asset base url: %s", script)
	}
	if !strings.Contains(script, `ASSET_NAME="nre-agent-$PLATFORM-$ARCH"`) {
		t.Fatalf("join-agent.sh missing asset name: %s", script)
	}
	if !strings.Contains(script, "BIN_TMP_PATH=\"$BIN_PATH.tmp.$$\"") {
		t.Fatalf("join-agent.sh missing staged binary temp path: %s", script)
	}
	if !strings.Contains(script, "run_root_cmd systemctl daemon-reload") {
		t.Fatalf("join-agent.sh missing systemd daemon-reload: %s", script)
	}
	if !strings.Contains(script, "SERVICE_EXISTS=\"0\"") {
		t.Fatalf("join-agent.sh missing service state detection: %s", script)
	}
	if !strings.Contains(script, "systemctl stop nginx-reverse-emby-agent.service") {
		t.Fatalf("join-agent.sh missing systemd stop before replace: %s", script)
	}
	if !strings.Contains(script, "mv \"$BIN_TMP_PATH\" \"$BIN_PATH\"") {
		t.Fatalf("join-agent.sh missing atomic staged binary move: %s", script)
	}
	if !strings.Contains(script, "systemctl start nginx-reverse-emby-agent.service") {
		t.Fatalf("join-agent.sh missing explicit systemd start after replace: %s", script)
	}
	if !strings.Contains(script, "extract_registered_agent_id()") {
		t.Fatalf("join-agent.sh missing registered agent id extraction helper: %s", script)
	}
	if !strings.Contains(script, "NRE_AGENT_ID=") {
		t.Fatalf("join-agent.sh missing persisted NRE_AGENT_ID env entry: %s", script)
	}
	if strings.Contains(script, "light-agent.js") {
		t.Fatalf("join-agent.sh unexpectedly references light-agent.js")
	}

	heartbeatBody := bytes.NewBufferString(`{"current_revision":1}`)
	heartbeatReq, err := http.NewRequest(http.MethodPost, server.URL+"/panel-api/agents/heartbeat", heartbeatBody)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	heartbeatReq.Header.Set("Content-Type", "application/json")
	heartbeatReq.Header.Set("X-Agent-Token", "agent-token")
	heartbeatReq.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.8")
	heartbeatResp, err := http.DefaultClient.Do(heartbeatReq)
	if err != nil {
		t.Fatalf("POST heartbeat error = %v", err)
	}
	defer heartbeatResp.Body.Close()
	if heartbeatResp.StatusCode != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", heartbeatResp.StatusCode)
	}
	var heartbeatPayload struct {
		Sync service.HeartbeatReply `json:"sync"`
	}
	if err := json.NewDecoder(heartbeatResp.Body).Decode(&heartbeatPayload); err != nil {
		t.Fatalf("Decode heartbeat response error = %v", err)
	}
	if heartbeatPayload.Sync.DesiredRevision != 12 {
		t.Fatalf("heartbeat desired revision = %d", heartbeatPayload.Sync.DesiredRevision)
	}
	if state.heartbeatToken != "agent-token" {
		t.Fatalf("heartbeat token = %q", state.heartbeatToken)
	}
	if state.heartbeat.LastSeenIP != "203.0.113.10" {
		t.Fatalf("heartbeat LastSeenIP = %q", state.heartbeat.LastSeenIP)
	}

	assetResp, err := http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js error = %v", err)
	}
	defer assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d", assetResp.StatusCode)
	}

	spaResp, err := http.Get(server.URL + "/agents/remote-1")
	if err != nil {
		t.Fatalf("GET /agents/remote-1 error = %v", err)
	}
	defer spaResp.Body.Close()
	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /agents/remote-1 = %d", spaResp.StatusCode)
	}

	binaryResp, err := http.Get(server.URL + "/panel-api/public/agent-assets/nre-agent-linux-amd64")
	if err != nil {
		t.Fatalf("GET public agent asset error = %v", err)
	}
	defer binaryResp.Body.Close()
	if binaryResp.StatusCode != http.StatusOK {
		t.Fatalf("GET public agent asset = %d", binaryResp.StatusCode)
	}
}

func TestPublicAgentAssetRejectsPathTraversal(t *testing.T) {
	assetDir := filepath.Join(t.TempDir(), "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetDir) error = %v", err)
	}
	secretPath := filepath.Join(filepath.Dir(assetDir), "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	deps := Dependencies{
		Config: config.Config{
			PublicAgentAssetsDir: assetDir,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/agent-assets/../secret.txt", nil)
	resp := httptest.NewRecorder()
	deps.handlePublicAgentAsset(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET traversal asset = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestJoinScriptIncludesMigrateFromMainCommand(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, "migrate-from-main") {
		t.Fatalf("join-agent.sh missing migrate-from-main command")
	}
	if !strings.Contains(script, `DATA_DIR="/var/lib/nre-agent"`) {
		t.Fatalf("join-agent.sh missing normalized agent data dir default")
	}
	if !strings.Contains(script, `SOURCE_DIR="/opt/nginx-reverse-emby-agent"`) {
		t.Fatalf("join-agent.sh missing legacy source dir default")
	}
	if !strings.Contains(script, "cleanup_legacy_acme()") {
		t.Fatalf("join-agent.sh missing legacy acme cleanup helper")
	}
	if !strings.Contains(script, "normalize_legacy_acme_domain()") {
		t.Fatalf("join-agent.sh missing wildcard acme normalization helper")
	}
	if !strings.Contains(script, `acme_domain="$(normalize_legacy_acme_domain "$cert_domain")"`) {
		t.Fatalf("join-agent.sh missing normalized acme domain usage")
	}
	if !strings.Contains(script, `--remove -d "$acme_domain" --ecc`) {
		t.Fatalf("join-agent.sh missing normalized acme remove command")
	}
}

func TestJoinScriptPreservesMigratedAgentIdentity(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, `load_existing_agent_env_if_present "$ENV_FILE"`) {
		t.Fatalf("join-agent.sh missing migrated agent env reload")
	}
	if strings.Contains(script, `mv "$OLD_DATA_DIR/." "$DATA_DIR/"`) {
		t.Fatalf("join-agent.sh still moves the literal dot entry during data migration")
	}
}

func TestJoinScriptIncludesUninstallAndLegacyNginxCleanup(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, "uninstall-agent") {
		t.Fatalf("join-agent.sh missing uninstall-agent command")
	}
	if !strings.Contains(script, "cleanup_legacy_nginx_runtime()") {
		t.Fatalf("join-agent.sh missing shared legacy nginx cleanup helper")
	}
	if !strings.Contains(script, "legacy_nginx_runtime_present()") {
		t.Fatalf("join-agent.sh missing legacy nginx runtime detection helper")
	}
	if !strings.Contains(script, "cleanup_local_agent_runtime()") {
		t.Fatalf("join-agent.sh missing local uninstall cleanup helper")
	}
	localCleanupBody, found := strings.CutPrefix(script[strings.Index(script, "cleanup_local_agent_runtime()"):], "cleanup_local_agent_runtime() {\n")
	if !found {
		t.Fatalf("join-agent.sh missing cleanup_local_agent_runtime body")
	}
	localCleanupBody, _, _ = strings.Cut(localCleanupBody, "\n}\n\nload_legacy_runtime()")
	if !strings.Contains(localCleanupBody, "cleanup_legacy_nginx_runtime") {
		t.Fatalf("join-agent.sh uninstall cleanup should stop host nginx via legacy cleanup helper: %s", localCleanupBody)
	}
	if !strings.Contains(script, "if ! legacy_nginx_runtime_present; then") {
		t.Fatalf("join-agent.sh cleanup should skip unrelated nginx installs")
	}
	if !strings.Contains(script, "disable_systemd_unit_if_present nginx.service") {
		t.Fatalf("join-agent.sh cleanup should only disable nginx when legacy runtime markers exist")
	}
	if strings.Contains(script, "/panel-api/agents/$NRE_AGENT_ID") {
		t.Fatalf("join-agent.sh unexpectedly attempts control-plane unregister during uninstall")
	}
}

func TestJoinScriptInstallsStableUninstallWrapper(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, `JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"`) {
		t.Fatalf("join-agent.sh missing persisted join script path")
	}
	if !strings.Contains(script, `UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"`) {
		t.Fatalf("join-agent.sh missing uninstall wrapper path")
	}
	if !strings.Contains(script, "persist_installed_join_script()") {
		t.Fatalf("join-agent.sh missing join script persistence helper")
	}
	if !strings.Contains(script, "install_uninstall_wrapper()") {
		t.Fatalf("join-agent.sh missing uninstall wrapper installer")
	}
	if !strings.Contains(script, `curl -fsSL --connect-timeout 15 --max-time 300 "$MASTER_URL/panel-api/public/join-agent.sh" -o "$JOIN_SCRIPT_PATH"`) {
		t.Fatalf("join-agent.sh missing persisted join script download")
	}
	if !strings.Contains(script, `exec $(shell_quote "$JOIN_SCRIPT_PATH") uninstall-agent --data-dir $(shell_quote "$DATA_DIR")`) {
		t.Fatalf("join-agent.sh missing uninstall wrapper delegation")
	}
	if !strings.Contains(script, `--source-dir $(shell_quote "$SOURCE_DIR")`) {
		t.Fatalf("join-agent.sh missing optional source-dir forwarding in uninstall wrapper")
	}
	if !strings.Contains(script, "install_uninstall_wrapper") {
		t.Fatalf("join-agent.sh missing uninstall wrapper install call")
	}
}

func TestDockerComposeMountsControlPlaneDataDir(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", ".."))
	composePath := filepath.Join(repoRoot, "docker-compose.yaml")
	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("ReadFile(docker-compose.yaml) error = %v", err)
	}
	compose := string(composeBytes)
	if !strings.Contains(compose, "./data:/opt/nginx-reverse-emby/panel/data") {
		t.Fatalf("docker-compose.yaml missing control-plane data dir mount: %s", compose)
	}
}

func TestHeartbeatResponseKeepsRelayCertificatesWhenRelayListenersPresentWithoutUpdate(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{heartbeatReply: service.HeartbeatReply{
			HasUpdate:       false,
			DesiredRevision: 7,
			RelayListeners:  []storage.RelayListener{{ID: 11, AgentID: "edge", Name: "relay-a"}},
			Certificates: []storage.ManagedCertificateBundle{{
				ID:      31,
				Domain:  "relay-a.example.com",
				CertPEM: "cert",
				KeyPEM:  "key",
			}},
			CertificatePolicies: []storage.ManagedCertificatePolicy{{
				ID:              31,
				Domain:          "relay-a.example.com",
				Enabled:         true,
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
			}},
		}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":7}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	syncPayload, ok := payload["sync"].(map[string]any)
	if !ok {
		t.Fatalf("sync payload = %#v", payload["sync"])
	}
	if _, found := syncPayload["rules"]; found {
		t.Fatalf("unexpected rules key in no-update relay payload: %+v", syncPayload)
	}
	if _, found := syncPayload["l4_rules"]; found {
		t.Fatalf("unexpected l4_rules key in no-update relay payload: %+v", syncPayload)
	}
	if _, found := syncPayload["relay_listeners"]; !found {
		t.Fatalf("expected relay_listeners key in no-update relay payload: %+v", syncPayload)
	}
	if _, found := syncPayload["certificates"]; !found {
		t.Fatalf("expected certificates key in no-update relay payload: %+v", syncPayload)
	}
	if _, found := syncPayload["certificate_policies"]; !found {
		t.Fatalf("expected certificate_policies key in no-update relay payload: %+v", syncPayload)
	}
}

func TestHeartbeatResponseIncludesEmptyArraysWhenUpdateClearsState(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{heartbeatReply: service.HeartbeatReply{
			HasUpdate:           true,
			DesiredRevision:     9,
			Rules:               []storage.HTTPRule{},
			L4Rules:             []storage.L4Rule{},
			RelayListeners:      []storage.RelayListener{},
			Certificates:        []storage.ManagedCertificateBundle{},
			CertificatePolicies: []storage.ManagedCertificatePolicy{},
		}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	syncPayload, ok := payload["sync"].(map[string]any)
	if !ok {
		t.Fatalf("sync payload = %#v", payload["sync"])
	}
	for _, key := range []string{"rules", "l4_rules", "certificates", "certificate_policies"} {
		value, found := syncPayload[key]
		if !found {
			t.Fatalf("expected %s key in update payload: %+v", key, syncPayload)
		}
		arrayValue, ok := value.([]any)
		if !ok || len(arrayValue) != 0 {
			t.Fatalf("expected %s to be an empty array, got %#v", key, value)
		}
	}
}

func TestHeartbeatResponseIncludesVersionPackageMetadataWithoutDesiredVersion(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{heartbeatReply: service.HeartbeatReply{
			HasUpdate:       false,
			DesiredVersion:  "",
			DesiredRevision: 9,
			VersionPackageMeta: &storage.VersionPackage{
				Platform: "linux-amd64",
				URL:      "/panel-api/public/agent-assets/nre-agent-linux-amd64",
				SHA256:   "desired-sha",
				Filename: "nre-agent-linux-amd64",
			},
			VersionSHA256: "desired-sha",
		}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":9}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "panel.example.com")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	syncPayload, ok := payload["sync"].(map[string]any)
	if !ok {
		t.Fatalf("sync payload = %#v", payload["sync"])
	}
	if syncPayload["desired_version"] != "" {
		t.Fatalf("desired_version = %#v", syncPayload["desired_version"])
	}
	if syncPayload["version_sha256"] != "desired-sha" {
		t.Fatalf("version_sha256 = %#v", syncPayload["version_sha256"])
	}
	meta, ok := syncPayload["version_package_meta"].(map[string]any)
	if !ok {
		t.Fatalf("version_package_meta = %#v", syncPayload["version_package_meta"])
	}
	if meta["sha256"] != "desired-sha" {
		t.Fatalf("version_package_meta.sha256 = %#v", meta["sha256"])
	}
	if meta["url"] != "https://panel.example.com/panel-api/public/agent-assets/nre-agent-linux-amd64" {
		t.Fatalf("version_package_meta.url = %#v", meta["url"])
	}
}

func TestHeartbeatUsesRemoteAddrHostWhenForwardedMissing(t *testing.T) {
	state := &fakeAgentServiceState{}
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{
			heartbeatReply: service.HeartbeatReply{DesiredRevision: 2},
			state:          state,
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	req.RemoteAddr = "198.51.100.7:12345"
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}
	if state.heartbeatToken != "agent-token" {
		t.Fatalf("heartbeat token = %q", state.heartbeatToken)
	}
	if state.heartbeat.LastSeenIP != "198.51.100.7" {
		t.Fatalf("heartbeat LastSeenIP = %q", state.heartbeat.LastSeenIP)
	}
}
