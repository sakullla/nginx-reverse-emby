package pluginsdk

import (
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func policyDatasetTestQuery() PolicyDatasetQueryRequest {
	return PolicyDatasetQueryRequest{Reference: datasetTestReference(), Classifications: []DatasetClassification{{Name: "cn-44", Kind: DatasetClassificationRegion}}, Budget: DatasetQueryBudget{MaxDurationMicros: 1000, MaxResponseBytes: 4096}}
}

func TestPolicyDatasetWireRoundtripAtMaximumBounds(t *testing.T) {
	request := policyDatasetTestQuery()
	request.Reference.Handle = strings.Repeat("a", 256)
	request.Reference.InstanceID = strings.Repeat("i", PolicyIdentityMaxBytes)
	request.Reference.Generation = strings.Repeat("g", PolicyIdentityMaxBytes)
	request.Reference.SourceID = strings.Repeat("s", PolicyIdentityMaxBytes)
	request.Classifications = nil
	response := DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryOK}
	for i := 0; i < DatasetMaxQueryClassifications; i++ {
		request.Classifications = append(request.Classifications, DatasetClassification{Name: "region-" + strconv.Itoa(i), Kind: DatasetClassificationRegion})
		response.Matches = append(response.Matches, DatasetMatch{Index: i, Matched: i%2 == 0, Coverage: DatasetCovered})
	}
	frame, err := MarshalPolicyDatasetQueryRequest(request, int(PolicyV1MaxInputFrameBytes))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPolicyDatasetQueryRequest(frame, int(PolicyV1MaxInputFrameBytes))
	if err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("request roundtrip: %+v, %v", decoded, err)
	}
	if _, err := MarshalPolicyDatasetQueryRequest(request, len(frame)-1); err == nil {
		t.Fatal("request ignored complete frame overhead")
	}
	frame, err = MarshalPolicyDatasetQueryResponse(response, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("64 matches with maximum reference lengths: %d protobuf bytes (ceiling 4096)", len(frame))
	decodedResponse, err := UnmarshalPolicyDatasetQueryResponse(frame, request)
	if err != nil || !reflect.DeepEqual(decodedResponse, response) {
		t.Fatalf("response roundtrip: %+v, %v", decodedResponse, err)
	}
	request.Budget.MaxResponseBytes = len(frame) - 1
	if _, err := MarshalPolicyDatasetQueryResponse(response, request); err == nil {
		t.Fatal("response encoder exceeded whole frame budget")
	}
	if _, err := UnmarshalPolicyDatasetQueryResponse(frame, request); err == nil {
		t.Fatal("response decoder exceeded whole frame budget")
	}
}

func TestPolicyDatasetWireFailuresAndMalformedFrames(t *testing.T) {
	request := policyDatasetTestQuery()
	for _, status := range []DatasetQueryStatus{DatasetQueryUnavailable, DatasetQueryMissingClassification, DatasetQueryBudgetExceeded, DatasetQueryUnauthorized, DatasetQueryStaleReference, DatasetQueryInvalidData} {
		response := DatasetQueryResponse{Reference: request.Reference, Status: status}
		frame, err := MarshalPolicyDatasetQueryResponse(response, request)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalPolicyDatasetQueryResponse(frame, request)
		if err != nil || decoded.Status != status || len(decoded.Matches) != 0 {
			t.Fatalf("failure became a match: %+v %v", decoded, err)
		}
	}
	for _, coverage := range []DatasetMatchCoverage{DatasetCovered, DatasetUnknown, DatasetUnsupportedFamily} {
		frame, err := MarshalPolicyDatasetQueryResponse(DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: coverage}}}, request)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalPolicyDatasetQueryResponse(frame, request)
		if err != nil || decoded.Matches[0].Coverage != coverage || decoded.Matches[0].Matched {
			t.Fatalf("coverage changed: %+v %v", decoded, err)
		}
	}
	frame, err := MarshalPolicyDatasetQueryRequest(request, 4096)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"truncated":           frame[:len(frame)-1],
		"caller address":      appendBytesField(append([]byte(nil), frame...), 5, []byte("192.0.2.1")),
		"duplicate budget":    appendVarintField(append([]byte(nil), frame...), 3, 1),
		"duplicate reference": appendBytesField(append([]byte(nil), frame...), 1, nil),
		"wrong wire":          []byte{0x08, 0x01},
		"overflow":            appendVarintField(nil, 3, 1<<32),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := UnmarshalPolicyDatasetQueryRequest(bad, 4096); err == nil {
				t.Fatal("malformed policy dataset request accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*PolicyDatasetQueryRequest){
		"domain classification": func(q *PolicyDatasetQueryRequest) {
			q.Classifications[0] = DatasetClassification{Name: "category-ai-!cn", Kind: DatasetClassificationDomain}
		},
		"time ceiling":   func(q *PolicyDatasetQueryRequest) { q.Budget.MaxDurationMicros = 2001 },
		"output ceiling": func(q *PolicyDatasetQueryRequest) { q.Budget.MaxResponseBytes = 4097 },
		"missing budget": func(q *PolicyDatasetQueryRequest) { q.Budget = DatasetQueryBudget{} },
	} {
		t.Run(name, func(t *testing.T) {
			query := policyDatasetTestQuery()
			mutate(&query)
			if query.Validate() == nil {
				t.Fatal("invalid policy lookup accepted")
			}
		})
	}
	response := DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: DatasetCovered}}}
	valid, err := MarshalPolicyDatasetQueryResponse(response, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{appendVarintField(append([]byte(nil), valid...), 2, 2), appendBytesField(append([]byte(nil), valid...), 1, nil), {0x10, 99}, appendBytesField(append([]byte(nil), valid...), 3, []byte{0x10, 2})} {
		if _, err := UnmarshalPolicyDatasetQueryResponse(bad, request); err == nil {
			t.Fatal("duplicate/unknown status or non-boolean match accepted")
		}
	}
	request.Reference.Generation = "generation-2"
	if _, err := UnmarshalPolicyDatasetQueryResponse(valid, request); err == nil {
		t.Fatal("cross-generation policy query response accepted")
	}
	if request.ValidateFor("another-instance", "generation-2") == nil {
		t.Fatal("cross-instance policy query request accepted")
	}
}

