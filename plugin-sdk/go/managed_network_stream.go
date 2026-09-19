package pluginsdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ErrManagedNetworkDirectionUncertain means a previously dispatched operation
// failed without a trustworthy byte count. That direction must not be reused;
// close the stream or finish the unaffected direction and then close it.
var ErrManagedNetworkDirectionUncertain = errors.New("managed TCP direction has uncertain delivery")

// ManagedTCPStream is the canonical io.ReadWriteCloser adapter for a Host-owned
// TCP stream. ReadContext/WriteContext accept per-operation deadlines and
// cancellation; Read/Write use the lifetime context passed to the constructor.
// Callers needing timeouts should use those methods instead of reproducing the
// transport. One reader and one writer can operate concurrently. Requests are
// split at the public frame limit and only acknowledged bytes advance a write.
// Only explicit Idle read results renew the bounded Host poll. Failed dispatched
// I/O, including cancellation, permanently blocks reuse of that direction: the
// Host may have consumed/read or accepted/written bytes before the failure.
// Cancellation before dispatch leaves the direction reusable. CloseRead and
// CloseWrite preserve the opposite direction; Close cancels pending calls.
type ManagedTCPStream struct {
	client         *HostRuntimeClient
	handle         ManagedNetworkHandle
	ctx            context.Context
	cancel         context.CancelFunc
	readGate       chan struct{}
	writeGate      chan struct{}
	closed         atomic.Bool
	readClosed     atomic.Bool
	writeClosed    atomic.Bool
	readEOF        bool
	readUncertain  bool // guarded by readGate
	writeUncertain bool // guarded by writeGate
	closeOnce      sync.Once
	closeErr       error
}

func NewManagedTCPStream(ctx context.Context, client *HostRuntimeClient, handle ManagedNetworkHandle) (*ManagedTCPStream, error) {
	if ctx == nil || client == nil || handle.Validate() != nil || handle.Kind != "stream" {
		return nil, errors.New("managed TCP stream requires a client, context and TCP handle")
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &ManagedTCPStream{client: client, handle: handle, ctx: lifetime, cancel: cancel, readGate: make(chan struct{}, 1), writeGate: make(chan struct{}, 1)}, nil
}

func managedRequestID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}

func (stream *ManagedTCPStream) request(action string) ManagedNetworkRequest {
	return ManagedNetworkRequest{Action: action, Binding: stream.handle.Binding, Handle: &stream.handle, RequestID: managedRequestID(), WaitMS: ManagedNetworkMaxWaitMS}
}

func (stream *ManagedTCPStream) operationContext(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("managed TCP operation context is missing")
	}
	if stream.closed.Load() {
		return nil, nil, net.ErrClosed
	}
	if err := stream.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(stream.ctx, cancel)
	return operation, func() { stop(); cancel() }, nil
}

func (stream *ManagedTCPStream) Read(value []byte) (int, error) {
	return stream.ReadContext(stream.ctx, value)
}

func (stream *ManagedTCPStream) ReadContext(ctx context.Context, value []byte) (int, error) {
	operation, cancel, err := stream.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()
	select {
	case stream.readGate <- struct{}{}:
		defer func() { <-stream.readGate }()
	case <-operation.Done():
		return 0, stream.operationError(operation, operation.Err())
	}
	if stream.readClosed.Load() {
		return 0, net.ErrClosed
	}
	if stream.readUncertain {
		return 0, ErrManagedNetworkDirectionUncertain
	}
	if len(value) == 0 {
		return 0, nil
	}
	if stream.readEOF {
		return 0, io.EOF
	}
	for {
		if err := operation.Err(); err != nil {
			return 0, stream.operationError(operation, err)
		}
		if stream.readClosed.Load() {
			return 0, net.ErrClosed
		}
		request := stream.request(ManagedNetworkRead)
		request.MaxBytes = min(len(value), ManagedNetworkMaxChunkBytes)
		response, dispatched, err := stream.dispatch(operation, request)
		if err != nil {
			stream.readUncertain = dispatched
			return 0, stream.operationError(operation, err)
		}
		if response.Idle {
			continue
		}
		n := copy(value, response.Data)
		if response.EOF {
			stream.readEOF = true
			return n, io.EOF
		}
		return n, nil
	}
}

