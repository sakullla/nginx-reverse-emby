package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// PluginCapabilityTargetVersion returns an opaque version of a core-owned
// resource. It never returns secret material. Handle owners use it to detect
// deletion or rotation while an external plugin call is in flight.
func (s *GormStore) PluginCapabilityTargetVersion(ctx context.Context, kind, id string) (string, bool, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if kind == "" || id == "" {
		return "", false, nil
	}
	var value any
	db := s.db.WithContext(ctx)
	switch kind {
	case "secret", "vault.secret":
		value = &SecretRow{}
		db = db.Where("id = ?", id)
	case "plugin.instance", "plugin_instance":
		value = &PluginInstanceRow{}
		db = db.Where("id = ?", id)
	case "agent":
		value = &AgentRow{}
		db = db.Where("id = ?", id)
	case "http_rule", "http.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", false, nil
		}
		value = &HTTPRuleRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
	case "l4", "l4_rule", "l4.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", false, nil
		}
		value = &L4RuleRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
	case "relay", "relay_listener", "relay.listener":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", false, nil
		}
		value = &RelayListenerRow{}
		db = db.Where("agent_id = ? AND id = ?", agentID, numericID)
	case "certificate", "egress_profile":
		numericID, err := strconv.Atoi(id)
		if err != nil || numericID <= 0 {
			return "", false, nil
		}
		if kind == "certificate" {
			value = &ManagedCertificateRow{}
		} else {
			value = &EgressProfileRow{}
		}
		db = db.Where("id = ?", numericID)
	default:
		value = &ResourceBindingRow{}
		db = db.Where("resource_kind = ? AND resource_id = ?", kind, id)
	}
	result := db.Limit(1).Find(value)
	if result.Error != nil {
		return "", false, result.Error
	}
	if result.RowsAffected != 1 {
		return "", false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false, err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true, nil
}
