package plugins

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gopkg.in/yaml.v3"
)

const MarketManifestFile = "market.yaml"

const OfficialSignatureKeyID = "sakullla-official-root-2026"

const officialSignaturePublicKeyHex = "9edfaf2a05f9eb3aeff6e9c68f587f8c330a497eadd6f80d4dacb21eb9ff47ce"

const (
	DefaultMaxPackageFiles     = 4096
	DefaultMaxMarketFiles      = 16384
	DefaultMaxFileBytes        = int64(128 << 20)
	DefaultMaxPackageBytes     = int64(512 << 20)
	DefaultMaxMarketBytes      = int64(2 << 30)
	DefaultMaxMarketPackages   = 512
	MaxPluginIDBytes           = 190
	MaxPluginVersionBytes      = 64
	MaxPackagePathBytes        = 2048
	MaxPermissionNameBytes     = 190
	MaxPermissionResourceBytes = 512
	MaxArtifactBytes           = int64(128 << 20)
	MaxRuntimeMemoryBytes      = int64(4 << 30)
	MaxRuntimeIOBytes          = int64(16 << 20)
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	hexDigestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func IsSemanticVersion(value string) bool {
	_, ok := parseVersion(value)
	return ok
}

// NormalizeBuildVersion accepts the release tooling's conventional v-prefix
// while preserving strict SemVer everywhere else.
func NormalizeBuildVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "dev" {
		return "0.0.0-dev", nil
	}
	if strings.HasPrefix(value, "v") && IsSemanticVersion(value[1:]) {
		return value[1:], nil
	}
	if IsSemanticVersion(value) {
		return value, nil
	}
	return "", fmt.Errorf("build version %q is not semantic version", value)
}

// CheckAgentCompatibility validates a concrete Agent-reported version against
// the package constraint. Callers must not substitute the control-plane build.
func CheckAgentCompatibility(version, constraint string) error {
	if !IsSemanticVersion(version) {
		return errors.New("agent version is missing or invalid")
	}
	if !validCompatibilityConstraint(constraint) || !versionSatisfies(version, constraint) {
		return fmt.Errorf("agent %s is outside %s", version, constraint)
	}
	return nil
}

type ValidatorOptions struct {
	HostVersion            string
	AgentVersion           string
	MaxFiles               int
	MaxPackageBytes        int64
	MaxFileBytes           int64
	MaxMarketFiles         int
	MaxMarketBytes         int64
	MaxMarketPackages      int
	AllowedPermissions     []string
	AllowedExtensionPoints []string
	TrustedSigners         map[string]ed25519.PublicKey
	TrustedSignerPolicy    TrustedSignerPolicy
	SupportedABIs          []string
	TargetGOOS             string
	TargetGOARCH           string
}

// TrustedSignerPolicy controls whether a validator implicitly trusts the
// built-in official root in addition to the explicitly supplied keys. Custom
// marketplace sources must use TrustedSignerPolicyExact so that a package can
// only be verified by the immutable signer bound to that source.
type TrustedSignerPolicy uint8

const (
	TrustedSignerPolicyOfficialRoot TrustedSignerPolicy = iota
	TrustedSignerPolicyExact
)

type Validator struct {
	options         ValidatorOptions
	permissions     map[string]struct{}
	extensionPoints map[string]struct{}
	trustedSigners  map[string]ed25519.PublicKey
	supportedABIs   map[string]struct{}
	snapshotHook    func(stage, sourceRoot, snapshotRoot string)
}

// SignerIdentity is an immutable projection of a trusted signing root.
// PublicKey is always cloned when it crosses the validator boundary.
type SignerIdentity struct {
	KeyID     string
	PublicKey ed25519.PublicKey
}

func OfficialSignerIdentity() SignerIdentity {
	key, _ := hex.DecodeString(officialSignaturePublicKeyHex)
	return SignerIdentity{KeyID: OfficialSignatureKeyID, PublicKey: append(ed25519.PublicKey(nil), key...)}
}

func NewValidator(options ValidatorOptions) *Validator {
	if options.MaxFiles <= 0 {
		options.MaxFiles = DefaultMaxPackageFiles
	}
	if options.MaxPackageBytes <= 0 {
		options.MaxPackageBytes = DefaultMaxPackageBytes
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = DefaultMaxFileBytes
	}
	if options.MaxMarketFiles <= 0 {
		options.MaxMarketFiles = DefaultMaxMarketFiles
	}
	if options.MaxMarketBytes <= 0 {
		options.MaxMarketBytes = DefaultMaxMarketBytes
	}
	if options.MaxMarketPackages <= 0 {
		options.MaxMarketPackages = DefaultMaxMarketPackages
	}
	if len(options.AllowedPermissions) == 0 {
		options.AllowedPermissions = []string{
			"agent.read", "agent.configure", "event.emit", "http.inspect", "http.respond",
			pluginsdk.PermissionHTTPOutbound,
			"l4.inspect", "l4.respond", "policy.read", "policy.write", "secret.use",
			"storage.read", "storage.write", "container.read", "container.manage", "dns.manage",
			string(pluginsdk.CapabilityPolicyAtomicState), string(pluginsdk.CapabilityPolicyMonotonicClock),
			string(pluginsdk.CapabilityPolicyTrustedSource), string(pluginsdk.CapabilityServiceRevocableResourceHandle),
			string(pluginsdk.CapabilityUIDynamicActions),
		}
	}
	if len(options.AllowedExtensionPoints) == 0 {
		options.AllowedExtensionPoints = []string{
			"http.request", "http.response", "l4.accept", "policy.provider", "dns.provider",
			pluginsdk.ExtensionHTTPBackendProvider,
			"container.provider", "tunnel.provider", "ui.route",
		}
	}
	if len(options.SupportedABIs) == 0 {
		options.SupportedABIs = []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}
	}
	v := &Validator{
		options: options, permissions: map[string]struct{}{}, extensionPoints: map[string]struct{}{},
		trustedSigners: map[string]ed25519.PublicKey{}, supportedABIs: map[string]struct{}{},
	}
	official := OfficialSignerIdentity()
	officialKey := official.PublicKey
	validTrustPolicy := options.TrustedSignerPolicy == TrustedSignerPolicyOfficialRoot || options.TrustedSignerPolicy == TrustedSignerPolicyExact
	if options.TrustedSignerPolicy == TrustedSignerPolicyOfficialRoot {
		v.trustedSigners[official.KeyID] = append(ed25519.PublicKey(nil), officialKey...)
	}
	for _, name := range options.AllowedPermissions {
		v.permissions[strings.TrimSpace(name)] = struct{}{}
	}
	for _, name := range options.AllowedExtensionPoints {
		v.extensionPoints[strings.TrimSpace(name)] = struct{}{}
	}
	acceptedSigners := make(map[string]ed25519.PublicKey, len(options.TrustedSigners))
	for id, key := range options.TrustedSigners {
		if !validTrustPolicy || ValidateSignerKeyID(id) != nil || len(key) != ed25519.PublicKeySize {
			continue
		}
		if id == OfficialSignatureKeyID && !bytes.Equal(key, officialKey) {
			continue
		}
		v.trustedSigners[id] = append(ed25519.PublicKey(nil), key...)
		acceptedSigners[id] = append(ed25519.PublicKey(nil), key...)
	}
	v.options.TrustedSigners = acceptedSigners
	for _, abi := range options.SupportedABIs {
		v.supportedABIs[strings.TrimSpace(abi)] = struct{}{}
	}
	return v
}

// ValidateSignerKeyID applies the single canonical signer identity grammar
// shared by source, manifest, market-index, and CLI validation. It rejects
// normalization-dependent identities so persisted and signed representations
// always compare byte-for-byte.
func ValidateSignerKeyID(value string) error {
	if len(value) == 0 || len(value) > MaxPermissionNameBytes || !identifierPattern.MatchString(value) {
		return errors.New("signer key identity must be a lowercase canonical identifier")
	}
	return nil
}

// WithTrustedSigners clones the validator's complete compatibility, platform,
// permission, ABI, and resource-limit configuration while replacing its custom
// signer set. The policy is explicit so custom-source validation cannot
// accidentally inherit the built-in official root.
func (v *Validator) WithTrustedSigners(signers map[string]ed25519.PublicKey, policy TrustedSignerPolicy) *Validator {
	options := v.options
	options.AllowedPermissions = append([]string(nil), v.options.AllowedPermissions...)
	options.AllowedExtensionPoints = append([]string(nil), v.options.AllowedExtensionPoints...)
	options.SupportedABIs = append([]string(nil), v.options.SupportedABIs...)
	options.TrustedSigners = make(map[string]ed25519.PublicKey, len(signers))
	for id, key := range signers {
		options.TrustedSigners[id] = append(ed25519.PublicKey(nil), key...)
	}
	options.TrustedSignerPolicy = policy
	return NewValidator(options)
}

// DefaultTrustedSigners returns a copy of the built-in official verification
// roots. Custom-market keys must be explicitly supplied in ValidatorOptions.
func DefaultTrustedSigners() map[string]ed25519.PublicKey {
	official := OfficialSignerIdentity()
	return map[string]ed25519.PublicKey{official.KeyID: append(ed25519.PublicKey(nil), official.PublicKey...)}
}

func (v *Validator) ValidatePackage(root string, expected PackageExpectation) (result ValidatedPackage, resultErr error) {
	snapshot, err := createPackageSnapshot(root, v.options)
	if err != nil {
		return ValidatedPackage{}, err
	}
	defer func() {
		if err := os.RemoveAll(snapshot.temporaryRoot); err != nil {
			result = ValidatedPackage{}
			resultErr = errors.Join(resultErr, validationError("snapshot_cleanup", ".", err))
		}
	}()
	v.runSnapshotHook("copied", snapshot.sourceRoot, snapshot.packageRoot)
	result, resultErr = v.validatePackageSnapshot(snapshot.packageRoot, snapshot.sourceRoot, expected)
	if resultErr != nil {
		return ValidatedPackage{}, resultErr
	}
	v.runSnapshotHook("validated", snapshot.sourceRoot, snapshot.packageRoot)
	if err := snapshot.verifySource(); err != nil {
		return ValidatedPackage{}, err
	}
	result.Root = snapshot.sourceRoot
	return result, nil
}

