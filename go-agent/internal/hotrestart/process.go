package hotrestart

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = 1

const (
	envChild             = "NRE_HOT_RESTART_CHILD"
	envIdentity          = "NRE_HOT_RESTART_IDENTITY"
	envStreamDescriptors = "NRE_HOT_RESTART_STREAMS"
	envEventFD           = "NRE_HOT_RESTART_EVENT_FD"
	envCommandFD         = "NRE_HOT_RESTART_COMMAND_FD"
	envStreamFDStart     = "NRE_HOT_RESTART_STREAM_FD_START"
)

type Identity struct {
	Revision       int64  `json:"revision"`
	SnapshotDigest string `json:"snapshot_digest"`
	GenerationID   string `json:"generation_id"`
	LeaseID        string `json:"lease_id"`
}

func (i Identity) Validate() error {
	if i.Revision <= 0 || strings.TrimSpace(i.SnapshotDigest) == "" || strings.TrimSpace(i.GenerationID) == "" || strings.TrimSpace(i.LeaseID) == "" {
		return errors.New("hot restart identity is incomplete")
	}
	return nil
}

type messageType string

const (
	messageReady        messageType = "ready"
	messageActivate     messageType = "activate"
	messageActivated    messageType = "activated"
	messageAuthority    messageType = "authority"
	messageAuthorityAck messageType = "authority_ack"
	messageFailed       messageType = "failed"
)

type message struct {
	Version  int         `json:"version"`
	Type     messageType `json:"type"`
	Identity Identity    `json:"identity"`
	Error    string      `json:"error,omitempty"`
}

func validateMessage(msg message, expectedType messageType, identity Identity) error {
	if msg.Version != ProtocolVersion {
		return fmt.Errorf("hot restart protocol version %d is unsupported", msg.Version)
	}
	if msg.Identity != identity {
		return errors.New("hot restart message identity does not match the launch identity")
	}
	if msg.Type == messageFailed {
		if msg.Error == "" {
			msg.Error = "child reported failure"
		}
		return errors.New(msg.Error)
	}
	if msg.Type != expectedType {
		return fmt.Errorf("unexpected hot restart message %q, want %q", msg.Type, expectedType)
	}
	return nil
}

type Launch struct {
	Binary            string
	Argv              []string
	Env               []string
	Identity          Identity
	StreamDescriptors []StreamDescriptor
	StreamFiles       []*os.File
	Stdout            io.Writer
	Stderr            io.Writer
}

type Supervisor struct {
	ReadyTimeout   time.Duration
	CommandTimeout time.Duration
}

func (s Supervisor) Start(ctx context.Context, launch Launch) (*ChildProcess, error) {
	if strings.TrimSpace(launch.Binary) == "" {
		return nil, errors.New("hot restart child binary is required")
	}
	if err := launch.Identity.Validate(); err != nil {
		return nil, err
	}
	if len(launch.StreamDescriptors) != len(launch.StreamFiles) {
		return nil, errors.New("stream descriptors and files must have equal length")
	}
	identityValue, err := encodedEnvironmentValue(launch.Identity)
	if err != nil {
		return nil, err
	}
	streamValue, err := encodedEnvironmentValue(launch.StreamDescriptors)
	if err != nil {
		return nil, err
	}
	eventReader, eventWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	commandReader, commandWriter, err := os.Pipe()
	if err != nil {
		_ = eventReader.Close()
		_ = eventWriter.Close()
		return nil, err
	}
	closePipes := func() {
		_ = eventReader.Close()
		_ = eventWriter.Close()
		_ = commandReader.Close()
		_ = commandWriter.Close()
	}

	args := launch.Argv
	if len(args) > 0 {
		args = args[1:]
	}
	cmd := exec.Command(launch.Binary, args...)
	cmd.Env = append([]string(nil), launch.Env...)
	cmd.Env = setEnv(cmd.Env, envChild, "1")
	cmd.Env = setEnv(cmd.Env, envIdentity, identityValue)
	cmd.Env = setEnv(cmd.Env, envStreamDescriptors, streamValue)
	cmd.Env = setEnv(cmd.Env, envEventFD, "3")
	cmd.Env = setEnv(cmd.Env, envCommandFD, "4")
	cmd.Env = setEnv(cmd.Env, envStreamFDStart, "5")
	cmd.ExtraFiles = append([]*os.File{eventWriter, commandReader}, launch.StreamFiles...)
	cmd.Stdout = launch.Stdout
	cmd.Stderr = launch.Stderr
	if err := cmd.Start(); err != nil {
		closePipes()
		return nil, err
	}
	_ = eventWriter.Close()
	_ = commandReader.Close()

	process := newChildProcess(cmd, launch.Identity, eventReader, commandWriter, s.commandTimeout())
	readyCtx, cancel := context.WithTimeout(ctx, s.readyTimeout())
	defer cancel()
	if err := process.waitFor(readyCtx, messageReady); err != nil {
		_ = process.Abort()
		return nil, fmt.Errorf("wait for hot restart child readiness: %w", err)
	}
	return process, nil
}

