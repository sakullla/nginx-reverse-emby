package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func ClonePolicyRef(ref *PolicyRef) *PolicyRef {
	if ref == nil {
		return nil
	}
	data, _ := json.Marshal(ref)
	var cloned PolicyRef
	_ = json.Unmarshal(data, &cloned)
	return &cloned
}
func cloneEntryStage(stage PolicyStage) PolicyStage {
	data, _ := json.Marshal(stage)
	var copy PolicyStage
	_ = json.Unmarshal(data, &copy)
	return copy
}

// ComposeEntryPolicy is a pure effective projection except for reading trusted
// entry mode records. Persisted PolicyRef and guest Config remain untouched.
// Invalid duplicate IP stages fail the candidate, preserving the prior snapshot.
func (s *GormStore) ComposeEntryPolicy(ctx context.Context, entry sdk.PolicyEntryTarget, original *PolicyRef, catalog []PluginPolicy) (*PolicyRef, *PluginPolicy, error) {
	if entry.Validate() != nil {
		return nil, nil, errors.New("entry identity is invalid")
	}
	stages := map[string]PolicyStage{}
	var explicit []PolicyStage
	if original != nil && original.ID != "" {
		found := false
		for _, policy := range catalog {
			if policy.ID == original.ID {
				if found {
					return nil, nil, errors.New("ambiguous explicit policy identity")
				}
				found = true
				explicit = policy.Stages
			}
		}
		if !found {
			// Existing references retain their legacy lifecycle behavior until a
			// new default IP actually requires a complete composed candidate.
			needsComposition := false
			for _, policy := range catalog {
				for _, stage := range policy.Stages {
					if stage.Automatic && stage.Kind == "ip" {
						needsComposition = true
					}
				}
			}
			if !needsComposition {
				return ClonePolicyRef(original), nil, nil
			}
			return nil, nil, fmt.Errorf("policy-composition-conflict: explicit policy %q is unavailable", original.ID)
		}
		for _, stage := range explicit {
			if stage.Kind == "waf" && entry.Kind != sdk.PolicyEntryHTTP {
				return nil, nil, errors.New("WAF is only applicable to HTTP")
			}
			if previous, exists := stages[stage.Kind]; exists && previous.PolicyID != stage.PolicyID {
				return nil, nil, errors.New("policy-composition-conflict: duplicate explicit stage")
			}
			stages[stage.Kind] = stage
		}
	}
	defaults := map[string]PolicyStage{}
	for _, policy := range catalog {
		for _, stage := range policy.Stages {
			if !stage.Automatic || (stage.Kind == "waf" && entry.Kind != sdk.PolicyEntryHTTP) {
				continue
			}
			if old, exists := defaults[stage.Kind]; exists && old.PolicyID != stage.PolicyID {
				if stage.Kind == "ip" {
					return nil, nil, errors.New("policy-composition-conflict: multiple default IP instances")
				}
				if old.PolicyID < stage.PolicyID {
					continue
				}
			}
			defaults[stage.Kind] = stage
		}
	}
	for kind, stage := range defaults {
		if prior, exists := stages[kind]; exists {
			if prior.PolicyID != stage.PolicyID && kind == "ip" {
				return nil, nil, fmt.Errorf("policy-composition-conflict: independent IP %q conflicts with default %q", prior.PolicyID, stage.PolicyID)
			}
			continue
		}
		if kind == "ip" && stage.PolicySettings == nil {
			return nil, nil, errors.New("default IP requires explicit typed settings")
		}
		stages[kind] = stage
	}
	if len(stages) == 0 {
		return nil, nil, nil
	}
	typed := false
	for _, stage := range stages {
		typed = typed || stage.PolicySettings != nil
	}
	if original != nil && len(stages) == len(explicit) && !typed && original.OverlayFormat == "" {
		return ClonePolicyRef(original), nil, nil
	}
	if original == nil && len(stages) == 1 && !typed {
		if waf, ok := stages["waf"]; ok {
			return &PolicyRef{ID: waf.PolicyID, Overlay: json.RawMessage(`{"mode":"observe"}`)}, nil, nil
		}
	}
	envelope := sdk.PolicyOverlayEnvelope{Schema: sdk.PolicyOverlaySchemaV1, Stages: []sdk.PolicyStageOverlay{}}
	if original != nil && len(original.Overlay) > 0 {
		format, owner := original.OverlayFormat, original.LegacyPolicyID
		if format == "" {
			// Old Host rule writers only emitted mode-only WAF overlays, or a
			// single explicitly owned stage payload. This is stored provenance,
			// not sniffing schema/mode fields in untrusted traffic.
			var legacy *PolicyStage
			if len(explicit) == 1 {
				legacy = &explicit[0]
			} else {
				for i := range explicit {
					if explicit[i].Kind == "waf" {
						legacy = &explicit[i]
					}
				}
			}
			if legacy == nil {
				return nil, nil, errors.New("legacy shared overlay owner is ambiguous")
			}
			owner = legacy.PolicyID
			switch legacy.Kind {
			case "ip":
				format = sdk.PolicyOverlayFormatLegacyIP
			case "rate":
				format = sdk.PolicyOverlayFormatLegacyRate
			case "waf":
				format = sdk.PolicyOverlayFormatLegacyWAF
			}
		}
		var err error
		envelope, err = sdk.DecodePolicyOverlay(original.Overlay, sdk.PolicyOverlayDecodeContext{Format: format, LegacyPolicyID: owner})
		if err != nil {
			return nil, nil, err
		}
		for _, overlay := range envelope.Stages {
			stage, exists := stages[overlay.Kind]
			if !exists || stage.PolicyID != overlay.PolicyID {
				return nil, nil, errors.New("overlay belongs to an unrelated stage")
			}
		}
	}
	if original == nil && defaults["waf"].PolicyID != "" && defaults["waf"].PolicySettings == nil {
		envelope.Stages = append(envelope.Stages, sdk.PolicyStageOverlay{Kind: "waf", PolicyID: defaults["waf"].PolicyID, Payload: json.RawMessage(`{"mode":"observe"}`)})
	}
	policy := &PluginPolicy{}
	ref := &PolicyRef{OverlayFormat: sdk.PolicyOverlayFormatEnvelopeV1}
	identities := []sdk.PolicyStageIdentity{}
	for _, kind := range []string{"ip", "rate", "waf"} {
		stage, exists := stages[kind]
		if !exists {
			continue
		}
		stage = cloneEntryStage(stage)
		policy.Stages = append(policy.Stages, stage)
		identities = append(identities, sdk.PolicyStageIdentity{Kind: kind, PolicyID: stage.PolicyID})
		if stage.PolicySettings != nil {
			data, _ := json.Marshal(stage.PolicySettings)
			var selected sdk.PolicySettingsSnapshot
			_ = json.Unmarshal(data, &selected)
			overrides, err := s.ListPluginPolicyEntryModes(ctx, stage.InstanceID)
			if err != nil {
				return nil, nil, err
			}
			for _, override := range overrides {
				if override.NodeID == entry.NodeID && override.Kind == entry.Kind && override.EntryID == entry.ID {
					mode := sdk.PolicyMode(override.Mode)
					selected.Settings.EntryMode = &mode
				}
			}
			if err := selected.Validate(); err != nil {
				return nil, nil, err
			}
			ref.StageModes = append(ref.StageModes, PolicyModeBinding{Stage: sdk.PolicyStageIdentity{Kind: kind, PolicyID: stage.PolicyID}, Snapshot: selected})
		}
	}
	encoded, _ := json.Marshal(identities)
	sum := sha256.Sum256(encoded)
	policy.ID = "effective-" + hex.EncodeToString(sum[:16])
	ref.ID = policy.ID
	for _, item := range catalog {
		if item.Revision > policy.Revision {
			policy.Revision = item.Revision
		}
	}
	ref.Overlay, _ = json.Marshal(envelope)
	return ref, policy, nil
}

