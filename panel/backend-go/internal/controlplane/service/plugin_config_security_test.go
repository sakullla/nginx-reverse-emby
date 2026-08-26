//go:build exhaustive && !integration

package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const hostInjectedConfigSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["mode","generation","secret_ref","resource_group_ref"],
  "properties":{
    "mode":{"type":"string","minLength":1},
    "note":{"type":"string"},
    "generation":{"type":"string","minLength":1,"maxLength":128,"hostInjected":true},
    "secret_ref":{"type":"string","pattern":"^[a-z0-9][a-z0-9._:/-]{0,127}$","hostInjected":true},
    "resource_group_ref":{"type":"string","minLength":1,"hostInjected":true}
  }
}`

const unmarkedGenerationSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["mode","generation"],
  "properties":{
    "mode":{"type":"string","minLength":1},
    "generation":{"type":"string","minLength":1}
  }
}`

const nestedHostInjectedConfigSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["apps"],
  "properties":{
    "apps":{
      "type":"array",
      "minItems":1,
      "items":{
        "type":"object",
        "additionalProperties":false,
        "required":["id","image","rule_ref","generation"],
        "properties":{
          "id":{"type":"string","minLength":1,"hostInjected":true},
          "image":{"type":"string","minLength":1,"maxLength":512},
          "rule_ref":{"type":"string","minLength":1,"maxLength":128},
          "generation":{"type":"string","minLength":1,"maxLength":128,"hostInjected":true},
          "secret_refs":{"type":"array","hostInjected":true,"items":{"type":"string"}},
          "auto_update":{"type":"boolean"}
        }
      }
    }
  }
}`

const nestedUnmarkedGenerationSchema = `{
  "type":"object",
  "additionalProperties":false,
  "required":["apps"],
  "properties":{
    "apps":{
      "type":"array",
      "minItems":1,
      "items":{
        "type":"object",
        "additionalProperties":false,
        "required":["image","generation"],
        "properties":{
          "image":{"type":"string","minLength":1},
          "generation":{"type":"string","minLength":1}
        }
      }
    }
  }
}`

func TestPluginConfigureInjectsHostInjectedKeysThenValidates(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-injected", hostInjectedConfigSchema, false)
	ctx := WithSystemMutationPrincipal(context.Background(), "system:owner")
	instance, err := fixture.service.ConfigureMutation(ctx, pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-1", json.RawMessage(`{"mode":"strict","note":"keep-me"}`)))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	if got["mode"] != "strict" || got["note"] != "keep-me" {
		t.Fatalf("user fields changed: %+v", got)
	}
	generation, _ := got["generation"].(string)
	if generation == "" {
		t.Fatalf("hostInjected generation was not written: %+v", got)
	}
	if got["resource_group_ref"] != "resource-group/injected" {
		t.Fatalf("resource_group_ref = %v, want catalog ref", got["resource_group_ref"])
	}
	secretRef, _ := got["secret_ref"].(string)
	if secretRef == "" || secretRef == "instance-1" {
		t.Fatalf("secret_ref = %q, want a vault handle id", secretRef)
	}
	if _, err := fixture.store.GetSecret(ctx, secretRef); err != nil {
		t.Fatalf("injected secret_ref %q is not a stored vault handle: %v", secretRef, err)
	}
	if err := plugins.ValidateConfig(fixture.schema, mustPluginJSON(t, got)); err != nil {
		t.Fatalf("injected config failed ValidateConfig: %v", err)
	}
	assertInjectedGenerationMatchesLifecycle(t, fixture, instance.ID, generation)
}

func TestPluginConfigureRecreatesHostSecretAfterRetiredNameCollision(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-recreate", hostInjectedConfigSchema, false)
	ctx := WithSystemMutationPrincipal(context.Background(), "system:owner")
	retiredAt := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	const instanceID = "instance-reused"
	const retiredSecretID = "sec-retired-host"
	if err := fixture.store.CreateSecret(ctx, storage.SecretRow{
		ID: retiredSecretID, Name: "plugin-host-" + instanceID + "-secret-ref",
		Purpose: "plugin-config:" + instanceID + ":/secret_ref", OwnerUserID: "admin", ResourceGroupID: "default",
		ActiveVersion: 1, Fingerprint: "retired", CreatedAt: retiredAt, RotatedAt: retiredAt, RetiredAt: &retiredAt,
	}, storage.SecretVersionRow{
		SecretID: retiredSecretID, Version: 1, KeyID: "test", Nonce: []byte("retired"), Ciphertext: []byte("retired"),
		CreatedAt: retiredAt, DestroyedAt: &retiredAt,
	}); err != nil {
		t.Fatalf("CreateSecret(retired host handle) error = %v", err)
	}

	instance, err := fixture.service.ConfigureMutation(ctx, pluginHostInjectedConfigureRequest(fixture.pluginID, instanceID, json.RawMessage(`{"mode":"strict"}`)))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	configured := pluginHostInjectedPendingObject(t, instance)
	secretRef, _ := configured["secret_ref"].(string)
	secret, err := fixture.store.GetSecret(ctx, secretRef)
	if err != nil {
		t.Fatalf("GetSecret(%q) error = %v", secretRef, err)
	}
	wantName := "plugin-host-" + instance.PendingOperationID + "-secret-ref"
	if secret.Name != wantName {
		t.Fatalf("host secret name = %q, want operation-bound %q", secret.Name, wantName)
	}
}

func TestPluginApplyRuntimeGenerationOverridesOnlyDeclaredHostFields(t *testing.T) {
	t.Parallel()
	topSchema, err := plugins.DecodeConfigSchema([]byte(hostInjectedConfigSchema))
	if err != nil {
		t.Fatal(err)
	}
	topRaw := json.RawMessage(`{"mode":"strict","generation":"stored-generation","secret_ref":"secret-ref","resource_group_ref":"resource-group/main"}`)
	topUpdated, err := pluginApplyRuntimeGeneration(topSchema, topRaw, "runtime-generation")
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(topUpdated, &top); err != nil {
		t.Fatal(err)
	}
	if top["generation"] != "runtime-generation" || top["resource_group_ref"] != "resource-group/main" || top["secret_ref"] != "secret-ref" {
		t.Fatalf("top-level runtime generation override changed unrelated fields: %+v", top)
	}

	nestedSchema, err := plugins.DecodeConfigSchema([]byte(nestedHostInjectedConfigSchema))
	if err != nil {
		t.Fatal(err)
	}
	nestedRaw := json.RawMessage(`{"apps":[{"id":"host-id","image":"nginx:latest","rule_ref":"rule-1","auto_update":true,"generation":"stored-generation","secret_refs":[]}]}`)
	nestedUpdated, err := pluginApplyRuntimeGeneration(nestedSchema, nestedRaw, "runtime-generation")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(nestedUpdated, &got); err != nil {
		t.Fatal(err)
	}
	app, nested := pluginHostInjectedApp(t, got)
	if nested != "runtime-generation" {
		t.Fatalf("runtime generation was not applied recursively: %+v", got)
	}
	if app["id"] != "host-id" || app["image"] != "nginx:latest" {
		t.Fatalf("unrelated host and user fields changed: %+v", app)
	}
}

func TestPluginConfigureDoesNotGuessUnmarkedRequiredNames(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-unmarked", unmarkedGenerationSchema, false)
	_, err := fixture.service.ConfigureMutation(WithSystemMutationPrincipal(context.Background(), "system:owner"), pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-unmarked", json.RawMessage(`{"mode":"strict"}`)))
	if err == nil {
		t.Fatal("ConfigureMutation filled unmarked required generation by name")
	}
	if !strings.Contains(err.Error(), "generation") {
		t.Fatalf("ConfigureMutation() error = %v, want missing unmarked generation", err)
	}
}

func TestPluginConfigureKeepsSubmittedUserFields(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-keep", hostInjectedConfigSchema, false)
	instance, err := fixture.service.ConfigureMutation(WithSystemMutationPrincipal(context.Background(), "system:owner"), pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-keep", json.RawMessage(`{"mode":"strict","note":"user-note","generation":"user-generation"}`)))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	if got["mode"] != "strict" || got["note"] != "user-note" || got["generation"] != "user-generation" {
		t.Fatalf("submitted fields were overwritten: %+v", got)
	}
	if got["resource_group_ref"] != "resource-group/injected" {
		t.Fatalf("missing hostInjected resource_group_ref: %+v", got)
	}
}

func TestPluginPublishDoesNotGuessUnmarkedRequiredNames(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-publish-unmarked", unmarkedGenerationSchema, true)
	_, err := callPluginPublish(t, fixture.service, WithSystemMutationPrincipal(context.Background(), "system:owner"), map[string]any{
		"PluginID":        fixture.pluginID,
		"InstanceID":      "instance-publish-unmarked",
		"ResourceGroupID": "default",
		"Targets":         []string{"local"},
		"PolicyChains":    []string{},
		"Config":          json.RawMessage(`{"mode":"strict"}`),
		"FrontendURL":     "https://emby.example.com",
		"ActorID":         "admin",
		"Actor":           pluginPublishAdmin(),
	})
	if err == nil {
		t.Fatal("PublishMutation filled unmarked required generation by name")
	}
	if !strings.Contains(err.Error(), "generation") {
		t.Fatalf("PublishMutation() error = %v, want missing unmarked generation", err)
	}
}

func TestPluginPublishKeepsSubmittedUserFields(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-publish-keep", hostInjectedConfigSchema, true)
	instance, err := callPluginPublish(t, fixture.service, WithSystemMutationPrincipal(context.Background(), "system:owner"), map[string]any{
		"PluginID":        fixture.pluginID,
		"InstanceID":      "instance-publish-keep",
		"ResourceGroupID": "default",
		"Targets":         []string{"local"},
		"PolicyChains":    []string{},
		"Config":          json.RawMessage(`{"mode":"strict","note":"user-note","generation":"user-generation"}`),
		"FrontendURL":     "https://emby.example.com",
		"ActorID":         "admin",
		"Actor":           pluginPublishAdmin(),
	})
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	if got["mode"] != "strict" || got["note"] != "user-note" || got["generation"] != "user-generation" {
		t.Fatalf("submitted fields were overwritten: %+v", got)
	}
	if got["resource_group_ref"] != "resource-group/injected" {
		t.Fatalf("missing hostInjected resource_group_ref: %+v", got)
	}
}

func TestPluginApplyMissingHostInjectedUsesSchemaDefault(t *testing.T) {
	t.Parallel()
	schema, err := plugins.DecodeConfigSchema([]byte(`{
		"type":"object",
		"additionalProperties":false,
		"required":["records"],
		"properties":{
			"records":{
				"type":"array",
				"maxItems":128,
				"hostInjected":true,
				"default":[],
				"items":{
					"type":"object",
					"additionalProperties":false,
					"required":["id","body","generation"],
					"properties":{
						"id":{"type":"string","minLength":1,"hostInjected":true},
						"body":{"type":"string","minLength":1},
						"generation":{"type":"string","minLength":1,"maxLength":128,"hostInjected":true}
					}
				}
			},
			"enabled":{"type":"boolean","hostInjected":true,"default":false}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := pluginApplyMissingHostInjected(schema, map[string]any{}, map[string]any{}, "", pluginHostInjectedSource{})
	if err != nil {
		t.Fatal(err)
	}
	object, _ := got.(map[string]any)
	records, _ := object["records"].([]any)
	if records == nil || len(records) != 0 {
		t.Fatalf("omitted hostInjected array default = %#v, want empty array", object["records"])
	}
	if enabled, _ := object["enabled"].(bool); enabled {
		t.Fatalf("omitted hostInjected boolean default = %#v, want false", object["enabled"])
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.ValidateConfig(schema, encoded); err != nil {
		t.Fatalf("injected schema defaults failed ValidateConfig: %v", err)
	}

	kept, err := pluginApplyMissingHostInjected(schema, map[string]any{
		"records": []any{map[string]any{"body": "keep"}},
		"enabled": true,
	}, map[string]any{}, "", pluginHostInjectedSource{Generation: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	keptObject, _ := kept.(map[string]any)
	keptRecords, _ := keptObject["records"].([]any)
	if len(keptRecords) != 1 {
		t.Fatalf("submitted hostInjected array was not kept: %#v", kept)
	}
	item, _ := keptRecords[0].(map[string]any)
	if item["body"] != "keep" {
		t.Fatalf("submitted item changed: %#v", item)
	}
	if id, _ := item["id"].(string); id == "" {
		t.Fatalf("nested hostInjected id was not written: %#v", item)
	}
	if generation, _ := item["generation"].(string); generation != "generation-1" {
		t.Fatalf("nested hostInjected generation = %v", item["generation"])
	}
	if enabled, _ := keptObject["enabled"].(bool); !enabled {
		t.Fatalf("submitted hostInjected boolean was overwritten: %#v", keptObject["enabled"])
	}
}

func TestPluginConfigureInjectsNestedHostInjectedKeysThenValidates(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-nested", nestedHostInjectedConfigSchema, false)
	instance, err := fixture.service.ConfigureMutation(WithSystemMutationPrincipal(context.Background(), "system:owner"), pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-nested", json.RawMessage(`{"apps":[{"image":"nginx:latest","rule_ref":"rule-1","auto_update":true}]}`)))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	app, generation := pluginHostInjectedApp(t, got)
	if app["image"] != "nginx:latest" || app["rule_ref"] != "rule-1" || app["auto_update"] != true {
		t.Fatalf("user fields changed: %+v", app)
	}
	if generation == "" {
		t.Fatalf("nested hostInjected generation was not written: %+v", got)
	}
	if id, _ := app["id"].(string); id == "" {
		t.Fatalf("nested hostInjected id was not written: %+v", app)
	}
	if err := plugins.ValidateConfig(fixture.schema, mustPluginJSON(t, got)); err != nil {
		t.Fatalf("injected nested config failed ValidateConfig: %v", err)
	}
	assertInjectedGenerationMatchesLifecycle(t, fixture, instance.ID, generation)
}

func TestPluginConfigureDoesNotGuessNestedUnmarkedRequiredNames(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-nested-unmarked", nestedUnmarkedGenerationSchema, false)
	_, err := fixture.service.ConfigureMutation(WithSystemMutationPrincipal(context.Background(), "system:owner"), pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-nested-unmarked", json.RawMessage(`{"apps":[{"image":"nginx:latest"}]}`)))
	if err == nil {
		t.Fatal("ConfigureMutation filled unmarked nested required generation by name")
	}
	if !strings.Contains(err.Error(), "generation") {
		t.Fatalf("ConfigureMutation() error = %v, want missing unmarked nested generation", err)
	}
}

func TestPluginConfigureKeepsNestedSubmittedUserFields(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-nested-keep", nestedHostInjectedConfigSchema, false)
	instance, err := fixture.service.ConfigureMutation(WithSystemMutationPrincipal(context.Background(), "system:owner"), pluginHostInjectedConfigureRequest(fixture.pluginID, "instance-nested-keep", json.RawMessage(`{"apps":[{"image":"nginx:latest","rule_ref":"rule-1","auto_update":true,"generation":"user-generation"}]}`)))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	app, generation := pluginHostInjectedApp(t, got)
	if app["image"] != "nginx:latest" || app["rule_ref"] != "rule-1" || app["auto_update"] != true || generation != "user-generation" {
		t.Fatalf("submitted nested fields were overwritten: %+v", app)
	}
}

func TestPluginPublishInjectsNestedHostInjectedKeysThenValidates(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-publish-nested", nestedHostInjectedConfigSchema, true)
	instance, err := callPluginPublish(t, fixture.service, WithSystemMutationPrincipal(context.Background(), "system:owner"), map[string]any{
		"PluginID":        fixture.pluginID,
		"InstanceID":      "instance-publish-nested",
		"ResourceGroupID": "default",
		"Targets":         []string{"local"},
		"PolicyChains":    []string{},
		"Config":          json.RawMessage(`{"apps":[{"image":"nginx:latest","rule_ref":"rule-1","auto_update":false}]}`),
		"FrontendURL":     "https://emby.example.com",
		"ActorID":         "admin",
		"Actor":           pluginPublishAdmin(),
	})
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	app, generation := pluginHostInjectedApp(t, got)
	if app["image"] != "nginx:latest" || app["rule_ref"] != "rule-1" || app["auto_update"] != false {
		t.Fatalf("published nested user fields changed: %+v", app)
	}
	if generation == "" {
		t.Fatalf("publish did not inject nested hostInjected generation: %+v", got)
	}
	if err := plugins.ValidateConfig(fixture.schema, mustPluginJSON(t, got)); err != nil {
		t.Fatalf("published nested config failed ValidateConfig: %v", err)
	}
	assertInjectedGenerationMatchesLifecycle(t, fixture, instance.ID, generation)
	assertInjectedGenerationMatchesRPC(t, fixture, instance.ID, generation)
}

func TestPluginPublishInjectsHostInjectedKeysThenValidates(t *testing.T) {
	t.Parallel()
	fixture := newPluginHostInjectedFixture(t, "official.host-publish", hostInjectedConfigSchema, true)
	ctx := WithSystemMutationPrincipal(context.Background(), "system:owner")
	instance, err := callPluginPublish(t, fixture.service, ctx, map[string]any{
		"PluginID":        fixture.pluginID,
		"InstanceID":      "instance-publish",
		"ResourceGroupID": "default",
		"Targets":         []string{"local"},
		"PolicyChains":    []string{},
		"Config":          json.RawMessage(`{"mode":"strict","note":"published"}`),
		"FrontendURL":     "https://emby.example.com",
		"ActorID":         "admin",
		"Actor":           pluginPublishAdmin(),
	})
	if err != nil {
		t.Fatalf("PublishMutation() error = %v", err)
	}
	got := pluginHostInjectedPendingObject(t, instance)
	if got["mode"] != "strict" || got["note"] != "published" {
		t.Fatalf("published user fields changed: %+v", got)
	}
	if got["generation"] == "" || got["resource_group_ref"] != "resource-group/injected" {
		t.Fatalf("publish did not inject host fields: %+v", got)
	}
	if err := plugins.ValidateConfig(fixture.schema, mustPluginJSON(t, got)); err != nil {
		t.Fatalf("published config failed ValidateConfig: %v", err)
	}
	generation, _ := got["generation"].(string)
	assertInjectedGenerationMatchesLifecycle(t, fixture, instance.ID, generation)
	assertInjectedGenerationMatchesRPC(t, fixture, instance.ID, generation)
}

type pluginHostInjectedHarness struct {
	pluginID string
	schema   map[string]any
	store    *storage.GormStore
	service  *PluginService
}

func newPluginHostInjectedFixture(t *testing.T, pluginID, schemaJSON string, httpBackend bool) pluginHostInjectedHarness {
	t.Helper()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "local", Name: "local", Version: "1.0.0", Platform: runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: `["package_manifest_v1"]`,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)
	if err := store.UpsertBuiltinResourceGroup(t.Context(), storage.ResourceGroupRow{
		ID: "default", Name: "default", Description: "default", Builtin: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })
	schema, validator := seedInstalledHostInjectedPackage(t, store, cacheRoot, pluginID, schemaJSON, httpBackend)
	svc := NewPluginServiceWithValidator(store, validator, cacheRoot)
	key := make([]byte, 32)
	copy(key, []byte("0123456789abcdef0123456789abcdef"))
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetSecretVault(vault)
	if httpBackend {
		svc.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	}
	return pluginHostInjectedHarness{pluginID: pluginID, schema: schema, store: store, service: svc}
}

func seedInstalledHostInjectedPackage(t *testing.T, store *storage.GormStore, cacheRoot, pluginID, schemaJSON string, httpBackend bool) (map[string]any, *plugins.Validator) {
	t.Helper()
	key := publishFixtureSigningKey()
	publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	validator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion:         "0.0.0-dev",
		TrustedSigners:      map[string]ed25519.PublicKey{"test-fixture": key.Public().(ed25519.PublicKey)},
		TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
		TargetGOOS:          runtime.GOOS,
		TargetGOARCH:        runtime.GOARCH,
	})
	root := writeHostInjectedPackage(t, pluginID, schemaJSON, httpBackend, key)
	validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{
		ID: pluginID, Version: "1.0.0", SignatureKeyID: "test-fixture",
	})
	if err != nil {
		t.Fatalf("ValidatePackage() error = %v", err)
	}
	trust := marketplace.SignatureTrust{
		SourceID: "host-injected-fixture", SourceKind: marketplace.SourceKindCustom,
		KeyID: "test-fixture", PublicKey: publicKey, Fingerprint: fingerprint,
	}
	cachePath, err := marketplace.ImportVerifiedPackage(cacheRoot, validated, validator, trust)
	if err != nil {
		t.Fatalf("ImportVerifiedPackage() error = %v", err)
	}
	now := time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)
	manifestJSON, err := json.Marshal(validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	schemaStored, err := json.Marshal(validated.ConfigSchema)
	if err != nil {
		t.Fatal(err)
	}
	cleanupJSON, err := json.Marshal(validated.Manifest.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	row := storage.PluginPackageRow{
		Digest: validated.Digest, PluginID: pluginID, Version: "1.0.0",
		SourceID: trust.SourceID, SourceKind: trust.SourceKind, SignatureKeyID: trust.KeyID,
		SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint,
		CachePath: cachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaStored),
		VerifiedAt: now,
	}
	row.Identity = storage.PluginPackageIdentity(validated.Digest, trust.SourceID, trust.Fingerprint)
	packageRow, artifacts, err := storage.ProjectPluginPackage(row, validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	operation := storage.PluginOperationRow{
		ID: "op-install-" + pluginID, PluginID: pluginID, Kind: "install", Status: "succeeded",
		TargetPackageDigest: validated.Digest, TargetPackageIdentity: packageRow.Identity,
		TargetSignatureKeyID: trust.KeyID, TargetSignaturePublicKey: trust.PublicKey,
		TargetSignatureFingerprint: trust.Fingerprint, ActorID: "admin", AgentResultsJSON: `[]`,
		SourceID: trust.SourceID, SourceKind: trust.SourceKind, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: validated.Digest, ActivePackageIdentity: packageRow.Identity,
		RuntimeKind: validated.Manifest.Runtime.Kind, RuntimeABI: validated.Manifest.Runtime.ABI,
		HostScope: validated.Manifest.Runtime.HostScope, ActiveSourceID: trust.SourceID,
		ActiveSourceKind: trust.SourceKind, ActiveSignatureKeyID: trust.KeyID,
		ActiveSignaturePublicKey: trust.PublicKey, ActiveSignatureFingerprint: trust.Fingerprint,
		DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: string(cleanupJSON),
		LastOperationID: operation.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.InstallPlugin(t.Context(), storage.PluginInstallTransaction{
		Package: packageRow, Artifacts: artifacts, Installed: installed, Operation: operation,
		Audit: storage.AuditEventRow{
			ID: "audit-install-" + pluginID, ActorID: "admin", Action: "plugin.install",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("InstallPlugin() error = %v", err)
	}
	return validated.ConfigSchema, validator
}

func writeHostInjectedPackage(t *testing.T, pluginID, schemaJSON string, httpBackend bool, key ed25519.PrivateKey) string {
	t.Helper()
	root := t.TempDir()
	writePublishFile(t, root, plugins.ConfigSchemaFile, schemaJSON)
	if httpBackend {
		artifact, dest := publishRPCArtifact(t, root)
		sum := sha256.Sum256(artifact)
		writePublishFile(t, root, plugins.PackageManifestFile, fmtHostInjectedRPCManifest(pluginID, dest, hex.EncodeToString(sum[:]), int64(len(artifact))))
	} else {
		artifact := publishWASMArtifact(t)
		sum := sha256.Sum256(artifact)
		writePublishBytes(t, root, "artifacts/policy.wasm", artifact)
		writePublishFile(t, root, plugins.PackageManifestFile, fmtHostInjectedWASMManifest(pluginID, hex.EncodeToString(sum[:]), int64(len(artifact))))
	}
	digest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageDigestFile, digest+"\n")
	writePublishFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(digest)))+"\n")
	return root
}

func fmtHostInjectedWASMManifest(pluginID, digest string, size int64) string {
	return strings.Replace(fmtPublishWASMManifest(pluginID, digest, size), "name: WAF\n", "name: Host Injected\nmetadata:\n  resource.group.ref: resource-group/injected\n", 1)
}

func fmtHostInjectedRPCManifest(pluginID, artifactPath, digest string, size int64) string {
	return strings.Replace(fmtPublishRPCManifest(pluginID, artifactPath, digest, size), "name: Publish HTTP\n", "name: Host Injected HTTP\nmetadata:\n  resource.group.ref: resource-group/injected\n", 1)
}

func assertInjectedGenerationMatchesLifecycle(t *testing.T, fixture pluginHostInjectedHarness, instanceID, injected string) {
	t.Helper()
	row, ok, err := fixture.store.GetPluginInstance(t.Context(), instanceID)
	if err != nil || !ok {
		t.Fatalf("GetPluginInstance() ok=%v err=%v", ok, err)
	}
	installed, ok, err := fixture.store.GetInstalledPlugin(t.Context(), fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	packageRow, ok, err := fixture.store.GetPluginPackageByIdentity(t.Context(), installed.ActivePackageIdentity)
	if err != nil || !ok {
		t.Fatalf("GetPluginPackageByIdentity() ok=%v err=%v", ok, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	artifacts, err := fixture.store.ListPluginArtifactsByIdentity(t.Context(), packageRow.Identity)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := pluginSelectAgentGenerationArtifact(artifacts, manifest, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := fixture.service.controlPlaneGenerationGrants(t.Context(), installed, packageRow)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := storage.BuildPluginGeneration(installed, row, packageRow, manifest, artifact, grants, "local")
	if err != nil {
		t.Fatalf("BuildPluginGeneration() error = %v", err)
	}
	if generation.ID != injected {
		t.Fatalf("injected generation %q != lifecycle generation.ID %q", injected, generation.ID)
	}
}

func assertInjectedGenerationMatchesRPC(t *testing.T, fixture pluginHostInjectedHarness, instanceID, injected string) {
	t.Helper()
	generations, err := fixture.store.LoadAgentPluginGenerations(t.Context(), "local", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations() error = %v", err)
	}
	for _, generation := range generations {
		if generation.InstanceID == instanceID {
			if generation.ID != injected {
				t.Fatalf("injected generation %q != RPC generation.ID %q", injected, generation.ID)
			}
			return
		}
	}
	t.Fatalf("RPC generation missing for instance %s: %+v", instanceID, generations)
}

func pluginHostInjectedConfigureRequest(pluginID, instanceID string, config json.RawMessage) PluginConfigureRequest {
	chains := []string{}
	return PluginConfigureRequest{
		PluginID: pluginID, InstanceID: instanceID, ResourceGroupID: "default",
		Targets: []string{"local"}, PolicyChains: &chains, Config: config,
		ActorID: "admin", Actor: pluginPublishAdmin(),
	}
}

func pluginHostInjectedApp(t *testing.T, got map[string]any) (map[string]any, string) {
	t.Helper()
	apps, _ := got["apps"].([]any)
	if len(apps) == 0 {
		t.Fatalf("apps missing: %+v", got)
	}
	app, _ := apps[0].(map[string]any)
	if app == nil {
		t.Fatalf("apps[0] is not an object: %+v", got)
	}
	generation, _ := app["generation"].(string)
	return app, generation
}

func pluginHostInjectedPendingObject(t *testing.T, instance PluginInstanceDetail) map[string]any {
	t.Helper()
	raw := instance.PendingConfig
	if len(raw) == 0 {
		raw = instance.Config
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode instance config: %v", err)
	}
	return object
}

func mustPluginJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
