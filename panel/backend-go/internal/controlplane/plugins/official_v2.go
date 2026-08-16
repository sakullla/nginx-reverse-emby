package plugins

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
)

const officialPackageBlobFormatV1 = "tar+gzip-v1"

type officialMarketManifestV2 struct {
	SchemaVersion int                       `yaml:"schema_version"`
	Commit        string                    `yaml:"commit"`
	SDKABI        string                    `yaml:"sdk_abi"`
	Packages      []officialMarketPackageV2 `yaml:"packages"`
}

type officialMarketPackageV2 struct {
	ID             string          `yaml:"id"`
	Version        string          `yaml:"version"`
	Description    string          `yaml:"description"`
	Capabilities   []string        `yaml:"capabilities"`
	Compatibility  Compatibility   `yaml:"compatibility"`
	Runtime        string          `yaml:"runtime"`
	ABI            string          `yaml:"abi"`
	HostScope      string          `yaml:"host_scope"`
	PolicyKind     string          `yaml:"policy_kind,omitempty"`
	Artifacts      []ArtifactIndex `yaml:"artifacts"`
	PackageSHA256  string          `yaml:"package_sha256"`
	PackageURL     string          `yaml:"package_url"`
	BlobSHA256     string          `yaml:"blob_sha256"`
	BlobSize       int64           `yaml:"blob_size"`
	BlobFormat     string          `yaml:"blob_format"`
	SignerIdentity string          `yaml:"signer_identity"`
}

type officialMarketProvenanceV2 struct {
	SchemaVersion       int                           `json:"schema_version"`
	RepositoryCommit    string                        `json:"repository_commit"`
	MarketSHA256        string                        `json:"market_sha256"`
	SDKRepositoryCommit string                        `json:"sdk_repository_commit"`
	SDKDescriptorSHA256 string                        `json:"sdk_descriptor_sha256"`
	SDKABIs             []string                      `json:"sdk_abis"`
	SignerIdentity      string                        `json:"signer_identity"`
	Packages            []officialPackageProvenanceV2 `json:"packages"`
}

type officialPackageProvenanceV2 struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	PackageSHA256 string `json:"package_sha256"`
	PackageURL    string `json:"package_url"`
	BlobSHA256    string `json:"blob_sha256"`
	BlobSize      int64  `json:"blob_size"`
	BlobFormat    string `json:"blob_format"`
}