// ValidateDirectPlugin validates a repository whose root is one plugin
// package without synthesizing a marketplace entry.
func (v *Validator) ValidateDirectPlugin(root string, officialSource bool) (ValidatedDirectPlugin, error) {
	data, err := readBoundedFile(filepath.Join(root, PackageManifestFile), v.options.MaxFileBytes)
	if err != nil {
		return ValidatedDirectPlugin{}, validationError("manifest", PackageManifestFile, err)
	}
	var manifest Manifest
	if err := decodeStrictYAML(data, &manifest); err != nil {
		return ValidatedDirectPlugin{}, validationError("manifest_schema", PackageManifestFile, err)
	}
	artifacts := make([]ArtifactIndex, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts = append(artifacts, ArtifactIndex{SHA256: artifact.SHA256, Size: artifact.Size, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH})
	}
	expected := PackageExpectation{
		ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility,
		Capabilities: append([]string(nil), manifest.ExtensionPoints...),
		Runtime:      RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, PolicyKind: manifest.Runtime.PolicyKind},
		Artifacts:    artifacts, SignatureKeyID: manifest.Signature.KeyID,
	}
	validated, err := v.ValidatePackage(root, expected)
	if err != nil {
		return ValidatedDirectPlugin{}, err
	}
	projection := DirectPluginSnapshot{
		ID: manifest.ID, Version: manifest.Version, Description: manifest.Description,
		Capabilities: append([]string(nil), manifest.ExtensionPoints...), Compatibility: manifest.Compatibility,
		Runtime: expected.Runtime, Artifacts: artifacts, PackageSHA256: validated.Digest,
		SignatureKeyID: manifest.Signature.KeyID, Provenance: "custom", Official: officialSource,
	}
	if officialSource {
		projection.Provenance = "sakullla-plugins"
	}
	return ValidatedDirectPlugin{Projection: projection, Package: validated}, nil
}

func (v *Validator) runSnapshotHook(stage, sourceRoot, snapshotRoot string) {
	if v.snapshotHook != nil {
		v.snapshotHook(stage, sourceRoot, snapshotRoot)
	}
}

func (v *Validator) validatePackageSnapshot(root, sourceRoot string, expected PackageExpectation) (ValidatedPackage, error) {
	official, err := hasOfficialPackageEnvelopeV1(root)
	if err != nil {
		return ValidatedPackage{}, err
	}
	if official {
		return v.validateOfficialPackageV1Snapshot(root, sourceRoot, expected)
	}
	stats, err := inspectPackageTree(root, v.options)
	if err != nil {
		return ValidatedPackage{}, err
	}
	return v.validatePackageContent(root, sourceRoot, expected, stats, v.validateLegacyPackageDigest, v.verifyPackageSignature)
}

func (v *Validator) validateLegacyPackageDigest(root string, expected PackageExpectation) (string, error) {
	digest, err := ComputePackageDigest(root)
	if err != nil {
		return "", err
	}
	declaredData, err := readBoundedFile(filepath.Join(root, PackageDigestFile), 4096)
	if err != nil {
		return "", validationError("checksum_missing", PackageDigestFile, err)
	}
	declared := strings.ToLower(strings.TrimSpace(string(declaredData)))
	if !hexDigestPattern.MatchString(declared) || !strings.EqualFold(digest, declared) {
		return "", validationError("checksum_mismatch", PackageDigestFile, fmt.Errorf("declared %q does not match computed digest", declared))
	}
	if expected.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(expected.SHA256), digest) {
		return "", validationError("index_checksum_mismatch", PackageDigestFile, fmt.Errorf("market digest does not match package"))
	}
	return digest, nil
}

func (v *Validator) validatePackageContent(root, sourceRoot string, expected PackageExpectation, stats packageStats, packageDigest func(string, PackageExpectation) (string, error), verifySignature func(string, Manifest, string, string) error) (ValidatedPackage, error) {
	manifestPath := filepath.Join(root, PackageManifestFile)
	manifestData, err := readBoundedFile(manifestPath, v.options.MaxFileBytes)
	if err != nil {
		return ValidatedPackage{}, validationError("manifest", PackageManifestFile, err)
	}
	var manifest Manifest
	if err := decodeStrictYAML(manifestData, &manifest); err != nil {
		return ValidatedPackage{}, validationError("manifest_schema", PackageManifestFile, err)
	}
	v.runSnapshotHook("manifest", sourceRoot, root)
	if err := v.validateManifest(root, manifest, expected); err != nil {
		return ValidatedPackage{}, err
	}

	schemaPath, err := securePackagePath(root, manifest.ConfigSchema)
	if err != nil {
		return ValidatedPackage{}, validationError("config_schema_path", manifest.ConfigSchema, err)
	}
	schemaData, err := readBoundedFile(schemaPath, v.options.MaxFileBytes)
	if err != nil {
		return ValidatedPackage{}, validationError("config_schema", manifest.ConfigSchema, err)
	}
	var schema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(schemaData))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return ValidatedPackage{}, validationError("config_schema", manifest.ConfigSchema, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values are forbidden")
		}
		return ValidatedPackage{}, validationError("config_schema", manifest.ConfigSchema, err)
	}
	if err := validateJSONSchema(schema); err != nil {
		return ValidatedPackage{}, validationError("config_schema", manifest.ConfigSchema, err)
	}
	dynamicActions := []pluginsdk.DynamicAction(nil)
	var declarativeUI *DeclarativeUIDocument
	if manifest.UISchema != "" {
		uiPath, err := securePackagePath(root, manifest.UISchema)
		if err != nil {
			return ValidatedPackage{}, validationError("ui_schema", manifest.UISchema, err)
		}
		uiData, err := readBoundedFile(uiPath, v.options.MaxFileBytes)
		if err != nil {
			return ValidatedPackage{}, validationError("ui_schema", manifest.UISchema, err)
		}
		if err := validateDeclarativeUIWithActions(uiData, schema, manifest.Permissions, &dynamicActions); err != nil {
			return ValidatedPackage{}, validationError("ui_schema", manifest.UISchema, err)
		}
		declarativeUI, err = ProjectDeclarativeUI(uiData, schema, manifest.Permissions)
		if err != nil {
			return ValidatedPackage{}, validationError("ui_schema", manifest.UISchema, err)
		}
	}
	v.runSnapshotHook("schema", sourceRoot, root)

	digest, err := packageDigest(root, expected)
	if err != nil {
		return ValidatedPackage{}, err
	}
	v.runSnapshotHook("digest", sourceRoot, root)
	if err := verifySignature(root, manifest, digest, expected.SignatureKeyID); err != nil {
		return ValidatedPackage{}, err
	}
	v.runSnapshotHook("signature", sourceRoot, root)
	if expected.Capabilities != nil && !sameStringSet(expected.Capabilities, manifest.ExtensionPoints) {
		return ValidatedPackage{}, validationError("capability_mismatch", PackageManifestFile, errors.New("market capabilities differ from signed manifest extension points"))
	}
	return ValidatedPackage{Manifest: manifest, Digest: digest, Root: root, FileCount: stats.files, Size: stats.bytes, ConfigSchema: schema, DynamicActions: dynamicActions, DeclarativeUI: declarativeUI}, nil
}

func (v *Validator) verifyPackageSignature(root string, manifest Manifest, digest, expectedKeyID string) error {
	if manifest.Signature.Algorithm != "ed25519" || strings.TrimSpace(manifest.Signature.KeyID) == "" || manifest.Signature.File != PackageSignatureFile {
		return validationError("signature", PackageManifestFile, errors.New("detached ed25519 package.sig signature identity is required"))
	}
	if ValidateSignerKeyID(manifest.Signature.KeyID) != nil {
		return validationError("signature_identity", PackageManifestFile, errors.New("signature key identity is invalid"))
	}
	if expectedKeyID != "" && manifest.Signature.KeyID != expectedKeyID {
		return validationError("signature_identity", PackageManifestFile, errors.New("manifest signer differs from market signer"))
	}
	publicKey, ok := v.trustedSigners[manifest.Signature.KeyID]
	if !ok {
		return validationError("signature_identity", PackageSignatureFile, fmt.Errorf("signer %q is not trusted", manifest.Signature.KeyID))
	}
	encoded, err := readBoundedFile(filepath.Join(root, PackageSignatureFile), 4096)
	if err != nil {
		return validationError("signature_missing", PackageSignatureFile, err)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return validationError("signature", PackageSignatureFile, errors.New("signature must be canonical base64 Ed25519 bytes"))
	}
	if !ed25519.Verify(publicKey, []byte(digest), signature) {
		return validationError("signature_mismatch", PackageSignatureFile, errors.New("detached package signature does not match canonical digest"))
	}
	return nil
}

// ValidatePackageIntegrity applies the full package, schema, permission and
// digest contract without requiring the package to remain compatible with the
// control-plane version that happens to be running now. This is used for safe
// inspection and cleanup of an already-installed package.
func (v *Validator) ValidatePackageIntegrity(root string, expected PackageExpectation) (ValidatedPackage, error) {
	options := v.options
	options.HostVersion = ""
	options.AgentVersion = ""
	return NewValidator(options).ValidatePackage(root, expected)
}

func (v *Validator) ValidateMarket(root string, officialSource bool) (ValidatedMarket, error) {
	return v.ValidateMarketWithManifestDigest(root, officialSource, "")
}

// ValidateMarketWithManifestDigest validates a complete market from one
// private, budgeted filesystem snapshot. When expectedManifestDigest is
// present, the market.yaml digest check is performed inside that same snapshot
// before the manifest, tree, or packages are consumed. This is the official
// lock entrypoint; callers must not pre-hash a mutable checkout and then invoke
// ordinary market validation.
func (v *Validator) ValidateMarketWithManifestDigest(root string, officialSource bool, expectedManifestDigest string) (result ValidatedMarket, resultErr error) {
	snapshot, err := createMarketSnapshot(root, v.options)
	if err != nil {
		return ValidatedMarket{}, err
	}
	defer func() {
		if err := os.RemoveAll(snapshot.temporaryRoot); err != nil {
			result = ValidatedMarket{}
			resultErr = errors.Join(resultErr, validationError("snapshot_cleanup", ".", err))
		}
	}()
	v.runSnapshotHook("market_copied", snapshot.sourceRoot, snapshot.packageRoot)
	result, resultErr = v.validateMarketSnapshot(snapshot.packageRoot, snapshot.sourceRoot, officialSource, expectedManifestDigest)
	if resultErr != nil {
		return ValidatedMarket{}, resultErr
	}
	v.runSnapshotHook("market_validated", snapshot.sourceRoot, snapshot.packageRoot)
	if err := snapshot.verifySource(); err != nil {
		return ValidatedMarket{}, err
	}
	for index := range result.Packages {
		packagePath := filepath.Clean(filepath.Join(snapshot.sourceRoot, filepath.FromSlash(result.Manifest.Entries[index].PackagePath)))
		result.Packages[index].Root = packagePath
	}
	return result, nil
}