func TestPolicyTrustedSourceWireAndBinding(t *testing.T) {
	source := PolicyTrustedSource{InstanceID: "instance-a", Generation: "generation-1", EntryID: "entry-1", PeerAddress: netip.MustParseAddr("192.0.2.1"), SourceAddress: netip.MustParseAddr("2001:db8::1"), Authority: PolicySourcePROXY}
	response := PolicyTrustedSourceResponse{Source: &source}
	frame, err := MarshalPolicyTrustedSourceResponse(response, 4096)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPolicyTrustedSourceResponse(frame, 4096)
	if err != nil || *decoded.Source != source {
		t.Fatalf("trusted source roundtrip: %+v %v", decoded, err)
	}
	if source.ValidateFor("instance-a", "generation-1", "entry-1") != nil {
		t.Fatal("valid binding rejected")
	}
	for _, binding := range [][3]string{{"foreign", "generation-1", "entry-1"}, {"instance-a", "generation-2", "entry-1"}, {"instance-a", "generation-1", "entry-2"}} {
		if source.ValidateFor(binding[0], binding[1], binding[2]) == nil {
			t.Fatal("foreign source binding accepted")
		}
	}
	for _, failure := range []ErrorCode{ErrorPermissionDenied, ErrorUnavailable, ErrorResourceExhausted} {
		encoded, err := MarshalPolicyTrustedSourceResponse(PolicyTrustedSourceResponse{Error: &RuntimeError{Code: failure, Message: "source unavailable"}}, 4096)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalPolicyTrustedSourceResponse(encoded, 4096)
		if err != nil || decoded.Source != nil || decoded.Error.Code != failure {
			t.Fatalf("source failure changed: %+v %v", decoded, err)
		}
	}
	for _, malformed := range [][]byte{nil, appendBytesField(append([]byte(nil), frame...), 2, []byte{0x08, 2}), appendBytesField(append([]byte(nil), frame...), 1, nil)} {
		if _, err := UnmarshalPolicyTrustedSourceResponse(malformed, 4096); err == nil {
			t.Fatal("missing/conflicting source result accepted")
		}
	}
	if _, err := MarshalPolicyTrustedSourceResponse(response, len(frame)-1); err == nil {
		t.Fatal("source frame exceeded output budget")
	}
	if ValidatePolicyTrustedSourceRequestFrame(nil) != nil {
		t.Fatal("empty source request rejected")
	}
	if ValidatePolicyTrustedSourceRequestFrame([]byte{0x08, 1}) == nil {
		t.Fatal("self-reported trusted flag accepted")
	}
	source.Authority = PolicySourceSocket
	if source.Validate() == nil {
		t.Fatal("socket authority accepted forwarded source")
	}
	source.SourceAddress = netip.MustParseAddr("::ffff:192.0.2.1")
	if source.Validate() == nil {
		t.Fatal("mapped IPv6 source accepted")
	}
}

func TestPolicyExtensionDescriptorsAreFixedAndSharedConcurrently(t *testing.T) {
	first, err := newPolicyExtensionMessage("DatasetQueryRequest")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			message, err := newPolicyExtensionMessage("DatasetQueryRequest")
			if err != nil || message.Descriptor() != first.Descriptor() || message == first {
				t.Error("descriptor was rebuilt or mutable message shared")
				return
			}
			policyExtensionSet(message, "max_duration_micros", protoreflect.ValueOfUint32(123))
		}()
	}
	group.Wait()
	if policyExtensionGet(first, "max_duration_micros").Uint() != 0 {
		t.Fatal("concurrent message mutation leaked")
	}
	if _, err := newPolicyExtensionMessage("attacker-controlled-name"); err == nil {
		t.Fatal("unbounded descriptor cache key accepted")
	}
	if len(policyExtensionDescriptors.messages) != 4 {
		t.Fatal("descriptor cache grew beyond fixed catalog")
	}
}
