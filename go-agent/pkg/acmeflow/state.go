package acmeflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	accountsDirectory      = "accounts"
	accountKeyFile         = "key.pem"
	accountMetadataFile    = "metadata.json"
	challengesDirectory    = "challenges"
	ChallengeIntentVersion = 1
)

var ErrChallengeIntentNotFound = errors.New("acmeflow: challenge intent not found")

type stateStoreConfig struct {
	clock  func() time.Time
	inject PersistenceFaultInjector
}

// StateStoreOption configures deterministic clock and fault boundaries without
// changing the owner-neutral persisted schema.
type StateStoreOption func(*stateStoreConfig)

func WithStateClock(clock func() time.Time) StateStoreOption {
	return func(config *stateStoreConfig) {
		if clock != nil {
			config.clock = clock
		}
	}
}

func WithPersistenceFaultInjector(injector PersistenceFaultInjector) StateStoreOption {
	return func(config *stateStoreConfig) {
		config.inject = injector
	}
}

// StateStore is the shared owner-neutral durable state implementation. It
// stores ACME account keys separately from versioned metadata, certificate
// generations, and credential-free challenge recovery intents.
type StateStore struct {
	mu    sync.Mutex
	fs    *durableFilesystem
	clock func() time.Time
}

func OpenStateStore(root string, options ...StateStoreOption) (*StateStore, error) {
	config := stateStoreConfig{clock: time.Now}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	filesystem, err := openDurableFilesystem(root, config.inject)
	if err != nil {
		return nil, WrapError(CategoryProtocol, "state_open", err)
	}
	store := &StateStore{fs: filesystem, clock: config.clock}
	for _, directory := range []string{
		accountsDirectory,
		challengesDirectory,
		stagingDirectory,
		generationsDirectory,
		currentDirectory,
		pendingDirectory,
	} {
		if err := filesystem.ensureDirectory(directory); err != nil {
			_ = filesystem.close()
			return nil, WrapError(CategoryProtocol, "state_open", err)
		}
	}
	return store, nil
}

func (store *StateStore) Close() error {
	if store == nil || store.fs == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.fs.close()
}

func (store *StateStore) LoadAccount(ctx context.Context, lookup AccountLookup) (AccountRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	var record AccountRecord
	lookup, err := normalizeAccountLookup(lookup)
	if err != nil {
		return record, WrapError(CategoryAccount, "account_load", err)
	}
	if err := contextError(ctx); err != nil {
		return record, normalizeError("account_load", err)
	}
	directory := statePath(accountsDirectory, accountDirectoryName(lookup))
	keyPEM, exists, err := store.fs.readOptionalRegularFile(statePath(directory, accountKeyFile), maxStateFileSize)
	if err != nil {
		return record, WrapError(CategoryAccount, "account_load", err)
	}
	if !exists {
		return record, ErrAccountNotFound
	}
	if _, err := parsePrivateKeyPEM(keyPEM); err != nil {
		return record, WrapError(CategoryAccount, "account_load", err)
	}
	record.KeyPEM = append([]byte(nil), keyPEM...)
	metadataJSON, exists, err := store.fs.readOptionalRegularFile(statePath(directory, accountMetadataFile), 1<<20)
	if err != nil {
		return AccountRecord{}, WrapError(CategoryAccount, "account_load", err)
	}
	if !exists {
		return record, nil
	}
	var metadata AccountMetadata
	if err := decodeStateJSON(metadataJSON, &metadata); err != nil {
		return AccountRecord{}, WrapError(CategoryAccount, "account_load", err)
	}
	metadata, err = normalizeAccountMetadata(metadata)
	if err != nil {
		return AccountRecord{}, WrapError(CategoryAccount, "account_load", err)
	}
	if metadata.DirectoryURL != lookup.DirectoryURL || metadata.Email != lookup.Email {
		return AccountRecord{}, WrapError(CategoryAccount, "account_load", errors.New("account metadata identity mismatch"))
	}
	record.Metadata = cloneAccountMetadata(metadata)
	return record, nil
}

