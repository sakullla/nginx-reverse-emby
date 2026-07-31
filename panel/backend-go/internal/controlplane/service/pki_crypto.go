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
	"strings"
)

const (
	pkiVaultMasterKeySize = 32
	pkiVaultMaxRecordSize = 1 << 20
)

var (
	pkiVaultMagic      = []byte("NREPKIV1")
	ErrPKIVaultInvalid = errors.New("invalid internal PKI vault")
)

type PKIVaultConfig struct {
	DataRoot      string
	MasterKeyFile string
	Random        io.Reader
}

type PKIVault struct {
	vaultDir      string
	masterKeyFile string
	masterKey     []byte
	random        io.Reader
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
	pkiRoot := filepath.Join(dataRoot, "pki")
	vaultDir := filepath.Join(pkiRoot, "vault")
	if err := ensurePKIRestrictedDirectory(pkiRoot); err != nil {
		return nil, err
	}
	if err := ensurePKIRestrictedDirectory(vaultDir); err != nil {
		return nil, err
	}
	masterKeyFile := strings.TrimSpace(config.MasterKeyFile)
	if masterKeyFile == "" {
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
	masterKey, err := loadOrCreatePKIMasterKey(masterKeyFile, randomSource)
	if err != nil {
		return nil, err
	}
	return &PKIVault{
		vaultDir:      vaultDir,
		masterKeyFile: masterKeyFile,
		masterKey:     masterKey,
		random:        randomSource,
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
	if err := writePKIRestrictedFile(filepath.Join(v.vaultDir, reference), payload); err != nil {
		// Publication may have completed before a cleanup/directory-sync error,
		// or another process may have won the no-clobber race. An authenticated
		// identical record is an idempotent retry; re-sync its parent before
		// reporting success.
		existing, openErr := v.OpenCAKey(reference, pkiDomainID, generation, purpose)
		if openErr == nil && bytes.Equal(existing, plaintext) {
			if syncErr := syncPKIVaultDirectory(v.vaultDir); syncErr == nil {
				return reference, nil
			} else {
				return "", errors.Join(err, syncErr)
			}
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

func loadOrCreatePKIMasterKey(path string, randomSource io.Reader) ([]byte, error) {
	key, err := readPKIMasterKey(path)
	if err == nil {
		return key, syncPKIVaultDirectory(filepath.Dir(path))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, pkiVaultMasterKeySize)
	if _, err := io.ReadFull(randomSource, key); err != nil {
		return nil, fmt.Errorf("generate PKI vault master key: %w", err)
	}
	if err := writePKIRestrictedFile(path, key); err != nil {
		published, readErr := readPKIMasterKey(path)
		if readErr == nil {
			if syncErr := syncPKIVaultDirectory(filepath.Dir(path)); syncErr != nil {
				return nil, errors.Join(err, syncErr)
			}
			return published, nil
		}
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
	return writePKIRestrictedFileFromReader(path, bytes.NewReader(payload), int64(len(payload)))
}

// writePKIRestrictedFileFromReader publishes only a completely written and
// durable inode. A hard link is the portable same-directory no-clobber publish
// primitive: concurrent creators get os.ErrExist and can safely read the
// winner, while a failed writer leaves no canonical partial file behind.
func writePKIRestrictedFileFromReader(path string, source io.Reader, expectedSize int64) (returnErr error) {
	if source == nil || expectedSize < 0 {
		return fmt.Errorf("%w: restricted file source is invalid", ErrPKIVaultInvalid)
	}
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
			removeErr := os.Remove(temporaryPath)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			returnErr = errors.Join(returnErr, removeErr, syncPKIVaultDirectory(directory))
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
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	temporaryPath = ""
	return syncPKIVaultDirectory(directory)
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
