package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	PluginCapabilityDockerSocketKind = "docker.socket"
	PluginCapabilityDockerSocketID   = "local"
	PluginCapabilityDockerSocketPath = "/var/run/docker.sock"
)

type PluginCapabilityTargetBinding struct {
	Kind            string
	ID              string
	ResourceGroupID string
	Version         string
}

// EnsurePluginCapabilityDockerSocketBinding registers the local execution
// host's Docker endpoint as a core-owned resource. The endpoint path remains
// host-side; every binding lookup also verifies the live Unix socket identity,
// so disappearance or replacement revokes existing handle versions.
func (s *GormStore) EnsurePluginCapabilityDockerSocketBinding(ctx context.Context, resourceGroupID, socketPath string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	resourceGroupID, socketPath = strings.TrimSpace(resourceGroupID), strings.TrimSpace(socketPath)
	if resourceGroupID == "" || socketPath == "" {
		return errors.New("Docker socket resource group and endpoint are required")
	}
	return s.BindResource(ctx, ResourceBindingRow{
		ID:                 "core-docker-socket-local",
		ResourceKind:       PluginCapabilityDockerSocketKind,
		ResourceID:         PluginCapabilityDockerSocketID,
		ResourceGroupID:    resourceGroupID,
		ParentResourceKind: "host.unix-socket",
		ParentResourceID:   socketPath,
		UpdatedAt:          time.Now().UTC(),
	})
}

// PluginCapabilityTargetVersion returns an opaque version of a core-owned
// resource. It never returns secret material. Handle owners use it to detect
// deletion or rotation while an external plugin call is in flight.
func (s *GormStore) PluginCapabilityTargetVersion(ctx context.Context, kind, id string) (string, bool, error) {
	binding, ok, err := s.PluginCapabilityTargetBinding(ctx, kind, id)
	return binding.Version, ok, err
}

// PluginCapabilityTargetBinding resolves both the core-owned resource version
// and its authoritative durable resource group in one repeatable-read view.
func (s *GormStore) PluginCapabilityTargetBinding(ctx context.Context, kind, id string) (PluginCapabilityTargetBinding, bool, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if kind == "" || id == "" {
		return PluginCapabilityTargetBinding{}, false, nil
	}
	var binding PluginCapabilityTargetBinding
	found := false
	err := s.readSnapshotTransaction(ctx, func(snapshot *GormStore) error {
		var err error
		binding, found, err = snapshot.loadPluginCapabilityTargetBinding(ctx, kind, id)
		return err
	})
	return binding, found, err
}

