//go:build !integration

package plugins

import (
	"encoding/json"
	"testing"
)

func TestDeclarativeUIProjectionRejectsExecutableMarkup(t *testing.T) {
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
}
