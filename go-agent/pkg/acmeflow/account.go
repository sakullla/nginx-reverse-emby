package acmeflow

import (
	"context"
	"crypto"
	"errors"
	"io"
	"net/http"
	"strings"

	"golang.org/x/crypto/acme"
)

const AccountMetadataVersion = 1

var ErrAccountNotFound = errors.New("acmeflow: account not found")

// AccountLookup is the owner-defined stable identity for an ACME account.
type AccountLookup struct {
	DirectoryURL string
	Email        string
}

// AccountMetadata contains no private key or provider credentials and is safe
// to persist independently from the account key.
type AccountMetadata struct {
	Version      int      `json:"version"`
	DirectoryURL string   `json:"directory_url"`
	Email        string   `json:"email,omitempty"`
	URI          string   `json:"uri"`
	Contact      []string `json:"contact,omitempty"`
}

// AccountRecord combines owner-loaded key bytes with neutral metadata at the
// engine boundary. It must never be included in error strings or logs.
type AccountRecord struct {
	KeyPEM   []byte
	Metadata AccountMetadata
}

// AccountStore persists the key before any registration request, then stores
// metadata separately after GetReg/Register succeeds. This ordering makes a
// crash between registration and metadata persistence recoverable with the
// same key.
type AccountStore interface {
	LoadAccount(context.Context, AccountLookup) (AccountRecord, error)
	SaveAccountKey(context.Context, AccountLookup, []byte) error
	SaveAccountMetadata(context.Context, AccountMetadata) error
}

type preparedAccount struct {
	record AccountRecord
	key    crypto.Signer
	client ProtocolClient
}

func prepareAccount(
	ctx context.Context,
	lookup AccountLookup,
	store AccountStore,
	clientFactory ProtocolClientFactory,
	httpClient *http.Client,
	random io.Reader,
) (preparedAccount, error) {
	var prepared preparedAccount
	lookup.DirectoryURL = strings.TrimSpace(lookup.DirectoryURL)
	lookup.Email = strings.TrimSpace(lookup.Email)
	if lookup.DirectoryURL == "" || store == nil {
		return prepared, WrapError(CategoryAccount, "account_load", errors.New("missing account directory or store"))
	}

	record, err := store.LoadAccount(ctx, lookup)
	switch {
	case err == nil:
	case errors.Is(err, ErrAccountNotFound):
		record = AccountRecord{}
	default:
		return prepared, normalizeError("account_load", err)
	}
	if err := validateLoadedAccount(record, lookup); err != nil {
		return prepared, err
	}

	keyPEM := append([]byte(nil), record.KeyPEM...)
	var key crypto.Signer
	if len(keyPEM) == 0 {
		key, keyPEM, _, err = prepareCertificateKey(nil, random)
		if err != nil {
			return prepared, normalizeError("account_key_generate", err)
		}
		if err := store.SaveAccountKey(ctx, lookup, append([]byte(nil), keyPEM...)); err != nil {
			return prepared, normalizeError("account_key_save", err)
		}
	} else {
		key, err = parsePrivateKeyPEM(keyPEM)
		if err != nil {
			return prepared, normalizeError("account_key_parse", err)
		}
	}

	if clientFactory == nil {
		clientFactory = NewProtocolClient
	}
	client := clientFactory(ClientConfig{
		Key:          key,
		AccountURI:   strings.TrimSpace(record.Metadata.URI),
		DirectoryURL: lookup.DirectoryURL,
		HTTPClient:   httpClient,
	})
	if client == nil {
		return prepared, WrapError(CategoryAccount, "account_client", errors.New("client factory returned nil"))
	}

	account, err := client.GetReg(ctx, strings.TrimSpace(record.Metadata.URI))
	if errors.Is(err, acme.ErrNoAccount) {
		account, err = registerAccount(ctx, client, lookup.Email)
	}
	if err != nil {
		return prepared, normalizeError("account_recover", err)
	}
	if account == nil || strings.TrimSpace(account.URI) == "" {
		return prepared, WrapError(CategoryAccount, "account_recover", errors.New("account URI is empty"))
	}

	metadata := AccountMetadata{
		Version:      AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          strings.TrimSpace(account.URI),
		Contact:      append([]string(nil), account.Contact...),
	}
	if len(metadata.Contact) == 0 && lookup.Email != "" {
		metadata.Contact = []string{"mailto:" + lookup.Email}
	}
	if err := store.SaveAccountMetadata(ctx, cloneAccountMetadata(metadata)); err != nil {
		return prepared, normalizeError("account_metadata_save", err)
	}
	client.SetAccountURI(metadata.URI)

	prepared.record = AccountRecord{KeyPEM: append([]byte(nil), keyPEM...), Metadata: cloneAccountMetadata(metadata)}
	prepared.key = key
	prepared.client = client
	return prepared, nil
}

func registerAccount(ctx context.Context, client ProtocolClient, email string) (*acme.Account, error) {
	account := &acme.Account{}
	if email = strings.TrimSpace(email); email != "" {
		account.Contact = []string{"mailto:" + email}
	}
	registered, err := client.Register(ctx, account, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		return client.GetReg(ctx, "")
	}
	return registered, err
}

func validateLoadedAccount(record AccountRecord, lookup AccountLookup) error {
	metadata := record.Metadata
	if metadata.Version != 0 && metadata.Version != AccountMetadataVersion {
		return WrapError(CategoryAccount, "account_load", errors.New("unsupported account metadata version"))
	}
	if metadata.DirectoryURL != "" && strings.TrimSpace(metadata.DirectoryURL) != lookup.DirectoryURL {
		return WrapError(CategoryAccount, "account_load", errors.New("account directory mismatch"))
	}
	if metadata.Email != "" && strings.TrimSpace(metadata.Email) != lookup.Email {
		return WrapError(CategoryAccount, "account_load", errors.New("account email mismatch"))
	}
	return nil
}

func cloneAccountRecord(record AccountRecord) AccountRecord {
	return AccountRecord{
		KeyPEM:   append([]byte(nil), record.KeyPEM...),
		Metadata: cloneAccountMetadata(record.Metadata),
	}
}

func cloneAccountMetadata(metadata AccountMetadata) AccountMetadata {
	metadata.Contact = append([]string(nil), metadata.Contact...)
	return metadata
}