func (v *Validator) validateMarketSnapshot(root, sourceRoot string, officialSource bool, expectedManifestDigest string) (ValidatedMarket, error) {
	data, err := readBoundedFile(filepath.Join(root, MarketManifestFile), v.options.MaxFileBytes)
	if err != nil {
		return ValidatedMarket{}, validationError("market_manifest", MarketManifestFile, err)
	}
	if expectedManifestDigest != "" {
		digest := sha256.Sum256(data)
		actual := hex.EncodeToString(digest[:])
		if !hexDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(expectedManifestDigest))) || !strings.EqualFold(actual, strings.TrimSpace(expectedManifestDigest)) {
			return ValidatedMarket{}, validationError("market_digest", MarketManifestFile, errors.New("market manifest digest does not match immutable expectation"))
		}
	}
	v.runSnapshotHook("market_manifest", sourceRoot, root)
	if officialSource {
		return v.validateOfficialMarketV1Snapshot(root, sourceRoot, data)
	}
	var manifest MarketManifest
	if err := decodeStrictYAML(data, &manifest); err != nil {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, err)
	}
	if manifest.SchemaVersion != 1 || strings.TrimSpace(manifest.Name) == "" {
		return ValidatedMarket{}, validationError("market_schema", MarketManifestFile, errors.New("schema_version 1 and name are required"))
	}
	if len(manifest.Entries) > v.options.MaxMarketPackages {
		return ValidatedMarket{}, validationError("market_budget", MarketManifestFile, errors.New("market package count exceeds limit"))
	}
	preflightSeen := map[string]struct{}{}
	for index, entry := range manifest.Entries {
		key := entry.ID + "@" + entry.Version
		if _, exists := preflightSeen[key]; exists {
			return ValidatedMarket{}, validationError("duplicate_entry", MarketManifestFile, fmt.Errorf("duplicate %s", key))
		}
		preflightSeen[key] = struct{}{}
		if !identifierPattern.MatchString(entry.ID) || !IsSemanticVersion(entry.Version) || !hexDigestPattern.MatchString(strings.ToLower(entry.PackageSHA256)) {
			return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d has invalid identity, version, or digest", index))
		}
		if len(entry.ID) > MaxPluginIDBytes || len(entry.Version) > MaxPluginVersionBytes || len(entry.PackagePath) > MaxPackagePathBytes {
			return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d exceeds persistence field limits", index))
		}
		if len(entry.Capabilities) == 0 || len(entry.Capabilities) > 64 {
			return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d requires a bounded capability list", index))
		}
		seenCapabilities := map[string]struct{}{}
		for _, capability := range entry.Capabilities {
			if !identifierPattern.MatchString(capability) {
				return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d has invalid capability %q", index, capability))
			}
			if _, duplicate := seenCapabilities[capability]; duplicate {
				return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d duplicates capability %q", index, capability))
			}
			seenCapabilities[capability] = struct{}{}
		}
		if entry.Official != officialSource {
			return ValidatedMarket{}, validationError("source_identity", MarketManifestFile, fmt.Errorf("entry %s official flag does not match source kind", key))
		}
		for _, capability := range entry.Capabilities {
			if capability != strings.TrimSpace(capability) {
				return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %s capability must use canonical whitespace", key))
			}
		}
		if officialSource && entry.Provenance != "sakullla-plugins" {
			return ValidatedMarket{}, validationError("source_identity", MarketManifestFile, fmt.Errorf("entry %s lacks official sakullla-plugins provenance", key))
		}
		if officialSource && entry.SignatureKeyID != OfficialSignatureKeyID {
			return ValidatedMarket{}, validationError("signature_identity", MarketManifestFile, fmt.Errorf("entry %s is not signed by the built-in official root", key))
		}
		if !officialSource && entry.Provenance != "custom" {
			return ValidatedMarket{}, validationError("source_identity", MarketManifestFile, fmt.Errorf("entry %s custom provenance is required", key))
		}
		if err := v.validateRuntimeIndex(entry.Runtime, entry.Artifacts); err != nil {
			return ValidatedMarket{}, validationError("market_runtime", MarketManifestFile, fmt.Errorf("entry %s: %w", key, err))
		}
		if strings.TrimSpace(entry.SignatureKeyID) == "" {
			return ValidatedMarket{}, validationError("signature_identity", MarketManifestFile, fmt.Errorf("entry %s signer is required", key))
		}
		if ValidateSignerKeyID(entry.SignatureKeyID) != nil {
			return ValidatedMarket{}, validationError("signature_identity", MarketManifestFile, fmt.Errorf("entry %s signer identity is invalid", key))
		}
	}
	if err := inspectMarketTree(root, manifest, v.options); err != nil {
		return ValidatedMarket{}, err
	}
	v.runSnapshotHook("market_tree", sourceRoot, root)
	seen := map[string]struct{}{}
	result := ValidatedMarket{Manifest: manifest, Packages: make([]ValidatedPackage, 0, len(manifest.Entries))}
	for index, entry := range manifest.Entries {
		key := entry.ID + "@" + entry.Version
		if _, exists := seen[key]; exists {
			return ValidatedMarket{}, validationError("duplicate_entry", MarketManifestFile, fmt.Errorf("duplicate %s", key))
		}
		seen[key] = struct{}{}
		if !identifierPattern.MatchString(entry.ID) || !IsSemanticVersion(entry.Version) || !hexDigestPattern.MatchString(strings.ToLower(entry.PackageSHA256)) {
			return ValidatedMarket{}, validationError("market_entry", MarketManifestFile, fmt.Errorf("entry %d has invalid identity, version, or digest", index))
		}
		if entry.Official != officialSource {
			return ValidatedMarket{}, validationError("source_identity", MarketManifestFile, fmt.Errorf("entry %s official flag does not match source kind", key))
		}
		packagePath, err := securePackagePath(root, entry.PackagePath)
		if err != nil {
			return ValidatedMarket{}, validationError("package_path", entry.PackagePath, err)
		}
		sourcePackagePath := filepath.Clean(filepath.Join(sourceRoot, filepath.FromSlash(entry.PackagePath)))
		validated, err := v.validatePackageSnapshot(packagePath, sourcePackagePath, PackageExpectation{
			ID: entry.ID, Version: entry.Version, SHA256: entry.PackageSHA256, Compatibility: entry.Compatibility,
			Capabilities: entry.Capabilities, Runtime: entry.Runtime, Artifacts: entry.Artifacts, SignatureKeyID: entry.SignatureKeyID,
		})
		if err != nil {
			return ValidatedMarket{}, err
		}
		result.Manifest.Entries[index].Capabilities = append([]string(nil), validated.Manifest.ExtensionPoints...)
		result.Packages = append(result.Packages, validated)
	}
	return result, nil
}

func (v *Validator) validateRuntimeIndex(runtime RuntimeIndex, artifacts []ArtifactIndex) error {
	if (v.options.TargetGOOS == "") != (v.options.TargetGOARCH == "") {
		return errors.New("target GOOS and GOARCH must be supplied together")
	}
	if err := v.validateRuntimeIdentity(runtime.Kind, runtime.ABI, runtime.HostScope); err != nil {
		return err
	}
	if err := validateRuntimePolicyKind(runtime.Kind, runtime.PolicyKind); err != nil {
		return err
	}
	if len(artifacts) == 0 {
		return errors.New("artifact platform matrix is required")
	}
	seen := map[string]struct{}{}
	for _, artifact := range artifacts {
		if !hexDigestPattern.MatchString(strings.ToLower(artifact.SHA256)) || artifact.Size <= 0 || artifact.Size > MaxArtifactBytes {
			return errors.New("artifact digest and bounded size are required")
		}
		platform := artifact.GOOS + "/" + artifact.GOARCH
		if runtime.Kind == pluginsdk.RuntimeWASMPolicy {
			if artifact.GOOS != "" || artifact.GOARCH != "" || len(artifacts) != 1 {
				return errors.New("wasm-policy requires one platform-neutral artifact")
			}
			platform = "wasm"
		} else if !validPlatform(artifact.GOOS, artifact.GOARCH) {
			return errors.New("rpc-service artifact requires an allowed GOOS/GOARCH")
		}
		if _, duplicate := seen[platform]; duplicate {
			return fmt.Errorf("duplicate artifact platform %s", platform)
		}
		seen[platform] = struct{}{}
	}
	if runtime.Kind == pluginsdk.RuntimeRPCService && v.options.TargetGOOS != "" && v.options.TargetGOARCH != "" {
		if _, ok := seen[v.options.TargetGOOS+"/"+v.options.TargetGOARCH]; !ok {
			return fmt.Errorf("target platform %s/%s is missing", v.options.TargetGOOS, v.options.TargetGOARCH)
		}
	}
	return nil
}

func inspectMarketTree(root string, manifest MarketManifest, options ValidatorOptions) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	packageRoots := make([]string, 0, len(manifest.Entries))
	seenRoots := map[string]struct{}{}
	for _, entry := range manifest.Entries {
		resolved, err := securePackagePath(root, entry.PackagePath)
		if err != nil {
			return validationError("package_path", entry.PackagePath, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return validationError("package_path", entry.PackagePath, errors.New("package path must be a directory"))
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, duplicate := seenRoots[relative]; duplicate {
			continue
		}
		for _, existing := range packageRoots {
			if strings.HasPrefix(relative+"/", existing+"/") || strings.HasPrefix(existing+"/", relative+"/") {
				return validationError("package_path", entry.PackagePath, errors.New("market package directories may not overlap"))
			}
		}
		seenRoots[relative] = struct{}{}
		packageRoots = append(packageRoots, relative)
	}
	files, totalBytes := 0, int64(0)
	regularFiles := newStableFileSet(options.MaxMarketFiles)
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ".git" || strings.HasPrefix(relative, ".git/") {
			return validationError("market_tree", relative, errors.New("Git metadata must not be persisted in a market snapshot"))
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return validationError("market_tree", relative, errors.New("market tree contains a non-regular file"))
		}
		if info.IsDir() {
			return nil
		}
		identity, err := stableRegularFileKey(name, info)
		if err != nil {
			return validationError("file_identity", relative, err)
		}
		if !regularFiles.add(identity) {
			return validationError("hardlink", relative, errors.New("hard-linked market files are forbidden"))
		}
		allowed := relative == MarketManifestFile
		for _, packageRoot := range packageRoots {
			if strings.HasPrefix(relative, packageRoot+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			return validationError("market_tree", relative, errors.New("market tree contains unreferenced content"))
		}
		files++
		totalBytes += info.Size()
		if files > options.MaxMarketFiles || totalBytes > options.MaxMarketBytes {
			return validationError("market_budget", relative, errors.New("market tree exceeds file or byte budget"))
		}
		return nil
	})
}

