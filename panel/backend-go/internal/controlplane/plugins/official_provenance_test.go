//go:build !integration

package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"gopkg.in/yaml.v3"
)

func TestOfficialMarketSDKProvenanceIsIndependentOfHostBuild(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		mutate   func(*officialMarketProvenanceV2)
		unsigned bool
		wantCode string
	}{
		{name: "different SDK descriptor"},
		{name: "same SDK descriptor", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = protoschema.CanonicalDescriptorSetSHA256 }},
		{name: "empty descriptor", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = "" }, wantCode: "market_provenance"},
		{name: "short descriptor", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = "abcd" }, wantCode: "market_provenance"},
		{name: "uppercase descriptor", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = strings.Repeat("A", 64) }, wantCode: "market_provenance"},
		{name: "zero descriptor", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = strings.Repeat("0", 64) }, wantCode: "market_provenance"},
		{name: "zero SDK commit", mutate: func(p *officialMarketProvenanceV2) { p.SDKRepositoryCommit = strings.Repeat("0", 40) }, wantCode: "market_provenance"},
		{name: "short SDK commit", mutate: func(p *officialMarketProvenanceV2) { p.SDKRepositoryCommit = "abcd" }, wantCode: "market_provenance"},
		{name: "ABI mismatch", mutate: func(p *officialMarketProvenanceV2) { p.SDKABIs[0] = "nre:policy/v2" }, wantCode: "market_provenance"},
		{name: "package digest mismatch", mutate: func(p *officialMarketProvenanceV2) { p.Packages[0].PackageSHA256 = strings.Repeat("c", 64) }, wantCode: "market_provenance"},
		{name: "market digest mismatch", mutate: func(p *officialMarketProvenanceV2) { p.MarketSHA256 = strings.Repeat("c", 64) }, wantCode: "market_provenance"},
		{name: "unsigned descriptor tamper", mutate: func(p *officialMarketProvenanceV2) { p.SDKDescriptorSHA256 = strings.Repeat("c", 64) }, unsigned: true, wantCode: "market_signature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, validator, provenance := newOfficialProvenanceFixture(t)
			if test.mutate != nil {
				test.mutate(&provenance)
			}
			writeOfficialProvenanceFixture(t, root, provenance, !test.unsigned)
			market, err := validator.ValidateMarket(root, true)
			if test.wantCode == "" {
				if err != nil || len(market.Manifest.Entries) != 1 || market.Manifest.Entries[0].ID != "rate-limit" {
					t.Fatalf("independent SDK market rejected: entries=%+v err=%v", market.Manifest.Entries, err)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Code != test.wantCode {
				t.Fatalf("want %s, got %v", test.wantCode, err)
			}
		})
	}
}

func TestOfficialMarketV1AcceptsIndependentSDKProvenance(t *testing.T) {
	t.Parallel()
	data := []byte("signed legacy market")
	market := officialMarketManifestV1{Commit: strings.Repeat("1", 40)}
	provenance := officialMarketProvenanceV1{
		SchemaVersion: 1, RepositoryCommit: market.Commit, MarketSHA256: digestBytes(data),
		SDKRepositoryCommit: strings.Repeat("2", 40), SDKDescriptorSHA256: strings.Repeat("a", 64),
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignerIdentity: OfficialSignatureKeyID,
	}
	if err := validateOfficialMarketProvenanceV1(data, market, provenance); err != nil {
		t.Fatalf("legacy market tied to host SDK build: %v", err)
	}
	provenance.SDKDescriptorSHA256 = "invalid"
	if err := validateOfficialMarketProvenanceV1(data, market, provenance); err == nil {
		t.Fatal("legacy market accepted malformed SDK provenance")
	}
}

