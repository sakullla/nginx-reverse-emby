package plugins

import (
	"bufio"
	"bytes"
	"crypto/sha256"
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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MarketManifestFile = "market.yaml"

const (
	DefaultMaxMarketFiles      = 16384
	DefaultMaxMarketBytes      = int64(256 << 20)
	DefaultMaxMarketPackages   = 512
	MaxPluginIDBytes           = 190
	MaxPluginVersionBytes      = 64
	MaxPackagePathBytes        = 2048
	MaxPermissionNameBytes     = 190
	MaxPermissionResourceBytes = 512
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
}

type Validator struct {
	options         ValidatorOptions
	permissions     map[string]struct{}
	extensionPoints map[string]struct{}
}

func NewValidator(options ValidatorOptions) *Validator {
	if options.MaxFiles <= 0 {
		options.MaxFiles = 4096
	}
	if options.MaxPackageBytes <= 0 {
		options.MaxPackageBytes = 64 << 20
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = 16 << 20
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
			"l4.inspect", "l4.respond", "policy.read", "policy.write", "secret.use",
			"storage.read", "storage.write", "container.read", "container.manage", "dns.manage",
		}
	}
	if len(options.AllowedExtensionPoints) == 0 {
		options.AllowedExtensionPoints = []string{
			"http.request", "http.response", "l4.accept", "policy.provider", "dns.provider",
			"container.provider", "tunnel.provider", "ui.route",
		}
	}
	v := &Validator{options: options, permissions: map[string]struct{}{}, extensionPoints: map[string]struct{}{}}
	for _, name := range options.AllowedPermissions {
		v.permissions[strings.TrimSpace(name)] = struct{}{}
	}
	for _, name := range options.AllowedExtensionPoints {
		v.extensionPoints[strings.TrimSpace(name)] = struct{}{}
	}
	return v
}

