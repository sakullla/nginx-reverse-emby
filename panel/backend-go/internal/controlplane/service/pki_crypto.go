package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	pkiVaultMasterKeySize = 32
	pkiVaultMaxRecordSize = 1 << 20
	pkiDirectoryLockName  = ".nre-pki.publish.lock"
)

var (
	pkiVaultMagic      = []byte("NREPKIV1")
	ErrPKIVaultInvalid = errors.New("invalid internal PKI vault")
	errPKIVaultCleanup = errors.New("internal PKI staging cleanup failed")
)

type pkiDirectoryLock interface {
	Close() error
}

type pkiCryptoFileOps struct {
	acquireLock   func(string) (pkiDirectoryLock, error)
	rename        func(string, string) error
	remove        func(string) error
	syncDirectory func(string) error
}

func defaultPKICryptoFileOps() pkiCryptoFileOps {
	return pkiCryptoFileOps{
		acquireLock:   acquirePKIOSDirectoryLock,
		rename:        os.Rename,
		remove:        os.Remove,
		syncDirectory: syncPKIVaultDirectory,
	}
}

type PKIVaultConfig struct {
	DataRoot      string
	MasterKeyFile string
	Random        io.Reader
	fileOps       *pkiCryptoFileOps
}

type PKIVault struct {
	vaultDir      string
	masterKeyFile string
	masterKey     []byte
	random        io.Reader
	fileOps       pkiCryptoFileOps
}

func OpenPKIVault(config PKIVaultConfig) (*PKIVault, error) {
	dataRoot := strings.TrimSpace(config.DataRoot)
	if dataRoot == "" {
		return nil, fmt.Errorf("%w: data root is required", ErrPKIVaultInvalid)
	}
	randomSource := config.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	fileOps := resolvePKICryptoFileOps(config.fileOps)
	pkiRoot := filepath.Join(dataRoot, "pki")
	vaultDir := filepath.Join(pkiRoot, "vault")
	if err := ensurePKIRestrictedDirectory(pkiRoot); err != nil {
		return nil, err
	}
	if err := ensurePKIRestrictedDirectory(vaultDir); err != nil {
		return nil, err
	}
	masterKeyFile := strings.TrimSpace(config.MasterKeyFile)
	externalMasterKey := masterKeyFile != ""
	if !externalMasterKey {
		masterKeyFile = filepath.Join(pkiRoot, "master.key")
	} else if !filepath.IsAbs(masterKeyFile) {
		return nil, fmt.Errorf("%w: configured master key file must be absolute", ErrPKIVaultInvalid)
	} else {
		// A configured secret location may be owned by a secret manager. Validate
		// its parent, but never change permissions outside the PKI data root.
		parentInfo, err := os.Lstat(filepath.Dir(masterKeyFile))
		if err != nil {
			return nil, err
		}
		if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			return nil, fmt.Errorf("%w: configured master key parent is not a regular directory", ErrPKIVaultInvalid)
		}
	}
	masterKey, err := loadOrCreatePKIMasterKey(masterKeyFile, randomSource, fileOps, !externalMasterKey)
	if err != nil {
		return nil, err
	}
	if err := withPKIDirectoryLock(vaultDir, fileOps, func() error {
		return cleanupPKIStagingLocked(vaultDir, isPKIVaultCanonicalName, fileOps)
	}); err != nil {
		return nil, err
	}
	return &PKIVault{
		vaultDir:      vaultDir,
		masterKeyFile: masterKeyFile,
		masterKey:     masterKey,
		random:        randomSource,
		fileOps:       fileOps,
	}, nil
}

