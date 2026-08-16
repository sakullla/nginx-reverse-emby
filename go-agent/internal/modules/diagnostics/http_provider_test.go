package diagnostics

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type diagnosticProviderResolverFixture struct {
	provider diagnosticHTTPBackendProvider
}

func (fixture diagnosticProviderResolverFixture) Resolve(instanceID, providerID string) (diagnosticHTTPBackendProvider, bool) {
	if fixture.provider == nil || instanceID != fixture.provider.InstanceID() || providerID != fixture.provider.ProviderID() {
		return nil, false
	}
	return fixture.provider, true
}

type diagnosticProviderFixture struct{}

func (diagnosticProviderFixture) InstanceID() string { return "accelerator-local" }
func (diagnosticProviderFixture) ProviderID() string { return "default" }
func (diagnosticProviderFixture) Acquire() (io.Closer, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (diagnosticProviderFixture) RoundTrip(request *http.Request, authority pluginrpc.HTTPBackendProviderAuthority) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestHTTPProberDiagnosesCurrentAgentPluginProviderWithoutResolvingAHost(t *testing.T) {
	t.Parallel()
	provider := diagnosticProviderFixture{}
	prober := NewHTTPProber(HTTPProberConfig{
		Attempts:             1,
		Timeout:              time.Second,
		HTTPBackendProviders: diagnosticProviderResolverFixture{provider: provider},
	})

	report, err := prober.Diagnose(context.Background(), model.HTTPRule{
		ID:          1,
		FrontendURL: "http://127.0.0.1",
		Backends: []model.HTTPBackend{{
			Kind: pluginsdk.HTTPBackendKindPluginProvider,
			PluginProvider: &pluginsdk.HTTPPluginProviderRef{
				InstanceID: provider.InstanceID(),
				ProviderID: provider.ProviderID(),
			},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if report.Kind != "http" || len(report.Backends) != 1 {
		t.Fatalf("Diagnose() = %+v", report)
	}
}
