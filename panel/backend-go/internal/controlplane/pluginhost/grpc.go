package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const rpcCookieMetadata = "x-nre-plugin-cookie"

type GRPCDialer struct{}

func (GRPCDialer) Dial(_ context.Context, endpoint Endpoint, deadline time.Duration) (RPCClient, io.Closer, error) {
	if strings.TrimSpace(endpoint.Cookie) == "" {
		return nil, nil, errors.New("control-plane plugin RPC cookie is required")
	}
	network := strings.ToLower(strings.TrimSpace(endpoint.Network))
	if network != "unix" && network != "tcp" {
		return nil, nil, errors.New("control-plane plugin RPC endpoint must be unix or loopback tcp")
	}
	if network == "tcp" {
		host, _, err := net.SplitHostPort(endpoint.Address)
		if err != nil {
			return nil, nil, err
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, nil, errors.New("control-plane plugin RPC endpoint must be loopback")
		}
		if endpoint.TLSConfig == nil || endpoint.TLSConfig.InsecureSkipVerify || strings.TrimSpace(endpoint.TLSConfig.ServerName) == "" || len(endpoint.TLSConfig.Certificates) == 0 || endpoint.TLSConfig.RootCAs == nil {
			return nil, nil, errors.New("control-plane plugin loopback RPC requires mutual TLS")
		}
	}
	transport := credentials.TransportCredentials(insecure.NewCredentials())
	if endpoint.TLSConfig != nil {
		transport = credentials.NewTLS(endpoint.TLSConfig.Clone())
	}
	conn, err := grpc.NewClient("passthrough:///nre-control-plane-plugin", grpc.WithTransportCredentials(transport), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint.Address)
	}))
	if err != nil {
		return nil, nil, err
	}
	client, err := newDynamicRPCClient(conn, endpoint.Cookie, deadline)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return client, conn, nil
}

func validateEndpoint(runtimeDirectory string, endpoint Endpoint) error {
	network := strings.ToLower(strings.TrimSpace(endpoint.Network))
	if network == "unix" {
		root, err := filepath.Abs(runtimeDirectory)
		if err != nil {
			return err
		}
		address, err := filepath.Abs(endpoint.Address)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, address)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("control-plane plugin unix endpoint escapes runtime directory")
		}
		info, err := os.Stat(filepath.Dir(address))
		if err != nil {
			return err
		}
		if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
			return errors.New("control-plane plugin unix endpoint directory must be private")
		}
	}
	return nil
}

type dynamicRPCClient struct {
	conn     grpc.ClientConnInterface
	cookie   string
	deadline time.Duration
	messages map[string]protoreflect.MessageDescriptor
}

