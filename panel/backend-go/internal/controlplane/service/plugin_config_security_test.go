package service

import (
	"encoding/json"
	"strings"
	"testing"
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

	preserved, err := pluginMergeWriteOnlyConfig(schema, json.RawMessage(`{"mode":"block"}`), `{"mode":"observe","credentials":{"token":"plaintext-secret"}}`, nil)
	if err != nil || !strings.Contains(string(preserved), "plaintext-secret") || !strings.Contains(string(preserved), `"mode":"block"`) {
		t.Fatalf("preserve result=%s error=%v", preserved, err)
	}
	replaced, err := pluginMergeWriteOnlyConfig(schema, json.RawMessage(`{"mode":"block"}`), string(preserved), map[string]json.RawMessage{"/credentials/token": json.RawMessage(`"replacement"`)})
	if err != nil || strings.Contains(string(replaced), "plaintext-secret") || !strings.Contains(string(replaced), "replacement") {
		t.Fatalf("replace result=%s error=%v", replaced, err)
	}
	if _, err := pluginMergeWriteOnlyConfig(schema, json.RawMessage(`{"mode":"block","credentials":{"token":"caller-plaintext"}}`), string(replaced), nil); err == nil {
		t.Fatal("writeOnly value in ordinary config was accepted")
	}
}
