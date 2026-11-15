package rpc

import (
	"context"
	"testing"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestFinishHostRuntimeResponseKeepsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := finishHostRuntimeResponse(ctx, sdk.HostRuntimeResponse{Error: &sdk.RuntimeError{Code: sdk.ErrorDeadlineExceeded, Message: "managed Host operation failed"}})
	if response.Error == nil || response.Error.Code != sdk.ErrorDeadlineExceeded {
		t.Fatalf("deadline response = %+v", response.Error)
	}
	replaced := finishHostRuntimeResponse(ctx, sdk.HostRuntimeResponse{Payload: []byte("payload")})
	if replaced.Error == nil || replaced.Error.Code != sdk.ErrorUnavailable || replaced.Error.Message != "managed generation call canceled" || len(replaced.Payload) != 0 {
		t.Fatalf("canceled response = %+v", replaced)
	}
}

func TestDispatchHostRuntimePublicCertificateListStaysUnsupported(t *testing.T) {
	host := &Host{}
	response := host.dispatchHostRuntime(context.Background(), HostCandidate{}, nil, &hostAttempt{}, sdk.HostRuntimeCall{Operation: sdk.HostRuntimePublicCertificate, Payload: []byte(`{"agent_id":"edge-a","key_pem":"SUPER-PRIVATE-KEY-MATERIAL"}`)})
	if response.Error == nil || response.Error.Code != sdk.ErrorInvalidArgument || response.Error.Message != "HostRuntime operation unsupported" || len(response.Payload) != 0 {
		t.Fatalf("list response = %+v", response)
	}
	if response.Error != nil && (containsPrivateMaterial(response.Error.Message)) {
		t.Fatal(response.Error.Message)
	}
}
