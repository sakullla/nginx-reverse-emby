package rpc

import (
	"crypto/tls"
	"crypto/x509"
	"path/filepath"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestRPCHandshakeRejectsIdentityAndCapabilityMismatch(t *testing.T) {
	request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "example", PluginVersion: "1.0.0", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), GrantedScopes: []string{"relay.read"}, Generation: "g1"}
	if err := ValidateHandshake(request, pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}}); err != nil {
		t.Fatalf("valid handshake: %v", err)
	}
	if err := ValidateHandshake(request, pluginsdk.RPCHandshakeResponse{ABI: "nre:rpc/v2"}); err == nil {
		t.Fatal("ABI mismatch accepted")
	}
	if err := ValidateHandshake(request, pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"docker.write"}}); err == nil {
		t.Fatal("ungranted capability accepted")
	}
}

func TestRPCHandshakeRequiresDurableActionFeatureBeforeActivation(t *testing.T) {
	request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "plugin", PluginVersion: "1.0.0", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation", GrantedScopes: []string{string(pluginsdk.CapabilityUIDynamicActions)}, RequiredFeatures: []string{pluginsdk.RPCFeatureDurableActionsV1}}
	response := pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: append([]string(nil), request.GrantedScopes...)}
	if err := ValidateHandshake(request, response); err == nil {
		t.Fatal("expected old v1 action guest to fail feature negotiation")
	}
	response.Features = append([]string(nil), request.RequiredFeatures...)
	if err := ValidateHandshake(request, response); err != nil {
		t.Fatal(err)
	}
}

func TestRPCDialSecurityRejectsUntrustedTLSAndExternalUnixSocket(t *testing.T) {
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: "plugin", RootCAs: x509.NewCertPool(), Certificates: []tls.Certificate{{Certificate: [][]byte{{1}}}}}
	if _, _, err := Dial(t.Context(), DialConfig{Network: "tcp", Address: "127.0.0.1:1", Cookie: "cookie", TLSConfig: tlsConfig}); err == nil {
		t.Fatal("InsecureSkipVerify RPC transport accepted")
	}
	root := t.TempDir()
	if err := validateUnixEndpoint(root, filepath.Join(filepath.Dir(root), "external.sock")); err == nil {
		t.Fatal("external unix endpoint accepted")
	}
}

func TestRPCLifecycleRejectsFalseReadinessAndUnknownError(t *testing.T) {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.rpc.v1.LifecycleResponse"))
	if err != nil {
		t.Fatal(err)
	}
	message := dynamicpb.NewMessage(descriptor)
	successField := descriptor.Fields().ByName("success")
	success := dynamicpb.NewMessage(successField.Message())
	success.Set(success.Descriptor().Fields().ByName("ready"), protoreflect.ValueOfBool(false))
	message.Set(successField, protoreflect.ValueOfMessage(success))
	response, err := decodeLifecycle(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err == nil {
		t.Fatal("false readiness accepted")
	}
	message.Clear(successField)
	errorField := descriptor.Fields().ByName("error")
	runtimeError := dynamicpb.NewMessage(errorField.Message())
	runtimeError.Set(runtimeError.Descriptor().Fields().ByName("code"), protoreflect.ValueOfEnum(99))
	message.Set(errorField, protoreflect.ValueOfMessage(runtimeError))
	response, err = decodeLifecycle(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err == nil {
		t.Fatal("unknown runtime error code accepted")
	}
}
