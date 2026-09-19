package pluginsdk

import (
	"strings"
	"testing"
)

func TestUIGridComponentValidate(t *testing.T) {
	valid := UIGridComponent{Type: UIComponentGrid, ID: "layout", Columns: 2, Children: []any{map[string]any{"type": "text"}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid grid rejected: %v", err)
	}
	valid.Columns = 3
	if err := valid.Validate(); err != nil {
		t.Fatalf("columns 3 rejected: %v", err)
	}
	valid.Columns = 0
	if err := valid.Validate(); err != nil {
		t.Fatalf("default columns rejected: %v", err)
	}

	invalidColumns := valid
	invalidColumns.Columns = 4
	if err := invalidColumns.Validate(); err == nil || !strings.Contains(err.Error(), "columns") {
		t.Fatalf("columns 4 accepted: %v", err)
	}
	noChildren := valid
	noChildren.Children = nil
	if err := noChildren.Validate(); err == nil || !strings.Contains(err.Error(), "child") {
		t.Fatalf("empty children accepted: %v", err)
	}
	badID := valid
	badID.ID = "Not canonical"
	if err := badID.Validate(); err == nil {
		t.Fatal("non-canonical id accepted")
	}
}

func TestUIRadioComponentValidate(t *testing.T) {
	valid := UIRadioComponent{
		Type: UIComponentRadio, ID: "mode", Label: "Mode", Binding: "/mode",
		Options: []UIOption{{Value: "observe", Label: "Observe"}, {Value: "block", Label: "Block"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid radio rejected: %v", err)
	}
	noOptions := valid
	noOptions.Options = nil
	if err := noOptions.Validate(); err == nil {
		t.Fatal("radio without options accepted")
	}
	duplicate := valid
	duplicate.Options = []UIOption{{Value: "a", Label: "A"}, {Value: "a", Label: "Again"}}
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate options accepted: %v", err)
	}
	badBinding := valid
	badBinding.Binding = "mode"
	if err := badBinding.Validate(); err == nil {
		t.Fatal("non-pointer binding accepted")
	}
}

func TestUIMultiselectComponentValidate(t *testing.T) {
	valid := UIMultiselectComponent{
		Type: UIComponentMultiselect, ID: "flags", Label: "Flags", Binding: "/flags",
		Options: []UIOption{{Value: "a", Label: "A"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid multiselect rejected: %v", err)
	}
	wrongType := valid
	wrongType.Type = UIComponentRadio
	if err := wrongType.Validate(); err == nil {
		t.Fatal("multiselect with radio type accepted")
	}
}

func TestUIKeyValueComponentValidate(t *testing.T) {
	valid := UIKeyValueComponent{Type: UIComponentKeyValue, ID: "labels", Label: "Labels", Binding: "/labels"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid keyvalue rejected: %v", err)
	}
	noLabel := valid
	noLabel.Label = ""
	if err := noLabel.Validate(); err == nil {
		t.Fatal("keyvalue without label accepted")
	}
	deepBinding := valid
	deepBinding.Binding = "/" + strings.Repeat("a/", 9) + "b"
	if err := deepBinding.Validate(); err == nil {
		t.Fatal("over-deep binding accepted")
	}
}
