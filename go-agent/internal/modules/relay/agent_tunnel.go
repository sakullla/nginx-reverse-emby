package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// agentTunnelCredentials resolves the tunnel credential provider, falling back
// to the process-wide binding used by the execution plane.
func agentTunnelCredentials(provider TunnelCredentialProvider) (TunnelCredentialProvider, error) {
	if provider == nil {
		provider = processTunnelCredentialProvider()
	}
	if provider == nil {
		return nil, errors.New("tunnel credential provider is required for agent tunnels")
	}
	return provider, nil
}

// AgentTunnelServerTLSConfig builds the TLS configuration for a host-managed
// agent-to-agent tunnel listener. The listener presents this agent's own
// agent-identity tunnel credential and requires every peer to present a
// current agent-identity credential from the same signed PKI security state
// (mutual TLS, TLS 1.3). When expectedPeerAgentID is non-empty the peer URI
// identity must belong to that agent.
func AgentTunnelServerTLSConfig(ctx context.Context, provider TunnelCredentialProvider, expectedPeerAgentID string) (*tls.Config, error) {
	tunnel, err := agentTunnelCredentials(provider)
	if err != nil {
		return nil, err
	}
	security, err := tunnel.LoadTunnelSecurity(ctx)
	if err != nil {
		return nil, errors.New("load tunnel security state: " + err.Error())
	}
	if err := validateTunnelSecurityState(security); err != nil {
		return nil, err
	}
	if _, err := validateInstalledTunnelCertificate(ctx, tunnel, AgentTunnelCredentialIdentity, model.PKICertificatePurposeClient, security, nil); err != nil {
		return nil, err
	}
	clientCAs, _, err := tunnelTrustPool(security.Snapshot)
	if err != nil {
		return nil, err
	}
	expectedPeerAgentID = strings.TrimSpace(expectedPeerAgentID)

	config := &tls.Config{
		MinVersion:                  tls.VersionTLS13,
		ClientAuth:                  tls.RequireAnyClientCert,
		ClientCAs:                   clientCAs,
		SessionTicketsDisabled:      true,
		DynamicRecordSizingDisabled: true,
	}
	// Agent identities are issued with the client-auth purpose, so the PKI store
	// only installs a client-certificate callback. The tunnel serves with that
	// same credential: the TLS server never validates its own chain, and peers
	// verify it explicitly through VerifyConnection below.
	config.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		certificateCtx := context.Background()
		if hello != nil {
			certificateCtx = hello.Context()
		}
		return loadTunnelClientCertificate(certificateCtx, tunnel, AgentTunnelCredentialIdentity)
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		current, err := tunnel.LoadTunnelSecurity(context.Background())
		if err != nil {
			return errors.New("load current tunnel security state: " + err.Error())
		}
		_, err = verifyTunnelPeer(state.PeerCertificates, current, tunnelPeerExpectation{
			purpose: model.PKICertificatePurposeClient,
			domain:  current.Snapshot.PKIDomainID,
			agentID: expectedPeerAgentID,
		})
		return err
	}
	return config, nil
}

// AgentTunnelClientTLSConfig builds the TLS configuration for dialing a
// host-managed agent-to-agent tunnel listener. This agent presents its own
// agent-identity credential and requires the peer to present the agent
// identity credential owned by expectedAgentID.
func AgentTunnelClientTLSConfig(ctx context.Context, provider TunnelCredentialProvider, expectedAgentID string) (*tls.Config, error) {
	tunnel, err := agentTunnelCredentials(provider)
	if err != nil {
		return nil, err
	}
	expectedAgentID = strings.TrimSpace(expectedAgentID)
	if expectedAgentID == "" {
		return nil, errors.New("expected peer agent id is required for agent tunnels")
	}
	security, err := tunnel.LoadTunnelSecurity(ctx)
	if err != nil {
		return nil, errors.New("load tunnel security state: " + err.Error())
	}
	if err := validateTunnelSecurityState(security); err != nil {
		return nil, err
	}
	if _, err := validateInstalledTunnelCertificate(ctx, tunnel, AgentTunnelCredentialIdentity, model.PKICertificatePurposeClient, security, nil); err != nil {
		return nil, err
	}

	config := &tls.Config{
		// codeql[go/disabled-certificate-check]
		// The peer agent credential carries a URI identity, not a DNS name.
		// Chain, time, EKU, URI identity, revocation, and generation are all
		// verified against the signed security owner in VerifyConnection.
		InsecureSkipVerify:          true,
		MinVersion:                  tls.VersionTLS13,
		SessionTicketsDisabled:      true,
		DynamicRecordSizingDisabled: true,
	}
	config.GetClientCertificate = func(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		certificateCtx := context.Background()
		if request != nil {
			certificateCtx = request.Context()
		}
		return loadTunnelClientCertificate(certificateCtx, tunnel, AgentTunnelCredentialIdentity)
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		current, err := tunnel.LoadTunnelSecurity(context.Background())
		if err != nil {
			return errors.New("load current tunnel security state: " + err.Error())
		}
		_, err = verifyTunnelPeer(state.PeerCertificates, current, tunnelPeerExpectation{
			purpose: model.PKICertificatePurposeClient,
			domain:  current.Snapshot.PKIDomainID,
			agentID: expectedAgentID,
		})
		return err
	}
	return config, nil
}