func (s *GormStore) composeSnapshotEntryPolicies(ctx context.Context, agent string, catalog []PluginPolicy, rules []HTTPRule, l4 []L4Rule, generations []PluginGeneration) ([]PluginPolicy, error) {
	result := append([]PluginPolicy(nil), catalog...)
	known := map[string]bool{}
	for _, policy := range catalog {
		known[policy.ID] = true
	}
	compose := func(entry sdk.PolicyEntryTarget, original *PolicyRef) (*PolicyRef, error) {
		ref, policy, err := s.ComposeEntryPolicy(ctx, entry, original, catalog)
		if err != nil {
			return nil, err
		}
		if policy != nil && !known[policy.ID] {
			known[policy.ID] = true
			result = append(result, *policy)
		}
		return ref, nil
	}
	for i := range rules {
		ref, err := compose(sdk.PolicyEntryTarget{NodeID: agent, Kind: sdk.PolicyEntryHTTP, ID: strconv.Itoa(rules[i].ID)}, rules[i].PolicyRef)
		if err != nil {
			return nil, err
		}
		rules[i].PolicyRef = ref
	}
	for i := range l4 {
		kind := sdk.PolicyEntryTCP
		if l4[i].Protocol == "udp" {
			kind = sdk.PolicyEntryUDP
		}
		ref, err := compose(sdk.PolicyEntryTarget{NodeID: agent, Kind: kind, ID: strconv.Itoa(l4[i].ID)}, l4[i].PolicyRef)
		if err != nil {
			return nil, err
		}
		l4[i].PolicyRef = ref
	}
	for i := range generations {
		managed := false
		for _, grant := range generations[i].Grants {
			if grant.Name == sdk.PermissionManagedNetworkListen {
				managed = true
			}
		}
		if !managed {
			continue
		}
		instance, found, err := s.GetPluginInstance(ctx, generations[i].InstanceID)
		if err != nil || !found {
			return nil, ErrPluginNotInstalled
		}
		chains, err := CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil {
			return nil, err
		}
		if len(chains) > 1 {
			return nil, errors.New("managed entry accepts at most one explicit policy chain")
		}
		var original *PolicyRef
		if len(chains) == 1 {
			original = &PolicyRef{ID: chains[0]}
		}
		generations[i].ManagedNetworkPolicies = map[string]*PolicyRef{}
		for protocol, kind := range map[string]string{"tcp": sdk.PolicyEntryManagedTCP, "udp": sdk.PolicyEntryManagedUDP} {
			ref, err := compose(sdk.PolicyEntryTarget{NodeID: agent, Kind: kind, ID: instance.ID}, original)
			if err != nil {
				return nil, err
			}
			if ref != nil {
				generations[i].ManagedNetworkPolicies[protocol] = ref
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
