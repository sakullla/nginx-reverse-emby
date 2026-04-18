package service

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type configIdentityAllocatorState struct {
	LocalAgentID   string
	Agents         []storage.AgentRow
	LocalState     storage.LocalAgentStateRow
	HTTPRules      []storage.HTTPRuleRow
	L4Rules        []storage.L4RuleRow
	RelayListeners []storage.RelayListenerRow
	Certificates   []storage.ManagedCertificateRow
}

type configIdentityAllocator struct {
	localAgentID        string
	usedRuleIDs         map[int]struct{}
	usedListenerIDs     map[int]struct{}
	usedCertificateIDs  map[int]struct{}
	nextRevisionByAgent map[string]int
}

type configIdentityAllocatorStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
}

func newConfigIdentityAllocator(state configIdentityAllocatorState) *configIdentityAllocator {
	allocator := &configIdentityAllocator{
		localAgentID:        strings.TrimSpace(state.LocalAgentID),
		usedRuleIDs:         map[int]struct{}{},
		usedListenerIDs:     map[int]struct{}{},
		usedCertificateIDs:  map[int]struct{}{},
		nextRevisionByAgent: map[string]int{},
	}
	allocator.seedIDs(state)
	allocator.seedRevisionFloors(state)
	return allocator
}

func newConfigIdentityAllocatorFromStore(ctx context.Context, cfg config.Config, store configIdentityAllocatorStore) (*configIdentityAllocator, error) {
	agentRows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	localState, err := store.LoadLocalAgentState(ctx)
	if err != nil {
		return nil, err
	}
	agentIDs, err := allKnownAgentIDs(ctx, cfg, store)
	if err != nil {
		return nil, err
	}
	httpRows, err := listAllHTTPRuleRows(ctx, store, agentIDs)
	if err != nil {
		return nil, err
	}
	l4Rows, err := listAllL4RuleRows(ctx, store, agentIDs)
	if err != nil {
		return nil, err
	}
	relayRows, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	certRows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}

	return newConfigIdentityAllocator(configIdentityAllocatorState{
		LocalAgentID:   cfg.LocalAgentID,
		Agents:         agentRows,
		LocalState:     localState,
		HTTPRules:      httpRows,
		L4Rules:        l4Rows,
		RelayListeners: relayRows,
		Certificates:   certRows,
	}), nil
}

func (a *configIdentityAllocator) AllocateRuleID(preferredID int) int {
	return allocatePreferredID(a.usedRuleIDs, preferredID)
}

func (a *configIdentityAllocator) AllocateListenerID(preferredID int) int {
	return allocatePreferredID(a.usedListenerIDs, preferredID)
}

func (a *configIdentityAllocator) AllocateCertificateID(preferredID int) int {
	return allocatePreferredID(a.usedCertificateIDs, preferredID)
}

func (a *configIdentityAllocator) AllocateRevisionForAgent(agentID string, maxExistingRevision int) int {
	agentID = strings.TrimSpace(agentID)
	next := maxExistingRevision + 1
	if floor := a.nextRevisionByAgent[agentID]; floor > next {
		next = floor
	}
	a.nextRevisionByAgent[agentID] = next + 1
	return next
}

func (a *configIdentityAllocator) AllocateRevisionForTargets(agentIDs []string, maxExistingRevision int) int {
	next := maxExistingRevision + 1
	seen := map[string]struct{}{}
	for _, raw := range agentIDs {
		agentID := strings.TrimSpace(raw)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		if floor := a.nextRevisionByAgent[agentID]; floor > next {
			next = floor
		}
	}
	for agentID := range seen {
		if a.nextRevisionByAgent[agentID] < next+1 {
			a.nextRevisionByAgent[agentID] = next + 1
		}
	}
	return next
}

func (a *configIdentityAllocator) seedIDs(state configIdentityAllocatorState) {
	for _, row := range state.HTTPRules {
		if row.ID > 0 {
			a.usedRuleIDs[row.ID] = struct{}{}
		}
	}
	for _, row := range state.L4Rules {
		if row.ID > 0 {
			a.usedRuleIDs[row.ID] = struct{}{}
		}
	}
	for _, row := range state.RelayListeners {
		if row.ID > 0 {
			a.usedListenerIDs[row.ID] = struct{}{}
		}
	}
	for _, row := range state.Certificates {
		if row.ID > 0 {
			a.usedCertificateIDs[row.ID] = struct{}{}
		}
	}
}

func (a *configIdentityAllocator) seedRevisionFloors(state configIdentityAllocatorState) {
	for _, row := range state.Agents {
		agentID := strings.TrimSpace(row.ID)
		if agentID == "" {
			continue
		}
		floor := maxInt(
			row.DesiredRevision,
			row.CurrentRevision,
			highestAgentRuleRevision(agentID, state.HTTPRules, state.L4Rules),
			highestAgentRelayRevision(agentID, state.RelayListeners),
			highestTargetCertificateRevision(agentID, state.Certificates),
		)
		a.nextRevisionByAgent[agentID] = floor + 1
	}
	if a.localAgentID != "" {
		floor := maxInt(
			state.LocalState.DesiredRevision,
			state.LocalState.CurrentRevision,
			highestAgentRuleRevision(a.localAgentID, state.HTTPRules, state.L4Rules),
			highestAgentRelayRevision(a.localAgentID, state.RelayListeners),
			highestTargetCertificateRevision(a.localAgentID, state.Certificates),
		)
		a.nextRevisionByAgent[a.localAgentID] = floor + 1
	}
}

func allocatePreferredID(used map[int]struct{}, preferredID int) int {
	if preferredID > 0 {
		if _, exists := used[preferredID]; !exists {
			used[preferredID] = struct{}{}
			return preferredID
		}
	}

	next := 1
	for id := range used {
		if id >= next {
			next = id + 1
		}
	}
	used[next] = struct{}{}
	return next
}

func preferredInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func listAllHTTPRuleRows(ctx context.Context, store configIdentityAllocatorStore, agentIDs []string) ([]storage.HTTPRuleRow, error) {
	rows := make([]storage.HTTPRuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func listAllL4RuleRows(ctx context.Context, store configIdentityAllocatorStore, agentIDs []string) ([]storage.L4RuleRow, error) {
	rows := make([]storage.L4RuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func highestAgentRuleRevision(agentID string, httpRows []storage.HTTPRuleRow, l4Rows []storage.L4RuleRow) int {
	maxRevision := 0
	for _, row := range httpRows {
		if strings.TrimSpace(row.AgentID) == agentID && row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	for _, row := range l4Rows {
		if strings.TrimSpace(row.AgentID) == agentID && row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision
}

func highestAgentRelayRevision(agentID string, rows []storage.RelayListenerRow) int {
	maxRevision := 0
	for _, row := range rows {
		if strings.TrimSpace(row.AgentID) == agentID && row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision
}

func highestTargetCertificateRevision(agentID string, rows []storage.ManagedCertificateRow) int {
	maxRevision := 0
	for _, row := range rows {
		targets := parseStringArray(row.TargetAgentIDs)
		if containsString(targets, agentID) && row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision
}
