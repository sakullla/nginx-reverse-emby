package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func managedTestBinding() ManagedBinding {
	return ManagedBinding{InstanceID: "instance-a", Generation: "generation-1", EntryID: "entry-a"}
}
func managedTestHandle(kind, protocol string) ManagedNetworkHandle {
	return ManagedNetworkHandle{Binding: managedTestBinding(), Token: strings.Repeat("a", 32), Kind: kind, Protocol: protocol}
}
func managedTestRequest(action, kind, protocol string) ManagedNetworkRequest {
	handle := managedTestHandle(kind, protocol)
	return ManagedNetworkRequest{Action: action, Binding: handle.Binding, RequestID: "request-a", Handle: &handle, WaitMS: 1000}
}

// This goes through the actual HostRuntime HTTP JSON transport and the public
// server decoder/validator. Actual Host socket admission belongs to Host tests.
func managedTestClient(t *testing.T, call func(*http.Request, HostRuntimeCall) HostRuntimeResponse) *HostRuntimeClient {
	t.Helper()
	return &HostRuntimeClient{credential: "test-credential", client: &http.Client{Transport: hostRuntimeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get(HeaderPluginHostCredential) != "test-credential" {
			t.Error("missing transport authentication")
		}
		var wire HostRuntimeCall
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		result := call(request, wire)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})}}
}