func (v *Validator) validateManifest(root string, manifest Manifest, expected PackageExpectation) error {
	if manifest.SchemaVersion != 1 || !identifierPattern.MatchString(manifest.ID) || !IsSemanticVersion(manifest.Version) {
		return validationError("manifest_schema", PackageManifestFile, errors.New("schema_version 1, a canonical id, and semantic version are required"))
	}
	if len(manifest.ID) > MaxPluginIDBytes || len(manifest.Version) > MaxPluginVersionBytes || len(manifest.ConfigSchema) > MaxPackagePathBytes {
		return validationError("manifest_schema", PackageManifestFile, errors.New("manifest identity, version, or schema path exceeds persistence limit"))
	}
	if expected.ID != "" && manifest.ID != expected.ID {
		return validationError("identity_mismatch", PackageManifestFile, fmt.Errorf("manifest id %q differs from index id %q", manifest.ID, expected.ID))
	}
	if expected.Version != "" && manifest.Version != expected.Version {
		return validationError("identity_mismatch", PackageManifestFile, fmt.Errorf("manifest version %q differs from index version %q", manifest.Version, expected.Version))
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.ConfigSchema) == "" || len(manifest.ExtensionPoints) == 0 {
		return validationError("manifest_schema", PackageManifestFile, errors.New("name, config_schema, and extension_points are required"))
	}
	if err := v.validateRuntime(root, manifest, expected); err != nil {
		return err
	}
	if err := validateCompatibility(manifest.Compatibility, expected.Compatibility, v.options); err != nil {
		return validationError("compatibility", PackageManifestFile, err)
	}
	seenPermissions := map[string]struct{}{}
	if len(manifest.Permissions) > 128 || len(manifest.ExtensionPoints) > 64 || len(manifest.Artifacts) > 64 || len(manifest.Assets) > 2048 || len(manifest.Migrations) > 256 {
		return validationError("manifest_budget", PackageManifestFile, errors.New("manifest collection exceeds its bounded contract"))
	}
	for _, permission := range manifest.Permissions {
		if permission.Name != strings.TrimSpace(permission.Name) || permission.Resource != strings.TrimSpace(permission.Resource) {
			return validationError("permission", PackageManifestFile, errors.New("permission name and resource must use canonical whitespace"))
		}
		if len(permission.Name) > MaxPermissionNameBytes || len(permission.Resource) > MaxPermissionResourceBytes {
			return validationError("permission", PackageManifestFile, errors.New("permission exceeds persistence field limit"))
		}
		if _, allowed := v.permissions[permission.Name]; !allowed {
			return validationError("permission", PackageManifestFile, fmt.Errorf("permission %q is not allowed", permission.Name))
		}
		resource := permission.Resource
		if resource == "*" || strings.Contains(resource, "..") || strings.ContainsAny(resource, "\r\n\x00") {
			return validationError("permission", PackageManifestFile, fmt.Errorf("permission %q has an unsafe resource scope", permission.Name))
		}
		key := permission.Name + "\x00" + resource
		if _, duplicate := seenPermissions[key]; duplicate {
			return validationError("permission", PackageManifestFile, fmt.Errorf("duplicate permission %q", permission.Name))
		}
		seenPermissions[key] = struct{}{}
	}
	seenPoints := map[string]struct{}{}
	for _, point := range manifest.ExtensionPoints {
		if point != strings.TrimSpace(point) {
			return validationError("extension_point", PackageManifestFile, errors.New("extension point must use canonical whitespace"))
		}
		if _, allowed := v.extensionPoints[point]; !allowed {
			return validationError("extension_point", PackageManifestFile, fmt.Errorf("extension point %q is not allowed", point))
		}
		if _, duplicate := seenPoints[point]; duplicate {
			return validationError("extension_point", PackageManifestFile, fmt.Errorf("duplicate extension point %q", point))
		}
		seenPoints[point] = struct{}{}
	}
	if err := pluginsdk.ValidateHTTPBackendProviderManifest(manifest); err != nil {
		return validationError("http_backend_provider", PackageManifestFile, err)
	}
	if err := validateCleanup(manifest.Cleanup); err != nil {
		return validationError("cleanup", PackageManifestFile, err)
	}
	paths := []string{manifest.ConfigSchema, manifest.Signature.File}
	if manifest.UISchema != "" {
		if manifest.UISchema != UISchemaFile && path.Ext(filepath.ToSlash(manifest.UISchema)) != ".json" {
			return validationError("ui_schema", manifest.UISchema, errors.New("UI schema must be a declarative JSON document"))
		}
		paths = append(paths, manifest.UISchema)
	}
	paths = append(paths, manifest.Assets...)
	for _, artifact := range manifest.Artifacts {
		paths = append(paths, artifact.Path)
	}
	for _, migration := range manifest.Migrations {
		if !IsSemanticVersion(migration.From) || !IsSemanticVersion(migration.To) || migration.From == migration.To {
			return validationError("migration", migration.File, errors.New("migration requires distinct semantic from/to versions"))
		}
	}
	if err := validateMigrationGraph(manifest.Version, manifest.Migrations); err != nil {
		return validationError("migration", PackageManifestFile, err)
	}
	for _, migration := range manifest.Migrations {
		if path.Ext(filepath.ToSlash(migration.File)) != ".json" || !strings.HasPrefix(filepath.ToSlash(migration.File), "migrations/") {
			return validationError("migration", migration.File, errors.New("only declarative JSON migrations under migrations/ are allowed"))
		}
		migrationPath, err := securePackagePath(root, migration.File)
		if err != nil {
			return validationError("migration", migration.File, err)
		}
		migrationLimit := v.options.MaxFileBytes
		if migrationLimit > MaxMigrationDocumentBytes {
			migrationLimit = MaxMigrationDocumentBytes
		}
		migrationData, err := readBoundedFile(migrationPath, migrationLimit)
		if err != nil {
			return validationError("migration", migration.File, err)
		}
		if err := validateMigrationDocument(migrationData); err != nil {
			return validationError("migration", migration.File, err)
		}
		paths = append(paths, migration.File)
	}
	for _, reference := range paths {
		if len(reference) > MaxPackagePathBytes {
			return validationError("path", reference, errors.New("package path exceeds persistence limit"))
		}
		resolved, err := securePackagePath(root, reference)
		if err != nil {
			return validationError("path", reference, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			if err == nil {
				err = errors.New("referenced path is a directory")
			}
			return validationError("path", reference, err)
		}
	}
	return nil
}

func validateMigrationGraph(targetVersion string, migrations []Migration) error {
	byFrom := make(map[string]Migration, len(migrations))
	for _, migration := range migrations {
		if migration.From == targetVersion {
			return fmt.Errorf("migration from target version %s cannot reach a newer package version", targetVersion)
		}
		if _, duplicate := byFrom[migration.From]; duplicate {
			return fmt.Errorf("duplicate migration from %s", migration.From)
		}
		byFrom[migration.From] = migration
	}
	for start := range byFrom {
		seen := make(map[string]struct{}, len(byFrom))
		for current := start; current != targetVersion; {
			if _, duplicate := seen[current]; duplicate {
				return fmt.Errorf("migration chain from %s contains a cycle at %s", start, current)
			}
			seen[current] = struct{}{}
			migration, ok := byFrom[current]
			if !ok {
				return fmt.Errorf("migration chain from %s terminates at %s instead of package version %s", start, current, targetVersion)
			}
			current = migration.To
		}
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		values[value] = struct{}{}
	}
	if len(values) != len(left) {
		return false
	}
	for _, value := range right {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func (v *Validator) validateRuntime(root string, manifest Manifest, expected PackageExpectation) error {
	if err := v.validateRuntimeIdentity(manifest.Runtime.Kind, manifest.Runtime.ABI, manifest.Runtime.HostScope); err != nil {
		return validationError("runtime", PackageManifestFile, err)
	}
	if err := validateRuntimePolicyKind(manifest.Runtime.Kind, manifest.Runtime.PolicyKind); err != nil {
		return validationError("runtime", PackageManifestFile, err)
	}
	if expected.Runtime.Kind != "" && (manifest.Runtime.Kind != expected.Runtime.Kind || manifest.Runtime.ABI != expected.Runtime.ABI || manifest.Runtime.HostScope != expected.Runtime.HostScope || manifest.Runtime.PolicyKind != expected.Runtime.PolicyKind) {
		return validationError("runtime_mismatch", PackageManifestFile, errors.New("manifest runtime differs from market index"))
	}
	if err := validateResourceBudget(manifest.Runtime.Kind, manifest.ResourceBudget); err != nil {
		return validationError("resource_budget", PackageManifestFile, err)
	}
	if err := validateFailurePolicy(manifest.Runtime.Kind, manifest.FailurePolicy); err != nil {
		return validationError("failure_policy", PackageManifestFile, err)
	}
	if len(manifest.Artifacts) == 0 {
		return validationError("artifact", PackageManifestFile, errors.New("at least one runtime artifact is required"))
	}
	seenPaths := map[string]struct{}{}
	index := make([]ArtifactIndex, 0, len(manifest.Artifacts))
	entryFound := false
	for _, artifact := range manifest.Artifacts {
		canonical := filepath.ToSlash(artifact.Path)
		if len(canonical) > MaxPackagePathBytes || !strings.HasPrefix(canonical, "artifacts/") {
			return validationError("artifact_path", artifact.Path, errors.New("artifact must be under artifacts/"))
		}
		if _, duplicate := seenPaths[canonical]; duplicate {
			return validationError("artifact", artifact.Path, errors.New("duplicate artifact path"))
		}
		seenPaths[canonical] = struct{}{}
		if !hexDigestPattern.MatchString(strings.ToLower(artifact.SHA256)) || artifact.Size <= 0 || artifact.Size > MaxArtifactBytes {
			return validationError("artifact", artifact.Path, errors.New("artifact digest and bounded size are required"))
		}
		if manifest.Runtime.Kind == pluginsdk.RuntimeWASMPolicy {
			if len(manifest.Artifacts) != 1 || artifact.Mode != "wasm" || artifact.GOOS != "" || artifact.GOARCH != "" || path.Ext(canonical) != ".wasm" || manifest.Runtime.Entry != canonical {
				return validationError("artifact", artifact.Path, errors.New("wasm-policy requires one platform-neutral wasm entry artifact"))
			}
			entryFound = true
		} else {
			extension := path.Ext(canonical)
			validName := (artifact.GOOS == "windows" && extension == ".exe") || (artifact.GOOS != "windows" && extension == "")
			platformPrefix := "artifacts/" + artifact.GOOS + "-" + artifact.GOARCH + "/"
			if artifact.Mode != "executable" || !validPlatform(artifact.GOOS, artifact.GOARCH) || !validName || !strings.HasPrefix(canonical, platformPrefix) {
				return validationError("artifact", artifact.Path, errors.New("rpc-service requires a native executable artifact for an allowed platform"))
			}
			base := strings.TrimSuffix(path.Base(canonical), ".exe")
			if base != manifest.Runtime.Entry {
				return validationError("runtime_entry", artifact.Path, errors.New("every RPC platform artifact must provide the declared logical entry"))
			}
			entryFound = true
		}
		resolved, err := securePackagePath(root, canonical)
		if err != nil {
			return validationError("artifact_path", artifact.Path, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return validationError("artifact", artifact.Path, errors.New("artifact must be a regular file"))
		}
		if info.Size() != artifact.Size {
			return validationError("artifact_size", artifact.Path, errors.New("declared artifact size does not match content"))
		}
		actual, err := digestFile(resolved)
		if err != nil || !strings.EqualFold(actual, artifact.SHA256) {
			return validationError("artifact_digest", artifact.Path, errors.New("declared artifact digest does not match content"))
		}
		if err := validateArtifactMagic(resolved, manifest.Runtime.Kind, artifact, manifest.ResourceBudget); err != nil {
			return validationError("artifact_format", artifact.Path, err)
		}
		index = append(index, ArtifactIndex{SHA256: strings.ToLower(artifact.SHA256), Size: artifact.Size, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH})
	}
	if !entryFound || strings.TrimSpace(manifest.Runtime.Entry) == "" {
		return validationError("runtime_entry", PackageManifestFile, errors.New("runtime entry does not resolve to every runtime kind's artifact contract"))
	}
	if err := v.validateRuntimeIndex(RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, PolicyKind: manifest.Runtime.PolicyKind}, index); err != nil {
		return validationError("artifact", PackageManifestFile, err)
	}
	if len(expected.Artifacts) > 0 && !sameArtifactIndex(index, expected.Artifacts) {
		return validationError("artifact_mismatch", PackageManifestFile, errors.New("manifest artifact matrix differs from market index"))
	}
	return nil
}

func (v *Validator) validateRuntimeIdentity(kind, abi, hostScope string) error {
	if _, ok := v.supportedABIs[abi]; !ok {
		return fmt.Errorf("ABI %q is not supported", abi)
	}
	switch kind {
	case pluginsdk.RuntimeWASMPolicy:
		if abi != pluginsdk.PolicyABIV1 || hostScope != pluginsdk.HostScopeAgent {
			return errors.New("wasm-policy requires nre:policy/v1 on the agent host")
		}
	case pluginsdk.RuntimeRPCService:
		if abi != pluginsdk.RPCABIV1 || (hostScope != pluginsdk.HostScopeAgent && hostScope != pluginsdk.HostScopeControlPlane) {
			return errors.New("rpc-service requires nre:rpc/v1 and an agent or control-plane host scope")
		}
	default:
		return fmt.Errorf("runtime kind %q is not allowed", kind)
	}
	return nil
}

func validateRuntimePolicyKind(runtimeKind, policyKind string) error {
	policyKind = strings.TrimSpace(policyKind)
	if runtimeKind != pluginsdk.RuntimeWASMPolicy {
		if policyKind != "" {
			return errors.New("policy_kind is only valid for wasm-policy")
		}
		return nil
	}
	switch policyKind {
	case "ip", "rate", "waf":
		return nil
	default:
		return errors.New("wasm-policy requires policy_kind ip, rate, or waf")
	}
}

func validateResourceBudget(kind string, budget ResourceBudget) error {
	switch kind {
	case pluginsdk.RuntimeWASMPolicy:
		if budget.CPUMillis != 0 || budget.Restarts != 0 {
			return errors.New("wasm-policy cannot declare process CPU or restart budgets")
		}
		return (pluginsdk.PolicyV1ResourceBudget{
			TimeoutMilliseconds: budget.TimeoutMS,
			MemoryBytes:         budget.MemoryBytes,
			Concurrency:         budget.Concurrency,
			InputFrameBytes:     budget.InputBytes,
			OutputFrameBytes:    budget.OutputBytes,
		}).Validate()
	case pluginsdk.RuntimeRPCService:
		if budget.TimeoutMS <= 0 || budget.TimeoutMS > 300000 || budget.MemoryBytes < 65536 || budget.MemoryBytes > MaxRuntimeMemoryBytes || budget.Concurrency <= 0 || budget.Concurrency > 4096 || budget.InputBytes <= 0 || budget.InputBytes > MaxRuntimeIOBytes || budget.OutputBytes <= 0 || budget.OutputBytes > MaxRuntimeIOBytes {
			return errors.New("RPC timeout, memory, concurrency, and IO budgets must be positive and within host limits")
		}
		if budget.CPUMillis <= 0 || budget.CPUMillis > 100000 || budget.Restarts < 0 || budget.Restarts > 100 {
			return errors.New("rpc-service requires bounded CPU and restart budgets")
		}
		return nil
	default:
		return fmt.Errorf("runtime kind %q does not have a resource budget contract", kind)
	}
}

func validateFailurePolicy(kind string, policy FailurePolicy) error {
	if policy.CoreFallback != "preserve" || (policy.OnError != "fail-open" && policy.OnError != "fail-closed" && policy.OnError != "degraded") || (policy.OnBudget != "fail-open" && policy.OnBudget != "fail-closed") {
		return errors.New("failure policy is outside the isolation allowlist")
	}
	if kind == pluginsdk.RuntimeWASMPolicy && policy.Restart != "never" {
		return errors.New("wasm-policy restart policy must be never")
	}
	if kind == pluginsdk.RuntimeRPCService && policy.Restart != "never" && policy.Restart != "on-failure" {
		return errors.New("rpc-service restart policy is outside the allowlist")
	}
	return nil
}

type packageStats struct {
	files int
	bytes int64
}

func inspectPackageTree(root string, options ValidatorOptions) (packageStats, error) {
	manifestData, err := readBoundedFile(filepath.Join(root, PackageManifestFile), options.MaxFileBytes)
	if err != nil {
		return packageStats{}, err
	}
	var manifest Manifest
	if err := decodeStrictYAML(manifestData, &manifest); err != nil {
		return packageStats{}, err
	}
	if _, err := securePackagePath(root, manifest.ConfigSchema); err != nil {
		return packageStats{}, validationError("path", manifest.ConfigSchema, err)
	}
	declared := map[string]struct{}{PackageManifestFile: {}, PackageDigestFile: {}, PackageSignatureFile: {}, filepath.ToSlash(manifest.ConfigSchema): {}}
	if manifest.UISchema != "" {
		declared[filepath.ToSlash(manifest.UISchema)] = struct{}{}
	}
	assets := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		asset = filepath.ToSlash(asset)
		if isExecutableName(asset) {
			return packageStats{}, validationError("executable", asset, errors.New("scripts and executable asset types are forbidden"))
		}
		if !isSafeAssetName(asset) {
			return packageStats{}, validationError("asset", asset, errors.New("asset type is outside the data-only allowlist"))
		}
		declared[asset] = struct{}{}
		assets[asset] = struct{}{}
	}
	for _, migration := range manifest.Migrations {
		declared[filepath.ToSlash(migration.File)] = struct{}{}
	}
	artifacts := make(map[string]Artifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		canonical := filepath.ToSlash(artifact.Path)
		declared[canonical] = struct{}{}
		artifacts[canonical] = artifact
	}
	var result packageStats
	regularFiles := newStableFileSet(options.MaxFiles)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return validationError("symlink", filepath.ToSlash(rel), errors.New("symbolic links are forbidden"))
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return validationError("file_type", filepath.ToSlash(rel), errors.New("only regular files are allowed"))
		}
		identity, err := stableRegularFileKey(current, info)
		if err != nil {
			return validationError("file_identity", filepath.ToSlash(rel), err)
		}
		if !regularFiles.add(identity) {
			return validationError("hardlink", filepath.ToSlash(rel), errors.New("hard-linked package files are forbidden"))
		}
		canonicalRel := filepath.ToSlash(rel)
		if _, ok := declared[canonicalRel]; !ok {
			return validationError("undeclared_payload", canonicalRel, errors.New("every package file must be declared by the manifest"))
		}
		result.files++
		result.bytes += info.Size()
		if result.files > options.MaxFiles || result.bytes > options.MaxPackageBytes || info.Size() > options.MaxFileBytes {
			return validationError("size_limit", filepath.ToSlash(rel), errors.New("package size or file count limit exceeded"))
		}
		_, runtimeArtifact := artifacts[canonicalRel]
		if info.Mode().Perm()&0o111 != 0 {
			return validationError("artifact_mode", filepath.ToSlash(rel), errors.New("package and cache files must not carry filesystem execute bits"))
		}
		if !runtimeArtifact && isExecutableName(rel) {
			return validationError("executable", filepath.ToSlash(rel), errors.New("only declared runtime artifacts may use executable file types"))
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		data := make([]byte, 8)
		read, readErr := io.ReadFull(file, data)
		closeErr := file.Close()
		data = data[:read]
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if !runtimeArtifact && hasExecutableMagic(data) {
			return validationError("executable", filepath.ToSlash(rel), errors.New("binary executable content is forbidden"))
		}
		if _, asset := assets[canonicalRel]; asset {
			if err := validateAssetContent(current, canonicalRel, options.MaxFileBytes); err != nil {
				return validationError("asset", canonicalRel, err)
			}
		}
		return nil
	})
	return result, err
}

