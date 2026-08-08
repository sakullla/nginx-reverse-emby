package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
)

const (
	maxDeclarativeUIBytes       = 64 << 10
	maxDeclarativeUIJSONDepth   = 32
	maxDeclarativeUIDepth       = 8
	maxDeclarativeUIChildren    = 64
	maxDeclarativeUIComponents  = 256
	maxDeclarativeUIActions     = 8
	maxDeclarativeUIOptions     = 128
	maxDeclarativeUITotalOption = 512
	maxDeclarativeUITextBytes   = 32 << 10
)

var (
	uiIDPattern           = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	uiBindingTokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type declarativeUIDocument struct {
	SchemaVersion int               `json:"schema_version"`
	Title         string            `json:"title"`
	Description   string            `json:"description,omitempty"`
	Components    []json.RawMessage `json:"components"`
	Actions       []json.RawMessage `json:"actions"`
}

type declarativeUIComponentEnvelope struct {
	Type string `json:"type"`
}

type declarativeUISection struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Children    []json.RawMessage `json:"children"`
}

type declarativeUITextInput struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Binding     string `json:"binding"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
}

type declarativeUINumberInput struct {
	Type        string   `json:"type"`
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Binding     string   `json:"binding"`
	Required    bool     `json:"required,omitempty"`
	ReadOnly    bool     `json:"read_only,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Step        *float64 `json:"step,omitempty"`
}

type declarativeUIToggle struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Binding     string `json:"binding"`
	ReadOnly    bool   `json:"read_only,omitempty"`
}

type declarativeUISelect struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Binding     string            `json:"binding"`
	Required    bool              `json:"required,omitempty"`
	ReadOnly    bool              `json:"read_only,omitempty"`
	Options     []json.RawMessage `json:"options"`
}

type declarativeUIOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type declarativeUINotice struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Tone        string `json:"tone"`
}

type declarativeUIActionEnvelope struct {
	Type string `json:"type"`
}

type declarativeUISubmitAction struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type declarativeUIResetAction struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm string `json:"confirm,omitempty"`
}

type declarativeUIValidation struct {
	componentCount int
	optionCount    int
	textBytes      int
	ids            map[string]struct{}
	bindings       map[string]struct{}
	actionTypes    map[string]struct{}
}

func validateDeclarativeUI(data []byte) error {
	if len(data) == 0 || len(data) > maxDeclarativeUIBytes {
		return fmt.Errorf("UI schema exceeds the %d-byte document budget", maxDeclarativeUIBytes)
	}
	if err := validateUIJSONStructure(data); err != nil {
		return fmt.Errorf("UI schema JSON structure is invalid: %w", err)
	}
	var document declarativeUIDocument
	if err := decodeStrictUIObject(data, &document, "schema_version", "title", "description", "components", "actions"); err != nil {
		return fmt.Errorf("UI schema is not the typed declarative contract: %w", err)
	}
	if document.SchemaVersion != DeclarativeUISchemaVersion {
		return fmt.Errorf("UI schema_version must be %d", DeclarativeUISchemaVersion)
	}
	if len(document.Components) == 0 || len(document.Components) > maxDeclarativeUIChildren {
		return fmt.Errorf("UI schema requires 1 to %d root components", maxDeclarativeUIChildren)
	}
	if len(document.Actions) == 0 || len(document.Actions) > maxDeclarativeUIActions {
		return fmt.Errorf("UI schema requires 1 to %d host actions", maxDeclarativeUIActions)
	}
	state := &declarativeUIValidation{
		ids:         make(map[string]struct{}),
		bindings:    make(map[string]struct{}),
		actionTypes: make(map[string]struct{}),
	}
	if err := state.text("title", document.Title, true, 128); err != nil {
		return err
	}
	if err := state.text("description", document.Description, false, 2048); err != nil {
		return err
	}
	if err := state.components(document.Components, 1); err != nil {
		return err
	}
	for index, raw := range document.Actions {
		if err := state.action(raw); err != nil {
			return fmt.Errorf("action %d: %w", index, err)
		}
	}
	if _, ok := state.actionTypes[UIActionSubmit]; !ok {
		return errors.New("UI schema requires exactly one host submit action")
	}
	return nil
}

func validateUIJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > maxDeclarativeUIJSONDepth {
			return fmt.Errorf("JSON depth exceeds %d", maxDeclarativeUIJSONDepth)
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if _, duplicate := keys[key]; duplicate {
					return fmt.Errorf("duplicate object field %q", key)
				}
				keys[key] = struct{}{}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected closing JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are forbidden")
		}
		return err
	}
	return nil
}

func (state *declarativeUIValidation) components(values []json.RawMessage, depth int) error {
	if depth > maxDeclarativeUIDepth {
		return fmt.Errorf("UI component depth exceeds %d", maxDeclarativeUIDepth)
	}
	if len(values) == 0 || len(values) > maxDeclarativeUIChildren {
		return fmt.Errorf("UI component collection must contain 1 to %d items", maxDeclarativeUIChildren)
	}
	for index, raw := range values {
		state.componentCount++
		if state.componentCount > maxDeclarativeUIComponents {
			return fmt.Errorf("UI component count exceeds %d", maxDeclarativeUIComponents)
		}
		if err := state.component(raw, depth); err != nil {
			return fmt.Errorf("component %d at depth %d: %w", index, depth, err)
		}
	}
	return nil
}

func (state *declarativeUIValidation) component(raw []byte, depth int) error {
	var envelope declarativeUIComponentEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("component must be an object: %w", err)
	}
	switch envelope.Type {
	case UIComponentSection:
		var component declarativeUISection
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "children"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		return state.components(component.Children, depth+1)
	case UIComponentText, UIComponentTextarea, UIComponentSecret:
		var component declarativeUITextInput
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "placeholder", "required", "read_only"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		if err := state.text("placeholder", component.Placeholder, false, 256); err != nil {
			return err
		}
		return state.binding(component.Binding)
	case UIComponentNumber:
		var component declarativeUINumberInput
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "required", "read_only", "minimum", "maximum", "step"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		if err := state.binding(component.Binding); err != nil {
			return err
		}
		return validateUINumberRange(component.Minimum, component.Maximum, component.Step)
	case UIComponentToggle:
		var component declarativeUIToggle
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "read_only"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		return state.binding(component.Binding)
	case UIComponentSelect:
		var component declarativeUISelect
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "required", "read_only", "options"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		if err := state.binding(component.Binding); err != nil {
			return err
		}
		return state.options(component.Options)
	case UIComponentNotice:
		var component declarativeUINotice
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "tone"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		if component.Tone != "info" && component.Tone != "warning" && component.Tone != "error" {
			return errors.New("notice tone must be info, warning, or error")
		}
		return nil
	default:
		return fmt.Errorf("unsupported host component type %q", envelope.Type)
	}
}

func (state *declarativeUIValidation) commonComponent(id, label, description string) error {
	if err := state.id(id); err != nil {
		return err
	}
	if err := state.text("label", label, true, 128); err != nil {
		return err
	}
	return state.text("description", description, false, 1024)
}

func (state *declarativeUIValidation) options(options []json.RawMessage) error {
	if len(options) == 0 || len(options) > maxDeclarativeUIOptions {
		return fmt.Errorf("select requires 1 to %d options", maxDeclarativeUIOptions)
	}
	state.optionCount += len(options)
	if state.optionCount > maxDeclarativeUITotalOption {
		return fmt.Errorf("UI option count exceeds %d", maxDeclarativeUITotalOption)
	}
	seen := make(map[string]struct{}, len(options))
	for index, raw := range options {
		var option declarativeUIOption
		if err := decodeStrictUIObject(raw, &option, "value", "label", "description"); err != nil {
			return fmt.Errorf("option %d: %w", index, err)
		}
		if err := state.text("option value", option.Value, true, 128); err != nil {
			return fmt.Errorf("option %d: %w", index, err)
		}
		if _, exists := seen[option.Value]; exists {
			return fmt.Errorf("option %d duplicates value %q", index, option.Value)
		}
		seen[option.Value] = struct{}{}
		if err := state.text("option label", option.Label, true, 128); err != nil {
			return fmt.Errorf("option %d: %w", index, err)
		}
		if err := state.text("option description", option.Description, false, 512); err != nil {
			return fmt.Errorf("option %d: %w", index, err)
		}
	}
	return nil
}

