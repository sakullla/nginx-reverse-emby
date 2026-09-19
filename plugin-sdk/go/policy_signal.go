package pluginsdk

import (
	"errors"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type PolicySecurityEventCode int32

const (
	PolicySecurityEventCodeUnspecified      PolicySecurityEventCode = 0
	PolicySecurityEventCodeWAFRuleMatch     PolicySecurityEventCode = 1
	PolicySecurityEventCodeIPRuleMatch      PolicySecurityEventCode = 2
	PolicySecurityEventCodeIPCheckFailure   PolicySecurityEventCode = 3
	PolicySecurityEventCodeRoutingRuleMatch PolicySecurityEventCode = 4
	PolicySecurityEventCodeRoutingFailure   PolicySecurityEventCode = 5
)

type PolicySecurityEventAction int32

const (
	PolicySecurityEventActionUnspecified PolicySecurityEventAction = 0
	PolicySecurityEventActionObserve     PolicySecurityEventAction = 1
	PolicySecurityEventActionDeny        PolicySecurityEventAction = 2
	PolicySecurityEventActionAllow       PolicySecurityEventAction = 3
	PolicySecurityEventActionDirect      PolicySecurityEventAction = 4
	PolicySecurityEventActionUpstream    PolicySecurityEventAction = 5
)

type PolicySecurityEventReason int32

const (
	PolicySecurityEventReasonNone PolicySecurityEventReason = iota
	PolicySecurityEventReasonSourceUnauthenticated
	PolicySecurityEventReasonDatasetUnavailable
	PolicySecurityEventReasonClassificationMissing
	PolicySecurityEventReasonBudgetExceeded
	PolicySecurityEventReasonUpstreamUnavailable
	PolicySecurityEventReasonUpstreamAuthentication
	PolicySecurityEventReasonProtocolUnsupported
	PolicySecurityEventReasonDataInvalid
	PolicySecurityEventReasonCoverageUnknown
	PolicySecurityEventReasonRevoked
)

type PolicyDomainSource int32

const (
	PolicyDomainSourceUnspecified PolicyDomainSource = iota
	PolicyDomainSourceOriginal
	PolicyDomainSourceHTTPHost
	PolicyDomainSourceTLSSNI
	PolicyDomainSourceIPOnly
	PolicyDomainSourceUnavailable
)

// PolicySecurityEvent contains only fixed-catalog enum values. Its printable
// code, action, and template are all host-owned and cannot transport request
// fields, bodies, credentials, or arbitrary guest bytes.
type PolicySecurityEvent struct {
	Code   PolicySecurityEventCode
	Action PolicySecurityEventAction
	// One-based references into Host-pinned dictionaries. Zero is absent.
	// Host must check actual dictionary membership before rendering names.
	RuleIndex           uint32
	DatasetIndex        uint32
	ClassificationIndex uint32
	OutboundIndex       uint32
	Reason              PolicySecurityEventReason
	DomainSource        PolicyDomainSource
}

func PolicySecurityEventFromWire(code, action int32) (PolicySecurityEvent, error) {
	event := PolicySecurityEvent{Code: PolicySecurityEventCode(code), Action: PolicySecurityEventAction(action)}
	if err := event.Validate(); err != nil {
		return PolicySecurityEvent{}, err
	}
	return event, nil
}

func (event PolicySecurityEvent) Validate() error {
	if event.Reason < PolicySecurityEventReasonNone || event.Reason > PolicySecurityEventReasonRevoked || event.DomainSource < PolicyDomainSourceUnspecified || event.DomainSource > PolicyDomainSourceUnavailable {
		return errors.New("policy event reason or domain source is unknown")
	}
	for _, index := range []uint32{event.RuleIndex, event.DatasetIndex, event.ClassificationIndex, event.OutboundIndex} {
		if index > 65535 {
			return errors.New("policy event dictionary reference exceeds bound")
		}
	}
	if event.ClassificationIndex != 0 && event.DatasetIndex == 0 {
		return errors.New("policy event classification requires a dataset reference")
	}
	switch event.Code {
	case PolicySecurityEventCodeWAFRuleMatch:
		if event.Action != PolicySecurityEventActionObserve && event.Action != PolicySecurityEventActionDeny {
			return errors.New("invalid WAF event action")
		}
		if event.Reason != 0 || event.DatasetIndex != 0 || event.ClassificationIndex != 0 || event.OutboundIndex != 0 || event.DomainSource != 0 {
			return errors.New("WAF event cannot carry unrelated routing or failure metadata")
		}
	case PolicySecurityEventCodeIPRuleMatch:
		if event.Action != PolicySecurityEventActionObserve && event.Action != PolicySecurityEventActionDeny && event.Action != PolicySecurityEventActionAllow {
			return errors.New("invalid IP event action")
		}
		if event.OutboundIndex != 0 || event.DomainSource != 0 || (event.Reason != 0 && event.Reason != PolicySecurityEventReasonCoverageUnknown) {
			return errors.New("invalid IP match metadata")
		}
	case PolicySecurityEventCodeIPCheckFailure:
		if event.Action != PolicySecurityEventActionObserve && event.Action != PolicySecurityEventActionDeny {
			return errors.New("IP check failure must observe an anomaly or deny")
		}
		if event.Reason == 0 || event.OutboundIndex != 0 || event.DomainSource != 0 {
			return errors.New("IP check failure requires a stable reason without routing metadata")
		}
	case PolicySecurityEventCodeRoutingRuleMatch:
		if event.Action != PolicySecurityEventActionDeny && event.Action != PolicySecurityEventActionDirect && event.Action != PolicySecurityEventActionUpstream {
			return errors.New("invalid routing match action")
		}
		if event.Reason != 0 || (event.Action == PolicySecurityEventActionUpstream) != (event.OutboundIndex != 0) {
			return errors.New("routing match must identify only its selected upstream")
		}
	case PolicySecurityEventCodeRoutingFailure:
		if event.Action != PolicySecurityEventActionDeny || event.Reason == 0 {
			return errors.New("routing failure must deny with a stable reason")
		}
	default:
		return errors.New("policy security event code is unknown")
	}
	return nil
}

// ValidateForCatalog resolves only indices valid in the actual immutable
// configuration dictionaries. ClassificationCount belongs to DatasetIndex;
// Hosts obtain it after resolving that dataset, never from guest assertions.
func (event PolicySecurityEvent) ValidateForCatalog(ruleCount, datasetCount, classificationCount, outboundCount uint32) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.RuleIndex > ruleCount || event.DatasetIndex > datasetCount || event.ClassificationIndex > classificationCount || event.OutboundIndex > outboundCount {
		return errors.New("policy event references a missing dictionary entry")
	}
	return nil
}

