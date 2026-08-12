package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginWriteOnlyConfigProjectionAndPreservation(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"mode": map[string]any{"type": "string"},
		"credentials": map[string]any{"type": "object", "properties": map[string]any{
			"token": map[string]any{"type": "string", "writeOnly": true, "default": "schema-plaintext", "enum": []any{"schema-plaintext"}},
		}},
	}}
	redacted, fields, err := pluginRedactedConfig(schema, `{"mode":"observe","credentials":{"token":"plaintext-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(struct {
		Config json.RawMessage          `json:"config"`
		Fields []PluginSecretFieldState `json:"secret_fields"`
	}{redacted, fields})
	if strings.Contains(string(encoded), "plaintext-secret") || string(redacted) != `{"credentials":{},"mode":"observe"}` || len(fields) != 1 || fields[0].Pointer != "/credentials/token" || !fields[0].Present {
		t.Fatalf("unsafe writeOnly projection: %s fields=%+v", encoded, fields)
	}
	publicSchema, _ := json.Marshal(pluginPublicConfigSchema(schema))
	if strings.Contains(string(publicSchema), "schema-plaintext") || !strings.Contains(string(publicSchema), `"writeOnly":true`) {
		t.Fatalf("writeOnly schema leaked a secret default: %s", publicSchema)
	}

	public, replacements, retained, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"observe","credentials":{"token":null}}`), json.RawMessage(`{"mode":"block","credentials":{}}`), []storage.PluginInstanceSecretHandle{{Pointer: "/credentials/token", ID: "secret-1"}}, nil)
	if err != nil || strings.Contains(string(public), "plaintext") || len(replacements) != 0 || len(retained) != 1 {
		t.Fatalf("preserve public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
	public, replacements, retained, err = pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"block","credentials":{"token":null}}`), json.RawMessage(`{"mode":"block","credentials":{}}`), retained, map[string]json.RawMessage{"/credentials/token": json.RawMessage(`"replacement"`)})
	if err != nil || strings.Contains(string(public), "replacement") || string(replacements["/credentials/token"]) != `"replacement"` || len(retained) != 0 {
		t.Fatalf("replace public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
	if _, _, _, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"block","credentials":{}}`), json.RawMessage(`{"mode":"block","credentials":{"token":"caller-plaintext"}}`), nil, nil); err == nil {
		t.Fatal("writeOnly value in ordinary config was accepted")
	}
}

