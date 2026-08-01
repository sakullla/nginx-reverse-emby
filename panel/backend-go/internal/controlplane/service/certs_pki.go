package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	PKIUpgradeStateMigrationRequired = "migration_required"
	PKIUpgradeStateTunnelMTLSOnly    = "tunnel_mtls_only"
	defaultPKICALifetime             = 10 * 365 * 24 * time.Hour
	defaultPKIEndpointLifetime       = 90 * 24 * time.Hour
	defaultPKIAuditRetentionDays     = 365
)

type internalPKIBootstrapStore interface {
	PKITransactionStore
	LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
	InspectLegacyPKIMigrationSources(context.Context) (storage.LegacyPKIMigrationSources, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type InternalPKIBootstrapOptions struct {
	Store          internalPKIBootstrapStore
	Vault          *PKIVault
	Lease          *PKILeaseService
	SnapshotSigner PKISecuritySnapshotSigner
	Clock          func() time.Time
	Random         io.Reader
}

type InternalPKIBootstrapResult struct {
	PKIDomainID  string
	PKIEpoch     int64
	UpgradeState string
}

// BootstrapInternalPKI initializes the internal tunnel PKI independently from
// managed/public certificates. Existing legacy relay facts only select the
// maintenance migration state; they are never copied into canonical PKI rows.
func BootstrapInternalPKI(ctx context.Context, options InternalPKIBootstrapOptions) (InternalPKIBootstrapResult, error) {
	if options.Store == nil || options.Vault == nil || options.Lease == nil || options.SnapshotSigner == nil {
		return InternalPKIBootstrapResult{}, fmt.Errorf("%w: PKI bootstrap store, vault, lease, and snapshot signer are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	state, err := options.Store.LoadPKICanonicalState(ctx)
	if err != nil {
		return InternalPKIBootstrapResult{}, err
	}
	var grant PKILeaseGrant
	if state.Settings == nil {
		now := options.Clock().UTC()
		if now.IsZero() {
			return InternalPKIBootstrapResult{}, fmt.Errorf("%w: bootstrap clock returned zero", ErrPKILifecycleInvalid)
		}
		domainID, err := randomPKIIdentifier(options.Random)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		legacy, err := options.Store.InspectLegacyPKIMigrationSources(ctx)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		agents, err := options.Store.ListAgents(ctx)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		upgradeState := PKIUpgradeStateTunnelMTLSOnly
		if len(legacy.ManagedCertificates) != 0 || len(legacy.RelayListeners) != 0 {
			upgradeState = PKIUpgradeStateMigrationRequired
		}
		eventID, err := randomPKIIdentifier(options.Random)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		grant, err = options.Lease.Bootstrap(ctx, domainID, 1, func(initialGrant PKILeaseGrant) error {
			return options.Store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
				if err := tx.CreatePKISettings(ctx, storage.PKISettingsRow{
					PKIDomainID: domainID, CALifetimeSeconds: int64(defaultPKICALifetime / time.Second),
					EndpointLifetimeSeconds: int64(defaultPKIEndpointLifetime / time.Second), AuditRetentionDays: defaultPKIAuditRetentionDays,
					SecurityRevision: 0, PKIEpoch: 1, UpgradeState: upgradeState, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					return err
				}
				if err := tx.CreatePKIInstanceLease(ctx, storage.PKIInstanceLeaseRow{
					PKIDomainID: domainID, PKIEpoch: 1, InstanceID: initialGrant.InstanceID,
					LeaseTerm: initialGrant.LeaseTerm, LeaseDeadline: initialGrant.LeaseDeadline,
					State: storage.PKIInstanceLeaseStateHeld, UpdatedAt: now,
				}); err != nil {
					return err
				}
				if upgradeState == PKIUpgradeStateMigrationRequired {
					for _, agent := range agents {
						identityID, idErr := randomPKIIdentifier(options.Random)
						if idErr != nil {
							return idErr
						}
						if err := tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
							ID: identityID, PKIDomainID: domainID, Kind: storage.PKIIdentityKindAgent, AgentID: agent.ID,
							State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: now, UpdatedAt: now,
						}); err != nil {
							return err
						}
					}
					for _, listener := range legacy.RelayListeners {
						identityID, idErr := randomPKIIdentifier(options.Random)
						if idErr != nil {
							return idErr
						}
						if err := tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
							ID: identityID, PKIDomainID: domainID, Kind: storage.PKIIdentityKindListener,
							AgentID: listener.AgentID, ListenerID: fmt.Sprintf("%d", listener.ID),
							State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: now, UpdatedAt: now,
						}); err != nil {
							return err
						}
					}
				}
				return tx.AppendPKIEvent(ctx, storage.PKIEventRow{
					ID: eventID, PKIDomainID: domainID, Type: "pki.initialized", OccurredAt: now,
					Source: "control_plane", ObjectType: "pki_domain", ObjectID: domainID,
					Result: "success", SecurityRevision: 0,
					DetailsJSON: fmt.Sprintf(`{"upgrade_state":%q}`, upgradeState),
				})
			})
		})
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
	} else {
		grant, err = options.Lease.Acquire(ctx)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
	}
	state, err = options.Store.LoadPKICanonicalState(ctx)
	if err != nil {
		return InternalPKIBootstrapResult{}, err
	}
	if state.Settings == nil || state.Settings.PKIDomainID != grant.PKIDomainID || state.Settings.PKIEpoch != grant.PKIEpoch {
		return InternalPKIBootstrapResult{}, ErrPKILeaseNotHeld
	}
	if len(state.Authorities) == 0 {
		if len(state.Certificates) != 0 || state.SecuritySnapshot != nil {
			return InternalPKIBootstrapResult{}, fmt.Errorf("%w: incomplete bootstrap contains authority-dependent facts", ErrPKILifecycleInvalid)
		}
		generator, err := NewPKIVaultAuthorityGenerator(PKIVaultAuthorityGeneratorOptions{
			Vault: options.Vault, PKIDomainID: state.Settings.PKIDomainID, Clock: options.Clock, Random: options.Random, Lifetime: defaultPKICALifetime,
		})
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		authority, err := generator.GeneratePKIAuthority(ctx, 1, "initial bootstrap")
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		now := options.Clock().UTC()
		err = options.Store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
			if err := requireBootstrapPKILeaseFence(ctx, tx, grant); err != nil {
				return err
			}
			current, err := tx.LoadPKICanonicalState(ctx)
			if err != nil {
				return err
			}
			if len(current.Authorities) != 0 {
				return fmt.Errorf("%w: bootstrap authority changed concurrently", ErrPKILifecycleInvalid)
			}
			keyRef := authority.KeyReference
			if err := tx.CreatePKIAuthority(ctx, storage.PKIAuthorityRow{
				ID: "authority-" + grant.PKIDomainID, PKIDomainID: grant.PKIDomainID, Generation: 1, Status: "active",
				CertificatePEM: authority.CertificatePEM, EncryptedKeyRef: &keyRef,
				FingerprintSHA256: authority.CertificateFingerprint, NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			return requireBootstrapPKILeaseFence(ctx, tx, grant)
		})
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
		state, err = options.Store.LoadPKICanonicalState(ctx)
		if err != nil {
			return InternalPKIBootstrapResult{}, err
		}
	}
	if err := validateBootstrappedInternalPKI(state); err != nil {
		return InternalPKIBootstrapResult{}, err
	}
	if err := ensureBootstrapPKISecuritySnapshot(ctx, options, state, grant); err != nil {
		return InternalPKIBootstrapResult{}, err
	}
	return InternalPKIBootstrapResult{
		PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch, UpgradeState: state.Settings.UpgradeState,
	}, nil
}

