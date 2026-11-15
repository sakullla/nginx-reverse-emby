package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (h *Host) SetPublicCertificateTLS(terminator interface {
	HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
}) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.publicTLS = terminator
	h.mu.Unlock()
}

func (h *Host) PublicCertificateTLSBound() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.publicTLS != nil
}

func (h *Host) publicCertificateTerminator() interface {
	HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
} {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	terminator, _ := h.publicTLS.(interface {
		HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
	})
	return terminator
}

func candidateHasHostPermission(candidate HostCandidate, permission string) bool {
	for _, scope := range candidate.Scopes {
		if scope == permission {
			return true
		}
	}
	for _, grant := range candidate.Grants {
		if grant.Name == permission {
			return true
		}
	}
	return false
}

func (attempt *hostAttempt) tlsStreams() *publicTLSStreams {
	if attempt == nil {
		return nil
	}
	attempt.publicTLSOnce.Do(func() {
		attempt.publicTLS = newPublicTLSStreams()
	})
	return attempt.publicTLS
}

type publicTLSStreams struct {
	mu       sync.Mutex
	closed   bool
	parents  map[string]struct{}
	children map[string]*terminatedStream
}

func newPublicTLSStreams() *publicTLSStreams {
	return &publicTLSStreams{parents: map[string]struct{}{}, children: map[string]*terminatedStream{}}
}

func (s *publicTLSStreams) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	children := s.children
	s.children = map[string]*terminatedStream{}
	s.parents = map[string]struct{}{}
	s.mu.Unlock()
	var err error
	for _, child := range children {
		err = errors.Join(err, child.finish())
	}
	return err
}

func (s *publicTLSStreams) claim(token string) bool {
	if s == nil || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, reserved := s.parents[token]; reserved {
		return false
	}
	if _, child := s.children[token]; child {
		return false
	}
	s.parents[token] = struct{}{}
	return true
}

func (s *publicTLSStreams) release(token string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.parents, token)
	s.mu.Unlock()
}

func (s *publicTLSStreams) register(stream *terminatedStream) bool {
	if s == nil || stream == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, reserved := s.parents[stream.parent]; !reserved {
		return false
	}
	if _, exists := s.children[stream.token]; exists {
		return false
	}
	s.children[stream.token] = stream
	return true
}

func (s *publicTLSStreams) classify(token string) (*terminatedStream, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stream := s.children[token]; stream != nil {
		return stream, false
	}
	_, parent := s.parents[token]
	return nil, parent
}

func (s *publicTLSStreams) drop(stream *terminatedStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.children[stream.token]; current == stream {
		delete(s.children, stream.token)
	}
	delete(s.parents, stream.parent)
}

func (h *Host) terminatePublicCertificate(ctx context.Context, candidate HostCandidate, owner *managed.Owner, attempt *hostAttempt, call sdk.HostRuntimeCall) sdk.HostRuntimeResponse {
	if !candidateHasHostPermission(candidate, sdk.PermissionManagedNetworkListen) || !candidateHasHostPermission(candidate, sdk.PermissionPublicCertificateTerminate) {
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "public certificate permission was not granted"})
	}
	request, err := sdk.DecodePublicCertificateTLSRequest(call.Payload)
	if err != nil || (call.OperationID != "" && call.OperationID != request.RequestID) {
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "public certificate request is invalid"})
	}
	if owner == nil || attempt == nil || request.Handle.Binding != owner.Binding() {
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate is unavailable"})
	}
	terminator := h.publicCertificateTerminator()
	if terminator == nil {
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate is unavailable"})
	}
	streams := attempt.tlsStreams()
	if !streams.claim(request.Handle.Token) {
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "managed stream is reserved"})
	}
	bridge := newPublicTLSBridge(owner.Context(), owner, request.Handle)
	application, err := terminator.HandshakeServer(ctx, request.CertificateID, bridge)
	if err != nil {
		if publicCertificateFailureKind(err) == "unavailable" {
			streams.release(request.Handle.Token)
			return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate is unavailable"})
		}
		_ = bridge.Close()
		streams.release(request.Handle.Token)
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate handshake failed"})
	}
	token, err := publicTLSToken()
	if err != nil {
		_ = application.Close()
		streams.release(request.Handle.Token)
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate handshake failed"})
	}
	stream := newTerminatedStream(streams, request.Handle.Token, token, application)
	if !streams.register(stream) {
		_ = application.Close()
		streams.release(request.Handle.Token)
		return runtimeReply(nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "public certificate handshake failed"})
	}
	return runtimeReply(sdk.PublicCertificateTLSResponse{Handle: sdk.ManagedNetworkHandle{Binding: request.Handle.Binding, Token: token, Kind: "stream", Protocol: "tcp"}}, nil)
}

