package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

const (
	HostRuntimeManagedNetwork                     = "network.managed"
	PermissionManagedNetworkListen                = "network.managed.listen"
	PermissionManagedNetworkDial                  = "network.managed.dial"
	CapabilityManagedNetworkListen HostCapability = PermissionManagedNetworkListen
	CapabilityManagedNetworkDial   HostCapability = PermissionManagedNetworkDial
	ManagedNetworkMaxChunkBytes                   = 64 << 10
	ManagedNetworkMaxDatagramBytes                = 65507
	ManagedNetworkMaxWaitMS                       = 30000
	ManagedNetworkMaxFlows                        = 4096
	ManagedNetworkMaxIdleMS                       = 300000
	ManagedNetworkListen                          = "listen"
	ManagedNetworkAccept                          = "accept"
	ManagedNetworkDial                            = "dial"
	ManagedNetworkRead                            = "read"
	ManagedNetworkWrite                           = "write"
	ManagedNetworkHalfClose                       = "half-close"
	ManagedNetworkReceive                         = "receive"
	ManagedNetworkSend                            = "send"
	ManagedNetworkCancel                          = "cancel"
	ManagedNetworkClose                           = "close"
)

// ManagedBinding identifies the authenticated instance generation and entry.
// It is a claim on the wire, never authority. Hosts compare it with the
// transport's authenticated caller and their resource table on every operation.
type ManagedBinding struct {
	InstanceID string `json:"instance_id"`
	Generation string `json:"generation"`
	EntryID    string `json:"entry_id"`
}

func (binding ManagedBinding) Validate() error {
	for _, value := range []string{binding.InstanceID, binding.Generation, binding.EntryID} {
		if ValidatePolicyIdentity(value) != nil {
			return errors.New("managed resource binding is invalid")
		}
	}
	return nil
}

// ManagedNetworkHandle is minted by the Host from at least 128 bits of random
// entropy. A syntactically valid token cannot authorize a resource by itself.
type ManagedNetworkHandle struct {
	Binding  ManagedBinding `json:"binding"`
	Token    string         `json:"token"`
	Kind     string         `json:"kind"`     // listener, stream, or datagram
	Protocol string         `json:"protocol"` // tcp or udp
}

func (handle ManagedNetworkHandle) Validate() error {
	if err := handle.Binding.Validate(); err != nil {
		return err
	}
	if !validManagedToken(handle.Token) {
		return errors.New("managed resource token is invalid")
	}
	if handle.Protocol != "tcp" && handle.Protocol != "udp" {
		return errors.New("managed network protocol is invalid")
	}
	if handle.Kind != "listener" && !(handle.Kind == "stream" && handle.Protocol == "tcp") && !(handle.Kind == "datagram" && handle.Protocol == "udp") {
		return errors.New("managed network handle kind is invalid")
	}
	return nil
}

func validManagedToken(token string) bool {
	if len(token) < 22 || len(token) > 128 {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type ManagedNetworkEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func (endpoint ManagedNetworkEndpoint) Validate() error { return endpoint.validate(false) }

func (endpoint ManagedNetworkEndpoint) validate(listen bool) error {
	if endpoint.Port < 1 || endpoint.Port > 65535 {
		return errors.New("managed network port is invalid")
	}
	if ip, err := netip.ParseAddr(endpoint.Host); err == nil {
		if ip.Zone() != "" || ip.IsMulticast() || (!listen && ip.IsUnspecified()) {
			return errors.New("managed network address is invalid")
		}
		return nil
	}
	if listen || len(endpoint.Host) > 253 || endpoint.Host == "" {
		return errors.New("managed network host is invalid")
	}
	for _, label := range strings.Split(endpoint.Host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("managed network hostname is invalid")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("managed network hostname is invalid")
			}
		}
	}
	return nil
}

// ManagedSourceMetadata is returned only after Host admission. Source is the
// Host-authenticated original source; Peer is the actual socket peer. Plugins
// cannot submit this structure to acquire network authority. Proxy/relay/XFF
// provenance requires the Host's independently authorized chain validation.
type ManagedSourceMetadata struct {
	Peer      ManagedNetworkEndpoint `json:"peer"`
	Source    ManagedNetworkEndpoint `json:"source"`
	Authority string                 `json:"authority"`
}

