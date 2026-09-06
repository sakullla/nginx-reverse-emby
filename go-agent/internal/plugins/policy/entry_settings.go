package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func resolveEntryOverlays(ref *model.PolicyRef, definition model.PluginPolicy) (sdk.PolicyOverlayEnvelope, error) {
	empty := sdk.PolicyOverlayEnvelope{Schema: sdk.PolicyOverlaySchemaV1, Stages: []sdk.PolicyStageOverlay{}}
	switch ref.OverlayFormat {
	case "":
		if ref.LegacyPolicyID != "" {
			return empty, errors.New("legacy overlay owner requires explicit format")
		}
	case sdk.PolicyOverlayFormatEnvelopeV1:
		if ref.LegacyPolicyID != "" {
			return empty, errors.New("envelope cannot name legacy owner")
		}
	case sdk.PolicyOverlayFormatLegacyWAF, sdk.PolicyOverlayFormatLegacyIP, sdk.PolicyOverlayFormatLegacyRate:
		if sdk.ValidatePolicyIdentity(ref.LegacyPolicyID) != nil {
			return empty, errors.New("legacy overlay owner required")
		}
	default:
		return empty, errors.New("unsupported overlay format")
	}
	if len(bytes.TrimSpace(ref.Overlay)) == 0 {
		return empty, nil
	}
	format, legacyID := ref.OverlayFormat, ref.LegacyPolicyID
	if format == "" {
		// Existing WAF-bearing chains historically admitted only mode-only WAF
		// overlays. Other nonempty legacy overlays need one unambiguous stage.
		var legacy *model.PolicyStage
		for i := range definition.Stages {
			stage := &definition.Stages[i]
			if stage.Kind == model.PolicyKindWAF {
				legacy = stage
				break
			}
		}
		if legacy == nil && len(definition.Stages) == 1 {
			legacy = &definition.Stages[0]
		}
		if legacy == nil {
			return empty, errors.New("overlay format/owner is required for a mixed chain")
		}
		legacyID = legacy.PolicyID
		format = "legacy-" + string(legacy.Kind)
	}
	envelope, err := sdk.DecodePolicyOverlay(ref.Overlay, sdk.PolicyOverlayDecodeContext{Format: format, LegacyPolicyID: legacyID})
	if err != nil {
		return empty, err
	}
	for _, overlay := range envelope.Stages {
		found := false
		for _, stage := range definition.Stages {
			if overlay.Kind == string(stage.Kind) && overlay.PolicyID == stage.PolicyID {
				found = true
			}
		}
		if !found {
			return empty, errors.New("overlay refers to a stage outside the effective chain")
		}
	}
	return envelope, nil
}
func stageModeProjection(stage model.PolicyStage, ref *model.PolicyRef) (sdk.PolicyStageModeProjection, error) {
	projection := sdk.PolicyStageModeProjection{Stage: sdk.PolicyStageIdentity{Kind: string(stage.Kind), PolicyID: stage.PolicyID}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingLegacy}}
	if stage.PolicySettings != nil {
		if stage.PolicySettings.Validate() != nil || stage.PolicySettings.Settings.EntryMode != nil {
			return projection, errors.New("invalid instance policy settings")
		}
		projection.Settings = model.ClonePolicySettings(stage.PolicySettings).Settings
	}
	seen := map[string]bool{}
	for _, binding := range ref.StageModes {
		key := binding.Stage.Kind
		if seen[key] || binding.Stage.Validate() != nil || binding.Snapshot.Validate() != nil {
			return projection, errors.New("invalid or duplicate entry mode binding")
		}
		seen[key] = true
		if binding.Stage.Kind != string(stage.Kind) {
			continue
		}
		if binding.Stage.PolicyID != stage.PolicyID || stage.PolicySettings == nil || binding.Snapshot.Settings.Handling != projection.Settings.Handling || !reflect.DeepEqual(binding.Snapshot.Settings.DefaultMode, projection.Settings.DefaultMode) || binding.Snapshot.Version.InstanceVersion != stage.PolicySettings.Version.InstanceVersion {
			return projection, errors.New("entry mode differs from current instance authority")
		}
		projection.Settings = model.ClonePolicySettings(&binding.Snapshot).Settings
	}
	return projection, projection.Validate()
}
func validateEntryModeBindings(ref *model.PolicyRef, definition model.PluginPolicy) error {
	if len(ref.StageModes) > 3 {
		return errors.New("entry policy modes exceed chain bound")
	}
	for _, binding := range ref.StageModes {
		found := false
		for _, stage := range definition.Stages {
			if binding.Stage.Kind == string(stage.Kind) && binding.Stage.PolicyID == stage.PolicyID {
				found = true
			}
		}
		if !found {
			return errors.New("entry mode names a foreign stage")
		}
	}
	for _, stage := range definition.Stages {
		if _, err := stageModeProjection(stage, ref); err != nil {
			return err
		}
	}
	return nil
}
func selectedStageOverlay(envelope sdk.PolicyOverlayEnvelope, projection sdk.PolicyStageModeProjection) (json.RawMessage, error) {
	return sdk.PolicyOverlayForMode(envelope, projection)
}
