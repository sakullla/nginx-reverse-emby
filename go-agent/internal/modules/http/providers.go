package http

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	"io"
	"net/http"
)

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) (Providers, error) {
	provider := Providers{EgressProfiles: egressProfiles}
	if resolver == nil {
		return provider, nil
	}
	tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial)
	if hostTLS, ok := tlsMaterial.(TLSMaterialProvider); ok {
		provider.TLS = hostTLS
	}
	if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
		provider.Relay = relayTLS
	}
	if egressResolver, _ := resolver.Resolve(module.ProviderEgressResolver); egressResolver != nil {
		if profileResolver, ok := egressResolver.(module.EgressResolver); ok {
			provider.EgressResolver = profileResolver
		}
	}
	if evaluator, _ := resolver.Resolve(module.ProviderPolicyEvaluator); evaluator != nil {
		if policyEvaluator, ok := evaluator.(policy.Evaluator); ok {
			provider.PolicyEvaluator = policyEvaluator
		}
	}
	if providers, _ := resolver.Resolve(rpc.ProviderHTTPBackendProviders); providers != nil {
		if direct, ok := providers.(HTTPBackendProviderResolver); ok {
			provider.HTTPBackendProviders = direct
		} else if set, ok := providers.(*rpc.HTTPBackendProviderSet); ok {
			provider.HTTPBackendProviders = rpcHTTPBackendProviderResolver{set: set}
		}
	}
	finalHopProvider, _ := resolver.Resolve(module.ProviderFinalHopDialer)
	if dialer := relay.FinalHopDialerFromProvider(finalHopProvider); dialer != nil {
		provider.FinalHopDialer = dialer
	}
	return provider, nil
}

type rpcHTTPBackendProviderResolver struct{ set *rpc.HTTPBackendProviderSet }

func (resolver rpcHTTPBackendProviderResolver) Resolve(instanceID, providerID string) (HTTPBackendProvider, bool) {
	handle, found := resolver.set.Resolve(instanceID, providerID)
	if !found {
		return nil, false
	}
	return rpcHTTPBackendProviderHandle{handle: handle}, true
}

type rpcHTTPBackendProviderHandle struct {
	handle *rpc.HTTPBackendProviderHandle
}

func (handle rpcHTTPBackendProviderHandle) InstanceID() string { return handle.handle.InstanceID() }
func (handle rpcHTTPBackendProviderHandle) ProviderID() string { return handle.handle.ProviderID() }
func (handle rpcHTTPBackendProviderHandle) Generation() string { return handle.handle.Generation() }
func (handle rpcHTTPBackendProviderHandle) Acquire() (io.Closer, error) {
	return handle.handle.Acquire()
}
func (handle rpcHTTPBackendProviderHandle) RoundTrip(request *http.Request, authority rpc.HTTPBackendProviderAuthority) (*http.Response, error) {
	return handle.handle.RoundTrip(request, authority)
}