func publicCertificateFailureKind(err error) string {
	var failure interface{ PublicCertificateFailure() string }
	if errors.As(err, &failure) && failure != nil && failure.PublicCertificateFailure() == "unavailable" {
		return "unavailable"
	}
	return "handshake"
}

func (h *Host) serveTerminatedTLS(ctx context.Context, candidate HostCandidate, attempt *hostAttempt, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, bool, error) {
	if attempt == nil || request.Handle == nil {
		return sdk.ManagedNetworkResponse{}, false, nil
	}
	stream, parent := attempt.tlsStreams().classify(request.Handle.Token)
	if stream != nil {
		if !candidateHasHostPermission(candidate, sdk.PermissionManagedNetworkListen) {
			return sdk.ManagedNetworkResponse{}, true, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "managed network permission is missing"}
		}
		response, err := stream.serve(ctx, request)
		return response, true, err
	}
	if parent {
		return sdk.ManagedNetworkResponse{}, true, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "managed stream is reserved"}
	}
	return sdk.ManagedNetworkResponse{}, false, nil
}

type terminatedStream struct {
	streams                 *publicTLSStreams
	parent                  string
	token                   string
	conn                    net.Conn
	readGate, writeGate     chan struct{}
	closed                  atomic.Bool
	readClosed, writeClosed atomic.Bool
	closeOnce               sync.Once
	closeErr                error
}

func newTerminatedStream(streams *publicTLSStreams, parent, token string, conn net.Conn) *terminatedStream {
	return &terminatedStream{streams: streams, parent: parent, token: token, conn: conn, readGate: make(chan struct{}, 1), writeGate: make(chan struct{}, 1)}
}

func (s *terminatedStream) finish() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.conn != nil {
			s.closeErr = s.conn.Close()
		}
		if s.streams != nil {
			s.streams.drop(s)
		}
	})
	return s.closeErr
}

func (s *terminatedStream) serve(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	if s == nil || s.closed.Load() {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream is closed"}
	}
	switch request.Action {
	case sdk.ManagedNetworkRead:
		return s.read(ctx, request)
	case sdk.ManagedNetworkWrite:
		return s.write(ctx, request)
	case sdk.ManagedNetworkHalfClose:
		return s.halfClose(request.Direction)
	case sdk.ManagedNetworkClose:
		if err := s.finish(); err != nil {
			return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream is closed"}
		}
		return sdk.ManagedNetworkResponse{Done: true}, nil
	default:
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorInvalidArgument, Message: "managed action unsupported"}
	}
}

func (s *terminatedStream) read(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	if s.readClosed.Load() {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "managed stream direction is closed"}
	}
	release, err := acquireTerminatedGate(ctx, s.readGate, &s.closed)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	if s.closed.Load() || s.readClosed.Load() {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream is closed"}
	}
	buffer := make([]byte, request.MaxBytes)
	stop, err := armStreamDeadline(ctx, s.conn.SetReadDeadline, request.WaitMS)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream read failed"}
	}
	defer func() {
		stop()
		_ = s.conn.SetReadDeadline(time.Time{})
	}()
	n, err := s.conn.Read(buffer)
	if n > 0 {
		return sdk.ManagedNetworkResponse{Data: buffer[:n], EOF: errors.Is(err, io.EOF)}, nil
	}
	if errors.Is(err, io.EOF) {
		return sdk.ManagedNetworkResponse{EOF: true}, nil
	}
	if ctx.Err() != nil {
		return sdk.ManagedNetworkResponse{}, ctx.Err()
	}
	if netTimeout(err) {
		return sdk.ManagedNetworkResponse{Idle: true}, nil
	}
	return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream read failed"}
}

