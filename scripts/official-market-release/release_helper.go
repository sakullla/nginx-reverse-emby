package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var oidPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type marketDocument struct {
	SchemaVersion int             `yaml:"schema_version"`
	Commit        string          `yaml:"commit"`
	SDKABI        string          `yaml:"sdk_abi"`
	Packages      []marketPackage `yaml:"packages"`
}

type marketPackage struct {
	ID             string            `yaml:"id" json:"id"`
	Version        string            `yaml:"version" json:"version"`
	Description    string            `yaml:"description" json:"-"`
	Capabilities   []string          `yaml:"capabilities" json:"-"`
	Compatibility  map[string]string `yaml:"compatibility" json:"-"`
	Runtime        string            `yaml:"runtime" json:"runtime"`
	ABI            string            `yaml:"abi" json:"abi"`
	HostScope      string            `yaml:"host_scope" json:"-"`
	PolicyKind     string            `yaml:"policy_kind" json:"-"`
	Artifacts      []marketArtifact  `yaml:"artifacts" json:"artifacts"`
	PackageSHA256  string            `yaml:"package_sha256" json:"package_sha256"`
	PackageURL     string            `yaml:"package_url" json:"package_url"`
	BlobSHA256     string            `yaml:"blob_sha256" json:"blob_sha256"`
	BlobSize       int64             `yaml:"blob_size" json:"blob_size"`
	BlobFormat     string            `yaml:"blob_format" json:"-"`
	SignerIdentity string            `yaml:"signer_identity" json:"-"`
}

type marketArtifact struct {
	SHA256 string `yaml:"sha256" json:"sha256"`
	Size   int64  `yaml:"size" json:"size"`
	GOOS   string `yaml:"goos" json:"goos"`
	GOARCH string `yaml:"goarch" json:"goarch"`
}

type provenanceDocument struct {
	SchemaVersion       int                 `json:"schema_version"`
	RepositoryCommit    string              `json:"repository_commit"`
	MarketSHA256        string              `json:"market_sha256"`
	SDKRepositoryCommit string              `json:"sdk_repository_commit"`
	SDKDescriptorSHA256 string              `json:"sdk_descriptor_sha256"`
	SDKABIs             []string            `json:"sdk_abis"`
	SignerIdentity      string              `json:"signer_identity"`
	Packages            []provenancePackage `json:"packages"`
}

type provenancePackage struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	PackageSHA256 string `json:"package_sha256"`
	PackageURL    string `json:"package_url"`
	BlobSHA256    string `json:"blob_sha256"`
	BlobSize      int64  `json:"blob_size"`
	BlobFormat    string `json:"blob_format"`
}

type marketOutput struct {
	SourceCommit string          `json:"source_commit"`
	Packages     []marketPackage `json:"packages"`
}

type packageFileManifest struct {
	Files []packageFile `json:"files"`
}

type packageFile struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type validationOutput struct {
	Valid   bool   `json:"valid"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

type preparedPackage struct {
	ID             string   `json:"id"`
	Version        string   `json:"version"`
	Runtime        string   `json:"runtime"`
	ArtifactPath   string   `json:"artifact_path,omitempty"`
	ArtifactSHA256 string   `json:"artifact_sha256,omitempty"`
	Passed         bool     `json:"passed"`
	Errors         []string `json:"errors"`
}

type prepareOutput struct {
	Valid          bool              `json:"valid"`
	SourceCommit   string            `json:"source_commit"`
	Packages       int               `json:"packages"`
	PackageResults []preparedPackage `json:"package_results"`
	Failures       []string          `json:"failures"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("release helper requires market or extract")
	}
	switch args[0] {
	case "market":
		flags := flag.NewFlagSet("market", flag.ContinueOnError)
		root := flags.String("root", "", "validated official market checkout")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		output, err := readMarket(*root)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(output)
	case "extract":
		flags := flag.NewFlagSet("extract", flag.ContinueOnError)
		archive := flags.String("archive", "", "signed .nrepkg archive")
		destination := flags.String("destination", "", "empty extraction directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return extractPackage(*archive, *destination)
	case "prepare":
		flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
		root := flags.String("root", "", "validated official market checkout")
		destination := flags.String("destination", "", "package download and extraction root")
		validator := flags.String("validator", "", "nre-plugin-validator executable")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		output, err := preparePackages(*root, *destination, *validator)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(output)
	default:
		return fmt.Errorf("unknown release helper command %q", args[0])
	}
}

