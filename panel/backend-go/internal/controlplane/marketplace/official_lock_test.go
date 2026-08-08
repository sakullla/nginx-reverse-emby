package marketplace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk"
)

func TestOfficialLockRequiresImmutableCandidateIdentity(t *testing.T) {
	lock := validOfficialLock()
	for name, mutate := range map[string]func(*OfficialMarketLock){
		"movable ref":  func(value *OfficialMarketLock) { value.Commit = "main" },
		"zero oid":     func(value *OfficialMarketLock) { value.Commit = strings.Repeat("0", 40) },
		"wrong repo":   func(value *OfficialMarketLock) { value.Repository = "https://example.invalid/market.git" },
		"wrong ABI":    func(value *OfficialMarketLock) { value.SDKABIs = []string{pluginsdk.PolicyABIV1} },
		"wrong signer": func(value *OfficialMarketLock) { value.SignatureKeyID = "custom" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := lock
			candidate.SDKABIs = append([]string(nil), lock.SDKABIs...)
			mutate(&candidate)
			if err := ValidateOfficialMarketLock(candidate); err == nil {
				t.Fatal("invalid official lock was accepted")
			}
		})
	}
}

func TestOfficialLockAcceptsOnlyRFC3339ZeroOffsetTimestamps(t *testing.T) {
	for _, verifiedAt := range []string{"2026-08-08T00:00:00Z", "2026-08-08T00:00:00+00:00"} {
		t.Run("accept_"+strings.ReplaceAll(verifiedAt, ":", "_"), func(t *testing.T) {
			lock := validOfficialLock()
			lock.VerifiedAt = verifiedAt
			if err := ValidateOfficialMarketLock(lock); err != nil {
				t.Fatalf("zero-offset RFC3339 timestamp %q rejected: %v", verifiedAt, err)
			}
		})
	}
	for _, verifiedAt := range []string{"2026-08-08T08:00:00+08:00", "2026-08-07T19:00:00-05:00"} {
		t.Run("reject_"+strings.ReplaceAll(verifiedAt, ":", "_"), func(t *testing.T) {
			lock := validOfficialLock()
			lock.VerifiedAt = verifiedAt
			if err := ValidateOfficialMarketLock(lock); err == nil {
				t.Fatalf("non-zero-offset RFC3339 timestamp %q accepted", verifiedAt)
			}
		})
	}
}

func TestOfficialLockPathConfigurationRequiresAbsoluteRegularFile(t *testing.T) {
	if _, err := ResolveOfficialMarketLockPath(OfficialMarketLockFile); err == nil {
		t.Fatal("relative official lock configuration was accepted")
	}
	configured := filepath.Join(t.TempDir(), OfficialMarketLockFile)
	if err := os.WriteFile(configured, []byte("pending"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveOfficialMarketLockPath(configured)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Clean(configured) {
		t.Fatalf("configured official lock path = %q, want %q", resolved, configured)
	}
}

func TestOfficialLockCheckoutUsesOIDMarketDigestAndCleanTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, plugins.MarketManifestFile), []byte("schema_version: 1\nname: Official\nplugins: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := validOfficialLock()
	digest, err := MarketManifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	lock.MarketSHA256 = digest
	validated, err := ValidateOfficialLockCheckout(lock, root, lock.Commit, plugins.NewValidator(plugins.ValidatorOptions{}))
	if err != nil || len(validated.Packages) != 0 {
		t.Fatalf("official lock checkout validation: %+v, %v", validated, err)
	}
	if _, err := ValidateOfficialLockCheckout(lock, root, strings.Repeat("b", 40), plugins.NewValidator(plugins.ValidatorOptions{})); err == nil {
		t.Fatal("mismatched checkout OID was accepted")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateOfficialLockCheckout(lock, root, lock.Commit, plugins.NewValidator(plugins.ValidatorOptions{})); err == nil {
		t.Fatal("checkout with Git metadata was accepted")
	}
}

func validOfficialLock() OfficialMarketLock {
	return OfficialMarketLock{
		SchemaVersion: 1, Repository: OfficialSourceURL, Commit: strings.Repeat("a", 40), MarketSHA256: strings.Repeat("b", 64),
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignatureKeyID: plugins.OfficialSignatureKeyID, VerifiedAt: "2026-08-08T00:00:00Z",
	}
}