func TestManagedNetworkRoundTripAndBinding(t *testing.T) {
	request := managedTestRequest(ManagedNetworkRead, "stream", "tcp")
	request.MaxBytes = 32
	record := ManagedNetworkRecord{Handle: *request.Handle, Active: true, OriginPermission: PermissionManagedNetworkListen}
	client := managedTestClient(t, func(_ *http.Request, wire HostRuntimeCall) HostRuntimeResponse {
		if wire.Operation != HostRuntimeManagedNetwork || wire.OperationID != request.RequestID {
			t.Fatal("wrong network operation")
		}
		decoded, err := DecodeManagedNetworkRequest(wire.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateManagedNetworkBinding(decoded, managedTestBinding(), &record, []string{PermissionManagedNetworkListen}); err != nil {
			return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "denied"}}
		}
		return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"aGVsbG8=","eof":true}`)}
	})
	response, err := client.ManagedNetwork(t.Context(), request)
	if err != nil || string(response.Data) != "hello" || !response.EOF {
		t.Fatalf("read roundtrip = %+v, %v", response, err)
	}
	record.Active = false
	_, err = client.ManagedNetwork(t.Context(), request)
	var failure *RuntimeError
	if !errors.As(err, &failure) || failure.Code != ErrorPermissionDenied {
		t.Fatalf("revocation = %v", err)
	}
	record.Active = true
	for _, field := range []string{"instance", "generation", "entry", "token", "grant", "half-close"} {
		t.Run(field, func(t *testing.T) {
			caller, changed := managedTestBinding(), record
			grants := []string{PermissionManagedNetworkListen}
			switch field {
			case "instance":
				caller.InstanceID = "other"
			case "generation":
				caller.Generation = "old"
			case "entry":
				caller.EntryID = "other"
			case "token":
				changed.Handle.Token = strings.Repeat("b", 32)
			case "grant":
				grants = nil
			case "half-close":
				changed.ReadClosed = true
			}
			if ValidateManagedNetworkBinding(request, caller, &changed, grants) == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestManagedNetworkRejectsInvalidRequestsAndTrustClaims(t *testing.T) {
	valid := managedTestRequest(ManagedNetworkWrite, "stream", "tcp")
	valid.Data = []byte("hello")
	for name, mutate := range map[string]func(*ManagedNetworkRequest){
		"oversize":    func(r *ManagedNetworkRequest) { r.Data = make([]byte, ManagedNetworkMaxChunkBytes+1) },
		"empty write": func(r *ManagedNetworkRequest) { r.Data = nil },
		"binding":     func(r *ManagedNetworkRequest) { r.Binding.Generation = "another" },
		"handle":      func(r *ManagedNetworkRequest) { copy := *r.Handle; copy.Token = "forged"; r.Handle = &copy },
		"wait":        func(r *ManagedNetworkRequest) { r.WaitMS = ManagedNetworkMaxWaitMS + 1 },
		"unrelated":   func(r *ManagedNetworkRequest) { r.TargetRequestID = "another" },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if request.Validate() == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	encoded, _ := json.Marshal(valid)
	for _, field := range []string{`"trusted":true`, `"source":{"host":"1.1.1.1"}`, `"extra":1`} {
		payload := append([]byte(`{`+field+`,`), encoded[1:]...)
		if _, err := DecodeManagedNetworkRequest(payload); err == nil {
			t.Fatal("unknown trust claim accepted")
		}
	}
	if _, err := DecodeManagedNetworkRequest(append(encoded, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := DecodeManagedNetworkRequest(make([]byte, PluginHostPayloadMaxBytes+1)); err == nil {
		t.Fatal("oversized frame accepted")
	}
	for _, host := range []string{"http://example.com", "a/b", "a b", "user@host", "a..b", "-a.test", "[::1]", "::", "ff02::1", "fe80::1%eth0"} {
		if (ManagedNetworkEndpoint{Host: host, Port: 443}).Validate() == nil {
			t.Errorf("invalid target %q accepted", host)
		}
	}
	for _, host := range []string{"example.com", "127.0.0.1", "::1"} {
		if err := (ManagedNetworkEndpoint{Host: host, Port: 443}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedNetworkUDPAndHalfCloseSemantics(t *testing.T) {
	request := managedTestRequest(ManagedNetworkReceive, "datagram", "udp")
	request.MaxBytes = ManagedNetworkMaxDatagramBytes
	for _, data := range [][]byte{nil, []byte("first"), []byte("second"), make([]byte, ManagedNetworkMaxDatagramBytes)} {
		if err := (ManagedNetworkResponse{Data: data}).ValidateFor(request); err != nil {
			t.Fatal(err)
		}
	}
	if (ManagedNetworkResponse{EOF: true}).ValidateFor(request) == nil {
		t.Fatal("UDP EOF accepted")
	}
	if (ManagedNetworkResponse{Data: make([]byte, ManagedNetworkMaxDatagramBytes+1)}).ValidateFor(request) == nil {
		t.Fatal("oversized UDP accepted")
	}
	request.MaxBytes--
	if request.Validate() == nil {
		t.Fatal("partial datagram read accepted")
	}
	request = managedTestRequest(ManagedNetworkSend, "datagram", "udp")
	request.Data = []byte("packet")
	if (ManagedNetworkResponse{Written: 1}).ValidateFor(request) == nil {
		t.Fatal("partial datagram acknowledgement accepted")
	}
	request.Data = nil
	if err := (ManagedNetworkResponse{}).ValidateFor(request); err != nil {
		t.Fatal("empty UDP datagram rejected", err)
	}
	request = managedTestRequest(ManagedNetworkHalfClose, "datagram", "udp")
	request.WaitMS, request.Direction = 0, "write"
	if request.Validate() == nil {
		t.Fatal("UDP half-close accepted")
	}
	request = managedTestRequest(ManagedNetworkHalfClose, "stream", "tcp")
	request.WaitMS, request.Direction = 0, "write"
	if err := (ManagedNetworkResponse{Done: true}).ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	request.Direction = "both"
	if request.Validate() == nil {
		t.Fatal("ambiguous half-close accepted")
	}
}

func TestManagedNetworkListenDialAcceptAndCancel(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		request := ManagedNetworkRequest{Action: ManagedNetworkListen, Binding: managedTestBinding(), RequestID: "listen", Endpoint: &ManagedNetworkEndpoint{Host: "0.0.0.0", Port: 8388}, Protocol: protocol, MaxFlows: 64, IdleMS: 10000}
		listener := managedTestHandle("listener", protocol)
		if err := (ManagedNetworkResponse{Handle: &listener}).ValidateFor(request); err != nil {
			t.Fatal(err)
		}
		request = managedTestRequest(ManagedNetworkAccept, "listener", protocol)
		kind := "stream"
		if protocol == "udp" {
			kind = "datagram"
		}
		flow := managedTestHandle(kind, protocol)
		flow.Token = strings.Repeat("b", 32)
		source := &ManagedSourceMetadata{Peer: ManagedNetworkEndpoint{Host: "192.0.2.1", Port: 30000}, Source: ManagedNetworkEndpoint{Host: "192.0.2.1", Port: 30000}, Authority: "socket"}
		if err := (ManagedNetworkResponse{Handle: &flow, Source: source}).ValidateFor(request); err != nil {
			t.Fatal(err)
		}
		if (ManagedNetworkResponse{Handle: &flow}).ValidateFor(request) == nil {
			t.Fatal("source-free admission accepted")
		}
		source.Source.Host = "192.0.2.2"
		if source.Validate() == nil {
			t.Fatal("self-reported socket source accepted")
		}
		request = ManagedNetworkRequest{Action: ManagedNetworkDial, Binding: managedTestBinding(), RequestID: "dial", Endpoint: &ManagedNetworkEndpoint{Host: "example.com", Port: 443}, Protocol: protocol, WaitMS: 1000}
		if protocol == "udp" {
			request.IdleMS = 10000
		}
		if err := (ManagedNetworkResponse{Handle: &flow}).ValidateFor(request); err != nil {
			t.Fatal(err)
		}
		if ValidateManagedNetworkBinding(request, managedTestBinding(), nil, []string{PermissionManagedNetworkListen}) == nil {
			t.Fatal("listen grant authorized dial")
		}
	}
	request := ManagedNetworkRequest{Action: ManagedNetworkCancel, Binding: managedTestBinding(), RequestID: "cancel", TargetRequestID: "read"}
	if err := (ManagedNetworkResponse{Done: true}).ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	request.TargetRequestID = request.RequestID
	if request.Validate() == nil {
		t.Fatal("self cancellation accepted")
	}
}

func TestManagedNetworkClientRejectsInvalidResponse(t *testing.T) {
	request := managedTestRequest(ManagedNetworkRead, "stream", "tcp")
	request.MaxBytes = 1
	client := managedTestClient(t, func(_ *http.Request, _ HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"aGVsbG8="}`)}
	})
	if _, err := client.ManagedNetwork(context.Background(), request); err == nil {
		t.Fatal("client accepted oversized read")
	}
}

