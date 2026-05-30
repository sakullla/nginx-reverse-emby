package core

import (
	"context"
	"reflect"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
)

type SnapshotActivationHandlers struct {
	ActivateAgentConfig         func(context.Context, model.AgentConfig) error
	ActivateManagedCertificates func(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
	ActivateHTTPRules           func(context.Context, SnapshotHTTPInput) error
	ActivateL4Rules             func(context.Context, SnapshotL4Input) error
	ActivateRelayListeners      func(context.Context, SnapshotRelayInput) error
}

type SnapshotHTTPInput struct {
	Rules             []model.HTTPRule
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

type SnapshotL4Input struct {
	Rules             []model.L4Rule
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

type SnapshotRelayInput struct {
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

func NewSnapshotActivator(agentID string, agentName string, handlers SnapshotActivationHandlers) agentruntime.Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if certificatesChanged(previous, next) && handlers.ActivateManagedCertificates != nil {
			if err := handlers.ActivateManagedCertificates(ctx, next.Certificates, next.CertificatePolicies); err != nil {
				return err
			}
		}

		if agentConfigChanged(previous, next) && handlers.ActivateAgentConfig != nil {
			if err := handlers.ActivateAgentConfig(ctx, next.AgentConfig); err != nil {
				return err
			}
		}

		if httpActivationNeeded(previous, next) && handlers.ActivateHTTPRules != nil {
			if err := handlers.ActivateHTTPRules(ctx, SnapshotHTTPInput{
				Rules:             next.Rules,
				RelayListeners:    next.RelayListeners,
				WireGuardProfiles: next.WireGuardProfiles,
				EgressProfiles:    next.EgressProfiles,
			}); err != nil {
				return err
			}
		}

		if l4ActivationNeeded(previous, next) && handlers.ActivateL4Rules != nil {
			if err := handlers.ActivateL4Rules(ctx, SnapshotL4Input{
				Rules:             next.L4Rules,
				RelayListeners:    next.RelayListeners,
				WireGuardProfiles: next.WireGuardProfiles,
				EgressProfiles:    next.EgressProfiles,
			}); err != nil {
				return err
			}
		}

		localPrevious := previous
		localPrevious.RelayListeners = localRelayListeners(previous.RelayListeners, agentID, agentName)
		localNext := next
		localNext.RelayListeners = localRelayListeners(next.RelayListeners, agentID, agentName)
		if relayActivationNeeded(localPrevious, localNext, previous, next) && handlers.ActivateRelayListeners != nil {
			if err := handlers.ActivateRelayListeners(ctx, SnapshotRelayInput{
				RelayListeners:    localNext.RelayListeners,
				WireGuardProfiles: next.WireGuardProfiles,
				EgressProfiles:    next.EgressProfiles,
			}); err != nil {
				return err
			}
		}

		return nil
	}
}

func certificatesChanged(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.Certificates, next.Certificates) ||
		!reflect.DeepEqual(previous.CertificatePolicies, next.CertificatePolicies)
}

func agentConfigChanged(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.AgentConfig, next.AgentConfig)
}

func httpActivationNeeded(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.Rules, next.Rules) ||
		httpRelayInputsChanged(next.Rules, previous.RelayListeners, next.RelayListeners) ||
		httpWireGuardInputsChanged(next.Rules, previous.WireGuardProfiles, next.WireGuardProfiles) ||
		httpEgressInputsChanged(next.Rules, previous.EgressProfiles, next.EgressProfiles)
}

func l4ActivationNeeded(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.L4Rules, next.L4Rules) ||
		l4.RelayInputsChanged(next.L4Rules, previous.RelayListeners, next.RelayListeners) ||
		l4WireGuardInputsChanged(next.L4Rules, previous.WireGuardProfiles, next.WireGuardProfiles) ||
		l4EgressInputsChanged(next.L4Rules, previous.EgressProfiles, next.EgressProfiles)
}

func relayActivationNeeded(localPrevious, localNext, previous, next model.Snapshot) bool {
	return relay.ListenersChanged(localPrevious.RelayListeners, localNext.RelayListeners) ||
		!reflect.DeepEqual(previous.WireGuardProfiles, next.WireGuardProfiles) ||
		(len(localNext.RelayListeners) > 0 && !reflect.DeepEqual(previous.EgressProfiles, next.EgressProfiles))
}

func l4WireGuardInputsChanged(rules []model.L4Rule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if !l4RuleUsesWireGuard(rule) {
			continue
		}
		return !reflect.DeepEqual(previousProfiles, nextProfiles)
	}
	return false
}

func l4EgressInputsChanged(rules []model.L4Rule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
			continue
		}
		return !reflect.DeepEqual(previousProfiles, nextProfiles)
	}
	return false
}

func httpWireGuardInputsChanged(rules []model.HTTPRule, previousProfiles, nextProfiles []model.WireGuardProfile) bool {
	for _, rule := range rules {
		if rule.WireGuardEntryEnabled {
			return !reflect.DeepEqual(previousProfiles, nextProfiles)
		}
	}
	return false
}

func httpEgressInputsChanged(rules []model.HTTPRule, previousProfiles, nextProfiles []model.EgressProfile) bool {
	for _, rule := range rules {
		if rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
			continue
		}
		return !reflect.DeepEqual(previousProfiles, nextProfiles)
	}
	return false
}

func httpRelayInputsChanged(rules []model.HTTPRule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		for _, layer := range rule.RelayLayers {
			for _, listenerID := range layer {
				if relayListenerChangedByID(listenerID, previousRelayListeners, nextRelayListeners) {
					return true
				}
			}
		}
	}
	return false
}

func relayListenerChangedByID(listenerID int, previous, next []model.RelayListener) bool {
	previousListener, previousOK := relayListenerByID(listenerID, previous)
	nextListener, nextOK := relayListenerByID(listenerID, next)
	if previousOK != nextOK {
		return true
	}
	if !previousOK {
		return false
	}
	return !reflect.DeepEqual(previousListener, nextListener)
}

func relayListenerByID(listenerID int, listeners []model.RelayListener) (model.RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == listenerID {
			return listener, true
		}
	}
	return model.RelayListener{}, false
}

func l4RuleUsesWireGuard(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard")
}

func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	identity := strings.TrimSpace(agentID)
	fallback := strings.TrimSpace(agentName)
	if identity == "" && fallback == "" {
		return listeners
	}
	filtered := make([]model.RelayListener, 0, len(listeners))
	for _, listener := range listeners {
		if listener.AgentID == identity || (identity == "" && listener.AgentID == fallback) || listener.AgentID == fallback {
			filtered = append(filtered, listener)
		}
	}
	return filtered
}
