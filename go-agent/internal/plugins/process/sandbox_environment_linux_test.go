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

func TestLinuxChildEnvironmentRemapsUIEndpointOntoGuestDescriptor(t *testing.T) {
	environment := linuxChildEnvironment([]string{
		"NRE_PLUGIN_ENDPOINT=unix:/proc/self/fd/99/r-old.sock",
		"NRE_PLUGIN_UI_ENDPOINT=unix:/proc/self/fd/99/u-host.sock",
	}, 8, 0, "/run/nre-plugin/r-guest.sock")
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "NRE_PLUGIN_ENDPOINT=unix:/proc/self/fd/8/r-guest.sock") {
		t.Fatalf("RPC endpoint was not remapped: %s", joined)
	}
	if !strings.Contains(joined, "NRE_PLUGIN_UI_ENDPOINT=unix:/proc/self/fd/8/u-host.sock") {
		t.Fatalf("UI endpoint was not remapped onto the guest descriptor: %s", joined)
	}
}
