package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginPublishedEntryWireIncludesInstanceIdentity(t *testing.T) {
	t.Parallel()
	entry := PluginPublishedEntry{
		InstanceID: "instance-a", RuleID: 7, AgentID: "edge-a",
		FrontendURL: "https://example.test", Enabled: true, Accessible: true,
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"instance_id":"instance-a"`) {
		t.Fatalf("published entry wire = %s", encoded)
	}
}

func TestPluginSummaryRollbackPermissionsUseRollbackManifest(t *testing.T) {
	t.Parallel()
	permissions, err := pluginPackagePermissions(storage.PluginPackageRow{ManifestJSON: `{
		"permissions":[
			{"name":"secret.use","resource":"vault/team"},
			{"name":"http.inspect"}
		]
	}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 2 || permissions[0] != "http.inspect" || permissions[1] != "secret.use:vault/team" {
		t.Fatalf("rollback permissions = %#v", permissions)
	}
	summary := PluginSummary{RollbackPackageDigest: strings.Repeat("a", 64), RollbackPermissions: permissions}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"rollback_permissions":["http.inspect","secret.use:vault/team"]`) {
		t.Fatalf("rollback permission wire = %s", encoded)
	}
}