func ensureBootstrapPKISecuritySnapshot(ctx context.Context, options InternalPKIBootstrapOptions, state storage.PKICanonicalState, grant PKILeaseGrant) error {
	if state.Settings == nil {
		return nil
	}
	if state.SecuritySnapshot != nil {
		_, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
		return err
	}
	if state.Settings.SecurityRevision != 0 {
		return fmt.Errorf("%w: non-initial security snapshot is missing and requires protected recovery", ErrPKILifecycleInvalid)
	}
	issuedAt := options.Clock().UTC()
	signed, err := options.SnapshotSigner.SignPKISecuritySnapshot(ctx, PKIUnsignedSecuritySnapshot{
		PKIDomainID: state.Settings.PKIDomainID,
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: state.Settings.PKIEpoch, SecurityRevision: state.Settings.SecurityRevision},
			Full:    true,
		},
		IssuedAt:           issuedAt,
		TrustGenerations:   activePKITrustGenerations(state.Authorities),
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{},
	})
	if err != nil {
		return err
	}
	persisted, err := storagePKISecuritySnapshot(state, signed)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		return err
	}
	return options.Store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requireBootstrapPKILeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
			PKIDomainID: persisted.PKIDomainID, PKIEpoch: persisted.PKIEpoch,
			SecurityRevision: persisted.SecurityRevision, SnapshotJSON: string(encoded), UpdatedAt: issuedAt,
		}); err != nil {
			return err
		}
		return requireBootstrapPKILeaseFence(ctx, tx, grant)
	})
}

