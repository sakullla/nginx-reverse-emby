package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	ErrKeyUnavailable = errors.New("vault master key unavailable")
	ErrInvalidSecret  = errors.New("invalid secret")
	ErrDecrypt        = errors.New("secret decryption failed")
)

type Store interface {
	CreateSecret(context.Context, storage.SecretRow, storage.SecretVersionRow) error
	RotateSecret(context.Context, storage.SecretRow, storage.SecretVersionRow) error
	GetSecret(context.Context, string) (storage.SecretRow, error)
	ListSecrets(context.Context, []string) ([]storage.SecretRow, error)
	GetSecretVersion(context.Context, string, uint64) (storage.SecretVersionRow, error)
	MarkSecretUsed(context.Context, string, time.Time) error
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
}

type Keyring struct {
	CurrentKeyID string
	Keys         map[string][]byte
}

type Vault struct {
	store   Store
	keyring Keyring
	now     func() time.Time
}

type Metadata struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Purpose         string     `json:"purpose"`
	OwnerUserID     string     `json:"owner_user_id"`
	ResourceGroupID string     `json:"resource_group_id"`
	ActiveVersion   uint64     `json:"active_version"`
	Fingerprint     string     `json:"fingerprint"`
	CreatedAt       time.Time  `json:"created_at"`
	RotatedAt       time.Time  `json:"rotated_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

type OperationContext struct {
	ActorID         string
	SessionID       string
	CorrelationID   string
	ResourceGroupID string
}

func NewVault(store Store, keyring Keyring) (*Vault, error) {
	if store == nil {
		return nil, fmt.Errorf("vault store is required")
	}
	keyID := strings.TrimSpace(keyring.CurrentKeyID)
	key := keyring.Keys[keyID]
	if keyID == "" || len(key) != 32 {
		return nil, ErrKeyUnavailable
	}
	keys := make(map[string][]byte, len(keyring.Keys))
	for id, candidate := range keyring.Keys {
		if strings.TrimSpace(id) == "" || len(candidate) != 32 {
			return nil, fmt.Errorf("%w: key %q must be 32 bytes", ErrKeyUnavailable, id)
		}
		keys[id] = append([]byte(nil), candidate...)
	}
	return &Vault{store: store, keyring: Keyring{CurrentKeyID: keyID, Keys: keys}, now: func() time.Time { return time.Now().UTC() }}, nil
}

// KeyringFromEnvironment loads the envelope key without persisting it in the
// database. The value may be raw base64, hex, or a 32-byte literal.
func KeyringFromEnvironment() (Keyring, error) {
	encoded := strings.TrimSpace(os.Getenv("PANEL_VAULT_MASTER_KEY"))
	if encoded == "" {
		return Keyring{}, ErrKeyUnavailable
	}
	key, err := decodeKey(encoded)
	if err != nil {
		return Keyring{}, err
	}
	keyID := strings.TrimSpace(os.Getenv("PANEL_VAULT_KEY_ID"))
	if keyID == "" {
		digest := sha256.Sum256(key)
		keyID = "env-" + hex.EncodeToString(digest[:6])
	}
	return Keyring{CurrentKeyID: keyID, Keys: map[string][]byte{keyID: key}}, nil
}

func decodeKey(encoded string) ([]byte, error) {
	for _, decode := range []func(string) ([]byte, error){base64.RawStdEncoding.DecodeString, base64.StdEncoding.DecodeString, hex.DecodeString} {
		if value, err := decode(encoded); err == nil && len(value) == 32 {
			return value, nil
		}
	}
	if len([]byte(encoded)) == 32 {
		return []byte(encoded), nil
	}
	return nil, fmt.Errorf("%w: PANEL_VAULT_MASTER_KEY must decode to 32 bytes", ErrKeyUnavailable)
}

func (v *Vault) Create(ctx context.Context, op OperationContext, name, purpose, plaintext string) (Metadata, error) {
	name = strings.TrimSpace(name)
	if name == "" || plaintext == "" || strings.TrimSpace(op.ResourceGroupID) == "" {
		return Metadata{}, ErrInvalidSecret
	}
	now := v.now()
	secretID := randomID("sec")
	row := storage.SecretRow{ID: secretID, Name: name, Purpose: strings.TrimSpace(purpose), OwnerUserID: op.ActorID, ResourceGroupID: op.ResourceGroupID, ActiveVersion: 1, Fingerprint: fingerprint(plaintext), CreatedAt: now, RotatedAt: now}
	version, err := v.encrypt(row, 1, plaintext, now)
	if err != nil {
		return Metadata{}, err
	}
	err = v.store.CreateSecret(ctx, row, version)
	v.audit(ctx, op, "secret.create", secretID, result(err), errorClass(err), map[string]any{"name": name, "purpose": purpose, "fingerprint": row.Fingerprint})
	if err != nil {
		return Metadata{}, err
	}
	return metadataFromRow(row), nil
}

// Generate creates a random value and is one of the only APIs allowed to
// return plaintext. Callers must not log or persist the returned value.
func (v *Vault) Generate(ctx context.Context, op OperationContext, name, purpose string, bytes int) (Metadata, string, error) {
	if bytes < 16 || bytes > 128 {
		bytes = 32
	}
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return Metadata{}, "", err
	}
	plaintext := base64.RawURLEncoding.EncodeToString(value)
	metadata, err := v.Create(ctx, op, name, purpose, plaintext)
	if err != nil {
		return Metadata{}, "", err
	}
	return metadata, plaintext, nil
}

func (v *Vault) Rotate(ctx context.Context, op OperationContext, id, plaintext string) (Metadata, error) {
	if plaintext == "" {
		return Metadata{}, ErrInvalidSecret
	}
	row, err := v.store.GetSecret(ctx, id)
	if err != nil {
		return Metadata{}, err
	}
	if op.ResourceGroupID != "" && row.ResourceGroupID != op.ResourceGroupID {
		return Metadata{}, ErrInvalidSecret
	}
	now := v.now()
	nextVersion := row.ActiveVersion + 1
	row.ActiveVersion = nextVersion
	row.Fingerprint = fingerprint(plaintext)
	row.RotatedAt = now
	version, err := v.encrypt(row, nextVersion, plaintext, now)
	if err != nil {
		return Metadata{}, err
	}
	err = v.store.RotateSecret(ctx, row, version)
	v.audit(ctx, op, "secret.rotate", id, result(err), errorClass(err), map[string]any{"fingerprint": row.Fingerprint, "version": nextVersion})
	if err != nil {
		return Metadata{}, err
	}
	return metadataFromRow(row), nil
}

func (v *Vault) Get(ctx context.Context, id string) (Metadata, error) {
	row, err := v.store.GetSecret(ctx, id)
	if err != nil {
		return Metadata{}, err
	}
	return metadataFromRow(row), nil
}

func (v *Vault) List(ctx context.Context, visibleGroupIDs []string) ([]Metadata, error) {
	rows, err := v.store.ListSecrets(ctx, visibleGroupIDs)
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(rows))
	for _, row := range rows {
		result = append(result, metadataFromRow(row))
	}
	return result, nil
}

// Resolve is for trusted control-plane consumers. It never exposes plaintext
// through HTTP metadata types and records only the secret fingerprint.
func (v *Vault) Resolve(ctx context.Context, op OperationContext, id string) ([]byte, error) {
	row, err := v.store.GetSecret(ctx, id)
	if err != nil {
		return nil, err
	}
	version, err := v.store.GetSecretVersion(ctx, id, row.ActiveVersion)
	if err != nil {
		return nil, err
	}
	plaintext, err := v.decrypt(row, version)
	v.audit(ctx, op, "secret.use", id, result(err), errorClass(err), map[string]any{"fingerprint": row.Fingerprint, "version": row.ActiveVersion})
	if err != nil {
		return nil, err
	}
	_ = v.store.MarkSecretUsed(ctx, id, v.now())
	return plaintext, nil
}

func (v *Vault) encrypt(secret storage.SecretRow, version uint64, plaintext string, now time.Time) (storage.SecretVersionRow, error) {
	keyID := v.keyring.CurrentKeyID
	aead, err := newAEAD(v.keyring.Keys[keyID])
	if err != nil {
		return storage.SecretVersionRow{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return storage.SecretVersionRow{}, err
	}
	digest := sha256.Sum256([]byte(plaintext))
	ciphertext := aead.Seal(nil, nonce, []byte(plaintext), additionalData(secret, version, keyID))
	return storage.SecretVersionRow{SecretID: secret.ID, Version: version, KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext, Digest: hex.EncodeToString(digest[:]), CreatedAt: now}, nil
}

func (v *Vault) decrypt(secret storage.SecretRow, version storage.SecretVersionRow) ([]byte, error) {
	key := v.keyring.Keys[version.KeyID]
	if len(key) != 32 {
		return nil, ErrKeyUnavailable
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, version.Nonce, version.Ciphertext, additionalData(secret, version.Version, version.KeyID))
	if err != nil {
		return nil, ErrDecrypt
	}
	digest := sha256.Sum256(plaintext)
	if !strings.EqualFold(version.Digest, hex.EncodeToString(digest[:])) {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func additionalData(secret storage.SecretRow, version uint64, keyID string) []byte {
	return []byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", secret.ID, version, keyID, secret.Purpose, secret.ResourceGroupID))
}

func metadataFromRow(row storage.SecretRow) Metadata {
	return Metadata{ID: row.ID, Name: row.Name, Purpose: row.Purpose, OwnerUserID: row.OwnerUserID, ResourceGroupID: row.ResourceGroupID, ActiveVersion: row.ActiveVersion, Fingerprint: row.Fingerprint, CreatedAt: row.CreatedAt, RotatedAt: row.RotatedAt, LastUsedAt: row.LastUsedAt}
}

func fingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:6])
}

func randomID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}

func (v *Vault) audit(ctx context.Context, op OperationContext, action, targetID, eventResult, eventError string, metadata map[string]any) {
	encoded, _ := json.Marshal(Redact(metadata))
	_ = v.store.AppendAuditEvent(ctx, storage.AuditEventRow{
		ID: randomID("audit"), ActorID: op.ActorID, SessionID: op.SessionID, Action: action,
		TargetKind: "secret", TargetID: targetID, ResourceGroupID: op.ResourceGroupID,
		CorrelationID: op.CorrelationID, Result: eventResult, ErrorClass: eventError,
		MetadataJSON: string(encoded), CreatedAt: v.now(),
	})
}

func result(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrDecrypt) {
		return "decrypt_failed"
	}
	if errors.Is(err, ErrInvalidSecret) {
		return "invalid_secret"
	}
	return "operation_failed"
}

// Redact removes common secret-bearing fields before values enter logs, audit,
// errors, or response projections.
func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") || strings.Contains(lower, "authorization") || strings.Contains(lower, "ciphertext") {
				out[key] = "[REDACTED]"
			} else {
				out[key] = Redact(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = Redact(item)
		}
		return out
	default:
		return typed
	}
}
