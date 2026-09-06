package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedTCPStreamPartialWriteEOFAndHalfClose(t *testing.T) {
	var delivered []byte
	var reads int
	var halfClosed bool
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		request, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		response := ManagedNetworkResponse{}
		switch request.Action {
		case ManagedNetworkWrite:
			if halfClosed {
				t.Fatal("write after half-close reached Host")
			}
			// Simulate a bounded Host buffer taking only part of each chunk.
			response.Written = min(4096, len(request.Data))
			delivered = append(delivered, request.Data[:response.Written]...)
		case ManagedNetworkRead:
			reads++
			response.Data, response.EOF = []byte("final"), true
		case ManagedNetworkHalfClose:
			halfClosed = true
			response.Done = true
		case ManagedNetworkClose:
			response.Done = true
		default:
			t.Fatal("unexpected action")
		}
		payload, _ := json.Marshal(response)
		return HostRuntimeResponse{Payload: payload}
	})
	stream, err := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	value := bytes.Repeat([]byte("payload"), 20000)
	if n, err := stream.Write(value); err != nil || n != len(value) || !bytes.Equal(delivered, value) {
		t.Fatalf("write n=%d err=%v received=%d", n, err, len(delivered))
	}
	if err := stream.CloseWrite(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after half-close = %v", err)
	}
	buffer := make([]byte, 10)
	if n, err := stream.Read(buffer); n != 5 || !errors.Is(err, io.EOF) || string(buffer[:n]) != "final" {
		t.Fatalf("final read n=%d err=%v", n, err)
	}
	if n, err := stream.Read(buffer); n != 0 || !errors.Is(err, io.EOF) || reads != 1 {
		t.Fatalf("cached EOF n=%d err=%v reads=%d", n, err, reads)
	}
}

func TestManagedTCPStreamNeverRetriesUncertainWrite(t *testing.T) {
	calls := 0
	client := managedTestClient(t, func(_ *http.Request, _ HostRuntimeCall) HostRuntimeResponse {
		calls++
		if calls == 1 {
			return HostRuntimeResponse{Payload: json.RawMessage(`{"written":2}`)}
		}
		return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorInternal, Message: "uncertain delivery", Retryable: true}}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	n, err := stream.Write([]byte("hello"))
	if n != 2 || err == nil || calls != 2 {
		t.Fatalf("uncertain write n=%d err=%v calls=%d", n, err, calls)
	}
	if n, err := stream.Write([]byte("llo")); n != 0 || !errors.Is(err, ErrManagedNetworkDirectionUncertain) || calls != 2 {
		t.Fatalf("uncertain write was reusable: n=%d err=%v calls=%d", n, err, calls)
	}
}

func TestManagedTCPStreamDeadlineAndCloseCancelPendingRead(t *testing.T) {
	for _, closeStream := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "close"}[closeStream], func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int32
			client := &HostRuntimeClient{credential: "test", client: &http.Client{Transport: hostRuntimeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var call HostRuntimeCall
				_ = json.NewDecoder(request.Body).Decode(&call)
				decoded, err := DecodeManagedNetworkRequest(call.Payload)
				if err != nil {
					t.Error(err)
					return nil, err
				}
				if decoded.Action == ManagedNetworkClose {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"payload":{"done":true}}`))}, nil
				}
				calls.Add(1)
				close(started)
				<-request.Context().Done()
				return nil, request.Context().Err()
			})}}
			stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
			ctx := t.Context()
			cancel := func() {}
			if !closeStream {
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
			}
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := stream.ReadContext(ctx, make([]byte, 10)); finished <- err }()
			<-started
			want := context.DeadlineExceeded
			if closeStream {
				want = net.ErrClosed
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-finished:
				if !errors.Is(err, want) {
					t.Fatalf("cancel error %v want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("read did not cancel")
			}
			if calls.Load() != 1 {
				t.Fatal("cancelled read retried")
			}
			if !closeStream {
				if _, err := stream.ReadContext(t.Context(), make([]byte, 10)); !errors.Is(err, ErrManagedNetworkDirectionUncertain) || calls.Load() != 1 {
					t.Fatalf("dispatched timeout left read reusable: %v", err)
				}
			}
		})
	}
}

func TestManagedTCPStreamRenewsOnlySafeIdlePolls(t *testing.T) {
	calls := 0
	ids := make(map[string]bool)
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		request, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if request.Action != ManagedNetworkRead || request.WaitMS != ManagedNetworkMaxWaitMS {
			t.Fatal("invalid read poll")
		}
		if ids[request.RequestID] {
			t.Fatal("poll reused operation identity")
		}
		ids[request.RequestID] = true
		calls++
		if calls <= 3 {
			return HostRuntimeResponse{Payload: json.RawMessage(`{"idle":true}`)}
		}
		return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"aGVsbG8=","eof":true}`)}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	result, err := io.ReadAll(stream)
	if err != nil || string(result) != "hello" || calls != 4 {
		t.Fatalf("idle polls truncated stream: %q %v calls=%d", result, err, calls)
	}
}

