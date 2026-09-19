package core

import (
	"reflect"
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

func TestManagedEntryPolicySnapshotIsolation(t *testing.T) {
	snapshot := model.Snapshot{Revision: 1, PluginGenerations: []model.PluginGeneration{{ManagedNetworkPolicy: &model.PolicyRef{ID: "entry-policy", Overlay: []byte(`{"mode":"deny"}`)}}}}
	generation, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.PluginGenerations[0].ManagedNetworkPolicy.ID = "foreign"
	snapshot.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] = '!'
	first := generation.Snapshot()
	if first.PluginGenerations[0].ManagedNetworkPolicy.ID != "entry-policy" || first.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] != '{' {
		t.Fatal("generation retained caller policy alias")
	}
	first.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] = '!'
	stable := generation.Snapshot()
	if stable.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] != '{' {
		t.Fatal("generation getter leaked overlay")
	}
	cloned := cloneSnapshot(stable)
	cloned.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] = '!'
	if stable.PluginGenerations[0].ManagedNetworkPolicy.Overlay[0] != '{' {
		t.Fatal("runtime clone leaked overlay")
	}
}

func TestRuntimeActivePolicySettingsRemainIsolatedAcrossUpdates(t *testing.T) {
	fixture := func(revision int64) model.Snapshot {
		defaultMode, entryMode := sdk.PolicyModeObserve, sdk.PolicyModeEnforce
		settings := sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: uint64(revision), InstanceVersion: 2},
			Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingRaw, DefaultMode: &defaultMode, EntryMode: &entryMode}}
		ref := &model.PolicyRef{ID: "chain", OverlayFormat: sdk.PolicyOverlayFormatLegacyWAF, LegacyPolicyID: "existing-waf", Overlay: []byte(`{"mode":"deny"}`),
			StageModes: []model.PolicyModeBinding{{Stage: sdk.PolicyStageIdentity{Kind: "ip", PolicyID: "default-ip"}, Snapshot: settings}}}
		return model.Snapshot{Revision: revision,
			Rules: []model.HTTPRule{{ID: 1, PolicyRef: ref}}, L4Rules: []model.L4Rule{{ID: 2, PolicyRef: ref}},
			PluginGenerations: []model.PluginGeneration{{ManagedNetworkPolicy: ref, Config: []byte(`{"enabled":true}`),
				ManagedNetworkPolicies: map[string]*model.PolicyRef{"tcp": ref, "udp": ref},
				RequiredFeatures:       []string{sdk.RPCFeatureExecutionScopeV1}, HTTPBackendProviders: []sdk.HTTPBackendProviderDescriptor{{ID: "web", DisplayName: "Web"}}}},
			PluginPolicies: []model.PluginPolicy{{ID: "chain", Stages: []model.PolicyStage{{PolicySettings: &settings,
				DeclaredScopes: []string{"dataset.query"}, GrantedScopes: []string{"dataset.query"}, ExtensionPoints: []string{"l4.accept"}, Config: []byte(`{"source":"geo"}`)}}}},
		}
	}
	mutations := map[string]func(model.Snapshot){
		"stage settings": func(s model.Snapshot) {
			settings := s.PluginPolicies[0].Stages[0].PolicySettings
			settings.Version.Revision = 99
			*settings.Settings.DefaultMode = sdk.PolicyModeEnforce
			*settings.Settings.EntryMode = sdk.PolicyModeObserve
		},
		"stage config and scopes": func(s model.Snapshot) {
			stage := &s.PluginPolicies[0].Stages[0]
			stage.Config[0] = '!'
			stage.DeclaredScopes[0], stage.GrantedScopes[0], stage.ExtensionPoints[0] = "foreign", "foreign", "foreign"
		},
		"managed config":       func(s model.Snapshot) { s.PluginGenerations[0].Config[0] = '!' },
		"managed protocol map": func(s model.Snapshot) { delete(s.PluginGenerations[0].ManagedNetworkPolicies, "udp") },
		"execution features and providers": func(s model.Snapshot) {
			s.PluginGenerations[0].RequiredFeatures[0] = "foreign"
			s.PluginGenerations[0].HTTPBackendProviders[0].ID = "foreign"
		},
	}
	for name, selectRef := range map[string]func(model.Snapshot) *model.PolicyRef{
		"managed entry":     func(s model.Snapshot) *model.PolicyRef { return s.PluginGenerations[0].ManagedNetworkPolicy },
		"managed TCP entry": func(s model.Snapshot) *model.PolicyRef { return s.PluginGenerations[0].ManagedNetworkPolicies["tcp"] },
		"managed UDP entry": func(s model.Snapshot) *model.PolicyRef { return s.PluginGenerations[0].ManagedNetworkPolicies["udp"] },
		"HTTP entry":        func(s model.Snapshot) *model.PolicyRef { return s.Rules[0].PolicyRef },
		"L4 entry":          func(s model.Snapshot) *model.PolicyRef { return s.L4Rules[0].PolicyRef },
	} {
		mutations[name] = func(s model.Snapshot) {
			ref := selectRef(s)
			ref.Overlay[0] = '!'
			ref.OverlayFormat, ref.LegacyPolicyID = "foreign", "foreign"
			ref.StageModes[0].Stage.PolicyID = "foreign"
			ref.StageModes[0].Snapshot.Version.Revision = 99
			*ref.StageModes[0].Snapshot.Settings.DefaultMode = sdk.PolicyModeEnforce
			*ref.StageModes[0].Snapshot.Settings.EntryMode = sdk.PolicyModeObserve
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			runtime := NewRuntime()
			input, want := fixture(1), fixture(1)
			if err := runtime.Apply(t.Context(), model.Snapshot{}, input); err != nil {
				t.Fatal(err)
			}
			mutate(input)
			if !reflect.DeepEqual(runtime.ActiveSnapshot(), want) {
				t.Fatal("Apply retained mutable caller policy state")
			}
			mutate(runtime.ActiveSnapshot())
			if !reflect.DeepEqual(runtime.ActiveSnapshot(), want) {
				t.Fatal("ActiveSnapshot exposed mutable policy state")
			}
			previous := runtime.ActiveSnapshot()
			if err := runtime.Apply(t.Context(), previous, fixture(2)); err != nil {
				t.Fatal(err)
			}
			mutate(previous)
			if !reflect.DeepEqual(runtime.ActiveSnapshot(), fixture(2)) {
				t.Fatal("old generation mutation corrupted the active update")
			}
			stable := runtime.ActiveSnapshot()
			mutate(cloneSnapshot(stable))
			if !reflect.DeepEqual(stable, fixture(2)) {
				t.Fatal("runtime clone retained policy aliases")
			}
			mutate(MergeSnapshotPayload(model.Snapshot{}, stable))
			if !reflect.DeepEqual(stable, fixture(2)) {
				t.Fatal("partial revision merge retained policy aliases")
			}
		})
	}
}
