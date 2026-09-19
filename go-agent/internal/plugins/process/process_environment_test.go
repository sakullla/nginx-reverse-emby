package process

import (
	"strings"
	"testing"
)

func TestProcessEnvironmentAcceptsGeneratedPluginAppWorkdir(t *testing.T) {
	environment, err := buildProcessEnvironment(nil, []string{
		"NRE_PLUGIN_ENDPOINT=unix:/run/nre-plugin/r.sock",
		"NRE_PLUGIN_APP_WORKDIR=/var/lib/nre-agent/plugin-resources/docker-compose/docker-app-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "NRE_PLUGIN_APP_WORKDIR=/var/lib/nre-agent/plugin-resources/docker-compose/docker-app-default") {
		t.Fatalf("plugin app workdir missing: %q", joined)
	}
}

func TestProcessEnvironmentRejectsGeneratedNonPluginKeys(t *testing.T) {
	_, err := buildProcessEnvironment(nil, []string{"WORKDIR=/var/lib/nre-agent/plugin-resources/docker-compose/docker-app-default"})
	if err == nil {
		t.Fatal("generated environment keys outside NRE_PLUGIN_ must be rejected")
	}
}