// ComputePackageDigest hashes a canonical UTF-8 byte-sorted manifest of
// "sha256  declared-mode  relative/path\n" lines. The digest and detached
// signature files are excluded so neither is self-referential.
func ComputePackageDigest(root string) (string, error) {
	root, err := resolvePackageRoot(root)
	if err != nil {
		return "", err
	}
	artifactModes := map[string]string{}
	if data, readErr := os.ReadFile(filepath.Join(root, PackageManifestFile)); readErr == nil {
		var manifest Manifest
		if decodeErr := decodeStrictYAML(data, &manifest); decodeErr == nil {
			for _, artifact := range manifest.Artifacts {
				artifactModes[filepath.ToSlash(artifact.Path)] = artifact.Mode
			}
		}
	}
	var paths []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if rel == "." || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return validationError("symlink", filepath.ToSlash(rel), errors.New("symbolic links are forbidden"))
		}
		rel = filepath.ToSlash(rel)
		if rel == PackageDigestFile || rel == PackageSignatureFile {
			return nil
		}
		if !fs.ValidPath(rel) {
			return validationError("path", rel, errors.New("non-canonical path"))
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(paths, func(i, j int) bool { return bytes.Compare([]byte(paths[i]), []byte(paths[j])) < 0 })
	packageHash := sha256.New()
	for _, rel := range paths {
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		contentHash := sha256.New()
		_, copyErr := io.Copy(contentHash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		mode := "data"
		if declared, ok := artifactModes[rel]; ok {
			mode = declared
		}
		fmt.Fprintf(packageHash, "%x  %s  %s\n", contentHash.Sum(nil), mode, rel)
	}
	return hex.EncodeToString(packageHash.Sum(nil)), nil
}

// resolvePackageRoot rejects a package whose root entry is itself a link or
// non-directory before any manifest read or tree walk. Resolving permitted
// links in ancestor components gives all later package operations one stable
// path anchor while SameFile verifies that resolution retained the identity
// checked by Lstat.
func resolvePackageRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", validationError("package_root", ".", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", validationError("package_root", ".", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", validationError("symlink", ".", errors.New("package root must not be a symbolic link"))
	}
	if !info.IsDir() {
		return "", validationError("file_type", ".", errors.New("package root must be a directory"))
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", validationError("package_root", ".", err)
	}
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil {
		return "", validationError("package_root", ".", err)
	}
	if resolvedInfo.Mode()&os.ModeSymlink != 0 || !resolvedInfo.IsDir() || !os.SameFile(info, resolvedInfo) {
		return "", validationError("package_root", ".", errors.New("package root identity changed during resolution"))
	}
	return filepath.Clean(resolved), nil
}

func digestFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateArtifactMagic(name, kind string, artifact Artifact, budget ResourceBudget) error {
	if kind == pluginsdk.RuntimeWASMPolicy {
		return validatePolicyWASMArtifact(name, budget.MemoryBytes)
	}
	return validateRPCExecutable(name, artifact)
}

func validPlatform(goos, goarch string) bool {
	validOS := goos == "linux" || goos == "windows" || goos == "darwin" || goos == "freebsd"
	validArch := goarch == "amd64" || goarch == "arm64"
	return validOS && validArch
}

func sameArtifactIndex(left, right []ArtifactIndex) bool {
	if len(left) != len(right) {
		return false
	}
	key := func(value ArtifactIndex) string {
		return value.GOOS + "\x00" + value.GOARCH + "\x00" + strings.ToLower(value.SHA256) + "\x00" + strconv.FormatInt(value.Size, 10)
	}
	leftKeys, rightKeys := make([]string, len(left)), make([]string, len(right))
	for index := range left {
		leftKeys[index], rightKeys[index] = key(left[index]), key(right[index])
	}
	sort.Strings(leftKeys)
	sort.Strings(rightKeys)
	return slicesEqual(leftKeys, rightKeys)
}

func slicesEqual(left, right []string) bool {
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateCleanup(policy CleanupPolicy) error {
	allowed := map[string]map[string]struct{}{
		"instances": {"delete": {}, "retain": {}}, "config": {"delete": {}, "retain": {}},
		"owned_data": {"delete": {}, "retain": {}}, "grants": {"delete": {}, "retain": {}},
		"shared_refs": {"retain": {}}, "audit_events": {"retain": {}},
	}
	values := map[string]string{"instances": policy.Instances, "config": policy.Config, "owned_data": policy.OwnedData, "grants": policy.Grants, "shared_refs": policy.SharedRefs, "audit_events": policy.AuditEvents}
	for field, value := range values {
		if _, ok := allowed[field][strings.TrimSpace(value)]; !ok {
			return fmt.Errorf("invalid %s cleanup action %q", field, value)
		}
	}
	// The lifecycle store currently has one ownership boundary for mutable
	// plugin state. Mixed retention would leave ambiguous owned data behind, so
	// accept only the two combinations it can execute completely.
	mutable := []string{policy.Instances, policy.Config, policy.OwnedData, policy.Grants}
	for _, action := range mutable[1:] {
		if action != mutable[0] {
			return errors.New("instances, config, owned_data, and grants cleanup actions must all match")
		}
	}
	return nil
}

type migrationDocument struct {
	Operations []migrationOperation `json:"operations"`
}

type migrationOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

func validateMigrationDocument(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document migrationDocument
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple migration JSON values are forbidden")
		}
		return err
	}
	if len(document.Operations) == 0 || len(document.Operations) > 256 {
		return errors.New("migration must contain 1 to 256 operations")
	}
	for index, operation := range document.Operations {
		if !validJSONPointer(operation.Path) {
			return fmt.Errorf("operation %d has an invalid JSON pointer", index)
		}
		switch operation.Op {
		case "set":
			if len(operation.Value) == 0 || operation.From != "" {
				return fmt.Errorf("set operation %d requires value and forbids from", index)
			}
			var value any
			if err := json.Unmarshal(operation.Value, &value); err != nil {
				return fmt.Errorf("set operation %d value: %w", index, err)
			}
		case "remove":
			if len(operation.Value) != 0 || operation.From != "" {
				return fmt.Errorf("remove operation %d accepts only path", index)
			}
		case "copy", "rename":
			if !validJSONPointer(operation.From) || len(operation.Value) != 0 {
				return fmt.Errorf("%s operation %d requires from and forbids value", operation.Op, index)
			}
			if jsonPointerUsesArrayIndex(operation.From) || jsonPointerUsesArrayIndex(operation.Path) {
				return fmt.Errorf("%s operation %d does not support array indices", operation.Op, index)
			}
			if operation.Op == "rename" && jsonPointersOverlap(operation.From, operation.Path) {
				return fmt.Errorf("rename operation %d cannot use overlapping paths", index)
			}
		default:
			return fmt.Errorf("operation %d uses forbidden migration op %q", index, operation.Op)
		}
	}
	return nil
}

func jsonPointerUsesArrayIndex(value string) bool {
	if value == "" {
		return false
	}
	for _, token := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if token == "-" {
			return true
		}
		if index, err := strconv.Atoi(token); err == nil && index >= 0 {
			return true
		}
	}
	return false
}

