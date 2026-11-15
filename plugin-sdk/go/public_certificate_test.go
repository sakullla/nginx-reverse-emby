package pluginsdk

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPublicCertificateRoundTripHidesMaterial(t *testing.T) {
	if err := CapabilityPublicCertificateRead.Validate(); err != nil || CapabilityPublicCertificateTerminate.Validate() != nil {
		t.Fatal(err)
	}
	if features := RequiredRPCFeatures([]string{PermissionPublicCertificateRead, PermissionPublicCertificateTerminate}); len(features) != 0 {
		t.Fatalf("public certificate permissions required RPC features %v", features)
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeRPCService}, Permissions: []Permission{{Name: PermissionPublicCertificateRead}, {Name: PermissionPublicCertificateTerminate}}}
	if err := ValidateManifestManagedCapabilities(manifest, nil); err != nil {
		t.Fatal(err)
	}

	handle := managedTestHandle("stream", "tcp")
	request := PublicCertificateTLSRequest{CertificateID: 7, Handle: handle, RequestID: "request-a"}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.CertificateID = 0
	if err := request.Validate(); err == nil {
		t.Fatal("missing certificate id was accepted")
	}

	listClient := managedTestClient(t, func(_ *http.Request, wire HostRuntimeCall) HostRuntimeResponse {
		if wire.Operation != HostRuntimePublicCertificate || wire.OperationID != "" {
			t.Fatalf("list call = %+v", wire)
		}
		var decoded PublicCertificateListRequest
		if err := json.Unmarshal(wire.Payload, &decoded); err != nil || decoded.AgentID != "edge-a" {
			t.Fatalf("list payload = %s", wire.Payload)
		}
		body, err := json.Marshal(PublicCertificateListResponse{Certificates: []PublicCertificateProjection{{
			ID: 2, Domain: "one.example", Enabled: true, MaterialAvailable: false,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return HostRuntimeResponse{Payload: body}
	})
	listed, err := listClient.ListPublicCertificates(t.Context(), "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(encoded)), "PRIVATE KEY") || strings.Contains(string(encoded), "BEGIN ") || strings.Contains(string(encoded), "cert_pem") {
		t.Fatalf("list response carried material: %s", encoded)
	}
	if len(listed.Certificates) != 1 || listed.Certificates[0].ID != 2 || listed.Certificates[0].Domain != "one.example" || !listed.Certificates[0].Enabled || listed.Certificates[0].MaterialAvailable {
		t.Fatalf("projection = %+v", listed.Certificates)
	}

	materialClient := managedTestClient(t, func(_ *http.Request, wire HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Payload: json.RawMessage(`{"certificates":[{"id":2,"domain":"one.example","enabled":true,"material_available":false,"cert_pem":"-----BEGIN CERTIFICATE-----"}]}`)}
	})
	if _, err := materialClient.ListPublicCertificates(t.Context(), "edge-a"); err == nil {
		t.Fatal("certificate PEM field was decoded")
	}
	emptyClient := managedTestClient(t, func(_ *http.Request, wire HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Payload: json.RawMessage(`{"certificates":null}`)}
	})
	if _, err := emptyClient.ListPublicCertificates(t.Context(), "edge-a"); err == nil {
		t.Fatal("null certificate list was accepted")
	}

	var child ManagedNetworkHandle
	tlsClient := managedTestClient(t, func(_ *http.Request, wire HostRuntimeCall) HostRuntimeResponse {
		if wire.Operation != HostRuntimePublicCertificateTLS || wire.OperationID != "request-a" {
			t.Fatalf("tls call = %+v", wire)
		}
		if strings.Contains(strings.ToUpper(string(wire.Payload)), "PRIVATE KEY") || strings.Contains(string(wire.Payload), "BEGIN ") {
			t.Fatalf("tls request carried material: %s", wire.Payload)
		}
		decoded, err := DecodePublicCertificateTLSRequest(wire.Payload)
		if err != nil {
			t.Fatal(err)
		}
		child = decoded.Handle
		child.Token = strings.Repeat("b", 32)
		body, err := json.Marshal(PublicCertificateTLSResponse{Handle: child})
		if err != nil {
			t.Fatal(err)
		}
		return HostRuntimeResponse{Payload: body}
	})
	response, err := tlsClient.TerminatePublicCertificateTLS(t.Context(), PublicCertificateTLSRequest{CertificateID: 7, Handle: handle, RequestID: "request-a"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Handle.Token == handle.Token || response.Handle.Kind != "stream" || response.Handle.Protocol != "tcp" {
		t.Fatalf("terminated handle = %+v", response.Handle)
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(wire)), "PRIVATE KEY") || strings.Contains(string(wire), "BEGIN ") {
		t.Fatalf("tls response carried material: %s", wire)
	}
	if _, err := tlsClient.TerminatePublicCertificateTLS(t.Context(), PublicCertificateTLSRequest{Handle: handle, RequestID: "request-a"}); err == nil {
		t.Fatal("missing certificate id was sent")
	}
	var failure *RuntimeError
	if err := (PublicCertificateListRequest{}).Validate(); err == nil || errors.As(err, &failure) {
		t.Fatal("empty agent id was accepted")
	}
}
