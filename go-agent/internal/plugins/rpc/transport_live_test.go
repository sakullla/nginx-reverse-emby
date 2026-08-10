package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestRPCRealMutualTLSRestartUsesFreshIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("real TLS process transport belongs to the full tier")
	}
	var previousCookie string
	for attemptNumber := 0; attemptNumber < 2; attemptNumber++ {
		security, err := provisionAttemptSecurity(t.TempDir(), DialConfig{Network: "tcp", Address: "127.0.0.1:0", Deadline: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if security.dial.Cookie == previousCookie {
			t.Fatal("restart reused RPC cookie")
		}
		previousCookie = security.dial.Cookie
		listener, server, err := startAgentAttemptServer(security)
		if err != nil {
			t.Fatal(err)
		}
		security.dial.Address = listener.Addr().String()
		client, closeClient, err := Dial(t.Context(), security.dial)
		if err != nil {
			server.Stop()
			listener.Close()
			t.Fatal(err)
		}
		request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "plugin", PluginVersion: "1", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "g1", GrantedScopes: []string{"relay.read"}}
		if _, err := client.Handshake(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		plan, err := client.PlanAction(t.Context(), pluginsdk.RPCActionRequest{Generation: "g1", ActionID: "rotate", OperationID: "operation-plan", ResourceHandle: "handle-plan"})
		if err != nil || len(plan.Calls) != 1 || plan.Calls[0].ResourceHandle != "handle-plan" || plan.Calls[0].Operation != pluginsdk.RPCResourceInspect {
			t.Fatalf("real mutual-TLS resource plan=%+v error=%v", plan, err)
		}
		action, err := client.InvokeAction(t.Context(), pluginsdk.RPCActionRequest{Generation: "g1", ActionID: "rotate", OperationID: "operation-1", ResourceHandle: "handle-plan", ResourceResults: []pluginsdk.RPCResourceResult{{RequestID: "resource-call-1", Value: []byte(`{"available":true}`)}}})
		if err != nil || action.Validate() != nil {
			t.Fatalf("real mutual-TLS action dispatch failed: %+v, %v", action, err)
		}
		for _, test := range []struct {
			actionID  string
			operation string
			code      pluginsdk.ErrorCode
			retryable bool
		}{{"retryable", "operation-2", pluginsdk.ErrorUnavailable, true}, {"denied", "operation-3", pluginsdk.ErrorPermissionDenied, false}} {
			response, invokeErr := client.InvokeAction(t.Context(), pluginsdk.RPCActionRequest{Generation: "g1", ActionID: test.actionID, TargetKind: "relay", TargetID: "relay-1", OperationID: test.operation})
			if invokeErr != nil || response.Validate() != nil || response.OperationID != test.operation || response.Error == nil || response.Error.Code != test.code || response.Error.Retryable != test.retryable {
				t.Fatalf("real mutual-TLS typed action error %s response=%+v error=%v", test.actionID, response, invokeErr)
			}
		}
		_ = closeClient()
		server.Stop()
		_ = listener.Close()
		if err := security.cleanup(); err != nil {
			t.Fatal(err)
		}
	}
}

func startAgentAttemptServer(security attemptSecurity) (net.Listener, *grpc.Server, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(security.credentialDirectory, "server.crt"), filepath.Join(security.credentialDirectory, "server.key"))
	if err != nil {
		return nil, nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(security.credentialDirectory, "ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(caPEM) {
		return nil, nil, errors.New("load attempt client CA")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: clientRoots, ClientAuth: tls.RequireAndVerifyClientCert})))
	server.RegisterService(agentAttemptServiceDesc(security.dial.Cookie), struct{}{})
	go server.Serve(listener)
	return listener, server, nil
}

