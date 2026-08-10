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

func agentAttemptServiceDesc(cookie string) *grpc.ServiceDesc {
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
		return response, nil
	}}}
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
			return response, nil
		}})
	}
	return &grpc.ServiceDesc{ServiceName: rpcServiceName, HandlerType: (*interface{})(nil), Methods: methods}
}