func preparePackages(root, destination, validator string) (prepareOutput, error) {
	market, err := readMarket(root)
	if err != nil {
		return prepareOutput{}, err
	}
	if validator == "" {
		return prepareOutput{}, errors.New("validator is required")
	}
	if err := os.Mkdir(destination, 0o755); err != nil {
		return prepareOutput{}, err
	}
	output := prepareOutput{SourceCommit: market.SourceCommit, Packages: len(market.Packages)}
	for _, pkg := range market.Packages {
		result := preparedPackage{ID: pkg.ID, Version: pkg.Version, Runtime: pkg.Runtime}
		archive := filepath.Join(destination, pkg.ID+".nrepkg")
		extracted := filepath.Join(destination, pkg.ID)
		if err := preparePackage(pkg, archive, extracted, validator, &result); err != nil {
			result.Errors = []string{err.Error()}
			output.Failures = append(output.Failures, fmt.Sprintf("package %s: %v", pkg.ID, err))
		} else {
			result.Passed = true
		}
		output.PackageResults = append(output.PackageResults, result)
	}
	output.Valid = len(output.Failures) == 0
	return output, nil
}

func preparePackage(pkg marketPackage, archive, extracted, validator string, result *preparedPackage) error {
	if err := downloadPackage(pkg.PackageURL, archive, pkg.BlobSHA256, pkg.BlobSize); err != nil {
		return err
	}
	if err := os.Mkdir(extracted, 0o755); err != nil {
		return err
	}
	if err := extractPackage(archive, extracted); err != nil {
		return err
	}
	command := exec.Command(validator, "--root", extracted, "--id", pkg.ID, "--version", pkg.Version, "--sha256", pkg.PackageSHA256)
	validationJSON, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate signed package: %w: %s", err, strings.TrimSpace(string(validationJSON)))
	}
	var validation validationOutput
	if err := json.Unmarshal(validationJSON, &validation); err != nil {
		return fmt.Errorf("decode package validation: %w", err)
	}
	if !validation.Valid || validation.ID != pkg.ID || validation.Version != pkg.Version {
		return fmt.Errorf("validator returned an unexpected package identity: %s", strings.TrimSpace(string(validationJSON)))
	}
	manifestData, err := os.ReadFile(filepath.Join(extracted, "package.files.json"))
	if err != nil {
		return err
	}
	var manifest packageFileManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	var records []packageFile
	switch pkg.Runtime {
	case "rpc-service":
		for _, file := range manifest.Files {
			if file.Mode == "0755" {
				records = append(records, file)
			}
		}
	case "wasm-policy":
		for _, file := range manifest.Files {
			if strings.HasSuffix(file.Path, ".wasm") {
				records = append(records, file)
			}
		}
	default:
		return fmt.Errorf("unsupported official runtime %q", pkg.Runtime)
	}
	var published []marketArtifact
	for _, artifact := range pkg.Artifacts {
		if (pkg.Runtime == "rpc-service" && artifact.GOOS == "linux" && artifact.GOARCH == "amd64") ||
			(pkg.Runtime == "wasm-policy" && artifact.GOOS == "" && artifact.GOARCH == "") {
			published = append(published, artifact)
		}
	}
	if len(records) != 1 || len(published) != 1 {
		return fmt.Errorf("runtime artifact selection produced package=%d index=%d, want one each", len(records), len(published))
	}
	artifact := filepath.Join(extracted, filepath.FromSlash(records[0].Path))
	data, err := os.ReadFile(artifact)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	actualDigest := hex.EncodeToString(digest[:])
	if actualDigest != records[0].SHA256 || int64(len(data)) != records[0].Size ||
		actualDigest != published[0].SHA256 || int64(len(data)) != published[0].Size {
		return errors.New("runtime artifact differs from signed package or signed market index")
	}
	absolute, err := filepath.Abs(artifact)
	if err != nil {
		return err
	}
	result.ArtifactPath, result.ArtifactSHA256 = absolute, actualDigest
	return nil
}

