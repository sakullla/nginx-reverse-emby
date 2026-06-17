package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeDefaultTimezoneUsesRuntimeTzdata(t *testing.T) {
	composePath := filepath.Join("..", "..", "..", "..", "..", "docker-compose.yaml")
	body, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", composePath, err)
	}
	timezone := ""
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "NRE_TIMEZONE:") {
			timezone = strings.TrimSpace(strings.TrimPrefix(trimmed, "NRE_TIMEZONE:"))
		}
	}
	if timezone != "Asia/Shanghai" && timezone != "${NRE_TIMEZONE:-Asia/Shanghai}" {
		t.Fatalf("docker-compose.yaml NRE_TIMEZONE = %q, want Asia/Shanghai default", timezone)
	}

	dockerfilePath := filepath.Join("..", "..", "..", "..", "..", "Dockerfile")
	dockerfileBody, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", dockerfilePath, err)
	}
	controlPlaneStage := strings.SplitN(string(dockerfileBody), "FROM debian:trixie-slim AS control-plane-runtime", 2)
	if len(controlPlaneStage) != 2 {
		t.Fatal("Dockerfile missing control-plane-runtime stage")
	}
	controlPlaneBody := controlPlaneStage[1]
	if nextStage := strings.Index(controlPlaneBody, "\nFROM "); nextStage >= 0 {
		controlPlaneBody = controlPlaneBody[:nextStage]
	}
	if !strings.Contains(controlPlaneBody, "ca-certificates tzdata") {
		t.Fatal("control-plane-runtime image must install tzdata before Compose defaults to an IANA timezone")
	}

	mainPath := filepath.Join("..", "..", "..", "cmd", "nre-control-plane", "main.go")
	mainBody, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", mainPath, err)
	}
	if strings.Contains(string(mainBody), `_ "time/tzdata"`) {
		t.Fatal("control-plane binary should not embed time/tzdata when runtime image installs tzdata")
	}
}

func TestComposeDefaultsDoNotExposeWeakTokensPublicly(t *testing.T) {
	composePath := filepath.Join("..", "..", "..", "..", "..", "docker-compose.yaml")
	body, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", composePath, err)
	}
	compose := string(body)

	for _, forbidden := range []string{
		"API_TOKEN: change-this-token",
		"MASTER_REGISTER_TOKEN: change-this-register-token",
		"PANEL_BACKEND_HOST: 0.0.0.0",
	} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("docker-compose.yaml must not include unsafe default %q", forbidden)
		}
	}
	for _, required := range []string{
		"API_TOKEN: ${API_TOKEN:?",
		"MASTER_REGISTER_TOKEN: ${MASTER_REGISTER_TOKEN:?",
		"PANEL_BACKEND_HOST: ${PANEL_BACKEND_HOST:-127.0.0.1}",
		"NRE_PANEL_PUBLIC_PATH: ${NRE_PANEL_PUBLIC_PATH:-}",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("docker-compose.yaml missing secure default %q: %s", required, compose)
		}
	}
}

func TestDocsDescribePanelSelfProxyHTTPSBootstrap(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..", "..", "..")
	for _, relPath := range []string{
		filepath.Join("README.md"),
		filepath.Join("docs-site", "getting-started", "deploy.md"),
		filepath.Join("docs-site", "getting-started", "quickstart.md"),
		filepath.Join("docs-site", "guides", "http-rules.md"),
		filepath.Join("docs-site", "reference", "security.md"),
	} {
		path := filepath.Join(repoRoot, relPath)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(body)
		if !strings.Contains(text, "http://127.0.0.1:8080") {
			t.Fatalf("%s should document local bootstrap access", relPath)
		}
		if !strings.Contains(text, "NRE_PUBLIC_URL") {
			t.Fatalf("%s should document NRE_PUBLIC_URL after HTTPS bootstrap", relPath)
		}
	}
}

func TestBeginnerComposeDeployScriptExists(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "..", "..", "..", "scripts", "deploy-compose.sh")
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", scriptPath, err)
	}
	script := string(body)
	for _, want := range []string{
		"#!/bin/sh",
		"docker compose",
		"docker-compose",
		"自动安装 Docker Compose",
		"API_TOKEN",
		"MASTER_REGISTER_TOKEN",
		"PANEL_BACKEND_HOST",
		"random_hex()",
		"Cloudflare API Token",
		"ACME_DNS_PROVIDER",
		"CF_TOKEN",
		"NRE_PANEL_PUBLIC_PATH",
		"/panel-api/agents/local/rules",
		"/panel-api/agents/local/apply",
		"随机访问路径",
		"ssh -L 8080:127.0.0.1:8080",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("deploy-compose.sh missing %q", want)
		}
	}
}

func TestLegacyNginxTemplatesMovedOutOfRootConfD(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..", "..", "..")
	for _, name := range []string{"p.example.com.conf", "p.example.com.no_tls.conf"} {
		legacyPath := filepath.Join(repoRoot, "legacy", "conf.d", name)
		if _, err := os.Stat(legacyPath); err != nil {
			t.Fatalf("legacy nginx template missing %s: %v", legacyPath, err)
		}
		rootPath := filepath.Join(repoRoot, "conf.d", name)
		if _, err := os.Stat(rootPath); !os.IsNotExist(err) {
			t.Fatalf("root conf.d template should be moved to legacy path: %s err=%v", rootPath, err)
		}
	}

	deployPath := filepath.Join(repoRoot, "deploy.sh")
	body, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", deployPath, err)
	}
	if !strings.Contains(string(body), "$CONF_HOME/legacy/conf.d/$tpl_name") {
		t.Fatalf("deploy.sh should fetch legacy templates from legacy/conf.d")
	}
}