func (store *StateStore) SaveAccountKey(ctx context.Context, lookup AccountLookup, keyPEM []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	lookup, err := normalizeAccountLookup(lookup)
	if err != nil {
		return WrapError(CategoryAccount, "account_key_save", err)
	}
	if err := contextError(ctx); err != nil {
		return normalizeError("account_key_save", err)
	}
	if _, err := parsePrivateKeyPEM(keyPEM); err != nil {
		return WrapError(CategoryAccount, "account_key_save", err)
	}
	directory := statePath(accountsDirectory, accountDirectoryName(lookup))
	if err := store.fs.ensureDirectory(directory); err != nil {
		return WrapError(CategoryAccount, "account_key_save", err)
	}
	name := statePath(directory, accountKeyFile)
	existing, exists, err := store.fs.readOptionalRegularFile(name, maxStateFileSize)
	if err != nil {
		return WrapError(CategoryAccount, "account_key_save", err)
	}
	if exists {
		if bytes.Equal(existing, keyPEM) {
			return nil
		}
		return WrapError(CategoryAccount, "account_key_save", errors.New("account key is immutable"))
	}
	if err := store.fs.writeFileAtomic(name, append([]byte(nil), keyPEM...), "account.key"); err != nil {
		return WrapError(CategoryAccount, "account_key_save", err)
	}
	return nil
}

func (store *StateStore) SaveAccountMetadata(ctx context.Context, metadata AccountMetadata) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	metadata, err := normalizeAccountMetadata(metadata)
	if err != nil {
		return WrapError(CategoryAccount, "account_metadata_save", err)
	}
	if err := contextError(ctx); err != nil {
		return normalizeError("account_metadata_save", err)
	}
	lookup := AccountLookup{DirectoryURL: metadata.DirectoryURL, Email: metadata.Email}
	directory := statePath(accountsDirectory, accountDirectoryName(lookup))
	keyPEM, exists, err := store.fs.readOptionalRegularFile(statePath(directory, accountKeyFile), maxStateFileSize)
	if err != nil {
		return WrapError(CategoryAccount, "account_metadata_save", err)
	}
	if !exists {
		return WrapError(CategoryAccount, "account_metadata_save", errors.New("account key is missing"))
	}
	if _, err := parsePrivateKeyPEM(keyPEM); err != nil {
		return WrapError(CategoryAccount, "account_metadata_save", err)
	}
	data, err := encodeStateJSON(metadata)
	if err != nil {
		return WrapError(CategoryAccount, "account_metadata_save", err)
	}
	if err := store.fs.writeFileAtomic(statePath(directory, accountMetadataFile), data, "account.metadata"); err != nil {
		return WrapError(CategoryAccount, "account_metadata_save", err)
	}
	return nil
}

type ChallengeIntentStatus string

const (
	ChallengeIntentPending   ChallengeIntentStatus = "pending"
	ChallengeIntentCompleted ChallengeIntentStatus = "completed"
)

// ChallengeIntent contains only the minimum credential-free information
// needed to clean up the exact DNS record after a crash.
type ChallengeIntent struct {
	Version   int                   `json:"version"`
	ID        string                `json:"id"`
	Status    ChallengeIntentStatus `json:"status"`
	Zone      string                `json:"zone"`
	FQDN      string                `json:"fqdn"`
	ValueHash string                `json:"value_hash"`
	RecordID  string                `json:"record_id,omitempty"`
}

func HashChallengeValue(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func NewChallengeIntent(zone, fqdn, value string) (ChallengeIntent, error) {
	if value == "" || len(value) > 4096 {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent", errors.New("challenge value is invalid"))
	}
	zone, err := normalizeStateDNSName(zone)
	if err != nil {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent", err)
	}
	fqdn, err = normalizeStateDNSName(fqdn)
	if err != nil {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent", err)
	}
	if fqdn != zone && !strings.HasSuffix(fqdn, "."+zone) {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent", errors.New("challenge name is outside the zone"))
	}
	valueHash := HashChallengeValue(value)
	intent := ChallengeIntent{
		Version:   ChallengeIntentVersion,
		Status:    ChallengeIntentPending,
		Zone:      zone,
		FQDN:      fqdn,
		ValueHash: valueHash,
	}
	intent.ID = challengeIntentID(intent)
	return intent, nil
}

func (store *StateStore) SaveChallengeIntent(ctx context.Context, intent ChallengeIntent) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saveChallengeIntentLocked(ctx, intent, true)
}

