package dockerproxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Optional field diagnostic: exercise the authenticated command handler and
// real Docker daemon without installing, pulling or changing containers.
func TestLiveDockerProxyReadOnlyMetadata(t *testing.T) {
	images := strings.Fields(os.Getenv("NRE_DOCKER_APP_LIVE_IMAGES"))
	if len(images) == 0 {
		t.Skip("set NRE_DOCKER_APP_LIVE_IMAGES for read-only Agent diagnostics")
	}
	handler := &handler{cookie: "diagnostic-only", workspaceRoot: t.TempDir(), runner: ExecRunner{}}
	commands := [][]string{{"info", "--format", "{{json .RegistryConfig.Mirrors}}"}}
	for _, image := range images {
		commands = append(commands, []string{"image", "inspect", "--format", "{{.Architecture}}\n{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}", image})
	}
	for _, args := range commands {
		payload, err := json.Marshal(Request{Args: args})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, proxyPath, bytes.NewReader(payload))
		request.Header.Set(credentialHeader, "diagnostic-only")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var result Response
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || result.ExitCode != 0 || result.Error != "" || len(result.Output) == 0 {
			t.Fatalf("read-only %s failed: status=%d exit=%d error=%s", args[0], response.Code, result.ExitCode, result.Error)
		}
		t.Logf("allowed %s: %s", args[0], strings.TrimSpace(string(result.Output)))
	}
}
