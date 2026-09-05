package main

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestLocalPluginSecretBindingRejectsMissingOwner(t *testing.T) {
	if err := localPluginSecretServiceBinding(nil)(nil); err == nil {
		t.Fatal("missing local source was accepted")
	}
	source := localagent.NewSyncSource(nil, "local")
	bind := localPluginSecretServiceBinding(source)
	if err := bind(nil); err == nil {
		t.Fatal("missing production service was accepted")
	}
	// The main composition hands off the already-resolved production instance;
	// it does not create another vault/capability owner in the callback.
	if err := bind(service.NewPluginService(nil, "")); err != nil {
		t.Fatal(err)
	}
}