func (v *Validator) ValidatePackage(root string, expected PackageExpectation) (ValidatedPackage, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return ValidatedPackage{}, err
	}
	stats, err := inspectPackageTree(root, v.options)
	if err != nil {
		return ValidatedPackage{}, err
	}

	manifestPath := filepath.Join(root, PackageManifestFile)
	manifestData, err := readBoundedFile(manifestPath, v.options.MaxFileBytes)
	if err != nil {
		return ValidatedPackage{}, validationError("manifest", PackageManifestFile, err)
	}
	var manifest Manifest
	if err := decodeStrictYAML(manifestData, &manifest); err != nil {
		return ValidatedPackage{}, validationError("manifest_schema", PackageManifestFile, err)
	}
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

	digest, err := ComputePackageDigest(root)
	if err != nil {
		return ValidatedPackage{}, err
	}
	declaredData, err := readBoundedFile(filepath.Join(root, PackageDigestFile), 4096)
	if err != nil {
		return ValidatedPackage{}, validationError("checksum_missing", PackageDigestFile, err)
	}
	declared := strings.ToLower(strings.TrimSpace(string(declaredData)))
	if !hexDigestPattern.MatchString(declared) || !strings.EqualFold(digest, declared) {
		return ValidatedPackage{}, validationError("checksum_mismatch", PackageDigestFile, fmt.Errorf("declared %q does not match computed digest", declared))
	}
	if expected.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(expected.SHA256), digest) {
		return ValidatedPackage{}, validationError("index_checksum_mismatch", PackageDigestFile, fmt.Errorf("market digest does not match package"))
	}
	return ValidatedPackage{Manifest: manifest, Digest: digest, Root: root, FileCount: stats.files, Size: stats.bytes, ConfigSchema: schema}, nil
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
	data, err := readBoundedFile(filepath.Join(root, MarketManifestFile), v.options.MaxFileBytes)
	if err != nil {
		return ValidatedMarket{}, validationError("market_manifest", MarketManifestFile, err)
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
		if entry.Official != officialSource {
			return ValidatedMarket{}, validationError("source_identity", MarketManifestFile, fmt.Errorf("entry %s official flag does not match source kind", key))
		}
	}
	if err := inspectMarketTree(root, manifest, v.options); err != nil {
		return ValidatedMarket{}, err
	}
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
		validated, err := v.ValidatePackage(packagePath, PackageExpectation{ID: entry.ID, Version: entry.Version, SHA256: entry.PackageSHA256, Compatibility: entry.Compatibility})
		if err != nil {
			return ValidatedMarket{}, err
		}
		result.Packages = append(result.Packages, validated)
	}
	return result, nil
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
	if err := validateCompatibility(manifest.Compatibility, expected.Compatibility, v.options); err != nil {
		return validationError("compatibility", PackageManifestFile, err)
	}
	seenPermissions := map[string]struct{}{}
	for _, permission := range manifest.Permissions {
		permission.Name = strings.TrimSpace(permission.Name)
		if len(permission.Name) > MaxPermissionNameBytes || len(strings.TrimSpace(permission.Resource)) > MaxPermissionResourceBytes {
			return validationError("permission", PackageManifestFile, errors.New("permission exceeds persistence field limit"))
		}
		if _, allowed := v.permissions[permission.Name]; !allowed {
			return validationError("permission", PackageManifestFile, fmt.Errorf("permission %q is not allowed", permission.Name))
		}
		key := permission.Name + "\x00" + strings.TrimSpace(permission.Resource)
		if _, duplicate := seenPermissions[key]; duplicate {
			return validationError("permission", PackageManifestFile, fmt.Errorf("duplicate permission %q", permission.Name))
		}
		seenPermissions[key] = struct{}{}
	}
	for _, point := range manifest.ExtensionPoints {
		if _, allowed := v.extensionPoints[strings.TrimSpace(point)]; !allowed {
			return validationError("extension_point", PackageManifestFile, fmt.Errorf("extension point %q is not allowed", point))
		}
	}
	if err := validateCleanup(manifest.Cleanup); err != nil {
		return validationError("cleanup", PackageManifestFile, err)
	}
	paths := append([]string{manifest.ConfigSchema}, manifest.Assets...)
	for _, migration := range manifest.Migrations {
		if !IsSemanticVersion(migration.From) || !IsSemanticVersion(migration.To) || migration.From == migration.To {
			return validationError("migration", migration.File, errors.New("migration requires distinct semantic from/to versions"))
		}
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
	declared := map[string]struct{}{PackageManifestFile: {}, PackageDigestFile: {}, filepath.ToSlash(manifest.ConfigSchema): {}}
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
	var result packageStats
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
		canonicalRel := filepath.ToSlash(rel)
		if _, ok := declared[canonicalRel]; !ok {
			return validationError("undeclared_payload", canonicalRel, errors.New("every package file must be declared by the manifest"))
		}
		result.files++
		result.bytes += info.Size()
		if result.files > options.MaxFiles || result.bytes > options.MaxPackageBytes || info.Size() > options.MaxFileBytes {
			return validationError("size_limit", filepath.ToSlash(rel), errors.New("package size or file count limit exceeded"))
		}
		if isExecutableName(rel) || info.Mode().Perm()&0o111 != 0 {
			return validationError("executable", filepath.ToSlash(rel), errors.New("scripts and platform executables are forbidden"))
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
		if hasExecutableMagic(data) {
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
// "sha256  relative/path\n" lines. package.sha256 itself is excluded.
func ComputePackageDigest(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
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
		if rel == PackageDigestFile {
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
		fmt.Fprintf(packageHash, "%x  %s\n", contentHash.Sum(nil), rel)
	}
	return hex.EncodeToString(packageHash.Sum(nil)), nil
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
	return validateSchemaNode(schema, true)
}

func validateSchemaNode(schema map[string]any, root bool) error {
	allowed := map[string]bool{"type": true, "enum": true, "title": true, "description": true, "default": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "minItems": true, "maxItems": true, "minLength": true, "maxLength": true}
	for keyword := range schema {
		if !allowed[keyword] {
			return fmt.Errorf("unsupported JSON Schema keyword %q", keyword)
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
	if enum, ok := schema["enum"]; ok {
		values, valid := enum.([]any)
		if !valid || len(values) == 0 {
			return errors.New("enum must be a non-empty array")
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
			if err := validateSchemaNode(childSchema, false); err != nil {
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
	if items, ok := schema["items"]; ok {
		child, valid := items.(map[string]any)
		if !valid || !hasType || typeName != "array" {
			return errors.New("items requires an array schema")
		}
		if err := validateSchemaNode(child, false); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	for _, bound := range []string{"minItems", "maxItems", "minLength", "maxLength"} {
		if value, ok := schema[bound]; ok {
			if (strings.HasSuffix(bound, "Items") && typeName != "array") || (strings.HasSuffix(bound, "Length") && typeName != "string") {
				return fmt.Errorf("%s is not valid for schema type %q", bound, typeName)
			}
			number, valid := numeric(value)
			if !valid || number < 0 || number != float64(int64(number)) {
				return fmt.Errorf("%s must be a non-negative integer", bound)
			}
		}
	}
	return nil
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
