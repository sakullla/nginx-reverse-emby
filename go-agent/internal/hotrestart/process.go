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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

const ProtocolVersion = 1

const (
	envChild             = "NRE_HOT_RESTART_CHILD"
	envIdentity          = "NRE_HOT_RESTART_IDENTITY"
	envStreamDescriptors = "NRE_HOT_RESTART_STREAMS"
	envAuthorityJournal  = "NRE_HOT_RESTART_AUTHORITY_JOURNAL"
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

const AuthorityJournalVersion = 1

type AuthorityPhase string

const (
	AuthorityPhaseParent AuthorityPhase = "parent"
	AuthorityPhaseReady  AuthorityPhase = "child_ready"
	AuthorityPhaseActive AuthorityPhase = "child_active"
	AuthorityPhaseChild  AuthorityPhase = "child_authority"
	AuthorityOwnerNone                  = "none"
	AuthorityOwnerParent                = "parent"
	AuthorityOwnerChild                 = "child"
)

type AuthorityRecord struct {
	Version   int            `json:"version"`
	Identity  Identity       `json:"identity"`
	Phase     AuthorityPhase `json:"phase"`
	ParentPID int            `json:"parent_pid"`
	ChildPID  int            `json:"child_pid,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type FileAuthorityJournal struct {
	path string
	mu   sync.Mutex
}

func NewFileAuthorityJournal(path string) *FileAuthorityJournal {
	return &FileAuthorityJournal{path: filepath.Clean(path)}
}

func (j *FileAuthorityJournal) Begin(identity Identity, parentPID int) error {
	if j == nil || strings.TrimSpace(j.path) == "" || j.path == "." {
		return errors.New("hot restart authority journal path is required")
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	if parentPID <= 0 {
		return errors.New("hot restart parent pid is required")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.writeLocked(AuthorityRecord{
		Version: AuthorityJournalVersion, Identity: identity, Phase: AuthorityPhaseParent,
		ParentPID: parentPID, UpdatedAt: time.Now().UTC(),
	})
}

func (j *FileAuthorityJournal) BeginOwned(identity Identity, parentPID int, alive func(int) bool) error {
	if _, err := os.Stat(j.path); os.IsNotExist(err) {
		return j.Begin(identity, parentPID)
	} else if err != nil {
		return err
	}
	existing, err := j.Load()
	if err != nil {
		return err
	}
	owner, recovered, err := j.Recover(existing.Identity, alive)
	if err != nil {
		return err
	}
	ownedByCurrentProcess := owner == AuthorityOwnerParent && recovered.ParentPID == parentPID ||
		owner == AuthorityOwnerChild && recovered.ChildPID == parentPID
	if !ownedByCurrentProcess {
		return errors.New("current process does not own the existing hot restart authority journal")
	}
	return j.Begin(identity, parentPID)
}

func (j *FileAuthorityJournal) Load() (AuthorityRecord, error) {
	if j == nil || strings.TrimSpace(j.path) == "" || j.path == "." {
		return AuthorityRecord{}, errors.New("hot restart authority journal path is required")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.loadLocked()
}

func (j *FileAuthorityJournal) Advance(identity Identity, childPID int, next AuthorityPhase) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	record, err := j.loadLocked()
	if err != nil {
		return err
	}
	if record.Identity != identity || record.Version != AuthorityJournalVersion {
		return errors.New("hot restart authority journal identity or version mismatch")
	}
	if childPID <= 0 || record.ChildPID != 0 && record.ChildPID != childPID {
		return errors.New("hot restart authority journal child pid mismatch")
	}
	if authorityPhaseRank(next) < authorityPhaseRank(record.Phase) {
		return errors.New("hot restart authority journal phase cannot move backward")
	}
	if authorityPhaseRank(next) > authorityPhaseRank(record.Phase)+1 {
		return errors.New("hot restart authority journal phase cannot skip a checkpoint")
	}
	if next == record.Phase {
		return nil
	}
	record.ChildPID = childPID
	record.Phase = next
	record.UpdatedAt = time.Now().UTC()
	return j.writeLocked(record)
}

func (j *FileAuthorityJournal) Recover(identity Identity, alive func(int) bool) (string, AuthorityRecord, error) {
	if alive == nil {
		return AuthorityOwnerNone, AuthorityRecord{}, errors.New("process liveness callback is required")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	record, err := j.loadLocked()
	if err != nil {
		return AuthorityOwnerNone, AuthorityRecord{}, err
	}
	if record.Identity != identity || record.Version != AuthorityJournalVersion {
		return AuthorityOwnerNone, record, errors.New("hot restart authority journal identity or version mismatch")
	}
	parentAlive := record.ParentPID > 0 && alive(record.ParentPID)
	childAlive := record.ChildPID > 0 && alive(record.ChildPID)
	owner := AuthorityOwnerNone
	switch {
	case childAlive && (!parentAlive || authorityPhaseRank(record.Phase) >= authorityPhaseRank(AuthorityPhaseActive)):
		owner = AuthorityOwnerChild
		if !parentAlive && record.Phase != AuthorityPhaseChild {
			record.Phase = AuthorityPhaseChild
			record.UpdatedAt = time.Now().UTC()
			if err := j.writeLocked(record); err != nil {
				return AuthorityOwnerNone, record, err
			}
		}
	case parentAlive:
		owner = AuthorityOwnerParent
		if record.Phase != AuthorityPhaseParent || record.ChildPID != 0 {
			record.Phase = AuthorityPhaseParent
			record.ChildPID = 0
			record.UpdatedAt = time.Now().UTC()
			if err := j.writeLocked(record); err != nil {
				return AuthorityOwnerNone, record, err
			}
		}
	}
	return owner, record, nil
}

func (j *FileAuthorityJournal) loadLocked() (AuthorityRecord, error) {
	payload, err := os.ReadFile(j.path)
	if err != nil {
		return AuthorityRecord{}, err
	}
	var record AuthorityRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return AuthorityRecord{}, err
	}
	if record.Version != AuthorityJournalVersion || record.Phase == "" {
		return AuthorityRecord{}, errors.New("hot restart authority journal is invalid")
	}
	return record, nil
}

func (j *FileAuthorityJournal) writeLocked(record AuthorityRecord) error {
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(j.path), ".authority-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, j.path); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		directory, err := os.Open(filepath.Dir(j.path))
		if err != nil {
			return err
		}
		syncErr := directory.Sync()
		closeErr := directory.Close()
		return errors.Join(syncErr, closeErr)
	}
	return nil
}

func authorityPhaseRank(phase AuthorityPhase) int {
	switch phase {
	case AuthorityPhaseParent:
		return 0
	case AuthorityPhaseReady:
		return 1
	case AuthorityPhaseActive:
		return 2
	case AuthorityPhaseChild:
		return 3
	default:
		return -1
	}
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
	AuthorityJournal  string
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
	journal := NewFileAuthorityJournal(launch.AuthorityJournal)
	if err := journal.BeginOwned(launch.Identity, os.Getpid(), platform.ProcessAlive); err != nil {
		return nil, err
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
	cmd.Env = setEnv(cmd.Env, envAuthorityJournal, launch.AuthorityJournal)
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

	process := newChildProcess(cmd, launch.Identity, eventReader, commandWriter, journal, s.commandTimeout())
	readyCtx, cancel := context.WithTimeout(ctx, s.readyTimeout())
	defer cancel()
	if err := process.waitFor(readyCtx, messageReady); err != nil {
		if !process.reconcilePhase(AuthorityPhaseReady, processReady) {
			_ = process.Abort()
			return nil, fmt.Errorf("wait for hot restart child readiness: %w", err)
		}
	} else {
		process.mu.Lock()
		process.state = processReady
		process.mu.Unlock()
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
	processStarting processState = iota + 1
	processReady
	processActivated
	processAuthority
	processAborted
)

type ChildProcess struct {
	cmd          *exec.Cmd
	identity     Identity
	events       chan eventResult
	commands     *os.File
	eventFile    *os.File
	journal      *FileAuthorityJournal
	done         chan struct{}
	waitMu       sync.Mutex
	waitErr      error
	mu           sync.Mutex
	transitionMu sync.Mutex
	completed    map[processState]error
	state        processState
	timeout      time.Duration
}

type eventResult struct {
	message message
	err     error
}

func newChildProcess(cmd *exec.Cmd, identity Identity, eventFile, commandFile *os.File, journal *FileAuthorityJournal, timeout time.Duration) *ChildProcess {
	process := &ChildProcess{
		cmd: cmd, identity: identity, events: make(chan eventResult, 1), commands: commandFile,
		eventFile: eventFile, journal: journal, done: make(chan struct{}), completed: make(map[processState]error),
		state: processStarting, timeout: timeout,
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
	p.transitionMu.Lock()
	defer p.transitionMu.Unlock()
	if completedErr, ok := p.completed[to]; ok {
		return completedErr
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
		phase := AuthorityPhaseActive
		if to == processAuthority {
			phase = AuthorityPhaseChild
		}
		if p.reconcilePhase(phase, to) {
			p.completed[to] = nil
			return nil
		}
		_ = p.Abort()
		p.completed[to] = err
		return err
	}
	p.mu.Lock()
	if p.state != from {
		p.mu.Unlock()
		return errors.New("hot restart state changed while awaiting child acknowledgement")
	}
	p.state = to
	p.mu.Unlock()
	p.completed[to] = nil
	return nil
}

func (p *ChildProcess) reconcilePhase(phase AuthorityPhase, state processState) bool {
	if p == nil || p.journal == nil || !p.isAlive() {
		return false
	}
	record, err := p.journal.Load()
	if err != nil || record.Identity != p.identity || authorityPhaseRank(record.Phase) < authorityPhaseRank(phase) || record.ChildPID != p.cmd.Process.Pid {
		return false
	}
	p.mu.Lock()
	if p.state != processAborted && p.state < state {
		p.state = state
	}
	p.mu.Unlock()
	return true
}

func (p *ChildProcess) isAlive() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
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
	waitErr := p.Wait()
	if p.journal != nil && p.cmd != nil && p.cmd.Process != nil {
		_, _, _ = p.journal.Recover(p.identity, func(pid int) bool {
			return pid == os.Getpid()
		})
	}
	return waitErr
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
	journal           *FileAuthorityJournal
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
	journalPath := strings.TrimSpace(os.Getenv(envAuthorityJournal))
	if journalPath == "" {
		_ = events.Close()
		_ = commands.Close()
		return nil, true, errors.New("hot restart authority journal path is required")
	}
	session := &ChildSession{
		Identity: identity, StreamDescriptors: descriptors, events: events, commands: commands,
		journal: NewFileAuthorityJournal(journalPath),
	}
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
	if err := s.journal.Advance(s.Identity, os.Getpid(), AuthorityPhaseReady); err != nil {
		return err
	}
	return s.send(messageReady, nil)
}

func (s *ChildSession) ConsumeStreamListeners() (*StreamSet, error) {
	if s == nil {
		return nil, errors.New("hot restart child session is required")
	}
	set, err := ImportStreamListeners(s.StreamDescriptors, s.StreamFiles)
	s.StreamFiles = nil
	return set, err
}

func (s *ChildSession) AwaitActivation(ctx context.Context, activate func() error) error {
	return s.await(ctx, messageActivate, messageActivated, AuthorityPhaseActive, activate)
}

func (s *ChildSession) AwaitAuthority(ctx context.Context, transfer func() error) error {
	return s.await(ctx, messageAuthority, messageAuthorityAck, AuthorityPhaseChild, transfer)
}

func (s *ChildSession) await(ctx context.Context, commandType, responseType messageType, phase AuthorityPhase, action func() error) error {
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
		if err := s.journal.Advance(s.Identity, os.Getpid(), phase); err != nil {
			_ = s.send(messageFailed, err)
			return err
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
