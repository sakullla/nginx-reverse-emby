package pluginsdk

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPolicyConsumptionSchemaMatchesPublicJSONShapes(t *testing.T) {
	var schema struct {
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(PolicyConsumptionSchemaV1(), &schema); err != nil {
		t.Fatal(err)
	}
	types := []any{ExecutionTargetSelection{}, DatasetBindingSpec{}, DatasetBindingRequest{}, DatasetBindingRecord{}, DatasetBindingResponse{}, DatasetBindingTargetStatus{}, DatasetBindingInstanceUpdate{}, DatasetAttribute{}, DatasetClassification{}, RuntimeError{}, PolicyDefaultSettingsUpdate{}, PolicyStageIdentity{}, PolicyEntryTarget{}, PolicyModeSettings{}, PolicyControlRequest{}, PolicyControlResponse{}, PolicySettingsVersion{}, PolicySettingsSnapshot{}, PolicySettingsNodeStatus{}, PolicyStageModeProjection{}}
	for _, value := range types {
		kind := reflect.TypeOf(value)
		definition, exists := schema.Definitions[kind.Name()]
		if !exists {
			t.Fatalf("schema lacks %s", kind.Name())
		}
		keys := map[string]bool{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			keys[name] = true
			if _, ok := definition.Properties[name]; !ok {
				t.Fatalf("schema %s lacks JSON field %s", kind.Name(), name)
			}
		}
		if len(definition.Properties) != len(keys) {
			t.Fatalf("schema %s has stale fields", kind.Name())
		}
	}
	first := PolicyConsumptionSchemaV1()
	first[0] = '!'
	if !json.Valid(PolicyConsumptionSchemaV1()) {
		t.Fatal("schema accessor leaked mutable backing bytes")
	}
}
