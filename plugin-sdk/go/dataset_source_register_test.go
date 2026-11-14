package pluginsdk

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func datasetRegisterRequestFixture() DatasetSourceRegisterRequest {
	return DatasetSourceRegisterRequest{
		Source:    DatasetSource{ID: "regions", Name: "Regions", URL: "https://datasets.example.com/regions.cidr", Format: DatasetFormatCIDR, RefreshIntervalSeconds: 3600},
		Retrieval: DatasetSourceRetrieval{Mode: DatasetRetrievalPinned, Revision: "v1", ExpectedDigest: "sha256:" + strings.Repeat("1", 64)},
	}
}

func TestDatasetSourceRegisterRequestContract(t *testing.T) {
	valid := datasetRegisterRequestFixture()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	rolling := valid
	rolling.Retrieval = DatasetSourceRetrieval{Mode: DatasetRetrievalRollingSHA256, ChecksumURL: "https://datasets.example.com/regions.cidr.sha256sum"}
	if err := rolling.Validate(); err != nil {
		t.Fatal(err)
	}
	manual := valid
	manual.Source.RefreshIntervalSeconds = 0
	if err := manual.Validate(); err != nil {
		t.Fatal("manual refresh remote source rejected")
	}
	for name, mutate := range map[string]func(*DatasetSourceRegisterRequest){
		"http transport":        func(r *DatasetSourceRegisterRequest) { r.Source.URL = "http://datasets.example.com/regions.cidr" },
		"credential userinfo":   func(r *DatasetSourceRegisterRequest) { r.Source.URL = "https://user:pw@datasets.example.com/r" },
		"private IPv4 literal":  func(r *DatasetSourceRegisterRequest) { r.Source.URL = "https://192.168.7.7/r" },
		"loopback literal":      func(r *DatasetSourceRegisterRequest) { r.Source.URL = "https://127.0.0.1/r" },
		"private IPv6 literal":  func(r *DatasetSourceRegisterRequest) { r.Source.URL = "https://[fd00::9]/r" },
		"upload-only source":    func(r *DatasetSourceRegisterRequest) { r.Source.URL = "" },
		"pinned checksum url":   func(r *DatasetSourceRegisterRequest) { r.Retrieval.ChecksumURL = "https://datasets.example.com/sum" },
		"pinned missing digest": func(r *DatasetSourceRegisterRequest) { r.Retrieval.ExpectedDigest = "" },
		"pinned missing rev":    func(r *DatasetSourceRegisterRequest) { r.Retrieval.Revision = "" },
		"unknown mode":          func(r *DatasetSourceRegisterRequest) { r.Retrieval.Mode = "etag" },
		"rolling pinned field": func(r *DatasetSourceRegisterRequest) {
			r.Retrieval = DatasetSourceRetrieval{Mode: DatasetRetrievalRollingSHA256, ChecksumURL: "https://datasets.example.com/sum", Revision: "v1"}
		},
		"rolling http checksum": func(r *DatasetSourceRegisterRequest) {
			r.Retrieval = DatasetSourceRetrieval{Mode: DatasetRetrievalRollingSHA256, ChecksumURL: "http://datasets.example.com/sum"}
		},
		"rolling private checksum": func(r *DatasetSourceRegisterRequest) {
			r.Retrieval = DatasetSourceRetrieval{Mode: DatasetRetrievalRollingSHA256, ChecksumURL: "https://10.0.0.4/sum"}
		},
		"rolling missing checksum": func(r *DatasetSourceRegisterRequest) {
			r.Retrieval = DatasetSourceRetrieval{Mode: DatasetRetrievalRollingSHA256}
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatalf("non-compliant remote source registration accepted: %+v", request)
			}
		})
	}
}

func TestDatasetSourceRegisterClientRoundtripAndEcho(t *testing.T) {
	request := datasetRegisterRequestFixture()
	version := datasetTestVersion()
	version.SourceID = request.Source.ID
	var received DatasetSourceRegisterRequest
	client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		if call.Operation != HostRuntimeDatasetSourceRegister || call.OperationID != "register-1" {
			t.Errorf("unexpected call %+v", call)
		}
		if err := json.Unmarshal(call.Payload, &received); err != nil || received.Validate() != nil || received != request {
			t.Error("register request changed on the wire")
		}
		return datasetTestResponse(t, DatasetSourceRegisterResponse{SourceID: request.Source.ID, Version: version})
	})
	response, err := client.RegisterDatasetSource(t.Context(), "register-1", request)
	if err != nil || response.Version != version || received != request {
		t.Fatalf("register roundtrip = %+v, %v", response, err)
	}
	mismatched := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		return datasetTestResponse(t, DatasetSourceRegisterResponse{SourceID: "other-source", Version: version})
	})
	if _, err := mismatched.RegisterDatasetSource(t.Context(), "register-2", request); err == nil {
		t.Fatal("foreign source acknowledgement accepted")
	}
	if _, err := client.RegisterDatasetSource(t.Context(), " register-3", request); err == nil {
		t.Fatal("invalid operation identity accepted")
	}
}

func TestDatasetSourceRegisterDeniedResponseSurfaces(t *testing.T) {
	request := datasetRegisterRequestFixture()
	client := datasetTestRuntimeClient(t, func(call HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset source registration denied"}}
	})
	_, err := client.RegisterDatasetSource(t.Context(), "register-1", request)
	var failure *RuntimeError
	if !errors.As(err, &failure) || failure.Code != ErrorPermissionDenied {
		t.Fatalf("denied registration returned %v", err)
	}
}
