package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func pluginStorageDirectoryBindings(grants []model.PluginGrantProjection, config json.RawMessage) ([]pluginprocess.DirectoryBinding, error) {
	bindings := make(map[string]pluginprocess.DirectoryBinding)
	for _, grant := range grants {
		if grant.Name != pluginsdk.PermissionStorageRead && grant.Name != pluginsdk.PermissionStorageWrite {
			continue
		}
		if grant.ResourceKind != pluginsdk.StorageResourceConfigPath {
			continue
		}
		values, found, err := resolvePluginConfigPaths(config, grant.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin storage binding %q: %w", grant.ResourceID, err)
		}
		if !found {
			continue
		}
		for _, value := range values {
			cleaned := filepath.Clean(value)
			if value != strings.TrimSpace(value) || value == "" || !filepath.IsAbs(cleaned) || isFilesystemRoot(cleaned) {
				return nil, fmt.Errorf("plugin storage binding %q must resolve to non-root absolute directories", grant.ResourceID)
			}
			binding := pluginprocess.DirectoryBinding{HostPath: cleaned, GuestPath: cleaned, ReadOnly: grant.Name == pluginsdk.PermissionStorageRead}
			if current, exists := bindings[cleaned]; !exists || current.ReadOnly && !binding.ReadOnly {
				bindings[cleaned] = binding
			}
		}
	}
	result := make([]pluginprocess.DirectoryBinding, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GuestPath < result[j].GuestPath })
	return result, nil
}

func resolvePluginConfigPaths(config json.RawMessage, pointer string) ([]string, bool, error) {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || pointer[0] != '/' || len(pointer) > 2048 {
		return nil, false, errors.New("config path must be a bounded JSON Pointer")
	}
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.UseNumber()
	var current any
	if err := decoder.Decode(&current); err != nil {
		return nil, false, err
	}
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		current, ok = object[token]
		if !ok {
			return nil, false, nil
		}
	}
	switch value := current.(type) {
	case string:
		return []string{value}, true, nil
	case []any:
		if len(value) > 64 {
			return nil, false, errors.New("config path array exceeds the storage binding limit")
		}
		paths := make([]string, len(value))
		for index, item := range value {
			path, ok := item.(string)
			if !ok {
				return nil, false, errors.New("config path array must contain only strings")
			}
			paths[index] = path
		}
		return paths, true, nil
	default:
		return nil, false, errors.New("config path must resolve to a string or string array")
	}
}

func isFilesystemRoot(value string) bool {
	volume := filepath.VolumeName(value)
	return value == string(filepath.Separator) || volume != "" && value == volume+string(filepath.Separator)
}
