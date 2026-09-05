package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func datasetTestReference() DatasetReference {
	return DatasetReference{Handle: strings.Repeat("a", 43), InstanceID: "instance-a", Generation: "generation-1", SourceID: "community", VersionDigest: "sha256:" + strings.Repeat("1", 64)}
}

func datasetTestQuery() DatasetQueryRequest {
	yes := true
	return DatasetQueryRequest{
		Reference: datasetTestReference(), Domain: "api.example.com",
		Classifications: []DatasetClassification{{Name: "category-ai-!cn", Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "cn", Boolean: &yes, Negate: true}}}},
		Budget:          DatasetQueryBudget{MaxDurationMicros: 1000, MaxResponseBytes: DatasetMaxQueryResponseBytes},
	}
}

func datasetTestVersion() DatasetVersion {
	digest := datasetTestReference().VersionDigest
	return DatasetVersion{
		SourceID: "community", Digest: digest, SourceURL: "https://datasets.example/revision/data",
		Revision: "commit-0123456789", FetchedAt: "2026-09-05T12:00:00Z", RawDigest: digest, IndexDigest: digest,
		Format: DatasetFormatCommunity, SemanticVersion: "v1", ClassificationCount: 4000, EntryCount: 500000, IndexBytes: 1 << 24,
		Coverage: DatasetAddressCoverage{IPv4: DatasetFamilyNone, IPv6: DatasetFamilyNone},
	}
}

