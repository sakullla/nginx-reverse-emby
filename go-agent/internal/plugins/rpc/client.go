// Package rpc implements the host side of the canonical nre:rpc/v1 local
// gRPC ABI. It uses the SDK descriptor set directly so the wire contract has a
// single owner even though generated stubs are intentionally not checked in.
package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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

const (
	CookieMetadataKey = "x-nre-plugin-cookie"
	rpcServiceName    = "nre.plugin.rpc.v1.PluginRuntime"
)

type DialConfig struct {
	Network     string
	Address     string
	Cookie      string
	TLSConfig   *tls.Config
	Deadline    time.Duration
	RuntimeRoot string
}

type Client struct {
	conn     grpc.ClientConnInterface
	cookie   string
	deadline time.Duration
	messages map[string]protoreflect.MessageDescriptor
}

func Dial(ctx context.Context, cfg DialConfig) (*Client, func() error, error) {
	if strings.TrimSpace(cfg.Cookie) == "" {
		return nil, nil, errors.New("RPC plugin cookie is required")
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network != "unix" && network != "tcp" {
		return nil, nil, errors.New("RPC plugin endpoint must use unix or loopback tcp")
	}
	if network == "unix" {
		if err := validateUnixEndpoint(cfg.RuntimeRoot, cfg.Address); err != nil {
			return nil, nil, err
		}
	}
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, nil, errors.New("RPC plugin endpoint address is required")
	}
	if network == "tcp" {
		host, _, err := net.SplitHostPort(cfg.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("parse RPC plugin endpoint: %w", err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, nil, errors.New("RPC plugin tcp endpoint must be loopback")
		}
		if cfg.TLSConfig == nil || cfg.TLSConfig.InsecureSkipVerify || strings.TrimSpace(cfg.TLSConfig.ServerName) == "" || len(cfg.TLSConfig.Certificates) == 0 || cfg.TLSConfig.RootCAs == nil {
			return nil, nil, errors.New("RPC plugin loopback tcp requires mutual TLS material")
		}
	}
	transport := credentials.TransportCredentials(insecure.NewCredentials())
	if cfg.TLSConfig != nil {
		transport = credentials.NewTLS(cfg.TLSConfig.Clone())
	}
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, cfg.Address)
	}
	conn, err := grpc.NewClient("passthrough:///nre-plugin", grpc.WithTransportCredentials(transport), grpc.WithContextDialer(dialer))
	if err != nil {
		return nil, nil, fmt.Errorf("create RPC plugin gRPC client: %w", err)
	}
	client, err := NewClient(conn, cfg.Cookie, cfg.Deadline)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, client.deadline)
	defer cancel()
	conn.Connect()
	select {
	case <-probeCtx.Done():
		_ = conn.Close()
		return nil, nil, probeCtx.Err()
	default:
	}
	return client, conn.Close, nil
}

func validateUnixEndpoint(root, address string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(address) == "" {
		return errors.New("RPC plugin unix endpoint requires a managed runtime root and address")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absoluteAddress, err := filepath.Abs(address)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absoluteRoot, absoluteAddress)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("RPC plugin unix endpoint escapes the managed runtime root")
	}
	parent := filepath.Dir(absoluteAddress)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("stat RPC plugin unix endpoint directory: %w", err)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return errors.New("RPC plugin unix endpoint directory must be private")
	}
	return nil
}

func NewClient(conn grpc.ClientConnInterface, cookie string, deadline time.Duration) (*Client, error) {
	if conn == nil {
		return nil, errors.New("RPC plugin gRPC connection is required")
	}
	if strings.TrimSpace(cookie) == "" {
		return nil, errors.New("RPC plugin cookie is required")
	}
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	names := []string{"HandshakeRequest", "HandshakeResponse", "LifecycleRequest", "LifecycleResponse", "ActionRequest", "ActionQueryRequest", "ActionResponse"}
	messages := make(map[string]protoreflect.MessageDescriptor, len(names))
	for _, name := range names {
		descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.rpc.v1." + name))
		if err != nil {
			return nil, err
		}
		messages[name] = descriptor
	}
	return &Client{conn: conn, cookie: cookie, deadline: deadline, messages: messages}, nil
}