func (source ManagedSourceMetadata) Validate() error {
	for _, endpoint := range []ManagedNetworkEndpoint{source.Peer, source.Source} {
		if endpoint.Validate() != nil {
			return errors.New("managed source endpoint is invalid")
		}
		if _, err := netip.ParseAddr(endpoint.Host); err != nil {
			return errors.New("managed source must be an IP endpoint")
		}
	}
	switch source.Authority {
	case "socket":
		if source.Peer != source.Source {
			return errors.New("socket source differs from peer")
		}
	case "proxy", "xff", "relay":
	default:
		return errors.New("managed source authority is invalid")
	}
	return nil
}

// ManagedNetworkRequest is one bounded operation over HostRuntime. Hosts must
// cap concurrent operations and buffered bytes per handle and instance. Each
// stream direction has at most one in-flight operation; a successful write
// acknowledges bytes accepted under that budget. Never automatically retry a
// write/send after a transport failure: its delivery may be ambiguous.
//
// Accept returns an admitted TCP stream or UDP flow, before any payload is
// exposed to the plugin. UDP flows fix their peer and can receive/send any
// number of datagrams until idle expiry or close, independently of requests.
// Receive returns one complete datagram (including an empty datagram), never
// truncation or EOF. Oversized datagrams fail explicitly at the Host.
//
// Cancel targets a RequestID in this exact binding. The Host cancels on either
// transport context cancellation or explicit cancel. Half-close affects only
// the named TCP direction; full close invalidates the handle and pending calls.
type ManagedNetworkRequest struct {
	Action          string                  `json:"action"`
	Binding         ManagedBinding          `json:"binding"`
	RequestID       string                  `json:"request_id"`
	Handle          *ManagedNetworkHandle   `json:"handle,omitempty"`
	Endpoint        *ManagedNetworkEndpoint `json:"endpoint,omitempty"`
	Protocol        string                  `json:"protocol,omitempty"`
	MaxBytes        int                     `json:"max_bytes,omitempty"`
	Data            []byte                  `json:"data,omitempty"`
	WaitMS          int                     `json:"wait_ms,omitempty"`
	MaxFlows        int                     `json:"max_flows,omitempty"`
	IdleMS          int                     `json:"idle_ms,omitempty"`
	Direction       string                  `json:"direction,omitempty"`
	TargetRequestID string                  `json:"target_request_id,omitempty"`
}

func (request ManagedNetworkRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil {
		return err
	}
	if ValidatePolicyIdentity(request.RequestID) != nil {
		return errors.New("managed network request identity is invalid")
	}
	if request.Handle != nil {
		if err := request.Handle.Validate(); err != nil {
			return err
		}
		if request.Handle.Binding != request.Binding {
			return errors.New("managed network handle binding mismatch")
		}
	}
	// Remove fields only for their permitted operation; any remaining option
	// is rejected, preventing accidental privilege or transport ambiguity.
	options := request
	options.Action, options.Binding, options.RequestID = "", ManagedBinding{}, ""
	switch request.Action {
	case ManagedNetworkListen, ManagedNetworkDial:
		if request.Handle != nil || request.Endpoint == nil || (request.Protocol != "tcp" && request.Protocol != "udp") {
			return errors.New("managed network open requires endpoint and protocol")
		}
		if err := request.Endpoint.validate(request.Action == ManagedNetworkListen); err != nil {
			return err
		}
		if request.Action == ManagedNetworkListen {
			if request.MaxFlows < 1 || request.MaxFlows > ManagedNetworkMaxFlows || request.IdleMS < 1 || request.IdleMS > ManagedNetworkMaxIdleMS {
				return errors.New("managed listener flow budget is invalid")
			}
			options.MaxFlows, options.IdleMS = 0, 0
		} else if request.Protocol == "udp" {
			if request.IdleMS < 1 || request.IdleMS > ManagedNetworkMaxIdleMS {
				return errors.New("managed datagram idle budget is invalid")
			}
			options.IdleMS = 0
		}
		options.Endpoint, options.Protocol = nil, ""
	case ManagedNetworkAccept, ManagedNetworkRead, ManagedNetworkWrite, ManagedNetworkHalfClose, ManagedNetworkReceive, ManagedNetworkSend, ManagedNetworkClose:
		if request.Handle == nil {
			return errors.New("managed network operation requires a handle")
		}
		kind := request.Handle.Kind
		switch request.Action {
		case ManagedNetworkAccept:
			if kind != "listener" {
				return errors.New("accept requires a listener")
			}
		case ManagedNetworkRead:
			if kind != "stream" || request.MaxBytes < 1 || request.MaxBytes > ManagedNetworkMaxChunkBytes {
				return errors.New("stream read bound is invalid")
			}
			options.MaxBytes = 0
		case ManagedNetworkWrite:
			if kind != "stream" || len(request.Data) < 1 || len(request.Data) > ManagedNetworkMaxChunkBytes {
				return errors.New("stream write bound is invalid")
			}
			options.Data = nil
		case ManagedNetworkHalfClose:
			if kind != "stream" || (request.Direction != "read" && request.Direction != "write") {
				return errors.New("half-close requires a TCP direction")
			}
			options.Direction = ""
		case ManagedNetworkReceive:
			if kind != "datagram" || request.MaxBytes != ManagedNetworkMaxDatagramBytes {
				return errors.New("datagram receive requires the complete datagram bound")
			}
			options.MaxBytes = 0
		case ManagedNetworkSend:
			if kind != "datagram" || len(request.Data) > ManagedNetworkMaxDatagramBytes {
				return errors.New("datagram send bound is invalid")
			}
			options.Data = nil
		}
		options.Handle = nil
	case ManagedNetworkCancel:
		if ValidatePolicyIdentity(request.TargetRequestID) != nil || request.TargetRequestID == request.RequestID {
			return errors.New("cancel target identity is invalid")
		}
		options.TargetRequestID = ""
	default:
		return errors.New("managed network action is unsupported")
	}
	if request.Action != ManagedNetworkClose && request.Action != ManagedNetworkCancel && request.Action != ManagedNetworkHalfClose && request.Action != ManagedNetworkListen {
		if request.WaitMS < 1 || request.WaitMS > ManagedNetworkMaxWaitMS {
			return errors.New("managed network wait bound is invalid")
		}
		options.WaitMS = 0
	}
	if options.Handle != nil || options.Endpoint != nil || options.Protocol != "" || options.MaxBytes != 0 || len(options.Data) != 0 || options.WaitMS != 0 || options.MaxFlows != 0 || options.IdleMS != 0 || options.Direction != "" || options.TargetRequestID != "" {
		return errors.New("managed network action contains unrelated fields")
	}
	return nil
}

