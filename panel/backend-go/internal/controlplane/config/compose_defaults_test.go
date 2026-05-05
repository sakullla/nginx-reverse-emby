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
	if timezone != "Asia/Shanghai" {
		t.Fatalf("docker-compose.yaml NRE_TIMEZONE = %q, want Asia/Shanghai", timezone)
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