func (v *Validator) validateOfficialMarketV2Snapshot(root, sourceRoot string, marketData []byte) (ValidatedMarket, error) {
	var market officialMarketManifestV2
	if err := decodeStrictYAML(marketData, &market); err != nil {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, err)
	}
	if err := v.validateOfficialMarketManifestV2(market); err != nil {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, err)
	}
	provenanceData, err := readBoundedFile(filepath.Join(root, OfficialMarketProvenanceFile), 1<<20)
	if err != nil {
		return ValidatedMarket{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	var provenance officialMarketProvenanceV2
	if err := decodeStrictJSONV1(provenanceData, &provenance); err != nil {
		return ValidatedMarket{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	if err := v.verifyOfficialMarketProvenanceSignatureV1(root, provenanceData); err != nil {
		return ValidatedMarket{}, err
	}
	if err := validateOfficialMarketProvenanceV2(marketData, market, provenance); err != nil {
		return ValidatedMarket{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	if err := inspectOfficialMarketIndexV2(root, v.options); err != nil {
		return ValidatedMarket{}, err
	}
	projection := MarketManifest{SchemaVersion: 2, Name: "Sakullla Official", Entries: make([]MarketEntry, 0, len(market.Packages))}
	for _, entry := range market.Packages {
		projection.Entries = append(projection.Entries, MarketEntry{
			ID: entry.ID, Version: entry.Version, Description: entry.Description, Capabilities: append([]string(nil), entry.Capabilities...), Compatibility: entry.Compatibility,
			Runtime: RuntimeIndex{Kind: entry.Runtime, ABI: entry.ABI, HostScope: entry.HostScope, PolicyKind: entry.PolicyKind}, Artifacts: append([]ArtifactIndex(nil), entry.Artifacts...),
			PackagePath: entry.PackageURL, PackageSHA256: entry.PackageSHA256, BlobSHA256: entry.BlobSHA256, BlobSize: entry.BlobSize, BlobFormat: entry.BlobFormat,
			SignatureKeyID: entry.SignerIdentity, Provenance: "sakullla-plugins", Official: true,
		})
	}
	v.runSnapshotHook("market_tree", sourceRoot, root)
	return ValidatedMarket{Manifest: projection}, nil
}

func (v *Validator) validateOfficialMarketManifestV2(market officialMarketManifestV2) error {
	if market.SchemaVersion != 2 || !officialCommitOIDPattern.MatchString(market.Commit) || market.Commit == strings.Repeat("0", 40) {
		return errors.New("schema_version 2 and a non-zero lowercase full source commit are required")
	}
	if market.SDKABI != pluginsdk.PolicyABIV1+","+pluginsdk.RPCABIV1 || len(market.Packages) == 0 || len(market.Packages) > v.options.MaxMarketPackages {
		return errors.New("SDK ABI or market package count is invalid")
	}
	previous := ""
	for index, entry := range market.Packages {
		key := entry.ID + "\x00" + entry.Version
		if !identifierPattern.MatchString(entry.ID) || !IsSemanticVersion(entry.Version) || len(entry.Description) == 0 || len(entry.Description) > 4096 || !hexDigestPattern.MatchString(entry.PackageSHA256) || !hexDigestPattern.MatchString(entry.BlobSHA256) {
			return fmt.Errorf("package %d has invalid identity, version, description, or digest", index)
		}
		if entry.PackageURL != officialPackageBlobURL(market.Commit, entry.ID, entry.Version, entry.BlobSHA256) || entry.BlobSize <= 0 || entry.BlobSize > v.options.MaxPackageBytes || entry.BlobFormat != officialPackageBlobFormatV1 || entry.SignerIdentity != OfficialSignatureKeyID {
			return fmt.Errorf("package %s@%s has invalid immutable transport metadata", entry.ID, entry.Version)
		}
		if len(entry.Capabilities) == 0 || len(entry.Capabilities) > 64 {
			return fmt.Errorf("package %s@%s capability list is invalid", entry.ID, entry.Version)
		}
		seenCapabilities := map[string]struct{}{}
		for _, capability := range entry.Capabilities {
			if !identifierPattern.MatchString(capability) || capability != strings.TrimSpace(capability) {
				return fmt.Errorf("package %s@%s capability is invalid", entry.ID, entry.Version)
			}
			if _, duplicate := seenCapabilities[capability]; duplicate {
				return fmt.Errorf("package %s@%s capability is duplicated", entry.ID, entry.Version)
			}
			seenCapabilities[capability] = struct{}{}
		}
		runtimeIndex := RuntimeIndex{Kind: entry.Runtime, ABI: entry.ABI, HostScope: entry.HostScope, PolicyKind: entry.PolicyKind}
		if err := v.validateRuntimeIndex(runtimeIndex, entry.Artifacts); err != nil {
			return fmt.Errorf("package %s@%s runtime: %w", entry.ID, entry.Version, err)
		}
		if previous != "" && key <= previous {
			return errors.New("market packages must be unique and sorted by id and version")
		}
		previous = key
	}
	return nil
}

func validateOfficialMarketProvenanceV2(marketData []byte, market officialMarketManifestV2, provenance officialMarketProvenanceV2) error {
	marketDigest := digestBytes(marketData)
	if provenance.SchemaVersion != 2 || provenance.RepositoryCommit != market.Commit || provenance.MarketSHA256 != marketDigest || provenance.SignerIdentity != OfficialSignatureKeyID {
		return errors.New("market source, digest, signer, or schema differs from provenance")
	}
	if !officialCommitOIDPattern.MatchString(provenance.SDKRepositoryCommit) || provenance.SDKRepositoryCommit == strings.Repeat("0", 40) || provenance.SDKDescriptorSHA256 != protoschema.CanonicalDescriptorSetSHA256 {
		return errors.New("SDK provenance is invalid")
	}
	if len(provenance.SDKABIs) != 2 || provenance.SDKABIs[0] != pluginsdk.PolicyABIV1 || provenance.SDKABIs[1] != pluginsdk.RPCABIV1 || len(provenance.Packages) != len(market.Packages) {
		return errors.New("SDK ABI or package provenance count differs")
	}
	for index, entry := range market.Packages {
		evidence := provenance.Packages[index]
		if evidence.ID != entry.ID || evidence.Version != entry.Version || evidence.PackageSHA256 != entry.PackageSHA256 || evidence.PackageURL != entry.PackageURL || evidence.BlobSHA256 != entry.BlobSHA256 || evidence.BlobSize != entry.BlobSize || evidence.BlobFormat != entry.BlobFormat {
			return fmt.Errorf("package provenance %d differs from market", index)
		}
	}
	return nil
}

func inspectOfficialMarketIndexV2(root string, options ValidatorOptions) error {
	allowed := map[string]struct{}{MarketManifestFile: {}, OfficialMarketProvenanceFile: {}, OfficialMarketSignatureFile: {}, OfficialMarketAgentsFile: {}, "NOTICE": {}, "SBOM.spdx.json": {}, "THIRD_PARTY_LICENSES.json": {}, ".gitattributes": {}}
	files, total := 0, int64(0)
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || entry.Type()&fs.ModeSymlink != 0 {
			return validationError("market_tree", relative, errors.New("market index contains a non-regular file"))
		}
		if _, ok := allowed[relative]; !ok {
			return validationError("market_tree", relative, errors.New("market index contains unreferenced content"))
		}
		files++
		total += info.Size()
		if files > options.MaxMarketFiles || total > options.MaxMarketBytes {
			return validationError("market_budget", relative, errors.New("market index exceeds file or byte budget"))
		}
		return nil
	})
}

func officialPackageBlobURL(commit, id, version, blobDigest string) string {
	name := id + "-" + version + "-" + blobDigest + ".nrepkg"
	return "https://github.com/sakullla/sakullla-plugins/releases/download/official-" + commit + "/" + name
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:])
}