func TestOfficialMarketIndependentSDKStillRequiresAuthenticSignature(t *testing.T) {
	t.Parallel()
	root, validator, provenance := newOfficialProvenanceFixture(t)
	data, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := json.Marshal(officialPackageSignatureV1{
		SchemaVersion: 1, Algorithm: "ed25519", Identity: OfficialSignatureKeyID,
		PayloadSHA256: digestBytes(data), Signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeOwnerBytes(t, root, OfficialMarketSignatureFile, signature)
	_, err = validator.ValidateMarket(root, true)
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "market_signature_mismatch" {
		t.Fatalf("forged signature accepted: %v", err)
	}
}

func TestPackageCompatibilityStillRejectsOlderHost(t *testing.T) {
	t.Parallel()
	root := newSignedWASMPackage(t, "")
	validator := newOwnerValidator()
	validator.options.HostVersion = "0.9.0"
	_, err := validator.ValidatePackage(root, PackageExpectation{})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "compatibility" {
		t.Fatalf("host below package minimum accepted: %v", err)
	}
}

func newOfficialProvenanceFixture(t *testing.T) (string, *Validator, officialMarketProvenanceV2) {
	t.Helper()
	root := t.TempDir()
	commit := strings.Repeat("1", 40)
	entry := officialMarketPackageV2{
		ID: "rate-limit", Version: "1.0.0", Description: "Rate limiting", Capabilities: []string{"http.request"},
		Compatibility: Compatibility{Host: "*", Agent: "*"}, Runtime: pluginsdk.RuntimeWASMPolicy,
		ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent, PolicyKind: "rate",
		Artifacts:     []ArtifactIndex{{SHA256: strings.Repeat("b", 64), Size: 8}},
		PackageSHA256: strings.Repeat("d", 64), BlobSHA256: strings.Repeat("e", 64),
		BlobSize: 100, BlobFormat: officialPackageBlobFormatV1, SignerIdentity: OfficialSignatureKeyID,
	}
	entry.PackageURL = officialPackageBlobURL(commit, entry.ID, entry.Version, entry.BlobSHA256)
	market := officialMarketManifestV2{SchemaVersion: 2, Commit: commit, SDKABI: pluginsdk.PolicyABIV1 + "," + pluginsdk.RPCABIV1, Packages: []officialMarketPackageV2{entry}}
	data, err := yaml.Marshal(market)
	if err != nil {
		t.Fatal(err)
	}
	writeOwnerBytes(t, root, MarketManifestFile, data)
	provenance := officialMarketProvenanceV2{
		SchemaVersion: 2, RepositoryCommit: commit, MarketSHA256: digestBytes(data),
		SDKRepositoryCommit: strings.Repeat("2", 40), SDKDescriptorSHA256: strings.Repeat("a", 64),
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignerIdentity: OfficialSignatureKeyID,
		Packages: []officialPackageProvenanceV2{{ID: entry.ID, Version: entry.Version, PackageSHA256: entry.PackageSHA256,
			PackageURL: entry.PackageURL, BlobSHA256: entry.BlobSHA256, BlobSize: entry.BlobSize, BlobFormat: entry.BlobFormat}},
	}
	writeOfficialProvenanceFixture(t, root, provenance, true)
	validator := NewValidator(ValidatorOptions{})
	validator.trustedSigners[OfficialSignatureKeyID] = ownerSigningKey().Public().(ed25519.PublicKey)
	return root, validator, provenance
}

func writeOfficialProvenanceFixture(t *testing.T, root string, provenance officialMarketProvenanceV2, sign bool) {
	t.Helper()
	data, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	writeOwnerBytes(t, root, OfficialMarketProvenanceFile, data)
	if sign {
		digest := sha256.Sum256(data)
		signature, err := json.Marshal(officialPackageSignatureV1{
			SchemaVersion: 1, Algorithm: "ed25519", Identity: OfficialSignatureKeyID, PayloadSHA256: digestBytes(data),
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(ownerSigningKey(), digest[:])),
		})
		if err != nil {
			t.Fatal(err)
		}
		writeOwnerBytes(t, root, OfficialMarketSignatureFile, signature)
	}
}