func TestManagedTCPStreamCancellationAfterSafeIdleDoesNotPoisonRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	client := managedTestClient(t, func(_ *http.Request, _ HostRuntimeCall) HostRuntimeResponse {
		calls++
		if calls == 1 {
			cancel()
			return HostRuntimeResponse{Payload: json.RawMessage(`{"idle":true}`)}
		}
		return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"eA=="}`)}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	if _, err := stream.ReadContext(ctx, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("idle renewal ignored cancellation: %v", err)
	}
	if n, err := stream.Read(make([]byte, 1)); err != nil || n != 1 || calls != 2 {
		t.Fatalf("safe cancellation poisoned read: %d %v calls=%d", n, err, calls)
	}
}

func TestManagedTCPStreamAmbiguousConsumedReadPreventsReuse(t *testing.T) {
	reads := 0
	client := &HostRuntimeClient{credential: "test", client: &http.Client{Transport: hostRuntimeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var call HostRuntimeCall
		_ = json.NewDecoder(request.Body).Decode(&call)
		decoded, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Action == ManagedNetworkRead {
			reads++ // Host consumed the first byte but the reply was lost.
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"payload":{"written":1}}`))}, nil
	})}}
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost reply = %v", err)
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, ErrManagedNetworkDirectionUncertain) || reads != 1 {
		t.Fatalf("lost byte silently skipped: %v reads=%d", err, reads)
	}
	if n, err := stream.Write([]byte("x")); n != 1 || err != nil {
		t.Fatalf("read failure blocked opposite direction: %d %v", n, err)
	}
}

func TestManagedTCPStreamCancelledBeforeDispatchLeavesDirectionsReusable(t *testing.T) {
	calls := 0
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		calls++
		request, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if request.Action == ManagedNetworkRead {
			return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"eA=="}`)}
		}
		return HostRuntimeResponse{Payload: json.RawMessage(`{"written":1}`)}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := stream.ReadContext(ctx, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := stream.WriteContext(ctx, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("pre-cancelled I/O was dispatched")
	}
	if n, err := stream.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("untouched read poisoned: %d %v", n, err)
	}
	if n, err := stream.Write([]byte("x")); n != 1 || err != nil {
		t.Fatalf("untouched write poisoned: %d %v", n, err)
	}
}

func TestManagedTCPStreamHostTimeoutIsNotASafeIdlePoll(t *testing.T) {
	calls := 0
	client := managedTestClient(t, func(_ *http.Request, _ HostRuntimeCall) HostRuntimeResponse {
		calls++
		return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorDeadlineExceeded, Message: "timeout", Retryable: true}}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	if _, err := stream.Read(make([]byte, 1)); err == nil || calls != 1 {
		t.Fatal("host timeout was renewed")
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, ErrManagedNetworkDirectionUncertain) || calls != 1 {
		t.Fatal("ambiguous Host timeout was reusable")
	}
}

func TestManagedTCPStreamCancelBetweenAcknowledgedWritesCanResume(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var received []byte
	calls := 0
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		request, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		calls++
		written := len(request.Data)
		if calls == 1 {
			written = 2
			cancel()
		}
		received = append(received, request.Data[:written]...)
		payload, _ := json.Marshal(ManagedNetworkResponse{Written: written})
		return HostRuntimeResponse{Payload: payload}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	if n, err := stream.WriteContext(ctx, []byte("hello")); n != 2 || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("acknowledged cancellation n=%d err=%v calls=%d", n, err, calls)
	}
	if n, err := stream.Write([]byte("llo")); n != 3 || err != nil || string(received) != "hello" {
		t.Fatalf("safe continuation n=%d err=%v received=%q", n, err, received)
	}
}

func TestManagedTCPStreamWriteProceedsWhileReadPollIsPending(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		request, err := DecodeManagedNetworkRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if request.Action == ManagedNetworkRead {
			close(started)
			<-release
			return HostRuntimeResponse{Payload: json.RawMessage(`{"data":"eA=="}`)}
		}
		return HostRuntimeResponse{Payload: json.RawMessage(`{"written":1}`)}
	})
	stream, _ := NewManagedTCPStream(t.Context(), client, managedTestHandle("stream", "tcp"))
	finished := make(chan error, 1)
	go func() { _, err := stream.Read(make([]byte, 1)); finished <- err }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	n, err := stream.WriteContext(ctx, []byte("x"))
	close(release)
	if n != 1 || err != nil {
		t.Fatalf("pending read blocked writer: %d %v", n, err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestManagedTCPStreamQueuedReadHonorsDeadline(t *testing.T) {
	started := make(chan struct{})
	client := &HostRuntimeClient{credential: "test", client: &http.Client{Transport: hostRuntimeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}}
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, _ := NewManagedTCPStream(lifetime, client, managedTestHandle("stream", "tcp"))
	finished := make(chan error, 1)
	go func() { _, err := stream.Read(make([]byte, 1)); finished <- err }()
	<-started
	ctx, deadlineCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer deadlineCancel()
	if _, err := stream.ReadContext(ctx, make([]byte, 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued read did not honor deadline: %v", err)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("initial read did not cancel")
	}
}
