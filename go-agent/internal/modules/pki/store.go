package pki

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	storeDirName       = "pki"
	identitiesDirName  = "identities"
	securityDirName    = "security"
	generationsDirName = "generations"
	pendingDirName     = "pending"

	pendingJournalName  = "request.json"
	pendingKeyName      = "private-key.pem"
	pendingCSRName      = "request.csr.pem"
	activePointerName   = "active.json"
	manifestName        = "manifest.json"
	certificateName     = "certificate.pem"
	privateKeyName      = "private-key.pem"
	securityName        = "security.json"
	acknowledgementName = "ack.json"
)

var safeIdentityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

var windowsReservedIdentity = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

type Store struct {
	dataRoot     string
	root         string
	clock        func() time.Time
	random       io.Reader
	checkpoint   func(string) error
	ackNeedsSync bool
	mu           sync.Mutex
}

type Option func(*Store)

func WithClock(clock func() time.Time) Option {
	return func(store *Store) {
		if clock != nil {
			store.clock = clock
		}
	}
}

func WithRandom(random io.Reader) Option {
	return func(store *Store) {
		if random != nil {
			store.random = random
		}
	}
}

// withPersistenceCheckpoint is intentionally package-private. It gives the
// deterministic persistence tests a way to model process loss at committed
// filesystem boundaries without exposing fault injection in production APIs.
func withPersistenceCheckpoint(checkpoint func(string) error) Option {
	return func(store *Store) {
		store.checkpoint = checkpoint
	}
}

// NewStore creates a credential store below the supplied agent data root.
// Remote and embedded agents must pass distinct data roots.
func NewStore(dataRoot string, options ...Option) (*Store, error) {
	dataRoot = strings.TrimSpace(dataRoot)
	if dataRoot == "" {
		return nil, errors.New("PKI data root is required")
	}
	absolute, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve PKI data root: %w", err)
	}
	store := &Store{
		dataRoot: absolute,
		root:     filepath.Join(absolute, storeDirName),
		clock:    time.Now,
		random:   rand.Reader,
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	if err := ensureStoreBaseDir(absolute); err != nil {
		return nil, err
	}
	if err := ensureDurablePrivateSubdir(absolute, storeDirName, store.random); err != nil {
		return nil, err
	}
	if err := ensureDurablePrivateSubdir(store.root, identitiesDirName, store.random); err != nil {
		return nil, err
	}
	if err := ensureDurablePrivateSubdir(store.root, securityDirName, store.random); err != nil {
		return nil, err
	}
	return store, nil
}

func ensureStoreBaseDir(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("PKI data root is not a directory: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create PKI data root %s: %w", path, err)
	}
	return nil
}

func ensureDurablePrivateSubdir(parent, name string, random io.Reader) error {
	target := filepath.Join(parent, name)
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("PKI path is not a private directory: %s", target)
		}
		if err := ensurePrivateDir(target); err != nil {
			return err
		}
		if err := syncDirectory(target); err != nil {
			return err
		}
		return syncDirectory(parent)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	suffix, err := randomHex(random, 8)
	if err != nil {
		return err
	}
	temporary := filepath.Join(parent, "."+name+"-new-"+suffix)
	if err := os.Mkdir(temporary, 0o700); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return err
	}
	if err := restrictPath(temporary, true); err != nil {
		return err
	}
	if err := syncDirectory(temporary); err != nil {
		return err
	}
	if err := publishDirectory(temporary, target); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(parent)
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) identityRoot(identity string) (string, error) {
	identity = strings.TrimSpace(identity)
	if !validStorageIdentity(identity) {
		return "", ErrInvalidIdentity
	}
	return filepath.Join(s.root, identitiesDirName, identity), nil
}

func validStorageIdentity(identity string) bool {
	return safeIdentityPattern.MatchString(identity) && !windowsReservedIdentity.MatchString(identity)
}

func (s *Store) persistenceCheckpoint(name string) error {
	if s == nil || s.checkpoint == nil {
		return nil
	}
	return s.checkpoint(name)
}

func ensurePrivateDir(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("PKI path is not a private directory: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create PKI directory %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("restrict PKI directory %s: %w", path, err)
	}
	return restrictPath(path, true)
}

func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("PKI file is not a regular file: %s", path)
	}
	return os.ReadFile(path)
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err := file.Write(data); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := restrictPath(path, false); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func writePrivateJSON(path string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := writePrivateFile(path, encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func writeImmutablePrivateFile(dir, name string, data []byte, random io.Reader) error {
	suffix, err := randomHex(random, 8)
	if err != nil {
		return err
	}
	temporary := filepath.Join(dir, "."+name+".tmp-"+suffix)
	target := filepath.Join(dir, name)
	if err := writePrivateFile(temporary, data); err != nil {
		return err
	}
	if err := publishImmutableFile(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(dir)
}

func writeAtomicPrivateJSON(dir, name string, value any, random io.Reader) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	suffix, err := randomHex(random, 8)
	if err != nil {
		return nil, err
	}
	temporary := filepath.Join(dir, "."+name+".tmp-"+suffix)
	target := filepath.Join(dir, name)
	if err := writePrivateFile(temporary, encoded); err != nil {
		return nil, err
	}
	if err := replaceActiveFile(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return nil, err
	}
	if err := syncDirectory(dir); err != nil {
		return nil, err
	}
	return encoded, nil
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func randomHex(source io.Reader, size int) (string, error) {
	if source == nil || size <= 0 {
		return "", errors.New("secure random source is unavailable")
	}
	value := make([]byte, size)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
