package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidatorAcceptsSingleTypedDeclarativeUISchema(t *testing.T) {
	data := validDeclarativeUIJSON(t)
	configSchema := validDeclarativeUIConfigSchema(t)
	if err := validateDeclarativeUI(data, configSchema); err != nil {
		t.Fatalf("valid typed UI schema rejected: %v", err)
	}
	metadataText := mutateDeclarativeUI(func(document map[string]any) { document["description"] = "Metadata: host-rendered values only" })(t)
	if err := validateDeclarativeUI(metadataText, configSchema); err != nil {
		t.Fatalf("ordinary colon-delimited text rejected as a URI: %v", err)
	}

	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"ui_schema: "+UISchemaFile+"\n")
	writeFixtureBytes(t, root, ConfigSchemaFile, mustMarshalJSON(t, configSchema))
	writeFixtureBytes(t, root, UISchemaFile, data)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("signed package with current UI schema rejected: %v", err)
	}
	if validated.Manifest.UISchema != UISchemaFile {
		t.Fatalf("validated UI schema path = %q", validated.Manifest.UISchema)
	}
}

func TestValidatorDeclarativeUICrossChecksConfigSchema(t *testing.T) {
	tests := []struct {
		name         string
		mutateUI     func(map[string]any)
		mutateSchema func(map[string]any)
		marker       string
	}{
		{name: "missing property", mutateUI: func(document map[string]any) { firstUIInput(document)["binding"] = "/missing" }, marker: "does not resolve"},
		{name: "component type mismatch", mutateSchema: func(schema map[string]any) { configProperty(schema, "name")["type"] = "boolean" }, marker: "requires a string schema"},
		{name: "required mismatch", mutateSchema: func(schema map[string]any) { schema["required"] = []any{} }, marker: "required=true contradicts"},
		{name: "range mismatch", mutateSchema: func(schema map[string]any) { configProperty(schema, "threshold")["maximum"] = json.Number("99") }, marker: "maximum contradicts"},
		{name: "enum mismatch", mutateSchema: func(schema map[string]any) { configProperty(schema, "mode")["enum"] = []any{"observe", "audit"} }, marker: "option \"block\" is absent"},
		{name: "UI read-only without schema metadata", mutateSchema: func(schema map[string]any) { delete(configProperty(schema, "status"), "readOnly") }, marker: "read_only=true contradicts"},
		{name: "schema read-only rendered editable", mutateUI: func(document map[string]any) { delete(componentByID(document, "status"), "read_only") }, marker: "read_only=false contradicts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			uiMutator := test.mutateUI
			if uiMutator == nil {
				uiMutator = func(map[string]any) {}
			}
			uiData := mutateDeclarativeUI(uiMutator)(t)
			schema := validDeclarativeUIConfigSchema(t)
			if test.mutateSchema != nil {
				test.mutateSchema(schema)
			}
			root := newPackageFixture(t)
			writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"ui_schema: "+UISchemaFile+"\n")
			writeFixtureBytes(t, root, ConfigSchemaFile, mustMarshalJSON(t, schema))
			writeFixtureBytes(t, root, UISchemaFile, uiData)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "ui_schema")
			if !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("cross-schema error = %v, want marker %q", err, test.marker)
			}
		})
	}
}

func TestValidatorRejectsSignedSecretConfigContracts(t *testing.T) {
	tests := []struct {
		name         string
		mutateUI     func(map[string]any)
		mutateSchema func(map[string]any)
		code         string
		marker       string
	}{
		{
			name:         "writeOnly config property",
			mutateSchema: func(schema map[string]any) { configProperty(schema, "endpoint")["writeOnly"] = true },
			code:         "config_schema",
			marker:       "brokered secret storage",
		},
		{
			name:     "secret UI component binding",
			mutateUI: func(document map[string]any) { componentByID(document, "endpoint")["type"] = UIComponentSecret },
			code:     "ui_schema",
			marker:   "brokered secret storage",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := validDeclarativeUIConfigSchema(t)
			if test.mutateSchema != nil {
				test.mutateSchema(schema)
			}
			uiData := validDeclarativeUIJSON(t)
			if test.mutateUI != nil {
				uiData = mutateDeclarativeUI(test.mutateUI)(t)
			}
			root := newPackageFixture(t)
			writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"ui_schema: "+UISchemaFile+"\n")
			writeFixtureBytes(t, root, ConfigSchemaFile, mustMarshalJSON(t, schema))
			writeFixtureBytes(t, root, UISchemaFile, uiData)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, test.code)
			if !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("secret contract error = %v, want marker %q", err, test.marker)
			}
		})
	}
}