func (c *Client) Handshake(ctx context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	message := dynamicpb.NewMessage(c.messages["HandshakeRequest"])
	setString(message, "abi", request.ABI)
	setString(message, "plugin_id", request.PluginID)
	setString(message, "plugin_version", request.PluginVersion)
	setString(message, "package_digest", request.PackageDigest)
	setString(message, "artifact_digest", request.ArtifactDigest)
	setStrings(message, "granted_scopes", request.GrantedScopes)
	setString(message, "generation", request.Generation)
	response := dynamicpb.NewMessage(c.messages["HandshakeResponse"])
	if err := c.invoke(ctx, "Handshake", message, response); err != nil {
		return pluginsdk.RPCHandshakeResponse{}, err
	}
	result := pluginsdk.RPCHandshakeResponse{ABI: getString(response, "abi"), Capabilities: getStrings(response, "capabilities")}
	if err := ValidateHandshake(request, result); err != nil {
		return pluginsdk.RPCHandshakeResponse{}, err
	}
	return result, nil
}

func (c *Client) Prepare(ctx context.Context, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Prepare", request)
}
func (c *Client) Activate(ctx context.Context, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Activate", request)
}
func (c *Client) Stop(ctx context.Context, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return c.lifecycle(ctx, "Stop", request)
}

func (c *Client) InvokeAction(ctx context.Context, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error) {
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	message := dynamicpb.NewMessage(c.messages["ActionRequest"])
	setString(message, "generation", request.Generation)
	setString(message, "action_id", request.ActionID)
	setString(message, "target_kind", request.TargetKind)
	setString(message, "target_id", request.TargetID)
	setString(message, "operation_id", request.OperationID)
	setString(message, "resource_handle", request.ResourceHandle)
	response := dynamicpb.NewMessage(c.messages["ActionResponse"])
	if err := c.invoke(ctx, "InvokeAction", message, response); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	return decodeActionResponse(response)
}

func (c *Client) QueryAction(ctx context.Context, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	if err := request.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	message := dynamicpb.NewMessage(c.messages["ActionQueryRequest"])
	setString(message, "generation", request.Generation)
	setString(message, "operation_id", request.OperationID)
	response := dynamicpb.NewMessage(c.messages["ActionResponse"])
	if err := c.invoke(ctx, "QueryAction", message, response); err != nil {
		return pluginsdk.RPCActionResponse{}, err
	}
	return decodeActionResponse(response)
}

func decodeActionResponse(response *dynamicpb.Message) (pluginsdk.RPCActionResponse, error) {
	result := pluginsdk.RPCActionResponse{OperationID: getString(response, "operation_id")}
	successField := response.Descriptor().Fields().ByName("success")
	errorField := response.Descriptor().Fields().ByName("error")
	pendingField := response.Descriptor().Fields().ByName("pending")
	missingField := response.Descriptor().Fields().ByName("missing")
	branches := 0
	for _, field := range []protoreflect.FieldDescriptor{successField, errorField, pendingField, missingField} {
		if response.Has(field) {
			branches++
		}
	}
	if branches != 1 {
		return pluginsdk.RPCActionResponse{}, errors.New("Agent RPC plugin action returned invalid result")
	}
	if response.Has(successField) {
		success := response.Get(successField).Message()
		result.Accepted = success.Get(success.Descriptor().Fields().ByName("accepted")).Bool()
	} else if response.Has(errorField) {
		failure := response.Get(errorField).Message()
		result.Error = &pluginsdk.RuntimeError{Code: pluginsdk.ErrorCode(failure.Get(failure.Descriptor().Fields().ByName("code")).Enum()), Message: failure.Get(failure.Descriptor().Fields().ByName("message")).String(), Retryable: failure.Get(failure.Descriptor().Fields().ByName("retryable")).Bool()}
	} else if response.Has(pendingField) {
		result.Pending = true
	} else {
		result.Missing = true
	}
	if err := result.Validate(); err != nil {
		return pluginsdk.RPCActionResponse{}, fmt.Errorf("Agent RPC plugin action response: %w", err)
	}
	return result, nil
}