func (store *StateStore) SetChallengeRecordID(ctx context.Context, id, recordID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	intent, err := store.loadChallengeIntentLocked(ctx, id)
	if err != nil {
		return err
	}
	if intent.Status != ChallengeIntentPending {
		return WrapError(CategoryChallenge, "challenge_intent_update", errors.New("challenge intent is complete"))
	}
	recordID = strings.TrimSpace(recordID)
	if recordID == "" || len(recordID) > 1024 {
		return WrapError(CategoryChallenge, "challenge_intent_update", errors.New("challenge record identifier is invalid"))
	}
	if intent.RecordID != "" && intent.RecordID != recordID {
		return WrapError(CategoryChallenge, "challenge_intent_update", errors.New("challenge record identifier is immutable"))
	}
	intent.RecordID = recordID
	return store.saveChallengeIntentLocked(ctx, intent, false)
}

func (store *StateStore) CompleteChallengeIntent(ctx context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	intent, err := store.loadChallengeIntentLocked(ctx, id)
	if err != nil {
		return err
	}
	if intent.Status == ChallengeIntentCompleted {
		return nil
	}
	intent.Status = ChallengeIntentCompleted
	return store.saveChallengeIntentLocked(ctx, intent, false)
}

func (store *StateStore) ListChallengeIntents(ctx context.Context) ([]ChallengeIntent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.listChallengeIntentsLocked(ctx)
}

func (store *StateStore) saveChallengeIntentLocked(ctx context.Context, intent ChallengeIntent, create bool) error {
	if err := contextError(ctx); err != nil {
		return normalizeError("challenge_intent_save", err)
	}
	intent, err := normalizeChallengeIntent(intent)
	if err != nil {
		return WrapError(CategoryChallenge, "challenge_intent_save", err)
	}
	name := statePath(challengesDirectory, intent.ID+".json")
	if create {
		existing, exists, err := store.fs.readOptionalRegularFile(name, 1<<20)
		if err != nil {
			return WrapError(CategoryChallenge, "challenge_intent_save", err)
		}
		if exists {
			var persisted ChallengeIntent
			if err := decodeStateJSON(existing, &persisted); err != nil {
				return WrapError(CategoryChallenge, "challenge_intent_save", err)
			}
			persisted, err = normalizeChallengeIntent(persisted)
			if err != nil || persisted != intent {
				return WrapError(CategoryChallenge, "challenge_intent_save", errors.New("challenge intent conflicts with persisted state"))
			}
			return nil
		}
	}
	data, err := encodeStateJSON(intent)
	if err != nil {
		return WrapError(CategoryChallenge, "challenge_intent_save", err)
	}
	if err := store.fs.writeFileAtomic(name, data, "challenge.intent"); err != nil {
		return WrapError(CategoryChallenge, "challenge_intent_save", err)
	}
	return nil
}

func (store *StateStore) loadChallengeIntentLocked(ctx context.Context, id string) (ChallengeIntent, error) {
	if err := contextError(ctx); err != nil {
		return ChallengeIntent{}, normalizeError("challenge_intent_load", err)
	}
	if !isLowerHex(id, sha256.Size*2) {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent_load", errors.New("challenge intent identifier is invalid"))
	}
	data, exists, err := store.fs.readOptionalRegularFile(statePath(challengesDirectory, id+".json"), 1<<20)
	if err != nil {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent_load", err)
	}
	if !exists {
		return ChallengeIntent{}, ErrChallengeIntentNotFound
	}
	var intent ChallengeIntent
	if err := decodeStateJSON(data, &intent); err != nil {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent_load", err)
	}
	intent, err = normalizeChallengeIntent(intent)
	if err != nil || intent.ID != id {
		return ChallengeIntent{}, WrapError(CategoryChallenge, "challenge_intent_load", errors.New("challenge intent is invalid"))
	}
	return intent, nil
}

func (store *StateStore) listChallengeIntentsLocked(ctx context.Context) ([]ChallengeIntent, error) {
	if err := contextError(ctx); err != nil {
		return nil, normalizeError("challenge_intent_list", err)
	}
	entries, err := store.fs.readDirectory(challengesDirectory)
	if err != nil {
		return nil, WrapError(CategoryChallenge, "challenge_intent_list", err)
	}
	intents := make([]ChallengeIntent, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, temporaryPrefix) {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			return nil, WrapError(CategoryChallenge, "challenge_intent_list", errors.New("challenge state directory contains an unexpected entry"))
		}
		id := strings.TrimSuffix(name, ".json")
		intent, err := store.loadChallengeIntentLocked(ctx, id)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].ID < intents[j].ID })
	return intents, nil
}