func (s *GormStore) loadPluginCapabilityTargetBinding(ctx context.Context, kind, id string) (PluginCapabilityTargetBinding, bool, error) {
	var value any
	bindingKind := kind
	resourceGroupID := ""
	endpointVersion := ""
	db := s.db.WithContext(ctx)
	switch kind {
	case "secret", "vault.secret":
		row := &SecretRow{}
		value = row
		db = db.Where("id = ?", id)
		bindingKind = "secret"
	case "plugin.instance", "plugin_instance":
		row := &PluginInstanceRow{}
		value = row
		db = db.Where("id = ?", id)
		bindingKind = "plugin_instance"
	case "agent":
		value = &AgentRow{}
		db = db.Where("id = ?", id)
	case "http_rule", "http.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return PluginCapabilityTargetBinding{}, false, nil
		}
		value = &HTTPRuleRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
		bindingKind = "http_rule"
	case "l4", "l4_rule", "l4.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return PluginCapabilityTargetBinding{}, false, nil
		}
		value = &L4RuleRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
		bindingKind = "l4_rule"
	case "relay", "relay_listener", "relay.listener":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return PluginCapabilityTargetBinding{}, false, nil
		}
		value = &RelayListenerRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
		bindingKind = "relay_listener"
	case "certificate", "egress_profile":
		numericID, err := strconv.Atoi(id)
		if err != nil || numericID <= 0 {
			return PluginCapabilityTargetBinding{}, false, nil
		}
		if kind == "certificate" {
			value = &ManagedCertificateRow{}
		} else {
			value = &EgressProfileRow{}
		}
		db = db.Where("id = ?", numericID)
	case PluginCapabilityDockerSocketKind:
		value = &ResourceBindingRow{}
		db = db.Where("resource_kind = ? AND resource_id = ?", PluginCapabilityDockerSocketKind, id)
	default:
		value = &ResourceBindingRow{}
		db = db.Where("resource_kind = ? AND resource_id = ?", kind, id)
	}
	result := db.Limit(1).Find(value)
	if result.Error != nil {
		return PluginCapabilityTargetBinding{}, false, result.Error
	}
	if result.RowsAffected != 1 {
		return PluginCapabilityTargetBinding{}, false, nil
	}
	switch row := value.(type) {
	case *SecretRow:
		resourceGroupID = row.ResourceGroupID
	case *PluginInstanceRow:
		resourceGroupID = row.ResourceGroupID
	case *ResourceBindingRow:
		resourceGroupID = row.ResourceGroupID
		if kind == PluginCapabilityDockerSocketKind {
			if row.ParentResourceKind != "host.unix-socket" {
				return PluginCapabilityTargetBinding{}, false, nil
			}
			var ok bool
			endpointVersion, ok = pluginCapabilityUnixSocketVersion(row.ParentResourceID)
			if !ok {
				return PluginCapabilityTargetBinding{}, false, nil
			}
		}
	default:
		var owner ResourceBindingRow
		ownerResult := s.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", bindingKind, id).Limit(1).Find(&owner)
		if ownerResult.Error != nil {
			return PluginCapabilityTargetBinding{}, false, ownerResult.Error
		}
		if ownerResult.RowsAffected != 1 {
			if agentID, _, ok := splitBoundResourceID(id); ok && (bindingKind == "http_rule" || bindingKind == "l4_rule" || bindingKind == "relay_listener") {
				ownerResult = s.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", "agent", agentID).Limit(1).Find(&owner)
			}
		}
		if ownerResult.RowsAffected != 1 || strings.TrimSpace(owner.ResourceGroupID) == "" {
			return PluginCapabilityTargetBinding{}, false, gorm.ErrRecordNotFound
		}
		resourceGroupID = owner.ResourceGroupID
	}
	if strings.TrimSpace(resourceGroupID) == "" {
		return PluginCapabilityTargetBinding{}, false, gorm.ErrRecordNotFound
	}
	encoded, err := json.Marshal(struct {
		Value           any
		Group           string
		EndpointVersion string
	}{value, resourceGroupID, endpointVersion})
	if err != nil {
		return PluginCapabilityTargetBinding{}, false, err
	}
	digest := sha256.Sum256(encoded)
	return PluginCapabilityTargetBinding{Kind: kind, ID: id, ResourceGroupID: resourceGroupID, Version: hex.EncodeToString(digest[:])}, true, nil
}

func pluginCapabilityUnixSocketVersion(path string) (string, bool) {
	if runtime.GOOS == "windows" || strings.TrimSpace(path) == "" {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", false
	}
	identity := fmt.Sprintf("%s:%d", info.Name(), info.ModTime().UnixNano())
	system := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if system.IsValid() {
		device, inode := system.FieldByName("Dev"), system.FieldByName("Ino")
		if device.IsValid() && inode.IsValid() && device.CanUint() && inode.CanUint() {
			identity = fmt.Sprintf("%d:%d", device.Uint(), inode.Uint())
			for _, fieldName := range []string{"Ctim", "Ctimespec", "Birthtimespec"} {
				field := system.FieldByName(fieldName)
				if field.IsValid() && field.CanInterface() {
					identity += fmt.Sprintf(":%v", field.Interface())
					break
				}
			}
		}
	}
	payload := fmt.Sprintf("%s\x00%s\x00%s", path, info.Mode().String(), identity)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:]), true
}