func TestValidatorDeclarativeUIRejectsUnsupportedAndExecutableConstructs(t *testing.T) {
	tests := []struct {
		name   string
		data   func(*testing.T) []byte
		marker string
	}{
		{name: "primitive root", data: func(*testing.T) []byte { return []byte(`true`) }, marker: "typed declarative contract"},
		{name: "parallel schema version", data: mutateDeclarativeUI(func(document map[string]any) { document["schema_version"] = 2 }), marker: "schema_version"},
		{name: "unknown root field", data: mutateDeclarativeUI(func(document map[string]any) { document["renderer"] = "remote" }), marker: "unknown field"},
		{name: "duplicate JSON field", data: func(*testing.T) []byte { return []byte(`{"schema_version":1,"schema_version":1}`) }, marker: "duplicate object field"},
		{name: "unknown component property", data: mutateDeclarativeUI(func(document map[string]any) { firstUIComponent(document)["href"] = "https://example.test" }), marker: "unknown field"},
		{name: "noncanonical field case", data: mutateDeclarativeUI(func(document map[string]any) { firstUIInput(document)["Label"] = "Alias" }), marker: `unknown field "Label"`},
		{name: "unsupported component", data: mutateDeclarativeUI(func(document map[string]any) { firstUIComponent(document)["type"] = "iframe" }), marker: "unsupported host component"},
		{name: "primitive component", data: mutateDeclarativeUI(func(document map[string]any) { document["components"] = []any{"text"} }), marker: "component must be an object"},
		{name: "unknown action property", data: mutateDeclarativeUI(func(document map[string]any) { firstUIAction(document)["endpoint"] = "/admin" }), marker: "unknown field"},
		{name: "unsupported action", data: mutateDeclarativeUI(func(document map[string]any) { firstUIAction(document)["type"] = "navigate" }), marker: "unsupported host action"},
		{name: "missing submit action", data: mutateDeclarativeUI(func(document map[string]any) {
			document["actions"] = []any{map[string]any{"type": "reset", "id": "reset", "label": "Reset"}}
		}), marker: "submit action"},
		{name: "HTML markup", data: mutateDeclarativeUI(func(document map[string]any) { firstUIComponent(document)["label"] = "<img src=x>" }), marker: "markup"},
		{name: "template binding", data: mutateDeclarativeUI(func(document map[string]any) { document["title"] = "${window.location}" }), marker: "template"},
		{name: "dangerous URI", data: mutateDeclarativeUI(func(document map[string]any) { document["description"] = "javascript:alert(1)" }), marker: "URI"},
		{name: "prototype binding", data: mutateDeclarativeUI(func(document map[string]any) { firstUIInput(document)["binding"] = "/__proto__/polluted" }), marker: "unsafe"},
		{name: "traversal binding", data: mutateDeclarativeUI(func(document map[string]any) { firstUIInput(document)["binding"] = "/settings/../token" }), marker: "unsafe"},
		{name: "duplicate binding", data: mutateDeclarativeUI(func(document map[string]any) {
			section := firstUIComponent(document)
			children := section["children"].([]any)
			children[1].(map[string]any)["binding"] = children[0].(map[string]any)["binding"]
		}), marker: "duplicate UI binding"},
		{name: "duplicate id", data: mutateDeclarativeUI(func(document map[string]any) { firstUIAction(document)["id"] = firstUIComponent(document)["id"] }), marker: "duplicate UI id"},
		{name: "invalid number range", data: mutateDeclarativeUI(func(document map[string]any) {
			componentByType(document, UIComponentNumber)["minimum"] = float64(20)
			componentByType(document, UIComponentNumber)["maximum"] = float64(10)
		}), marker: "minimum exceeds maximum"},
		{name: "duplicate select option", data: mutateDeclarativeUI(func(document map[string]any) {
			options := componentByType(document, UIComponentSelect)["options"].([]any)
			options[1].(map[string]any)["value"] = options[0].(map[string]any)["value"]
		}), marker: "duplicates value"},
		{name: "unknown select option property", data: mutateDeclarativeUI(func(document map[string]any) {
			options := componentByType(document, UIComponentSelect)["options"].([]any)
			options[0].(map[string]any)["html"] = "bold"
		}), marker: "unknown field"},
		{name: "unsupported notice tone", data: mutateDeclarativeUI(func(document map[string]any) { componentByType(document, UIComponentNotice)["tone"] = "html" }), marker: "notice tone"},
		{name: "multiple documents", data: func(t *testing.T) []byte { return append(validDeclarativeUIJSON(t), []byte(` {}`)...) }, marker: "multiple JSON values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDeclarativeUI(test.data(t), validDeclarativeUIConfigSchema(t))
			if err == nil || !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("validation error = %v, want marker %q", err, test.marker)
			}
		})
	}
}

