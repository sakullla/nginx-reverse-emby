package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// ValidateConfig applies the deterministic subset of JSON Schema supported by
// plugin configuration: object/array/scalar types, required, properties,
// additionalProperties, enum, items, and numeric/string bounds.
func ValidateConfig(schema map[string]any, raw json.RawMessage) error {
	if err := validateJSONSchema(schema); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are forbidden")
		}
		return err
	}
	return validateSchemaValue(schema, value, "$")
}

func validateSchemaValue(schema map[string]any, value any, location string) error {
	if values, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range values {
			if reflect.DeepEqual(candidate, value) || fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is outside enum", location)
		}
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", location)
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range stringList(schema["required"]) {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s.%s is required", location, required)
			}
		}
		additional, hasAdditional := schema["additionalProperties"].(bool)
		for name, child := range object {
			childSchema, exists := properties[name].(map[string]any)
			if !exists {
				if hasAdditional && !additional {
					return fmt.Errorf("%s.%s is not allowed", location, name)
				}
				continue
			}
			if err := validateSchemaValue(childSchema, child, location+"."+name); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", location)
		}
		if minimum, ok := numeric(schema["minItems"]); ok && float64(len(items)) < minimum {
			return fmt.Errorf("%s has too few items", location)
		}
		if maximum, ok := numeric(schema["maxItems"]); ok && float64(len(items)) > maximum {
			return fmt.Errorf("%s has too many items", location)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range items {
				if err := validateSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", location)
		}
		if minimum, ok := numeric(schema["minLength"]); ok && float64(len([]rune(text))) < minimum {
			return fmt.Errorf("%s is too short", location)
		}
		if maximum, ok := numeric(schema["maxLength"]); ok && float64(len([]rune(text))) > maximum {
			return fmt.Errorf("%s is too long", location)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok || strings.Contains(number.String(), ".") {
			return fmt.Errorf("%s must be an integer", location)
		}
		if _, err := number.Int64(); err != nil {
			return fmt.Errorf("%s must be an integer", location)
		}
	case "number":
		if _, ok := numeric(value); !ok {
			return fmt.Errorf("%s must be a number", location)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", location)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", location)
		}
	}
	return nil
}

func stringList(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
