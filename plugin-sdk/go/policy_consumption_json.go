package pluginsdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// decodePolicyConsumptionJSON rejects duplicate/case-aliased/unknown envelope
// fields at every typed level. RawMessage remains explicitly opaque Config.
func decodePolicyConsumptionJSON(raw json.RawMessage, target any, limit int) error {
	if err := validatePolicyOverlayJSON(raw, limit); err != nil {
		return err
	}
	if err := validatePolicyConsumptionFields(raw, reflect.TypeOf(target)); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
func validatePolicyConsumptionFields(raw json.RawMessage, kind reflect.Type) error {
	if kind == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	if kind.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return validatePolicyConsumptionFields(raw, kind.Elem())
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null is not a typed consumption value")
	}
	switch kind.Kind() {
	case reflect.Struct:
		if !policyOverlayJSONObject(raw) {
			return errors.New("consumption typed object required")
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			if field.IsExported() && name != "-" {
				fields[name] = field.Type
			}
		}
		for name, value := range values {
			field, ok := fields[name]
			if !ok {
				return errors.New("unknown consumption field")
			}
			if err := validatePolicyConsumptionFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := validatePolicyConsumptionFields(value, kind.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
