package acmeflow

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"net"
	"testing"

	"golang.org/x/crypto/acme"
)

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