func (s Supervisor) readyTimeout() time.Duration {
	if s.ReadyTimeout <= 0 {
		return 60 * time.Second
	}
	return s.ReadyTimeout
}

func (s Supervisor) commandTimeout() time.Duration {
	if s.CommandTimeout <= 0 {
		return 10 * time.Second
	}
	return s.CommandTimeout
}

type processState uint8

const (
	processReady processState = iota + 1
	processActivated
	processAuthority
	processAborted
)

type ChildProcess struct {
	cmd       *exec.Cmd
	identity  Identity
	events    chan eventResult
	commands  *os.File
	eventFile *os.File
	done      chan struct{}
	waitMu    sync.Mutex
	waitErr   error
	mu        sync.Mutex
	state     processState
	timeout   time.Duration
}

type eventResult struct {
	message message
	err     error
}

func newChildProcess(cmd *exec.Cmd, identity Identity, eventFile, commandFile *os.File, timeout time.Duration) *ChildProcess {
	process := &ChildProcess{
		cmd: cmd, identity: identity, events: make(chan eventResult, 1), commands: commandFile,
		eventFile: eventFile, done: make(chan struct{}), state: processReady, timeout: timeout,
	}
	go func() {
		decoder := json.NewDecoder(eventFile)
		for {
			var msg message
			if err := decoder.Decode(&msg); err != nil {
				process.events <- eventResult{err: err}
				return
			}
			process.events <- eventResult{message: msg}
		}
	}()
	go func() {
		err := cmd.Wait()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	return process
}

func (p *ChildProcess) Activate(ctx context.Context) error {
	return p.transition(ctx, processReady, processActivated, messageActivate, messageActivated)
}

func (p *ChildProcess) TransferAuthority(ctx context.Context) error {
	return p.transition(ctx, processActivated, processAuthority, messageAuthority, messageAuthorityAck)
}

func (p *ChildProcess) transition(ctx context.Context, from, to processState, commandType, responseType messageType) error {
	if p == nil {
		return errors.New("hot restart child process is required")
	}
	p.mu.Lock()
	if p.state == to || p.state == processAuthority && to == processAuthority {
		p.mu.Unlock()
		return nil
	}
	if p.state != from {
		state := p.state
		p.mu.Unlock()
		return fmt.Errorf("hot restart transition %q is invalid from state %d", commandType, state)
	}
	if err := json.NewEncoder(p.commands).Encode(message{Version: ProtocolVersion, Type: commandType, Identity: p.identity}); err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	waitCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := p.waitFor(waitCtx, responseType); err != nil {
		return err
	}
	p.mu.Lock()
	if p.state != from {
		p.mu.Unlock()
		return errors.New("hot restart state changed while awaiting child acknowledgement")
	}
	p.state = to
	p.mu.Unlock()
	return nil
}

func (p *ChildProcess) waitFor(ctx context.Context, expected messageType) error {
	select {
	case result := <-p.events:
		if result.err != nil {
			return result.err
		}
		return validateMessage(result.message, expected, p.identity)
	default:
	}
	select {
	case result := <-p.events:
		if result.err != nil {
			return result.err
		}
		return validateMessage(result.message, expected, p.identity)
	case <-p.done:
		if err := p.Wait(); err != nil {
			return fmt.Errorf("hot restart child exited: %w", err)
		}
		return errors.New("hot restart child exited before acknowledgement")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *ChildProcess) Abort() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.state == processAborted {
		p.mu.Unlock()
		return p.Wait()
	}
	p.state = processAborted
	p.mu.Unlock()
	_ = p.commands.Close()
	_ = p.eventFile.Close()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.Wait()
}

func (p *ChildProcess) Wait() error {
	if p == nil {
		return nil
	}
	<-p.done
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.waitErr
}

type ChildSession struct {
	Identity          Identity
	StreamDescriptors []StreamDescriptor
	StreamFiles       []*os.File
	events            *os.File
	commands          *os.File
	decoder           *json.Decoder
	encoder           *json.Encoder
	closeOnce         sync.Once
	closeErr          error
}

func OpenChildSessionFromEnvironment() (*ChildSession, bool, error) {
	if os.Getenv(envChild) != "1" {
		return nil, false, nil
	}
	var identity Identity
	if err := decodeEnvironmentValue(os.Getenv(envIdentity), &identity); err != nil {
		return nil, true, err
	}
	if err := identity.Validate(); err != nil {
		return nil, true, err
	}
	var descriptors []StreamDescriptor
	if err := decodeEnvironmentValue(os.Getenv(envStreamDescriptors), &descriptors); err != nil {
		return nil, true, err
	}
	eventFD, err := environmentFD(envEventFD)
	if err != nil {
		return nil, true, err
	}
	commandFD, err := environmentFD(envCommandFD)
	if err != nil {
		return nil, true, err
	}
	streamStart, err := environmentFD(envStreamFDStart)
	if err != nil {
		return nil, true, err
	}
	events := os.NewFile(uintptr(eventFD), "hot-restart-events")
	commands := os.NewFile(uintptr(commandFD), "hot-restart-commands")
	if events == nil || commands == nil {
		return nil, true, errors.New("hot restart control file descriptors are invalid")
	}
	session := &ChildSession{Identity: identity, StreamDescriptors: descriptors, events: events, commands: commands}
	session.encoder = json.NewEncoder(events)
	session.decoder = json.NewDecoder(commands)
	for index := range descriptors {
		file := os.NewFile(uintptr(streamStart+index), fmt.Sprintf("hot-restart-stream-%d", index))
		if file == nil {
			_ = session.Close()
			return nil, true, errors.New("hot restart stream file descriptor is invalid")
		}
		session.StreamFiles = append(session.StreamFiles, file)
	}
	return session, true, nil
}

func (s *ChildSession) Ready() error {
	return s.send(messageReady, nil)
}

func (s *ChildSession) AwaitActivation(ctx context.Context, activate func() error) error {
	return s.await(ctx, messageActivate, messageActivated, activate)
}

func (s *ChildSession) AwaitAuthority(ctx context.Context, transfer func() error) error {
	return s.await(ctx, messageAuthority, messageAuthorityAck, transfer)
}

func (s *ChildSession) await(ctx context.Context, commandType, responseType messageType, action func() error) error {
	result := make(chan eventResult, 1)
	go func() {
		var msg message
		err := s.decoder.Decode(&msg)
		result <- eventResult{message: msg, err: err}
	}()
	select {
	case received := <-result:
		if received.err != nil {
			return received.err
		}
		if err := validateMessage(received.message, commandType, s.Identity); err != nil {
			return err
		}
		if action != nil {
			if err := action(); err != nil {
				_ = s.send(messageFailed, err)
				return err
			}
		}
		return s.send(responseType, nil)
	case <-ctx.Done():
		_ = s.Close()
		return ctx.Err()
	}
}

func (s *ChildSession) send(kind messageType, sendErr error) error {
	msg := message{Version: ProtocolVersion, Type: kind, Identity: s.Identity}
	if sendErr != nil {
		msg.Error = sendErr.Error()
	}
	return s.encoder.Encode(msg)
}

func (s *ChildSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		for _, file := range s.StreamFiles {
			if file != nil {
				s.closeErr = errors.Join(s.closeErr, file.Close())
			}
		}
		s.closeErr = errors.Join(s.closeErr, s.commands.Close(), s.events.Close())
	})
	return s.closeErr
}

func encodedEnvironmentValue(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeEnvironmentValue(value string, target any) error {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func environmentFD(name string) (int, error) {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 3 {
		return 0, fmt.Errorf("%s is not a valid inherited file descriptor", name)
	}
	return value, nil
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for index := range env {
		if strings.HasPrefix(env[index], prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
