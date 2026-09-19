package pluginhost

import (
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestLifecycleResponseErrorRequiresReadySuccess(t *testing.T) {
	if err := lifecycleResponseError(pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}); err != nil {
		t.Fatalf("ready success rejected: %v", err)
	}

	failure := &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "plugin activation unavailable", Retryable: true}
	if err := lifecycleResponseError(pluginsdk.LifecycleResponse{Error: failure}); err != failure {
		t.Fatalf("error branch = %v, want exact runtime error", err)
	}

	if err := lifecycleResponseError(pluginsdk.LifecycleResponse{}); err == nil {
		t.Fatal("missing lifecycle result accepted")
	}
}
