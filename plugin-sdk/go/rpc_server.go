package pluginsdk

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	EnvPluginEndpoint   = "NRE_PLUGIN_ENDPOINT"
	EnvPluginCookieFile = "NRE_PLUGIN_COOKIE_FILE"
	rpcCookieMetadata   = "x-nre-plugin-cookie"
	rpcServiceName      = "nre.plugin.rpc.v1.PluginRuntime"
)

// RPCLifecycle is the complete lifecycle surface required by an rpc-service
// plugin. ServeRPCPlugin owns the transport and authentication details.
type RPCLifecycle interface {
	Handshake(context.Context, RPCHandshakeRequest) (RPCHandshakeResponse, error)
	Prepare(context.Context, LifecycleRequest) LifecycleResponse
	Activate(context.Context, LifecycleRequest) LifecycleResponse
	Stop(context.Context, LifecycleRequest) LifecycleResponse
}

// ServeRPCPlugin serves the canonical authenticated local RPC transport.
// Plugin authors should normally run this beside ServePluginUI and only
// implement RPCLifecycle.
func ServeRPCPlugin(ctx context.Context, lifecycle RPCLifecycle) error {
	if ctx == nil || lifecycle == nil {
		return errors.New("RPC plugin context and lifecycle are required")
	}
	endpoint := strings.TrimSpace(os.Getenv(EnvPluginEndpoint))
	network, address, ok := strings.Cut(endpoint, ":")
	if !ok || network != "unix" || !filepath.IsAbs(address) {
		return errors.New("NRE_PLUGIN_ENDPOINT must use an absolute unix socket")
	}
	cookiePath := strings.TrimSpace(os.Getenv(EnvPluginCookieFile))
	if cookiePath == "" || !filepath.IsAbs(cookiePath) {
		return errors.New("NRE_PLUGIN_COOKIE_FILE must be absolute")
	}
	cookie, err := os.ReadFile(cookiePath)
	if err != nil {
		return err
	}
	if len(cookie) == 0 || len(cookie) > 4096 || strings.TrimSpace(string(cookie)) == "" {
		return errors.New("RPC plugin cookie is invalid")
	}
	if stat, statErr := os.Lstat(address); statErr == nil {
		if stat.Mode()&os.ModeSocket == 0 {
			return errors.New("RPC plugin endpoint exists and is not a socket")
		}
		if err := os.Remove(address); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(address)
	}()
	if err := os.Chmod(address, 0o600); err != nil {
		return err
	}
	server := grpc.NewServer()
	server.RegisterService(rpcLifecycleServiceDesc(string(cookie), lifecycle), lifecycle)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		stopped := make(chan struct{})
		go func() { server.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			server.Stop()
		}
		return nil
	case err := <-done:
		if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

func rpcLifecycleServiceDesc(cookie string, lifecycle RPCLifecycle) *grpc.ServiceDesc {
	methods := []grpc.MethodDesc{{MethodName: "Handshake", Handler: rpcDynamicUnary(cookie, "HandshakeRequest", func(ctx context.Context, request *dynamicpb.Message) (*dynamicpb.Message, error) {
		result, err := lifecycle.Handshake(ctx, rpcDecodeHandshake(request))
		if err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return rpcEncodeHandshake(result)
	})}}
	for _, methodName := range []string{"Prepare", "Activate", "Stop"} {
		name := methodName
		methods = append(methods, grpc.MethodDesc{MethodName: name, Handler: rpcDynamicUnary(cookie, "LifecycleRequest", func(ctx context.Context, request *dynamicpb.Message) (*dynamicpb.Message, error) {
			wire := rpcDecodeLifecycleRequest(request)
			var result LifecycleResponse
			switch name {
			case "Prepare":
				result = lifecycle.Prepare(ctx, wire)
			case "Activate":
				result = lifecycle.Activate(ctx, wire)
			default:
				result = lifecycle.Stop(ctx, wire)
			}
			return rpcEncodeLifecycle(result)
		})})
	}
	return &grpc.ServiceDesc{ServiceName: rpcServiceName, HandlerType: (*interface{})(nil), Methods: methods}
}

type rpcDynamicHandler func(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)

func rpcDynamicUnary(cookie, requestName string, call rpcDynamicHandler) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		values := incoming.Get(rpcCookieMetadata)
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(cookie)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "RPC plugin capability rejected")
		}
		descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.rpc.v1." + requestName))
		if err != nil {
			return nil, err
		}
		request := dynamicpb.NewMessage(descriptor)
		if err := decode(request); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(ctx, request)
		}
		info := &grpc.UnaryServerInfo{FullMethod: "/" + rpcServiceName + "/" + requestName}
		return interceptor(ctx, request, info, func(ctx context.Context, request any) (any, error) {
			return call(ctx, request.(*dynamicpb.Message))
		})
	}
}

