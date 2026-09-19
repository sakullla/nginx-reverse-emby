//go:build !integration

package plugins

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestConfigLocalDefinitionsAndExclusiveAlternatives(t *testing.T) {
	t.Parallel()
	const raw = `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.test/config","type":"object","additionalProperties":false,"required":["selector"],"properties":{"selector":{"$ref":"#/$defs/selector"}},"$defs":{"selector":{"oneOf":[{"type":"object","additionalProperties":false,"required":["kind","value"],"properties":{"kind":{"const":"ip"},"value":{"type":"string","maxLength":64}}},{"type":"object","additionalProperties":false,"required":["kind","id"],"properties":{"kind":{"const":"classification"},"id":{"$ref":"#/$defs/id"}}}]},"id":{"type":"string","pattern":"^[a-z]+$"}}}`
	root := newSignedWASMPackage(t, "")
	writeOwnerFile(t, root, ConfigSchemaFile, raw)
	refreshOwnerPackage(t, root)
	validated, err := newOwnerValidator().ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []string{`{"selector":{"kind":"ip","value":"192.0.2.1"}}`, `{"selector":{"kind":"classification","id":"cn"}}`} {
		if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(valid)); err != nil {
			t.Fatalf("valid config %s: %v", valid, err)
		}
	}
	for _, invalid := range []string{`{}`, `{"selector":{"kind":"other","value":"x"}}`, `{"selector":{"kind":"classification","id":"!"}}`, `{"selector":{"kind":"ip","value":"x","id":"cn"}}`} {
		if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(invalid)); err == nil {
			t.Fatalf("accepted invalid config %s", invalid)
		}
	}
}

func TestConfigLocalReferencesHaveBoundedExpansion(t *testing.T) {
	t.Parallel()
	defs := map[string]any{"leaf": map[string]any{"type": "string"}}
	previous := "leaf"
	for index := range 13 {
		name := fmt.Sprintf("level%d", index)
		defs[name] = map[string]any{"type": "object", "properties": map[string]any{
			"left":  map[string]any{"$ref": "#/$defs/" + previous},
			"right": map[string]any{"$ref": "#/$defs/" + previous},
		}}
		previous = name
	}
	schema := map[string]any{"type": "object", "$defs": defs, "properties": map[string]any{"tree": map[string]any{"$ref": "#/$defs/" + previous}}}
	if err := validateJSONSchema(schema); err == nil || !strings.Contains(err.Error(), "expansion budget") {
		t.Fatalf("unbounded reference expansion: %v", err)
	}
}

func TestConfigOneOfRequiredMatchesExactlyOneBranch(t *testing.T) {
	t.Parallel()
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"attribute":{"type":"object","properties":{"boolean":{"type":"boolean"},"integer":{"type":"integer"}},"oneOf":[{"required":["boolean"]},{"required":["integer"]}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{"attribute":{"boolean":false}}`, `{"attribute":{"integer":0}}`} {
		if err := ValidateConfig(schema, json.RawMessage(value)); err != nil {
			t.Fatalf("valid attribute %s: %v", value, err)
		}
	}
	for _, value := range []string{`{"attribute":{}}`, `{"attribute":{"boolean":true,"integer":1}}`, `{"attribute":{"boolean":1}}`} {
		if err := ValidateConfig(schema, json.RawMessage(value)); err == nil {
			t.Fatalf("accepted invalid attribute %s", value)
		}
	}
}

func TestConfigCompositionRejectsUnsafeOrUnresolvedSchemas(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"type":"object","properties":{"x":{"$ref":"https://example.test/schema"}}}`,
		`{"type":"object","properties":{"x":{"$ref":"#/$defs/missing"}}}`,
		`{"type":"object","properties":{"x":{"$ref":"#/$defs/x"}},"$defs":{"x":{"$ref":"#/$defs/x"}}}`,
		`{"type":"object","properties":{"x":{"$ref":"#/$defs/x","maxLength":1}},"$defs":{"x":{"type":"string"}}}`,
		`{"type":"object","oneOf":[]}`,
		`{"type":"object","oneOf":[true]}`,
		`{"type":"object","oneOf":[{"type":"object","properties":{"token":{"type":"string","writeOnly":true}}}]}`,
		`{"type":"object","oneOf":[{"type":"object","properties":{"id":{"type":"string","hostInjected":true}}}]}`,
	} {
		schema, err := DecodeConfigSchema([]byte(raw))
		if err == nil {
			err = validateJSONSchema(schema)
		}
		if err == nil {
			t.Fatalf("accepted unsafe schema %s", raw)
		}
	}
}

func TestConfigLocalReferencePreservesReadOnlyProjection(t *testing.T) {
	t.Parallel()
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"id":{"$ref":"#/$defs/id"}},"$defs":{"id":{"type":"string","readOnly":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigWritableInput(schema, json.RawMessage(`{"id":"override"}`)); err == nil {
		t.Fatal("referenced readOnly property accepted client input")
	}
	properties := schema["properties"].(map[string]any)
	if properties["id"].(map[string]any)["readOnly"] != true {
		t.Fatal("read projection lost referenced annotation")
	}
}
