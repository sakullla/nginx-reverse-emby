//go:build !integration

package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidatorRuntimeConfigExactNumericConstraints(t *testing.T) {
	tests := []struct {
		name       string
		schema     string
		values     []string
		wantErr    string
		errorValue string
	}{
		{
			name:   "fractional divisor and inclusive boundaries",
			schema: numericConfigSchema("number", `"minimum":-1.25,"maximum":2.5,"multipleOf":0.125`),
			values: []string{"-1.25", "2.5", "0", "0.375", "-0.25"},
		},
		{
			name:       "below fractional minimum",
			schema:     numericConfigSchema("number", `"minimum":-1.25,"maximum":2.5,"multipleOf":0.125`),
			errorValue: "-1.251",
			wantErr:    "below minimum",
		},
		{
			name:       "above fractional maximum",
			schema:     numericConfigSchema("number", `"minimum":-1.25,"maximum":2.5,"multipleOf":0.125`),
			errorValue: "2.501",
			wantErr:    "above maximum",
		},
		{
			name:       "fractional off step",
			schema:     numericConfigSchema("number", `"minimum":-1.25,"maximum":2.5,"multipleOf":0.125`),
			errorValue: "0.38",
			wantErr:    "not an exact multipleOf value",
		},
		{
			name:   "exponent notation",
			schema: numericConfigSchema("number", `"minimum":1e-20,"maximum":5E-20,"multipleOf":1e-20`),
			values: []string{"1e-20", "3e-20", "5E-20"},
		},
		{
			name:       "exponent off step",
			schema:     numericConfigSchema("number", `"minimum":1e-20,"maximum":5E-20,"multipleOf":1e-20`),
			errorValue: "3.5e-20",
			wantErr:    "not an exact multipleOf value",
		},
		{
			name:   "large integer beyond float64 precision",
			schema: numericConfigSchema("integer", `"minimum":9007199254740992,"maximum":9007199254741000,"multipleOf":2`),
			values: []string{"9007199254740992", "9007199254740994", "9007199254741000"},
		},
		{
			name:       "large integer off step",
			schema:     numericConfigSchema("integer", `"minimum":9007199254740992,"maximum":9007199254741000,"multipleOf":2`),
			errorValue: "9007199254740993",
			wantErr:    "not an exact multipleOf value",
		},
		{
			name:       "large integer preserves integer type",
			schema:     numericConfigSchema("integer", `"minimum":9007199254740992,"maximum":9007199254741000,"multipleOf":2`),
			errorValue: "9007199254740992.5",
			wantErr:    "must be an integer",
		},
		{
			name:       "large integer below minimum",
			schema:     numericConfigSchema("integer", `"minimum":9007199254740992,"maximum":9007199254741000,"multipleOf":2`),
			errorValue: "9007199254740990",
			wantErr:    "below minimum",
		},
		{
			name:       "large integer above maximum",
			schema:     numericConfigSchema("integer", `"minimum":9007199254740992,"maximum":9007199254741000,"multipleOf":2`),
			errorValue: "9007199254741002",
			wantErr:    "above maximum",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema, err := DecodeConfigSchema([]byte(test.schema))
			if err != nil {
				t.Fatalf("DecodeConfigSchema() error = %v", err)
			}
			for _, value := range test.values {
				if err := ValidateConfig(schema, json.RawMessage(`{"value":`+value+`}`)); err != nil {
					t.Errorf("ValidateConfig(value=%s) error = %v", value, err)
				}
			}
			if test.wantErr == "" {
				return
			}
			err = ValidateConfig(schema, json.RawMessage(`{"value":`+test.errorValue+`}`))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateConfig(value=%s) error = %v, want containing %q", test.errorValue, err, test.wantErr)
			}
		})
	}
}

func TestValidatorRuntimeConfigRejectsNonPositiveMultipleOf(t *testing.T) {
	for _, multipleOf := range []string{"0", "-0.5", "0e10"} {
		t.Run(multipleOf, func(t *testing.T) {
			schema, err := DecodeConfigSchema([]byte(numericConfigSchema("number", `"multipleOf":`+multipleOf)))
			if err != nil {
				t.Fatalf("DecodeConfigSchema() error = %v", err)
			}
			err = ValidateConfig(schema, json.RawMessage(`{"value":1}`))
			if err == nil || !strings.Contains(err.Error(), "multipleOf must be positive") {
				t.Fatalf("ValidateConfig(multipleOf=%s) error = %v, want positive constraint error", multipleOf, err)
			}
		})
	}
}

func numericConfigSchema(typeName, constraints string) string {
	if constraints != "" {
		constraints = "," + constraints
	}
	return `{"type":"object","properties":{"value":{"type":"` + typeName + `"` + constraints + `}},"required":["value"],"additionalProperties":false}`
}
