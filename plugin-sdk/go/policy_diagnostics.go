package pluginsdk

import (
	"errors"
	"net/netip"
)

// PolicyDiagnosticContext is a Host-owned administrative event projection.
// Resolve guest event indices against the pinned configuration and supplement
// them with authenticated connection/node facts. Never copy guest labels or
// request headers, bodies, credentials or traffic payloads into this context.
type PolicyDiagnosticContext struct {
	InstanceID     string                    `json:"instance_id"`
	EntryID        string                    `json:"entry_id"`
	NodeID         string                    `json:"node_id"`
	Generation     string                    `json:"generation"`
	SourceAddress  string                    `json:"source_address,omitempty"`
	RuleID         string                    `json:"rule_id,omitempty"`
	DatasetVersion string                    `json:"dataset_version,omitempty"`
	Country        string                    `json:"country,omitempty"`
	Region         string                    `json:"region,omitempty"`
	OutboundID     string                    `json:"outbound_id,omitempty"`
	Mode           string                    `json:"mode"`
	DomainSource   PolicyDomainSource        `json:"domain_source,omitempty"`
	Reason         PolicySecurityEventReason `json:"reason,omitempty"`
}

func (event PolicyDiagnosticContext) Validate() error {
	for _, value := range []string{event.InstanceID, event.EntryID, event.NodeID, event.Generation} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return err
		}
	}
	for _, value := range []string{event.RuleID, event.OutboundID} {
		if value != "" {
			if err := ValidatePolicyIdentity(value); err != nil {
				return err
			}
		}
	}
	if event.SourceAddress != "" {
		address, err := netip.ParseAddr(event.SourceAddress)
		if err != nil || address.Zone() != "" || address.Is4In6() || address.String() != event.SourceAddress {
			return errors.New("invalid diagnostic source address")
		}
	}
	if event.DatasetVersion != "" {
		if err := validateDatasetDigest(event.DatasetVersion); err != nil {
			return err
		}
	}
	if event.Country != "" && (len(event.Country) != 2 || event.Country[0] < 'A' || event.Country[0] > 'Z' || event.Country[1] < 'A' || event.Country[1] > 'Z') {
		return errors.New("invalid diagnostic country code")
	}
	if event.Region != "" {
		if err := ValidateDatasetClassificationName(event.Region); err != nil {
			return err
		}
	}
	if event.Mode != "observe" && event.Mode != "enforce" {
		return errors.New("invalid diagnostic policy mode")
	}
	if event.DomainSource < PolicyDomainSourceUnspecified || event.DomainSource > PolicyDomainSourceUnavailable || event.Reason < PolicySecurityEventReasonNone || event.Reason > PolicySecurityEventReasonRevoked {
		return errors.New("invalid diagnostic event catalog value")
	}
	return nil
}
