package service

import (
	"encoding/json"
	"strings"
	"testing"

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

	public, replacements, retained, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"block","credentials":{}}`), []storage.PluginInstanceSecretHandle{{Pointer: "/credentials/token", ID: "secret-1"}}, nil)
	if err != nil || strings.Contains(string(public), "plaintext") || len(replacements) != 0 || len(retained) != 1 {
		t.Fatalf("preserve public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
	public, replacements, retained, err = pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"block","credentials":{}}`), retained, map[string]json.RawMessage{"/credentials/token": json.RawMessage(`"replacement"`)})
	if err != nil || strings.Contains(string(public), "replacement") || string(replacements["/credentials/token"]) != `"replacement"` || len(retained) != 0 {
		t.Fatalf("replace public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
	if _, _, _, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"mode":"block","credentials":{"token":"caller-plaintext"}}`), nil, nil); err == nil {
		t.Fatal("writeOnly value in ordinary config was accepted")
	}
}

func TestPluginBrokeredConfigSupportsArrayPointersAndExplicitDelete(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"accounts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}}}}}}
	handles := []storage.PluginInstanceSecretHandle{{Pointer: "/accounts/0/token", ID: "secret-a"}, {Pointer: "/accounts/1/token", ID: "secret-b"}}
	public, replacements, retained, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(`{"accounts":[{"token":null},{"token":null}]}`), handles, map[string]json.RawMessage{"/accounts/0/token": json.RawMessage(`null`), "/accounts/1/token": json.RawMessage(`"next"`)})
	if err != nil || strings.Contains(string(public), "next") || len(replacements) != 1 || replacements["/accounts/1/token"] == nil || len(retained) != 0 {
		t.Fatalf("array public=%s replacements=%v retained=%+v error=%v", public, replacements, retained, err)
	}
}
