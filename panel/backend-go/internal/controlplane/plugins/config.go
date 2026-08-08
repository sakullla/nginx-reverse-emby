package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

func DecodeConfigSchema(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values are forbidden")
		}
		return nil, err
	}
	return schema, nil
}

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
			if enumEqual(candidate, value) {
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
		if minimum, ok := nonNegativeIntegerBound(schema["minItems"]); ok && len(items) < minimum {
			return fmt.Errorf("%s has too few items", location)
		}
		if maximum, ok := nonNegativeIntegerBound(schema["maxItems"]); ok && len(items) > maximum {
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
		if minimum, ok := nonNegativeIntegerBound(schema["minLength"]); ok && len([]rune(text)) < minimum {
			return fmt.Errorf("%s is too short", location)
		}
		if maximum, ok := nonNegativeIntegerBound(schema["maxLength"]); ok && len([]rune(text)) > maximum {
			return fmt.Errorf("%s is too long", location)
		}
	case "integer":
		number, ok := exactNumber(value)
		if !ok || !number.IsInt() {
			return fmt.Errorf("%s must be an integer", location)
		}
	case "number":
		if _, ok := exactNumber(value); !ok {
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

func enumEqual(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	leftNumber, leftNumeric := exactNumber(left)
	rightNumber, rightNumeric := exactNumber(right)
	return leftNumeric && rightNumeric && leftNumber.Cmp(rightNumber) == 0
}

func exactNumber(value any) (*big.Rat, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case float64:
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case int:
		text = strconv.Itoa(typed)
	case int64:
		text = strconv.FormatInt(typed, 10)
	default:
		return nil, false
	}
	number, ok := new(big.Rat).SetString(text)
	return number, ok
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

func nonNegativeIntegerBound(value any) (int, bool) {
	if hasZeroSignificand(value) {
		return 0, true
	}
	number, ok := exactNumber(value)
	if !ok || !number.IsInt() || number.Sign() < 0 {
		return 0, false
	}
	maximum := new(big.Int).SetUint64(uint64(^uint(0) >> 1))
	if number.Num().Cmp(maximum) > 0 {
		return 0, false
	}
	return int(number.Num().Uint64()), true
}

func hasZeroSignificand(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	text := number.String()
	if strings.HasPrefix(text, "-") {
		text = text[1:]
	}
	if exponentAt := strings.IndexAny(text, "eE"); exponentAt >= 0 {
		exponent := text[exponentAt+1:]
		text = text[:exponentAt]
		if strings.HasPrefix(exponent, "+") || strings.HasPrefix(exponent, "-") {
			exponent = exponent[1:]
		}
		if exponent == "" {
			return false
		}
		for _, digit := range exponent {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] != "0" || len(parts) == 2 && parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		for _, digit := range parts[1] {
			if digit != '0' {
				return false
			}
		}
	}
	return true
}
