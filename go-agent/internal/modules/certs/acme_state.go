package certs

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

const managedCertificateStateFileName = "managed_state.json"

type managedCertificateState struct {
	LocalMetadata localMaterialMetadata              `json:"local_metadata,omitempty"`
	ACME          *model.ManagedCertificateACMEState `json:"acme,omitempty"`
}

func (m *Manager) loadManagedCertificateState(certificateID int) (managedCertificateState, bool, error) {
	payload, err := os.ReadFile(m.managedCertificateStatePath(certificateID))
	if err != nil {
		if os.IsNotExist(err) {
			return managedCertificateState{}, false, nil
		}
		return managedCertificateState{}, false, err
	}

	var state managedCertificateState
	if err := json.Unmarshal(payload, &state); err != nil {
		return managedCertificateState{}, false, nil
	}
	return state, true, nil
}

func (m *Manager) saveManagedCertificateState(certificateID int, state managedCertificateState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomically(m.managedCertificateStatePath(certificateID), payload, 0600)
}

func (m *Manager) managedCertificateStatePath(certificateID int) string {
	return filepath.Join(m.materialDir(certificateID), managedCertificateStateFileName)
}

func (m *Manager) acmeStateRoot(certificateID int) string {
	return filepath.Join(m.materialDir(certificateID), "acmeflow")
}

func (m *Manager) acmeAccountLookup() acmeflow.AccountLookup {
	return acmeflow.AccountLookup{
		DirectoryURL: strings.TrimSpace(m.cfg.acme.directoryURL),
		Email:        strings.TrimSpace(m.cfg.acme.email),
	}
}

type agentACMEStateStore struct {
	*acmeflow.StateStore
	manager       *Manager
	certificateID int
}

func (store *agentACMEStateStore) SaveAccountKey(ctx context.Context, lookup acmeflow.AccountLookup, keyPEM []byte) error {
	if err := store.StateStore.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
		return err
	}
	return store.manager.savePersistedACMEAccountState(store.certificateID, acmeIssueResult{AccountKeyPEM: keyPEM})
}

func (store *agentACMEStateStore) SaveAccountMetadata(ctx context.Context, metadata acmeflow.AccountMetadata) error {
	if err := store.StateStore.SaveAccountMetadata(ctx, metadata); err != nil {
		return err
	}
	return store.manager.savePersistedACMEAccountState(store.certificateID, acmeIssueResult{Account: metadata})
}

func metadataFromLegacyRegistration(payload []byte, lookup acmeflow.AccountLookup) (acmeflow.AccountMetadata, bool) {
	var legacy struct {
		URI  string `json:"uri"`
		Body struct {
			Contact []string `json:"contact"`
		} `json:"body"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &legacy) != nil {
		return acmeflow.AccountMetadata{}, false
	}
	uri := strings.TrimSpace(legacy.URI)
	parsed, err := url.Parse(uri)
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return acmeflow.AccountMetadata{}, false
	}
	return acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: strings.TrimSpace(lookup.DirectoryURL),
		Email:        strings.TrimSpace(lookup.Email),
		URI:          uri,
		Contact:      append([]string(nil), legacy.Body.Contact...),
	}, true
}

func accountMetadataFromModel(metadata model.ManagedCertificateACMEAccountMetadata) acmeflow.AccountMetadata {
	return acmeflow.AccountMetadata{
		Version:      metadata.Version,
		DirectoryURL: metadata.DirectoryURL,
		Email:        metadata.Email,
		URI:          metadata.URI,
		Contact:      append([]string(nil), metadata.Contact...),
	}
}

func accountMetadataToModel(metadata acmeflow.AccountMetadata) model.ManagedCertificateACMEAccountMetadata {
	return model.ManagedCertificateACMEAccountMetadata{
		Version:      metadata.Version,
		DirectoryURL: metadata.DirectoryURL,
		Email:        metadata.Email,
		URI:          metadata.URI,
		Contact:      append([]string(nil), metadata.Contact...),
	}
}
