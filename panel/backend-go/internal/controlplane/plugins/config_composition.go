package plugins

import (
	"errors"
	"fmt"
	"strings"
)

// Expand local definitions before storing or projecting the schema so config
// validation, secret brokering and read-only fields see the same property tree.
// Remote references, recursive definitions and reference siblings are outside
// the bounded host schema vocabulary.
func expandConfigSchema(schema map[string]any) (map[string]any, error) {
	defs, _ := schema["$defs"].(map[string]any)
	if raw, exists := schema["$defs"]; exists && defs == nil {
		return nil, fmt.Errorf("$defs must be an object, got %T", raw)
	}
	nodes := 0
	active := map[string]bool{}
	var expand func(map[string]any, int, bool) (map[string]any, error)
	expand = func(node map[string]any, depth int, inAlternative bool) (map[string]any, error) {
		nodes++
		if depth > 32 || nodes > 4096 {
			return nil, errors.New("config schema exceeds expansion budget")
		}
		if raw, exists := node["$ref"]; exists {
			ref, ok := raw.(string)
			if !ok || !strings.HasPrefix(ref, "#/$defs/") || len(node) != 1 {
				return nil, errors.New("$ref must be a local $defs reference without sibling keywords")
			}
			name := strings.TrimPrefix(ref, "#/$defs/")
			if name == "" || strings.ContainsAny(name, "/~") {
				return nil, errors.New("$ref must name one unescaped root definition")
			}
			target, ok := defs[name].(map[string]any)
			if !ok || active[ref] {
				return nil, fmt.Errorf("$ref %q is unresolved or recursive", ref)
			}
			active[ref] = true
			defer delete(active, ref)
			return expand(target, depth+1, inAlternative)
		}
		result := make(map[string]any, len(node))
		for key, value := range node {
			switch key {
			case "$defs", "$id":
				if depth != 0 {
					return nil, fmt.Errorf("%s is only allowed at the root", key)
				}
				if key == "$id" {
					if id, ok := value.(string); !ok || strings.TrimSpace(id) == "" {
						return nil, errors.New("$id must be a nonempty string")
					}
				}
			case "properties":
				properties, ok := value.(map[string]any)
				if !ok {
					return nil, errors.New("properties must be an object")
				}
				children := make(map[string]any, len(properties))
				for name, raw := range properties {
					child, ok := raw.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("property %q schema must be an object", name)
					}
					expanded, err := expand(child, depth+1, inAlternative)
					if err != nil {
						return nil, fmt.Errorf("property %q: %w", name, err)
					}
					children[name] = expanded
				}
				result[key] = children
			case "items":
				child, ok := value.(map[string]any)
				if !ok {
					return nil, errors.New("items schema must be an object")
				}
				expanded, err := expand(child, depth+1, inAlternative)
				if err != nil {
					return nil, err
				}
				result[key] = expanded
			case "oneOf":
				branches, ok := value.([]any)
				if !ok || len(branches) == 0 || len(branches) > 32 {
					return nil, errors.New("oneOf must contain 1 to 32 schemas")
				}
				expanded := make([]any, 0, len(branches))
				for _, raw := range branches {
					branch, ok := raw.(map[string]any)
					if !ok {
						return nil, errors.New("oneOf branches must be schema objects")
					}
					child, err := expand(branch, depth+1, true)
					if err != nil {
						return nil, err
					}
					expanded = append(expanded, child)
				}
				result[key] = expanded
			default:
				// Field ownership must be unconditional for the host's config
				// projection and secret broker, rather than branch-dependent.
				if inAlternative && (key == "readOnly" || key == "writeOnly" || key == "hostInjected") {
					return nil, fmt.Errorf("%s must be declared outside oneOf", key)
				}
				result[key] = value
			}
		}
		return result, nil
	}
	return expand(schema, 0, false)
}
