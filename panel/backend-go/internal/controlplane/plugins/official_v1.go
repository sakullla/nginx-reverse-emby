package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
)

const (
	OfficialMarketProvenanceFile = "provenance.json"
	OfficialPackageFilesFile     = "package.files.json"
	OfficialPackageSignatureFile = "signature.json"
)

var officialCommitOIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type officialMarketManifestV1 struct {
	SchemaVersion int                       `yaml:"schema_version"`
	Commit        string                    `yaml:"commit"`
	SDKABI        string                    `yaml:"sdk_abi"`
	Packages      []officialMarketPackageV1 `yaml:"packages"`
}

type officialMarketPackageV1 struct {
	ID             string `yaml:"id"`
	Version        string `yaml:"version"`
	Runtime        string `yaml:"runtime"`
	ABI            string `yaml:"abi"`
	PackageSHA256  string `yaml:"package_sha256"`
	PackageURL     string `yaml:"package_url"`
	SignerIdentity string `yaml:"signer_identity"`
}

type officialPackageFileManifestV1 struct {
	SchemaVersion int                    `json:"schema_version"`
	PayloadSHA256 string                 `json:"payload_sha256"`
	Files         []officialFileRecordV1 `json:"files"`
}

type officialFileRecordV1 struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type officialPackageSignatureV1 struct {
	SchemaVersion int    `json:"schema_version"`
	Algorithm     string `json:"algorithm"`
	Identity      string `json:"identity"`
	PayloadSHA256 string `json:"payload_sha256"`
	Signature     string `json:"signature"`
}

type officialMarketProvenanceV1 struct {
	SchemaVersion       int                           `json:"schema_version"`
	RepositoryCommit    string                        `json:"repository_commit"`
	MarketSHA256        string                        `json:"market_sha256"`
	SDKRepositoryCommit string                        `json:"sdk_repository_commit"`
	SDKDescriptorSHA256 string                        `json:"sdk_descriptor_sha256"`
	SDKABIs             []string                      `json:"sdk_abis"`
	SignerIdentity      string                        `json:"signer_identity"`
	Packages            []officialPackageProvenanceV1 `json:"packages"`
}

type officialPackageProvenanceV1 struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Path          string `json:"path"`
	PackageSHA256 string `json:"package_sha256"`
}

type officialPackageEnvelopeV1 struct {
	digest  string
	stats   packageStats
	records []officialFileRecordV1
}