func downloadPackage(url, destination, expectedDigest string, expectedSize int64) error {
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, expectedSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("downloaded package blob differs from the signed index")
	}
	return nil
}

func readMarket(root string) (marketOutput, error) {
	marketFile, err := os.Open(filepath.Join(root, "market.yaml"))
	if err != nil {
		return marketOutput{}, err
	}
	defer marketFile.Close()
	decoder := yaml.NewDecoder(marketFile)
	decoder.KnownFields(true)
	var market marketDocument
	if err := decoder.Decode(&market); err != nil {
		return marketOutput{}, fmt.Errorf("decode market.yaml: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return marketOutput{}, errors.New("market.yaml must contain exactly one document")
	}
	provenanceFile, err := os.Open(filepath.Join(root, "provenance.json"))
	if err != nil {
		return marketOutput{}, err
	}
	defer provenanceFile.Close()
	jsonDecoder := json.NewDecoder(provenanceFile)
	jsonDecoder.DisallowUnknownFields()
	var provenance provenanceDocument
	if err := jsonDecoder.Decode(&provenance); err != nil {
		return marketOutput{}, fmt.Errorf("decode provenance.json: %w", err)
	}
	if market.SchemaVersion != 2 || provenance.SchemaVersion != 2 || !validOID(market.Commit) || provenance.RepositoryCommit != market.Commit {
		return marketOutput{}, errors.New("official market must be a signed v2 projection with one non-zero source OID")
	}
	if len(market.Packages) == 0 || len(provenance.Packages) != len(market.Packages) {
		return marketOutput{}, fmt.Errorf("official market/provenance package counts are %d/%d", len(market.Packages), len(provenance.Packages))
	}
	for index, pkg := range market.Packages {
		evidence := provenance.Packages[index]
		if pkg.ID != evidence.ID || pkg.Version != evidence.Version || pkg.PackageSHA256 != evidence.PackageSHA256 || pkg.PackageURL != evidence.PackageURL || pkg.BlobSHA256 != evidence.BlobSHA256 || pkg.BlobSize != evidence.BlobSize || pkg.BlobFormat != evidence.BlobFormat {
			return marketOutput{}, fmt.Errorf("package %d differs from signed provenance", index)
		}
	}
	return marketOutput{SourceCommit: market.Commit, Packages: market.Packages}, nil
}

func validOID(value string) bool {
	return oidPattern.MatchString(value) && value != strings.Repeat("0", 40)
}

func extractPackage(archive, destination string) error {
	if strings.TrimSpace(archive) == "" || strings.TrimSpace(destination) == "" {
		return errors.New("archive and destination are required")
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("package extraction destination must be empty")
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := map[string]struct{}{}
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || strings.Contains(name, `\`) || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("archive contains unsafe path %q", header.Name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("archive contains duplicate path %q", name)
		}
		seen[name] = struct{}{}
		target := filepath.Join(root, filepath.FromSlash(name))
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path %q escapes destination", name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(output, tarReader)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != header.Size {
				return fmt.Errorf("archive entry %q wrote %d bytes, want %d", name, written, header.Size)
			}
		default:
			return fmt.Errorf("archive entry %q has forbidden type %d", name, header.Typeflag)
		}
	}
	if len(seen) == 0 {
		return errors.New("package archive is empty")
	}
	return nil
}
