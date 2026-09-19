package pluginsdk

import (
	"context"
	"errors"
	"net/netip"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// PolicyDatasetQueryRequest has no address or trust flag. QuerySourceDatasets
// obtains the admission source from authenticated Host call context. A Host
// must never manufacture that context from plugin or guest request fields.
type PolicyDatasetQueryRequest struct {
	Reference       DatasetReference
	Classifications []DatasetClassification
	Budget          DatasetQueryBudget
}

func (request PolicyDatasetQueryRequest) Validate() error {
	if err := request.Reference.Validate(); err != nil {
		return err
	}
	if err := request.Budget.Validate(); err != nil {
		return err
	}
	if request.Budget.MaxResponseBytes > int(PolicyV1MaxOutputFrameBytes) {
		return errors.New("policy dataset output budget exceeds the complete policy/v1 frame ceiling")
	}
	if len(request.Classifications) < 1 || len(request.Classifications) > DatasetMaxQueryClassifications {
		return errors.New("policy dataset classification count exceeds the bound")
	}
	seen := make(map[string]bool, len(request.Classifications))
	for _, classification := range request.Classifications {
		if err := classification.Validate(); err != nil {
			return err
		}
		if classification.Kind == DatasetClassificationDomain || len(classification.Attributes) != 0 {
			return errors.New("policy source lookup only accepts address classifications")
		}
		key := datasetClassificationKey(classification)
		if seen[key] {
			return errors.New("duplicate policy dataset classification")
		}
		seen[key] = true
	}
	return nil
}

func (request PolicyDatasetQueryRequest) ValidateFor(instanceID, generation string) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return request.Reference.ValidateFor(instanceID, generation)
}

// PolicyDatasetHost is additive: PolicyHost implementations need not implement
// it unless they provide nre_host_dataset_query. The Host resolves grants and
// immutable local indices; lookup time consumes the enclosing admission budget.
type PolicyDatasetHost interface {
	QuerySourceDatasets(context.Context, PolicyDatasetQueryRequest) (DatasetQueryResponse, error)
}

type PolicySourceAuthority int32

const (
	PolicySourceSocket PolicySourceAuthority = 1
	PolicySourceXFF    PolicySourceAuthority = 2
	PolicySourcePROXY  PolicySourceAuthority = 3
	PolicySourceRelay  PolicySourceAuthority = 4
)

// PolicyTrustedSource is emitted by Host only. Its syntax is not proof of
// authority; Host authenticates socket/proxy chains before producing this value.
type PolicyTrustedSource struct {
	InstanceID    string
	Generation    string
	EntryID       string
	PeerAddress   netip.Addr
	SourceAddress netip.Addr
	Authority     PolicySourceAuthority
}

func (source PolicyTrustedSource) Validate() error {
	for _, value := range []string{source.InstanceID, source.Generation, source.EntryID} {
		if err := ValidatePolicyIdentity(value); err != nil {
			return err
		}
	}
	for _, address := range []netip.Addr{source.PeerAddress, source.SourceAddress} {
		if !address.IsValid() || address.Zone() != "" || address.Is4In6() || address.IsUnspecified() || address.IsMulticast() {
			return errors.New("invalid policy source address")
		}
	}
	switch source.Authority {
	case PolicySourceSocket:
		if source.PeerAddress != source.SourceAddress {
			return errors.New("socket source must equal the real peer")
		}
	case PolicySourceXFF, PolicySourcePROXY, PolicySourceRelay:
	default:
		return errors.New("unknown policy source authority")
	}
	return nil
}

func (source PolicyTrustedSource) ValidateFor(instanceID, generation, entryID string) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if source.InstanceID != instanceID || source.Generation != generation || source.EntryID != entryID {
		return errors.New("policy source belongs to a different instance, generation, or entry")
	}
	return nil
}

type PolicyTrustedSourceHost interface {
	ReadTrustedSource(context.Context) (PolicyTrustedSource, error)
}

