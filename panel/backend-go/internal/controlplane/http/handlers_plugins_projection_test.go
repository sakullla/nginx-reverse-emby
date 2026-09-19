package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestPluginPublishedEntryProjectsMatchedInstanceIdentity(t *testing.T) {
	t.Parallel()
	entry := pluginPublishedEntryFromRule(" instance-a ", service.HTTPRule{
		ID: 7, AgentID: "edge-a", FrontendURL: "https://example.test", Enabled: true,
	}, true)
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if entry.InstanceID != "instance-a" || !strings.Contains(string(encoded), `"instance_id":"instance-a"`) {
		t.Fatalf("published entry wire = %s", encoded)
	}
}
