package service

import (
	"context"
	"crypto"
	"fmt"
	"io"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// PKILeaseAuthoritySigner is the lease-enforcing adapter for every control-
// plane CA signer. It checks ownership around vault decryption and around each
// signature, so a signer obtained before a lease loss cannot be used afterward.
type PKILeaseAuthoritySigner struct {
	gate     PKILeaseGate
	delegate PKIEnrollmentAuthoritySigner
}

func NewPKILeaseAuthoritySigner(gate PKILeaseGate, delegate PKIEnrollmentAuthoritySigner) (*PKILeaseAuthoritySigner, error) {
	if gate == nil || delegate == nil {
		return nil, fmt.Errorf("%w: lease gate and authority signer are required", ErrPKILeaseInvalid)
	}
	return &PKILeaseAuthoritySigner{gate: gate, delegate: delegate}, nil
}

func (s *PKILeaseAuthoritySigner) LoadSigner(ctx context.Context, authority storage.PKIAuthorityRow) (crypto.Signer, error) {
	grant, err := s.gate.RequirePKILease(ctx)
	if err != nil {
		return nil, fmt.Errorf("authorize CA key decryption: %w", err)
	}
	if grant.PKIDomainID != authority.PKIDomainID {
		return nil, fmt.Errorf("authorize CA key decryption: %w", ErrPKILeaseNotHeld)
	}
	signer, err := s.delegate.LoadSigner(ctx, authority)
	if err != nil {
		return nil, err
	}
	confirmed, err := s.gate.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(grant, confirmed) {
		if err != nil {
			return nil, fmt.Errorf("recheck CA key decryption lease: %w", err)
		}
		return nil, fmt.Errorf("recheck CA key decryption lease: %w", ErrPKILeaseNotHeld)
	}
	return &pkiLeaseCryptoSigner{gate: s.gate, grant: grant, delegate: signer}, nil
}

type pkiLeaseCryptoSigner struct {
	gate     PKILeaseGate
	grant    PKILeaseGrant
	delegate crypto.Signer
}

func (s *pkiLeaseCryptoSigner) Public() crypto.PublicKey {
	return s.delegate.Public()
}

func (s *pkiLeaseCryptoSigner) Sign(random io.Reader, digest []byte, options crypto.SignerOpts) ([]byte, error) {
	grant, err := s.gate.RequirePKILease(context.Background())
	if err != nil || !samePKILeaseAuthority(s.grant, grant) {
		if err != nil {
			return nil, fmt.Errorf("authorize CA signature: %w", err)
		}
		return nil, fmt.Errorf("authorize CA signature: %w", ErrPKILeaseNotHeld)
	}
	signature, err := s.delegate.Sign(random, digest, options)
	if err != nil {
		return nil, err
	}
	confirmed, leaseErr := s.gate.RequirePKILease(context.Background())
	if leaseErr != nil || !samePKILeaseAuthority(s.grant, confirmed) {
		clear(signature)
		if leaseErr != nil {
			return nil, fmt.Errorf("recheck CA signature lease: %w", leaseErr)
		}
		return nil, fmt.Errorf("recheck CA signature lease: %w", ErrPKILeaseNotHeld)
	}
	return signature, nil
}