func TestDatasetClassificationNamesAndTypedAttributes(t *testing.T) {
	for _, name := range []string{"category-ai-!cn", "geolocation-!cn", "CN", "cn-44", "category_ai", "example@cn", "a+b"} {
		if err := ValidateDatasetClassificationName(name); err != nil {
			t.Errorf("legitimate source category %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "../cn", "cn/44", "cn\\44", "cn\n", " cn", strings.Repeat("x", 129), "cn\x00"} {
		if err := ValidateDatasetClassificationName(name); err == nil {
			t.Errorf("invalid source category %q accepted", name)
		}
	}
	yes, number := true, int64(7)
	for _, attribute := range []DatasetAttribute{{Name: "ads", Boolean: &yes}, {Name: "rank", Integer: &number}, {Name: "cn", Boolean: &yes, Negate: true}} {
		if err := attribute.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, attribute := range []DatasetAttribute{{Name: "cn"}, {Name: "cn", Boolean: &yes, Integer: &number}, {Name: "x/y", Boolean: &yes}} {
		if err := attribute.Validate(); err == nil {
			t.Fatalf("invalid typed attribute accepted: %+v", attribute)
		}
	}
}

func TestDatasetAttributeNamesHaveIndependentBoundedGrammar(t *testing.T) {
	for _, name := range []string{"!cn", "cn", "ads", "rank", "cn!", "a-b", "a_b", "a.b", "a+b", "a@b", "!" + strings.Repeat("a", 127)} {
		if err := ValidateDatasetAttributeName(name); err != nil {
			t.Errorf("valid literal attribute %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", " ", "!cn ", " !cn", "! cn", "!cn\t", "!cn\n", "!cn\r", "!cn\x00", "!cn\x1f", "../cn", "..", "/cn", "cn/ads", "cn\\ads", "!cn/ads", "!cn:ads", "!" + strings.Repeat("a", 128)} {
		if err := ValidateDatasetAttributeName(name); err == nil {
			t.Errorf("invalid literal attribute %q accepted", name)
		}
	}
	if err := ValidateDatasetClassificationName("!cn"); err == nil {
		t.Fatal("attribute support loosened classification name validation")
	}
	if err := ValidateDatasetClassificationName("category-ai-!cn"); err != nil {
		t.Fatal("existing classification name became invalid")
	}
}

func TestDatasetLiteralBangAttributeContractRoundtrips(t *testing.T) {
	yes, no, number := true, false, int64(-7)
	for name, attribute := range map[string]DatasetAttribute{
		"boolean true":              {Name: "!cn", Boolean: &yes},
		"boolean false":             {Name: "!cn", Boolean: &no},
		"integer":                   {Name: "!cn", Integer: &number},
		"negated boolean predicate": {Name: "!cn", Boolean: &yes, Negate: true},
		"negated integer predicate": {Name: "!cn", Integer: &number, Negate: true},
	} {
		t.Run(name, func(t *testing.T) {
			request := datasetTestQuery()
			request.Classifications[0].Attributes = []DatasetAttribute{attribute}
			if err := request.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var decoded DatasetQueryRequest
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Validate() != nil || !reflect.DeepEqual(decoded, request) {
				t.Fatalf("literal !cn attribute lost its key, value type or negation: %+v %v", decoded, err)
			}
			client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
				if call.Operation != HostRuntimeDatasetQuery {
					t.Errorf("unexpected operation %q", call.Operation)
				}
				var received DatasetQueryRequest
				if err := json.Unmarshal(call.Payload, &received); err != nil || received.Validate() != nil || !reflect.DeepEqual(received, request) {
					t.Error("public HostRuntime contract changed literal attribute semantics")
				}
				return datasetTestResponse(t, DatasetQueryResponse{Reference: request.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: DatasetCovered}}})
			})
			if response, err := client.QueryDatasets(t.Context(), request); err != nil || response.Status != DatasetQueryOK {
				t.Fatalf("query with !cn attribute failed: %+v %v", response, err)
			}
		})
	}
	if err := (DatasetAttribute{Name: "!cn"}).Validate(); err == nil {
		t.Fatal("missing typed attribute value accepted")
	}
	if err := (DatasetAttribute{Name: "!cn", Boolean: &yes, Integer: &number}).Validate(); err == nil {
		t.Fatal("conflicting typed attribute values accepted")
	}
}

func TestDatasetQueryRejectsInvalidBindingInputAndBudget(t *testing.T) {
	if err := datasetTestQuery().ValidateFor("instance-a", "generation-1"); err != nil {
		t.Fatal(err)
	}
	for _, binding := range [][2]string{{"instance-b", "generation-1"}, {"instance-a", "generation-2"}, {"", ""}} {
		if err := datasetTestQuery().ValidateFor(binding[0], binding[1]); err == nil {
			t.Fatal("foreign or stale dataset reference accepted")
		}
	}
	cases := map[string]func(*DatasetQueryRequest){
		"missing opaque grant":       func(q *DatasetQueryRequest) { q.Reference.Handle = "claimed" },
		"bad digest":                 func(q *DatasetQueryRequest) { q.Reference.VersionDigest = "latest" },
		"unsupported classification": func(q *DatasetQueryRequest) { q.Classifications[0].Kind = "regex" },
		"wrong classification input": func(q *DatasetQueryRequest) { q.Classifications[0].Kind = DatasetClassificationCountry },
		"missing input":              func(q *DatasetQueryRequest) { q.Domain = "" },
		"both inputs":                func(q *DatasetQueryRequest) { q.Address = "192.0.2.1" },
		"no classifications":         func(q *DatasetQueryRequest) { q.Classifications = nil },
		"duplicate classifications":  func(q *DatasetQueryRequest) { q.Classifications = append(q.Classifications, q.Classifications[0]) },
		"over classification bound":  func(q *DatasetQueryRequest) { q.Classifications = make([]DatasetClassification, 65) },
		"duplicate attribute": func(q *DatasetQueryRequest) {
			q.Classifications[0].Attributes = append(q.Classifications[0].Attributes, q.Classifications[0].Attributes[0])
		},
		"unbounded attributes":      func(q *DatasetQueryRequest) { q.Classifications[0].Attributes = make([]DatasetAttribute, 33) },
		"no time budget":            func(q *DatasetQueryRequest) { q.Budget.MaxDurationMicros = 0 },
		"excessive time budget":     func(q *DatasetQueryRequest) { q.Budget.MaxDurationMicros = 2001 },
		"no response budget":        func(q *DatasetQueryRequest) { q.Budget.MaxResponseBytes = 0 },
		"excessive response budget": func(q *DatasetQueryRequest) { q.Budget.MaxResponseBytes = DatasetMaxQueryResponseBytes + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			query := datasetTestQuery()
			mutate(&query)
			if err := query.Validate(); err == nil {
				t.Fatal("invalid query accepted")
			}
		})
	}
	for _, domain := range []string{"API.example.com", "example.com.", "a..example", "-bad.example", "bad-.example", "https://example.com", "example.com:443", "192.0.2.1", "例子.example", strings.Repeat("a", 64) + ".example"} {
		query := datasetTestQuery()
		query.Domain = domain
		if err := query.Validate(); err == nil {
			t.Errorf("noncanonical domain accepted: %q", domain)
		}
	}
	for _, address := range []string{"192.0.2.1", "2001:db8::1"} {
		query := DatasetQueryRequest{Reference: datasetTestReference(), Address: address, Classifications: []DatasetClassification{{Name: "cn-44", Kind: DatasetClassificationRegion}}, Budget: datasetTestQuery().Budget}
		if err := query.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"192.0.2.1:80", "fe80::1%eth0", "2001:0db8::1", "192.168.001.1"} {
		query := DatasetQueryRequest{Reference: datasetTestReference(), Address: address, Classifications: []DatasetClassification{{Name: "cn", Kind: DatasetClassificationCountry}}, Budget: datasetTestQuery().Budget}
		if err := query.Validate(); err == nil {
			t.Errorf("noncanonical address accepted: %q", address)
		}
	}
}

