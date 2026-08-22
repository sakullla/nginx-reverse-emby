package relay

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var processTunnelCredentials struct {
	sync.RWMutex
	provider TunnelCredentialProvider
}

// SetProcessTunnelCredentialProvider binds the execution plane's private PKI
// facade to relay consumers that receive the shared managed-certificate
// provider from another module (HTTP, L4, and diagnostics). A process owns one
// agent identity; embedded and remote agents run in separate processes/stores.
func SetProcessTunnelCredentialProvider(provider TunnelCredentialProvider) {
	processTunnelCredentials.Lock()
	processTunnelCredentials.provider = provider
	processTunnelCredentials.Unlock()
}

func processTunnelCredentialProvider() TunnelCredentialProvider {
	processTunnelCredentials.RLock()
	provider := processTunnelCredentials.provider
	processTunnelCredentials.RUnlock()
	return provider
}

func tunnelCredentialProvider(provider TLSMaterialProvider) (TunnelCredentialProvider, error) {
	tunnel, ok := provider.(TunnelCredentialProvider)
	if !ok || tunnel == nil {
		tunnel = processTunnelCredentialProvider()
	}
	if tunnel == nil {
		return nil, errors.New("tunnel credential provider is required for pki_mtls")
	}
	return tunnel, nil
}

func serverTunnelTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener) (*tls.Config, error) {
	tunnel, err := tunnelCredentialProvider(provider)
	if err != nil {
		return nil, err
	}
	security, err := tunnel.LoadTunnelSecurity(ctx)
	if err != nil {
		return nil, fmt.Errorf("load tunnel security state: %w", err)
	}
	if err := validateTunnelSecurityState(security); err != nil {
		return nil, err
	}
	storageIdentity := relayListenerStorageIdentity(listener.ID)
	metadata, err := validateInstalledTunnelCertificate(ctx, tunnel, storageIdentity, model.PKICertificatePurposeServer, security, &listener)
	if err != nil {
		return nil, err
	}
	if err := validateTunnelListenerMetadata(metadata, listener); err != nil {
		return nil, err
	}
	clientCAs, _, err := tunnelTrustPool(security.Snapshot)
	if err != nil {
		return nil, err
	}

	config := &tls.Config{
		MinVersion:                  tls.VersionTLS13,
		ClientAuth:                  tls.RequireAnyClientCert,
		ClientCAs:                   clientCAs,
		SessionTicketsDisabled:      true,
		DynamicRecordSizingDisabled: true,
	}
	config.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		certificateCtx := context.Background()
		if hello != nil {
			certificateCtx = hello.Context()
		}
		return loadTunnelServerCertificate(certificateCtx, tunnel, storageIdentity)
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		current, err := tunnel.LoadTunnelSecurity(context.Background())
		if err != nil {
			return fmt.Errorf("load current tunnel security state: %w", err)
		}
		_, err = verifyTunnelPeer(state.PeerCertificates, current, tunnelPeerExpectation{
			purpose: model.PKICertificatePurposeClient,
			domain:  current.Snapshot.PKIDomainID,
		})
		return err
	}
	return config, nil
}

func clientTunnelTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener, address, serverNameOverride string) (*tls.Config, error) {
	tunnel, err := tunnelCredentialProvider(provider)
	if err != nil {
		return nil, err
	}
	security, err := tunnel.LoadTunnelSecurity(ctx)
	if err != nil {
		return nil, fmt.Errorf("load tunnel security state: %w", err)
	}
	if err := validateTunnelSecurityState(security); err != nil {
		return nil, err
	}
	metadata, err := validateInstalledTunnelCertificate(ctx, tunnel, AgentTunnelCredentialIdentity, model.PKICertificatePurposeClient, security, nil)
	if err != nil {
		return nil, err
	}
	if metadata.ListenerID != "" || strings.TrimSpace(metadata.AgentID) == "" {
		return nil, errors.New("active tunnel client credential is not an agent identity")
	}
	serverName, err := verificationServerName(address, serverNameOverride)
	if err != nil {
		return nil, err
	}

	config := &tls.Config{
		// codeql[go/disabled-certificate-check]
		// pki_mtls performs chain, time, EKU, SAN, URI, revocation, and
		// generation verification below against the signed security owner.
		InsecureSkipVerify:          true,
		MinVersion:                  tls.VersionTLS13,
		ServerName:                  serverName,
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
			return fmt.Errorf("load current tunnel security state: %w", err)
		}
		_, err = verifyTunnelPeer(state.PeerCertificates, current, tunnelPeerExpectation{
			purpose:            model.PKICertificatePurposeServer,
			domain:             current.Snapshot.PKIDomainID,
			agentID:            strings.TrimSpace(listener.AgentID),
			listenerID:         strconv.Itoa(listener.ID),
			identityID:         strings.TrimSpace(listener.PKIIdentityID),
			verificationName:   serverName,
			expectedCredential: strings.TrimSpace(listener.PKICertificateID),
		})
		return err
	}
	return config, nil
}

