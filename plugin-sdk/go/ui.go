package pluginsdk

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Declarative UI vocabulary extensions. The schema_version 1 component
// vocabulary grows by these canonical constants; the control plane remains
// the single enforcement point and aliases these values. Packages using them
// are rejected by older hosts, which do not know the extended vocabulary.
const (
	UIComponentGrid        = "grid"
	UIComponentRadio       = "radio"
	UIComponentMultiselect = "multiselect"
	UIComponentKeyValue    = "keyvalue"
)

var (
	uiComponentIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	uiBindingPointerRegex = regexp.MustCompile(`^/[a-z][a-z0-9_]{0,63}(/[a-z][a-z0-9_]{0,63}){0,7}$`)
)

// UIOption is one canonical option entry shared by select-like components.
type UIOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func validateUIComponentID(id string) error {
	if !uiComponentIDPattern.MatchString(id) {
		return fmt.Errorf("UI id %q is not canonical", id)
	}
	return nil
}

func validateUIBindingPointer(binding string) error {
	if !uiBindingPointerRegex.MatchString(binding) {
		return fmt.Errorf("binding %q is not a canonical config JSON pointer", binding)
	}
	return nil
}

func validateUIOptions(options []UIOption) error {
	if len(options) == 0 || len(options) > 128 {
		return fmt.Errorf("options must contain 1 to 128 entries, got %d", len(options))
	}
	seen := make(map[string]struct{}, len(options))
	for index, option := range options {
		if strings.TrimSpace(option.Value) != option.Value || option.Value == "" {
			return fmt.Errorf("option %d value is missing or not canonical", index)
		}
		if _, duplicate := seen[option.Value]; duplicate {
			return fmt.Errorf("option %d duplicates value %q", index, option.Value)
		}
		seen[option.Value] = struct{}{}
		if strings.TrimSpace(option.Label) != option.Label || option.Label == "" {
			return fmt.Errorf("option %d label is missing or not canonical", index)
		}
	}
	return nil
}

// UIGridComponent lays out child components in a bounded multi-column grid.
// Columns is 2 or 3; zero selects the host default of 2. Children carries the
// nested component documents (section, inputs, or further grids).
type UIGridComponent struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Columns     int    `json:"columns,omitempty"`
	Children    []any  `json:"children"`
}

func (component UIGridComponent) Validate() error {
	if component.Type != UIComponentGrid {
		return fmt.Errorf("grid component type must be %q", UIComponentGrid)
	}
	if err := validateUIComponentID(component.ID); err != nil {
		return err
	}
	if component.Columns != 0 && component.Columns != 2 && component.Columns != 3 {
		return fmt.Errorf("grid columns must be 2 or 3, got %d", component.Columns)
	}
	if len(component.Children) == 0 {
		return errors.New("grid requires at least one child component")
	}
	return nil
}

// UIRadioComponent is a single-choice input bound to a string enum config
// property; options must match the config schema enum exactly.
type UIRadioComponent struct {
	Type        string     `json:"type"`
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Binding     string     `json:"binding"`
	Required    bool       `json:"required,omitempty"`
	ReadOnly    bool       `json:"read_only,omitempty"`
	Options     []UIOption `json:"options"`
}

func (component UIRadioComponent) Validate() error {
	if component.Type != UIComponentRadio {
		return fmt.Errorf("radio component type must be %q", UIComponentRadio)
	}
	if err := validateUIComponentID(component.ID); err != nil {
		return err
	}
	if strings.TrimSpace(component.Label) == "" {
		return errors.New("radio label is required")
	}
	if err := validateUIBindingPointer(component.Binding); err != nil {
		return err
	}
	return validateUIOptions(component.Options)
}

// UIMultiselectComponent is a multi-choice input bound to an array config
// property whose items declare a string enum; options must match that enum.
type UIMultiselectComponent struct {
	Type        string     `json:"type"`
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Binding     string     `json:"binding"`
	Required    bool       `json:"required,omitempty"`
	ReadOnly    bool       `json:"read_only,omitempty"`
	Options     []UIOption `json:"options"`
}

func (component UIMultiselectComponent) Validate() error {
	if component.Type != UIComponentMultiselect {
		return fmt.Errorf("multiselect component type must be %q", UIComponentMultiselect)
	}
	if err := validateUIComponentID(component.ID); err != nil {
		return err
	}
	if strings.TrimSpace(component.Label) == "" {
		return errors.New("multiselect label is required")
	}
	if err := validateUIBindingPointer(component.Binding); err != nil {
		return err
	}
	return validateUIOptions(component.Options)
}

// UIKeyValueComponent edits a map of string values bound to an object config
// property. The bound schema must allow additional properties
// (additionalProperties absent or true) and must not declare fixed properties.
type UIKeyValueComponent struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Binding     string `json:"binding"`
	Required    bool   `json:"required,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
}

func (component UIKeyValueComponent) Validate() error {
	if component.Type != UIComponentKeyValue {
		return fmt.Errorf("keyvalue component type must be %q", UIComponentKeyValue)
	}
	if err := validateUIComponentID(component.ID); err != nil {
		return err
	}
	if strings.TrimSpace(component.Label) == "" {
		return errors.New("keyvalue label is required")
	}
	return validateUIBindingPointer(component.Binding)
}