func TestLegacyPluginSecretSlotMigrationScrubsRecursivePlaintext(t *testing.T) {
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewPluginService(store, t.TempDir())
	service.SetSecretVault(vault)
	schema := map[string]any{"type": "object", "properties": map[string]any{"accounts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}}}}}}
	instance := storage.PluginInstanceRow{ID: "legacy-instance", PluginID: "plugin", ResourceGroupID: "default"}
	groups := map[string]string{"active": "group-a", "pending": "group-b", "rollback": "group-c"}
	for _, slot := range []string{"active", "pending", "rollback"} {
		public, handles, writes, err := service.migrateLegacyPluginSecretSlot(context.Background(), instance, slot, groups[slot], schema, `{"accounts":[{"token":"legacy-plaintext"}]}`, `[]`)
		if err != nil || strings.Contains(public, "legacy-plaintext") || strings.Contains(handles, "legacy-plaintext") || len(writes) != 1 || !strings.Contains(writes[0].Secret.Purpose, "/accounts/0/token") {
			t.Fatalf("slot=%s public=%s handles=%s writes=%+v err=%v", slot, public, handles, writes, err)
		}
		if len(writes[0].Version.Ciphertext) == 0 || strings.Contains(string(writes[0].Version.Ciphertext), "legacy-plaintext") {
			t.Fatalf("slot=%s did not envelope-encrypt plaintext", slot)
		}
		if writes[0].Secret.ResourceGroupID != groups[slot] {
			t.Fatalf("slot=%s resource group=%q", slot, writes[0].Secret.ResourceGroupID)
		}
	}
	if _, _, _, err := service.migrateLegacyPluginSecretSlot(context.Background(), instance, "pending", "", schema, `{"accounts":[{"token":"legacy-plaintext"}]}`, `[]`); err == nil {
		t.Fatal("pending legacy plaintext migrated without immutable resource ownership")
	}
	if empty, err := legacyPluginSecretSlotEmpty("", `[]`); err != nil || !empty {
		t.Fatalf("empty legacy slot was not accepted: empty=%t err=%v", empty, err)
	}
	for _, handles := range []string{`[{"pointer":"/accounts/0/token","id":"orphan"}]`, `{not-json`} {
		if _, err := legacyPluginSecretSlotEmpty("", handles); err == nil {
			t.Fatalf("empty legacy slot accepted orphaned handles %q", handles)
		}
	}
}

func TestPluginBrokeredConfigSupportsArrayPointersAndExplicitDelete(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"accounts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}}}}}}
	handles := []storage.PluginInstanceSecretHandle{{Pointer: "/accounts/0/token", ID: "secret-a"}, {Pointer: "/accounts/1/token", ID: "secret-b"}}
	public, replacements, retained, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"accounts":[{"token":null},{"token":null}]}`), json.RawMessage(`{"accounts":[{"token":null},{"token":null}]}`), handles, map[string]json.RawMessage{"/accounts/0/token": json.RawMessage(`null`), "/accounts/1/token": json.RawMessage(`"next"`)})
	if err != nil || strings.Contains(string(public), "next") || len(replacements) != 1 || replacements["/accounts/1/token"] == nil || len(retained) != 0 {
		t.Fatalf("array public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
}

func TestPluginBrokeredConfigRejectsArraySecretRebindAndPrunesExplicitRemoval(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"accounts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "writeOnly": true},
	}}}}}
	current := json.RawMessage(`{"accounts":[{"id":"alice","token":null},{"id":"bob","token":null}]}`)
	handles := []storage.PluginInstanceSecretHandle{{Pointer: "/accounts/0/token", ID: "secret-a"}, {Pointer: "/accounts/1/token", ID: "secret-b"}}
	for label, requested := range map[string]json.RawMessage{
		"remove":  json.RawMessage(`{"accounts":[{"id":"alice","token":null}]}`),
		"insert":  json.RawMessage(`{"accounts":[{"id":"other","token":null},{"id":"alice","token":null},{"id":"bob","token":null}]}`),
		"reorder": json.RawMessage(`{"accounts":[{"id":"bob","token":null},{"id":"alice","token":null}]}`),
	} {
		if _, _, _, err := pluginPrepareBrokeredConfig(schema, current, requested, handles, nil); err == nil {
			t.Fatalf("%s retained positional array secrets", label)
		}
	}
	public, replacements, retained, err := pluginPrepareBrokeredConfig(schema, current, json.RawMessage(`{"accounts":[{"id":"alice","token":null}]}`), handles, map[string]json.RawMessage{
		"/accounts/0/token": json.RawMessage(`"alice-rotated"`),
		"/accounts/1/token": json.RawMessage(`null`),
	})
	if err != nil || len(retained) != 0 || len(replacements) != 1 || strings.Contains(string(public), "rotated") {
		t.Fatalf("explicit array retirement failed: public=%s replacements=%v retained=%+v err=%v", public, replacements, retained, err)
	}
}

func TestPluginSecretLogValuesCoverCanonicalObjectsAndStringLeaves(t *testing.T) {
	values, err := pluginSecretLogValues(json.RawMessage(`{"credentials":{"token":"quoted-secret","nested":{"key":"nested-secret"}}}`), []storage.PluginInstanceSecretHandle{{Pointer: "/credentials", ID: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(values, "\n")
	if !strings.Contains(joined, `"token":"quoted-secret"`) || !strings.Contains(joined, "quoted-secret") || !strings.Contains(joined, "nested-secret") {
		t.Fatalf("secret log values = %q", values)
	}
}