func (code PolicySecurityEventCode) String() string {
	switch code {
	case PolicySecurityEventCodeWAFRuleMatch:
		return "waf.rule_match"
	case PolicySecurityEventCodeIPRuleMatch:
		return "ip.rule_match"
	case PolicySecurityEventCodeIPCheckFailure:
		return "ip.check_failure"
	case PolicySecurityEventCodeRoutingRuleMatch:
		return "routing.rule_match"
	case PolicySecurityEventCodeRoutingFailure:
		return "routing.failure"
	}
	return "unknown"
}

func (action PolicySecurityEventAction) String() string {
	switch action {
	case PolicySecurityEventActionObserve:
		return "observe"
	case PolicySecurityEventActionDeny:
		return "deny"
	case PolicySecurityEventActionAllow:
		return "allow"
	case PolicySecurityEventActionDirect:
		return "direct"
	case PolicySecurityEventActionUpstream:
		return "upstream"
	default:
		return "unknown"
	}
}

func (event PolicySecurityEvent) Template() string {
	switch event.Code {
	case PolicySecurityEventCodeWAFRuleMatch:
		return "WAF rule matched"
	case PolicySecurityEventCodeIPRuleMatch:
		return "IP rule matched"
	case PolicySecurityEventCodeIPCheckFailure:
		return "IP check failed"
	case PolicySecurityEventCodeRoutingRuleMatch:
		return "Routing rule matched"
	case PolicySecurityEventCodeRoutingFailure:
		return "Routing failed"
	}
	return "Policy security event"
}

func (reason PolicySecurityEventReason) String() string {
	names := [...]string{"none", "source-unauthenticated", "dataset-unavailable", "classification-missing", "budget-exceeded", "upstream-unavailable", "upstream-authentication", "protocol-unsupported", "data-invalid", "coverage-unknown", "revoked"}
	if reason < 0 || int(reason) >= len(names) {
		return "unknown"
	}
	return names[reason]
}

func (source PolicyDomainSource) String() string {
	names := [...]string{"unspecified", "original", "http-host", "tls-sni", "ip-only", "unavailable"}
	if source < 0 || int(source) >= len(names) {
		return "unknown"
	}
	return names[source]
}

func MarshalPolicySecurityEvent(event PolicySecurityEvent, inputFrameBytes int) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("EmitEventRequest")
	if err != nil {
		return nil, err
	}
	for name, value := range map[protoreflect.Name]int32{"code": int32(event.Code), "action": int32(event.Action), "reason": int32(event.Reason), "domain_source": int32(event.DomainSource)} {
		policyExtensionSet(message, name, protoreflect.ValueOfEnum(protoreflect.EnumNumber(value)))
	}
	for name, value := range map[protoreflect.Name]uint32{"rule_index": event.RuleIndex, "dataset_index": event.DatasetIndex, "classification_index": event.ClassificationIndex, "outbound_index": event.OutboundIndex} {
		policyExtensionSet(message, name, protoreflect.ValueOfUint32(value))
	}
	return marshalPolicyExtension(message, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
}

func UnmarshalPolicySecurityEvent(frame []byte, inputFrameBytes int) (PolicySecurityEvent, error) {
	message, err := unmarshalPolicyExtension("EmitEventRequest", frame, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
	if err != nil {
		return PolicySecurityEvent{}, err
	}
	event := PolicySecurityEvent{
		Code: PolicySecurityEventCode(policyExtensionGet(message, "code").Enum()), Action: PolicySecurityEventAction(policyExtensionGet(message, "action").Enum()),
		RuleIndex: uint32(policyExtensionGet(message, "rule_index").Uint()), DatasetIndex: uint32(policyExtensionGet(message, "dataset_index").Uint()), ClassificationIndex: uint32(policyExtensionGet(message, "classification_index").Uint()), OutboundIndex: uint32(policyExtensionGet(message, "outbound_index").Uint()),
		Reason: PolicySecurityEventReason(policyExtensionGet(message, "reason").Enum()), DomainSource: PolicyDomainSource(policyExtensionGet(message, "domain_source").Enum()),
	}
	if err := event.Validate(); err != nil {
		return PolicySecurityEvent{}, err
	}
	return event, nil
}