func (v *PKIVault) SealCAKey(pkiDomainID string, generation int64, purpose string, plaintext []byte) (string, error) {
	if v == nil || len(v.masterKey) != pkiVaultMasterKeySize {
		return "", fmt.Errorf("%w: vault is not open", ErrPKIVaultInvalid)
	}
	if err := validatePKIVaultIdentity(pkiDomainID, generation, purpose); err != nil {
		return "", err
	}
	if len(plaintext) == 0 || len(plaintext) > pkiVaultMaxRecordSize/2 {
		return "", fmt.Errorf("%w: CA key payload size is invalid", ErrPKIVaultInvalid)
	}
	block, err := aes.NewCipher(v.masterKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(v.random, nonce); err != nil {
		return "", fmt.Errorf("generate PKI vault nonce: %w", err)
	}
	aad := pkiVaultAAD(pkiDomainID, generation, purpose)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	payload := make([]byte, 0, len(pkiVaultMagic)+len(nonce)+len(ciphertext))
	payload = append(payload, pkiVaultMagic...)
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)
	reference := pkiVaultReference(pkiDomainID, generation, purpose)
	if err := writePKIRestrictedFileWithOps(filepath.Join(v.vaultDir, reference), payload, v.fileOps); err != nil {
		if errors.Is(err, errPKIVaultCleanup) {
			return "", err
		}
		existing, openErr := v.OpenCAKey(reference, pkiDomainID, generation, purpose)
		if errors.Is(err, os.ErrExist) && openErr == nil && bytes.Equal(existing, plaintext) {
			return reference, nil
		}
		return "", err
	}
	return reference, nil
}

