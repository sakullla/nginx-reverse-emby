package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
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

func TestPluginHostSecretRevealPreservesMaterialUntilResponseEncoding(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{
		InstanceID:      "cloudflare-dns-default",
		ResourceGroupID: "default",
		Identity: pluginhost.Identity{
			PluginID:   "cloudflare-dns",
			Generation: "generation-1",
		},
		Grants: []string{"secret.use"},
	}
	ref := "vault/cloudflare/map/test"
	want := []byte("non-zero-cloudflare-token")
	if _, err := vault.Create(t.Context(), secrets.OperationContext{
		ActorID:         "plugin/cloudflare-dns",
		CorrelationID:   candidate.Identity.Generation,
		ResourceGroupID: candidate.ResourceGroupID,
	}, pluginHostSecretName(candidate, ref), pluginHostSecretPurpose, string(want)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(pluginHostSecretPayload{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	response := (&PluginCapabilityManager{secretVault: vault}).dispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation: "secret.reveal",
		Payload:   payload,
	})
	if response.Error != nil {
		t.Fatalf("secret.reveal error = %v", response.Error)
	}
	var result struct {
		Material []byte `json:"material"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Material, want) {
		t.Fatalf("revealed material = %q, want %q", result.Material, want)
	}
}
