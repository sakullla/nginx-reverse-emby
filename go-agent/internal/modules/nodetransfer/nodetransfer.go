// Package nodetransfer implements the agent-side endpoints of the
// host-mediated node transfer: a sender serves one plugin workspace artifact
// from a loopback listener, and a receiver pulls one artifact through the
// entry-agent bridge of a reverse channel into the plugin-managed backup
// directory. Artifact bytes only flow over the channel data plane, which is
// mutually authenticated with agent-identity tunnel PKI credentials; this
// package never opens a non-TLS listener or accepts an unauthenticated peer.
package nodetransfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	StatePending      = "pending"
	StateTransferring = "transferring"
	StateCompleted    = "completed"
	StateFailed       = "failed"

	KindSend    = "send"
	KindReceive = "receive"

	// backupsDir is the plugin-managed backup directory every artifact is
	// served from and stored in; the flat artifact name never escapes it.
	backupsDir = "backups"

	// acceptWait bounds how long a sender waits for the receiver's connection
	// to arrive through the channel before failing the send side.
	acceptWait = 10 * time.Minute
	// dialWait bounds the receiver's connection to the entry-agent bridge.
	dialWait = 30 * time.Second
)

// Config configures a Service. WorkspaceRoot is the docker-compose plugin
// resource root (plugin-resources/docker-compose under the agent data
// directory). Artifact paths resolve strictly inside
// <WorkspaceRoot>/<instanceID>/<appID>/backups, matching the workspace
// layout the plugin sandbox is bound to.
type Config struct {
	WorkspaceRoot string
}

type transfer struct {
	mu    sync.Mutex
	id    string
	kind  string
	state string
	bytes int64
	err   string

	cancel  context.CancelFunc
	closers []func() error
}

type Service struct {
	cfg Config

	mu     sync.Mutex
	active map[string]*transfer
	closed bool
}

func NewService(cfg Config) (*Service, error) {
	root := filepath.Clean(strings.TrimSpace(cfg.WorkspaceRoot))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return nil, errors.New("node transfer workspace root is invalid")
	}
	return &Service{cfg: Config{WorkspaceRoot: root}, active: make(map[string]*transfer)}, nil
}

// artifactPath resolves <root>/<instanceID>/<appID>/backups/<artifactName>
// and rejects any escape from the instance workspace the sandbox is bound to.
func (s *Service) artifactPath(instanceID, appID, artifactName string) (string, error) {
	if !validName(instanceID) || !validName(appID) || !validName(artifactName) {
		return "", errors.New("node transfer artifact identity is invalid")
	}
	path := filepath.Join(s.cfg.WorkspaceRoot, instanceID, appID, backupsDir, artifactName)
	if filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))) != s.cfg.WorkspaceRoot {
		return "", errors.New("node transfer artifact escapes the managed root")
	}
	return path, nil
}

func validName(value string) bool {
	if value == "" || len(value) > 190 || value != strings.TrimSpace(value) || strings.HasPrefix(value, "-") || strings.Contains(value, "..") {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char) {
			continue
		}
		return false
	}
	return true
}



func (s *Service) record(id string, current *transfer) (*transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("node transfer service is closed")
	}
	if _, exists := s.active[id]; exists {
		return nil, fmt.Errorf("node transfer %q already exists", id)
	}
	s.active[id] = current
	return current, nil
}

func (t *transfer) update(mutate func(*transfer)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mutate(t)
}

func (t *transfer) snapshot() (kind, state string, bytes int64, lastError string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kind, t.state, t.bytes, t.err
}

// OpenSend starts serving one artifact on a loopback listener and returns the
// address the channel exit role will use as its backend. The listener accepts
// exactly one connection and stops; the artifact is streamed, never buffered.
func (s *Service) OpenSend(ctx context.Context, transferID, instanceID, appID, artifactName string) (string, error) {
	path, err := s.artifactPath(instanceID, appID, artifactName)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("node transfer source artifact is missing")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", errors.New("node transfer sender could not bind a loopback listener")
	}
	opCtx, cancel := context.WithCancel(context.Background())
	current := &transfer{id: transferID, kind: KindSend, state: StatePending, cancel: cancel, closers: []func() error{listener.Close}}
	if _, err := s.record(transferID, current); err != nil {
		cancel()
		_ = listener.Close()
		return "", err
	}
	go func() {
		defer func() {
			cancel()
			_ = listener.Close()
		}()
		conn, acceptErr := acceptOne(opCtx, listener)
		if acceptErr != nil {
			current.update(func(t *transfer) { t.state = StateFailed; t.err = "node transfer sender accept failed" })
			return
		}
		current.update(func(t *transfer) { t.state = StateTransferring })
		file, openErr := os.Open(path)
		if openErr != nil {
			_ = conn.Close()
			current.update(func(t *transfer) { t.state = StateFailed; t.err = "node transfer source artifact could not be opened" })
			return
		}
		written, copyErr := io.Copy(conn, file)
		_ = file.Close()
		_ = conn.Close()
		if copyErr != nil || written != info.Size() {
			current.update(func(t *transfer) { t.state = StateFailed; t.err = "node transfer send was incomplete" })
			return
		}
		current.update(func(t *transfer) { t.state = StateCompleted; t.bytes = written })
	}()
	return listener.Addr().String(), nil
}