func TestDatasetQueryCoverageAndFailuresStayDistinct(t *testing.T) {
	query := DatasetQueryRequest{Reference: datasetTestReference(), Address: "2001:db8::1", Classifications: []DatasetClassification{{Name: "cn-44", Kind: DatasetClassificationRegion}}, Budget: datasetTestQuery().Budget}
	for _, match := range []DatasetMatch{{Index: 0, Matched: true, Coverage: DatasetCovered}, {Index: 0, Coverage: DatasetCovered}, {Index: 0, Coverage: DatasetUnknown}, {Index: 0, Coverage: DatasetUnsupportedFamily}} {
		response := DatasetQueryResponse{Reference: query.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{match}}
		if err := response.ValidateFor(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []DatasetQueryStatus{DatasetQueryUnavailable, DatasetQueryMissingClassification, DatasetQueryBudgetExceeded, DatasetQueryUnauthorized, DatasetQueryStaleReference, DatasetQueryInvalidData} {
		response := DatasetQueryResponse{Reference: query.Reference, Status: status}
		if err := response.ValidateFor(query); err != nil {
			t.Fatalf("valid failure %s rejected: %v", status, err)
		}
		response.Matches = []DatasetMatch{{Index: 0, Matched: true, Coverage: DatasetCovered}}
		if err := response.ValidateFor(query); err == nil {
			t.Fatalf("failure %s accepted partial success", status)
		}
	}
	cases := map[string]func(*DatasetQueryResponse){
		"unknown success":     func(r *DatasetQueryResponse) { r.Matches[0].Coverage = DatasetUnknown },
		"unsupported success": func(r *DatasetQueryResponse) { r.Matches[0].Coverage = DatasetUnsupportedFamily },
		"omitted result":      func(r *DatasetQueryResponse) { r.Matches = nil },
		"reordered result":    func(r *DatasetQueryResponse) { r.Matches[0].Index = 1 },
		"extra result": func(r *DatasetQueryResponse) {
			r.Matches = append(r.Matches, DatasetMatch{Index: 1, Coverage: DatasetCovered})
		},
		"switched generation": func(r *DatasetQueryResponse) { r.Reference.Generation = "generation-2" },
		"switched digest":     func(r *DatasetQueryResponse) { r.Reference.VersionDigest = "sha256:" + strings.Repeat("2", 64) },
		"unrecognized status": func(r *DatasetQueryResponse) { r.Status = "not-found" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			response := DatasetQueryResponse{Reference: query.Reference, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Matched: true, Coverage: DatasetCovered}}}
			mutate(&response)
			if err := response.ValidateFor(query); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	query.Budget.MaxResponseBytes = 10
	if err := (DatasetQueryResponse{Reference: query.Reference, Status: DatasetQueryUnavailable}).ValidateFor(query); err == nil {
		t.Fatal("response accepted beyond caller budget")
	}
}

func TestDatasetQueryAtClassificationBound(t *testing.T) {
	query := datasetTestQuery()
	query.Classifications = nil
	response := DatasetQueryResponse{Reference: query.Reference, Status: DatasetQueryOK}
	for i := 0; i < DatasetMaxQueryClassifications; i++ {
		query.Classifications = append(query.Classifications, DatasetClassification{Name: "category-" + strconv.Itoa(i), Kind: DatasetClassificationDomain})
		response.Matches = append(response.Matches, DatasetMatch{Index: i, Coverage: DatasetCovered})
	}
	if err := response.ValidateFor(query); err != nil {
		t.Fatalf("full bounded response rejected: %v", err)
	}
	query = datasetTestQuery()
	yes := true
	query.Classifications[0].Attributes = append(query.Classifications[0].Attributes, DatasetAttribute{Name: "ads", Boolean: &yes})
	duplicate := query.Classifications[0]
	duplicate.Attributes = []DatasetAttribute{duplicate.Attributes[1], duplicate.Attributes[0]}
	query.Classifications = append(query.Classifications, duplicate)
	if err := query.Validate(); err == nil {
		t.Fatal("attribute-order permutation bypassed selector deduplication")
	}
}

func TestDatasetSourceAndVersionBounds(t *testing.T) {
	source := DatasetSource{ID: "uploaded", Name: "Uploaded CIDRs", Format: DatasetFormatCIDR}
	if err := source.Validate(); err != nil {
		t.Fatalf("upload-only source rejected: %v", err)
	}
	source.RefreshIntervalSeconds = 86400
	if err := source.Validate(); err == nil {
		t.Fatal("upload-only source accepted remote refresh schedule")
	}
	source.URL = "https://example.com/data"
	for _, interval := range []int64{-1, 1, 3599, 367 * 86400} {
		source.RefreshIntervalSeconds = interval
		if err := source.Validate(); err == nil {
			t.Errorf("invalid refresh interval accepted: %d", interval)
		}
	}
	cases := map[string]func(*DatasetVersion){
		"no revision":             func(v *DatasetVersion) { v.Revision = "" },
		"no digest":               func(v *DatasetVersion) { v.RawDigest = "" },
		"invalid timestamp":       func(v *DatasetVersion) { v.FetchedAt = "yesterday" },
		"unsupported format":      func(v *DatasetVersion) { v.Format = "executable" },
		"no semantic version":     func(v *DatasetVersion) { v.SemanticVersion = "" },
		"no entries":              func(v *DatasetVersion) { v.EntryCount = 0 },
		"entry overflow":          func(v *DatasetVersion) { v.EntryCount = DatasetMaxEntries + 1 },
		"classification overflow": func(v *DatasetVersion) { v.ClassificationCount = DatasetMaxClassifications + 1 },
		"no index":                func(v *DatasetVersion) { v.IndexBytes = 0 },
		"implicit ipv6":           func(v *DatasetVersion) { v.Coverage.IPv6 = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			version := datasetTestVersion()
			mutate(&version)
			if err := version.Validate(); err == nil {
				t.Fatal("invalid version accepted")
			}
		})
	}
}

func TestDatasetVersionPinsAttributionAndAcceptsGeoMMDB(t *testing.T) {
	source := DatasetSource{ID: "licensed-source", Name: "Region data", Format: DatasetFormatGeoMMDB, AttributionText: "Data attribution", AttributionURL: "https://data.example/credits", LicenseURL: "https://data.example/license"}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	version := datasetTestVersion()
	version.Format = DatasetFormatGeoMMDB
	version.AttributionText, version.AttributionURL, version.LicenseURL = source.AttributionText, source.AttributionURL, source.LicenseURL
	source.AttributionText = "Next version credit"
	encoded, err := json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DatasetVersion
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Validate() != nil || decoded.AttributionText != "Data attribution" {
		t.Fatal("immutable version lost original attribution")
	}
	version.AttributionURL = "javascript:alert(1)"
	if version.Validate() == nil {
		t.Fatal("unsafe attribution URL accepted")
	}
	source.AttributionText = "bad\ncredit"
	if source.Validate() == nil {
		t.Fatal("attribution control characters accepted")
	}
}

func TestDatasetControlCatalogAndStatusValidation(t *testing.T) {
	version := datasetTestVersion()
	source := DatasetSource{ID: version.SourceID, Name: "Community categories", URL: version.SourceURL, Format: version.Format, RefreshIntervalSeconds: 86400}
	candidate := DatasetImportCandidate{Revision: version.Revision, ExpectedDigest: version.RawDigest, ArtifactDigest: version.RawDigest}
	requests := []DatasetControlRequest{
		{Action: DatasetControlPutSource, SourceID: source.ID, Source: &source},
		{Action: DatasetControlImport, SourceID: source.ID, Candidate: &candidate},
		{Action: DatasetControlRefresh, SourceID: source.ID},
		{Action: DatasetControlActivate, SourceID: source.ID, VersionDigest: version.Digest},
		{Action: DatasetControlRollback, SourceID: source.ID, VersionDigest: version.Digest},
		{Action: DatasetControlDeleteVersion, SourceID: source.ID, VersionDigest: version.Digest},
		{Action: DatasetControlDeleteSource, SourceID: source.ID},
	}
	for _, request := range requests {
		if err := request.Validate(); err != nil {
			t.Fatalf("control action %s: %v", request.Action, err)
		}
		wire, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded DatasetControlRequest
		if err := json.Unmarshal(wire, &decoded); err != nil || decoded.Validate() != nil || !reflect.DeepEqual(request, decoded) {
			t.Fatalf("control action did not roundtrip: %s", request.Action)
		}
	}
	for _, request := range []DatasetControlRequest{
		{Action: DatasetControlPutSource, SourceID: source.ID},
		{Action: DatasetControlImport, SourceID: source.ID},
		{Action: DatasetControlActivate, SourceID: source.ID},
		{Action: DatasetControlRefresh, SourceID: source.ID, Candidate: &candidate},
		{Action: DatasetControlDeleteSource, SourceID: source.ID, VersionDigest: version.Digest},
		{Action: DatasetControlDeleteVersion, SourceID: source.ID, Source: &source, VersionDigest: version.Digest},
		{Action: DatasetControlPutSource, SourceID: "other", Source: &source},
		{Action: "download-inline", SourceID: source.ID},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid control action accepted: %+v", request)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:password@example.com/data", "https:///missing-host", "https://example.com/data#fragment"} {
		bad := source
		bad.URL = raw
		if err := bad.Validate(); err == nil {
			t.Errorf("invalid source URL accepted: %q", raw)
		}
	}
	badCandidate := candidate
	badCandidate.URL = source.URL
	if err := badCandidate.Validate(); err == nil {
		t.Fatal("ambiguous artifact and remote import accepted")
	}
	badCandidate = candidate
	badCandidate.ExpectedDigest = "sha256:" + strings.Repeat("2", 64)
	if err := badCandidate.Validate(); err == nil {
		t.Fatal("import digest mismatch accepted")
	}
	if err := version.Validate(); err != nil {
		t.Fatal(err)
	}
	pageRequest := DatasetCatalogRequest{SourceID: source.ID, Limit: 1}
	page := DatasetCatalogResponse{SourceID: source.ID, Versions: []DatasetVersion{version}}
	if err := page.ValidateFor(pageRequest); err != nil {
		t.Fatal(err)
	}
	page.Versions = append(page.Versions, version)
	if err := page.ValidateFor(pageRequest); err == nil {
		t.Fatal("duplicate or oversized history accepted")
	}
	pageRequest.VersionDigest = version.Digest
	page = DatasetCatalogResponse{SourceID: source.ID, VersionDigest: version.Digest, Classifications: []DatasetCatalogEntry{{Classification: DatasetClassification{Name: "cn-44", Kind: DatasetClassificationRegion}, DisplayName: "广东省", EntryCount: 100, Coverage: DatasetAddressCoverage{IPv4: DatasetFamilyPartial, IPv6: DatasetFamilyNone}}}}
	if err := page.ValidateFor(pageRequest); err != nil {
		t.Fatal(err)
	}
	page.VersionDigest = "sha256:" + strings.Repeat("2", 64)
	if err := page.ValidateFor(pageRequest); err == nil {
		t.Fatal("catalog from another version accepted")
	}
	for _, phase := range []DatasetNodePhase{DatasetNodeApplied, DatasetNodeOffline, DatasetNodePreparing, DatasetNodeFailed} {
		status := DatasetStatusResponse{SourceID: source.ID, NodeID: "node-a", Desired: version.Digest, Applied: version.Digest, LastGood: version.Digest, Generation: "generation-1", Phase: phase}
		if phase != DatasetNodeApplied {
			status.Desired = "sha256:" + strings.Repeat("2", 64)
		}
		if phase == DatasetNodeFailed {
			status.Failure = DatasetFailureDigest
		}
		if err := status.Validate(); err != nil {
			t.Fatalf("node phase %s cannot retain last good: %v", phase, err)
		}
	}
	for _, status := range []DatasetStatusResponse{
		{SourceID: source.ID, NodeID: "node-a", Phase: DatasetNodeApplied, Desired: version.Digest},
		{SourceID: source.ID, NodeID: "node-a", Phase: DatasetNodeFailed},
		{SourceID: source.ID, NodeID: "node-a", Phase: DatasetNodeOffline, Applied: version.Digest},
		{SourceID: source.ID, NodeID: "node-a", Phase: DatasetNodeOffline, Failure: DatasetFailureDigest},
	} {
		if err := status.Validate(); err == nil {
			t.Fatalf("misleading dataset status accepted: %+v", status)
		}
	}
}

func datasetTestRuntimeClient(t *testing.T, handle func(HostRuntimeCall) HostRuntimeResponse) *HostRuntimeClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != PluginHostCallPath || request.Header.Get(HeaderPluginHostCredential) != "test-instance-credential" {
			t.Error("missing HostRuntime path or credential")
			http.Error(writer, "denied", http.StatusForbidden)
			return
		}
		var call HostRuntimeCall
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&call); err != nil || call.Validate() != nil {
			t.Error("invalid runtime request")
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(handle(call))
	}))
	t.Cleanup(server.Close)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &HostRuntimeClient{client: &http.Client{Transport: transport}, credential: "test-instance-credential"}
}

