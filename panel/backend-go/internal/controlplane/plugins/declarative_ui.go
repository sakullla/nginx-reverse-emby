package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"unicode"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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

// DeclarativeUIDocument is the bounded host-renderable projection. It contains
// data for the fixed component vocabulary only; package HTML, script and URLs
// are never part of this contract.
type DeclarativeUIDocument struct {
	SchemaVersion int                      `json:"schema_version"`
	Title         string                   `json:"title"`
	Description   string                   `json:"description,omitempty"`
	Components    []DeclarativeUIComponent `json:"components"`
	Actions       []DeclarativeUIAction    `json:"actions"`
}

type DeclarativeUIComponent struct {
	Type        string                   `json:"type"`
	ID          string                   `json:"id"`
	Label       string                   `json:"label"`
	Description string                   `json:"description,omitempty"`
	Binding     string                   `json:"binding,omitempty"`
	Placeholder string                   `json:"placeholder,omitempty"`
	Required    bool                     `json:"required,omitempty"`
	ReadOnly    bool                     `json:"read_only,omitempty"`
	Minimum     *json.Number             `json:"minimum,omitempty"`
	Maximum     *json.Number             `json:"maximum,omitempty"`
	Step        *json.Number             `json:"step,omitempty"`
	Options     []DeclarativeUIOption    `json:"options,omitempty"`
	Children    []DeclarativeUIComponent `json:"children,omitempty"`
	Tone        string                   `json:"tone,omitempty"`
}

type DeclarativeUIOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type DeclarativeUIAction struct {
	Type       string                   `json:"type"`
	ID         string                   `json:"id"`
	Label      string                   `json:"label"`
	Capability pluginsdk.HostCapability `json:"capability,omitempty"`
	TargetKind string                   `json:"target_kind,omitempty"`
	Confirm    string                   `json:"confirm,omitempty"`
}

func ProjectDeclarativeUI(data []byte, configSchema map[string]any, permissions []Permission) (*DeclarativeUIDocument, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if err := validateDeclarativeUIForPermissions(data, configSchema, permissions); err != nil {
		return nil, err
	}
	var source declarativeUIDocument
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	document := &DeclarativeUIDocument{SchemaVersion: source.SchemaVersion, Title: source.Title, Description: source.Description, Components: make([]DeclarativeUIComponent, 0, len(source.Components)), Actions: make([]DeclarativeUIAction, 0, len(source.Actions))}
	for _, raw := range source.Components {
		component, err := projectDeclarativeUIComponent(raw)
		if err != nil {
			return nil, err
		}
		document.Components = append(document.Components, component)
	}
	for _, raw := range source.Actions {
		var action DeclarativeUIAction
		if err := json.Unmarshal(raw, &action); err != nil {
			return nil, err
		}
		document.Actions = append(document.Actions, action)
	}
	return document, nil
}

func projectDeclarativeUIComponent(raw json.RawMessage) (DeclarativeUIComponent, error) {
	var component DeclarativeUIComponent
	if err := json.Unmarshal(raw, &component); err != nil {
		return DeclarativeUIComponent{}, err
	}
	if component.Type != UIComponentSection {
		return component, nil
	}
	var section declarativeUISection
	if err := json.Unmarshal(raw, &section); err != nil {
		return DeclarativeUIComponent{}, err
	}
	component.Children = make([]DeclarativeUIComponent, 0, len(section.Children))
	for _, childRaw := range section.Children {
		child, err := projectDeclarativeUIComponent(childRaw)
		if err != nil {
			return DeclarativeUIComponent{}, err
		}
		component.Children = append(component.Children, child)
	}
	return component, nil
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
	Type        string       `json:"type"`
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	Binding     string       `json:"binding"`
	Required    bool         `json:"required,omitempty"`
	ReadOnly    bool         `json:"read_only,omitempty"`
	Minimum     *json.Number `json:"minimum,omitempty"`
	Maximum     *json.Number `json:"maximum,omitempty"`
	Step        *json.Number `json:"step,omitempty"`
}

type declarativeUIToggle struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Binding     string `json:"binding"`
	Required    bool   `json:"required,omitempty"`
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