func (s *terminatedStream) write(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	if s.writeClosed.Load() {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorPermissionDenied, Message: "managed stream direction is closed"}
	}
	release, err := acquireTerminatedGate(ctx, s.writeGate, &s.closed)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	if s.closed.Load() || s.writeClosed.Load() {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream is closed"}
	}
	stop, err := armStreamDeadline(ctx, s.conn.SetWriteDeadline, request.WaitMS)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream write failed"}
	}
	defer func() {
		stop()
		_ = s.conn.SetWriteDeadline(time.Time{})
	}()
	n, err := s.conn.Write(request.Data)
	if n > 0 {
		return sdk.ManagedNetworkResponse{Written: n}, nil
	}
	if ctx.Err() != nil {
		return sdk.ManagedNetworkResponse{}, ctx.Err()
	}
	if err == nil {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream write failed"}
	}
	return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream write failed"}
}

func (s *terminatedStream) halfClose(direction string) (sdk.ManagedNetworkResponse, error) {
	if direction == "read" {
		s.readClosed.Store(true)
		return sdk.ManagedNetworkResponse{Done: true}, nil
	}
	writer, ok := s.conn.(interface{ CloseWrite() error })
	if !ok {
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream write failed"}
	}
	if err := writer.CloseWrite(); err != nil {
		s.writeClosed.Store(true)
		return sdk.ManagedNetworkResponse{}, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream write failed"}
	}
	s.writeClosed.Store(true)
	return sdk.ManagedNetworkResponse{Done: true}, nil
}

func acquireTerminatedGate(ctx context.Context, gate chan struct{}, closed *atomic.Bool) (func(), error) {
	select {
	case gate <- struct{}{}:
		if closed.Load() {
			<-gate
			return nil, &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "managed stream is closed"}
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func armStreamDeadline(ctx context.Context, set func(time.Time) error, wait int) (func(), error) {
	deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := set(deadline); err != nil {
		return func() {}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()) })
	return func() { stop() }, nil
}

func netTimeout(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

type publicTLSBridge struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	owner                   *managed.Owner
	handle                  sdk.ManagedNetworkHandle
	binding                 sdk.ManagedBinding
	local, remote           net.Addr
	mu                      sync.Mutex
	closed                  bool
	readDeadline            time.Time
	writeDeadline           time.Time
	readCancel, writeCancel context.CancelFunc
	readGen, writeGen       uint64
	closeOnce               sync.Once
}

func newPublicTLSBridge(parent context.Context, owner *managed.Owner, handle sdk.ManagedNetworkHandle) *publicTLSBridge {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &publicTLSBridge{
		ctx: ctx, cancel: cancel, owner: owner, handle: handle, binding: handle.Binding,
		local:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
		remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
	}
}

func (b *publicTLSBridge) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if b.isClosed() {
			return 0, net.ErrClosed
		}
		opCtx, finish, wait, err := b.begin(true)
		if err != nil {
			return 0, err
		}
		maxBytes := len(p)
		if maxBytes > sdk.ManagedNetworkMaxChunkBytes {
			maxBytes = sdk.ManagedNetworkMaxChunkBytes
		}
		response, err := b.owner.Handle(opCtx, sdk.ManagedNetworkRequest{
			Action: sdk.ManagedNetworkRead, Binding: b.binding, RequestID: publicTLSRequestID(),
			Handle: &b.handle, MaxBytes: maxBytes, WaitMS: wait,
		})
		finish()
		if err != nil {
			if b.isClosed() || errors.Is(err, net.ErrClosed) {
				return 0, net.ErrClosed
			}
			if b.deadlinePassed(true) || netTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
				return 0, os.ErrDeadlineExceeded
			}
			if b.ctx.Err() != nil {
				return 0, b.ctx.Err()
			}
			return 0, err
		}
		if response.Idle {
			if b.deadlinePassed(true) {
				return 0, os.ErrDeadlineExceeded
			}
			if b.ctx.Err() != nil {
				return 0, b.ctx.Err()
			}
			continue
		}
		n := copy(p, response.Data)
		if response.EOF {
			if n == 0 {
				return 0, io.EOF
			}
			return n, io.EOF
		}
		if n == 0 {
			continue
		}
		return n, nil
	}
}