func (stream *ManagedTCPStream) Write(value []byte) (int, error) {
	return stream.WriteContext(stream.ctx, value)
}

func (stream *ManagedTCPStream) WriteContext(ctx context.Context, value []byte) (int, error) {
	operation, cancel, err := stream.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()
	select {
	case stream.writeGate <- struct{}{}:
		defer func() { <-stream.writeGate }()
	case <-operation.Done():
		return 0, stream.operationError(operation, operation.Err())
	}
	if stream.writeClosed.Load() {
		return 0, net.ErrClosed
	}
	if stream.writeUncertain {
		return 0, ErrManagedNetworkDirectionUncertain
	}
	total := 0
	for total < len(value) {
		if err := operation.Err(); err != nil {
			return total, stream.operationError(operation, err)
		}
		request := stream.request(ManagedNetworkWrite)
		request.Data = value[total : total+min(len(value)-total, ManagedNetworkMaxChunkBytes)]
		response, dispatched, err := stream.dispatch(operation, request)
		if err != nil {
			stream.writeUncertain = dispatched
			return total, stream.operationError(operation, err)
		}
		total += response.Written
	}
	return total, nil
}

// dispatch observes entry into RoundTrip, the last public boundary before a
// transport may send bytes. Cancellation before that boundary is provably safe;
// after it, even a connection error is conservatively treated as uncertain.
// The per-call client copy preserves endpoint, credential and redirect policy
// and avoids mutating a shared client or relying on real-socket tracing hooks.
func (stream *ManagedTCPStream) dispatch(ctx context.Context, request ManagedNetworkRequest) (ManagedNetworkResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return ManagedNetworkResponse{}, false, err
	}
	if stream.client.client == nil {
		response, err := stream.client.ManagedNetwork(ctx, request)
		return response, false, err
	}
	client := *stream.client
	httpClient := *client.client
	transport := &managedStreamDispatchTransport{base: httpClient.Transport}
	if transport.base == nil {
		transport.base = http.DefaultTransport
	}
	httpClient.Transport = transport
	client.client = &httpClient
	response, err := client.ManagedNetwork(ctx, request)
	return response, transport.dispatched.Load(), err
}

type managedStreamDispatchTransport struct {
	base       http.RoundTripper
	dispatched atomic.Bool
}

func (transport *managedStreamDispatchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	transport.dispatched.Store(true)
	return transport.base.RoundTrip(request)
}

func (stream *ManagedTCPStream) operationError(ctx context.Context, err error) error {
	if stream.closed.Load() {
		return net.ErrClosed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (stream *ManagedTCPStream) CloseRead(ctx context.Context) error {
	return stream.halfClose(ctx, "read")
}
func (stream *ManagedTCPStream) CloseWrite(ctx context.Context) error {
	return stream.halfClose(ctx, "write")
}

func (stream *ManagedTCPStream) halfClose(ctx context.Context, direction string) error {
	operation, cancel, err := stream.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	request := stream.request(ManagedNetworkHalfClose)
	request.WaitMS, request.Direction = 0, direction
	if _, err := stream.client.ManagedNetwork(operation, request); err != nil {
		return stream.operationError(operation, err)
	}
	if direction == "read" {
		stream.readClosed.Store(true)
	} else {
		stream.writeClosed.Store(true)
	}
	return nil
}

func (stream *ManagedTCPStream) Close() error {
	stream.closeOnce.Do(func() {
		stream.closed.Store(true)
		stream.cancel()
		// Closing must still reach the Host after the lifetime context expires.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request := stream.request(ManagedNetworkClose)
		request.WaitMS = 0
		_, stream.closeErr = stream.client.ManagedNetwork(ctx, request)
	})
	return stream.closeErr
}

var _ io.ReadWriteCloser = (*ManagedTCPStream)(nil)
