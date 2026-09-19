package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"os"
	"path/filepath"
)

func generationFenceKey(instance, generation string) string { return instance + "/" + generation }

// SetRevocationPath loads the durable restart fence before any plugin can start.
func (h *Host) SetRevocationPath(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.instances) != 0 {
		return errors.New("revocation store must be configured before launch")
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fences := map[string]model.PluginGenerationRevokeRequest{}
	if len(raw) > 16<<20 {
		return errors.New("revocation store exceeds bound")
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &fences); err != nil {
			return err
		}
	}
	for key, request := range fences {
		if request.Validate() != nil || key != generationFenceKey(request.InstanceID, request.GenerationID) {
			return errors.New("invalid persisted generation fence")
		}
	}
	h.revoked = fences
	h.revocationPath = path
	return nil
}
func (h *Host) checkGenerationRevoked(candidate HostCandidate) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, exists := h.revoked[generationFenceKey(candidate.InstanceID, candidate.Generation)]; exists {
		return errors.New("plugin generation is revoked")
	}
	return nil
}

// RevokeGeneration fences launch before joining every active, prepared and draining instance.
func (h *Host) RevokeGeneration(ctx context.Context, request model.PluginGenerationRevokeRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	lock := h.instanceLock(request.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	h.mu.Lock()
	key := generationFenceKey(request.InstanceID, request.GenerationID)
	if previous, ok := h.revoked[key]; ok && (previous.PluginID != request.PluginID || previous.ProviderGenerationID != request.ProviderGenerationID || previous.Revision != request.Revision) {
		h.mu.Unlock()
		return errors.New("generation fence identity mismatch")
	}
	var instances []*HostedInstance
	for instance := range h.instances {
		candidate := instance.candidate
		if candidate.InstanceID != request.InstanceID || candidate.Generation != request.GenerationID {
			continue
		}
		if candidate.PluginID != request.PluginID || candidate.ProviderGenerationID != request.ProviderGenerationID || candidate.Revision != request.Revision {
			h.mu.Unlock()
			return errors.New("generation revoke authority mismatch")
		}
		instances = append(instances, instance)
	}
	// A missing durable store cannot acknowledge a restart-safe revocation.
	if h.revocationPath == "" {
		h.mu.Unlock()
		return errors.New("generation revocation store unavailable")
	}
	previous, existed := h.revoked[key]
	h.revoked[key] = request
	raw, err := json.Marshal(h.revoked)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(h.revocationPath), 0o700)
	}
	if err == nil {
		var file *os.File
		file, err = os.CreateTemp(filepath.Dir(h.revocationPath), ".revoke-*")
		if err == nil {
			temp := file.Name()
			err = file.Chmod(0o600)
			if err == nil {
				_, err = file.Write(raw)
			}
			if err == nil {
				err = file.Sync()
			}
			err = errors.Join(err, file.Close())
			if err == nil {
				err = os.Rename(temp, h.revocationPath)
			}
			_ = os.Remove(temp)
		}
	}
	if err != nil {
		if existed {
			h.revoked[key] = previous
		} else {
			delete(h.revoked, key)
		}
		h.mu.Unlock()
		return err
	}
	h.mu.Unlock()
	var result error
	for _, instance := range instances {
		instance.mu.RLock()
		attempt := instance.attempt
		instance.mu.RUnlock()
		if attempt != nil && attempt.network != nil {
			_ = attempt.network.Close()
		}
		stopErr := instance.stop(ctx)
		if !instance.terminated() {
			result = errors.Join(result, stopErr, errors.New("revoked generation has not terminated"))
			continue
		}
		h.mu.Lock()
		delete(h.instances, instance)
		delete(h.pending, instance)
		if h.active[request.InstanceID] == instance {
			delete(h.active, request.InstanceID)
		}
		h.mu.Unlock()
	}
	return result
}
