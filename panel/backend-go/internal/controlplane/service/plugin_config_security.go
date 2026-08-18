package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// pluginHostInjectedSource is the existing host identity written into schema
// hostInjected properties. Callers must not invent a second identity set.
type pluginHostInjectedSource struct {
	Generation        string
	ResourceGroupRef  string
	SecretRef         string
	SecretRefs        []string
	Handles           []storage.PluginInstanceSecretHandle
	ResolveGeneration func(public any) (string, error)
}

// PluginSecretFieldState is the only read projection for schema writeOnly
// values. The value itself never crosses the control-plane API boundary.
type PluginSecretFieldState struct {
	Pointer string `json:"pointer"`
	Present bool   `json:"present"`
}

func pluginPrepareBrokeredConfig(schema map[string]any, currentConfig, requested json.RawMessage, currentHandles []storage.PluginInstanceSecretHandle, replacements map[string]json.RawMessage, host ...pluginHostInjectedSource) (json.RawMessage, map[string]json.RawMessage, []storage.PluginInstanceSecretHandle, error) {
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
	current, err := pluginConfigValue(currentConfig)
	if err != nil {
		return nil, nil, nil, ErrPluginReadProjection
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
		_, clearingExisting := handles[pointer]
		isNull := strings.TrimSpace(string(raw)) == "null"
		if !pluginPointerIsWriteOnly(schema, public, pointer) && !(isNull && clearingExisting && pluginPointerIsWriteOnly(schema, current, pointer)) {
			return nil, nil, nil, fmt.Errorf("%w: secret replacement %q is not a concrete schema writeOnly field", ErrInvalidArgument, pointer)
		}
		if isNull {
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
	for pointer := range handles {
		if _, explicit := replacements[pointer]; explicit {
			continue
		}
		if !pluginPointerIsWriteOnly(schema, public, pointer) {
			return nil, nil, nil, fmt.Errorf("%w: retained secret pointer %q no longer exists; explicitly clear it", ErrInvalidArgument, pointer)
		}
		if !pluginArraySecretPointerStable(current, public, pointer) {
			return nil, nil, nil, fmt.Errorf("%w: array structure for retained secret %q changed; explicitly clear or replace it", ErrInvalidArgument, pointer)
		}
	}
	source := pluginHostSource(currentHandles, host...)
	public, err = pluginApplyMissingHostInjected(schema, public, current, "", source)
	if err != nil {
		return nil, nil, nil, err
	}
	if source.Generation == "" && source.ResolveGeneration != nil {
		source.Generation, err = source.ResolveGeneration(public)
		if err != nil {
			return nil, nil, nil, err
		}
		source.ResolveGeneration = nil
		public, err = pluginApplyMissingHostInjected(schema, public, current, "", source)
		if err != nil {
			return nil, nil, nil, err
		}
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

func pluginNamedPropertyHostInjected(schema map[string]any, name string) bool {
	properties, _ := schema["properties"].(map[string]any)
	child, _ := properties[name].(map[string]any)
	if child == nil {
		return false
	}
	injected, err := pluginsdk.ConfigSchemaHostInjected(child)
	return err == nil && injected
}

func pluginConfigObjectValue(raw json.RawMessage) map[string]any {
	value, err := pluginConfigValue(raw)
	if err != nil {
		return map[string]any{}
	}
	object, _ := value.(map[string]any)
	if object == nil {
		return map[string]any{}
	}
	return object
}

func pluginConfigString(object map[string]any, key string) string {
	text, _ := object[key].(string)
	return strings.TrimSpace(text)
}

func pluginRawObjectHasKey(raw json.RawMessage, key string) bool {
	object := pluginConfigObjectValue(raw)
	_, exists := object[key]
	return exists
}

func pluginHostSource(handles []storage.PluginInstanceSecretHandle, host ...pluginHostInjectedSource) pluginHostInjectedSource {
	source := pluginHostInjectedSource{Handles: handles}
	if len(host) == 0 {
		return source
	}
	source.Generation = host[0].Generation
	source.ResourceGroupRef = host[0].ResourceGroupRef
	source.SecretRef = host[0].SecretRef
	source.SecretRefs = host[0].SecretRefs
	source.ResolveGeneration = host[0].ResolveGeneration
	if len(host[0].Handles) > 0 {
		source.Handles = host[0].Handles
	}
	return source
}

// pluginApplyMissingHostInjected writes host values only for named properties
// marked hostInjected that the submitted document omitted. Unmarked keys are
// never filled, including well-known names, and present user keys stay as-is.
func pluginApplyMissingHostInjected(schema map[string]any, requested, current any, pointer string, host pluginHostInjectedSource) (any, error) {
	if requested == nil {
		if _, ok := schema["properties"].(map[string]any); ok {
			requested = map[string]any{}
		} else {
			return requested, nil
		}
	}
	switch typed := requested.(type) {
	case map[string]any:
		currentObject, _ := current.(map[string]any)
		if currentObject == nil {
			currentObject = map[string]any{}
		}
		properties, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childSchema, _ := properties[key].(map[string]any)
			if childSchema == nil {
				continue
			}
			childPointer := pointer + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			if child, exists := typed[key]; exists {
				injected, err := pluginApplyMissingHostInjected(childSchema, child, currentObject[key], childPointer, host)
				if err != nil {
					return nil, err
				}
				typed[key] = injected
				continue
			}
			hostInjected, err := pluginsdk.ConfigSchemaHostInjected(childSchema)
			if err != nil {
				return nil, err
			}
			if !hostInjected {
				continue
			}
			value, ok := pluginResolveHostInjectedValue(key, childPointer, currentObject[key], typed, host)
			if ok {
				typed[key] = value
			}
		}
		return typed, nil
	case []any:
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema == nil {
			return typed, nil
		}
		currentItems, _ := current.([]any)
		for index, child := range typed {
			currentChild := pluginHostArrayItemCurrent(currentItems, child)
			injected, err := pluginApplyMissingHostInjected(itemSchema, child, currentChild, pointer+"/"+strconv.Itoa(index), host)
			if err != nil {
				return nil, err
			}
			typed[index] = injected
		}
		return typed, nil
	default:
		return requested, nil
	}
}

func pluginHostArrayItemCurrent(currentItems []any, requested any) any {
	requestedObject, ok := requested.(map[string]any)
	if !ok {
		return nil
	}
	var fallback any
	for _, item := range currentItems {
		currentObject, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if pluginArrayItemIdentityEqual(currentObject, requestedObject) {
			return currentObject
		}
		if pluginHostItemUserIdentityEqual(currentObject, requestedObject) {
			fallback = currentObject
		}
	}
	return fallback
}

func pluginHostItemUserIdentityEqual(current, requested map[string]any) bool {
	matched := false
	for _, key := range []string{"image", "rule_ref", "key", "name"} {
		currentValue, currentFound := current[key]
		requestedValue, requestedFound := requested[key]
		if !currentFound && !requestedFound {
			continue
		}
		if !currentFound || !requestedFound || !reflect.DeepEqual(currentValue, requestedValue) {
			return false
		}
		matched = true
	}
	return matched
}

func pluginResolveHostInjectedValue(name, pointer string, current any, parent map[string]any, host pluginHostInjectedSource) (any, bool) {
	if text, ok := current.(string); ok && text == "" {
		current = nil
	}
	if current != nil {
		return current, true
	}
	switch name {
	case "generation":
		if host.Generation != "" {
			return host.Generation, true
		}
	case "resource_group_ref":
		if host.ResourceGroupRef != "" {
			return host.ResourceGroupRef, true
		}
	case "secret_ref":
		if host.SecretRef != "" {
			return host.SecretRef, true
		}
		if id := pluginHandleID(host.Handles, pointer); id != "" {
			return id, true
		}
	case "secret_refs":
		if host.SecretRefs != nil {
			refs := make([]any, len(host.SecretRefs))
			for index, ref := range host.SecretRefs {
				refs[index] = ref
			}
			return refs, true
		}
	case "id":
		return pluginStableHostItemID(parent), true
	}
	return nil, false
}

func pluginHandleID(handles []storage.PluginInstanceSecretHandle, pointer string) string {
	for _, handle := range handles {
		if handle.Pointer == pointer && handle.ID != "" {
			return handle.ID
		}
	}
	return ""
}

func pluginStableHostItemID(item map[string]any) string {
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		encoded, err := json.Marshal(item[key])
		if err != nil {
			continue
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.Write(encoded)
		builder.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return "h" + hex.EncodeToString(sum[:8])
}

func pluginArraySecretPointerStable(current, requested any, pointer string) bool {
	if pointer == "" || pointer[0] != '/' {
		return false
	}
	left, right := current, requested
	for _, escaped := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		switch currentValue := left.(type) {
		case map[string]any:
			requestedValue, ok := right.(map[string]any)
			if !ok {
				return false
			}
			left, right = currentValue[token], requestedValue[token]
		case []any:
			requestedValue, ok := right.([]any)
			index, indexErr := strconv.Atoi(token)
			if !ok || indexErr != nil || index < 0 || index >= len(currentValue) || index >= len(requestedValue) || len(currentValue) != len(requestedValue) {
				return false
			}
			if !pluginArrayItemIdentityEqual(currentValue[index], requestedValue[index]) {
				return false
			}
			left, right = currentValue[index], requestedValue[index]
		default:
			return false
		}
	}
	return true
}

func pluginArrayItemIdentityEqual(current, requested any) bool {
	currentObject, currentOK := current.(map[string]any)
	requestedObject, requestedOK := requested.(map[string]any)
	if currentOK && requestedOK {
		for _, key := range []string{"id", "key", "name"} {
			currentIdentity, currentFound := currentObject[key]
			requestedIdentity, requestedFound := requestedObject[key]
			if currentFound || requestedFound {
				return currentFound && requestedFound && reflect.DeepEqual(currentIdentity, requestedIdentity)
			}
		}
	}
	return reflect.DeepEqual(current, requested)
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

func pluginSecretLogValues(config json.RawMessage, handles []storage.PluginInstanceSecretHandle) ([]string, error) {
	value, err := pluginConfigValue(config)
	if err != nil {
		return nil, ErrPluginReadProjection
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(handles)*2)
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		if _, exists := seen[candidate]; exists {
			return
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	for _, handle := range handles {
		secret, ok := pluginJSONPointerValue(value, handle.Pointer)
		if !ok {
			return nil, ErrPluginReadProjection
		}
		encoded, err := json.Marshal(secret)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		add(string(encoded))
		var collectStrings func(any)
		collectStrings = func(current any) {
			switch typed := current.(type) {
			case string:
				add(typed)
			case []any:
				for _, item := range typed {
					collectStrings(item)
				}
			case map[string]any:
				for _, item := range typed {
					collectStrings(item)
				}
			}
		}
		collectStrings(secret)
	}
	sort.Strings(result)
	return result, nil
}

func pluginJSONPointerValue(value any, pointer string) (any, bool) {
	if pointer == "" || pointer[0] != '/' {
		return nil, false
	}
	current := value
	for _, escaped := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[token]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
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