type declarativeUIDynamicAction struct {
	Type       string                   `json:"type"`
	ID         string                   `json:"id"`
	Label      string                   `json:"label"`
	Capability pluginsdk.HostCapability `json:"capability"`
	TargetKind string                   `json:"target_kind"`
	Confirm    string                   `json:"confirm,omitempty"`
}

type declarativeUIValidation struct {
	componentCount int
	optionCount    int
	textBytes      int
	ids            map[string]struct{}
	bindings       map[string]struct{}
	actionTypes    map[string]struct{}
	permissions    map[string]struct{}
	configSchema   map[string]any
	dynamicActions *[]pluginsdk.DynamicAction
}

type declarativeUIBinding struct {
	path     string
	node     map[string]any
	required bool
}

func validateDeclarativeUI(data []byte, configSchema map[string]any) error {
	return validateDeclarativeUIWithActions(data, configSchema, nil, nil)
}

func validateDeclarativeUIForPermissions(data []byte, configSchema map[string]any, permissions []Permission) error {
	return validateDeclarativeUIWithActions(data, configSchema, permissions, nil)
}

func validateDeclarativeUIWithActions(data []byte, configSchema map[string]any, permissions []Permission, actions *[]pluginsdk.DynamicAction) error {
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
		ids:            make(map[string]struct{}),
		bindings:       make(map[string]struct{}),
		actionTypes:    make(map[string]struct{}),
		configSchema:   configSchema,
		permissions:    make(map[string]struct{}, len(permissions)),
		dynamicActions: actions,
	}
	for _, permission := range permissions {
		state.permissions[permission.Name] = struct{}{}
	}
	if configSchema == nil {
		return errors.New("UI schema requires the validated config schema")
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
		binding, err := state.binding(component.Binding)
		if err != nil {
			return err
		}
		return validateUIBindingContract(binding, component.Type, component.Required, component.ReadOnly, nil, nil, nil, nil)
	case UIComponentNumber:
		var component declarativeUINumberInput
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "required", "read_only", "minimum", "maximum", "step"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		binding, err := state.binding(component.Binding)
		if err != nil {
			return err
		}
		if err := validateUINumberRange(component.Minimum, component.Maximum, component.Step); err != nil {
			return err
		}
		return validateUIBindingContract(binding, component.Type, component.Required, component.ReadOnly, component.Minimum, component.Maximum, component.Step, nil)
	case UIComponentToggle:
		var component declarativeUIToggle
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "required", "read_only"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		binding, err := state.binding(component.Binding)
		if err != nil {
			return err
		}
		return validateUIBindingContract(binding, component.Type, component.Required, component.ReadOnly, nil, nil, nil, nil)
	case UIComponentSelect:
		var component declarativeUISelect
		if err := decodeStrictUIObject(raw, &component, "type", "id", "label", "description", "binding", "required", "read_only", "options"); err != nil {
			return err
		}
		if err := state.commonComponent(component.ID, component.Label, component.Description); err != nil {
			return err
		}
		binding, err := state.binding(component.Binding)
		if err != nil {
			return err
		}
		options, err := state.options(component.Options)
		if err != nil {
			return err
		}
		return validateUIBindingContract(binding, component.Type, component.Required, component.ReadOnly, nil, nil, nil, options)
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

func (state *declarativeUIValidation) options(options []json.RawMessage) ([]string, error) {
	if len(options) == 0 || len(options) > maxDeclarativeUIOptions {
		return nil, fmt.Errorf("select requires 1 to %d options", maxDeclarativeUIOptions)
	}
	state.optionCount += len(options)
	if state.optionCount > maxDeclarativeUITotalOption {
		return nil, fmt.Errorf("UI option count exceeds %d", maxDeclarativeUITotalOption)
	}
	seen := make(map[string]struct{}, len(options))
	values := make([]string, 0, len(options))
	for index, raw := range options {
		var option declarativeUIOption
		if err := decodeStrictUIObject(raw, &option, "value", "label", "description"); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
		if err := state.text("option value", option.Value, true, 128); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
		if _, exists := seen[option.Value]; exists {
			return nil, fmt.Errorf("option %d duplicates value %q", index, option.Value)
		}
		seen[option.Value] = struct{}{}
		values = append(values, option.Value)
		if err := state.text("option label", option.Label, true, 128); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
		if err := state.text("option description", option.Description, false, 512); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
	}
	return values, nil
}

func (state *declarativeUIValidation) action(raw []byte) error {
	var envelope declarativeUIActionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("action must be an object: %w", err)
	}
	actionKey := envelope.Type
	if envelope.Type == UIActionDynamic {
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return fmt.Errorf("dynamic action must be an object: %w", err)
		}
		actionKey += ":" + identity.ID
	}
	if _, duplicate := state.actionTypes[actionKey]; duplicate {
		return fmt.Errorf("duplicate host action type %q", envelope.Type)
	}
	state.actionTypes[actionKey] = struct{}{}
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
	case UIActionDynamic:
		var action declarativeUIDynamicAction
		if err := decodeStrictUIObject(raw, &action, "type", "id", "label", "capability", "target_kind", "confirm"); err != nil {
			return err
		}
		if err := state.commonAction(action.ID, action.Label); err != nil {
			return err
		}
		if err := (pluginsdk.DynamicAction{ID: action.ID, Label: action.Label, Capability: action.Capability, TargetKind: action.TargetKind, Confirm: action.Confirm}).Validate(); err != nil {
			return err
		}
		if _, ok := state.permissions[string(pluginsdk.CapabilityUIDynamicActions)]; !ok {
			return errors.New("dynamic action requires signed ui.dynamic-actions permission")
		}
		if _, ok := state.permissions[string(action.Capability)]; !ok {
			return errors.New("dynamic action capability is absent from signed permissions")
		}
		if state.dynamicActions != nil {
			*state.dynamicActions = append(*state.dynamicActions, pluginsdk.DynamicAction{ID: action.ID, Label: action.Label, Capability: action.Capability, TargetKind: action.TargetKind, Confirm: action.Confirm})
		}
		return nil
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