func jsonPointersOverlap(left, right string) bool {
	if left == right || left == "" || right == "" {
		return true
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func validJSONPointer(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") || len(value) > 1024 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			continue
		}
		if index+1 >= len(value) || (value[index+1] != '0' && value[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

func validateJSONSchema(schema map[string]any) error {
	if len(schema) == 0 {
		return errors.New("schema must be an object")
	}
	if schemaType, ok := schema["type"].(string); !ok || schemaType != "object" {
		return errors.New("root schema type must be object")
	}
	return validateSchemaNode(schema, true, false)
}

// ValidateConfigWritableInput rejects client-owned values for schema nodes
// declared readOnly. It is intentionally separate from response projection:
// readOnly fields remain part of the single declarative schema for display,
// but clients cannot persist or stage them through configure operations.
func ValidateConfigWritableInput(schema map[string]any, raw json.RawMessage) error {
	if err := validateJSONSchema(schema); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("config must be one JSON value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values are forbidden")
		}
		return fmt.Errorf("config must be one JSON value: %w", err)
	}
	return rejectReadOnlyConfigValue(schema, value, "")
}

func rejectReadOnlyConfigValue(schema map[string]any, value any, pointer string) error {
	readOnly, err := schemaBooleanAnnotation(schema, "readOnly")
	if err != nil {
		return err
	}
	if readOnly {
		if pointer == "" {
			pointer = "/"
		}
		return fmt.Errorf("config property %q is readOnly and cannot be submitted", pointer)
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for name, childValue := range typed {
			childSchema, ok := properties[name].(map[string]any)
			if !ok {
				continue
			}
			childPointer := pointer + "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
			if err := rejectReadOnlyConfigValue(childSchema, childValue, childPointer); err != nil {
				return err
			}
		}
	case []any:
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema == nil {
			return nil
		}
		for index, childValue := range typed {
			if err := rejectReadOnlyConfigValue(itemSchema, childValue, pointer+"/"+strconv.Itoa(index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSchemaNode(schema map[string]any, root, namedObjectProperty bool) error {
	allowed := map[string]bool{"$schema": true, "type": true, "enum": true, "title": true, "description": true, "default": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "minItems": true, "maxItems": true, "uniqueItems": true, "minLength": true, "maxLength": true, "pattern": true, "minimum": true, "maximum": true, "multipleOf": true, "readOnly": true, "writeOnly": true}
	for keyword := range schema {
		if !allowed[keyword] {
			return fmt.Errorf("unsupported JSON Schema keyword %q", keyword)
		}
	}
	if dialect, ok := schema["$schema"]; ok {
		if !root {
			return errors.New("$schema is only allowed at the root")
		}
		if dialect != "https://json-schema.org/draft/2020-12/schema" {
			return fmt.Errorf("unsupported JSON Schema dialect %q", dialect)
		}
	}
	typeName, hasType := schema["type"].(string)
	if root && (!hasType || typeName != "object") {
		return errors.New("root schema type must be object")
	}
	if hasType {
		switch typeName {
		case "object", "array", "string", "integer", "number", "boolean", "null":
		default:
			return fmt.Errorf("unsupported JSON Schema type %q", typeName)
		}
	}
	if rawPattern, ok := schema["pattern"]; ok {
		pattern, valid := rawPattern.(string)
		if !valid || !hasType || typeName != "string" {
			return errors.New("pattern requires a string schema")
		}
		if len(pattern) > 1024 {
			return errors.New("pattern exceeds 1024 bytes")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("pattern is invalid: %w", err)
		}
	}
	if rawUnique, ok := schema["uniqueItems"]; ok {
		unique, valid := rawUnique.(bool)
		if !valid || !hasType || typeName != "array" {
			return errors.New("uniqueItems requires a boolean on an array schema")
		}
		if unique {
			maximum, bounded := nonNegativeIntegerBound(schema["maxItems"])
			if !bounded || maximum > maxUniqueConfigItems {
				return fmt.Errorf("uniqueItems requires maxItems no greater than %d", maxUniqueConfigItems)
			}
		}
	}
	if enum, ok := schema["enum"]; ok {
		values, valid := enum.([]any)
		if !valid || len(values) == 0 || len(values) > maxConfigEnumValues {
			return fmt.Errorf("enum must contain 1 to %d values", maxConfigEnumValues)
		}
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			key, err := jsonSemanticKey(value)
			if err != nil {
				return fmt.Errorf("enum value is invalid: %w", err)
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("enum values must be unique under JSON numeric equality")
			}
			seen[key] = struct{}{}
		}
	}
	if properties, ok := schema["properties"]; ok {
		children, valid := properties.(map[string]any)
		if !valid || !hasType || typeName != "object" {
			return errors.New("properties requires an object schema")
		}
		for name, child := range children {
			childSchema, valid := child.(map[string]any)
			if !valid {
				return fmt.Errorf("property %q schema must be an object", name)
			}
			if err := validateSchemaNode(childSchema, false, true); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
		}
	}
	if required, ok := schema["required"]; ok {
		if !hasType || typeName != "object" {
			return errors.New("required requires an object schema")
		}
		items, valid := required.([]any)
		if !valid {
			return errors.New("required must be an array")
		}
		for _, item := range items {
			if _, valid := item.(string); !valid {
				return errors.New("required entries must be strings")
			}
		}
	}
	if additional, ok := schema["additionalProperties"]; ok {
		if _, valid := additional.(bool); !valid || !hasType || typeName != "object" {
			return errors.New("additionalProperties must be boolean")
		}
	}
	if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range stringList(schema["required"]) {
			if _, exists := properties[name]; !exists {
				return fmt.Errorf("required property %q is absent from properties while additionalProperties is false", name)
			}
		}
	}
	readOnly, err := schemaBooleanAnnotation(schema, "readOnly")
	if err != nil {
		return err
	}
	if root && readOnly {
		return errors.New("root config schema cannot be readOnly")
	}
	if readOnly && !namedObjectProperty {
		return errors.New("readOnly is only valid on named object properties")
	}
	writeOnly, err := schemaBooleanAnnotation(schema, "writeOnly")
	if err != nil {
		return err
	}
	if readOnly && writeOnly {
		return errors.New("readOnly and writeOnly cannot both be true")
	}
	// writeOnly values are accepted only through the control-plane broker; the
	// ordinary config document and read DTO never carry their plaintext.
	if typeName == "object" {
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range stringList(schema["required"]) {
			childSchema, ok := properties[name].(map[string]any)
			if !ok {
				continue
			}
			childReadOnly, err := schemaBooleanAnnotation(childSchema, "readOnly")
			if err != nil {
				return fmt.Errorf("required property %q: %w", name, err)
			}
			if childReadOnly {
				return fmt.Errorf("required property %q cannot be readOnly", name)
			}
		}
	}
	var minimum, maximum, multiple *big.Rat
	for _, keyword := range []string{"minimum", "maximum", "multipleOf"} {
		value, ok := schema[keyword]
		if !ok {
			continue
		}
		if typeName != "integer" && typeName != "number" {
			return fmt.Errorf("%s is not valid for schema type %q", keyword, typeName)
		}
		number, valid := exactNumber(value)
		if !valid {
			return fmt.Errorf("%s must be a finite JSON number", keyword)
		}
		switch keyword {
		case "minimum":
			minimum = number
		case "maximum":
			maximum = number
		case "multipleOf":
			if number.Sign() <= 0 {
				return errors.New("multipleOf must be positive")
			}
			multiple = number
		}
	}
	if minimum != nil && maximum != nil && minimum.Cmp(maximum) > 0 {
		return errors.New("minimum exceeds maximum")
	}
	if items, ok := schema["items"]; ok {
		child, valid := items.(map[string]any)
		if !valid || !hasType || typeName != "array" {
			return errors.New("items requires an array schema")
		}
		if err := validateSchemaNode(child, false, false); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	bounds := make(map[string]int, 4)
	for _, bound := range []string{"minItems", "maxItems", "minLength", "maxLength"} {
		if value, ok := schema[bound]; ok {
			if (strings.HasSuffix(bound, "Items") && typeName != "array") || (strings.HasSuffix(bound, "Length") && typeName != "string") {
				return fmt.Errorf("%s is not valid for schema type %q", bound, typeName)
			}
			parsed, valid := nonNegativeIntegerBound(value)
			if !valid {
				return fmt.Errorf("%s must be a non-negative integer", bound)
			}
			bounds[bound] = parsed
		}
	}
	for _, pair := range [][2]string{{"minItems", "maxItems"}, {"minLength", "maxLength"}} {
		minimum, hasMinimum := bounds[pair[0]]
		maximum, hasMaximum := bounds[pair[1]]
		if hasMinimum && hasMaximum && minimum > maximum {
			return fmt.Errorf("%s exceeds %s", pair[0], pair[1])
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		for _, candidate := range enum {
			if validateSchemaValue(schema, candidate, "$enum") == nil {
				return nil
			}
		}
		return errors.New("enum has no value satisfying its type and sibling constraints")
	}
	if typeName == "integer" || typeName == "number" {
		if err := validateNumericSchemaDomain(typeName, minimum, maximum, multiple); err != nil {
			return err
		}
	}
	return nil
}

func validateNumericSchemaDomain(typeName string, minimum, maximum, multiple *big.Rat) error {
	if typeName == "number" {
		if multiple == nil || minimum == nil || maximum == nil {
			return nil
		}
		lower := ratCeil(new(big.Rat).Quo(minimum, multiple))
		upper := ratFloor(new(big.Rat).Quo(maximum, multiple))
		if lower.Cmp(upper) > 0 {
			return errors.New("numeric range contains no exact multipleOf value")
		}
		return nil
	}

	var lower, upper *big.Int
	if minimum != nil {
		lower = ratCeil(minimum)
	}
	if maximum != nil {
		upper = ratFloor(maximum)
	}
	if lower != nil && upper != nil && lower.Cmp(upper) > 0 {
		return errors.New("integer range contains no integer value")
	}
	if multiple == nil || lower == nil || upper == nil {
		return nil
	}
	// For a reduced positive rational p/q, integral multiples are exactly
	// integer multiples of p: k*p/q is integral iff k is a multiple of q.
	step := new(big.Int).Abs(new(big.Int).Set(multiple.Num()))
	firstMultiplier := ratCeil(new(big.Rat).SetFrac(lower, step))
	first := new(big.Int).Mul(firstMultiplier, step)
	if first.Cmp(upper) > 0 {
		return errors.New("integer range contains no exact multipleOf value")
	}
	return nil
}

func ratFloor(value *big.Rat) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if remainder.Sign() < 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func ratCeil(value *big.Rat) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func schemaBooleanAnnotation(schema map[string]any, keyword string) (bool, error) {
	value, ok := schema[keyword]
	if !ok {
		return false, nil
	}
	boolean, valid := value.(bool)
	if !valid {
		return false, fmt.Errorf("%s must be boolean", keyword)
	}
	return boolean, nil
}

func validateCompatibility(manifest, expected Compatibility, options ValidatorOptions) error {
	if strings.TrimSpace(manifest.Host) == "" || strings.TrimSpace(manifest.Agent) == "" {
		return errors.New("host and agent compatibility ranges are required")
	}
	if expected.Host != "" && manifest.Host != expected.Host {
		return errors.New("host compatibility differs from market index")
	}
	if expected.Agent != "" && manifest.Agent != expected.Agent {
		return errors.New("agent compatibility differs from market index")
	}
	if !validCompatibilityConstraint(manifest.Host) || !validCompatibilityConstraint(manifest.Agent) {
		return errors.New("compatibility range uses unsupported syntax")
	}
	if options.HostVersion != "" && !versionSatisfies(options.HostVersion, manifest.Host) {
		return fmt.Errorf("host %s is outside %s", options.HostVersion, manifest.Host)
	}
	if options.AgentVersion != "" && !versionSatisfies(options.AgentVersion, manifest.Agent) {
		return fmt.Errorf("agent %s is outside %s", options.AgentVersion, manifest.Agent)
	}
	return nil
}

func validCompatibilityConstraint(constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "*" {
		return true
	}
	if constraint == "" {
		return false
	}
	return versionSatisfies("0.0.0", constraint) || compatibilityTermsParse(constraint)
}

func compatibilityTermsParse(constraint string) bool {
	for _, term := range strings.Fields(constraint) {
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				term = strings.TrimPrefix(term, candidate)
				break
			}
		}
		if _, ok := parseVersion(term); !ok {
			return false
		}
	}
	return len(strings.Fields(constraint)) > 0
}

// versionSatisfies intentionally implements the small, deterministic range
// grammar accepted by manifests: *, exact versions, and whitespace-separated
// >=, >, <=, < comparisons.
func versionSatisfies(version, constraint string) bool {
	if strings.TrimSpace(constraint) == "*" {
		return IsSemanticVersion(version)
	}
	actual, ok := parseVersion(version)
	if !ok {
		return false
	}
	for _, term := range strings.Fields(constraint) {
		op := "="
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				op = candidate
				term = strings.TrimPrefix(term, candidate)
				break
			}
		}
		expected, ok := parseVersion(term)
		if !ok {
			return false
		}
		cmp := compareVersion(actual, expected)
		if (op == "=" && cmp != 0) || (op == ">=" && cmp < 0) || (op == "<=" && cmp > 0) || (op == ">" && cmp <= 0) || (op == "<" && cmp >= 0) {
			return false
		}
	}
	return true
}

type semanticVersion struct {
	core       [3]string
	prerelease []string
}

func parseVersion(value string) (semanticVersion, bool) {
	if value == "" || value != strings.TrimSpace(value) {
		return semanticVersion{}, false
	}
	match := versionPattern.FindStringSubmatch(value)
	if match == nil {
		return semanticVersion{}, false
	}
	var result semanticVersion
	for index := range result.core {
		result.core[index] = match[index+1]
	}
	trimmed := value
	if plus := strings.IndexByte(trimmed, '+'); plus >= 0 {
		for _, identifier := range strings.Split(trimmed[plus+1:], ".") {
			if identifier == "" {
				return semanticVersion{}, false
			}
		}
		trimmed = trimmed[:plus]
	}
	if dash := strings.IndexByte(trimmed, '-'); dash >= 0 {
		result.prerelease = strings.Split(trimmed[dash+1:], ".")
		for _, identifier := range result.prerelease {
			if identifier == "" || (allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0') {
				return semanticVersion{}, false
			}
		}
	}
	return result, true
}

func compareVersion(a, b semanticVersion) int {
	for i := range a.core {
		if len(a.core[i]) < len(b.core[i]) || (len(a.core[i]) == len(b.core[i]) && a.core[i] < b.core[i]) {
			return -1
		}
		if len(a.core[i]) > len(b.core[i]) || (len(a.core[i]) == len(b.core[i]) && a.core[i] > b.core[i]) {
			return 1
		}
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(a.prerelease) && index < len(b.prerelease); index++ {
		left, right := a.prerelease[index], b.prerelease[index]
		if left == right {
			continue
		}
		leftNumeric, rightNumeric := allDigits(left), allDigits(right)
		if leftNumeric && rightNumeric {
			if len(left) < len(right) {
				return -1
			}
			if len(left) > len(right) {
				return 1
			}
			if left < right {
				return -1
			}
			return 1
		}
		if leftNumeric {
			return -1
		}
		if rightNumeric {
			return 1
		}
		if left < right {
			return -1
		}
		return 1
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	return 0
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func securePackagePath(root, name string) (string, error) {
	name = strings.TrimSpace(filepath.ToSlash(name))
	if name == "" || path.IsAbs(name) || !fs.ValidPath(name) {
		return "", errors.New("path must be a canonical relative path")
	}
	resolved := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes package root")
	}
	return resolved, nil
}

func decodeStrictYAML(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are forbidden")
		}
		return err
	}
	return nil
}

var errFileTooLarge = errors.New("file exceeds limit")

func readBoundedFile(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, limit+1))
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errFileTooLarge
	}
	return data, nil
}