func (v *PKIVault) OpenCAKey(reference, pkiDomainID string, generation int64, purpose string) ([]byte, error) {
	if v == nil || len(v.masterKey) != pkiVaultMasterKeySize {
		return nil, fmt.Errorf("%w: vault is not open", ErrPKIVaultInvalid)
	}
	if err := validatePKIVaultIdentity(pkiDomainID, generation, purpose); err != nil {
		return nil, err
	}
	if reference != pkiVaultReference(pkiDomainID, generation, purpose) || filepath.Base(reference) != reference {
		return nil, fmt.Errorf("%w: CA key reference does not match its authenticated identity", ErrPKIVaultInvalid)
	}
	path := filepath.Join(v.vaultDir, reference)
	if err := validatePKIRestrictedFile(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, pkiVaultMaxRecordSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(payload) > pkiVaultMaxRecordSize || !bytes.HasPrefix(payload, pkiVaultMagic) {
		return nil, fmt.Errorf("%w: malformed CA key record", ErrPKIVaultInvalid)
	}
	block, err := aes.NewCipher(v.masterKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	encoded := payload[len(pkiVaultMagic):]
	if len(encoded) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("%w: truncated CA key record", ErrPKIVaultInvalid)
	}
	nonce := encoded[:gcm.NonceSize()]
	ciphertext := encoded[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, pkiVaultAAD(pkiDomainID, generation, purpose))
	if err != nil {
		return nil, fmt.Errorf("%w: authenticate CA key record: %v", ErrPKIVaultInvalid, err)
	}
	return plaintext, nil
}

func loadOrCreatePKIMasterKey(path string, randomSource io.Reader, fileOps pkiCryptoFileOps, recoverStaging bool) ([]byte, error) {
	if !recoverStaging {
		key, err := readPKIMasterKey(path)
		if err == nil {
			return key, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	var key []byte
	err := withPKIDirectoryLock(filepath.Dir(path), fileOps, func() error {
		if err := cleanupPKIStagingLocked(filepath.Dir(path), func(name string) bool {
			return name == filepath.Base(path)
		}, fileOps); err != nil {
			return err
		}
		published, err := readPKIMasterKey(path)
		if err == nil {
			key = published
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		key = make([]byte, pkiVaultMasterKeySize)
		if _, err := io.ReadFull(randomSource, key); err != nil {
			return fmt.Errorf("generate PKI vault master key: %w", err)
		}
		return writePKIRestrictedFileLocked(path, bytes.NewReader(key), int64(len(key)), fileOps)
	})
	if err != nil {
		return nil, err
	}
	return key, nil
}

func readPKIMasterKey(path string) ([]byte, error) {
	if err := validatePKIRestrictedFile(path); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != pkiVaultMasterKeySize {
		return nil, fmt.Errorf("%w: master key must contain exactly 32 bytes", ErrPKIVaultInvalid)
	}
	return key, nil
}

func ensurePKIRestrictedDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %s is not a regular directory", ErrPKIVaultInvalid, path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

func writePKIRestrictedFile(path string, payload []byte) error {
	return writePKIRestrictedFileWithOps(path, payload, defaultPKICryptoFileOps())
}

func writePKIRestrictedFileWithOps(path string, payload []byte, fileOps pkiCryptoFileOps) error {
	return writePKIRestrictedFileFromReaderWithOps(path, bytes.NewReader(payload), int64(len(payload)), fileOps)
}

func writePKIRestrictedFileFromReader(path string, source io.Reader, expectedSize int64) error {
	return writePKIRestrictedFileFromReaderWithOps(path, source, expectedSize, defaultPKICryptoFileOps())
}

// writePKIRestrictedFileFromReaderWithOps serializes publishers across
// processes with an advisory lock. Inside that lock it removes recoverable
// staging leftovers, writes and fsyncs a restricted same-directory temporary,
// closes it, and atomically renames it to the canonical path.
func writePKIRestrictedFileFromReaderWithOps(path string, source io.Reader, expectedSize int64, fileOps pkiCryptoFileOps) error {
	if source == nil || expectedSize < 0 {
		return fmt.Errorf("%w: restricted file source is invalid", ErrPKIVaultInvalid)
	}
	directory := filepath.Dir(path)
	return withPKIDirectoryLock(directory, fileOps, func() error {
		if err := cleanupPKIStagingLocked(directory, func(name string) bool {
			return name == filepath.Base(path)
		}, fileOps); err != nil {
			return err
		}
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("publish restricted PKI file %s: %w", path, os.ErrExist)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writePKIRestrictedFileLocked(path, source, expectedSize, fileOps)
	})
}

func writePKIRestrictedFileLocked(path string, source io.Reader, expectedSize int64, fileOps pkiCryptoFileOps) (returnErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		if temporaryPath != "" {
			removeErr := fileOps.remove(temporaryPath)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			if removeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("%w: remove %s: %w", errPKIVaultCleanup, temporaryPath, removeErr))
			} else {
				returnErr = errors.Join(returnErr, syncPKIDirectoryIfSupported(fileOps, directory))
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	written, err := io.CopyN(temporary, source, expectedSize)
	if err != nil {
		return fmt.Errorf("write restricted PKI temporary file: %w", err)
	}
	if written != expectedSize {
		return fmt.Errorf("%w: restricted PKI temporary file was truncated", ErrPKIVaultInvalid)
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("publish restricted PKI file %s: %w", path, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fileOps.rename(temporaryPath, path); err != nil {
		return err
	}
	temporaryPath = ""
	if err := syncPKIDirectoryIfSupported(fileOps, directory); err != nil {
		removeErr := fileOps.remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("%w: roll back %s: %w", errPKIVaultCleanup, path, removeErr))
		}
		return errors.Join(err, syncPKIDirectoryIfSupported(fileOps, directory))
	}
	return nil
}

func withPKIDirectoryLock(directory string, fileOps pkiCryptoFileOps, mutate func() error) (returnErr error) {
	lock, err := fileOps.acquireLock(directory)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Close())
	}()
	return mutate()
}

func cleanupPKIStagingLocked(directory string, acceptsCanonical func(string) bool, fileOps pkiCryptoFileOps) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("%w: list %s: %v", errPKIVaultCleanup, directory, err)
	}
	removed := false
	for _, entry := range entries {
		canonical, ok := pkiStagingCanonicalName(entry.Name())
		if !ok || !acceptsCanonical(canonical) {
			continue
		}
		if entry.IsDir() {
			return fmt.Errorf("%w: staging path %s is a directory", errPKIVaultCleanup, filepath.Join(directory, entry.Name()))
		}
		path := filepath.Join(directory, entry.Name())
		if err := fileOps.remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("%w: remove %s: %w", errPKIVaultCleanup, path, err)
		}
		removed = true
	}
	if !removed {
		return nil
	}
	if err := syncPKIDirectoryIfSupported(fileOps, directory); err != nil {
		return fmt.Errorf("%w: sync %s: %w", errPKIVaultCleanup, directory, err)
	}
	return nil
}