func (state *declarativeUIValidation) binding(value string) (declarativeUIBinding, error) {
	if len(value) < 2 || len(value) > 512 || value[0] != '/' {
		return declarativeUIBinding{}, errors.New("binding must be a bounded canonical config JSON pointer")
	}
	parts := strings.Split(value[1:], "/")
	if len(parts) == 0 || len(parts) > 8 {
		return declarativeUIBinding{}, errors.New("binding depth exceeds 8")
	}
	for _, part := range parts {
		if !uiBindingTokenPattern.MatchString(part) || part == "__proto__" || part == "prototype" || part == "constructor" {
			return declarativeUIBinding{}, fmt.Errorf("binding token %q is unsafe or non-canonical", part)
		}
	}
	if _, duplicate := state.bindings[value]; duplicate {
		return declarativeUIBinding{}, fmt.Errorf("duplicate UI binding %q", value)
	}
	state.bindings[value] = struct{}{}

	current := state.configSchema
	for index, part := range parts {
		if current["type"] != "object" {
			return declarativeUIBinding{}, fmt.Errorf("binding %q traverses non-object schema at %q", value, strings.Join(parts[:index], "/"))
		}
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return declarativeUIBinding{}, fmt.Errorf("binding %q does not resolve to a declared config property", value)
		}
		child, ok := properties[part].(map[string]any)
		if !ok {
			return declarativeUIBinding{}, fmt.Errorf("binding %q does not resolve to a declared config property", value)
		}
		if index == len(parts)-1 {
			return declarativeUIBinding{path: value, node: child, required: schemaPropertyRequired(current, part)}, nil
		}
		current = child
	}
	return declarativeUIBinding{}, fmt.Errorf("binding %q does not resolve to a declared config property", value)
}

func schemaPropertyRequired(parent map[string]any, property string) bool {
	required, _ := parent["required"].([]any)
	for _, value := range required {
		if value == property {
			return true
		}
	}
	return false
}