type PolicyTrustedSourceResponse struct {
	Source *PolicyTrustedSource
	Error  *RuntimeError
}

func (response PolicyTrustedSourceResponse) Validate() error {
	if (response.Source == nil) == (response.Error == nil) {
		return errors.New("trusted source response requires exactly one source or error")
	}
	if response.Error != nil {
		return response.Error.Validate()
	}
	return response.Source.Validate()
}

// MarshalPolicyDatasetQueryRequest returns a complete canonical protobuf frame.
// inputFrameBytes is the current manifest budget, not a payload-only allowance.
func MarshalPolicyDatasetQueryRequest(request PolicyDatasetQueryRequest, inputFrameBytes int) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("DatasetQueryRequest")
	if err != nil {
		return nil, err
	}
	encodePolicyDatasetReference(policyExtensionChild(message, "reference"), request.Reference)
	list := message.Mutable(policyExtensionField(message, "classifications")).List()
	for _, classification := range request.Classifications {
		entry := list.NewElement().Message()
		policyExtensionSet(entry, "name", protoreflect.ValueOfString(classification.Name))
		policyExtensionSet(entry, "kind", protoreflect.ValueOfEnum(policyDatasetKindNumber(classification.Kind)))
		list.Append(protoreflect.ValueOfMessage(entry))
	}
	policyExtensionSet(message, "max_duration_micros", protoreflect.ValueOfUint32(uint32(request.Budget.MaxDurationMicros)))
	policyExtensionSet(message, "max_response_bytes", protoreflect.ValueOfUint32(uint32(request.Budget.MaxResponseBytes)))
	return marshalPolicyExtension(message, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
}

func UnmarshalPolicyDatasetQueryRequest(frame []byte, inputFrameBytes int) (PolicyDatasetQueryRequest, error) {
	message, err := unmarshalPolicyExtension("DatasetQueryRequest", frame, inputFrameBytes, int(PolicyV1MaxInputFrameBytes))
	if err != nil {
		return PolicyDatasetQueryRequest{}, err
	}
	request := PolicyDatasetQueryRequest{
		Reference: decodePolicyDatasetReference(policyExtensionGet(message, "reference").Message()),
		Budget:    DatasetQueryBudget{MaxDurationMicros: int(policyExtensionGet(message, "max_duration_micros").Uint()), MaxResponseBytes: int(policyExtensionGet(message, "max_response_bytes").Uint())},
	}
	list := policyExtensionGet(message, "classifications").List()
	for i := 0; i < list.Len(); i++ {
		entry := list.Get(i).Message()
		request.Classifications = append(request.Classifications, DatasetClassification{Name: policyExtensionGet(entry, "name").String(), Kind: policyDatasetKind(policyExtensionGet(entry, "kind").Enum())})
	}
	if err := request.Validate(); err != nil {
		return PolicyDatasetQueryRequest{}, err
	}
	return request, nil
}

func MarshalPolicyDatasetQueryResponse(response DatasetQueryResponse, request PolicyDatasetQueryRequest) ([]byte, error) {
	if err := validatePolicyDatasetResponse(response, request); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("DatasetQueryResponse")
	if err != nil {
		return nil, err
	}
	encodePolicyDatasetReference(policyExtensionChild(message, "reference"), response.Reference)
	policyExtensionSet(message, "status", protoreflect.ValueOfEnum(policyDatasetStatusNumber(response.Status)))
	list := message.Mutable(policyExtensionField(message, "matches")).List()
	for _, match := range response.Matches {
		entry := list.NewElement().Message()
		policyExtensionSet(entry, "index", protoreflect.ValueOfUint32(uint32(match.Index)))
		policyExtensionSet(entry, "matched", protoreflect.ValueOfBool(match.Matched))
		policyExtensionSet(entry, "coverage", protoreflect.ValueOfEnum(policyDatasetCoverageNumber(match.Coverage)))
		list.Append(protoreflect.ValueOfMessage(entry))
	}
	return marshalPolicyExtension(message, request.Budget.MaxResponseBytes, int(PolicyV1MaxOutputFrameBytes))
}

