package pki

import (
	"bytes"
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
	renewalStateName    = "renewal.json"
)

var safeIdentityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

var windowsReservedIdentity = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

var pendingCandidatePattern = regexp.MustCompile(`^\.pending-[0-9a-f]{16}$`)

var generationCandidatePattern = regexp.MustCompile(`^\.candidate-[0-9a-f]{16}$`)

var pendingTombstonePattern = regexp.MustCompile(`^\.pending-tombstone-[0-9a-f]{32}-[0-9a-f]{16}$`)

type Store struct {
	dataRoot     string
	root         string
	clock        func() time.Time
	random       io.Reader
	checkpoint   func(string) error
	syncDir      func(string) error
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

func withDirectorySync(syncer func(string) error) Option {
	return func(store *Store) {
		if syncer != nil {
			store.syncDir = syncer
		}
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
		syncDir:  syncDirectory,
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	if err := ensureStoreBaseDir(absolute, store.random); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(absolute); err != nil {
		return nil, fmt.Errorf("secure PKI data root: %w", err)
	}
	if err := store.persistenceCheckpoint("store.after_data_root_publish"); err != nil {
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
	if err := migratePrivateTree(store.root); err != nil {
		return nil, err
	}
	if err := cleanupAbandonedPrivateSubdirs(filepath.Join(store.root, identitiesDirName), validStorageIdentity); err != nil {
		return nil, err
	}
	if err := cleanupAbandonedPrivateSubdirs(filepath.Join(store.root, securityDirName), func(name string) bool { return name == "snapshots" }); err != nil {
		return nil, err
	}
	if err := cleanupIdentityCrashArtifacts(filepath.Join(store.root, identitiesDirName)); err != nil {
		return nil, err
	}
	return store, nil
}

func ensureStoreBaseDir(path string, random io.Reader) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("PKI data root is not a directory: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("PKI data root ancestor is not a directory: %s", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("create PKI data root %s: no durable parent directory", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := ensureDurablePrivateSubdir(current, missing[index], random); err != nil {
			return fmt.Errorf("create PKI data root %s: %w", path, err)
		}
		current = filepath.Join(current, missing[index])
	}
	return nil
}

func ensureDurablePrivateSubdir(parent, name string, random io.Reader) error {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("invalid private subdirectory name %q", name)
	}
	if err := cleanupAbandonedPrivateSubdirs(parent, func(candidate string) bool { return candidate == name }); err != nil {
		return err
	}
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

func cleanupAbandonedPrivateSubdirs(parent string, allowed func(string) bool) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		name, ok := privateSubdirStagingTarget(entry.Name())
		if !ok || allowed == nil || !allowed(name) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("abandoned PKI staging path is unsafe: %s", path)
		}
		if err := verifyPrivatePath(path, true); err != nil {
			return err
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fmt.Errorf("abandoned PKI staging directory is not empty: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(parent)
	}
	return nil
}

func migratePrivateTree(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("PKI store contains an unsafe path: %s", path)
		}
		if err := migratePrivatePath(path, info.IsDir()); err != nil {
			return fmt.Errorf("secure existing PKI path %s: %w", path, err)
		}
		return nil
	})
}

func cleanupIdentityCrashArtifacts(identitiesRoot string) error {
	entries, err := os.ReadDir(identitiesRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !validStorageIdentity(entry.Name()) {
			continue
		}
		identityRoot := filepath.Join(identitiesRoot, entry.Name())
		info, err := os.Lstat(identityRoot)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("PKI identity path is unsafe: %s", identityRoot)
		}
		if err := cleanupAbandonedPrivateTrees(identityRoot, func(name string) bool {
			return pendingCandidatePattern.MatchString(name) || pendingTombstonePattern.MatchString(name)
		}, map[string]struct{}{
			pendingJournalName: {}, pendingKeyName: {}, pendingCSRName: {}, "request-id": {}, "response.json": {},
		}); err != nil {
			return err
		}
		generationsRoot := filepath.Join(identityRoot, generationsDirName)
		if generationInfo, statErr := os.Lstat(generationsRoot); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		} else if generationInfo.Mode()&os.ModeSymlink != 0 || !generationInfo.IsDir() {
			return fmt.Errorf("PKI generations path is unsafe: %s", generationsRoot)
		}
		if err := cleanupAbandonedPrivateTrees(generationsRoot, generationCandidatePattern.MatchString, map[string]struct{}{
			manifestName: {}, privateKeyName: {}, certificateName: {}, securityName: {},
		}); err != nil {
			return err
		}
	}
	return nil
}

func cleanupAbandonedPrivateTrees(parent string, matches func(string) bool, allowedFiles map[string]struct{}) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if matches == nil || !matches(entry.Name()) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		if err := validateAbandonedPrivateTree(path, allowedFiles); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(parent)
	}
	return nil
}

func validateAbandonedPrivateTree(path string, allowedFiles map[string]struct{}) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("abandoned PKI private tree is unsafe: %s", path)
	}
	if err := verifyPrivatePath(path, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, ok := allowedFiles[entry.Name()]; !ok {
			return fmt.Errorf("abandoned PKI private tree contains unexpected entry %q", entry.Name())
		}
		child := filepath.Join(path, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil {
			return err
		}
		if childInfo.Mode()&os.ModeSymlink != 0 || !childInfo.Mode().IsRegular() {
			return fmt.Errorf("abandoned PKI private tree contains an unsafe entry: %s", child)
		}
		if err := verifyPrivatePath(child, false); err != nil {
			return err
		}
	}
	return nil
}

