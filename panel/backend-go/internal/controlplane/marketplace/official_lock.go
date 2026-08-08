package marketplace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk"
	"gopkg.in/yaml.v3"
)

const (
	OfficialMarketLockFile         = "official-market.lock"
	OfficialMarketLockPathEnv      = "PANEL_OFFICIAL_MARKET_LOCK_FILE"
	packagedOfficialMarketLockPath = "/opt/nginx-reverse-emby/official-market.lock"
)

var fullGitOIDPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type OfficialMarketLock struct {
	SchemaVersion  int      `yaml:"schema_version" json:"schema_version"`
	Repository     string   `yaml:"repository" json:"repository"`
	Commit         string   `yaml:"commit" json:"commit"`
	MarketSHA256   string   `yaml:"market_sha256" json:"market_sha256"`
	SDKABIs        []string `yaml:"sdk_abis" json:"sdk_abis"`
	SignatureKeyID string   `yaml:"signature_key_id" json:"signature_key_id"`
	VerifiedAt     string   `yaml:"verified_at" json:"verified_at"`
}

// ResolveOfficialMarketLockPath resolves either an explicit absolute path, the
// packaged container location, or a repository root identified by its Go
// module marker. It never treats the process working directory itself as the
// lock owner.
func ResolveOfficialMarketLockPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("configured official market lock path must be absolute")
		}
		return verifiedOfficialMarketLockFile(configured)
	}
	if candidate, err := verifiedOfficialMarketLockFile(packagedOfficialMarketLockPath); err == nil {
		return candidate, nil
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve official market lock working directory: %w", err)
	}
	for directory := filepath.Clean(workingDirectory); ; directory = filepath.Dir(directory) {
		moduleMarker := filepath.Join(directory, "panel", "backend-go", "go.mod")
		if info, markerErr := os.Lstat(moduleMarker); markerErr == nil && info.Mode().IsRegular() {
			if candidate, candidateErr := verifiedOfficialMarketLockFile(filepath.Join(directory, OfficialMarketLockFile)); candidateErr == nil {
				return candidate, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	return "", errors.New("official market lock was not found at the configured, packaged, or repository location")
}

func verifiedOfficialMarketLockFile(name string) (string, error) {
	name, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("official market lock path is not a regular file")
	}
	return filepath.Clean(name), nil
}

func ReadOfficialMarketLock(name string) (OfficialMarketLock, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return OfficialMarketLock{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var lock OfficialMarketLock
	if err := decoder.Decode(&lock); err != nil {
		return OfficialMarketLock{}, fmt.Errorf("official market lock schema: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return OfficialMarketLock{}, fmt.Errorf("official market lock trailing document: %w", err)
		}
		return OfficialMarketLock{}, errors.New("official market lock must contain exactly one YAML document")
	}
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return OfficialMarketLock{}, err
	}
	return lock, nil
}

func ValidateOfficialMarketLock(lock OfficialMarketLock) error {
	if lock.SchemaVersion != 1 || lock.Repository != OfficialSourceURL {
		return errors.New("official market lock has an invalid schema or immutable repository identity")
	}
	if !fullGitOIDPattern.MatchString(lock.Commit) || strings.Trim(lock.Commit, "0") == "" {
		return errors.New("official market lock requires a non-zero lowercase full Git OID")
	}
	if !IsDigest(lock.MarketSHA256) || strings.Trim(lock.MarketSHA256, "0") == "" {
		return errors.New("official market lock requires a non-zero market digest")
	}
	wantABIs := []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}
	gotABIs := append([]string(nil), lock.SDKABIs...)
	sort.Strings(gotABIs)
	sort.Strings(wantABIs)
	if len(gotABIs) != len(wantABIs) || gotABIs[0] != wantABIs[0] || gotABIs[1] != wantABIs[1] {
		return errors.New("official market lock SDK ABI set is incomplete or unsupported")
	}
	if lock.SignatureKeyID != plugins.OfficialSignatureKeyID {
		return errors.New("official market lock signature identity is not the built-in official root")
	}
	verifiedAt, err := time.Parse(time.RFC3339, lock.VerifiedAt)
	if err != nil || verifiedAt.Location() != time.UTC {
		return errors.New("official market lock verified_at must be an RFC3339 UTC timestamp")
	}
	return nil
}

func MarketManifestDigest(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, plugins.MarketManifestFile))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// ValidateOfficialLockCheckout validates an already isolated, metadata-free
// checkout. Callers must bind checkoutOID to the immutable fetch result.
func ValidateOfficialLockCheckout(lock OfficialMarketLock, checkoutRoot, checkoutOID string, validator *plugins.Validator) (plugins.ValidatedMarket, error) {
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return plugins.ValidatedMarket{}, err
	}
	if validator == nil {
		return plugins.ValidatedMarket{}, errors.New("plugin validator is required")
	}
	if checkoutOID != lock.Commit {
		return plugins.ValidatedMarket{}, errors.New("official checkout OID does not match lock")
	}
	if _, err := os.Stat(filepath.Join(checkoutRoot, ".git")); !errors.Is(err, os.ErrNotExist) {
		return plugins.ValidatedMarket{}, errors.New("official validation checkout must not contain Git metadata")
	}
	digest, err := MarketManifestDigest(checkoutRoot)
	if err != nil || digest != lock.MarketSHA256 {
		return plugins.ValidatedMarket{}, errors.New("official market digest does not match lock")
	}
	validated, err := validator.ValidateMarket(checkoutRoot, true)
	if err != nil {
		return plugins.ValidatedMarket{}, err
	}
	for _, entry := range validated.Manifest.Entries {
		if entry.SignatureKeyID != lock.SignatureKeyID {
			return plugins.ValidatedMarket{}, errors.New("official package signer does not match lock identity")
		}
	}
	return validated, nil
}

// ValidateOfficialMarketAtLock always creates a temporary clean checkout of
// the exact immutable OID. It never reads a sibling development worktree.
func ValidateOfficialMarketAtLock(ctx context.Context, lock OfficialMarketLock, validator *plugins.Validator) (plugins.ValidatedMarket, error) {
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return plugins.ValidatedMarket{}, err
	}
	temporary, err := os.MkdirTemp("", "nre-official-market-")
	if err != nil {
		return plugins.ValidatedMarket{}, err
	}
	defer os.RemoveAll(temporary)
	bare := filepath.Join(temporary, "repository")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		return plugins.ValidatedMarket{}, err
	}
	repository, closeStorage, err := cloneBareBudgeted(ctx, bare, &git.CloneOptions{URL: lock.Repository, NoCheckout: true}, plugins.DefaultMaxMarketBytes)
	if err != nil {
		return plugins.ValidatedMarket{}, err
	}
	defer closeStorage()
	commit, err := repository.CommitObject(plumbing.NewHash(lock.Commit))
	if err != nil || commit.Hash.String() != lock.Commit {
		return plugins.ValidatedMarket{}, errors.New("locked official commit is unavailable from the immutable repository")
	}
	tree, err := commit.Tree()
	if err != nil {
		return plugins.ValidatedMarket{}, err
	}
	checkout := filepath.Join(temporary, "checkout")
	if err := checkoutBudgetedTree(ctx, tree, checkout, plugins.DefaultMaxMarketFiles, plugins.DefaultMaxMarketBytes); err != nil {
		return plugins.ValidatedMarket{}, err
	}
	return ValidateOfficialLockCheckout(lock, checkout, commit.Hash.String(), validator)
}