func (v *Validator) validateOfficialMarketV1Snapshot(root, sourceRoot string, marketData []byte) (ValidatedMarket, error) {
	var market officialMarketManifestV1
	if err := decodeStrictYAML(marketData, &market); err != nil {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, err)
	}
	if err := v.validateOfficialMarketManifestV1(market); err != nil {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, err)
	}
	provenance, err := readOfficialMarketProvenanceV1(root)
	if err != nil {
		return ValidatedMarket{}, err
	}
	if err := validateOfficialMarketProvenanceV1(marketData, market, provenance); err != nil {
		return ValidatedMarket{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	if err := inspectOfficialMarketTreeV1(root, market, v.options); err != nil {
		return ValidatedMarket{}, err
	}

	projection := MarketManifest{SchemaVersion: 1, Name: "Sakullla Official", Entries: make([]MarketEntry, 0, len(market.Packages))}
	result := ValidatedMarket{Manifest: projection, Packages: make([]ValidatedPackage, 0, len(market.Packages))}
	for _, entry := range market.Packages {
		packageRoot, err := securePackagePath(root, entry.PackageURL)
		if err != nil {
			return ValidatedMarket{}, validationError("package_path", entry.PackageURL, err)
		}
		envelope, err := v.verifyOfficialPackageEnvelopeV1(packageRoot, entry)
		if err != nil {
			return ValidatedMarket{}, err
		}
		sourcePackageRoot := filepath.Clean(filepath.Join(sourceRoot, filepath.FromSlash(entry.PackageURL)))
		expected := PackageExpectation{ID: entry.ID, Version: entry.Version, SignatureKeyID: entry.SignerIdentity}
		validated, err := v.validatePackageContent(packageRoot, sourcePackageRoot, expected, envelope.stats, func(string, PackageExpectation) (string, error) {
			return envelope.digest, nil
		}, v.verifyOfficialManifestSignatureV1)
		if err != nil {
			return ValidatedMarket{}, err
		}
		if validated.Manifest.Runtime.Kind != entry.Runtime || validated.Manifest.Runtime.ABI != entry.ABI {
			return ValidatedMarket{}, validationError("runtime_mismatch", PackageManifestFile, fmt.Errorf("market runtime for %s@%s differs from signed manifest", entry.ID, entry.Version))
		}
		if err := validateOfficialManifestPayloadV1(validated.Manifest, envelope.records); err != nil {
			return ValidatedMarket{}, err
		}
		artifacts := make([]ArtifactIndex, 0, len(validated.Manifest.Artifacts))
		for _, artifact := range validated.Manifest.Artifacts {
			artifacts = append(artifacts, ArtifactIndex{SHA256: strings.ToLower(artifact.SHA256), Size: artifact.Size, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH})
		}
		result.Manifest.Entries = append(result.Manifest.Entries, MarketEntry{
			ID: entry.ID, Version: entry.Version, Description: validated.Manifest.Description,
			Capabilities: append([]string(nil), validated.Manifest.ExtensionPoints...), Compatibility: validated.Manifest.Compatibility,
			Runtime:   RuntimeIndex{Kind: validated.Manifest.Runtime.Kind, ABI: validated.Manifest.Runtime.ABI, HostScope: validated.Manifest.Runtime.HostScope, PolicyKind: validated.Manifest.Runtime.PolicyKind},
			Artifacts: artifacts, PackagePath: entry.PackageURL, PackageSHA256: envelope.digest,
			SignatureKeyID: entry.SignerIdentity, Provenance: "sakullla-plugins", Official: true,
		})
		result.Packages = append(result.Packages, validated)
	}
	return result, nil
}

func (v *Validator) validateOfficialMarketManifestV1(market officialMarketManifestV1) error {
	if market.SchemaVersion != 1 || !officialCommitOIDPattern.MatchString(market.Commit) || market.Commit == strings.Repeat("0", 40) {
		return errors.New("schema_version 1 and a non-zero lowercase full source commit are required")
	}
	if market.SDKABI != pluginsdk.PolicyABIV1+","+pluginsdk.RPCABIV1 {
		return errors.New("sdk_abi must be the canonical supported ABI list")
	}
	if len(market.Packages) == 0 || len(market.Packages) > v.options.MaxMarketPackages {
		return errors.New("market package count is empty or exceeds limit")
	}
	previous := ""
	for index, entry := range market.Packages {
		key := entry.ID + "\x00" + entry.Version
		expectedPath := "packages/" + entry.ID + "/" + entry.Version
		if !identifierPattern.MatchString(entry.ID) || !IsSemanticVersion(entry.Version) || !hexDigestPattern.MatchString(entry.PackageSHA256) {
			return fmt.Errorf("package %d has invalid identity, version, or digest", index)
		}
		if entry.PackageURL != expectedPath || entry.SignerIdentity != OfficialSignatureKeyID {
			return fmt.Errorf("package %s@%s has non-canonical path or signer", entry.ID, entry.Version)
		}
		if entry.Runtime != pluginsdk.RuntimeWASMPolicy && entry.Runtime != pluginsdk.RuntimeRPCService {
			return fmt.Errorf("package %s@%s has unsupported runtime", entry.ID, entry.Version)
		}
		if (entry.Runtime == pluginsdk.RuntimeWASMPolicy && entry.ABI != pluginsdk.PolicyABIV1) || (entry.Runtime == pluginsdk.RuntimeRPCService && entry.ABI != pluginsdk.RPCABIV1) {
			return fmt.Errorf("package %s@%s runtime and ABI differ", entry.ID, entry.Version)
		}
		if previous != "" && key <= previous {
			return errors.New("market packages must be unique and sorted by id and version")
		}
		previous = key
	}
	return nil
}

func readOfficialMarketProvenanceV1(root string) (officialMarketProvenanceV1, error) {
	data, err := readBoundedFile(filepath.Join(root, OfficialMarketProvenanceFile), 1<<20)
	if err != nil {
		return officialMarketProvenanceV1{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	var provenance officialMarketProvenanceV1
	if err := decodeStrictJSONV1(data, &provenance); err != nil {
		return officialMarketProvenanceV1{}, validationError("market_provenance", OfficialMarketProvenanceFile, err)
	}
	return provenance, nil
}

func validateOfficialMarketProvenanceV1(marketData []byte, market officialMarketManifestV1, provenance officialMarketProvenanceV1) error {
	marketDigest := sha256.Sum256(marketData)
	if provenance.SchemaVersion != 1 || provenance.RepositoryCommit != market.Commit || provenance.MarketSHA256 != hex.EncodeToString(marketDigest[:]) {
		return errors.New("market source commit or SHA-256 differs from provenance")
	}
	if !officialCommitOIDPattern.MatchString(provenance.SDKRepositoryCommit) || provenance.SDKRepositoryCommit == strings.Repeat("0", 40) {
		return errors.New("SDK provenance requires a non-zero lowercase full commit")
	}
	if provenance.SDKDescriptorSHA256 != protoschema.CanonicalDescriptorSetSHA256 {
		return errors.New("SDK descriptor digest differs from the host contract")
	}
	if len(provenance.SDKABIs) != 2 || provenance.SDKABIs[0] != pluginsdk.PolicyABIV1 || provenance.SDKABIs[1] != pluginsdk.RPCABIV1 || provenance.SignerIdentity != OfficialSignatureKeyID {
		return errors.New("SDK ABI or signer provenance is invalid")
	}
	if len(provenance.Packages) != len(market.Packages) {
		return errors.New("package provenance count differs from market")
	}
	for index, entry := range market.Packages {
		evidence := provenance.Packages[index]
		if evidence.ID != entry.ID || evidence.Version != entry.Version || evidence.Path != entry.PackageURL || evidence.PackageSHA256 != entry.PackageSHA256 {
			return fmt.Errorf("package provenance %d differs from market", index)
		}
	}
	return nil
}

func (v *Validator) verifyOfficialPackageEnvelopeV1(root string, entry officialMarketPackageV1) (officialPackageEnvelopeV1, error) {
	manifestData, err := readBoundedFile(filepath.Join(root, OfficialPackageFilesFile), v.options.MaxFileBytes)
	if err != nil {
		return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, err)
	}
	var manifest officialPackageFileManifestV1
	if err := decodeStrictJSONV1(manifestData, &manifest); err != nil {
		return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, err)
	}
	if manifest.SchemaVersion != 1 || !hexDigestPattern.MatchString(manifest.PayloadSHA256) || len(manifest.Files) == 0 {
		return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, errors.New("schema_version 1, payload digest, and files are required"))
	}
	actual, stats, err := collectOfficialPackageFilesV1(root, v.options)
	if err != nil {
		return officialPackageEnvelopeV1{}, err
	}
	if len(actual) != len(manifest.Files)+2 {
		return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, errors.New("file manifest does not cover the package tree"))
	}
	actualByPath := make(map[string]officialFileRecordV1, len(actual))
	for _, record := range actual {
		actualByPath[record.Path] = record
	}
	previous := ""
	for index, record := range manifest.Files {
		if !fs.ValidPath(record.Path) || record.Path == OfficialPackageFilesFile || record.Path == OfficialPackageSignatureFile || record.Path <= previous {
			return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, fmt.Errorf("record %d path is non-canonical, duplicate, or unsorted", index))
		}
		if (record.Mode != "0644" && record.Mode != "0755") || !hexDigestPattern.MatchString(record.SHA256) || record.Size < 0 {
			return officialPackageEnvelopeV1{}, validationError("package_files", OfficialPackageFilesFile, fmt.Errorf("record %d mode, digest, or size is invalid", index))
		}
		got, ok := actualByPath[record.Path]
		if !ok || got.SHA256 != record.SHA256 || got.Size != record.Size {
			return officialPackageEnvelopeV1{}, validationError("package_payload", record.Path, errors.New("file bytes or size differ from signed record"))
		}
		previous = record.Path
	}
	if digestOfficialFileRecordsV1(manifest.Files) != manifest.PayloadSHA256 {
		return officialPackageEnvelopeV1{}, validationError("package_digest", OfficialPackageFilesFile, errors.New("payload digest differs from file records"))
	}

	signatureData, err := readBoundedFile(filepath.Join(root, OfficialPackageSignatureFile), 4096)
	if err != nil {
		return officialPackageEnvelopeV1{}, validationError("signature_missing", OfficialPackageSignatureFile, err)
	}
	var signature officialPackageSignatureV1
	if err := decodeStrictJSONV1(signatureData, &signature); err != nil {
		return officialPackageEnvelopeV1{}, validationError("signature", OfficialPackageSignatureFile, err)
	}
	if signature.SchemaVersion != 1 || signature.Algorithm != "ed25519" || signature.Identity != entry.SignerIdentity || signature.PayloadSHA256 != manifest.PayloadSHA256 {
		return officialPackageEnvelopeV1{}, validationError("signature", OfficialPackageSignatureFile, errors.New("signature identity, algorithm, or payload digest differs"))
	}
	encoded, err := base64.StdEncoding.Strict().DecodeString(signature.Signature)
	if err != nil || len(encoded) != ed25519.SignatureSize {
		return officialPackageEnvelopeV1{}, validationError("signature", OfficialPackageSignatureFile, errors.New("signature must be canonical base64 Ed25519 bytes"))
	}
	publicKey, ok := v.trustedSigners[entry.SignerIdentity]
	payload, decodeErr := hex.DecodeString(manifest.PayloadSHA256)
	if !ok || decodeErr != nil || len(payload) != sha256.Size || !ed25519.Verify(publicKey, payload, encoded) {
		return officialPackageEnvelopeV1{}, validationError("signature_mismatch", OfficialPackageSignatureFile, errors.New("raw payload SHA-256 signature is invalid"))
	}

	all := append([]officialFileRecordV1(nil), manifest.Files...)
	filesRecord := actualByPath[OfficialPackageFilesFile]
	filesRecord.Mode = "0644"
	signatureRecord := actualByPath[OfficialPackageSignatureFile]
	signatureRecord.Mode = "0644"
	all = append(all, filesRecord, signatureRecord)
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	packageDigest := digestOfficialFileRecordsV1(all)
	if packageDigest != entry.PackageSHA256 {
		return officialPackageEnvelopeV1{}, validationError("index_checksum_mismatch", OfficialPackageFilesFile, errors.New("market package_sha256 differs from complete package tree"))
	}
	return officialPackageEnvelopeV1{digest: packageDigest, stats: stats, records: manifest.Files}, nil
}

