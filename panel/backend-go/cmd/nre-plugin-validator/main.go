package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type validationOutput struct {
	Valid          bool                      `json:"valid"`
	Kind           string                    `json:"kind"`
	ID             string                    `json:"id,omitempty"`
	Version        string                    `json:"version,omitempty"`
	Digest         string                    `json:"digest,omitempty"`
	Packages       int                       `json:"packages,omitempty"`
	PackageDetails []validationPackageOutput `json:"package_details,omitempty"`
	FileCount      int                       `json:"file_count,omitempty"`
	Size           int64                     `json:"size,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Codes          []string                  `json:"codes,omitempty"`
	Commit         string                    `json:"commit,omitempty"`
}

type validationPackageOutput struct {
	ID             string                     `json:"id"`
	Version        string                     `json:"version"`
	PackagePath    string                     `json:"package_path"`
	RuntimeKind    string                     `json:"runtime_kind"`
	RuntimeABI     string                     `json:"runtime_abi"`
	RuntimeEntry   string                     `json:"runtime_entry"`
	ArtifactSHA256 string                     `json:"artifact_sha256,omitempty"`
	Artifacts      []validationArtifactOutput `json:"artifacts"`
}

type validationArtifactOutput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	GOOS   string `json:"goos,omitempty"`
	GOARCH string `json:"goarch,omitempty"`
}

type stringFlags []string

func (values *stringFlags) String() string { return strings.Join(*values, ",") }
func (values *stringFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("nre-plugin-validator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "plugin package root")
	marketPath := flags.String("market", "", "path to a market.yaml file")
	officialLockPath := flags.String("official-lock", "", "validate the current official market against its repository and trust policy")
	official := flags.Bool("official", true, "validate market entries as the built-in official source")
	expectedID := flags.String("id", "", "expected plugin id")
	expectedVersion := flags.String("version", "", "expected plugin version")
	expectedDigest := flags.String("sha256", "", "expected package digest")
	hostVersion := flags.String("host-version", "", "control-plane version used for compatibility validation")
	agentVersion := flags.String("agent-version", "", "Agent version used for compatibility validation")
	targetGOOS := flags.String("target-goos", "", "required RPC artifact operating system")
	targetGOARCH := flags.String("target-goarch", "", "required RPC artifact architecture")
	var trustedKeys stringFlags
	flags.Var(&trustedKeys, "trusted-key", "trusted Ed25519 signer as key-id=base64-public-key (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if *officialLockPath != "" && (*marketPath != "" || *root != "." || *expectedID != "" || *expectedVersion != "" || *expectedDigest != "") {
		fmt.Fprintln(stderr, "--official-lock cannot be combined with package or market selectors")
		return 2
	}
	trustedSigners, err := parseTrustedSigners(trustedKeys)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	validator := plugins.NewValidator(plugins.ValidatorOptions{HostVersion: *hostVersion, AgentVersion: *agentVersion, TargetGOOS: *targetGOOS, TargetGOARCH: *targetGOARCH, TrustedSigners: trustedSigners})
	output := validationOutput{Valid: true, Kind: "package"}
	var validationErr error
	if *officialLockPath != "" {
		output.Kind = "official-lock"
		lock, err := marketplace.ReadOfficialMarketLock(*officialLockPath)
		if err != nil {
			validationErr = err
		} else {
			var result plugins.ValidatedMarket
			result, output.Commit, validationErr = marketplace.ValidateOfficialMarketAtLock(context.Background(), lock, validator)
			if validationErr == nil {
				output.Packages = len(result.Packages)
				output.PackageDetails = validationPackageDetails(result)
			}
		}
	} else if *marketPath != "" {
		output.Kind = "market"
		marketRoot, err := marketRootFromPath(*marketPath)
		if err != nil {
			validationErr = err
		} else {
			var result plugins.ValidatedMarket
			result, validationErr = validator.ValidateMarket(marketRoot, *official)
			if validationErr == nil {
				output.Packages = len(result.Packages)
				output.PackageDetails = validationPackageDetails(result)
			}
		}
	} else {
		result, err := validator.ValidatePackage(*root, plugins.PackageExpectation{ID: *expectedID, Version: *expectedVersion, SHA256: *expectedDigest})
		validationErr = err
		if validationErr == nil {
			output.ID, output.Version, output.Digest = result.Manifest.ID, result.Manifest.Version, result.Digest
			output.FileCount, output.Size = result.FileCount, result.Size
		}
	}
	if validationErr != nil {
		output.Valid = false
		output.Error = validationErr.Error()
		var typed *plugins.ValidationError
		if errors.As(validationErr, &typed) {
			output.Codes = []string{typed.Code}
		}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if validationErr != nil {
		return 1
	}
	return 0
}

func validationPackageDetails(market plugins.ValidatedMarket) []validationPackageOutput {
	details := make([]validationPackageOutput, 0, len(market.Packages))
	for index, pkg := range market.Packages {
		packagePath := ""
		if index < len(market.Manifest.Entries) {
			packagePath = market.Manifest.Entries[index].PackagePath
		}
		artifactDigest := ""
		artifacts := make([]validationArtifactOutput, 0, len(pkg.Manifest.Artifacts))
		for _, artifact := range pkg.Manifest.Artifacts {
			artifacts = append(artifacts, validationArtifactOutput{
				Path: artifact.Path, SHA256: artifact.SHA256, Size: artifact.Size,
				Mode: artifact.Mode, GOOS: artifact.GOOS, GOARCH: artifact.GOARCH,
			})
			if artifact.Path == pkg.Manifest.Runtime.Entry {
				artifactDigest = artifact.SHA256
			}
		}
		details = append(details, validationPackageOutput{
			ID: pkg.Manifest.ID, Version: pkg.Manifest.Version, PackagePath: packagePath,
			RuntimeKind: pkg.Manifest.Runtime.Kind, RuntimeABI: pkg.Manifest.Runtime.ABI,
			RuntimeEntry: pkg.Manifest.Runtime.Entry, ArtifactSHA256: artifactDigest, Artifacts: artifacts,
		})
	}
	return details
}

func parseTrustedSigners(values []string) (map[string]ed25519.PublicKey, error) {
	result := map[string]ed25519.PublicKey{}
	for _, value := range values {
		keyID, encoded, ok := strings.Cut(value, "=")
		if !ok || keyID == "" {
			return nil, errors.New("--trusted-key requires key-id=base64-public-key")
		}
		if err := plugins.ValidateSignerKeyID(keyID); err != nil {
			return nil, fmt.Errorf("trusted key %q is not a canonical signer identity", keyID)
		}
		if keyID == plugins.OfficialSignatureKeyID {
			return nil, errors.New("the built-in official signature root cannot be overridden")
		}
		if encoded != strings.TrimSpace(encoded) {
			return nil, fmt.Errorf("trusted key %q public key must use canonical whitespace", keyID)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted key %q is not a canonical Ed25519 public key", keyID)
		}
		if _, duplicate := result[keyID]; duplicate {
			return nil, fmt.Errorf("trusted key %q is duplicated", keyID)
		}
		result[keyID] = ed25519.PublicKey(key)
	}
	return result, nil
}

func marketRootFromPath(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if filepath.Base(absolute) != plugins.MarketManifestFile {
		return "", fmt.Errorf("--market must name %s", plugins.MarketManifestFile)
	}
	return filepath.Dir(absolute), nil
}