func datasetTestResponse(t *testing.T, value any) HostRuntimeResponse {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return HostRuntimeResponse{Payload: payload}
}

func TestDatasetPublicQueryRoundtripAndFailure(t *testing.T) {
	for _, status := range []DatasetQueryStatus{DatasetQueryOK, DatasetQueryMissingClassification, DatasetQueryUnavailable, DatasetQueryBudgetExceeded, DatasetQueryUnauthorized, DatasetQueryStaleReference, DatasetQueryInvalidData} {
		t.Run(string(status), func(t *testing.T) {
			client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
				if call.Operation != HostRuntimeDatasetQuery {
					t.Errorf("wrong operation: %s", call.Operation)
				}
				var request DatasetQueryRequest
				if err := json.Unmarshal(call.Payload, &request); err != nil || request.ValidateFor("instance-a", "generation-1") != nil {
					t.Error("invalid dataset request at server")
				}
				response := DatasetQueryResponse{Reference: request.Reference, Status: status}
				if status == DatasetQueryOK {
					response.Matches = []DatasetMatch{{Index: 0, Matched: true, Coverage: DatasetCovered}}
				}
				return datasetTestResponse(t, response)
			})
			response, err := client.QueryDatasets(t.Context(), datasetTestQuery())
			if err != nil || response.Status != status {
				t.Fatalf("roundtrip status = %s, err = %v", response.Status, err)
			}
		})
	}
	client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset query capability denied"}}
	})
	_, err := client.QueryDatasets(t.Context(), datasetTestQuery())
	var runtimeError *RuntimeError
	if !errors.As(err, &runtimeError) || runtimeError.Code != ErrorPermissionDenied {
		t.Fatalf("missing capability became a successful query: %v", err)
	}
	client = datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		ref := datasetTestReference()
		ref.Generation = "generation-2"
		return datasetTestResponse(t, DatasetQueryResponse{Reference: ref, Status: DatasetQueryOK, Matches: []DatasetMatch{{Index: 0, Coverage: DatasetCovered}}})
	})
	if _, err := client.QueryDatasets(t.Context(), datasetTestQuery()); err == nil {
		t.Fatal("foreign generation response accepted by public client")
	}
}

