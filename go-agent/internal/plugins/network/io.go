package network

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Cancellation wakes the actual synchronous socket operation. Cleanup waits for
// the deadline callback to finish before a later operation can reuse the fd.
func socketDeadline(ctx context.Context, set func(time.Time) error, wait int) func() {
	deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = set(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		_ = set(time.Time{})
	}
}
func (resource *resource) read(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	release, err := acquire(ctx, resource, resource.readGate)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	resource.mu.Lock()
	connection := resource.conn
	resource.mu.Unlock()
	if connection == nil {
		return sdk.ManagedNetworkResponse{}, net.ErrClosed
	}
	cleanup := socketDeadline(ctx, connection.SetReadDeadline, request.WaitMS)
	defer cleanup()
	buffer := make([]byte, request.MaxBytes)
	n, err := connection.Read(buffer)
	if n > 0 {
		resource.touch()
		return sdk.ManagedNetworkResponse{Data: buffer[:n], EOF: errors.Is(err, io.EOF)}, nil
	}
	if errors.Is(err, io.EOF) {
		return sdk.ManagedNetworkResponse{EOF: true}, nil
	}
	if ctx.Err() != nil {
		return sdk.ManagedNetworkResponse{}, ctx.Err()
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		return sdk.ManagedNetworkResponse{Idle: true}, nil
	}
	return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed stream read failed")
}
func (resource *resource) write(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	release, err := acquire(ctx, resource, resource.writeGate)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	resource.mu.Lock()
	connection := resource.conn
	resource.mu.Unlock()
	if connection == nil {
		return sdk.ManagedNetworkResponse{}, net.ErrClosed
	}
	cleanup := socketDeadline(ctx, connection.SetWriteDeadline, request.WaitMS)
	defer cleanup()
	n, err := connection.Write(request.Data)
	if n > 0 {
		resource.touch()
		return sdk.ManagedNetworkResponse{Written: n}, nil
	}
	if ctx.Err() != nil {
		return sdk.ManagedNetworkResponse{}, ctx.Err()
	}
	if err != nil {
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed stream write failed")
	}
	return sdk.ManagedNetworkResponse{}, io.ErrShortWrite
}
func (resource *resource) halfClose(direction string) error {
	resource.mu.Lock()
	connection, ok := resource.conn.(*net.TCPConn)
	resource.mu.Unlock()
	if !ok {
		return net.ErrClosed
	}
	if direction == "read" {
		resource.readClosed.Store(true)
		return connection.CloseRead()
	}
	resource.writeClosed.Store(true)
	return connection.CloseWrite()
}
func (resource *resource) receive(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	release, err := acquire(ctx, resource, resource.readGate)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	if resource.datagrams != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Duration(request.WaitMS)*time.Millisecond)
		defer cancel()
		select {
		case packet := <-resource.datagrams:
			resource.owner.manager.buffered.Add(-int64(len(packet)))
			return sdk.ManagedNetworkResponse{Data: packet}, nil
		case <-resource.done:
			return sdk.ManagedNetworkResponse{}, net.ErrClosed
		case <-ctx.Done():
			return sdk.ManagedNetworkResponse{}, ctx.Err()
		}
	}
	resource.mu.Lock()
	connection := resource.conn
	resource.mu.Unlock()
	if connection == nil {
		return sdk.ManagedNetworkResponse{}, net.ErrClosed
	}
	cleanup := socketDeadline(ctx, connection.SetReadDeadline, request.WaitMS)
	defer cleanup()
	buffer := make([]byte, 65536)
	n, err := connection.Read(buffer)
	if err != nil {
		if ctx.Err() != nil {
			return sdk.ManagedNetworkResponse{}, ctx.Err()
		}
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed datagram receive failed")
	}
	if n > sdk.ManagedNetworkMaxDatagramBytes {
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorResourceExhausted, "managed datagram exceeds frame limit")
	}
	resource.touch()
	return sdk.ManagedNetworkResponse{Data: buffer[:n]}, nil
}
func (resource *resource) send(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	release, err := acquire(ctx, resource, resource.writeGate)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	defer release()
	var n int
	if resource.listener != nil {
		select {
		case resource.listener.writeGate <- struct{}{}:
			defer func() { <-resource.listener.writeGate }()
		case <-ctx.Done():
			return sdk.ManagedNetworkResponse{}, ctx.Err()
		case <-resource.done:
			return sdk.ManagedNetworkResponse{}, net.ErrClosed
		}
		cleanup := socketDeadline(ctx, resource.listener.udp.SetWriteDeadline, request.WaitMS)
		defer cleanup()
		n, err = resource.listener.udp.WriteToUDP(request.Data, resource.peer)
	} else {
		resource.mu.Lock()
		connection := resource.conn
		resource.mu.Unlock()
		if connection == nil {
			return sdk.ManagedNetworkResponse{}, net.ErrClosed
		}
		cleanup := socketDeadline(ctx, connection.SetWriteDeadline, request.WaitMS)
		defer cleanup()
		n, err = connection.Write(request.Data)
	}
	if err != nil || n != len(request.Data) {
		if ctx.Err() != nil {
			return sdk.ManagedNetworkResponse{}, ctx.Err()
		}
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed datagram send failed")
	}
	resource.touch()
	return sdk.ManagedNetworkResponse{Written: n}, nil
}