func validateInstalledTunnelCertificate(
	ctx context.Context,
	provider TunnelCredentialProvider,
	storageIdentity string,
	purpose string,
	security TunnelSecurityState,
	listener *Listener,
) (TunnelCredentialMetadata, error) {
	temporary := &tls.Config{MinVersion: tls.VersionTLS13}
	metadata, err := provider.InstallTunnelCertificate(ctx, storageIdentity, temporary)
	if err != nil {
		return TunnelCredentialMetadata{}, fmt.Errorf("install tunnel credential %q: %w", storageIdentity, err)
	}
	if purpose == model.PKICertificatePurposeClient && temporary.GetClientCertificate == nil && len(temporary.Certificates) == 0 {
		return TunnelCredentialMetadata{}, errors.New("tunnel client credential callback is unavailable")
	}
	if purpose == model.PKICertificatePurposeServer && temporary.GetCertificate == nil && len(temporary.Certificates) == 0 {
		return TunnelCredentialMetadata{}, errors.New("tunnel server credential callback is unavailable")
	}
	if err := validateTunnelCredentialMetadata(metadata, security, purpose); err != nil {
		return TunnelCredentialMetadata{}, err
	}
	if listener != nil {
		if err := validateTunnelListenerMetadata(metadata, *listener); err != nil {
			return TunnelCredentialMetadata{}, err
		}
	}
	return metadata, nil
}

func loadTunnelServerCertificate(ctx context.Context, provider TunnelCredentialProvider, storageIdentity string) (*tls.Certificate, error) {
	temporary := &tls.Config{MinVersion: tls.VersionTLS13}
	if _, err := provider.InstallTunnelCertificate(ctx, storageIdentity, temporary); err != nil {
		return nil, err
	}
	if temporary.GetCertificate != nil {
		return temporary.GetCertificate(nil)
	}
	if len(temporary.Certificates) == 0 {
		return nil, errors.New("tunnel server certificate is unavailable")
	}
	certificate := temporary.Certificates[0]
	return &certificate, nil
}

func loadTunnelClientCertificate(ctx context.Context, provider TunnelCredentialProvider, storageIdentity string) (*tls.Certificate, error) {
	temporary := &tls.Config{MinVersion: tls.VersionTLS13}
	if _, err := provider.InstallTunnelCertificate(ctx, storageIdentity, temporary); err != nil {
		return nil, err
	}
	if temporary.GetClientCertificate != nil {
		return temporary.GetClientCertificate(nil)
	}
	if len(temporary.Certificates) == 0 {
		return nil, errors.New("tunnel client certificate is unavailable")
	}
	certificate := temporary.Certificates[0]
	return &certificate, nil
}

func validateTunnelCredentialMetadata(metadata TunnelCredentialMetadata, security TunnelSecurityState, purpose string) error {
	metadata.CredentialFingerprintSHA256 = normalizeTunnelFingerprint(metadata.CredentialFingerprintSHA256)
	if strings.TrimSpace(metadata.Generation) == "" || len(metadata.CredentialFingerprintSHA256) != sha256.Size*2 ||
		strings.TrimSpace(metadata.IdentityID) == "" || strings.TrimSpace(metadata.CertificateID) == "" ||
		metadata.Purpose != purpose || metadata.PKIDomainID != security.Snapshot.PKIDomainID ||
		metadata.PKIEpoch > security.Snapshot.PKIEpoch || metadata.CAGeneration <= 0 {
		return errors.New("active tunnel credential metadata is incomplete or inconsistent")
	}
	if metadata.PKIEpoch == security.Snapshot.PKIEpoch && metadata.SecurityRevision > security.Snapshot.SecurityRevision {
		return errors.New("active tunnel credential is ahead of tunnel security state")
	}
	if slices.Contains(security.Snapshot.RevokedIdentityIDs, metadata.IdentityID) {
		return errors.New("active tunnel identity is revoked")
	}
	root, ok := tunnelTrustRootByGeneration(security.Snapshot, metadata.CAGeneration)
	if !ok || root.AuthorityID != metadata.AuthorityID || !tunnelTrustRootUsable(root.Status) {
		return errors.New("active tunnel credential CA generation is not usable")
	}
	return nil
}

