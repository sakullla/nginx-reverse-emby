package rpc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	guestEndpointDirectory   = "/run/nre-plugin"
	guestCredentialDirectory = "/run/nre-plugin-credentials"
)

type attemptSecurity struct {
	dial                DialConfig
	endpointDirectory   string
	credentialDirectory string
	guestEndpoint       string
	environment         []string
	cleanup             func() error
}

func provisionAttemptSecurity(runtimeDirectory string, dial DialConfig) (attemptSecurity, error) {
	runtimeDirectory, err := filepath.Abs(runtimeDirectory)
	if err != nil || strings.TrimSpace(runtimeDirectory) == "" {
		return attemptSecurity{}, errors.New("RPC plugin attempt requires a managed runtime directory")
	}
	root, err := os.MkdirTemp(runtimeDirectory, ".rpc-attempt-")
	if err != nil {
		return attemptSecurity{}, err
	}
	failed := true
	defer func() {
		if failed {
			_ = cleanupAttemptDirectory(runtimeDirectory, root)
		}
	}()
	if err := os.Chmod(root, 0o700); err != nil {
		return attemptSecurity{}, err
	}
	endpointDirectory := filepath.Join(root, "endpoint")
	credentialDirectory := filepath.Join(root, "credentials")
	if err := os.Mkdir(endpointDirectory, 0o700); err != nil {
		return attemptSecurity{}, err
	}
	if err := os.Mkdir(credentialDirectory, 0o700); err != nil {
		return attemptSecurity{}, err
	}
	cookieBytes := make([]byte, 32)
	if _, err := rand.Read(cookieBytes); err != nil {
		return attemptSecurity{}, err
	}
	cookie := hex.EncodeToString(cookieBytes)
	cookieFile := filepath.Join(credentialDirectory, "cookie")
	if err := os.WriteFile(cookieFile, []byte(cookie), 0o600); err != nil {
		return attemptSecurity{}, err
	}
	dial.Cookie = cookie
	environment := []string{"NRE_PLUGIN_COOKIE_FILE=" + cookieFile}
	guestEndpoint := ""
	if strings.EqualFold(dial.Network, "unix") {
		dial.Address = filepath.Join(endpointDirectory, "rpc.sock")
		if runtime.GOOS != "windows" && len(dial.Address) >= 104 {
			return attemptSecurity{}, errors.New("RPC plugin managed unix endpoint path is too long")
		}
		dial.RuntimeRoot = endpointDirectory
		guestEndpoint = guestEndpointDirectory + "/rpc.sock"
		environment = append(environment, "NRE_PLUGIN_ENDPOINT=unix:"+dial.Address)
	} else {
		environment = append(environment, "NRE_PLUGIN_ENDPOINT="+dial.Network+":"+dial.Address)
	}
	if strings.EqualFold(dial.Network, "tcp") {
		clientTLS, tlsEnvironment, err := writeAttemptTLS(credentialDirectory)
		if err != nil {
			return attemptSecurity{}, err
		}
		dial.TLSConfig = clientTLS
		environment = append(environment, tlsEnvironment...)
	}
	failed = false
	return attemptSecurity{dial: dial, endpointDirectory: endpointDirectory, credentialDirectory: credentialDirectory, guestEndpoint: guestEndpoint, environment: environment, cleanup: func() error { return cleanupAttemptDirectory(runtimeDirectory, root) }}, nil
}

func cleanupAttemptDirectory(runtimeDirectory, root string) error {
	runtimeDirectory, err := filepath.Abs(runtimeDirectory)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(runtimeDirectory, root)
	if err != nil || filepath.Dir(relative) != "." || !strings.HasPrefix(filepath.Base(relative), ".rpc-attempt-") {
		return errors.New("refusing to clean RPC attempt outside managed runtime directory")
	}
	var removeErr error
	for attempt := 0; attempt < 5; attempt++ {
		if removeErr = os.RemoveAll(root); removeErr == nil {
			return nil
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
		}
	}
	return removeErr
}

func writeAttemptTLS(directory string) (*tls.Config, []string, error) {
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	caTemplate := &x509.Certificate{SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "nre-plugin-attempt-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}
	serverCert, serverKey, err := signedAttemptCertificate(caCert, caKey, "nre-plugin", true, now)
	if err != nil {
		return nil, nil, err
	}
	clientCert, clientKey, err := signedAttemptCertificate(caCert, caKey, "nre-host", false, now)
	if err != nil {
		return nil, nil, err
	}
	caPath := filepath.Join(directory, "ca.crt")
	serverCertPath := filepath.Join(directory, "server.crt")
	serverKeyPath := filepath.Join(directory, "server.key")
	if err := writePEM(caPath, "CERTIFICATE", caDER, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(serverCertPath, "CERTIFICATE", serverCert, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(serverKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey), 0o600); err != nil {
		return nil, nil, err
	}
	clientPair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}))
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	config := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "nre-plugin", Certificates: []tls.Certificate{clientPair}, RootCAs: roots}
	environment := []string{"NRE_PLUGIN_TLS_CA_FILE=" + caPath, "NRE_PLUGIN_TLS_CERT_FILE=" + serverCertPath, "NRE_PLUGIN_TLS_KEY_FILE=" + serverKeyPath}
	return config, environment, nil
}

func signedAttemptCertificate(ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, server bool, now time.Time) ([]byte, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	usage := x509.ExtKeyUsageClientAuth
	template := &x509.Certificate{SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: commonName}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"nre-plugin"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	return der, key, err
}

func randomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), mode)
}

func replaceGeneratedEnvironment(current, replacement []string) []string {
	result := make([]string, 0, len(current)+len(replacement))
	for _, entry := range current {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "NRE_PLUGIN_") {
			result = append(result, entry)
		}
	}
	return append(result, replacement...)
}