func requireBootstrapPKILeaseFence(ctx context.Context, tx *storage.PKITransaction, grant PKILeaseGrant) error {
	err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
		PKIDomainID: grant.PKIDomainID, PKIEpoch: grant.PKIEpoch, InstanceID: grant.InstanceID,
		LeaseTerm: grant.LeaseTerm, LeaseDeadline: grant.LeaseDeadline,
	})
	if errors.Is(err, storage.ErrPKILeaseFence) {
		return ErrPKILeaseNotHeld
	}
	return err
}

func activePKITrustGenerations(authorities []storage.PKIAuthorityRow) []int64 {
	result := make([]int64, 0, len(authorities))
	for _, authority := range authorities {
		if authority.Status == "active" || authority.Status == "prepared" || authority.Status == "retiring" {
			result = append(result, authority.Generation)
		}
	}
	slices.Sort(result)
	return result
}

func validateBootstrappedInternalPKI(state storage.PKICanonicalState) error {
	settings := state.Settings
	if settings == nil || strings.TrimSpace(settings.PKIDomainID) == "" || settings.PKIEpoch < 0 ||
		(settings.UpgradeState != PKIUpgradeStateMigrationRequired && settings.UpgradeState != PKIUpgradeStateTunnelMTLSOnly) {
		return fmt.Errorf("%w: persisted PKI settings are invalid", ErrPKILifecycleInvalid)
	}
	active := 0
	for _, authority := range state.Authorities {
		if authority.Status != "active" {
			continue
		}
		active++
		if authority.PKIDomainID != settings.PKIDomainID || authority.Generation <= 0 || authority.EncryptedKeyRef == nil ||
			*authority.EncryptedKeyRef != pkiVaultReference(settings.PKIDomainID, authority.Generation, "ca-signing") {
			return fmt.Errorf("%w: persisted active authority metadata is invalid", ErrPKILifecycleInvalid)
		}
		certificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
		if err != nil || !certificate.IsCA {
			return fmt.Errorf("%w: persisted active authority certificate is invalid", ErrPKILifecycleInvalid)
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		if !strings.EqualFold(authority.FingerprintSHA256, hex.EncodeToString(fingerprint[:])) ||
			!authority.NotBefore.Equal(certificate.NotBefore) || !authority.NotAfter.Equal(certificate.NotAfter) {
			return fmt.Errorf("%w: persisted active authority certificate metadata does not match", ErrPKILifecycleInvalid)
		}
	}
	if active != 1 {
		return fmt.Errorf("%w: persisted PKI must have exactly one active authority", ErrPKILifecycleInvalid)
	}
	return nil
}

type PKIVaultAuthorityGeneratorOptions struct {
	Vault       *PKIVault
	PKIDomainID string
	Clock       func() time.Time
	Random      io.Reader
	Lifetime    time.Duration
}

type PKIVaultAuthorityGenerator struct {
	vault    *PKIVault
	domainID string
	clock    func() time.Time
	random   io.Reader
	lifetime time.Duration
}

func NewPKIVaultAuthorityGenerator(options PKIVaultAuthorityGeneratorOptions) (*PKIVaultAuthorityGenerator, error) {
	if options.Vault == nil || strings.TrimSpace(options.PKIDomainID) == "" {
		return nil, fmt.Errorf("%w: authority vault and PKI domain are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Lifetime == 0 {
		options.Lifetime = defaultPKICALifetime
	}
	if options.Lifetime < 365*24*time.Hour || options.Lifetime > 20*365*24*time.Hour {
		return nil, fmt.Errorf("%w: CA lifetime is outside 1-20 years", ErrPKILifecycleInvalid)
	}
	return &PKIVaultAuthorityGenerator{
		vault: options.Vault, domainID: strings.TrimSpace(options.PKIDomainID), clock: options.Clock,
		random: options.Random, lifetime: options.Lifetime,
	}, nil
}

func (g *PKIVaultAuthorityGenerator) GeneratePKIAuthority(ctx context.Context, generation int64, _ string) (PKIAuthorityMaterial, error) {
	if err := ctx.Err(); err != nil {
		return PKIAuthorityMaterial{}, err
	}
	if generation <= 0 {
		return PKIAuthorityMaterial{}, fmt.Errorf("%w: authority generation must be positive", ErrPKILifecycleInvalid)
	}
	keyReference := pkiVaultReference(g.domainID, generation, "ca-signing")
	var key *ecdsa.PrivateKey
	privateDER, openErr := g.vault.OpenCAKey(keyReference, g.domainID, generation, "ca-signing")
	if openErr == nil {
		parsed, err := x509.ParsePKCS8PrivateKey(privateDER)
		clear(privateDER)
		if err != nil {
			return PKIAuthorityMaterial{}, fmt.Errorf("parse staged PKI authority key: %w", err)
		}
		var ok bool
		key, ok = parsed.(*ecdsa.PrivateKey)
		if !ok || key.Curve != elliptic.P256() {
			return PKIAuthorityMaterial{}, fmt.Errorf("%w: staged PKI authority key is invalid", ErrPKIVaultInvalid)
		}
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return PKIAuthorityMaterial{}, openErr
	}
	generatedKey := key == nil
	if generatedKey {
		var err error
		key, err = ecdsa.GenerateKey(elliptic.P256(), g.random)
		if err != nil {
			return PKIAuthorityMaterial{}, fmt.Errorf("generate PKI authority key: %w", err)
		}
	}
	serialBytes := make([]byte, 16)
	if _, err := io.ReadFull(g.random, serialBytes); err != nil {
		return PKIAuthorityMaterial{}, fmt.Errorf("generate PKI authority serial: %w", err)
	}
	// big.Int consumes an unsigned magnitude here; setting the high bit keeps
	// the serial positive while guaranteeing the required 128-bit entropy span.
	serialBytes[0] |= 0x80
	now := g.clock().UTC()
	notBefore := now.Add(-5 * time.Minute)
	template := &x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(serialBytes),
		Subject:      pkix.Name{CommonName: fmt.Sprintf("NRE Internal Tunnel CA %s generation %d", g.domainID, generation)},
		NotBefore:    notBefore, NotAfter: now.Add(g.lifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, MaxPathLen: 0, MaxPathLenZero: true,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(g.random, template, template, &key.PublicKey, key)
	if err != nil {
		return PKIAuthorityMaterial{}, fmt.Errorf("create PKI authority certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return PKIAuthorityMaterial{}, fmt.Errorf("parse PKI authority certificate: %w", err)
	}
	if generatedKey {
		privateDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return PKIAuthorityMaterial{}, fmt.Errorf("marshal PKI authority key: %w", err)
		}
		sealedReference, err := g.vault.SealCAKey(g.domainID, generation, "ca-signing", privateDER)
		clear(privateDER)
		if err != nil {
			return PKIAuthorityMaterial{}, err
		}
		keyReference = sealedReference
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return PKIAuthorityMaterial{}, err
	}
	keyFingerprint := sha256.Sum256(publicDER)
	certificateFingerprint := sha256.Sum256(der)
	return PKIAuthorityMaterial{
		Generation: generation, CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyReference: keyReference, KeyFingerprint: hex.EncodeToString(keyFingerprint[:]),
		CertificateFingerprint: hex.EncodeToString(certificateFingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}, nil
}

func randomPKIIdentifier(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate PKI identifier: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func (s *certificateService) rejectCanonicalPKICertificateMutation(ctx context.Context, certificate ManagedCertificate) error {
	internalRelay := certificate.Usage == "relay_ca" || certificate.Usage == "relay_tunnel" ||
		strings.EqualFold(strings.TrimSpace(certificate.Domain), relayCADomainIdentity)
	if !internalRelay {
		return nil
	}
	source, ok := s.store.(interface {
		LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
	})
	if !ok {
		return nil
	}
	state, err := source.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	if state.Settings == nil || state.Settings.UpgradeState != PKIUpgradeStateTunnelMTLSOnly {
		return nil
	}
	return fmt.Errorf("%w: internal relay certificates are owned by the canonical PKI service", ErrInvalidArgument)
}
