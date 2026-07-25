package acmeflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/acme"
)

func TestRecoverOrderCertificateUsesOriginalOrderURL(t *testing.T) {
	primary := errors.New("finalize response omitted order URI")
	client := &fakeOrderCertificateRecoveryClient{}
	client.waitOrderFn = func(_ context.Context, orderURL string) (*acme.Order, error) {
		if orderURL != "https://ca.invalid/order/known" {
			t.Fatalf("WaitOrder() URL = %q", orderURL)
		}
		return &acme.Order{URI: orderURL, Status: acme.StatusValid, CertURL: "https://ca.invalid/cert/1"}, nil
	}
	client.fetchCertFn = func(_ context.Context, certURL string, bundle bool) ([][]byte, error) {
		if certURL != "https://ca.invalid/cert/1" || !bundle {
			t.Fatalf("FetchCert() = URL %q bundle %v", certURL, bundle)
		}
		return [][]byte{[]byte("leaf"), []byte("issuer")}, nil
	}
	chain, certURL, err := recoverOrderCertificate(context.Background(), client, "https://ca.invalid/order/known", true, primary)
	if err != nil {
		t.Fatalf("recoverOrderCertificate() error = %v", err)
	}
	if certURL != "https://ca.invalid/cert/1" || len(chain) != 2 || !bytes.Equal(chain[0], []byte("leaf")) {
		t.Fatalf("recovered certificate = URL %q chain %q", certURL, chain)
	}
}

func TestRecoverOrderCertificatePreservesPrimaryErrorUnlessValidCertificateIsFetched(t *testing.T) {
	primary := errors.New("primary finalize failure")
	fetchFailure := errors.New("fetch failed")
	tests := []struct {
		name  string
		order *acme.Order
		wait  error
		fetch error
	}{
		{name: "wait-failure", wait: errors.New("wait failed")},
		{name: "nil-order"},
		{name: "processing", order: &acme.Order{Status: acme.StatusProcessing, CertURL: "https://ca.invalid/cert/1"}},
		{name: "missing-certificate-url", order: &acme.Order{Status: acme.StatusValid}},
		{name: "fetch-failure", order: &acme.Order{Status: acme.StatusValid, CertURL: "https://ca.invalid/cert/1"}, fetch: fetchFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeOrderCertificateRecoveryClient{
				waitOrderFn: func(context.Context, string) (*acme.Order, error) { return test.order, test.wait },
				fetchCertFn: func(context.Context, string, bool) ([][]byte, error) { return nil, test.fetch },
			}
			chain, certURL, err := recoverOrderCertificate(context.Background(), client, "https://ca.invalid/order/known", true, primary)
			if !errors.Is(err, primary) || chain != nil || certURL != "" {
				t.Fatalf("recoverOrderCertificate() = chain %v URL %q error %v", chain, certURL, err)
			}
		})
	}
}

type fakeOrderCertificateRecoveryClient struct {
	waitOrderFn func(context.Context, string) (*acme.Order, error)
	fetchCertFn func(context.Context, string, bool) ([][]byte, error)
}

func (client *fakeOrderCertificateRecoveryClient) WaitOrder(ctx context.Context, orderURL string) (*acme.Order, error) {
	return client.waitOrderFn(ctx, orderURL)
}

func (client *fakeOrderCertificateRecoveryClient) FetchCert(ctx context.Context, certURL string, bundle bool) ([][]byte, error) {
	return client.fetchCertFn(ctx, certURL, bundle)
}

func TestEngineStandardOrderDoesNotUseProfileStarter(t *testing.T) {
	client := &fakeProtocolClient{}
	client.authorizeOrderFn = func(_ context.Context, identifiers []acme.AuthzID, _ ...acme.OrderOption) (*acme.Order, error) {
		if len(identifiers) != 1 || identifiers[0].Type != "dns" || identifiers[0].Value != "example.com" {
			t.Fatalf("AuthorizeOrder identifiers = %#v", identifiers)
		}
		return &acme.Order{URI: "https://ca.invalid/order/1"}, nil
	}
	profileCalled := false
	starter := DefaultOrderStarter{
		ProfileStarter: OrderStarterFunc(func(context.Context, OrderStartRequest) (*acme.Order, error) {
			profileCalled = true
			return nil, nil
		}),
	}

	order, err := starter.StartOrder(context.Background(), OrderStartRequest{
		Client:      client,
		Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
	})
	if err != nil {
		t.Fatalf("StartOrder() error = %v", err)
	}
	if profileCalled {
		t.Fatal("profile starter was called for an order without a profile")
	}
	if order.URI != "https://ca.invalid/order/1" {
		t.Fatalf("order URI = %q", order.URI)
	}
}

func TestEngineCSRUsesDNSCommonNameButNotIPCommonName(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	dnsCSRDER, err := createCSR(key, []Identifier{{Type: IdentifierDNS, Value: "example.com"}})
	if err != nil {
		t.Fatalf("create DNS CSR: %v", err)
	}
	dnsCSR, err := x509.ParseCertificateRequest(dnsCSRDER)
	if err != nil {
		t.Fatal(err)
	}
	if dnsCSR.Subject.CommonName != "example.com" || len(dnsCSR.DNSNames) != 1 || dnsCSR.DNSNames[0] != "example.com" {
		t.Fatalf("DNS CSR = %#v", dnsCSR)
	}

	ipCSRDER, err := createCSR(key, []Identifier{{Type: IdentifierIP, Value: "192.0.2.30"}})
	if err != nil {
		t.Fatalf("create IP CSR: %v", err)
	}
	ipCSR, err := x509.ParseCertificateRequest(ipCSRDER)
	if err != nil {
		t.Fatal(err)
	}
	if ipCSR.Subject.CommonName != "" {
		t.Fatalf("IP CSR CommonName = %q, want empty", ipCSR.Subject.CommonName)
	}
	if len(ipCSR.IPAddresses) != 1 || !ipCSR.IPAddresses[0].Equal(net.ParseIP("192.0.2.30")) {
		t.Fatalf("IP CSR addresses = %#v", ipCSR.IPAddresses)
	}
}
