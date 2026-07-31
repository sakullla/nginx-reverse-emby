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
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, pkiVaultMasterKeySize)
	if _, err := io.ReadFull(randomSource, key); err != nil {
		return nil, fmt.Errorf("generate PKI vault master key: %w", err)
	}
	if err := writePKIRestrictedFile(path, key); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readPKIMasterKey(path)
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
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