func UnmarshalPolicyDatasetQueryResponse(frame []byte, request PolicyDatasetQueryRequest) (DatasetQueryResponse, error) {
	if err := request.Validate(); err != nil {
		return DatasetQueryResponse{}, err
	}
	message, err := unmarshalPolicyExtension("DatasetQueryResponse", frame, request.Budget.MaxResponseBytes, int(PolicyV1MaxOutputFrameBytes))
	if err != nil {
		return DatasetQueryResponse{}, err
	}
	response := DatasetQueryResponse{Reference: decodePolicyDatasetReference(policyExtensionGet(message, "reference").Message()), Status: policyDatasetStatus(policyExtensionGet(message, "status").Enum())}
	list := policyExtensionGet(message, "matches").List()
	for i := 0; i < list.Len(); i++ {
		entry := list.Get(i).Message()
		response.Matches = append(response.Matches, DatasetMatch{Index: int(policyExtensionGet(entry, "index").Uint()), Matched: policyExtensionGet(entry, "matched").Bool(), Coverage: policyDatasetCoverage(policyExtensionGet(entry, "coverage").Enum())})
	}
	if err := validatePolicyDatasetResponse(response, request); err != nil {
		return DatasetQueryResponse{}, err
	}
	return response, nil
}

func validatePolicyDatasetResponse(response DatasetQueryResponse, request PolicyDatasetQueryRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.Reference != request.Reference || (response.Status == DatasetQueryOK && len(response.Matches) != len(request.Classifications)) {
		return errors.New("policy dataset response differs from requested snapshot or classifications")
	}
	return nil
}

// ValidatePolicyTrustedSourceRequestFrame rejects any caller-supplied source,
// trusted flag, handle or other field; this request must be completely empty.
func ValidatePolicyTrustedSourceRequestFrame(frame []byte) error {
	if len(frame) != 0 {
		return errors.New("trusted source request must be empty")
	}
	return nil
}

func MarshalPolicyTrustedSourceResponse(response PolicyTrustedSourceResponse, outputFrameBytes int) ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	message, err := newPolicyExtensionMessage("TrustedSourceResponse")
	if err != nil {
		return nil, err
	}
	if response.Source != nil {
		source := policyExtensionChild(message, "source")
		policyExtensionSet(source, "instance_id", protoreflect.ValueOfString(response.Source.InstanceID))
		policyExtensionSet(source, "generation", protoreflect.ValueOfString(response.Source.Generation))
		policyExtensionSet(source, "entry_id", protoreflect.ValueOfString(response.Source.EntryID))
		policyExtensionSet(source, "peer_address", protoreflect.ValueOfBytes(response.Source.PeerAddress.AsSlice()))
		policyExtensionSet(source, "source_address", protoreflect.ValueOfBytes(response.Source.SourceAddress.AsSlice()))
		policyExtensionSet(source, "authority", protoreflect.ValueOfEnum(protoreflect.EnumNumber(response.Source.Authority)))
	} else {
		failure := policyExtensionChild(message, "error")
		policyExtensionSet(failure, "code", protoreflect.ValueOfEnum(protoreflect.EnumNumber(response.Error.Code)))
		policyExtensionSet(failure, "message", protoreflect.ValueOfString(response.Error.Message))
		policyExtensionSet(failure, "retryable", protoreflect.ValueOfBool(response.Error.Retryable))
	}
	return marshalPolicyExtension(message, outputFrameBytes, int(PolicyV1MaxOutputFrameBytes))
}