func collectOfficialPackageFilesV1(root string, options ValidatorOptions) ([]officialFileRecordV1, packageStats, error) {
	var records []officialFileRecordV1
	var stats packageStats
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
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
			return validationError("file_type", relative, errors.New("only regular non-symlink package files are allowed"))
		}
		data, err := readBoundedFile(name, options.MaxFileBytes)
		if err != nil {
			return validationError("package_payload", relative, err)
		}
		digest := sha256.Sum256(data)
		records = append(records, officialFileRecordV1{Path: relative, SHA256: hex.EncodeToString(digest[:]), Size: info.Size()})
		stats.files++
		stats.bytes += info.Size()
		if stats.files > options.MaxFiles || stats.bytes > options.MaxPackageBytes {
			return validationError("size_limit", relative, errors.New("package size or file count limit exceeded"))
		}
		return nil
	})
	return records, stats, err
}

func digestOfficialFileRecordsV1(records []officialFileRecordV1) string {
	hash := sha256.New()
	for _, record := range records {
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%s\n", record.Path, record.Mode, record.Size, record.SHA256)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (v *Validator) verifyOfficialManifestSignatureV1(_ string, manifest Manifest, _ string, expectedKeyID string) error {
	if manifest.Signature.Algorithm != "ed25519" || manifest.Signature.KeyID != expectedKeyID || manifest.Signature.File != OfficialPackageSignatureFile {
		return validationError("signature", PackageManifestFile, errors.New("official manifest must bind the verified signature.json signer"))
	}
	if _, ok := v.trustedSigners[manifest.Signature.KeyID]; !ok {
		return validationError("signature_identity", OfficialPackageSignatureFile, fmt.Errorf("signer %q is not trusted", manifest.Signature.KeyID))
	}
	return nil
}

func validateOfficialManifestPayloadV1(manifest Manifest, records []officialFileRecordV1) error {
	declared := map[string]string{
		PackageManifestFile: "0644", manifest.ConfigSchema: "0644", "NOTICE": "0644", "sbom.spdx.json": "0644",
	}
	if manifest.UISchema != "" {
		declared[manifest.UISchema] = "0644"
	}
	for _, asset := range manifest.Assets {
		declared[filepath.ToSlash(asset)] = "0644"
	}
	for _, migration := range manifest.Migrations {
		declared[filepath.ToSlash(migration.File)] = "0644"
	}
	for _, artifact := range manifest.Artifacts {
		mode := "0644"
		if artifact.Mode == "executable" {
			mode = "0755"
		}
		declared[filepath.ToSlash(artifact.Path)] = mode
	}
	if len(declared) != len(records) {
		return validationError("undeclared_payload", OfficialPackageFilesFile, errors.New("signed payload and manifest declarations differ"))
	}
	for _, record := range records {
		mode, ok := declared[record.Path]
		if !ok || mode != record.Mode {
			return validationError("undeclared_payload", record.Path, errors.New("signed file path or mode is not declared by plugin.yaml"))
		}
	}
	return nil
}

func inspectOfficialMarketTreeV1(root string, market officialMarketManifestV1, options ValidatorOptions) error {
	allowedRoot := map[string]struct{}{
		MarketManifestFile: {}, OfficialMarketProvenanceFile: {}, "NOTICE": {}, "SBOM.spdx.json": {}, "THIRD_PARTY_LICENSES.json": {},
	}
	packageRoots := make([]string, 0, len(market.Packages))
	for _, entry := range market.Packages {
		packageRoots = append(packageRoots, entry.PackageURL)
	}
	files, bytesTotal := 0, int64(0)
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
			return validationError("market_tree", relative, errors.New("market tree contains a non-regular file"))
		}
		_, allowed := allowedRoot[relative]
		if !allowed {
			for _, packageRoot := range packageRoots {
				if strings.HasPrefix(relative, packageRoot+"/") {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return validationError("market_tree", relative, errors.New("market tree contains unreferenced content"))
		}
		files++
		bytesTotal += info.Size()
		if files > options.MaxMarketFiles || bytesTotal > options.MaxMarketBytes {
			return validationError("market_budget", relative, errors.New("market tree exceeds file or byte budget"))
		}
		return nil
	})
}

func decodeStrictJSONV1(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are forbidden")
		}
		return err
	}
	return nil
}