func privateSubdirStagingTarget(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	body := strings.TrimPrefix(name, ".")
	separator := strings.LastIndex(body, "-new-")
	if separator <= 0 {
		return "", false
	}
	target, suffix := body[:separator], body[separator+len("-new-"):]
	if len(suffix) != 16 || !validLowerHex(suffix) || filepath.Base(target) != target {
		return "", false
	}
	return target, true
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
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !openedInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return nil, fmt.Errorf("PKI file is not a regular file: %s", path)
	}
	if err := verifyPrivateFile(file); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
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

func writeImmutablePrivateFile(dir, name string, data []byte, random io.Reader, syncer func(string) error) error {
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
		existing, readErr := readPrivateFile(target)
		if readErr != nil || !bytes.Equal(existing, data) {
			_ = os.Remove(temporary)
			return err
		}
		_ = os.Remove(temporary)
	}
	if syncer == nil {
		syncer = syncDirectory
	}
	return syncer(dir)
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
		existing, readErr := readPrivateFile(target)
		_ = os.Remove(temporary)
		if readErr == nil && bytes.Equal(existing, encoded) {
			return encoded, &ActivationCommittedError{Stage: "active pointer replacement", Cause: err}
		}
		return nil, err
	}
	if err := syncDirectory(dir); err != nil {
		return encoded, &ActivationCommittedError{Stage: "active pointer directory sync", Cause: err}
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

// LoadRenewalState loads the crash-safe scheduling record for one identity.
func (s *Store) LoadRenewalState(storageIdentity string) (RenewalState, error) {
	if s == nil {
		return RenewalState{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadRenewalStateLocked(storageIdentity)
}

func (s *Store) loadRenewalStateLocked(storageIdentity string) (RenewalState, error) {
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return RenewalState{}, err
	}
	encoded, err := readPrivateFile(filepath.Join(identityRoot, renewalStateName))
	if errors.Is(err, os.ErrNotExist) {
		return RenewalState{}, ErrRenewalStateNotFound
	}
	if err != nil {
		return RenewalState{}, err
	}
	var state RenewalState
	if err := decodeStrictJSON(encoded, &state); err != nil {
		return RenewalState{}, fmt.Errorf("decode PKI renewal state: %w", err)
	}
	return validateRenewalState(state, false)
}

// SaveRenewalState atomically persists a normalized record and stamps it with
// Store's trusted clock. An unchanged record is returned without rewriting it.
func (s *Store) SaveRenewalState(storageIdentity string, state RenewalState) (RenewalState, error) {
	if s == nil {
		return RenewalState{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return RenewalState{}, err
	}
	if err := ensureDurablePrivateSubdir(filepath.Join(s.root, identitiesDirName), strings.TrimSpace(storageIdentity), s.random); err != nil {
		return RenewalState{}, err
	}
	state.Version = 1
	state.UpdatedAt = s.clock().UTC()
	state, err = validateRenewalState(state, true)
	if err != nil {
		return RenewalState{}, err
	}
	if existing, loadErr := s.loadRenewalStateLocked(storageIdentity); loadErr == nil {
		candidate := state
		candidate.UpdatedAt = existing.UpdatedAt
		if reflectRenewalStateEqual(existing, candidate) {
			return existing, nil
		}
	} else if !errors.Is(loadErr, ErrRenewalStateNotFound) {
		return RenewalState{}, loadErr
	}
	if _, err := writeAtomicPrivateJSON(identityRoot, renewalStateName, state, s.random); err != nil {
		return state, fmt.Errorf("persist PKI renewal state: %w", err)
	}
	return state, nil
}

func validateRenewalState(state RenewalState, normalize bool) (RenewalState, error) {
	originalIdentity := state.CredentialIdentity
	originalFingerprint := state.CredentialFingerprint
	originalReason := state.Reason
	state.CredentialIdentity = strings.TrimSpace(state.CredentialIdentity)
	state.CredentialFingerprint = strings.TrimSpace(state.CredentialFingerprint)
	state.Reason = strings.TrimSpace(state.Reason)
	if !normalize && (state.CredentialIdentity != originalIdentity || state.CredentialFingerprint != originalFingerprint || state.Reason != originalReason) {
		return RenewalState{}, fmt.Errorf("%w: PKI renewal state is not canonical", ErrCredentialInvalid)
	}
	if state.Version != 1 || state.CredentialIdentity == "" || len(state.CredentialIdentity) > 256 ||
		len(state.CredentialFingerprint) != sha256.Size*2 || !validLowerHex(state.CredentialFingerprint) ||
		state.DueAt.IsZero() || state.FailureCount < 0 || state.UpdatedAt.IsZero() {
		return RenewalState{}, fmt.Errorf("%w: PKI renewal state is incomplete", ErrCredentialInvalid)
	}
	if state.ReenrollmentRequired != (state.Reason != "") || len(state.Reason) > 256 || strings.ContainsAny(state.Reason, "\r\n\x00") {
		return RenewalState{}, fmt.Errorf("%w: PKI renewal recovery reason is invalid", ErrCredentialInvalid)
	}
	state.DueAt = state.DueAt.UTC()
	if !state.NextAttemptAt.IsZero() {
		state.NextAttemptAt = state.NextAttemptAt.UTC()
	}
	state.UpdatedAt = state.UpdatedAt.UTC()
	return state, nil
}

func reflectRenewalStateEqual(left, right RenewalState) bool {
	return left.Version == right.Version && left.CredentialIdentity == right.CredentialIdentity &&
		left.CredentialFingerprint == right.CredentialFingerprint && left.DueAt.Equal(right.DueAt) &&
		left.FailureCount == right.FailureCount && left.NextAttemptAt.Equal(right.NextAttemptAt) &&
		left.ReenrollmentRequired == right.ReenrollmentRequired && left.Reason == right.Reason && left.UpdatedAt.Equal(right.UpdatedAt)
}
