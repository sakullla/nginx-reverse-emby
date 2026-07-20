package service

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// These guards are temporary while retired rows remain readable. T6 removes
// them together with the retired storage and snapshot fields.
func retiredRuntimeSnapshot(ctx context.Context, store *storage.GormStore, _ revision.Target, snapshot storage.Snapshot) (storage.Snapshot, error) {
	retiredRelayIDs := make(map[int]struct{})
	retiredEgressIDs := make(map[int]struct{})
	if store != nil {
		listeners, err := store.ListRelayListeners(ctx, "")
		if err != nil {
			return storage.Snapshot{}, err
		}
		for _, listener := range listeners {
			if strings.EqualFold(strings.TrimSpace(listener.TransportMode), "wireguard") || listener.WireGuardProfileID != nil {
				retiredRelayIDs[listener.ID] = struct{}{}
			}
		}
		profiles, err := store.ListEgressProfiles(ctx)
		if err != nil {
			return storage.Snapshot{}, err
		}
		for _, profile := range profiles {
			if strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
				retiredEgressIDs[profile.ID] = struct{}{}
			}
		}
	}

	relayListeners := make([]storage.RelayListener, 0, len(snapshot.RelayListeners))
	for _, listener := range snapshot.RelayListeners {
		if retiredRelayListener(listener) {
			retiredRelayIDs[listener.ID] = struct{}{}
			continue
		}
		relayListeners = append(relayListeners, listener)
	}
	egressProfiles := make([]storage.EgressProfile, 0, len(snapshot.EgressProfiles))
	for _, profile := range snapshot.EgressProfiles {
		if retiredEgressProfile(profile) {
			retiredEgressIDs[profile.ID] = struct{}{}
			continue
		}
		egressProfiles = append(egressProfiles, profile)
	}

	rules := make([]storage.HTTPRule, 0, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		if retiredHTTPRule(rule) || referencesRetiredRelay(rule.RelayLayers, retiredRelayIDs) || referencesRetiredEgress(rule.EgressProfileID, retiredEgressIDs) {
			continue
		}
		rules = append(rules, rule)
	}
	l4Rules := make([]storage.L4Rule, 0, len(snapshot.L4Rules))
	for _, rule := range snapshot.L4Rules {
		if retiredL4Rule(rule) || referencesRetiredRelay(rule.RelayLayers, retiredRelayIDs) || referencesRetiredEgress(rule.EgressProfileID, retiredEgressIDs) {
			continue
		}
		l4Rules = append(l4Rules, rule)
	}

	filtered := snapshot
	filtered.Rules = rules
	filtered.L4Rules = l4Rules
	filtered.RelayListeners = relayListeners
	filtered.WireGuardProfiles = []storage.WireGuardProfile{}
	filtered.EgressProfiles = egressProfiles
	return filtered, nil
}

func retiredHTTPRule(rule storage.HTTPRule) bool {
	return rule.WireGuardEntryEnabled || rule.WireGuardProfileID != nil
}

func retiredL4Rule(rule storage.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") || rule.WireGuardProfileID != nil
}

func retiredRelayListener(listener storage.RelayListener) bool {
	return strings.EqualFold(strings.TrimSpace(listener.TransportMode), "wireguard") || listener.WireGuardProfileID != nil
}

func retiredEgressProfile(profile storage.EgressProfile) bool {
	return strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard")
}

func referencesRetiredRelay(layers [][]int, retiredIDs map[int]struct{}) bool {
	for _, layer := range layers {
		for _, id := range layer {
			if _, retired := retiredIDs[id]; retired {
				return true
			}
		}
	}
	return false
}

func referencesRetiredEgress(profileID *int, retiredIDs map[int]struct{}) bool {
	if profileID == nil {
		return false
	}
	_, retired := retiredIDs[*profileID]
	return retired
}

type retiredReferenceValidator struct{}

func (retiredReferenceValidator) ValidateMutation(ctx context.Context, store *storage.GormStore, input revision.MutationValidation) error {
	relayIDs, egressIDs := requestedSharedResourceIDs(input.Request)
	if len(relayIDs) > 0 {
		listeners, err := store.ListRelayListeners(ctx, "")
		if err != nil {
			return err
		}
		retiredIDs := make(map[int]struct{})
		for _, listener := range listeners {
			if strings.EqualFold(strings.TrimSpace(listener.TransportMode), "wireguard") || listener.WireGuardProfileID != nil {
				retiredIDs[listener.ID] = struct{}{}
			}
		}
		for _, id := range relayIDs {
			if _, retired := retiredIDs[id]; retired {
				return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("relay listener %d was not found", id), nil)
			}
		}
	}
	if len(egressIDs) > 0 {
		profiles, err := store.ListEgressProfiles(ctx)
		if err != nil {
			return err
		}
		retiredIDs := make(map[int]struct{})
		for _, profile := range profiles {
			if strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
				retiredIDs[profile.ID] = struct{}{}
			}
		}
		for _, id := range egressIDs {
			if _, retired := retiredIDs[id]; retired {
				return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("egress profile %d was not found", id), nil)
			}
		}
	}
	return nil
}

func requestedSharedResourceIDs(request any) ([]int, []int) {
	for request != nil {
		switch value := request.(type) {
		case HTTPRuleInput:
			return httpRuleInputSharedResourceIDs(value)
		case *HTTPRuleInput:
			if value != nil {
				return httpRuleInputSharedResourceIDs(*value)
			}
			return nil, nil
		case L4RuleInput:
			return l4RuleInputSharedResourceIDs(value)
		case *L4RuleInput:
			if value != nil {
				return l4RuleInputSharedResourceIDs(*value)
			}
			return nil, nil
		}

		value := reflect.ValueOf(request)
		for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
			if value.IsNil() {
				return nil, nil
			}
			value = value.Elem()
		}
		if !value.IsValid() || value.Kind() != reflect.Struct {
			return nil, nil
		}
		input := value.FieldByName("Input")
		if !input.IsValid() || !input.CanInterface() {
			return nil, nil
		}
		request = input.Interface()
	}
	return nil, nil
}

func httpRuleInputSharedResourceIDs(input HTTPRuleInput) ([]int, []int) {
	return requestedRelayIDs(input.RelayChain, input.RelayLayers), requestedEgressIDs(input.EgressProfileID)
}

func l4RuleInputSharedResourceIDs(input L4RuleInput) ([]int, []int) {
	return requestedRelayIDs(input.RelayChain, input.RelayLayers), requestedEgressIDs(input.EgressProfileID)
}

func requestedRelayIDs(chain *[]int, layers *[][]int) []int {
	ids := make([]int, 0)
	if chain != nil {
		ids = append(ids, (*chain)...)
	}
	if layers != nil {
		for _, layer := range *layers {
			ids = append(ids, layer...)
		}
	}
	return positiveUniqueIDs(ids)
}

func requestedEgressIDs(profileID *int) []int {
	if profileID == nil || *profileID <= 0 {
		return nil
	}
	return []int{*profileID}
}

func positiveUniqueIDs(ids []int) []int {
	result := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