func validateUIBindingContract(binding declarativeUIBinding, componentType string, required, readOnly bool, minimum, maximum, step *json.Number, options []string) error {
	schemaType, _ := binding.node["type"].(string)
	wantType := "string"
	switch componentType {
	case UIComponentNumber:
		if schemaType != "number" && schemaType != "integer" {
			return fmt.Errorf("binding %q component %s requires a number or integer schema, got %q", binding.path, componentType, schemaType)
		}
	case UIComponentToggle:
		wantType = "boolean"
	case UIComponentText, UIComponentTextarea, UIComponentSecret, UIComponentSelect:
	default:
		return fmt.Errorf("binding %q has unsupported component type %q", binding.path, componentType)
	}
	if componentType != UIComponentNumber && schemaType != wantType {
		return fmt.Errorf("binding %q component %s requires a %s schema, got %q", binding.path, componentType, wantType, schemaType)
	}
	if binding.required != required {
		return fmt.Errorf("binding %q required=%t contradicts config schema required=%t", binding.path, required, binding.required)
	}
	schemaReadOnly, _ := schemaBooleanAnnotation(binding.node, "readOnly")
	schemaWriteOnly, _ := schemaBooleanAnnotation(binding.node, "writeOnly")
	if schemaReadOnly != readOnly {
		return fmt.Errorf("binding %q read_only=%t contradicts config schema readOnly=%t", binding.path, readOnly, schemaReadOnly)
	}
	if (componentType == UIComponentSecret) != schemaWriteOnly {
		return fmt.Errorf("binding %q secret component and config schema writeOnly metadata must match", binding.path)
	}

	_, hasEnum := binding.node["enum"]
	if componentType == UIComponentSelect {
		if !hasEnum {
			return fmt.Errorf("binding %q select requires a config schema enum", binding.path)
		}
		if err := validateUIEnum(binding.path, binding.node["enum"], options); err != nil {
			return err
		}
	} else if hasEnum {
		return fmt.Errorf("binding %q config schema enum requires a select component", binding.path)
	}
	if componentType == UIComponentNumber {
		for _, constraint := range []struct {
			ui        *json.Number
			schemaKey string
			uiName    string
		}{{minimum, "minimum", "minimum"}, {maximum, "maximum", "maximum"}, {step, "multipleOf", "step"}} {
			if err := validateUIExactConstraint(binding.path, constraint.uiName, constraint.ui, binding.node, constraint.schemaKey); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateUIEnum(path string, schemaValue any, options []string) error {
	values, ok := schemaValue.([]any)
	if !ok || len(values) != len(options) {
		return fmt.Errorf("binding %q select options contradict config schema enum", path)
	}
	want := make(map[string]struct{}, len(values))
	for _, value := range values {
		stringValue, ok := value.(string)
		if !ok {
			return fmt.Errorf("binding %q select requires a string enum", path)
		}
		if _, duplicate := want[stringValue]; duplicate {
			return fmt.Errorf("binding %q config schema enum contains duplicate value %q", path, stringValue)
		}
		want[stringValue] = struct{}{}
	}
	for _, option := range options {
		if _, ok := want[option]; !ok {
			return fmt.Errorf("binding %q select option %q is absent from config schema enum", path, option)
		}
	}
	return nil
}

func validateUIExactConstraint(path, uiName string, uiValue *json.Number, schema map[string]any, schemaKey string) error {
	schemaValue, schemaPresent := schema[schemaKey]
	if (uiValue != nil) != schemaPresent {
		return fmt.Errorf("binding %q UI %s and config schema %s must both be present or absent", path, uiName, schemaKey)
	}
	if uiValue == nil {
		return nil
	}
	uiNumber, valid := exactNumber(*uiValue)
	if !valid {
		return fmt.Errorf("binding %q UI %s must be a finite JSON number", path, uiName)
	}
	schemaNumber, valid := exactNumber(schemaValue)
	if !valid || uiNumber.Cmp(schemaNumber) != 0 {
		return fmt.Errorf("binding %q UI %s contradicts config schema %s", path, uiName, schemaKey)
	}
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

func validateUINumberRange(minimum, maximum, step *json.Number) error {
	parsed := make(map[string]*big.Rat, 3)
	for name, value := range map[string]*json.Number{"minimum": minimum, "maximum": maximum, "step": step} {
		if value == nil {
			continue
		}
		number, valid := exactNumber(*value)
		if !valid {
			return fmt.Errorf("number %s must be finite", name)
		}
		parsed[name] = number
	}
	if parsed["minimum"] != nil && parsed["maximum"] != nil && parsed["minimum"].Cmp(parsed["maximum"]) > 0 {
		return errors.New("number minimum exceeds maximum")
	}
	if parsed["step"] != nil && parsed["step"].Sign() <= 0 {
		return errors.New("number step must be positive")
	}
	return nil
}

func decodeStrictUIJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
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
