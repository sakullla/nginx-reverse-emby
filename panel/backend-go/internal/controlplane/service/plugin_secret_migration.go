package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type legacyPluginSecretMigrationStore interface {
	MigrateLegacyPluginInstanceSecrets(context.Context, uint64, storage.PluginInstanceRow, []storage.PluginSecretWrite) error
}

// MigrateLegacyWriteOnlySecrets is the startup boundary from the pre-handle
// schema. It fails closed before runtime publication when a package schema or
// Vault conversion is unavailable.
func (s *PluginService) MigrateLegacyWriteOnlySecrets(ctx context.Context) error {
	installedRows, err := s.store.ListInstalledPlugins(ctx)
	if err != nil {
		return err
	}
	if len(installedRows) == 0 {
		return nil
	}
	store, ok := s.store.(legacyPluginSecretMigrationStore)
	if !ok || s.secretVault == nil {
		return errors.New("legacy plugin secret migration is unavailable")
	}
	for _, installed := range installedRows {
		instances, err := s.store.ListPluginInstances(ctx, installed.PluginID)
		if err != nil {
			return err
		}
		schemas := make(map[string]map[string]any)
		loadSchema := func(identity, digest string) (map[string]any, error) {
			key := identity + ":" + digest
			if schema := schemas[key]; schema != nil {
				return schema, nil
			}
			row, found, err := s.storedPackage(ctx, identity, digest)
			if err != nil || !found {
				return nil, errors.New("plugin package schema is unavailable for legacy secret migration")
			}
			var schema map[string]any
			if err := json.Unmarshal([]byte(row.ConfigSchemaJSON), &schema); err != nil || schema == nil {
				return nil, errors.New("plugin package schema is invalid for legacy secret migration")
			}
			schemas[key] = schema
			return schema, nil
		}
		activeSchema, err := loadSchema(installed.ActivePackageIdentity, installed.ActivePackageDigest)
		if err != nil {
			return err
		}
		pendingSchema := activeSchema
		if installed.StagedPackageDigest != "" {
			pendingSchema, err = loadSchema(installed.StagedPackageIdentity, installed.StagedPackageDigest)
			if err != nil {
				return err
			}
		}
		rollbackSchema := activeSchema
		if installed.RollbackPackageDigest != "" {
			rollbackSchema, err = loadSchema(installed.RollbackPackageIdentity, installed.RollbackPackageDigest)
			if err != nil {
				return err
			}
		}
		for _, instance := range instances {
			beforeVersion := instance.StateVersion
			var writes []storage.PluginSecretWrite
			for _, slot := range []struct {
				name, config, resourceGroupID string
				handles                       *string
				schema                        map[string]any
			}{
				{name: "active", config: instance.ConfigJSON, resourceGroupID: instance.ResourceGroupID, handles: &instance.SecretHandlesJSON, schema: activeSchema},
				{name: "pending", config: instance.PendingConfigJSON, resourceGroupID: instance.PendingResourceGroupID, handles: &instance.PendingSecretHandlesJSON, schema: pendingSchema},
				{name: "rollback", config: instance.RollbackConfigJSON, resourceGroupID: instance.RollbackResourceGroupID, handles: &instance.RollbackSecretHandlesJSON, schema: rollbackSchema},
			} {
				empty, err := legacyPluginSecretSlotEmpty(slot.config, *slot.handles)
				if err != nil {
					return fmt.Errorf("migrate plugin %s instance %s %s secrets: %w", installed.PluginID, instance.ID, slot.name, err)
				}
				if empty {
					continue
				}
				public, handles, prepared, err := s.migrateLegacyPluginSecretSlot(ctx, instance, slot.name, slot.resourceGroupID, slot.schema, slot.config, *slot.handles)
				if err != nil {
					return fmt.Errorf("migrate plugin %s instance %s %s secrets: %w", installed.PluginID, instance.ID, slot.name, err)
				}
				switch slot.name {
				case "active":
					instance.ConfigJSON = public
				case "pending":
					instance.PendingConfigJSON = public
				case "rollback":
					instance.RollbackConfigJSON = public
				}
				*slot.handles = handles
				writes = append(writes, prepared...)
			}
			if len(writes) == 0 {
				continue
			}
			instance.UpdatedAt = time.Now().UTC()
			if err := store.MigrateLegacyPluginInstanceSecrets(ctx, beforeVersion, instance, writes); err != nil {
				return err
			}
		}
	}
	return nil
}

