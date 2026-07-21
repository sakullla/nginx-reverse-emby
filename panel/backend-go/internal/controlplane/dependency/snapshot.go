package dependency

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type SnapshotRevision struct {
	AgentID  string
	Revision int64
	Snapshot storage.Snapshot
}

func BuildPlan(operationID string, action Action, revisions []SnapshotRevision) (Plan, error) {
	if action != ActionApply && action != ActionDelete {
		return Plan{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, action)
	}
	nodes := make([]Node, 0, len(revisions))
	snapshots := make(map[string]storage.Snapshot, len(revisions))
	listeners := make(map[int]storage.RelayListener)
	egressProfiles := make(map[string]map[int]storage.EgressProfile, len(revisions))

	for _, entry := range revisions {
		agentID := strings.TrimSpace(entry.AgentID)
		if agentID == "" || entry.Revision <= 0 {
			return Plan{}, fmt.Errorf("%w: snapshot revision identity is invalid for agent %q", ErrInvalidPlan, agentID)
		}
		if action == ActionApply && entry.Snapshot.Revision != entry.Revision {
			return Plan{}, fmt.Errorf("%w: apply snapshot revision must match target revision for agent %q", ErrInvalidPlan, agentID)
		}
		if action == ActionDelete && (entry.Snapshot.Revision < 0 || entry.Snapshot.Revision >= entry.Revision) {
			return Plan{}, fmt.Errorf("%w: delete snapshot revision must precede target revision for agent %q", ErrInvalidPlan, agentID)
		}
		if _, exists := snapshots[agentID]; exists {
			return Plan{}, fmt.Errorf("%w: duplicate snapshot for agent %q", ErrInvalidPlan, agentID)
		}
		snapshots[agentID] = entry.Snapshot
		nodes = append(nodes, Node{AgentID: agentID, Revision: entry.Revision})

		egress := make(map[int]storage.EgressProfile, len(entry.Snapshot.EgressProfiles))
		for _, profile := range entry.Snapshot.EgressProfiles {
			egress[profile.ID] = profile
		}
		egressProfiles[agentID] = egress

		for _, listener := range entry.Snapshot.RelayListeners {
			listenerAgentID := strings.TrimSpace(listener.AgentID)
			if listenerAgentID == "" {
				return Plan{}, fmt.Errorf("%w: relay listener %d has no owner in snapshot %q", ErrInvalidPlan, listener.ID, agentID)
			}
			// Consumer snapshots include remote listener copies needed at runtime;
			// the owner's snapshot remains authoritative for dependency validation.
			if listenerAgentID != agentID {
				continue
			}
			if _, exists := listeners[listener.ID]; exists {
				return Plan{}, fmt.Errorf("%w: relay listener id %d is duplicated", ErrInvalidPlan, listener.ID)
			}
			listeners[listener.ID] = listener
		}
	}

	edges := make([]Edge, 0)
	seenEdges := make(map[string]struct{})
	addEdge := func(edge Edge) {
		if edge.FromAgentID == edge.ToAgentID {
			return
		}
		key := edgeKey(edge)
		if _, exists := seenEdges[key]; exists {
			return
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, edge)
	}

	for agentID, snapshot := range snapshots {
		for _, rule := range snapshot.Rules {
			owner := normalizedResourceAgent(agentID, rule.AgentID)
			if owner == "" {
				return Plan{}, fmt.Errorf("%w: HTTP rule %d belongs to another agent", ErrInvalidPlan, rule.ID)
			}
			if err := addRuleEdges(owner, fmt.Sprintf("http_rule:%s:%d", owner, rule.ID), rule.RelayLayers, rule.EgressProfileID, listeners, snapshots, egressProfiles, addEdge); err != nil {
				return Plan{}, err
			}
		}
		for _, rule := range snapshot.L4Rules {
			owner := normalizedResourceAgent(agentID, rule.AgentID)
			if owner == "" {
				return Plan{}, fmt.Errorf("%w: L4 rule %d belongs to another agent", ErrInvalidPlan, rule.ID)
			}
			if err := addRuleEdges(owner, fmt.Sprintf("l4_rule:%s:%d", owner, rule.ID), rule.RelayLayers, rule.EgressProfileID, listeners, snapshots, egressProfiles, addEdge); err != nil {
				return Plan{}, err
			}
		}
	}
	return NewPlan(operationID, action, nodes, edges)
}

func addRuleEdges(
	owner, resource string,
	layers [][]int,
	egressProfileID *int,
	listeners map[int]storage.RelayListener,
	snapshots map[string]storage.Snapshot,
	egressProfiles map[string]map[int]storage.EgressProfile,
	addEdge func(Edge),
) error {
	previous := []string{owner}
	for layerIndex, layer := range layers {
		currentSet := make(map[string]struct{})
		for _, listenerID := range layer {
			listener, ok := listeners[listenerID]
			if !ok || !listener.Enabled {
				return fmt.Errorf("%w: %s references unavailable relay listener %d", ErrMissingDependency, resource, listenerID)
			}
			dependencyAgent := strings.TrimSpace(listener.AgentID)
			if _, ok := snapshots[dependencyAgent]; !ok {
				return fmt.Errorf("%w: %s relay listener %d belongs to absent agent %q", ErrMissingDependency, resource, listenerID, dependencyAgent)
			}
			currentSet[dependencyAgent] = struct{}{}
		}
		if len(currentSet) == 0 {
			continue
		}
		current := sortedSet(currentSet)
		for _, from := range previous {
			for _, to := range current {
				addEdge(Edge{
					FromAgentID: from, ToAgentID: to, Kind: EdgeKindRelayLayer,
					Resource: fmt.Sprintf("%s:layer:%d", resource, layerIndex),
				})
			}
		}
		previous = current
	}
	if egressProfileID == nil {
		return nil
	}
	for _, executorAgent := range previous {
		profile, ok := egressProfiles[executorAgent][*egressProfileID]
		if !ok || !profile.Enabled {
			return fmt.Errorf("%w: %s egress profile %d is unavailable on executor agent %q", ErrMissingDependency, resource, *egressProfileID, executorAgent)
		}
		addEdge(Edge{
			FromAgentID: owner, ToAgentID: executorAgent, Kind: EdgeKindEgressExecutor,
			Resource: fmt.Sprintf("%s:egress:%d", resource, *egressProfileID),
		})
	}
	return nil
}

func normalizedResourceAgent(snapshotAgentID, resourceAgentID string) string {
	resourceAgentID = strings.TrimSpace(resourceAgentID)
	if resourceAgentID == "" {
		return snapshotAgentID
	}
	if resourceAgentID != snapshotAgentID {
		return ""
	}
	return resourceAgentID
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
