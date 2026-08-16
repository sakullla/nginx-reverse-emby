package acmeflow

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestIntegrationGenerationStagePromoteAndProjection(t *testing.T) {
	if !acmeflowIntegrationTierEnabled {
		t.Skip("generation persistence scenarios run in the integration tier")
	}
	if testing.Short() {
		t.Skip("durable generation projection runs in the integration tier")
	}
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.July, 25, 6, 0, 0, 0, time.UTC)
	store, err := OpenStateStore(t.TempDir(), WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	oldInput := testGenerationInput(t, 1, now)
	oldManifest, err := store.StageGeneration(ctx, oldInput)
	if err != nil {
		t.Fatalf("StageGeneration(old) error = %v", err)
	}
	if err := store.PromoteGeneration(ctx, oldManifest.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration(old) error = %v", err)
	}

	current, err := store.LoadCurrent(ctx)
	if err != nil {
		t.Fatalf("LoadCurrent() error = %v", err)
	}
	assertGenerationEqualsInput(t, current, oldInput)

	stagedAgain, err := store.StageGeneration(ctx, oldInput)
	if err != nil {
		t.Fatalf("StageGeneration(idempotent) error = %v", err)
	}
	if stagedAgain.ID != oldManifest.ID {
		t.Fatalf("StageGeneration(idempotent) ID = %q, want %q", stagedAgain.ID, oldManifest.ID)
	}

	newInput := testGenerationInput(t, 2, now)
	newManifest, err := store.StageGeneration(ctx, newInput)
	if err != nil {
		t.Fatalf("StageGeneration(new) error = %v", err)
	}
	projectionErr := errors.New("legacy projection failed")
	err = store.PromoteGeneration(ctx, newManifest.ID, LegacyProjectionFunc(func(context.Context, Generation) error {
		return projectionErr
	}))
	if !errors.Is(err, projectionErr) {
		t.Fatalf("PromoteGeneration(projection failure) error = %v", err)
	}
	current, err = store.LoadCurrent(ctx)
	if err != nil {
		t.Fatalf("LoadCurrent() after projection failure error = %v", err)
	}
	if current.Manifest.ID != oldManifest.ID {
		t.Fatalf("projection failure promoted generation %q", current.Manifest.ID)
	}

	projected := false
	if err := store.PromoteGeneration(ctx, newManifest.ID, LegacyProjectionFunc(func(_ context.Context, generation Generation) error {
		projected = generation.Manifest.ID == newManifest.ID
		return nil
	})); err != nil {
		t.Fatalf("PromoteGeneration(new) error = %v", err)
	}
	if !projected {
		t.Fatal("PromoteGeneration() did not run the legacy projection hook")
	}
	current, err = store.LoadCurrent(ctx)
	if err != nil {
		t.Fatalf("LoadCurrent() new error = %v", err)
	}
	assertGenerationEqualsInput(t, current, newInput)
}

