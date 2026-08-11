package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PluginSecretFieldState is the only read projection for schema writeOnly
// values. The value itself never crosses the control-plane API boundary.
type PluginSecretFieldState struct {
	Pointer string `json:"pointer"`
	Present bool   `json:"present"`
}

func pluginRedactedConfig(schema map[string]any, raw string) (json.RawMessage, []PluginSecretFieldState, error) {
	object, err := pluginConfigObject(raw)
	if err != nil {
		return nil, nil, err
	}
	paths := pluginWriteOnlyPaths(schema)
	states := make([]PluginSecretFieldState, 0, len(paths))
	for _, path := range paths {
		_, present := pluginObjectPath(object, path)
		states = append(states, PluginSecretFieldState{Pointer: pluginJSONPointer(path), Present: present})
		pluginDeleteObjectPath(object, path)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, nil, ErrPluginReadProjection
	}
	return encoded, states, nil
}

func pluginPublicConfigSchema(schema map[string]any) map[string]any {
	encoded, _ := json.Marshal(schema)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	var walk func(map[string]any)
	walk = func(node map[string]any) {
		if writeOnly, _ := node["writeOnly"].(bool); writeOnly {
			delete(node, "default")
			delete(node, "enum")
		}
		if properties, _ := node["properties"].(map[string]any); properties != nil {
			for _, child := range properties {
				if childNode, _ := child.(map[string]any); childNode != nil {
					walk(childNode)
				}
			}
		}
		if items, _ := node["items"].(map[string]any); items != nil {
			walk(items)
		}
	}
	walk(result)
	return result
}

func pluginMergeWriteOnlyConfig(schema map[string]any, requested json.RawMessage, current string, replacements map[string]json.RawMessage) (json.RawMessage, error) {
	requestedObject, err := pluginConfigObject(string(requested))
	if err != nil {
		return nil, fmt.Errorf("%w: plugin config must be a JSON object", ErrInvalidArgument)
	}
	currentObject := map[string]any{}
	if strings.TrimSpace(current) != "" {
		currentObject, err = pluginConfigObject(current)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
	}
	paths := pluginWriteOnlyPaths(schema)
	allowed := make(map[string][]string, len(paths))
	for _, path := range paths {
		pointer := pluginJSONPointer(path)
		allowed[pointer] = path
		if _, present := pluginObjectPath(requestedObject, path); present {
			return nil, fmt.Errorf("%w: writeOnly config field %s must use secret_replacements", ErrInvalidArgument, pointer)
		}
		if value, present := pluginObjectPath(currentObject, path); present {
			pluginSetObjectPath(requestedObject, path, value)
		}
	}
	for pointer, raw := range replacements {
		path, ok := allowed[pointer]
		if !ok {
			return nil, fmt.Errorf("%w: secret replacement %q is not a schema writeOnly field", ErrInvalidArgument, pointer)
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: secret replacement %q is invalid JSON", ErrInvalidArgument, pointer)
		}
		pluginSetObjectPath(requestedObject, path, value)
	}
	encoded, err := json.Marshal(requestedObject)
	if err != nil {
		return nil, fmt.Errorf("%w: plugin config is invalid", ErrInvalidArgument)
	}
	return encoded, nil
}

func pluginConfigObject(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("empty plugin config")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrPluginReadProjection
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, ErrPluginReadProjection
	}
	return object, nil
}

func pluginWriteOnlyPaths(schema map[string]any) [][]string {
	var result [][]string
	var walk func(map[string]any, []string)
	walk = func(node map[string]any, path []string) {
		if writeOnly, _ := node["writeOnly"].(bool); writeOnly && len(path) > 0 {
			result = append(result, append([]string(nil), path...))
			return
		}
		properties, _ := node["properties"].(map[string]any)
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, _ := properties[key].(map[string]any)
			if child != nil {
				walk(child, append(path, key))
			}
		}
	}
	walk(schema, nil)
	return result
}

func pluginJSONPointer(path []string) string {
	parts := make([]string, len(path))
	for index, part := range path {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

func pluginObjectPath(object map[string]any, path []string) (any, bool) {
	var current any = object
	for _, token := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = next[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func pluginSetObjectPath(object map[string]any, path []string, value any) {
	current := object
	for _, token := range path[:len(path)-1] {
		next, ok := current[token].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[token] = next
		}
		current = next
	}
	current[path[len(path)-1]] = value
}

func pluginDeleteObjectPath(object map[string]any, path []string) {
	current := object
	for _, token := range path[:len(path)-1] {
		next, ok := current[token].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, path[len(path)-1])
}