func validateTunnelListenerMetadata(metadata TunnelCredentialMetadata, listener Listener) error {
	if metadata.Purpose != model.PKICertificatePurposeServer || metadata.AgentID != strings.TrimSpace(listener.AgentID) ||
		metadata.ListenerID != strconv.Itoa(listener.ID) {
		return errors.New("active tunnel listener credential owner is inconsistent")
	}
	if expected := strings.TrimSpace(listener.PKIIdentityID); expected != "" && metadata.IdentityID != expected {
		return errors.New("active tunnel listener identity differs from the control snapshot")
	}
	return nil
}

func validateTunnelSecurityState(security TunnelSecurityState) error {
	snapshot := security.Snapshot
	if strings.TrimSpace(security.Hash) == "" || strings.TrimSpace(snapshot.PKIDomainID) == "" ||
		snapshot.PKIEpoch < 0 || snapshot.SecurityRevision < 0 || len(snapshot.TrustRoots) == 0 {
		return errors.New("tunnel security state is incomplete")
	}
	_, _, err := tunnelTrustPool(snapshot)
	return err
}

type tunnelPeerExpectation struct {
	purpose            string
	domain             string
	agentID            string
	listenerID         string
	identityID         string
	verificationName   string
	expectedCredential string
}

type tunnelPeerIdentity struct {
	AgentID      string
	ListenerID   string
	SerialHex    string
	CAGeneration int64
}

func verifyTunnelPeer(peerCertificates []*x509.Certificate, security TunnelSecurityState, expected tunnelPeerExpectation) (tunnelPeerIdentity, error) {
	if err := validateTunnelSecurityState(security); err != nil {
		return tunnelPeerIdentity{}, err
	}
	if len(peerCertificates) == 0 || peerCertificates[0] == nil {
		return tunnelPeerIdentity{}, errors.New("tunnel peer certificate is required")
	}
	leaf := peerCertificates[0]
	if leaf.IsCA || !leaf.BasicConstraintsValid || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return tunnelPeerIdentity{}, errors.New("tunnel peer certificate profile is invalid")
	}
	expectedUsage := x509.ExtKeyUsageClientAuth
	if expected.purpose == model.PKICertificatePurposeServer {
		expectedUsage = x509.ExtKeyUsageServerAuth
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != expectedUsage || len(leaf.UnknownExtKeyUsage) != 0 {
		return tunnelPeerIdentity{}, errors.New("tunnel peer certificate EKU is invalid")
	}

	roots, rootsByFingerprint, err := tunnelTrustPool(security.Snapshot)
	if err != nil {
		return tunnelPeerIdentity{}, err
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range peerCertificates[1:] {
		if certificate != nil {
			intermediates.AddCert(certificate)
		}
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       expected.verificationName,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{expectedUsage},
	})
	if err != nil {
		return tunnelPeerIdentity{}, fmt.Errorf("tunnel peer chain verification failed: %w", err)
	}
	trustedRoots := make(map[string]model.PKITrustRoot)
	for _, chain := range chains {
		if len(chain) < 2 {
			return tunnelPeerIdentity{}, errors.New("tunnel peer did not present a CA-issued chain")
		}
		root := chain[len(chain)-1]
		digest := sha256.Sum256(root.Raw)
		fingerprint := hex.EncodeToString(digest[:])
		descriptor, ok := rootsByFingerprint[fingerprint]
		if !ok {
			return tunnelPeerIdentity{}, errors.New("tunnel peer chain terminates at an unknown CA generation")
		}
		trustedRoots[fingerprint] = descriptor
	}
	if len(trustedRoots) != 1 {
		return tunnelPeerIdentity{}, errors.New("tunnel peer has multiple trusted CA chains")
	}
	var trustRoot model.PKITrustRoot
	for _, root := range trustedRoots {
		trustRoot = root
	}

	serial := strings.ToLower(leaf.SerialNumber.Text(16))
	if slices.Contains(security.Snapshot.RevokedSerials, serial) {
		return tunnelPeerIdentity{}, errors.New("tunnel peer certificate is revoked")
	}
	if expected.identityID != "" && slices.Contains(security.Snapshot.RevokedIdentityIDs, expected.identityID) {
		return tunnelPeerIdentity{}, errors.New("tunnel peer identity is revoked")
	}
	agentID, listenerID, err := parseTunnelIdentityURI(leaf, expected.domain)
	if err != nil {
		return tunnelPeerIdentity{}, err
	}
	if expected.purpose == model.PKICertificatePurposeClient {
		if listenerID != "" {
			return tunnelPeerIdentity{}, errors.New("tunnel client certificate contains a listener identity")
		}
		if expected.agentID != "" && agentID != expected.agentID {
			return tunnelPeerIdentity{}, errors.New("tunnel client URI identity does not match the expected agent")
		}
	} else if agentID != expected.agentID || listenerID != expected.listenerID {
		return tunnelPeerIdentity{}, errors.New("tunnel server URI identity does not match the relay listener")
	}

	return tunnelPeerIdentity{AgentID: agentID, ListenerID: listenerID, SerialHex: serial, CAGeneration: trustRoot.Generation}, nil
}

