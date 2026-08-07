package plugins

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const MarketManifestFile = "market.yaml"

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	versionPattern    = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	hexDigestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func IsSemanticVersion(value string) bool {
	_, ok := parseVersion(value)
	return ok
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
	seen := map[string]struct{}{}
	result := ValidatedMarket{Manifest: manifest, Packages: make([]ValidatedPackage, 0, len(manifest.Entries))}
	for index, entry := range manifest.Entries {
		key := entry.ID + "@" + entry.Version
		if _, exists := seen[key]; exists {
			return ValidatedMarket{}, validationError("duplicate_entry", MarketManifestFile, fmt.Errorf("duplicate %s", key))
		}
		seen[key] = struct{}{}
		if !identifierPattern.MatchString(entry.ID) || !versionPattern.MatchString(entry.Version) || !hexDigestPattern.MatchString(strings.ToLower(entry.PackageSHA256)) {
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

func (v *Validator) validateManifest(root string, manifest Manifest, expected PackageExpectation) error {
	if manifest.SchemaVersion != 1 || !identifierPattern.MatchString(manifest.ID) || !versionPattern.MatchString(manifest.Version) {
		return validationError("manifest_schema", PackageManifestFile, errors.New("schema_version 1, a canonical id, and semantic version are required"))
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
		if !versionPattern.MatchString(migration.From) || !versionPattern.MatchString(migration.To) || migration.From == migration.To {
			return validationError("migration", migration.File, errors.New("migration requires distinct semantic from/to versions"))
		}
		if path.Ext(filepath.ToSlash(migration.File)) != ".json" || !strings.HasPrefix(filepath.ToSlash(migration.File), "migrations/") {
			return validationError("migration", migration.File, errors.New("only declarative JSON migrations under migrations/ are allowed"))
		}
		migrationPath, err := securePackagePath(root, migration.File)
		if err != nil {
			return validationError("migration", migration.File, err)
		}
		migrationData, err := readBoundedFile(migrationPath, v.options.MaxFileBytes)
		if err != nil {
			return validationError("migration", migration.File, err)
		}
		if err := validateMigrationDocument(migrationData); err != nil {
			return validationError("migration", migration.File, err)
		}
		paths = append(paths, migration.File)
	}
	for _, reference := range paths {
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
	var result packageStats
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
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
		data := make([]byte, 4)
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
		default:
			return fmt.Errorf("operation %d uses forbidden migration op %q", index, operation.Op)
		}
	}
	return nil
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
		return versionPattern.MatchString(version)
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
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return semanticVersion{}, false
	}
	var result semanticVersion
	for index := range result.core {
		result.core[index] = match[index+1]
	}
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "v")
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
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".com", ".bat", ".cmd", ".ps1", ".sh", ".bash", ".zsh", ".py", ".pyc", ".pyo", ".pl", ".rb", ".js", ".wasm", ".class", ".jar", ".dex", ".luac", ".bc":
		return true
	}
	return false
}

func hasExecutableMagic(data []byte) bool {
	if len(data) < 4 {
		return bytes.HasPrefix(data, []byte("#!"))
	}
	for _, magic := range [][]byte{
		{0x7f, 'E', 'L', 'F'}, {'M', 'Z'},
		{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, {0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe},
		{0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca}, {0xca, 0xfe, 0xba, 0xbf}, {0xbf, 0xba, 0xfe, 0xca},
		{0x00, 0x61, 0x73, 0x6d}, {'B', 'C', 0xc0, 0xde}, {0x1b, 'L', 'u', 'a'}, {'d', 'e', 'x', '\n'},
	} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
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
