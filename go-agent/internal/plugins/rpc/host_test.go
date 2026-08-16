//go:build !integration

package rpc

import (
	"context"

	"errors"

	"io"
	"os"
	"os/exec"

	"sync"

	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type hostProcess struct {
	done    chan error
	once    sync.Once
	pid     int
	stopped chan struct{}
}

type hostTestSandbox struct{}

func (hostTestSandbox) Available() bool                       { return true }
func (hostTestSandbox) Provider() string                      { return "test-kernel-boundary" }
func (hostTestSandbox) Validate(pluginprocess.Security) error { return nil }
func (hostTestSandbox) Configure(*exec.Cmd, pluginprocess.Security) (func() error, func() error, func(int) error, error) {
	return func() error { return nil }, func() error { return nil }, func(int) error { return nil }, nil
}

func (p *hostProcess) PID() int {
	if p.pid == 0 {
		return 9
	}
	return p.pid
}
func (p *hostProcess) Wait() error { return <-p.done }
func (p *hostProcess) Signal(os.Signal) error {
	p.once.Do(func() {
		if p.stopped != nil {
			close(p.stopped)
		}
		p.done <- nil
	})
	return nil
}
func (p *hostProcess) Kill() error {
	p.once.Do(func() {
		if p.stopped != nil {
			close(p.stopped)
		}
		p.done <- nil
	})
	return nil
}

type hostRunner struct{}

func (hostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return &hostProcess{done: make(chan error, 1)}, func() error { return nil }, nil
}

type loggingHostRunner struct{}

func (loggingHostRunner) Start(_ context.Context, _ pluginprocess.InstanceSpec, _ pluginprocess.Sandbox, output io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	for _, chunk := range []string{`{"safe":"candidate-`, `secret","token":"quoted-secret"}` + "\n"} {
		if _, err := io.WriteString(output, chunk); err != nil {
			return nil, nil, err
		}
	}
	return &hostProcess{done: make(chan error, 1)}, func() error { return nil }, nil
}

type failingStartHostRunner struct{ err error }

func (r failingStartHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return nil, nil, r.err
}

type drainHostProcess struct {
	done       chan error
	killCalled chan struct{}
	killDelay  time.Duration
	killErr    error
	once       sync.Once
}

type retryDrainHostProcess struct {
	done      chan error
	mu        sync.Mutex
	killCalls int
	stopped   chan struct{}
}

func (p *retryDrainHostProcess) PID() int               { return 73 }
func (p *retryDrainHostProcess) Wait() error            { return <-p.done }
func (p *retryDrainHostProcess) Signal(os.Signal) error { return nil }
func (p *retryDrainHostProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killCalls++
	if p.killCalls == 1 {
		return errors.New("candidate kill failed")
	}
	close(p.stopped)
	p.done <- nil
	return nil
}

func (p *drainHostProcess) PID() int               { return 71 }
func (p *drainHostProcess) Wait() error            { return <-p.done }
func (p *drainHostProcess) Signal(os.Signal) error { return nil }
func (p *drainHostProcess) Kill() error {
	p.once.Do(func() { close(p.killCalled) })
	if p.killErr != nil {
		return p.killErr
	}
	go func() {
		time.Sleep(p.killDelay)
		p.done <- nil
	}()
	return nil
}

type queuedHostRunner struct {
	mu        sync.Mutex
	processes []pluginprocess.ManagedProcess
}

type cleanupRetryHostRunner struct {
	process      *hostProcess
	mu           sync.Mutex
	cleanupCalls int
}

func (r *cleanupRetryHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	return r.process, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cleanupCalls++
		if r.cleanupCalls == 1 {
			return errors.New("sandbox cleanup failed")
		}
		return nil
	}, nil
}

func (r *queuedHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.processes) == 0 {
		return nil, nil, errors.New("unexpected process start")
	}
	process := r.processes[0]
	r.processes = r.processes[1:]
	return process, func() error { return nil }, nil
}

type restartHostRunner struct {
	mu      sync.Mutex
	started chan *hostProcess
	count   int
}

type blockingHostRunner struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (r *blockingHostRunner) Start(ctx context.Context, _ pluginprocess.InstanceSpec, _ pluginprocess.Sandbox, _ io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.returned)
	return nil, nil, ctx.Err()
}

func (r *restartHostRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	r.mu.Lock()
	r.count++
	process := &hostProcess{done: make(chan error, 1), pid: r.count}
	r.mu.Unlock()
	r.started <- process
	return process, func() error { return nil }, nil
}

func (r *restartHostRunner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

type hostCloser struct{}

func (hostCloser) Close() error { return nil }

type unblockHostCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (c *unblockHostCloser) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type hostClient struct {
	abi        string
	prepareErr error
}

type cancelOnStopHostClient struct {
	hostClient
	onStop func()
}

type blockingStopHostClient struct {
	hostClient
	released <-chan struct{}
}

func (c blockingStopHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	<-c.released
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func (c cancelOnStopHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.onStop != nil {
		c.onStop()
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

func (c hostClient) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{ABI: c.abi, Capabilities: []string{"relay.read"}, Features: append([]string(nil), request.RequiredFeatures...)}, nil
}
func (c hostClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	if c.prepareErr != nil {
		return pluginsdk.LifecycleResponse{}, c.prepareErr
	}
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (hostClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (hostClient) InvokeAction(_ context.Context, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{Accepted: true, OperationID: request.OperationID}, nil
}

func (hostClient) PlanAction(context.Context, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	return pluginsdk.RPCActionPlanResponse{}, nil
}

func (hostClient) QueryAction(_ context.Context, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{OperationID: request.OperationID, Missing: true}, nil
}
func (hostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}

type hostLifecycleCounts struct {
	mu                                sync.Mutex
	handshakes, prepares, activations int
	stops                             int
}

func (c *hostLifecycleCounts) snapshot() (int, int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handshakes, c.prepares, c.activations, c.stops
}

type countingHostClient struct{ counts *hostLifecycleCounts }

func (c countingHostClient) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	c.counts.mu.Lock()
	c.counts.handshakes++
	c.counts.mu.Unlock()
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: []string{"relay.read"}, Features: append([]string(nil), request.RequiredFeatures...)}, nil
}
func (c countingHostClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.prepares++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c countingHostClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.activations++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c countingHostClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.counts.mu.Lock()
	c.counts.stops++
	c.counts.mu.Unlock()
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