func UnmarshalPolicyTrustedSourceResponse(frame []byte, outputFrameBytes int) (PolicyTrustedSourceResponse, error) {
	message, err := unmarshalPolicyExtension("TrustedSourceResponse", frame, outputFrameBytes, int(PolicyV1MaxOutputFrameBytes))
	if err != nil {
		return PolicyTrustedSourceResponse{}, err
	}
	var response PolicyTrustedSourceResponse
	if message.Has(policyExtensionField(message, "source")) {
		source := policyExtensionGet(message, "source").Message()
		peer, peerOK := netip.AddrFromSlice(policyExtensionGet(source, "peer_address").Bytes())
		address, sourceOK := netip.AddrFromSlice(policyExtensionGet(source, "source_address").Bytes())
		if !peerOK || !sourceOK {
			return response, errors.New("invalid trusted source address bytes")
		}
		response.Source = &PolicyTrustedSource{InstanceID: policyExtensionGet(source, "instance_id").String(), Generation: policyExtensionGet(source, "generation").String(), EntryID: policyExtensionGet(source, "entry_id").String(), PeerAddress: peer, SourceAddress: address, Authority: PolicySourceAuthority(policyExtensionGet(source, "authority").Enum())}
	} else if message.Has(policyExtensionField(message, "error")) {
		failure := policyExtensionGet(message, "error").Message()
		response.Error = &RuntimeError{Code: ErrorCode(policyExtensionGet(failure, "code").Enum()), Message: policyExtensionGet(failure, "message").String(), Retryable: policyExtensionGet(failure, "retryable").Bool()}
	}
	if err := response.Validate(); err != nil {
		return PolicyTrustedSourceResponse{}, err
	}
	return response, nil
}

func encodePolicyDatasetReference(message protoreflect.Message, ref DatasetReference) {
	for name, value := range map[protoreflect.Name]string{"handle": ref.Handle, "instance_id": ref.InstanceID, "generation": ref.Generation, "source_id": ref.SourceID, "version_digest": ref.VersionDigest} {
		policyExtensionSet(message, name, protoreflect.ValueOfString(value))
	}
}

func decodePolicyDatasetReference(message protoreflect.Message) DatasetReference {
	return DatasetReference{Handle: policyExtensionGet(message, "handle").String(), InstanceID: policyExtensionGet(message, "instance_id").String(), Generation: policyExtensionGet(message, "generation").String(), SourceID: policyExtensionGet(message, "source_id").String(), VersionDigest: policyExtensionGet(message, "version_digest").String()}
}

func policyDatasetKind(number protoreflect.EnumNumber) DatasetClassificationKind {
	switch number {
	case 1:
		return DatasetClassificationCountry
	case 2:
		return DatasetClassificationRegion
	case 3:
		return DatasetClassificationCIDR
	default:
		return ""
	}
}
func policyDatasetKindNumber(kind DatasetClassificationKind) protoreflect.EnumNumber {
	for i := protoreflect.EnumNumber(1); i <= 3; i++ {
		if policyDatasetKind(i) == kind {
			return i
		}
	}
	return 0
}
func policyDatasetStatus(number protoreflect.EnumNumber) DatasetQueryStatus {
	switch number {
	case 1:
		return DatasetQueryOK
	case 2:
		return DatasetQueryUnavailable
	case 3:
		return DatasetQueryMissingClassification
	case 4:
		return DatasetQueryBudgetExceeded
	case 5:
		return DatasetQueryUnauthorized
	case 6:
		return DatasetQueryStaleReference
	case 7:
		return DatasetQueryInvalidData
	default:
		return ""
	}
}
func policyDatasetStatusNumber(status DatasetQueryStatus) protoreflect.EnumNumber {
	for i := protoreflect.EnumNumber(1); i <= 7; i++ {
		if policyDatasetStatus(i) == status {
			return i
		}
	}
	return 0
}
func policyDatasetCoverage(number protoreflect.EnumNumber) DatasetMatchCoverage {
	switch number {
	case 1:
		return DatasetCovered
	case 2:
		return DatasetUnknown
	case 3:
		return DatasetUnsupportedFamily
	default:
		return ""
	}
}
func policyDatasetCoverageNumber(coverage DatasetMatchCoverage) protoreflect.EnumNumber {
	for i := protoreflect.EnumNumber(1); i <= 3; i++ {
		if policyDatasetCoverage(i) == coverage {
			return i
		}
	}
	return 0
}