func (b *publicTLSBridge) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		if b.isClosed() {
			if written > 0 {
				return written, net.ErrClosed
			}
			return 0, net.ErrClosed
		}
		chunk := p[written:]
		if len(chunk) > sdk.ManagedNetworkMaxChunkBytes {
			chunk = chunk[:sdk.ManagedNetworkMaxChunkBytes]
		}
		opCtx, finish, wait, err := b.begin(false)
		if err != nil {
			if written > 0 {
				return written, err
			}
			return 0, err
		}
		response, err := b.owner.Handle(opCtx, sdk.ManagedNetworkRequest{
			Action: sdk.ManagedNetworkWrite, Binding: b.binding, RequestID: publicTLSRequestID(),
			Handle: &b.handle, Data: chunk, WaitMS: wait,
		})
		finish()
		if err != nil || response.Written <= 0 {
			if written > 0 {
				return written, err
			}
			if b.deadlinePassed(false) || netTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
				return 0, os.ErrDeadlineExceeded
			}
			if err == nil {
				err = io.ErrShortWrite
			}
			return 0, err
		}
		if response.Written > len(chunk) {
			response.Written = len(chunk)
		}
		written += response.Written
	}
	return written, nil
}

func (b *publicTLSBridge) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		readCancel, writeCancel := b.readCancel, b.writeCancel
		b.mu.Unlock()
		if readCancel != nil {
			readCancel()
		}
		if writeCancel != nil {
			writeCancel()
		}
		if b.cancel != nil {
			b.cancel()
		}
		handle := b.handle
		_, _ = b.owner.Handle(context.Background(), sdk.ManagedNetworkRequest{
			Action: sdk.ManagedNetworkClose, Binding: b.binding, RequestID: publicTLSRequestID(), Handle: &handle,
		})
	})
	return nil
}

func (b *publicTLSBridge) LocalAddr() net.Addr  { return b.local }
func (b *publicTLSBridge) RemoteAddr() net.Addr { return b.remote }

func (b *publicTLSBridge) SetDeadline(deadline time.Time) error {
	if err := b.SetReadDeadline(deadline); err != nil {
		return err
	}
	return b.SetWriteDeadline(deadline)
}

func (b *publicTLSBridge) SetReadDeadline(deadline time.Time) error {
	return b.setDeadline(true, deadline)
}

func (b *publicTLSBridge) SetWriteDeadline(deadline time.Time) error {
	return b.setDeadline(false, deadline)
}

func (b *publicTLSBridge) setDeadline(read bool, deadline time.Time) error {
	if b == nil {
		return net.ErrClosed
	}
	b.mu.Lock()
	var cancel context.CancelFunc
	if read {
		b.readDeadline = deadline
		cancel = b.readCancel
	} else {
		b.writeDeadline = deadline
		cancel = b.writeCancel
	}
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (b *publicTLSBridge) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed || b.ctx.Err() != nil
}

func (b *publicTLSBridge) deadlinePassed(read bool) bool {
	b.mu.Lock()
	deadline := b.writeDeadline
	if read {
		deadline = b.readDeadline
	}
	b.mu.Unlock()
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

func (b *publicTLSBridge) begin(read bool) (context.Context, func(), int, error) {
	if b.isClosed() {
		return nil, nil, 0, net.ErrClosed
	}
	b.mu.Lock()
	deadline := b.writeDeadline
	if read {
		deadline = b.readDeadline
	}
	b.mu.Unlock()
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return nil, nil, 0, os.ErrDeadlineExceeded
	}
	wait := sdk.ManagedNetworkMaxWaitMS
	opCtx, cancel := context.WithCancel(b.ctx)
	if !deadline.IsZero() {
		var stop context.CancelFunc
		opCtx, stop = context.WithDeadline(opCtx, deadline)
		previous := cancel
		cancel = func() {
			stop()
			previous()
		}
		millis := int(time.Until(deadline) / time.Millisecond)
		if millis < 1 {
			millis = 1
		}
		if millis < wait {
			wait = millis
		}
	}
	b.mu.Lock()
	var generation uint64
	if read {
		b.readGen++
		generation = b.readGen
		b.readCancel = cancel
	} else {
		b.writeGen++
		generation = b.writeGen
		b.writeCancel = cancel
	}
	b.mu.Unlock()
	finish := func() {
		b.mu.Lock()
		if read && b.readGen == generation {
			b.readCancel = nil
		}
		if !read && b.writeGen == generation {
			b.writeCancel = nil
		}
		b.mu.Unlock()
		cancel()
	}
	return opCtx, finish, wait, nil
}

var publicTLSRequestSeq atomic.Uint64

func publicTLSRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return "tlsreq" + strconv.FormatUint(publicTLSRequestSeq.Add(1), 16)
}

func publicTLSToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
