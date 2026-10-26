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
