package pluginhost

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	controlGuestEndpointDirectory   = "/run/nre-plugin"
	controlGuestCredentialDirectory = "/run/nre-plugin-credentials"
)

type controlAttemptSecurity struct {
	endpoint            Endpoint
	endpointDirectory   string
	credentialDirectory string
	guestEndpoint       string
	environment         []string
	sandboxUID          int
	cleanup             func() error
}

type controlAttemptSecurityOps struct {
	writeTLS func(string) (*tls.Config, []string, error)
	cleanup  func(string, string) error
}

func provisionControlAttemptSecurity(runtimeDirectory string, endpoint Endpoint) (controlAttemptSecurity, error) {
	return provisionControlAttemptSecurityWithOps(runtimeDirectory, endpoint, controlAttemptSecurityOps{})
}

func provisionControlAttemptSecurityWithOps(runtimeDirectory string, endpoint Endpoint, ops controlAttemptSecurityOps) (controlAttemptSecurity, error) {
	runtimeDirectory, err := filepath.Abs(runtimeDirectory)
	if err != nil || strings.TrimSpace(runtimeDirectory) == "" {
		return controlAttemptSecurity{}, errors.New("control-plane plugin attempt requires a managed runtime directory")
	}
	root, err := os.MkdirTemp(runtimeDirectory, ".p-")
	if err != nil {
		return controlAttemptSecurity{}, err
	}
	cleanup := ops.cleanup
	if cleanup == nil {
		cleanup = cleanupControlAttemptDirectory
	}
	sandboxUID, releaseSandboxUID, err := allocateAttemptSandboxUID()
	if err != nil {
		_ = cleanup(runtimeDirectory, root)
		return controlAttemptSecurity{}, err
	}
	var endpointHandle *os.File
	var cleanupMu sync.Mutex
	security := controlAttemptSecurity{endpoint: endpoint, cleanup: func() error {
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		var closeErr error
		if endpointHandle != nil {
			closeErr = endpointHandle.Close()
			endpointHandle = nil
		}
		cleanupErr := cleanup(runtimeDirectory, root)
		if cleanupErr == nil {
			releaseSandboxUID()
		}
		return errors.Join(closeErr, cleanupErr)
	}}
	security.sandboxUID = sandboxUID
	if err := os.Chmod(root, 0o700); err != nil {
		return security, err
	}
	endpointDirectory := filepath.Join(root, "e")
	credentialDirectory := filepath.Join(root, "c")
	security.endpointDirectory = endpointDirectory
	security.credentialDirectory = credentialDirectory
	if err := os.Mkdir(endpointDirectory, 0o700); err != nil {
		return security, err
	}
	if err := os.Mkdir(credentialDirectory, 0o700); err != nil {
		return security, err
	}
	cookieBytes := make([]byte, 32)
	if _, err := rand.Read(cookieBytes); err != nil {
		return security, err
	}
	endpoint.Cookie = hex.EncodeToString(cookieBytes)
	cookieFile := filepath.Join(credentialDirectory, "cookie")
	if err := os.WriteFile(cookieFile, []byte(endpoint.Cookie), 0o600); err != nil {
		return security, err
	}
	environment := []string{"NRE_PLUGIN_COOKIE_FILE=" + cookieFile}
	guestEndpoint := ""
	if strings.EqualFold(endpoint.Network, "unix") {
		socketName := "r-" + endpoint.Cookie[:16] + ".sock"
		endpoint.Address = filepath.Join(endpointDirectory, socketName)
		if runtime.GOOS == "linux" {
			endpointHandle, err = os.Open(endpointDirectory)
			if err != nil {
				return security, err
			}
			endpoint.Address = fmt.Sprintf("/proc/self/fd/%d/%s", endpointHandle.Fd(), socketName)
		} else if runtime.GOOS != "windows" && len(endpoint.Address) >= 104 {
			return security, errors.New("control-plane plugin managed unix endpoint path is too long")
		}
		guestEndpoint = controlGuestEndpointDirectory + "/" + socketName
		environment = append(environment, "NRE_PLUGIN_ENDPOINT=unix:"+endpoint.Address)
	} else {
		environment = append(environment, "NRE_PLUGIN_ENDPOINT="+endpoint.Network+":"+endpoint.Address)
	}
	if strings.EqualFold(endpoint.Network, "tcp") {
		writeTLS := ops.writeTLS
		if writeTLS == nil {
			writeTLS = writeControlAttemptTLS
		}
		clientTLS, tlsEnvironment, err := writeTLS(credentialDirectory)
		if err != nil {
			security.endpoint = endpoint
			security.guestEndpoint = guestEndpoint
			security.environment = environment
			return security, err
		}
		endpoint.TLSConfig = clientTLS
		environment = append(environment, tlsEnvironment...)
	}
	security.endpoint = endpoint
	security.guestEndpoint = guestEndpoint
	security.environment = environment
	if err := ownAttemptSandboxPaths(root, sandboxUID); err != nil {
		return security, err
	}
	return security, nil
}

func cleanupControlAttemptDirectory(runtimeDirectory, root string) error {
	runtimeDirectory, err := filepath.Abs(runtimeDirectory)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(runtimeDirectory, root)
	if err != nil || filepath.Dir(relative) != "." || !strings.HasPrefix(filepath.Base(relative), ".p-") {
		return errors.New("refusing to clean control-plane RPC attempt outside managed runtime directory")
	}
	return os.RemoveAll(root)
}

func writeControlAttemptTLS(directory string) (*tls.Config, []string, error) {
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	caTemplate := &x509.Certificate{SerialNumber: controlRandomSerial(), Subject: pkix.Name{CommonName: "nre-plugin-attempt-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}
	serverCert, serverKey, err := controlSignedAttemptCertificate(caCert, caKey, "nre-plugin", true, now)
	if err != nil {
		return nil, nil, err
	}
	clientCert, clientKey, err := controlSignedAttemptCertificate(caCert, caKey, "nre-host", false, now)
	if err != nil {
		return nil, nil, err
	}
	caPath := filepath.Join(directory, "ca.crt")
	serverCertPath := filepath.Join(directory, "server.crt")
	serverKeyPath := filepath.Join(directory, "server.key")
	if err := controlWritePEM(caPath, "CERTIFICATE", caDER); err != nil {
		return nil, nil, err
	}
	if err := controlWritePEM(serverCertPath, "CERTIFICATE", serverCert); err != nil {
		return nil, nil, err
	}
	if err := controlWritePEM(serverKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)); err != nil {
		return nil, nil, err
	}
	clientPair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}))
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "nre-plugin", Certificates: []tls.Certificate{clientPair}, RootCAs: roots}, []string{"NRE_PLUGIN_TLS_CA_FILE=" + caPath, "NRE_PLUGIN_TLS_CERT_FILE=" + serverCertPath, "NRE_PLUGIN_TLS_KEY_FILE=" + serverKeyPath}, nil
}

func controlSignedAttemptCertificate(ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, server bool, now time.Time) ([]byte, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: controlRandomSerial(), Subject: pkix.Name{CommonName: commonName}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"nre-plugin"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	return der, key, err
}

func controlRandomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}

func controlWritePEM(path, blockType string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600)
}
