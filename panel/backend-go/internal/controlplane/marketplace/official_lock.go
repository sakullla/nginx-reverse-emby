package marketplace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gopkg.in/yaml.v3"
)

const (
	OfficialMarketLockFile         = "official-market.lock"
	OfficialMarketLockPathEnv      = "PANEL_OFFICIAL_MARKET_LOCK_FILE"
	packagedOfficialMarketLockPath = "/opt/nginx-reverse-emby/official-market.lock"
)

type OfficialMarketLock struct {
	SchemaVersion  int      `yaml:"schema_version" json:"schema_version"`
	Repository     string   `yaml:"repository" json:"repository"`
	RefKind        string   `yaml:"ref_kind" json:"ref_kind"`
	RefName        string   `yaml:"ref_name" json:"ref_name"`
	SDKABIs        []string `yaml:"sdk_abis" json:"sdk_abis"`
	SignatureKeyID string   `yaml:"signature_key_id" json:"signature_key_id"`
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
		return errors.New("official market lock has an invalid schema or repository identity")
	}
	if lock.RefKind != GitRefKindBranch {
		return errors.New("official market policy must track a movable branch")
	}
	if _, err := OfficialSourceForBranch(lock.RefName, 1); err != nil {
		return fmt.Errorf("official market policy branch: %w", err)
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
	return nil
}

func (lock OfficialMarketLock) Source(revision uint64) (Source, error) {
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return Source{}, err
	}
	return OfficialSourceForBranch(lock.RefName, revision)
}

// ValidateOfficialLockCheckout validates one isolated, metadata-free refresh
// result. The policy intentionally does not pin checkoutOID, but requires the
// fetcher to return a full immutable OID for snapshot provenance.
func ValidateOfficialLockCheckout(lock OfficialMarketLock, checkoutRoot, checkoutOID string, validator *plugins.Validator) (plugins.ValidatedMarket, error) {
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return plugins.ValidatedMarket{}, err
	}
	if validator == nil {
		return plugins.ValidatedMarket{}, errors.New("plugin validator is required")
	}
	if !IsFullCommitOID(checkoutOID) {
		return plugins.ValidatedMarket{}, errors.New("official checkout requires a non-zero lowercase full Git OID")
	}
	validated, err := validator.ValidateMarket(checkoutRoot, true)
	if err != nil {
		return plugins.ValidatedMarket{}, fmt.Errorf("official market snapshot is invalid: %w", err)
	}
	for _, entry := range validated.Manifest.Entries {
		if entry.SignatureKeyID != lock.SignatureKeyID {
			return plugins.ValidatedMarket{}, errors.New("official package signer does not match lock identity")
		}
	}
	return validated, nil
}

// ValidateOfficialMarketAtLock resolves the current built-in official branch,
// validates its isolated tree, and returns the full OID used for this refresh.
// It never reads a sibling development worktree.
func ValidateOfficialMarketAtLock(ctx context.Context, lock OfficialMarketLock, validator *plugins.Validator) (plugins.ValidatedMarket, string, error) {
	if err := ValidateOfficialMarketLock(lock); err != nil {
		return plugins.ValidatedMarket{}, "", err
	}
	temporary, err := os.MkdirTemp("", "nre-official-market-")
	if err != nil {
		return plugins.ValidatedMarket{}, "", err
	}
	defer os.RemoveAll(temporary)
	checkout := filepath.Join(temporary, "checkout")
	source, err := lock.Source(1)
	if err != nil {
		return plugins.ValidatedMarket{}, "", err
	}
	commit, err := (GoGitFetcher{}).Fetch(ctx, source, checkout)
	if err != nil {
		return plugins.ValidatedMarket{}, "", err
	}
	validated, err := ValidateOfficialLockCheckout(lock, checkout, commit, validator)
	return validated, commit, err
}