// ManagedNetworkResponse carries a single successful operation; failures use
// HostRuntimeResponse.Error, including revoked, denied and exhausted. Read may
// return final bytes and EOF together. An empty read is either EOF or Idle.
//
// Idle is a TCP-read-only success: WaitMS elapsed, no bytes were consumed from
// the socket or removed from Host buffers, no read remains in flight, and the
// stream is still live. Host must settle/cancel the underlying read before
// acknowledging Idle; a late completion must never swallow bytes. Idle must
// contain neither Data nor EOF. The client may then issue a new read with a new
// request ID. A RuntimeError timeout or any transport failure does NOT provide
// this guarantee and must never be treated as a safe poll renewal. This lets an
// ordinary idle TCP connection outlive one bounded Host operation.
//
// Written is exact for datagrams and may be partial for a TCP stream.
type ManagedNetworkResponse struct {
	Handle  *ManagedNetworkHandle  `json:"handle,omitempty"`
	Source  *ManagedSourceMetadata `json:"source,omitempty"`
	Data    []byte                 `json:"data,omitempty"`
	EOF     bool                   `json:"eof,omitempty"`
	Idle    bool                   `json:"idle,omitempty"`
	Written int                    `json:"written,omitempty"`
	Done    bool                   `json:"done,omitempty"`
}