func TestDatasetManagementClientRoundtrips(t *testing.T) {
	ref, version := datasetTestReference(), datasetTestVersion()
	client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		switch call.Operation {
		case HostRuntimeDatasetOpen:
			var request DatasetOpenRequest
			if err := json.Unmarshal(call.Payload, &request); err != nil || request.Validate() != nil {
				t.Error("invalid open request")
			}
			return datasetTestResponse(t, ref)
		case HostRuntimeDatasetControl:
			var request DatasetControlRequest
			if err := json.Unmarshal(call.Payload, &request); err != nil || request.Validate() != nil {
				t.Error("invalid control request")
			}
			return datasetTestResponse(t, DatasetControlResponse{OperationID: call.OperationID, SourceID: request.SourceID})
		case HostRuntimeDatasetStatus:
			var request DatasetStatusRequest
			if err := json.Unmarshal(call.Payload, &request); err != nil || request.Validate() != nil {
				t.Error("invalid status request")
			}
			return datasetTestResponse(t, DatasetStatusResponse{SourceID: request.SourceID, NodeID: request.NodeID, Desired: "sha256:" + strings.Repeat("2", 64), Applied: ref.VersionDigest, LastGood: ref.VersionDigest, Generation: ref.Generation, Phase: DatasetNodeFailed, Failure: DatasetFailureDigest})
		case HostRuntimeDatasetCatalog:
			var request DatasetCatalogRequest
			if err := json.Unmarshal(call.Payload, &request); err != nil || request.Validate() != nil {
				t.Error("invalid catalog request")
			}
			return datasetTestResponse(t, DatasetCatalogResponse{SourceID: request.SourceID, Versions: []DatasetVersion{version}})
		default:
			t.Errorf("unknown dataset operation %s", call.Operation)
			return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorInternal, Message: "unexpected operation"}}
		}
	})
	if actual, err := client.OpenDataset(t.Context(), DatasetOpenRequest{SourceID: ref.SourceID, VersionDigest: ref.VersionDigest}); err != nil || actual != ref {
		t.Fatalf("open dataset = %+v, %v", actual, err)
	}
	if response, err := client.ControlDataset(t.Context(), "operation-1", DatasetControlRequest{SourceID: ref.SourceID, Action: DatasetControlRefresh}); err != nil || response.OperationID != "operation-1" {
		t.Fatalf("control dataset = %+v, %v", response, err)
	}
	if response, err := client.DatasetStatus(t.Context(), DatasetStatusRequest{SourceID: ref.SourceID, NodeID: "node-a"}); err != nil || response.Desired == response.Applied || response.Failure != DatasetFailureDigest {
		t.Fatalf("dataset node status = %+v, %v", response, err)
	}
	if response, err := client.DatasetCatalog(t.Context(), DatasetCatalogRequest{SourceID: ref.SourceID, Limit: 1}); err != nil || len(response.Versions) != 1 {
		t.Fatalf("dataset history = %+v, %v", response, err)
	}
}