func isExecutableName(name string) bool {
	ext := strings.ToLower(path.Ext(filepath.ToSlash(name)))
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".a", ".lib", ".bin", ".com", ".bat", ".cmd", ".ps1", ".sh", ".bash", ".zsh", ".py", ".pyc", ".pyo", ".pl", ".rb", ".js", ".wasm", ".class", ".jar", ".zip", ".dex", ".luac", ".bc":
		return true
	}
	return false
}

func isSafeAssetName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".json", ".yaml", ".yml", ".txt", ".md", ".png", ".jpg", ".jpeg", ".gif":
		return true
	default:
		return false
	}
}

func validateAssetContent(name, logicalName string, limit int64) error {
	data, err := readBoundedFile(name, limit)
	if err != nil {
		return err
	}
	ext := strings.ToLower(path.Ext(logicalName))
	switch ext {
	case ".json":
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return errors.New("JSON asset contains multiple values")
		}
	case ".yaml", ".yml":
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var value yaml.Node
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		var trailing yaml.Node
		if err := decoder.Decode(&trailing); err != io.EOF {
			return errors.New("YAML asset contains multiple documents")
		}
	case ".txt", ".md":
		if !utf8.Valid(data) {
			return errors.New("text asset is not valid UTF-8")
		}
		for _, value := range string(data) {
			if value == '\n' || value == '\r' || value == '\t' {
				continue
			}
			if unicode.IsControl(value) {
				return errors.New("text asset contains binary control data")
			}
		}
	case ".png":
		if err := validatePNGStructure(data); err != nil {
			return err
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return err
		}
		if err := validateImageDimensions(config); err != nil {
			return err
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			return err
		}
	case ".jpg", ".jpeg":
		if err := validateJPEGStructure(data); err != nil {
			return err
		}
		config, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return err
		}
		if err := validateImageDimensions(config); err != nil {
			return err
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			return err
		}
	case ".gif":
		frames, cumulativePixels, err := validateGIFStructure(data)
		if err != nil {
			return err
		}
		config, err := gif.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return err
		}
		if err := validateImageDimensions(config); err != nil {
			return err
		}
		if frames > 128 || cumulativePixels > 32<<20 {
			return errors.New("GIF asset exceeds frame or cumulative pixel budget")
		}
		if _, err := gif.DecodeAll(bytes.NewReader(data)); err != nil {
			return err
		}
	default:
		return errors.New("asset type is outside the data-only allowlist")
	}
	return nil
}

func validateImageDimensions(config image.Config) error {
	if config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return errors.New("image asset exceeds dimension or pixel budget")
	}
	return nil
}

func validatePNGStructure(data []byte) error {
	if len(data) < 8 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return errors.New("PNG asset has an invalid signature")
	}
	for offset := 8; ; {
		if offset+12 > len(data) {
			return errors.New("PNG asset is truncated")
		}
		length := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		end := int64(offset) + 12 + length
		if length < 0 || end > int64(len(data)) {
			return errors.New("PNG asset contains an invalid chunk")
		}
		chunkType := string(data[offset+4 : offset+8])
		offset = int(end)
		if chunkType == "IEND" {
			if length != 0 || offset != len(data) {
				return errors.New("PNG asset contains trailing data or multiple terminators")
			}
			return nil
		}
	}
}

func validateJPEGStructure(data []byte) error {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return errors.New("JPEG asset has an invalid signature")
	}
	offset, entropy := 2, false
	for offset < len(data) {
		if entropy {
			for offset < len(data) && data[offset] != 0xff {
				offset++
			}
			if offset >= len(data) {
				break
			}
			for offset < len(data) && data[offset] == 0xff {
				offset++
			}
			if offset >= len(data) {
				break
			}
			marker := data[offset]
			offset++
			if marker == 0x00 || marker >= 0xd0 && marker <= 0xd7 {
				continue
			}
			if marker == 0xd9 {
				if offset != len(data) {
					return errors.New("JPEG asset contains trailing data or multiple terminators")
				}
				return nil
			}
			entropy = false
			if marker == 0xda {
				entropy = true
			}
			if marker == 0x01 {
				continue
			}
			if offset+2 > len(data) {
				return errors.New("JPEG asset is truncated")
			}
			length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			if length < 2 || offset+length > len(data) {
				return errors.New("JPEG asset contains an invalid segment")
			}
			offset += length
			continue
		}
		if data[offset] != 0xff {
			return errors.New("JPEG asset contains data outside a scan")
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xd9 {
			if offset != len(data) {
				return errors.New("JPEG asset contains trailing data or multiple terminators")
			}
			return nil
		}
		if marker == 0xd8 || marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			return errors.New("JPEG asset is truncated")
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			return errors.New("JPEG asset contains an invalid segment")
		}
		offset += length
		if marker == 0xda {
			entropy = true
		}
	}
	return errors.New("JPEG asset has no unique end marker")
}

func validateGIFStructure(data []byte) (int, int64, error) {
	if len(data) < 13 || string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a" {
		return 0, 0, errors.New("GIF asset has an invalid signature")
	}
	offset := 13
	packed := data[10]
	if packed&0x80 != 0 {
		offset += 3 * (1 << ((packed & 0x07) + 1))
	}
	frames, cumulative := 0, int64(0)
	skipSubBlocks := func() error {
		for {
			if offset >= len(data) {
				return errors.New("GIF asset is truncated")
			}
			size := int(data[offset])
			offset++
			if size == 0 {
				return nil
			}
			if offset+size > len(data) {
				return errors.New("GIF asset contains an invalid data block")
			}
			offset += size
		}
	}
	for offset < len(data) {
		switch data[offset] {
		case 0x3b:
			offset++
			if offset != len(data) {
				return 0, 0, errors.New("GIF asset contains trailing data or multiple terminators")
			}
			return frames, cumulative, nil
		case 0x21:
			offset += 2
			if offset > len(data) {
				return 0, 0, errors.New("GIF asset is truncated")
			}
			if err := skipSubBlocks(); err != nil {
				return 0, 0, err
			}
		case 0x2c:
			if offset+10 > len(data) {
				return 0, 0, errors.New("GIF asset is truncated")
			}
			width := int(binary.LittleEndian.Uint16(data[offset+5 : offset+7]))
			height := int(binary.LittleEndian.Uint16(data[offset+7 : offset+9]))
			if width <= 0 || height <= 0 || width > 8192 || height > 8192 || int64(width)*int64(height) > 16<<20 {
				return 0, 0, errors.New("GIF frame exceeds dimension or pixel budget")
			}
			frames++
			cumulative += int64(width) * int64(height)
			localPacked := data[offset+9]
			offset += 10
			if localPacked&0x80 != 0 {
				offset += 3 * (1 << ((localPacked & 0x07) + 1))
			}
			if offset >= len(data) {
				return 0, 0, errors.New("GIF asset is truncated")
			}
			offset++
			if err := skipSubBlocks(); err != nil {
				return 0, 0, err
			}
		default:
			return 0, 0, errors.New("GIF asset contains an invalid block")
		}
	}
	return 0, 0, errors.New("GIF asset has no unique trailer")
}

func hasExecutableMagic(data []byte) bool {
	if len(data) < 4 {
		return bytes.HasPrefix(data, []byte("#!"))
	}
	for _, magic := range [][]byte{
		{'!', '<', 'a', 'r', 'c', 'h', '>', '\n'}, {'P', 'K', 0x03, 0x04}, {'P', 'K', 0x05, 0x06}, {'P', 'K', 0x07, 0x08},
		{0x7f, 'E', 'L', 'F'}, {'M', 'Z'},
		{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, {0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe},
		{0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca}, {0xca, 0xfe, 0xba, 0xbf}, {0xbf, 0xba, 0xfe, 0xca},
		{0x00, 0x61, 0x73, 0x6d}, {'B', 'C', 0xc0, 0xde}, {0x1b, 'L', 'u', 'a'}, {'d', 'e', 'x', '\n'},
	} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
	}
	if len(data) >= 2 {
		// COFF object machine fields, including i386, AMD64, ARM/ARM64.
		for _, machine := range [][2]byte{{0x4c, 0x01}, {0x64, 0x86}, {0xc0, 0x01}, {0x64, 0xaa}} {
			if data[0] == machine[0] && data[1] == machine[1] {
				return true
			}
		}
	}
	if len(data) >= 4 && data[2] == '\r' && data[3] == '\n' {
		return true // CPython bytecode magic (version-specific first two bytes).
	}
	return bytes.HasPrefix(data, []byte("#!"))
}

func validationError(code, name string, err error) error {
	return &ValidationError{Code: code, Path: name, Err: err}
}
func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