func (response ManagedNetworkResponse) ValidateFor(request ManagedNetworkRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	remaining := response
	switch request.Action {
	case ManagedNetworkListen, ManagedNetworkDial, ManagedNetworkAccept:
		if response.Handle == nil || response.Handle.Validate() != nil || response.Handle.Binding != request.Binding {
			return errors.New("managed network response handle is invalid")
		}
		protocol := request.Protocol
		if request.Action == ManagedNetworkAccept {
			protocol = request.Handle.Protocol
		}
		kind := "stream"
		if protocol == "udp" {
			kind = "datagram"
		}
		if request.Action == ManagedNetworkListen {
			kind = "listener"
		}
		if response.Handle.Kind != kind || response.Handle.Protocol != protocol {
			return errors.New("managed network response handle type differs")
		}
		if request.Action == ManagedNetworkAccept {
			if response.Source == nil || response.Source.Validate() != nil || response.Handle.Token == request.Handle.Token {
				return errors.New("accepted flow source or token is invalid")
			}
			remaining.Source = nil
		}
		remaining.Handle = nil
	case ManagedNetworkRead:
		if len(response.Data) > request.MaxBytes || (len(response.Data) == 0 && !response.EOF && !response.Idle) || (response.Idle && (len(response.Data) != 0 || response.EOF)) {
			return errors.New("managed stream read result is invalid")
		}
		remaining.Data, remaining.EOF = nil, false
		remaining.Idle = false
	case ManagedNetworkReceive:
		if len(response.Data) > ManagedNetworkMaxDatagramBytes {
			return errors.New("managed datagram result is oversized")
		}
		remaining.Data = nil
	case ManagedNetworkWrite, ManagedNetworkSend:
		if response.Written < 0 || response.Written > len(request.Data) || (request.Action == ManagedNetworkWrite && response.Written == 0) || (request.Action == ManagedNetworkSend && response.Written != len(request.Data)) {
			return errors.New("managed network write acknowledgement is invalid")
		}
		remaining.Written = 0
	case ManagedNetworkClose, ManagedNetworkHalfClose, ManagedNetworkCancel:
		if !response.Done {
			return errors.New("managed network operation was not completed")
		}
		remaining.Done = false
	}
	if remaining.Handle != nil || remaining.Source != nil || len(remaining.Data) != 0 || remaining.EOF || remaining.Idle || remaining.Written != 0 || remaining.Done {
		return errors.New("managed network response contains unrelated fields")
	}
	return nil
}

// ManagedNetworkRecord is trusted Host table state, never decoded from plugin
// JSON. OriginPermission is fixed at creation and inherited by accepted flows.
// Host must hold its resource lock across validation and operation admission,
// independently verify target/entry scopes and quotas, and maintain half-close
// state. Active must include generation revocation and UDP idle expiry.
type ManagedNetworkRecord struct {
	Handle           ManagedNetworkHandle
	Active           bool
	OriginPermission string
	ReadClosed       bool
	WriteClosed      bool
}

func ValidateManagedNetworkBinding(request ManagedNetworkRequest, caller ManagedBinding, record *ManagedNetworkRecord, grants []string) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if caller.Validate() != nil || caller != request.Binding {
		return errors.New("managed network caller binding mismatch")
	}
	required := PermissionManagedNetworkListen
	if request.Action == ManagedNetworkDial {
		required = PermissionManagedNetworkDial
	}
	if request.Handle != nil {
		if record == nil || !record.Active || record.Handle != *request.Handle {
			return errors.New("managed network handle is unknown or revoked")
		}
		required = record.OriginPermission
		if (request.Action == ManagedNetworkRead && record.ReadClosed) || (request.Action == ManagedNetworkWrite && record.WriteClosed) {
			return errors.New("managed stream direction is closed")
		}
	}
	if request.Action == ManagedNetworkCancel {
		// The Host must separately look up TargetRequestID under caller, then
		// cancel only that in-flight call. No handle is supplied by the plugin.
		if hasManagedGrant(grants, PermissionManagedNetworkListen) || hasManagedGrant(grants, PermissionManagedNetworkDial) {
			return nil
		}
	}
	if (required != PermissionManagedNetworkListen && required != PermissionManagedNetworkDial) || !hasManagedGrant(grants, required) {
		return errors.New("managed network permission is missing")
	}
	return nil
}

func hasManagedGrant(grants []string, required string) bool {
	for _, grant := range grants {
		if grant == required {
			return true
		}
	}
	return false
}

// DecodeManagedNetworkRequest rejects unknown fields (including trusted/source)
// and enforces bounds before any Host resource lookup or operation.
func DecodeManagedNetworkRequest(payload json.RawMessage) (ManagedNetworkRequest, error) {
	var request ManagedNetworkRequest
	if err := decodeManagedPayload(payload, &request); err != nil {
		return request, err
	}
	return request, request.Validate()
}

func decodeManagedPayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 || len(payload) > PluginHostPayloadMaxBytes {
		return errors.New("managed payload exceeds the canonical bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("managed payload is invalid")
	}
	if ensureJSONDecoderEOF(decoder) != nil {
		return errors.New("managed payload contains trailing data")
	}
	return nil
}

func (client *HostRuntimeClient) ManagedNetwork(ctx context.Context, request ManagedNetworkRequest) (ManagedNetworkResponse, error) {
	var response ManagedNetworkResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimeManagedNetwork, OperationID: request.RequestID, Payload: payload}, &response); err != nil {
		return ManagedNetworkResponse{}, err
	}
	if err := response.ValidateFor(request); err != nil {
		return ManagedNetworkResponse{}, err
	}
	return response, nil
}
