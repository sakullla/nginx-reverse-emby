package process

import (
	"strings"
	"testing"
)

func TestLinuxChildEnvironmentAddsGenerationDockerProxyToPath(t *testing.T) {
	environment := linuxChildEnvironment([]string{
		"PATH=/usr/bin:/bin",
		"NRE_PLUGIN_DOCKER_CLI=/run/nre-plugin/docker",
		"NRE_PLUGIN_DOCKER_PROXY_ENDPOINT=unix:/run/nre-plugin/docker-proxy.sock",
	}, 0, 0, "")
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "PATH=/run/nre-plugin:/usr/bin:/bin") {
		t.Fatalf("Docker proxy PATH was not injected: %s", joined)
	}
}
