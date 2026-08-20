package pluginhost

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type HostResourceDispatcher interface {
	DispatchPluginHostResource(context.Context, Candidate, pluginsdk.HostRuntimeCall) pluginsdk.HostRuntimeResponse
}

func startHostResourceServer(ctx context.Context, candidate Candidate, dispatcher HostResourceDispatcher) (func() error, error) {
	endpoint := candidate.hostEndpoint
	if endpoint.Network != "unix" || endpoint.Address == "" || endpoint.Cookie == "" {
		return nil, errors.New("control-plane plugin host resource endpoint is invalid")
	}
	listener, err := net.Listen(endpoint.Network, endpoint.Address)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint.Address, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	if candidate.sandboxUID != 0 {
		if err := os.Chown(endpoint.Address, candidate.sandboxUID, candidate.sandboxUID); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodPost || request.URL.Path != pluginsdk.PluginHostCallPath {
			writeHostRuntimeResponse(writer, http.StatusNotFound, pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorInvalidArgument, Message: "host resource call was not found"}})
			return
		}
		credential := request.Header.Get(pluginsdk.HeaderPluginHostCredential)
		if subtle.ConstantTimeCompare([]byte(credential), []byte(endpoint.Cookie)) != 1 {
			writeHostRuntimeResponse(writer, http.StatusForbidden, pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorPermissionDenied, Message: "host resource credential was rejected"}})
			return
		}
		payload, err := io.ReadAll(io.LimitReader(request.Body, pluginsdk.PluginHostPayloadMaxBytes+4096))
		if err != nil || len(payload) > pluginsdk.PluginHostPayloadMaxBytes+2048 {
			writeHostRuntimeResponse(writer, http.StatusRequestEntityTooLarge, pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorInvalidArgument, Message: "host resource request exceeds the canonical bound"}})
			return
		}
		var call pluginsdk.HostRuntimeCall
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&call); err != nil || call.Validate() != nil {
			writeHostRuntimeResponse(writer, http.StatusBadRequest, pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorInvalidArgument, Message: "host resource request is invalid"}})
			return
		}
		if dispatcher == nil {
			writeHostRuntimeResponse(writer, http.StatusServiceUnavailable, pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "host resource dispatcher is unavailable", Retryable: true}})
			return
		}
		response := dispatcher.DispatchPluginHostResource(request.Context(), candidate, call)
		if err := response.Validate(); err != nil {
			response = pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorInternal, Message: "host resource response is invalid"}}
		}
		status := http.StatusOK
		if response.Error != nil {
			status = http.StatusBadRequest
			if response.Error.Code == pluginsdk.ErrorPermissionDenied {
				status = http.StatusForbidden
			} else if response.Error.Code == pluginsdk.ErrorUnavailable {
				status = http.StatusServiceUnavailable
			}
		}
		writeHostRuntimeResponse(writer, status, response)
	})}
	go func() { _ = server.Serve(listener) }()
	stopContext := context.AfterFunc(ctx, func() { _ = server.Close() })
	var once sync.Once
	var closeErr error
	return func() error {
		once.Do(func() {
			stopContext()
			serverErr := server.Close()
			listenerErr := listener.Close()
			if errors.Is(serverErr, http.ErrServerClosed) || errors.Is(serverErr, net.ErrClosed) {
				serverErr = nil
			}
			if errors.Is(listenerErr, net.ErrClosed) {
				listenerErr = nil
			}
			closeErr = errors.Join(serverErr, listenerErr)
		})
		return closeErr
	}, nil
}

func writeHostRuntimeResponse(writer http.ResponseWriter, status int, response pluginsdk.HostRuntimeResponse) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}
