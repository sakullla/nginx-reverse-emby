//go:build !integration

package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclarativeUIProjectionRejectsExecutableMarkupAndWriteOnlyMismatch(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "writeOnly": true}}}
	valid := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{
			map[string]any{"type": "text", "id": "name", "label": "Name", "binding": "/name"},
			map[string]any{"type": "secret", "id": "token", "label": "Token", "binding": "/token"},
		},
		"actions": []any{map[string]any{"type": "submit", "id": "save", "label": "Save"}},
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if document, err := ProjectDeclarativeUI(data, schema, nil); err != nil || len(document.Components) != 2 {
		t.Fatalf("valid=%+v err=%v", document, err)
	}
	valid["title"] = "<script>run()</script>"
	bad, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(bad, schema, nil); err == nil {
		t.Fatal("executable markup accepted")
	}

	mismatch := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{map[string]any{"type": "text", "id": "token", "label": "Token", "binding": "/token"}},
		"actions":    []any{map[string]any{"type": "submit", "id": "save", "label": "Save"}},
	}
	mismatchData, err := json.Marshal(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(mismatchData, schema, nil); err == nil || !strings.Contains(err.Error(), "writeOnly") {
		t.Fatalf("writeOnly mismatch error = %v", err)
	}
}

func TestValidatorRuntimeConfigExactNumericConstraints(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number","minimum":-1.25,"maximum":2.5,"multipleOf":0.125}},"required":["value"],"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"-1.25", "2.5", "0.375"} {
		if err := ValidateConfig(schema, json.RawMessage(`{"value":`+value+`}`)); err != nil {
			t.Fatalf("ValidateConfig(%s) = %v", value, err)
		}
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":-1.251}`)); err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("below minimum error = %v", err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":0.38}`)); err == nil || !strings.Contains(err.Error(), "not an exact multipleOf value") {
		t.Fatalf("multipleOf error = %v", err)
	}
	zeroSchema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number","multipleOf":0}},"required":["value"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(zeroSchema, json.RawMessage(`{"value":1}`)); err == nil || !strings.Contains(err.Error(), "multipleOf must be") {
		t.Fatalf("non-positive multipleOf error = %v", err)
	}
}

func TestValidatePackageRejectsMissingManifest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("not a plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err == nil {
		t.Fatal("missing package manifest accepted")
	}
	official := NewValidator(ValidatorOptions{})
	exact := official.WithTrustedSigners(nil, TrustedSignerPolicyExact)
	if exact == official {
		t.Fatal("WithTrustedSigners returned the same validator")
	}
}