func pkiStagingCanonicalName(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	marker := strings.LastIndex(name, ".tmp-")
	if marker <= 1 || marker+len(".tmp-") >= len(name) {
		return "", false
	}
	canonical := name[1:marker]
	if filepath.Base(canonical) != canonical {
		return "", false
	}
	return canonical, true
}

func isPKIVaultCanonicalName(name string) bool {
	if !strings.HasPrefix(name, "ca-") || !strings.HasSuffix(name, ".vault") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "ca-"), ".vault"), "-")
	if len(parts) != 2 || len(parts[1]) != 16 {
		return false
	}
	generation, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || generation <= 0 {
		return false
	}
	decoded, err := hex.DecodeString(parts[1])
	return err == nil && len(decoded) == 8
}

func resolvePKICryptoFileOps(overrides *pkiCryptoFileOps) pkiCryptoFileOps {
	resolved := defaultPKICryptoFileOps()
	if overrides == nil {
		return resolved
	}
	if overrides.acquireLock != nil {
		resolved.acquireLock = overrides.acquireLock
	}
	if overrides.rename != nil {
		resolved.rename = overrides.rename
	}
	if overrides.remove != nil {
		resolved.remove = overrides.remove
	}
	if overrides.syncDirectory != nil {
		resolved.syncDirectory = overrides.syncDirectory
	}
	return resolved
}

func syncPKIDirectoryIfSupported(fileOps pkiCryptoFileOps, path string) error {
	err := fileOps.syncDirectory(path)
	if errors.Is(err, errors.ErrUnsupported) || pkiDirectorySyncUnsupported(err) {
		return nil
	}
	return err
}

func syncPKIVaultDirectory(path string) error {
	// Windows does not expose directory handles that os.File.Sync can flush.
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validatePKIRestrictedFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrPKIVaultInvalid, path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s permissions must not grant group or other access", ErrPKIVaultInvalid, path)
	}
	return nil
}

func validatePKIVaultIdentity(pkiDomainID string, generation int64, purpose string) error {
	if strings.TrimSpace(pkiDomainID) == "" || strings.ContainsRune(pkiDomainID, '\x00') || generation <= 0 || strings.TrimSpace(purpose) == "" || strings.ContainsRune(purpose, '\x00') {
		return fmt.Errorf("%w: PKI domain, positive generation, and purpose are required", ErrPKIVaultInvalid)
	}
	return nil
}

func pkiVaultAAD(pkiDomainID string, generation int64, purpose string) []byte {
	buffer := bytes.NewBuffer(make([]byte, 0, len(pkiDomainID)+len(purpose)+32))
	buffer.WriteString("nre-internal-pki-ca-key-v1")
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(pkiDomainID)))
	buffer.WriteString(pkiDomainID)
	_ = binary.Write(buffer, binary.BigEndian, generation)
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(purpose)))
	buffer.WriteString(purpose)
	return buffer.Bytes()
}

func pkiVaultReference(pkiDomainID string, generation int64, purpose string) string {
	digest := sha256.Sum256(pkiVaultAAD(pkiDomainID, generation, purpose))
	return fmt.Sprintf("ca-%d-%s.vault", generation, hex.EncodeToString(digest[:8]))
}
