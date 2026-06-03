package certs

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
