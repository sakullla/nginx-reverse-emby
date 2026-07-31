package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKILeaseAuthoritySignerFailsClosedAfterLeaseLoss(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	grant := PKILeaseGrant{
		PKIDomainID: "domain-1", PKIEpoch: 3, InstanceID: "instance-a",
		LeaseDeadline: time.Date(2026, 8, 1, 12, 0, 30, 0, time.UTC),
	}
	gate := &pkiLeaseSignerTestGate{grant: grant}
	delegate := &pkiLeaseSignerTestDelegate{signer: key}
	service, err := NewPKILeaseAuthoritySigner(gate, delegate)
	if err != nil {
		t.Fatalf("NewPKILeaseAuthoritySigner() error = %v", err)
	}
	signer, err := service.LoadSigner(t.Context(), storage.PKIAuthorityRow{PKIDomainID: "domain-1"})
	if err != nil {
		t.Fatalf("LoadSigner() error = %v", err)
	}
	digest := sha256.Sum256([]byte("lease-fenced-signature"))
	if _, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256); err != nil {
		t.Fatalf("Sign(held lease) error = %v", err)
	}

	gate.setError(ErrPKILeaseNotHeld)
	if signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256); !errors.Is(err, ErrPKILeaseNotHeld) || len(signature) != 0 {
		t.Fatalf("Sign(lost lease) = (%d bytes, %v), want empty ErrPKILeaseNotHeld", len(signature), err)
	}
	if _, err := service.LoadSigner(t.Context(), storage.PKIAuthorityRow{PKIDomainID: "domain-1"}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("LoadSigner(lost lease) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if delegate.loadCount() != 1 {
		t.Fatalf("delegate LoadSigner calls = %d, want one", delegate.loadCount())
	}
}

type pkiLeaseSignerTestGate struct {
	mutex sync.Mutex
	grant PKILeaseGrant
	err   error
}

func (g *pkiLeaseSignerTestGate) RequirePKILease(context.Context) (PKILeaseGrant, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.err != nil {
		return PKILeaseGrant{}, g.err
	}
	return g.grant, nil
}

func (g *pkiLeaseSignerTestGate) setError(err error) {
	g.mutex.Lock()
	g.err = err
	g.mutex.Unlock()
}

type pkiLeaseSignerTestDelegate struct {
	mutex  sync.Mutex
	signer crypto.Signer
	loads  int
}

func (d *pkiLeaseSignerTestDelegate) LoadSigner(context.Context, storage.PKIAuthorityRow) (crypto.Signer, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.loads++
	return d.signer, nil
}

func (d *pkiLeaseSignerTestDelegate) loadCount() int {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.loads
}