func TestValidatorDeclarativeUIEnforcesDepthCollectionAndByteBudgets(t *testing.T) {
	t.Run("document bytes", func(t *testing.T) {
		err := validateDeclarativeUI(bytes.Repeat([]byte{' '}, maxDeclarativeUIBytes+1), validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "document budget") {
			t.Fatalf("document budget error = %v", err)
		}
	})
	t.Run("component depth", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			var component any = map[string]any{"type": UIComponentNotice, "id": "leaf", "label": "Leaf", "tone": "info"}
			for depth := 0; depth < maxDeclarativeUIDepth; depth++ {
				component = map[string]any{"type": UIComponentSection, "id": fmt.Sprintf("level_%d", depth), "label": "Level", "children": []any{component}}
			}
			document["components"] = []any{component}
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "depth exceeds") {
			t.Fatalf("depth budget error = %v", err)
		}
	})
	t.Run("root collection", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			components := make([]any, maxDeclarativeUIChildren+1)
			for index := range components {
				components[index] = noticeComponent(index)
			}
			document["components"] = components
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "root components") {
			t.Fatalf("root collection error = %v", err)
		}
	})
	t.Run("total components", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			sections := make([]any, 5)
			for sectionIndex := range sections {
				children := make([]any, maxDeclarativeUIChildren)
				for childIndex := range children {
					children[childIndex] = map[string]any{"type": UIComponentNotice, "id": fmt.Sprintf("n_%d_%d", sectionIndex, childIndex), "label": "N", "tone": "info"}
				}
				sections[sectionIndex] = map[string]any{"type": UIComponentSection, "id": fmt.Sprintf("s_%d", sectionIndex), "label": "S", "children": children}
			}
			document["components"] = sections
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "component count") {
			t.Fatalf("component count error = %v", err)
		}
	})
	t.Run("select options", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			options := make([]any, maxDeclarativeUIOptions+1)
			for index := range options {
				options[index] = map[string]any{"value": fmt.Sprintf("v%d", index), "label": "Value"}
			}
			componentByType(document, UIComponentSelect)["options"] = options
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "select requires") {
			t.Fatalf("option collection error = %v", err)
		}
	})
	t.Run("total text", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			components := make([]any, 40)
			for index := range components {
				components[index] = map[string]any{"type": UIComponentNotice, "id": fmt.Sprintf("notice_%d", index), "label": "Notice", "description": strings.Repeat("x", 900), "tone": "info"}
			}
			document["components"] = components
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "UI text exceeds") {
			t.Fatalf("text budget error = %v", err)
		}
	})
	t.Run("actions", func(t *testing.T) {
		data := mutateDeclarativeUI(func(document map[string]any) {
			actions := make([]any, maxDeclarativeUIActions+1)
			for index := range actions {
				actions[index] = map[string]any{"type": UIActionReset, "id": fmt.Sprintf("reset_%d", index), "label": "Reset"}
			}
			document["actions"] = actions
		})(t)
		err := validateDeclarativeUI(data, validDeclarativeUIConfigSchema(t))
		if err == nil || !strings.Contains(err.Error(), "host actions") {
			t.Fatalf("action collection error = %v", err)
		}
	})
}