func (c *Client) lifecycle(ctx context.Context, method string, request pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	message := dynamicpb.NewMessage(c.messages["LifecycleRequest"])
	setString(message, "generation", request.Generation)
	setBytes(message, "config", request.Config)
	response := dynamicpb.NewMessage(c.messages["LifecycleResponse"])
	if err := c.invoke(ctx, method, message, response); err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
	result, err := decodeLifecycle(response)
	if err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
	if err := result.Validate(); err != nil {
		return pluginsdk.LifecycleResponse{}, fmt.Errorf("RPC plugin %s response: %w", strings.ToLower(method), err)
	}
	return result, nil
}

func (c *Client) invoke(ctx context.Context, method string, request, response *dynamicpb.Message) error {
	callCtx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx, CookieMetadataKey, c.cookie)
	if err := c.conn.Invoke(callCtx, "/"+rpcServiceName+"/"+method, request, response); err != nil {
		return fmt.Errorf("RPC plugin %s: %w", strings.ToLower(method), err)
	}
	return nil
}

func ValidateHandshake(request pluginsdk.RPCHandshakeRequest, response pluginsdk.RPCHandshakeResponse) error {
	if request.ABI != pluginsdk.RPCABIV1 || response.ABI != request.ABI {
		return errors.New("RPC plugin handshake ABI mismatch")
	}
	if strings.TrimSpace(request.PluginID) == "" || strings.TrimSpace(request.PluginVersion) == "" || strings.TrimSpace(request.PackageDigest) == "" || strings.TrimSpace(request.ArtifactDigest) == "" || strings.TrimSpace(request.Generation) == "" {
		return errors.New("RPC plugin handshake identity is incomplete")
	}
	granted := make(map[string]struct{}, len(request.GrantedScopes))
	for _, scope := range request.GrantedScopes {
		granted[strings.TrimSpace(scope)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(response.Capabilities))
	for _, capability := range response.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("RPC plugin handshake returned an empty capability")
		}
		if _, ok := granted[capability]; !ok {
			return fmt.Errorf("RPC plugin handshake returned ungranted capability %q", capability)
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("RPC plugin handshake returned duplicate capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func decodeLifecycle(message *dynamicpb.Message) (pluginsdk.LifecycleResponse, error) {
	descriptor := message.Descriptor()
	successField := descriptor.Fields().ByName("success")
	errorField := descriptor.Fields().ByName("error")
	hasSuccess, hasError := message.Has(successField), message.Has(errorField)
	if hasSuccess == hasError {
		return pluginsdk.LifecycleResponse{}, errors.New("RPC plugin lifecycle response must contain exactly one result")
	}
	if hasSuccess {
		success := message.Get(successField).Message()
		ready := success.Get(success.Descriptor().Fields().ByName("ready")).Bool()
		return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: ready}}, nil
	}
	runtimeError := message.Get(errorField).Message()
	code := pluginsdk.ErrorCode(runtimeError.Get(runtimeError.Descriptor().Fields().ByName("code")).Enum())
	return pluginsdk.LifecycleResponse{Error: &pluginsdk.RuntimeError{Code: code, Message: runtimeError.Get(runtimeError.Descriptor().Fields().ByName("message")).String(), Retryable: runtimeError.Get(runtimeError.Descriptor().Fields().ByName("retryable")).Bool()}}, nil
}

func field(message *dynamicpb.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}
func setString(message *dynamicpb.Message, name protoreflect.Name, value string) {
	message.Set(field(message, name), protoreflect.ValueOfString(value))
}
func setBytes(message *dynamicpb.Message, name protoreflect.Name, value []byte) {
	message.Set(field(message, name), protoreflect.ValueOfBytes(append([]byte(nil), value...)))
}
func setStrings(message *dynamicpb.Message, name protoreflect.Name, values []string) {
	list := message.Mutable(field(message, name)).List()
	for _, value := range values {
		list.Append(protoreflect.ValueOfString(value))
	}
}
func getString(message *dynamicpb.Message, name protoreflect.Name) string {
	return message.Get(field(message, name)).String()
}
func getStrings(message *dynamicpb.Message, name protoreflect.Name) []string {
	list := message.Get(field(message, name)).List()
	values := make([]string, list.Len())
	for i := range values {
		values[i] = list.Get(i).String()
	}
	return values
}
