package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostOutboundRequiresGrantedExactHostname(t *testing.T) {
	t.Parallel()
	candidate := pluginhost.Candidate{
		Identity:       pluginhost.Identity{Scopes: []string{pluginsdk.PermissionHTTPOutbound}},
		Grants:         []string{pluginsdk.PermissionHTTPOutbound},
		GrantSelectors: map[string][]string{pluginsdk.PermissionHTTPOutbound: {"api.cloudflare.com"}},
	}
	if !pluginCandidateAllowsOutboundHost(candidate, "API.CLOUDFLARE.COM.") {
		t.Fatal("exact granted host was denied")
	}
	if pluginCandidateAllowsOutboundHost(candidate, "example.com") || pluginCandidateAllowsOutboundHost(candidate, "sub.api.cloudflare.com") {
		t.Fatal("ungranted outbound host was accepted")
	}
	candidate.Grants = nil
	if pluginCandidateHasGrant(candidate, pluginsdk.PermissionHTTPOutbound) {
		t.Fatal("declared scope was treated as a durable grant")
	}
}

func TestPluginHostMutationsRequireDurableOperationID(t *testing.T) {
	t.Parallel()
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "secret.put"}) {
		t.Fatal("secret.put did not require an operation id")
	}
	getPayload, _ := json.Marshal(pluginHostHTTPRequest{Method: http.MethodGet})
	if pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "http.secret-request", Payload: getPayload}) {
		t.Fatal("GET unexpectedly required a mutation operation id")
	}
	postPayload, _ := json.Marshal(pluginHostHTTPRequest{Method: http.MethodPost})
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "http.secret-request", Payload: postPayload}) {
		t.Fatal("POST did not require a durable operation id")
	}
}
