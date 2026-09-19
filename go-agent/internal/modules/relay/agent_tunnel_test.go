package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"testing"
)

type stubTunnelCredentialProvider struct {
	securityErr error
}

func (s *stubTunnelCredentialProvider) InstallTunnelCertificate(context.Context, string, *tls.Config) (TunnelCredentialMetadata, error) {
	return TunnelCredentialMetadata{}, errors.New("stub provider has no credentials")
}

func (s *stubTunnelCredentialProvider) LoadTunnelCredential(context.Context, string) (TunnelCredentialMetadata, error) {
	return TunnelCredentialMetadata{}, errors.New("stub provider has no credentials")
}

func (s *stubTunnelCredentialProvider) LoadTunnelSecurity(context.Context) (TunnelSecurityState, error) {
	if s.securityErr != nil {
		return TunnelSecurityState{}, s.securityErr
	}
	return TunnelSecurityState{}, nil
}

func TestAgentTunnelTLSConfigRequiresCredentials(t *testing.T) {
	if _, err := AgentTunnelServerTLSConfig(context.Background(), nil, ""); err == nil {
		t.Fatal("server config without a tunnel credential provider should fail")
	}
	if _, err := AgentTunnelClientTLSConfig(context.Background(), nil, "agent-1"); err == nil {
		t.Fatal("client config without a tunnel credential provider should fail")
	}
}

func TestAgentTunnelClientTLSConfigRequiresExpectedAgent(t *testing.T) {
	provider := &stubTunnelCredentialProvider{}
	if _, err := AgentTunnelClientTLSConfig(context.Background(), provider, "  "); err == nil {
		t.Fatal("client config without an expected peer agent id should fail")
	}
}

func TestAgentTunnelTLSConfigSurfacesSecurityStateErrors(t *testing.T) {
	provider := &stubTunnelCredentialProvider{securityErr: errors.New("security state unavailable")}
	if _, err := AgentTunnelServerTLSConfig(context.Background(), provider, ""); err == nil ||
		!strings.Contains(err.Error(), "security state unavailable") {
		t.Fatalf("server config error = %v", err)
	}
	if _, err := AgentTunnelClientTLSConfig(context.Background(), provider, "agent-1"); err == nil ||
		!strings.Contains(err.Error(), "security state unavailable") {
		t.Fatalf("client config error = %v", err)
	}
}

func TestAgentTunnelTLSConfigRejectsIncompleteSecurityState(t *testing.T) {
	// A provider that returns an empty security state cannot establish a
	// verified trust domain; both configs must fail closed.
	provider := &stubTunnelCredentialProvider{}
	if _, err := AgentTunnelServerTLSConfig(context.Background(), provider, ""); err == nil {
		t.Fatal("server config with an incomplete security state should fail")
	}
	if _, err := AgentTunnelClientTLSConfig(context.Background(), provider, "agent-1"); err == nil {
		t.Fatal("client config with an incomplete security state should fail")
	}
}