func TestIntegrationGenerationPendingReferenceSurvivesRestart(t *testing.T) {
	if !acmeflowIntegrationTierEnabled {
		t.Skip("generation persistence scenarios run in the integration tier")
	}
	if testing.Short() {
		t.Skip("durable pending generation recovery runs in the integration tier")
	}
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.July, 27, 7, 30, 0, 0, time.UTC)
	root := t.TempDir()
	input := testGenerationInput(t, 3, now)
	input.Pending = &PendingGenerationInput{
		PolicySHA256:  strings.Repeat("a", sha256.Size*2),
		RecordRenewal: true,
	}
	store, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	manifest, err := store.StageGeneration(ctx, input)
	if err != nil {
		t.Fatalf("StageGeneration() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore(reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	pending, err := reopened.LoadPendingGeneration(ctx)
	if err != nil {
		t.Fatalf("LoadPendingGeneration() error = %v", err)
	}
	if pending.Reference.GenerationID != manifest.ID || pending.Reference.PolicySHA256 != input.Pending.PolicySHA256 || !pending.Reference.RecordRenewal {
		t.Fatalf("pending reference = %#v", pending.Reference)
	}
	assertGenerationEqualsInput(t, pending.Generation, input)
	if err := reopened.ClearPendingGeneration(ctx, manifest.ID); err != nil {
		t.Fatalf("ClearPendingGeneration() error = %v", err)
	}
	if _, err := reopened.LoadPendingGeneration(ctx); !errors.Is(err, ErrNoPendingGeneration) {
		t.Fatalf("LoadPendingGeneration() after clear error = %v, want ErrNoPendingGeneration", err)
	}
}

func TestIntegrationGenerationFaultMatrixNeverMixesCurrentMaterial(t *testing.T) {
	if !acmeflowIntegrationTierEnabled {
		t.Skip("generation persistence scenarios run in the integration tier")
	}
	if testing.Short() {
		t.Skip("durable generation fault recovery runs in the integration tier")
	}
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.July, 25, 6, 0, 0, 0, time.UTC)
	oldInput := testGenerationInput(t, 11, now)
	newInput := testGenerationInput(t, 12, now)

	var discovered []PersistenceFaultPoint
	discoveryStore, err := OpenStateStore(t.TempDir(),
		WithStateClock(func() time.Time { return now }),
		WithPersistenceFaultInjector(func(point PersistenceFaultPoint) error {
			discovered = append(discovered, point)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("OpenStateStore(discovery) error = %v", err)
	}
	oldManifest, err := discoveryStore.StageGeneration(ctx, oldInput)
	if err != nil {
		t.Fatalf("StageGeneration(discovery old) error = %v", err)
	}
	if err := discoveryStore.PromoteGeneration(ctx, oldManifest.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration(discovery old) error = %v", err)
	}
	discovered = nil
	newManifest, err := discoveryStore.StageGeneration(ctx, newInput)
	if err != nil {
		t.Fatalf("StageGeneration(discovery new) error = %v", err)
	}
	if err := discoveryStore.PromoteGeneration(ctx, newManifest.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration(discovery new) error = %v", err)
	}
	_ = discoveryStore.Close()

	points := uniqueFaultPoints(discovered)
	if len(points) < 20 {
		t.Fatalf("discovered only %d persistence boundaries: %v", len(points), points)
	}
	points = representativeGenerationFaultPoints(t, points)
	for _, point := range points {
		point := point
		t.Run(strings.NewReplacer(".", "_", "/", "_").Replace(string(point)), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			injected := errors.New("injected persistence fault")
			armed := false
			store, err := OpenStateStore(root,
				WithStateClock(func() time.Time { return now }),
				WithPersistenceFaultInjector(func(actual PersistenceFaultPoint) error {
					if armed && actual == point {
						return injected
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("OpenStateStore() error = %v", err)
			}
			base, err := store.StageGeneration(ctx, oldInput)
			if err != nil {
				t.Fatalf("StageGeneration(base) error = %v", err)
			}
			if err := store.PromoteGeneration(ctx, base.ID, nil); err != nil {
				t.Fatalf("PromoteGeneration(base) error = %v", err)
			}

			armed = true
			candidate, stageErr := store.StageGeneration(ctx, newInput)
			operationErr := stageErr
			if stageErr == nil {
				operationErr = store.PromoteGeneration(ctx, candidate.ID, nil)
			}
			if !errors.Is(operationErr, injected) {
				t.Fatalf("fault point %q produced error %v", point, operationErr)
			}
			_ = store.Close()

			reopened, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
			if err != nil {
				t.Fatalf("OpenStateStore(reopen) error = %v", err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if _, err := reopened.Reconcile(ctx); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			current, err := reopened.LoadCurrent(ctx)
			if err != nil {
				t.Fatalf("LoadCurrent() error = %v", err)
			}
			switch current.Manifest.ID {
			case base.ID:
				assertGenerationEqualsInput(t, current, oldInput)
			case candidate.ID:
				assertGenerationEqualsInput(t, current, newInput)
			default:
				t.Fatalf("LoadCurrent() ID = %q, want old or complete candidate", current.Manifest.ID)
			}
			oldGeneration, err := reopened.LoadGeneration(ctx, base.ID)
			if err != nil {
				t.Fatalf("LoadGeneration(old) error = %v", err)
			}
			assertGenerationEqualsInput(t, oldGeneration, oldInput)
		})
	}
}

func TestIntegrationGenerationFallsBackAfterTruncatedLatestState(t *testing.T) {
	if !acmeflowIntegrationTierEnabled {
		t.Skip("generation persistence scenarios run in the integration tier")
	}
	if testing.Short() {
		t.Skip("durable generation recovery runs in the integration tier")
	}
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.July, 25, 6, 0, 0, 0, time.UTC)
	oldInput := testGenerationInput(t, 21, now)
	newInput := testGenerationInput(t, 22, now)

	for _, test := range []struct {
		name     string
		truncate func(root, newID string) string
	}{
		{
			name: "certificate",
			truncate: func(root, newID string) string {
				return filepath.Join(root, "generations", newID, generationCertificateFile)
			},
		},
		{
			name: "account metadata",
			truncate: func(root, newID string) string {
				return filepath.Join(root, "generations", newID, generationAccountFile)
			},
		},
		{
			name: "current reference",
			truncate: func(root, _ string) string {
				return filepath.Join(root, currentDirectory, currentSlotName(2))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
			if err != nil {
				t.Fatalf("OpenStateStore() error = %v", err)
			}
			oldManifest, err := store.StageGeneration(ctx, oldInput)
			if err != nil {
				t.Fatalf("StageGeneration(old) error = %v", err)
			}
			if err := store.PromoteGeneration(ctx, oldManifest.ID, nil); err != nil {
				t.Fatalf("PromoteGeneration(old) error = %v", err)
			}
			newManifest, err := store.StageGeneration(ctx, newInput)
			if err != nil {
				t.Fatalf("StageGeneration(new) error = %v", err)
			}
			if err := store.PromoteGeneration(ctx, newManifest.ID, nil); err != nil {
				t.Fatalf("PromoteGeneration(new) error = %v", err)
			}
			_ = store.Close()

			if err := os.WriteFile(test.truncate(root, newManifest.ID), []byte("{"), 0o600); err != nil {
				t.Fatalf("truncate latest state: %v", err)
			}
			reopened, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
			if err != nil {
				t.Fatalf("OpenStateStore(reopen) error = %v", err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			current, err := reopened.LoadCurrent(ctx)
			if err != nil {
				t.Fatalf("LoadCurrent() error = %v", err)
			}
			if current.Manifest.ID != oldManifest.ID {
				t.Fatalf("LoadCurrent() ID = %q, want fallback %q", current.Manifest.ID, oldManifest.ID)
			}
			assertGenerationEqualsInput(t, current, oldInput)
		})
	}
}

func testGenerationInput(t *testing.T, serial int64, now time.Time) GenerationInput {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "media.example.com"},
		DNSNames:     []string{"media.example.com"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	return GenerationInput{
		Material: CertificateMaterial{
			CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
			PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		},
		Policy: MaterialPolicy{
			Identifiers: []Identifier{{Type: IdentifierDNS, Value: "media.example.com"}},
			Now:         now,
		},
		Account: AccountMetadata{
			Version:      AccountMetadataVersion,
			DirectoryURL: "https://ca.example/directory",
			Email:        "ops@example.com",
			URI:          "https://ca.example/acct/7",
			Contact:      []string{"mailto:ops@example.com"},
		},
	}
}

func assertGenerationEqualsInput(t *testing.T, generation Generation, input GenerationInput) {
	t.Helper()
	if !bytes.Equal(generation.Material.CertificatePEM, input.Material.CertificatePEM) ||
		!bytes.Equal(generation.Material.PrivateKeyPEM, input.Material.PrivateKeyPEM) ||
		generation.Material.Profile != input.Material.Profile {
		t.Fatalf("generation %q contains mixed certificate material", generation.Manifest.ID)
	}
	if generation.Account.URI != input.Account.URI || generation.Account.DirectoryURL != input.Account.DirectoryURL {
		t.Fatalf("generation %q contains mixed account metadata: %#v", generation.Manifest.ID, generation.Account)
	}
}

func uniqueFaultPoints(points []PersistenceFaultPoint) []PersistenceFaultPoint {
	set := make(map[PersistenceFaultPoint]struct{}, len(points))
	for _, point := range points {
		set[point] = struct{}{}
	}
	result := make([]PersistenceFaultPoint, 0, len(set))
	for point := range set {
		result = append(result, point)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func representativeGenerationFaultPoints(t *testing.T, points []PersistenceFaultPoint) []PersistenceFaultPoint {
	t.Helper()
	wanted := map[PersistenceFaultPoint]struct{}{
		"generation.certificate.temp_created":         {},
		"generation.certificate.renamed":              {},
		"generation.private_key.file_synced":          {},
		"generation.account.parent_synced":            {},
		"generation.manifest.data_written":            {},
		"generation.manifest.renamed":                 {},
		"generation.commit.renamed":                   {},
		"generation.commit.destination_parent_synced": {},
		"current.slot.temp_created":                   {},
		"current.slot.file_synced":                    {},
		"current.slot.renamed":                        {},
		"current.slot.parent_synced":                  {},
	}
	representatives := make([]PersistenceFaultPoint, 0, len(wanted))
	for _, point := range points {
		if _, ok := wanted[point]; ok {
			representatives = append(representatives, point)
			delete(wanted, point)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("representative persistence boundaries were not discovered: %v", wanted)
	}
	return representatives
}
