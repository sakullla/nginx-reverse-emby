package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// PluginSecretFieldState is the only read projection for schema writeOnly
// values. The value itself never crosses the control-plane API boundary.
type PluginSecretFieldState struct {
	Pointer string `json:"pointer"`
	Present bool   `json:"present"`
}

func pluginPrepareBrokeredConfig(schema map[string]any, requested json.RawMessage, currentHandles []storage.PluginInstanceSecretHandle, replacements map[string]json.RawMessage) (json.RawMessage, map[string]json.RawMessage, []storage.PluginInstanceSecretHandle, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(requested)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: plugin config must be one JSON value", ErrInvalidArgument)
	}
	public, submitted, err := pluginStripWriteOnly(schema, value, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if len(submitted) > 0 {
		return nil, nil, nil, fmt.Errorf("%w: writeOnly config values must use secret_replacements", ErrInvalidArgument)
	}
	handles := make(map[string]storage.PluginInstanceSecretHandle, len(currentHandles))
	for _, handle := range currentHandles {
		if handle.Pointer == "" || handle.ID == "" {
			return nil, nil, nil, ErrPluginReadProjection
		}
		handles[handle.Pointer] = handle
	}
	prepared := make(map[string]json.RawMessage)
	for pointer, raw := range replacements {
		if !pluginPointerIsWriteOnly(schema, public, pointer) {
			return nil, nil, nil, fmt.Errorf("%w: secret replacement %q is not a concrete schema writeOnly field", ErrInvalidArgument, pointer)
		}
		if strings.TrimSpace(string(raw)) == "null" {
			delete(handles, pointer)
			continue
		}
		var secret any
		secretDecoder := json.NewDecoder(strings.NewReader(string(raw)))
		secretDecoder.UseNumber()
		if err := secretDecoder.Decode(&secret); err != nil || secret == nil {
			return nil, nil, nil, fmt.Errorf("%w: secret replacement %q is invalid", ErrInvalidArgument, pointer)
		}
		canonical, err := json.Marshal(secret)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: secret replacement %q is invalid", ErrInvalidArgument, pointer)
		}
		prepared[pointer] = canonical
		delete(handles, pointer)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: plugin config is invalid", ErrInvalidArgument)
	}
	retained := make([]storage.PluginInstanceSecretHandle, 0, len(handles))
	for _, handle := range handles {
		retained = append(retained, handle)
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].Pointer < retained[j].Pointer })
	return encoded, prepared, retained, nil
}

func pluginStripWriteOnly(schema map[string]any, value any, pointer string) (any, map[string]json.RawMessage, error) {
	if writeOnly, _ := schema["writeOnly"].(bool); writeOnly {
		if value == nil {
			return nil, nil, nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]json.RawMessage{pointer: encoded}, nil
	}
	found := map[string]json.RawMessage{}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			childSchema, _ := properties[key].(map[string]any)
			if childSchema == nil {
				out[key] = child
				continue
			}
			childPointer := pointer + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			clean, secrets, err := pluginStripWriteOnly(childSchema, child, childPointer)
			if err != nil {
				return nil, nil, err
			}
			out[key] = clean
			for path, raw := range secrets {
				found[path] = raw
			}
		}
		return out, found, nil
	case []any:
		itemSchema, _ := schema["items"].(map[string]any)
		out := make([]any, len(typed))
		for index, child := range typed {
			if itemSchema == nil {
				out[index] = child
				continue
			}
			clean, secrets, err := pluginStripWriteOnly(itemSchema, child, pointer+"/"+strconv.Itoa(index))
			if err != nil {
				return nil, nil, err
			}
			out[index] = clean
			for path, raw := range secrets {
				found[path] = raw
			}
		}
		return out, found, nil
	default:
		return value, found, nil
	}
}

func pluginPointerIsWriteOnly(schema map[string]any, public any, pointer string) bool {
	if pointer == "" || pointer[0] != '/' {
		return false
	}
	node := schema
	value := public
	for _, escaped := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		switch typed := value.(type) {
		case map[string]any:
			properties, _ := node["properties"].(map[string]any)
			node, _ = properties[token].(map[string]any)
			value = typed[token]
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			node, _ = node["items"].(map[string]any)
			value = typed[index]
		default:
			return false
		}
		if node == nil {
			return false
		}
	}
	writeOnly, _ := node["writeOnly"].(bool)
	return writeOnly
}

func pluginSetJSONPointer(value any, pointer string, replacement any) error {
	if pointer == "" || pointer[0] != '/' {
		return ErrInvalidArgument
	}
	tokens := strings.Split(pointer[1:], "/")
	var current any = value
	for index, escaped := range tokens {
		token := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		last := index == len(tokens)-1
		switch typed := current.(type) {
		case map[string]any:
			if last {
				typed[token] = replacement
				return nil
			}
			current = typed[token]
		case []any:
			item, err := strconv.Atoi(token)
			if err != nil || item < 0 || item >= len(typed) {
				return ErrInvalidArgument
			}
			if last {
				typed[item] = replacement
				return nil
			}
			current = typed[item]
		default:
			return ErrInvalidArgument
		}
	}
	return ErrInvalidArgument
}

func pluginConfigValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func pluginSecretFieldStates(raw string, base []PluginSecretFieldState) ([]PluginSecretFieldState, error) {
	var handles []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultJSONArray(raw)), &handles); err != nil {
		return nil, ErrPluginReadProjection
	}
	states := make(map[string]bool, len(base)+len(handles))
	for _, state := range base {
		states[state.Pointer] = false
	}
	for _, handle := range handles {
		if handle.Pointer == "" || handle.ID == "" {
			return nil, ErrPluginReadProjection
		}
		states[handle.Pointer] = true
	}
	result := make([]PluginSecretFieldState, 0, len(states))
	for pointer, present := range states {
		result = append(result, PluginSecretFieldState{Pointer: pointer, Present: present})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Pointer < result[j].Pointer })
	return result, nil
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