func TestManagedNetworkIdleIsOnlyANoConsumptionTCPReadResult(t *testing.T) {
	read := managedTestRequest(ManagedNetworkRead, "stream", "tcp")
	read.MaxBytes = 10
	if err := (ManagedNetworkResponse{Idle: true}).ValidateFor(read); err != nil {
		t.Fatal(err)
	}
	for _, response := range []ManagedNetworkResponse{
		{Idle: true, EOF: true}, {Idle: true, Data: []byte("consumed")},
		{Idle: true, Written: 1}, {Idle: true, Done: true},
	} {
		if response.ValidateFor(read) == nil {
			t.Fatal("ambiguous idle result accepted")
		}
	}
	write := managedTestRequest(ManagedNetworkWrite, "stream", "tcp")
	write.Data = []byte("x")
	if (ManagedNetworkResponse{Idle: true, Written: 1}).ValidateFor(write) == nil {
		t.Fatal("write accepted read-only idle flag")
	}
	receive := managedTestRequest(ManagedNetworkReceive, "datagram", "udp")
	receive.MaxBytes = ManagedNetworkMaxDatagramBytes
	if (ManagedNetworkResponse{Idle: true}).ValidateFor(receive) == nil {
		t.Fatal("UDP empty datagram confused with idle poll")
	}
}