func newDynamicRPCClient(conn grpc.ClientConnInterface, cookie string, deadline time.Duration) (*dynamicRPCClient, error) {
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	names := []string{"HandshakeRequest", "HandshakeResponse", "LifecycleRequest", "LifecycleResponse", "ActionRequest", "ActionResponse"}
	messages := map[string]protoreflect.MessageDescriptor{}
	for _, name := range names {
		descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.rpc.v1." + name))
		if err != nil {
			return nil, err
		}
		messages[name] = descriptor
	}
	return &dynamicRPCClient{conn: conn, cookie: cookie, deadline: deadline, messages: messages}, nil
}
func (c *dynamicRPCClient) Handshake(ctx context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	input := dynamicpb.NewMessage(c.messages["HandshakeRequest"])
	setRPCString(input, "abi", request.ABI)
	setRPCString(input, "plugin_id", request.PluginID)
	setRPCString(input, "plugin_version", request.PluginVersion)
	setRPCString(input, "package_digest", request.PackageDigest)
	setRPCString(input, "artifact_digest", request.ArtifactDigest)
	setRPCStrings(input, "granted_scopes", request.GrantedScopes)
	setRPCString(input, "generation", request.Generation)
	output := dynamicpb.NewMessage(c.messages["HandshakeResponse"])
	if err := c.invoke(ctx, "Handshake", input, output); err != nil {
		return pluginsdk.RPCHandshakeResponse{}, err
	}
	return pluginsdk.RPCHandshakeResponse{ABI: getRPCString(output, "abi"), Capabilities: getRPCStrings(output, "capabilities")}, nil
}
func (c *dynamicRPCClient) Prepare(ctx context.Context, r pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Prepare", r)
}
func (c *dynamicRPCClient) Activate(ctx context.Context, r pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Activate", r)
}
func (c *dynamicRPCClient) Stop(ctx context.Context, r pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Stop", r)
}
func (c *dynamicRPCClient) InvokeAction(ctx context.Context, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error) {
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	input := dynamicpb.NewMessage(c.messages["ActionRequest"])
	setRPCString(input, "generation", request.Generation)
	setRPCString(input, "action_id", request.ActionID)
	setRPCString(input, "target_kind", request.TargetKind)
	setRPCString(input, "target_id", request.TargetID)
	output := dynamicpb.NewMessage(c.messages["ActionResponse"])
	if err := c.invoke(ctx, "InvokeAction", input, output); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	successField := output.Descriptor().Fields().ByName("success")
	errorField := output.Descriptor().Fields().ByName("error")
	if output.Has(successField) == output.Has(errorField) {
		return pluginsdk.RPCActionResponse{}, errors.New("control-plane plugin action returned invalid result")
	}
	if output.Has(successField) {
		success := output.Get(successField).Message()
		return pluginsdk.RPCActionResponse{Accepted: success.Get(success.Descriptor().Fields().ByName("accepted")).Bool()}, nil
	}
	failure := output.Get(errorField).Message()
	return pluginsdk.RPCActionResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorCode(failure.Get(failure.Descriptor().Fields().ByName("code")).Enum()), Message: failure.Get(failure.Descriptor().Fields().ByName("message")).String(), Retryable: failure.Get(failure.Descriptor().Fields().ByName("retryable")).Bool()}}, nil
}
func (c *dynamicRPCClient) lifecycle(ctx context.Context, method string, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	input := dynamicpb.NewMessage(c.messages["LifecycleRequest"])
	setRPCString(input, "generation", request.Generation)
	input.Set(input.Descriptor().Fields().ByName("config"), protoreflect.ValueOfBytes(append([]byte(nil), request.Config...)))
	output := dynamicpb.NewMessage(c.messages["LifecycleResponse"])
	if err := c.invoke(ctx, method, input, output); err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
	successField := output.Descriptor().Fields().ByName("success")
	errorField := output.Descriptor().Fields().ByName("error")
	if output.Has(successField) == output.Has(errorField) {
		return pluginsdk.LifecycleResponse{}, errors.New("control-plane plugin lifecycle returned invalid result")
	}
	if output.Has(successField) {
		success := output.Get(successField).Message()
		return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: success.Get(success.Descriptor().Fields().ByName("ready")).Bool()}}, nil
	}
	failure := output.Get(errorField).Message()
	return pluginsdk.LifecycleResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorCode(failure.Get(failure.Descriptor().Fields().ByName("code")).Enum()), Message: failure.Get(failure.Descriptor().Fields().ByName("message")).String(), Retryable: failure.Get(failure.Descriptor().Fields().ByName("retryable")).Bool()}}, nil
}
func (c *dynamicRPCClient) invoke(ctx context.Context, method string, input, output *dynamicpb.Message) error {
	callCtx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx, rpcCookieMetadata, c.cookie)
	if err := c.conn.Invoke(callCtx, "/nre.plugin.rpc.v1.PluginRuntime/"+method, input, output); err != nil {
		return fmt.Errorf("control-plane plugin RPC %s: %w", strings.ToLower(method), err)
	}
	return nil
}
func setRPCString(m *dynamicpb.Message, name protoreflect.Name, value string) {
	m.Set(m.Descriptor().Fields().ByName(name), protoreflect.ValueOfString(value))
}
func setRPCStrings(m *dynamicpb.Message, name protoreflect.Name, values []string) {
	list := m.Mutable(m.Descriptor().Fields().ByName(name)).List()
	for _, value := range values {
		list.Append(protoreflect.ValueOfString(value))
	}
}
func getRPCString(m *dynamicpb.Message, name protoreflect.Name) string {
	return m.Get(m.Descriptor().Fields().ByName(name)).String()
}
func getRPCStrings(m *dynamicpb.Message, name protoreflect.Name) []string {
	list := m.Get(m.Descriptor().Fields().ByName(name)).List()
	values := make([]string, list.Len())
	for index := range values {
		values[index] = list.Get(index).String()
	}
	return values
}
