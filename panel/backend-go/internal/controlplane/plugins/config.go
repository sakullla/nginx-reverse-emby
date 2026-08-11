package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxExactNumberMantissaDigits    = 4096
	maxExactNumberExponentMagnitude = 4096
	maxUniqueConfigItems            = 1024
	maxConfigEnumValues             = 256
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
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := make(map[string]struct{}, len(items))
			for _, item := range items {
				key, err := jsonSemanticKey(item)
				if err != nil {
					return fmt.Errorf("%s cannot be compared for uniqueness: %w", location, err)
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("%s contains duplicate items", location)
				}
				seen[key] = struct{}{}
			}
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
		if expression, ok := schema["pattern"].(string); ok {
			compiled, err := regexp.Compile(expression)
			if err != nil {
				return fmt.Errorf("schema pattern is invalid: %w", err)
			}
			if !compiled.MatchString(text) {
				return fmt.Errorf("%s does not match pattern", location)
			}
		}
	case "integer":
		number, ok := exactNumber(value)
		if !ok || !number.IsInt() {
			return fmt.Errorf("%s must be an integer", location)
		}
		if err := validateExactNumericConstraints(schema, number, location); err != nil {
			return err
		}
	case "number":
		number, ok := exactNumber(value)
		if !ok {
			return fmt.Errorf("%s must be a number", location)
		}
		if err := validateExactNumericConstraints(schema, number, location); err != nil {
			return err
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

func validateExactNumericConstraints(schema map[string]any, number *big.Rat, location string) error {
	if raw, exists := schema["minimum"]; exists {
		minimum, ok := exactNumber(raw)
		if !ok {
			return errors.New("schema minimum must be an exact JSON number")
		}
		if number.Cmp(minimum) < 0 {
			return fmt.Errorf("%s is below minimum", location)
		}
	}
	if raw, exists := schema["maximum"]; exists {
		maximum, ok := exactNumber(raw)
		if !ok {
			return errors.New("schema maximum must be an exact JSON number")
		}
		if number.Cmp(maximum) > 0 {
			return fmt.Errorf("%s is above maximum", location)
		}
	}
	if raw, exists := schema["multipleOf"]; exists {
		multiple, ok := exactNumber(raw)
		if !ok || multiple.Sign() <= 0 {
			return errors.New("schema multipleOf must be an exact positive JSON number")
		}
		quotient := new(big.Rat).Quo(number, multiple)
		if !quotient.IsInt() {
			return fmt.Errorf("%s is not an exact multipleOf value", location)
		}
	}
	return nil
}

func enumEqual(left, right any) bool {
	leftKey, leftErr := jsonSemanticKey(left)
	rightKey, rightErr := jsonSemanticKey(right)
	return leftErr == nil && rightErr == nil && leftKey == rightKey
}

func jsonSemanticKey(value any) (string, error) {
	var builder strings.Builder
	if err := writeJSONSemanticKey(&builder, value, 0); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func writeJSONSemanticKey(builder *strings.Builder, value any, depth int) error {
	if depth > 64 {
		return errors.New("JSON value exceeds comparison depth")
	}
	if number, ok := exactNumber(value); ok {
		builder.WriteByte('n')
		builder.WriteString(number.RatString())
		builder.WriteByte(';')
		return nil
	}
	switch typed := value.(type) {
	case nil:
		builder.WriteByte('0')
	case bool:
		if typed {
			builder.WriteByte('t')
		} else {
			builder.WriteByte('f')
		}
	case string:
		builder.WriteByte('s')
		builder.WriteString(strconv.Itoa(len(typed)))
		builder.WriteByte(':')
		builder.WriteString(typed)
	case []any:
		builder.WriteByte('[')
		for _, item := range typed {
			if err := writeJSONSemanticKey(builder, item, depth+1); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteByte('{')
		for _, key := range keys {
			builder.WriteString(strconv.Itoa(len(key)))
			builder.WriteByte(':')
			builder.WriteString(key)
			if err := writeJSONSemanticKey(builder, typed[key], depth+1); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value type %T", value)
	}
	return nil
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
	if !boundedJSONNumber(text) {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(text)
	return number, ok
}

// boundedJSONNumber validates the JSON number grammar and caps both inputs to
// big.Rat.SetString. In particular, a compact decimal such as 1e1000000000
// must not be allowed to request a billion-digit numerator allocation.
func boundedJSONNumber(text string) bool {
	if text == "" {
		return false
	}
	index := 0
	if text[index] == '-' {
		index++
		if index == len(text) {
			return false
		}
	}
	mantissaDigits := 0
	if text[index] == '0' {
		index++
		mantissaDigits++
		if index < len(text) && text[index] >= '0' && text[index] <= '9' {
			return false
		}
	} else if text[index] >= '1' && text[index] <= '9' {
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
			mantissaDigits++
			if mantissaDigits > maxExactNumberMantissaDigits {
				return false
			}
		}
	} else {
		return false
	}
	if index < len(text) && text[index] == '.' {
		index++
		fractionStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
			mantissaDigits++
			if mantissaDigits > maxExactNumberMantissaDigits {
				return false
			}
		}
		if index == fractionStart {
			return false
		}
	}
	if index == len(text) {
		return true
	}
	if text[index] != 'e' && text[index] != 'E' {
		return false
	}
	index++
	if index < len(text) && (text[index] == '+' || text[index] == '-') {
		index++
	}
	if index == len(text) {
		return false
	}
	exponentMagnitude := 0
	for ; index < len(text); index++ {
		digit := text[index]
		if digit < '0' || digit > '9' {
			return false
		}
		value := int(digit - '0')
		if exponentMagnitude > (maxExactNumberExponentMagnitude-value)/10 {
			return false
		}
		exponentMagnitude = exponentMagnitude*10 + value
	}
	return true
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