func rpcDecodeHandshake(message *dynamicpb.Message) RPCHandshakeRequest {
	return RPCHandshakeRequest{
		ABI: rpcGetString(message, "abi"), PluginID: rpcGetString(message, "plugin_id"), PluginVersion: rpcGetString(message, "plugin_version"),
		PackageDigest: rpcGetString(message, "package_digest"), ArtifactDigest: rpcGetString(message, "artifact_digest"),
		GrantedScopes: rpcGetStrings(message, "granted_scopes"), Generation: rpcGetString(message, "generation"), RequiredFeatures: rpcGetStrings(message, "required_features"),
	}
}

func rpcEncodeHandshake(result RPCHandshakeResponse) (*dynamicpb.Message, error) {
	message, err := rpcNewMessage("HandshakeResponse")
	if err != nil {
		return nil, err
	}
	rpcSetString(message, "abi", result.ABI)
	rpcSetStrings(message, "capabilities", result.Capabilities)
	rpcSetStrings(message, "features", result.Features)
	return message, nil
}

func rpcDecodeLifecycleRequest(message *dynamicpb.Message) LifecycleRequest {
	return LifecycleRequest{Generation: rpcGetString(message, "generation"), Config: append([]byte(nil), message.Get(rpcField(message, "config")).Bytes()...)}
}

func rpcEncodeLifecycle(result LifecycleResponse) (*dynamicpb.Message, error) {
	message, err := rpcNewMessage("LifecycleResponse")
	if err != nil {
		return nil, err
	}
	if result.Success != nil {
		success := message.Mutable(rpcField(message, "success")).Message()
		success.Set(success.Descriptor().Fields().ByName("ready"), protoreflect.ValueOfBool(result.Success.Ready))
		return message, nil
	}
	failure := result.Error
	if failure == nil {
		failure = &RuntimeError{Code: ErrorInternal, Message: "invalid lifecycle result"}
	}
	wire := message.Mutable(rpcField(message, "error")).Message()
	wire.Set(wire.Descriptor().Fields().ByName("code"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(failure.Code)))
	wire.Set(wire.Descriptor().Fields().ByName("message"), protoreflect.ValueOfString(failure.Message))
	wire.Set(wire.Descriptor().Fields().ByName("retryable"), protoreflect.ValueOfBool(failure.Retryable))
	return message, nil
}

func rpcNewMessage(name string) (*dynamicpb.Message, error) {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.rpc.v1." + name))
	if err != nil {
		return nil, err
	}
	return dynamicpb.NewMessage(descriptor), nil
}

func rpcField(message *dynamicpb.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}

func rpcSetString(message *dynamicpb.Message, name protoreflect.Name, value string) {
	message.Set(rpcField(message, name), protoreflect.ValueOfString(value))
}

func rpcSetStrings(message *dynamicpb.Message, name protoreflect.Name, values []string) {
	list := message.Mutable(rpcField(message, name)).List()
	for _, value := range values {
		list.Append(protoreflect.ValueOfString(value))
	}
}

func rpcGetString(message *dynamicpb.Message, name protoreflect.Name) string {
	return message.Get(rpcField(message, name)).String()
}

func rpcGetStrings(message *dynamicpb.Message, name protoreflect.Name) []string {
	list := message.Get(rpcField(message, name)).List()
	result := make([]string, list.Len())
	for index := range result {
		result[index] = list.Get(index).String()
	}
	return result
}