func legacyPluginSecretSlotEmpty(rawConfig, rawHandles string) (bool, error) {
	if strings.TrimSpace(rawConfig) != "" {
		return false, nil
	}
	var handles []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultJSONArray(rawHandles)), &handles); err != nil {
		return false, ErrPluginReadProjection
	}
	if len(handles) != 0 {
		return false, errors.New("legacy secret handles exist without slot config")
	}
	return true, nil
}

func (s *PluginService) migrateLegacyPluginSecretSlot(ctx context.Context, instance storage.PluginInstanceRow, slot, resourceGroupID string, schema map[string]any, rawConfig, rawHandles string) (string, string, []storage.PluginSecretWrite, error) {
	value, err := pluginConfigValue(json.RawMessage(rawConfig))
	if err != nil {
		return "", "", nil, ErrPluginReadProjection
	}
	public, submitted, err := pluginStripWriteOnly(schema, value, "")
	if err != nil {
		return "", "", nil, err
	}
	var handles []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultJSONArray(rawHandles)), &handles); err != nil {
		return "", "", nil, ErrPluginReadProjection
	}
	resourceGroupID = strings.TrimSpace(resourceGroupID)
	if len(handles) > 0 && (resourceGroupID == "" || s.secretVault == nil) {
		return "", "", nil, errors.New("legacy secret slot resource ownership is unavailable")
	}
	byPointer := make(map[string]storage.PluginInstanceSecretHandle, len(handles))
	for _, handle := range handles {
		if handle.Pointer == "" || handle.ID == "" || !pluginPointerIsWriteOnly(schema, public, handle.Pointer) {
			return "", "", nil, ErrPluginReadProjection
		}
		plaintext, err := s.secretVault.ResolvePluginReference(ctx, secrets.OperationContext{ActorID: "system:plugin-secret-migration", CorrelationID: "plugin-secret-migration:" + instance.ID, ResourceGroupID: resourceGroupID}, handle.ID, handle.Version, handle.Purpose, handle.Digest)
		if err != nil {
			return "", "", nil, err
		}
		clear(plaintext)
		byPointer[handle.Pointer] = handle
	}
	pointers := make([]string, 0, len(submitted))
	for pointer := range submitted {
		pointers = append(pointers, pointer)
	}
	sort.Strings(pointers)
	writes := make([]storage.PluginSecretWrite, 0, len(pointers))
	if len(pointers) > 0 && resourceGroupID == "" {
		return "", "", nil, errors.New("legacy secret slot resource ownership is unavailable")
	}
	for _, pointer := range pointers {
		if _, duplicate := byPointer[pointer]; duplicate {
			return "", "", nil, errors.New("legacy config contains both plaintext and a secret handle")
		}
		raw := submitted[pointer]
		digest := sha256.Sum256(raw)
		pointerDigest := sha256.Sum256([]byte(slot + ":" + pointer))
		op := secrets.OperationContext{ActorID: "system:plugin-secret-migration", CorrelationID: "plugin-secret-migration:" + instance.ID, ResourceGroupID: resourceGroupID}
		prepared, err := s.secretVault.PreparePluginSecret(op, "plugin-config-migration-"+instance.ID+"-"+hex.EncodeToString(pointerDigest[:6]), "plugin-config:"+instance.ID+":"+pointer, string(raw))
		if err != nil {
			return "", "", nil, err
		}
		byPointer[pointer] = storage.PluginInstanceSecretHandle{Pointer: pointer, ID: prepared.Metadata.ID, Version: prepared.Metadata.ActiveVersion, Digest: hex.EncodeToString(digest[:]), Purpose: prepared.Metadata.Purpose}
		writes = append(writes, storage.PluginSecretWrite{Secret: prepared.Secret, Version: prepared.Version, Audit: prepared.Audit})
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return "", "", nil, err
	}
	handles = handles[:0]
	for _, handle := range byPointer {
		handles = append(handles, handle)
	}
	sort.Slice(handles, func(i, j int) bool { return handles[i].Pointer < handles[j].Pointer })
	encodedHandles, err := json.Marshal(handles)
	if err != nil {
		return "", "", nil, err
	}
	return string(encoded), string(encodedHandles), writes, nil
}