func agentAttemptServiceDesc(cookie string, stopCallbacks ...func()) *grpc.ServiceDesc {
	methods := []grpc.MethodDesc{{MethodName: "Handshake", Handler: func(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		requestDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.HandshakeRequest")
		if err != nil {
			return nil, err
		}
		request := dynamicpb.NewMessage(requestDescriptor)
		if err := decode(request); err != nil {
			return nil, err
		}
		incoming, _ := metadata.FromIncomingContext(ctx)
		if values := incoming.Get(CookieMetadataKey); len(values) != 1 || values[0] != cookie {
			return nil, errors.New("attempt cookie mismatch")
		}
		responseDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.HandshakeResponse")
		if err != nil {
			return nil, err
		}
		response := dynamicpb.NewMessage(responseDescriptor)
		response.Set(responseDescriptor.Fields().ByName(protoreflect.Name("abi")), protoreflect.ValueOfString(pluginsdk.RPCABIV1))
		capabilities := response.Mutable(responseDescriptor.Fields().ByName("capabilities")).List()
		capabilities.Append(protoreflect.ValueOfString("relay.read"))
		features := response.Mutable(responseDescriptor.Fields().ByName("features")).List()
		requestFeatures := request.Get(requestDescriptor.Fields().ByName("required_features")).List()
		for index := 0; index < requestFeatures.Len(); index++ {
			features.Append(requestFeatures.Get(index))
		}
		return response, nil
	}}}
	methods = append(methods, grpc.MethodDesc{MethodName: "PlanAction", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		requestDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionRequest")
		if err != nil {
			return nil, err
		}
		request := dynamicpb.NewMessage(requestDescriptor)
		if err := decode(request); err != nil {
			return nil, err
		}
		responseDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionPlanResponse")
		if err != nil {
			return nil, err
		}
		response := dynamicpb.NewMessage(responseDescriptor)
		handle := request.Get(requestDescriptor.Fields().ByName("resource_handle")).String()
		if handle != "" {
			callsField := responseDescriptor.Fields().ByName("calls")
			call := dynamicpb.NewMessage(callsField.Message())
			call.Set(call.Descriptor().Fields().ByName("request_id"), protoreflect.ValueOfString("resource-call-1"))
			call.Set(call.Descriptor().Fields().ByName("resource_handle"), protoreflect.ValueOfString(handle))
			call.Set(call.Descriptor().Fields().ByName("operation"), protoreflect.ValueOfEnum(1))
			response.Mutable(callsField).List().Append(protoreflect.ValueOfMessage(call))
		}
		return response, nil
	}})
	for _, name := range []string{"Prepare", "Activate", "Stop"} {
		methodName := name
		methods = append(methods, grpc.MethodDesc{MethodName: methodName, Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			requestDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.LifecycleRequest")
			if err != nil {
				return nil, err
			}
			request := dynamicpb.NewMessage(requestDescriptor)
			if err := decode(request); err != nil {
				return nil, err
			}
			responseDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.LifecycleResponse")
			if err != nil {
				return nil, err
			}
			response := dynamicpb.NewMessage(responseDescriptor)
			successField := responseDescriptor.Fields().ByName("success")
			success := response.Mutable(successField).Message()
			success.Set(success.Descriptor().Fields().ByName("ready"), protoreflect.ValueOfBool(true))
			if methodName == "Stop" {
				for _, callback := range stopCallbacks {
					go callback()
				}
			}
			return response, nil
		}})
	}
	methods = append(methods, grpc.MethodDesc{MethodName: "InvokeAction", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		requestDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionRequest")
		if err != nil {
			return nil, err
		}
		request := dynamicpb.NewMessage(requestDescriptor)
		if err := decode(request); err != nil {
			return nil, err
		}
		responseDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionResponse")
		if err != nil {
			return nil, err
		}
		response := dynamicpb.NewMessage(responseDescriptor)
		response.Set(responseDescriptor.Fields().ByName("operation_id"), request.Get(requestDescriptor.Fields().ByName("operation_id")))
		actionID := request.Get(requestDescriptor.Fields().ByName("action_id")).String()
		if actionID == "retryable" || actionID == "denied" {
			runtimeError := response.Mutable(responseDescriptor.Fields().ByName("error")).Message()
			code := protoreflect.EnumNumber(5)
			retryable := true
			message := "try later"
			if actionID == "denied" {
				code = protoreflect.EnumNumber(2)
				retryable = false
				message = "permission denied"
			}
			runtimeError.Set(runtimeError.Descriptor().Fields().ByName("code"), protoreflect.ValueOfEnum(code))
			runtimeError.Set(runtimeError.Descriptor().Fields().ByName("message"), protoreflect.ValueOfString(message))
			runtimeError.Set(runtimeError.Descriptor().Fields().ByName("retryable"), protoreflect.ValueOfBool(retryable))
			return response, nil
		}
		success := response.Mutable(responseDescriptor.Fields().ByName("success")).Message()
		success.Set(success.Descriptor().Fields().ByName("accepted"), protoreflect.ValueOfBool(true))
		return response, nil
	}})
	methods = append(methods, grpc.MethodDesc{MethodName: "QueryAction", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		requestDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionQueryRequest")
		if err != nil {
			return nil, err
		}
		request := dynamicpb.NewMessage(requestDescriptor)
		if err := decode(request); err != nil {
			return nil, err
		}
		responseDescriptor, err := protoschema.Message("nre.plugin.rpc.v1.ActionResponse")
		if err != nil {
			return nil, err
		}
		response := dynamicpb.NewMessage(responseDescriptor)
		response.Set(responseDescriptor.Fields().ByName("operation_id"), request.Get(requestDescriptor.Fields().ByName("operation_id")))
		response.Mutable(responseDescriptor.Fields().ByName("missing"))
		return response, nil
	}})
	return &grpc.ServiceDesc{ServiceName: rpcServiceName, HandlerType: (*interface{})(nil), Methods: methods}
}