func validDeclarativeUIJSON(t *testing.T) []byte {
	t.Helper()
	document := map[string]any{
		"schema_version": DeclarativeUISchemaVersion,
		"title":          "WAF settings",
		"description":    "Host-rendered plugin configuration",
		"components": []any{
			map[string]any{"type": UIComponentSection, "id": "general", "label": "General", "children": []any{
				map[string]any{"type": UIComponentText, "id": "name", "label": "Name", "binding": "/name", "placeholder": "Primary policy", "required": true},
				map[string]any{"type": UIComponentTextarea, "id": "notes", "label": "Notes", "binding": "/notes"},
				map[string]any{"type": UIComponentText, "id": "endpoint", "label": "Endpoint", "binding": "/endpoint"},
				map[string]any{"type": UIComponentText, "id": "status", "label": "Status", "binding": "/status", "read_only": true},
				map[string]any{"type": UIComponentNumber, "id": "threshold", "label": "Threshold", "binding": "/threshold", "minimum": float64(1), "maximum": float64(100), "step": float64(1)},
				map[string]any{"type": UIComponentToggle, "id": "enabled", "label": "Enabled", "binding": "/enabled"},
				map[string]any{"type": UIComponentSelect, "id": "mode", "label": "Mode", "binding": "/mode", "options": []any{
					map[string]any{"value": "observe", "label": "Observe"},
					map[string]any{"value": "block", "label": "Block", "description": "Reject matching requests"},
				}},
			}},
			map[string]any{"type": UIComponentNotice, "id": "warning", "label": "Apply carefully", "description": "Changes affect new requests", "tone": "warning"},
		},
		"actions": []any{
			map[string]any{"type": UIActionSubmit, "id": "save", "label": "Save"},
			map[string]any{"type": UIActionReset, "id": "reset", "label": "Reset", "confirm": "Reset all fields?"},
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validDeclarativeUIConfigSchema(t *testing.T) map[string]any {
	t.Helper()
	data := []byte(`{
		"type":"object",
		"additionalProperties":false,
		"required":["name"],
		"properties":{
			"name":{"type":"string"},
			"notes":{"type":"string"},
			"endpoint":{"type":"string"},
			"status":{"type":"string","readOnly":true},
			"threshold":{"type":"number","minimum":1,"maximum":100,"multipleOf":1},
			"enabled":{"type":"boolean"},
			"mode":{"type":"string","enum":["observe","block"]}
		}
	}`)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	if err := validateJSONSchema(schema); err != nil {
		t.Fatalf("valid UI config schema rejected: %v", err)
	}
	return schema
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mutateDeclarativeUI(mutate func(map[string]any)) func(*testing.T) []byte {
	return func(t *testing.T) []byte {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(validDeclarativeUIJSON(t), &document); err != nil {
			t.Fatal(err)
		}
		mutate(document)
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
}

func firstUIComponent(document map[string]any) map[string]any {
	return document["components"].([]any)[0].(map[string]any)
}

func firstUIInput(document map[string]any) map[string]any {
	return firstUIComponent(document)["children"].([]any)[0].(map[string]any)
}

func firstUIAction(document map[string]any) map[string]any {
	return document["actions"].([]any)[0].(map[string]any)
}

func componentByType(document map[string]any, componentType string) map[string]any {
	var find func([]any) map[string]any
	find = func(components []any) map[string]any {
		for _, value := range components {
			component := value.(map[string]any)
			if component["type"] == componentType {
				return component
			}
			if children, ok := component["children"].([]any); ok {
				if found := find(children); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return find(document["components"].([]any))
}

func componentByID(document map[string]any, id string) map[string]any {
	var find func([]any) map[string]any
	find = func(components []any) map[string]any {
		for _, value := range components {
			component := value.(map[string]any)
			if component["id"] == id {
				return component
			}
			if children, ok := component["children"].([]any); ok {
				if found := find(children); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return find(document["components"].([]any))
}

func configProperty(schema map[string]any, name string) map[string]any {
	return schema["properties"].(map[string]any)[name].(map[string]any)
}

func noticeComponent(index int) map[string]any {
	return map[string]any{"type": UIComponentNotice, "id": fmt.Sprintf("notice_%d", index), "label": "Notice", "tone": "info"}
}
