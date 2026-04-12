package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func generateRelayLeafMaterial(domain string, caBundle storage.ManagedCertificateBundle, aliases ...string) (storage.ManagedCertificateBundle, error) {
	caCert, caKey, err := parseCertificateSigner(caBundle.CertPEM, caBundle.KeyPEM)
	if err != nil {
		return storage.ManagedCertificateBundle{}, err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	now := time.Now().UTC()
	dnsNames, ipAddresses := relayLeafSANs(domain, aliases...)
	template := &x509.Certificate{
		SerialNumber: randomCertificateSerial(),
		Subject:      pkix.Name{CommonName: relayLeafCommonName(domain)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	return storage.ManagedCertificateBundle{
		Domain:  strings.TrimSpace(domain),
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})),
	}, nil
}

func relayLeafSANs(domain string, aliases ...string) ([]string, []net.IP) {
	dnsNames := make([]string, 0, 1+len(aliases))
	ipAddresses := make([]net.IP, 0, 1+len(aliases))
	seenDNS := make(map[string]struct{}, 1+len(aliases))
	seenIPs := make(map[string]struct{}, 1+len(aliases))

	addHost := func(raw string) {
		host := strings.TrimSpace(raw)
		if host == "" {
			return
		}
		if ip := net.ParseIP(host); ip != nil {
			key := ip.String()
			if _, ok := seenIPs[key]; ok {
				return
			}
			seenIPs[key] = struct{}{}
			ipAddresses = append(ipAddresses, ip)
			return
		}
		key := strings.ToLower(host)
		if _, ok := seenDNS[key]; ok {
			return
		}
		seenDNS[key] = struct{}{}
		dnsNames = append(dnsNames, host)
	}

	addHost(domain)
	for _, alias := range aliases {
		addHost(alias)
	}
	return dnsNames, ipAddresses
}

func generateInternalCAMaterial(domain string) (storage.ManagedCertificateBundle, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	commonName := strings.TrimSpace(domain)
	if commonName == "" {
		commonName = fmt.Sprintf("internal-ca-%d", time.Now().UTC().UnixNano())
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          randomCertificateSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(3650 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	return storage.ManagedCertificateBundle{
		Domain:  strings.TrimSpace(domain),
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})),
	}, nil
}

func parseCertificateSigner(certPEM string, keyPEM string) (*x509.Certificate, crypto.Signer, error) {
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		return nil, nil, fmt.Errorf("%w: invalid relay ca certificate PEM", ErrInvalidArgument)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("%w: invalid relay ca key PEM", ErrInvalidArgument)
	}
	if key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); err == nil {
		return cert, key, nil
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err == nil {
		signer, ok := parsed.(crypto.Signer)
		if !ok {
			return nil, nil, fmt.Errorf("%w: relay ca key is not a signing key", ErrInvalidArgument)
		}
		return cert, signer, nil
	}
	return nil, nil, fmt.Errorf("%w: unsupported relay ca key PEM", ErrInvalidArgument)
}

func loadManagedCertificateMaterial(ctx context.Context, store storage.Store, domain string, pending []storage.ManagedCertificateBundle) (storage.ManagedCertificateBundle, bool, error) {
	for _, bundle := range pending {
		if strings.EqualFold(strings.TrimSpace(bundle.Domain), strings.TrimSpace(domain)) {
			return bundle, true, nil
		}
	}
	return store.LoadManagedCertificateMaterial(ctx, domain)
}

func deriveRelayPinSetFromCertificate(certPEM string) ([]RelayPin, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, fmt.Errorf("%w: invalid certificate PEM", ErrInvalidArgument)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	spkiDER, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(spkiDER)
	return []RelayPin{{
		Type:  "spki_sha256",
		Value: base64.StdEncoding.EncodeToString(sum[:]),
	}}, nil
}

func certificateChainUsesRelayCA(material storage.ManagedCertificateBundle, relayCA storage.ManagedCertificateBundle) bool {
	chain, err := parseCertificateChain(material.CertPEM)
	if err != nil || len(chain) == 0 {
		return false
	}
	relayChain, err := parseCertificateChain(relayCA.CertPEM)
	if err != nil || len(relayChain) == 0 {
		return false
	}
	relayRoot := relayChain[0]
	if len(chain) == 1 {
		return chain[0].CheckSignatureFrom(relayRoot) == nil
	}
	if !chain[len(chain)-1].Equal(relayRoot) {
		return false
	}
	for index := 0; index < len(chain)-1; index++ {
		if chain[index].CheckSignatureFrom(chain[index+1]) != nil {
			return false
		}
	}
	return true
}

func parseCertificateChain(certPEM string) ([]*x509.Certificate, error) {
	rest := []byte(certPEM)
	certificates := make([]*x509.Certificate, 0)
	for {
		rest = bytesTrimSpace(rest)
		if len(rest) == 0 {
			return certificates, nil
		}
		block, next := pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("%w: invalid certificate PEM", ErrInvalidArgument)
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: expected CERTIFICATE PEM block", ErrInvalidArgument)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, cert)
		rest = next
	}
}

func hashManagedCertificateMaterial(certPEM string, keyPEM string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n---\n%s", certPEM, keyPEM)))
	return fmt.Sprintf("%x", sum[:])
}

func randomCertificateSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}

func bytesTrimSpace(raw []byte) []byte {
	start := 0
	for start < len(raw) && (raw[start] == ' ' || raw[start] == '\n' || raw[start] == '\r' || raw[start] == '\t') {
		start++
	}
	end := len(raw)
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\n' || raw[end-1] == '\r' || raw[end-1] == '\t') {
		end--
	}
	return raw[start:end]
}

func relayLeafCommonName(domain string) string {
	trimmed := strings.TrimSpace(domain)
	if trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("relay-listener-%d", time.Now().UnixNano())
}