func parseTunnelIdentityURI(certificate *x509.Certificate, domain string) (string, string, error) {
	if certificate == nil || len(certificate.URIs) != 1 || certificate.URIs[0] == nil {
		return "", "", errors.New("tunnel peer requires exactly one URI identity")
	}
	identity := certificate.URIs[0]
	if identity.Scheme != "spiffe" || identity.Host != domain || identity.RawQuery != "" || identity.Fragment != "" || identity.User != nil {
		return "", "", errors.New("tunnel peer PKI domain URI identity is invalid")
	}
	parts := strings.Split(strings.TrimPrefix(identity.EscapedPath(), "/"), "/")
	if len(parts) != 2 && len(parts) != 4 {
		return "", "", errors.New("tunnel peer URI identity shape is invalid")
	}
	if parts[0] != "agent" || parts[1] == "" {
		return "", "", errors.New("tunnel peer agent URI identity is invalid")
	}
	agentID, err := url.PathUnescape(parts[1])
	if err != nil || url.PathEscape(agentID) != parts[1] {
		return "", "", errors.New("tunnel peer agent URI identity is not canonical")
	}
	if len(parts) == 2 {
		return agentID, "", nil
	}
	if parts[2] != "listener" || parts[3] == "" {
		return "", "", errors.New("tunnel peer listener URI identity is invalid")
	}
	listenerID, err := url.PathUnescape(parts[3])
	if err != nil || url.PathEscape(listenerID) != parts[3] {
		return "", "", errors.New("tunnel peer listener URI identity is not canonical")
	}
	return agentID, listenerID, nil
}

func tunnelTrustPool(snapshot model.PKISecuritySnapshot) (*x509.CertPool, map[string]model.PKITrustRoot, error) {
	pool := x509.NewCertPool()
	byFingerprint := make(map[string]model.PKITrustRoot)
	for _, descriptor := range snapshot.TrustRoots {
		if !tunnelTrustRootUsable(descriptor.Status) {
			continue
		}
		certificate, err := parseFirstCertificatePEM(descriptor.CertificatePEM)
		if err != nil || !certificate.IsCA {
			return nil, nil, errors.New("tunnel trust root certificate is invalid")
		}
		digest := sha256.Sum256(certificate.Raw)
		fingerprint := hex.EncodeToString(digest[:])
		if fingerprint != normalizeTunnelFingerprint(descriptor.FingerprintSHA256) || descriptor.Generation <= 0 || strings.TrimSpace(descriptor.AuthorityID) == "" {
			return nil, nil, errors.New("tunnel trust root metadata is inconsistent")
		}
		if _, duplicate := byFingerprint[fingerprint]; duplicate {
			return nil, nil, errors.New("tunnel trust root is duplicated")
		}
		pool.AddCert(certificate)
		byFingerprint[fingerprint] = descriptor
	}
	if len(byFingerprint) == 0 {
		return nil, nil, errors.New("tunnel security state has no usable trust root")
	}
	return pool, byFingerprint, nil
}

func tunnelTrustRootByGeneration(snapshot model.PKISecuritySnapshot, generation int64) (model.PKITrustRoot, bool) {
	for _, root := range snapshot.TrustRoots {
		if root.Generation == generation {
			return root, true
		}
	}
	return model.PKITrustRoot{}, false
}

func tunnelTrustRootUsable(status string) bool {
	return status == "active" || status == "prepared" || status == "retiring"
}

func parseFirstCertificatePEM(encoded string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(encoded)))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certificate PEM is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate, nil
}