func normalizeAccountLookup(lookup AccountLookup) (AccountLookup, error) {
	lookup.DirectoryURL = strings.TrimSpace(lookup.DirectoryURL)
	lookup.Email = strings.TrimSpace(lookup.Email)
	if err := validateStateURL(lookup.DirectoryURL, true); err != nil {
		return AccountLookup{}, err
	}
	if len(lookup.Email) > 320 || strings.ContainsAny(lookup.Email, "\r\n\x00") {
		return AccountLookup{}, errors.New("account email is invalid")
	}
	return lookup, nil
}

func normalizeAccountMetadata(metadata AccountMetadata) (AccountMetadata, error) {
	if metadata.Version != AccountMetadataVersion {
		return AccountMetadata{}, errors.New("unsupported account metadata version")
	}
	lookup, err := normalizeAccountLookup(AccountLookup{DirectoryURL: metadata.DirectoryURL, Email: metadata.Email})
	if err != nil {
		return AccountMetadata{}, err
	}
	metadata.DirectoryURL = lookup.DirectoryURL
	metadata.Email = lookup.Email
	metadata.URI = strings.TrimSpace(metadata.URI)
	if err := validateStateURL(metadata.URI, true); err != nil {
		return AccountMetadata{}, errors.New("account URI is invalid")
	}
	if len(metadata.Contact) > 32 {
		return AccountMetadata{}, errors.New("account contact list is too large")
	}
	contacts := make([]string, 0, len(metadata.Contact))
	for _, contact := range metadata.Contact {
		contact = strings.TrimSpace(contact)
		parsed, err := url.Parse(contact)
		if err != nil || parsed.Scheme == "" || parsed.User != nil || len(contact) > 1024 || strings.ContainsAny(contact, "\r\n\x00") {
			return AccountMetadata{}, errors.New("account contact is invalid")
		}
		contacts = append(contacts, contact)
	}
	metadata.Contact = contacts
	return metadata, nil
}

func validateStateURL(value string, requireHTTP bool) error {
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("state URL is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("state URL is invalid")
	}
	if requireHTTP && (parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "") {
		return errors.New("state URL is invalid")
	}
	return nil
}

func accountDirectoryName(lookup AccountLookup) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(lookup.DirectoryURL) + "\x00" + strings.TrimSpace(lookup.Email)))
	return hex.EncodeToString(hash[:])
}

func normalizeChallengeIntent(intent ChallengeIntent) (ChallengeIntent, error) {
	if intent.Version != ChallengeIntentVersion {
		return ChallengeIntent{}, errors.New("unsupported challenge intent version")
	}
	if intent.Status != ChallengeIntentPending && intent.Status != ChallengeIntentCompleted {
		return ChallengeIntent{}, errors.New("challenge intent status is invalid")
	}
	zone, err := normalizeStateDNSName(intent.Zone)
	if err != nil {
		return ChallengeIntent{}, err
	}
	fqdn, err := normalizeStateDNSName(intent.FQDN)
	if err != nil {
		return ChallengeIntent{}, err
	}
	if fqdn != zone && !strings.HasSuffix(fqdn, "."+zone) {
		return ChallengeIntent{}, errors.New("challenge name is outside the zone")
	}
	if !isLowerHex(intent.ValueHash, sha256.Size*2) {
		return ChallengeIntent{}, errors.New("challenge value hash is invalid")
	}
	if len(intent.RecordID) > 1024 || strings.ContainsAny(intent.RecordID, "\r\n\x00") {
		return ChallengeIntent{}, errors.New("challenge record identifier is invalid")
	}
	intent.Zone = zone
	intent.FQDN = fqdn
	if intent.ID != challengeIntentID(intent) {
		return ChallengeIntent{}, errors.New("challenge intent identifier is invalid")
	}
	return intent, nil
}

func normalizeStateDNSName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 || strings.ContainsAny(value, " /\\\t\r\n\x00*") {
		return "", errors.New("DNS recovery name is invalid")
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("DNS recovery name is invalid")
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
				continue
			}
			return "", errors.New("DNS recovery name is invalid")
		}
	}
	return value, nil
}

func challengeIntentID(intent ChallengeIntent) string {
	hash := sha256.Sum256([]byte(intent.Zone + "\x00" + intent.FQDN + "\x00" + intent.ValueHash))
	return hex.EncodeToString(hash[:])
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func encodeStateJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("state JSON could not be encoded")
	}
	return append(data, '\n'), nil
}

func decodeStateJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("state JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("state JSON contains trailing data")
	} else if !errors.Is(err, io.EOF) {
		return errors.New("state JSON is invalid")
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	return ctx.Err()
}
