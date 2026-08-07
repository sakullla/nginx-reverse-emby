package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ApplyMigrationChain deterministically applies the single unambiguous chain
// from fromVersion to the candidate manifest version.
func ApplyMigrationChain(root string, manifest Manifest, fromVersion string, raw json.RawMessage) (json.RawMessage, error) {
	if fromVersion == manifest.Version {
		return append(json.RawMessage(nil), raw...), nil
	}
	byFrom := make(map[string]Migration, len(manifest.Migrations))
	for _, migration := range manifest.Migrations {
		if _, duplicate := byFrom[migration.From]; duplicate {
			return nil, fmt.Errorf("ambiguous migration chain from %s", migration.From)
		}
		byFrom[migration.From] = migration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("configuration contains multiple JSON values")
	}
	seen := map[string]bool{}
	for current := fromVersion; current != manifest.Version; {
		if seen[current] {
			return nil, errors.New("migration chain contains a cycle")
		}
		seen[current] = true
		migration, ok := byFrom[current]
		if !ok {
			return nil, fmt.Errorf("missing migration from %s to %s", current, manifest.Version)
		}
		name, err := securePackagePath(root, migration.File)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var document migrationDocument
		migrationDecoder := json.NewDecoder(bytes.NewReader(data))
		migrationDecoder.DisallowUnknownFields()
		if err := migrationDecoder.Decode(&document); err != nil {
			return nil, err
		}
		for index, operation := range document.Operations {
			value, err = applyMigrationOperation(value, operation)
			if err != nil {
				return nil, fmt.Errorf("migration %s operation %d: %w", migration.File, index, err)
			}
		}
		current = migration.To
	}
	return json.Marshal(value)
}

func applyMigrationOperation(root any, operation migrationOperation) (any, error) {
	tokens, err := pointerTokens(operation.Path)
	if err != nil {
		return nil, err
	}
	switch operation.Op {
	case "set":
		decoder := json.NewDecoder(bytes.NewReader(operation.Value))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return replacePointer(root, tokens, value, false)
	case "remove":
		return replacePointer(root, tokens, nil, true)
	case "copy", "rename":
		from, err := pointerTokens(operation.From)
		if err != nil {
			return nil, err
		}
		value, err := readPointer(root, from)
		if err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(value)
		var copied any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		_ = decoder.Decode(&copied)
		if operation.Op == "rename" {
			root, err = replacePointer(root, from, nil, true)
			if err != nil {
				return nil, err
			}
		}
		return replacePointer(root, tokens, copied, false)
	default:
		return nil, fmt.Errorf("unsupported operation %q", operation.Op)
	}
}

func pointerTokens(pointer string) ([]string, error) {
	if !validJSONPointer(pointer) {
		return nil, errors.New("invalid JSON pointer")
	}
	if pointer == "" {
		return nil, nil
	}
	parts := strings.Split(pointer[1:], "/")
	for index := range parts {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(parts[index], "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func readPointer(value any, tokens []string) (any, error) {
	for _, token := range tokens {
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[token]
			if !ok {
				return nil, fmt.Errorf("path component %q does not exist", token)
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(current) {
				return nil, fmt.Errorf("invalid array index %q", token)
			}
			value = current[index]
		default:
			return nil, fmt.Errorf("path component %q traverses a scalar", token)
		}
	}
	return value, nil
}

func replacePointer(current any, tokens []string, replacement any, remove bool) (any, error) {
	if len(tokens) == 0 {
		if remove {
			return nil, errors.New("root cannot be removed")
		}
		return replacement, nil
	}
	token := tokens[0]
	switch typed := current.(type) {
	case map[string]any:
		if len(tokens) == 1 {
			if remove {
				if _, ok := typed[token]; !ok {
					return nil, fmt.Errorf("path component %q does not exist", token)
				}
				delete(typed, token)
			} else {
				typed[token] = replacement
			}
			return typed, nil
		}
		child, ok := typed[token]
		if !ok {
			return nil, fmt.Errorf("path component %q does not exist", token)
		}
		updated, err := replacePointer(child, tokens[1:], replacement, remove)
		if err != nil {
			return nil, err
		}
		typed[token] = updated
		return typed, nil
	case []any:
		if token == "-" && len(tokens) == 1 && !remove {
			return append(typed, replacement), nil
		}
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 || index >= len(typed) {
			return nil, fmt.Errorf("invalid array index %q", token)
		}
		if len(tokens) == 1 {
			if remove {
				return append(typed[:index], typed[index+1:]...), nil
			}
			typed[index] = replacement
			return typed, nil
		}
		updated, err := replacePointer(typed[index], tokens[1:], replacement, remove)
		if err != nil {
			return nil, err
		}
		typed[index] = updated
		return typed, nil
	default:
		return nil, fmt.Errorf("path component %q traverses a scalar", token)
	}
}