func acceptOne(ctx context.Context, listener net.Listener) (net.Conn, error) {
	accepted := make(chan net.Conn, 1)
	errorsCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errorsCh <- err
			return
		}
		accepted <- conn
	}()
	timer := time.NewTimer(acceptWait)
	defer timer.Stop()
	select {
	case conn := <-accepted:
		return conn, nil
	case err := <-errorsCh:
		return nil, err
	case <-timer.C:
		return nil, errors.New("timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// StartReceive pulls one artifact through the entry-agent bridge address and
// stores it under the plugin-managed backup directory of the app workspace.
// The artifact lands only after size and SHA-256 verification, and the read
// is capped at the expected size so a misbehaving stream cannot exhaust disk.
func (s *Service) StartReceive(transferID, instanceID, appID, artifactName, bridgeAddress string, size int64, digest string) error {
	path, err := s.artifactPath(instanceID, appID, artifactName)
	if err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(strings.TrimSpace(bridgeAddress)); err != nil {
		return errors.New("node transfer bridge address is invalid")
	}
	if size <= 0 {
		return errors.New("node transfer artifact size is invalid")
	}
	if len(digest) != 64 {
		return errors.New("node transfer artifact sha256 is invalid")
	}
	backupDir := filepath.Dir(path)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return errors.New("node transfer backup directory could not be prepared")
	}
	opCtx, cancel := context.WithCancel(context.Background())
	current := &transfer{id: transferID, kind: KindReceive, state: StatePending, cancel: cancel}
	if _, err := s.record(transferID, current); err != nil {
		cancel()
		return err
	}
	go func() {
		defer cancel()
		if receiveErr := receiveArtifact(opCtx, current, path, s.cfg.WorkspaceRoot, instanceID, bridgeAddress, size, digest); receiveErr != nil {
			current.update(func(t *transfer) { t.state = StateFailed; t.err = receiveErr.Error() })
			return
		}
		current.update(func(t *transfer) { t.state = StateCompleted; t.bytes = size })
	}()
	return nil
}

func receiveArtifact(ctx context.Context, current *transfer, path, workspaceRoot, instanceID, bridgeAddress string, size int64, digest string) error {
	dialer := &net.Dialer{Timeout: dialWait}
	conn, err := dialer.DialContext(ctx, "tcp", strings.TrimSpace(bridgeAddress))
	if err != nil {
		return errors.New("node transfer bridge could not be reached")
	}
	defer func() { _ = conn.Close() }()
	current.update(func(t *transfer) { t.state = StateTransferring })
	part := path + ".incoming-" + hex.EncodeToString(randomID())
	defer func() { _ = os.Remove(part) }()
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("node transfer staging file could not be created")
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(conn, size+1))
	if syncErr := file.Sync(); syncErr == nil {
		_ = file.Close()
	} else {
		_ = file.Close()
		if copyErr == nil {
			copyErr = syncErr
		}
	}
	if copyErr != nil {
		return errors.New("node transfer receive was interrupted")
	}
	if written != size || hex.EncodeToString(hasher.Sum(nil)) != digest {
		return errors.New("node transfer artifact verification failed")
	}
	if err := inheritWorkspaceOwner(workspaceRoot, instanceID, filepath.Dir(path), part); err != nil {
		return err
	}
	if err := os.Rename(part, path); err != nil {
		return errors.New("node transfer artifact could not be stored")
	}
	return nil
}

// Status reports the live state of one transfer on this agent.
func (s *Service) Status(transferID string) (kind, state string, bytes int64, lastError string, err error) {
	s.mu.Lock()
	current, exists := s.active[transferID]
	s.mu.Unlock()
	if !exists {
		return "", "", 0, "", fmt.Errorf("node transfer %q is unknown", transferID)
	}
	kind, state, bytes, lastError = current.snapshot()
	return kind, state, bytes, lastError, nil
}

// Close stops one transfer's background work. Completed records are dropped;
// in-flight transfers move to failed.
func (s *Service) Close(transferID string) error {
	s.mu.Lock()
	current, exists := s.active[transferID]
	if exists {
		delete(s.active, transferID)
	}
	s.mu.Unlock()
	if !exists {
		return nil
	}
	current.cancel()
	for _, closer := range current.closers {
		_ = closer()
	}
	return nil
}

// Shutdown releases every transfer and listener.
func (s *Service) Shutdown() error {
	s.mu.Lock()
	s.closed = true
	transfers := make([]*transfer, 0, len(s.active))
	for _, current := range s.active {
		transfers = append(transfers, current)
	}
	s.active = make(map[string]*transfer)
	s.mu.Unlock()
	var firstErr error
	for _, current := range transfers {
		current.cancel()
		for _, closer := range current.closers {
			if err := closer(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func randomID() []byte {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return []byte(time.Now().Format("150405.000000000"))
	}
	return buf
}
