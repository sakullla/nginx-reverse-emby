package pluginsdk

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// HostCapability is a signed manifest permission which can be projected into
// an administrator grant. Hosts must verify both sets on every call.
type HostCapability string

const (
	CapabilityPolicyAtomicState              HostCapability = "policy.atomic-state"
	CapabilityPolicyMonotonicClock           HostCapability = "policy.monotonic-clock"
	CapabilityPolicyTrustedSource            HostCapability = "policy.trusted-source"
	CapabilityServiceRevocableResourceHandle HostCapability = "service.revocable-resource-handle"
	CapabilityUIDynamicActions               HostCapability = "ui.dynamic-actions"
	CapabilityHTTPOutbound                   HostCapability = PermissionHTTPOutbound
	CapabilityHTTPRule                       HostCapability = "http.rule"
	CapabilityL4Rule                         HostCapability = "l4.rule"
	CapabilityChannelReverse                 HostCapability = "channel.reverse"
	CapabilityUIDynamic                      HostCapability = "ui.dynamic"
	CapabilityDatasetQuery                   HostCapability = "dataset.query"
	CapabilityDatasetResolve                 HostCapability = "dataset.resolve"
	CapabilityDatasetManage                  HostCapability = "dataset.manage"
)

var hostCapabilityIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

func (capability HostCapability) Validate() error {
	value := string(capability)
	if value != strings.TrimSpace(value) || !hostCapabilityIDPattern.MatchString(value) {
		return fmt.Errorf("host capability %q is not canonical", value)
	}
	switch capability {
	case CapabilityPolicyAtomicState, CapabilityPolicyMonotonicClock, CapabilityPolicyTrustedSource,
		CapabilityServiceRevocableResourceHandle, CapabilityUIDynamicActions, CapabilityHTTPOutbound,
		CapabilityHTTPRule, CapabilityL4Rule, CapabilityChannelReverse, CapabilityUIDynamic,
		CapabilityDatasetQuery, CapabilityDatasetResolve, CapabilityDatasetManage, CapabilityManagedNetworkListen, CapabilityManagedNetworkDial,
		CapabilityScopedSecretRead, CapabilityScopedSecretWrite:
		return nil
	default:
		return fmt.Errorf("host capability %q is not in the canonical catalog", value)
	}
}

// ValidateHostCapabilityGrant verifies the signed declaration and administrator
// grant separately. Resource scope, authenticated caller binding, revocation and
// quota remain Host-owned checks and cannot be inferred from these names.
func ValidateHostCapabilityGrant(capability HostCapability, declared, granted []string) error {
	if err := capability.Validate(); err != nil {
		return err
	}
	contains := func(scopes []string) bool {
		for _, scope := range scopes {
			if scope == string(capability) {
				return true
			}
		}
		return false
	}
	if !contains(declared) || !contains(granted) {
		return fmt.Errorf("host capability %q must be both declared and granted", capability)
	}
	return nil
}

type HostActor struct {
	ID              string
	ResourceGroupID string
}

type HostTarget struct {
	Kind            string
	ID              string
	ResourceGroupID string
}

type HostCapabilityCall struct {
	PluginID    string
	InstanceID  string
	Generation  string
	Capability  HostCapability
	Actor       HostActor
	Target      HostTarget
	QuotaMetric string
	QuotaUnits  int64
}

func (call HostCapabilityCall) Validate() error {
	if err := call.Capability.Validate(); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"plugin": call.PluginID, "instance": call.InstanceID, "generation": call.Generation,
		"actor": call.Actor.ID, "actor resource group": call.Actor.ResourceGroupID,
		"target kind": call.Target.Kind, "target": call.Target.ID, "target resource group": call.Target.ResourceGroupID,
	} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return fmt.Errorf("%s identity: %w", name, err)
		}
	}
	if call.Actor.ResourceGroupID != call.Target.ResourceGroupID {
		return errors.New("host actor and target resource groups differ")
	}
	if call.QuotaMetric != "" {
		if !hostCapabilityIDPattern.MatchString(call.QuotaMetric) || call.QuotaUnits <= 0 {
			return errors.New("host capability quota must use a canonical metric and positive units")
		}
	} else if call.QuotaUnits != 0 {
		return errors.New("host capability quota units require a metric")
	}
	return nil
}

type DynamicAction struct {
	ID         string
	Label      string
	Capability HostCapability
	TargetKind string
	Confirm    string
}

func (action DynamicAction) Validate() error {
	if err := ValidatePolicyIdentity(action.ID); err != nil {
		return fmt.Errorf("dynamic action id: %w", err)
	}
	if strings.TrimSpace(action.Label) == "" || len(action.Label) > 128 {
		return errors.New("dynamic action label is missing or too large")
	}
	if err := action.Capability.Validate(); err != nil {
		return err
	}
	if action.Capability == CapabilityUIDynamicActions || action.Capability == CapabilityUIDynamic {
		return errors.New("dynamic actions cannot recursively invoke ui.dynamic-actions")
	}
	if !hostCapabilityIDPattern.MatchString(action.TargetKind) {
		return errors.New("dynamic action target kind is not canonical")
	}
	if len(action.Confirm) > 512 || strings.ContainsAny(action.Confirm, "\r\n\x00") {
		return errors.New("dynamic action confirmation is not safe host text")
	}
	return nil
}
