package core

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestDatasetSnapshotCopiesNestedSelectorPointersAcrossGenerationViews(t *testing.T) {
	boolean, integer := true, int64(7)
	snapshot := model.Snapshot{Revision: 1, Datasets: []model.DatasetSnapshot{{Bindings: []model.DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "ai", Kind: sdk.DatasetClassificationDomain, Attributes: []sdk.DatasetAttribute{{Name: "!cn", Boolean: &boolean}, {Name: "rank", Integer: &integer}}}}}}}}}
	generation, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	boolean = false
	integer = 8
	snapshot.Datasets[0].Bindings[0].InstanceID = "mutated"
	first := generation.Snapshot()
	attrs := first.Datasets[0].Bindings[0].Classifications[0].Attributes
	if !*attrs[0].Boolean || *attrs[1].Integer != 7 || first.Datasets[0].Bindings[0].InstanceID != "instance" {
		t.Fatal("generation aliased caller dataset bindings")
	}
	*attrs[0].Boolean = false
	*attrs[1].Integer = 9
	second := generation.Snapshot().Datasets[0].Bindings[0].Classifications[0].Attributes
	if !*second[0].Boolean || *second[1].Integer != 7 {
		t.Fatal("generation snapshot getter leaked nested attribute pointers")
	}
	cloned := cloneSnapshot(generation.Snapshot())
	*cloned.Datasets[0].Bindings[0].Classifications[0].Attributes[1].Integer = 10
	if *generation.Snapshot().Datasets[0].Bindings[0].Classifications[0].Attributes[1].Integer != 7 {
		t.Fatal("runtime clone mutated source snapshot")
	}
	previous := generation.Snapshot()
	merged := MergeSnapshotPayload(model.Snapshot{}, previous)
	*merged.Datasets[0].Bindings[0].Classifications[0].Attributes[0].Boolean = false
	if !*previous.Datasets[0].Bindings[0].Classifications[0].Attributes[0].Boolean {
		t.Fatal("partial snapshot merge aliased old generation")
	}
	removed := MergeSnapshotPayload(model.Snapshot{Datasets: []model.DatasetSnapshot{}}, previous)
	if len(removed.Datasets) != 0 {
		t.Fatal("explicit dataset removal restored prior bindings")
	}
}