func tunnelPoolSecurityBinding(ctx context.Context, provider TLSMaterialProvider, listener Listener) (string, error) {
	mode, err := normalizeTLSMode(listener.TLSMode)
	if err != nil {
		return "", err
	}
	if mode != tlsModePKIMTLS {
		return "", nil
	}
	tunnel, err := tunnelCredentialProvider(provider)
	if err != nil {
		return "", err
	}
	security, err := tunnel.LoadTunnelSecurity(ctx)
	if err != nil {
		return "", err
	}
	if err := validateTunnelSecurityState(security); err != nil {
		return "", err
	}
	credential, err := tunnel.LoadTunnelCredential(ctx, AgentTunnelCredentialIdentity)
	if err != nil {
		return "", err
	}
	if err := validateTunnelCredentialMetadata(credential, security, model.PKICertificatePurposeClient); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%d|%s|%s|%s|%s|%s|%s|%s",
		security.Snapshot.PKIDomainID,
		security.Snapshot.PKIEpoch,
		security.Snapshot.SecurityRevision,
		strings.TrimSpace(security.Hash),
		strings.TrimSpace(credential.Generation),
		normalizeTunnelFingerprint(credential.CredentialFingerprintSHA256),
		strings.TrimSpace(credential.IdentityID),
		strings.TrimSpace(listener.PKIIdentityID),
		strings.TrimSpace(listener.PKICertificateID),
		strings.TrimSpace(listener.AgentID),
	), nil
}

func bindTunnelSecurityToHop(ctx context.Context, provider TLSMaterialProvider, hop Hop) (Hop, error) {
	binding, err := tunnelPoolSecurityBinding(ctx, provider, hop.Listener)
	if err != nil {
		return Hop{}, err
	}
	hop.securityBinding = binding
	return hop, nil
}

func bindTunnelSecurityToFirstHop(ctx context.Context, provider TLSMaterialProvider, chain []Hop) ([]Hop, error) {
	if len(chain) == 0 {
		return chain, nil
	}
	bound, err := bindTunnelSecurityToHop(ctx, provider, chain[0])
	if err != nil {
		return nil, err
	}
	if bound.securityBinding == chain[0].securityBinding {
		return chain, nil
	}
	cloned := append([]Hop(nil), chain...)
	cloned[0] = bound
	return cloned, nil
}

func cloneTunnelSecurityState(source TunnelSecurityState) TunnelSecurityState {
	cloned := source
	cloned.Snapshot.TrustRoots = slices.Clone(source.Snapshot.TrustRoots)
	cloned.Snapshot.RevokedIdentityIDs = slices.Clone(source.Snapshot.RevokedIdentityIDs)
	cloned.Snapshot.RevokedSerials = slices.Clone(source.Snapshot.RevokedSerials)
	cloned.Snapshot.Signature = slices.Clone(source.Snapshot.Signature)
	return cloned
}

func tunnelSecurityRequiresFence(previous, next TunnelSecurityState) bool {
	if previous.Snapshot.PKIDomainID == "" {
		return false
	}
	if previous.Snapshot.PKIDomainID != next.Snapshot.PKIDomainID || previous.Snapshot.PKIEpoch != next.Snapshot.PKIEpoch ||
		next.Snapshot.SecurityRevision < previous.Snapshot.SecurityRevision {
		return true
	}
	if containsNewTunnelSecurityValue(previous.Snapshot.RevokedIdentityIDs, next.Snapshot.RevokedIdentityIDs) ||
		containsNewTunnelSecurityValue(previous.Snapshot.RevokedSerials, next.Snapshot.RevokedSerials) {
		return true
	}
	nextRoots := make(map[int64]model.PKITrustRoot, len(next.Snapshot.TrustRoots))
	for _, root := range next.Snapshot.TrustRoots {
		nextRoots[root.Generation] = root
	}
	for _, root := range previous.Snapshot.TrustRoots {
		if !tunnelTrustRootUsable(root.Status) {
			continue
		}
		candidate, ok := nextRoots[root.Generation]
		if !ok || candidate.AuthorityID != root.AuthorityID ||
			normalizeTunnelFingerprint(candidate.FingerprintSHA256) != normalizeTunnelFingerprint(root.FingerprintSHA256) ||
			!tunnelTrustRootUsable(candidate.Status) {
			return true
		}
	}
	return false
}

func containsNewTunnelSecurityValue(previous, next []string) bool {
	known := make(map[string]struct{}, len(previous))
	for _, value := range previous {
		known[value] = struct{}{}
	}
	for _, value := range next {
		if _, ok := known[value]; !ok {
			return true
		}
	}
	return false
}
