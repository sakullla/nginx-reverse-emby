package marketplace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestOfficialLockRequiresRepositoryAndTrustIdentity(t *testing.T) {
	lock := validOfficialLock()
	for name, mutate := range map[string]func(*OfficialMarketLock){
		"wrong schema": func(value *OfficialMarketLock) { value.SchemaVersion = 2 },
		"wrong repo":   func(value *OfficialMarketLock) { value.Repository = "https://example.invalid/market.git" },
		"tag pin":      func(value *OfficialMarketLock) { value.RefKind = GitRefKindTag },
		"invalid ref":  func(value *OfficialMarketLock) { value.RefName = "refs/heads/main" },
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

func TestOfficialLockSchemaRejectsVersionPins(t *testing.T) {
	base := "schema_version: 1\nrepository: " + OfficialSourceURL + "\nref_kind: branch\nref_name: official-market\nsdk_abis: [nre:policy/v1, nre:rpc/v1]\nsignature_key_id: " + plugins.OfficialSignatureKeyID + "\n"
	for name, field := range map[string]string{
		"commit":        "commit: " + strings.Repeat("a", 40) + "\n",
		"market_digest": "market_sha256: " + strings.Repeat("b", 64) + "\n",
		"verified_at":   "verified_at: 2026-08-08T00:00:00Z\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), OfficialMarketLockFile)
			if err := os.WriteFile(path, []byte(base+field), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadOfficialMarketLock(path); err == nil {
				t.Fatal("version-pinned official market policy was accepted")
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

func TestRepositoryOfficialMarketPolicyTracksMovableBranch(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", OfficialMarketLockFile)
	lock, err := ReadOfficialMarketLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if lock.RefKind != GitRefKindBranch || lock.RefName != "official-market" {
		t.Fatalf("repository official market policy = %+v", lock)
	}
}

func TestOfficialLockCheckoutRequiresFullOIDBeforeValidatingSignedTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, plugins.MarketManifestFile), []byte("schema_version: 1\nname: Official\nplugins: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := validOfficialLock()
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	for _, oid := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		_, err := ValidateOfficialLockCheckout(lock, root, oid, validator)
		if err == nil || strings.Contains(err.Error(), "requires a non-zero lowercase full Git OID") {
			t.Fatalf("full OID %s did not reach signed market validation: %v", oid, err)
		}
	}
	if _, err := ValidateOfficialLockCheckout(lock, root, "main", validator); err == nil {
		t.Fatal("non-OID official checkout provenance was accepted")
	}
}

func validOfficialLock() OfficialMarketLock {
	return OfficialMarketLock{
		SchemaVersion: 1, Repository: OfficialSourceURL, RefKind: GitRefKindBranch, RefName: "official-market",
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignatureKeyID: plugins.OfficialSignatureKeyID,
	}
}