func (state *declarativeUIValidation) action(raw []byte) error {
	var envelope declarativeUIActionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("action must be an object: %w", err)
	}
	if _, duplicate := state.actionTypes[envelope.Type]; duplicate {
		return fmt.Errorf("duplicate host action type %q", envelope.Type)
	}
	state.actionTypes[envelope.Type] = struct{}{}
	switch envelope.Type {
	case UIActionSubmit:
		var action declarativeUISubmitAction
		if err := decodeStrictUIObject(raw, &action, "type", "id", "label"); err != nil {
			return err
		}
		return state.commonAction(action.ID, action.Label)
	case UIActionReset:
		var action declarativeUIResetAction
		if err := decodeStrictUIObject(raw, &action, "type", "id", "label", "confirm"); err != nil {
			return err
		}
		if err := state.commonAction(action.ID, action.Label); err != nil {
			return err
		}
		return state.text("confirmation", action.Confirm, false, 512)
	default:
		return fmt.Errorf("unsupported host action type %q", envelope.Type)
	}
}

func (state *declarativeUIValidation) commonAction(id, label string) error {
	if err := state.id(id); err != nil {
		return err
	}
	return state.text("action label", label, true, 128)
}

func (state *declarativeUIValidation) id(value string) error {
	if !uiIDPattern.MatchString(value) {
		return fmt.Errorf("UI id %q is not canonical", value)
	}
	if _, duplicate := state.ids[value]; duplicate {
		return fmt.Errorf("duplicate UI id %q", value)
	}
	state.ids[value] = struct{}{}
	return nil
}

func (state *declarativeUIValidation) binding(value string) error {
	if len(value) < 2 || len(value) > 512 || value[0] != '/' {
		return errors.New("binding must be a bounded canonical config JSON pointer")
	}
	parts := strings.Split(value[1:], "/")
	if len(parts) == 0 || len(parts) > 8 {
		return errors.New("binding depth exceeds 8")
	}
	for _, part := range parts {
		if !uiBindingTokenPattern.MatchString(part) || part == "__proto__" || part == "prototype" || part == "constructor" {
			return fmt.Errorf("binding token %q is unsafe or non-canonical", part)
		}
	}
	if _, duplicate := state.bindings[value]; duplicate {
		return fmt.Errorf("duplicate UI binding %q", value)
	}
	state.bindings[value] = struct{}{}
	return nil
}

func (state *declarativeUIValidation) text(name, value string, required bool, maximum int) error {
	if required && value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > maximum {
		return fmt.Errorf("%s must be canonical text of at most %d bytes", name, maximum)
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"<", ">", "{{", "}}", "${", "url("} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("%s contains markup, template, or URI syntax", name)
		}
	}
	if containsDangerousUIScheme(lower) {
		return fmt.Errorf("%s contains markup, template, or URI syntax", name)
	}
	for _, valueRune := range value {
		if unicode.IsControl(valueRune) || valueRune == '\u2028' || valueRune == '\u2029' || (valueRune >= '\u202a' && valueRune <= '\u202e') || (valueRune >= '\u2066' && valueRune <= '\u2069') {
			return fmt.Errorf("%s contains control or bidi formatting characters", name)
		}
	}
	state.textBytes += len(value)
	if state.textBytes > maxDeclarativeUITextBytes {
		return fmt.Errorf("UI text exceeds the %d-byte budget", maxDeclarativeUITextBytes)
	}
	return nil
}

func containsDangerousUIScheme(lower string) bool {
	for _, scheme := range []string{"javascript:", "vbscript:", "data:", "file:"} {
		for start := 0; start < len(lower); {
			index := strings.Index(lower[start:], scheme)
			if index < 0 {
				break
			}
			index += start
			if index == 0 || !isUISchemeTokenByte(lower[index-1]) {
				return true
			}
			start = index + len(scheme)
		}
	}
	return false
}

func isUISchemeTokenByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-'
}

func validateUINumberRange(minimum, maximum, step *float64) error {
	for name, value := range map[string]*float64{"minimum": minimum, "maximum": maximum, "step": step} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return fmt.Errorf("number %s must be finite", name)
		}
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return errors.New("number minimum exceeds maximum")
	}
	if step != nil && *step <= 0 {
		return errors.New("number step must be positive")
	}
	return nil
}

func decodeStrictUIJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are forbidden")
		}
		return err
	}
	return nil
}

func decodeStrictUIObject(data []byte, target any, allowedFields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("value must be an object: %w", err)
	}
	if object == nil {
		return errors.New("value must be an object")
	}
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	for field := range object {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("unknown field %q", field)
		}
	}
	return decodeStrictUIJSON(data, target)
}
