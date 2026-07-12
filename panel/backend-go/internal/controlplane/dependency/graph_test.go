package dependency

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestBuildPlanExtractsRelayEgressAndWireGuardDependencies(t *testing.T) {
	plan, err := BuildPlan("operation-1", ActionApply, []SnapshotRevision{
		{
			AgentID: "edge-a", Revision: 7,
			Snapshot: storage.Snapshot{Revision: 7, Rules: []storage.HTTPRule{{
				ID: 1, AgentID: "edge-a",
				RelayLayers: [][]int{{10, 11}, {20}}, EgressProfileID: intPointer(9),
			}}},
		},
		{
			AgentID: "edge-b", Revision: 8,
			Snapshot: storage.Snapshot{Revision: 8, RelayListeners: []storage.RelayListener{{
				ID: 10, AgentID: "edge-b", Enabled: true,
			}}},
		},
		{
			AgentID: "edge-c", Revision: 9,
			Snapshot: storage.Snapshot{Revision: 9, RelayListeners: []storage.RelayListener{{
				ID: 11, AgentID: "edge-c", Enabled: true,
			}}},
		},
		{
			AgentID: "edge-d", Revision: 10,
			Snapshot: storage.Snapshot{
				Revision: 10,
				RelayListeners: []storage.RelayListener{{
					ID: 20, AgentID: "edge-d", Enabled: true,
					TransportMode: "wireguard", WireGuardProfileID: intPointer(5),
				}},
				WireGuardProfiles: []storage.WireGuardProfile{{ID: 5, AgentID: "edge-d", Enabled: true}},
				EgressProfiles:    []storage.EgressProfile{{ID: 9, Enabled: true, Type: "direct"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	wantNodes := []Node{
		{AgentID: "edge-a", Revision: 7},
		{AgentID: "edge-b", Revision: 8},
		{AgentID: "edge-c", Revision: 9},
		{AgentID: "edge-d", Revision: 10},
	}
	if !reflect.DeepEqual(plan.Nodes, wantNodes) {
		t.Fatalf("nodes = %+v, want %+v", plan.Nodes, wantNodes)
	}
	assertEdge(t, plan, "edge-a", "edge-b", EdgeKindRelayLayer)
	assertEdge(t, plan, "edge-a", "edge-c", EdgeKindRelayLayer)
	assertEdge(t, plan, "edge-b", "edge-d", EdgeKindRelayLayer)
	assertEdge(t, plan, "edge-c", "edge-d", EdgeKindRelayLayer)
	assertEdge(t, plan, "edge-a", "edge-d", EdgeKindEgressExecutor)

	payload, err := plan.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	restored, err := ParsePlan(payload)
	if err != nil {
		t.Fatalf("ParsePlan() error = %v", err)
	}
	restoredPayload, err := restored.Marshal()
	if err != nil {
		t.Fatalf("restored Marshal() error = %v", err)
	}
	if string(restoredPayload) != string(payload) {
		t.Fatalf("plan payload is not stable:\n%s\n%s", payload, restoredPayload)
	}
	if restored.Digest() != plan.Digest() {
		t.Fatalf("restored digest = %q, want %q", restored.Digest(), plan.Digest())
	}
}

func TestBuildPlanAcceptsRemoteRelayCopiesInConsumerSnapshots(t *testing.T) {
	profileID := 5
	remoteListener := storage.RelayListener{
		ID: 10, AgentID: "edge-b", Enabled: true,
		TransportMode: "wireguard", WireGuardProfileID: &profileID,
	}
	plan, err := BuildPlan("operation-remote-copy", ActionApply, []SnapshotRevision{
		{
			AgentID: "edge-a", Revision: 1,
			Snapshot: storage.Snapshot{
				Revision:       1,
				Rules:          []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
				RelayListeners: []storage.RelayListener{remoteListener},
			},
		},
		{
			AgentID: "edge-b", Revision: 1,
			Snapshot: storage.Snapshot{
				Revision:          1,
				RelayListeners:    []storage.RelayListener{remoteListener},
				WireGuardProfiles: []storage.WireGuardProfile{{ID: profileID, AgentID: "edge-b", Enabled: true}},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	assertEdge(t, plan, "edge-a", "edge-b", EdgeKindRelayLayer)
}

func TestBuildPlanRejectsInvalidSnapshotDependenciesAndCycles(t *testing.T) {
	testCases := []struct {
		name      string
		revisions []SnapshotRevision
		wantError error
	}{
		{
			name: "missing relay listener",
			revisions: []SnapshotRevision{{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
				Revision: 1, Rules: []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{99}}}},
			}}},
			wantError: ErrMissingDependency,
		},
		{
			name: "remote relay copy without owner snapshot",
			revisions: []SnapshotRevision{{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
				Revision:       1,
				Rules:          []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
				RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}},
			}}},
			wantError: ErrMissingDependency,
		},
		{
			name: "disabled relay listener",
			revisions: []SnapshotRevision{
				{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
					Revision: 1, Rules: []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
				}},
				{AgentID: "edge-b", Revision: 1, Snapshot: storage.Snapshot{
					Revision: 1, RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: false}},
				}},
			},
			wantError: ErrMissingDependency,
		},
		{
			name: "missing egress on final hop",
			revisions: []SnapshotRevision{
				{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{Revision: 1, Rules: []storage.HTTPRule{{
					ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}, EgressProfileID: intPointer(4),
				}}}},
				{AgentID: "edge-b", Revision: 1, Snapshot: storage.Snapshot{Revision: 1, RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}}}},
			},
			wantError: ErrMissingDependency,
		},
		{
			name: "wireguard relay without profile",
			revisions: []SnapshotRevision{{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
				Revision: 1, RelayListeners: []storage.RelayListener{{
					ID: 10, AgentID: "edge-a", Enabled: true, TransportMode: "wireguard", WireGuardProfileID: intPointer(3),
				}},
			}}},
			wantError: ErrMissingDependency,
		},
		{
			name: "cross-agent cycle",
			revisions: []SnapshotRevision{
				{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
					Revision:       1,
					Rules:          []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{20}}}},
					RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-a", Enabled: true}},
				}},
				{AgentID: "edge-b", Revision: 1, Snapshot: storage.Snapshot{
					Revision:       1,
					Rules:          []storage.HTTPRule{{ID: 2, AgentID: "edge-b", RelayLayers: [][]int{{10}}}},
					RelayListeners: []storage.RelayListener{{ID: 20, AgentID: "edge-b", Enabled: true}},
				}},
			},
			wantError: ErrCycle,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildPlan("operation-invalid", ActionApply, tc.revisions)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("BuildPlan() error = %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestBuildPlanValidatesSnapshotRevisionForAction(t *testing.T) {
	prior := []SnapshotRevision{{
		AgentID: "edge-a", Revision: 2, Snapshot: storage.Snapshot{Revision: 1},
	}}
	plan, err := BuildPlan("operation-delete", ActionDelete, prior)
	if err != nil {
		t.Fatalf("BuildPlan(delete) error = %v", err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].Revision != 2 {
		t.Fatalf("delete nodes = %+v, want target revision 2", plan.Nodes)
	}
	if _, err := BuildPlan("operation-apply", ActionApply, prior); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("BuildPlan(apply prior snapshot) error = %v, want %v", err, ErrInvalidPlan)
	}
	current := []SnapshotRevision{{
		AgentID: "edge-a", Revision: 2, Snapshot: storage.Snapshot{Revision: 2},
	}}
	if _, err := BuildPlan("operation-delete", ActionDelete, current); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("BuildPlan(delete current snapshot) error = %v, want %v", err, ErrInvalidPlan)
	}
}

func TestNewPlanRejectsUnsupportedEdgeKind(t *testing.T) {
	_, err := NewPlan("operation-invalid-kind", ActionApply,
		[]Node{{AgentID: "edge-a", Revision: 1}, {AgentID: "edge-b", Revision: 1}},
		[]Edge{{FromAgentID: "edge-a", ToAgentID: "edge-b", Kind: EdgeKind("unknown")}},
	)
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("NewPlan() error = %v, want %v", err, ErrInvalidPlan)
	}
}

func TestPlanMarshalAndDigestAreDeterministic(t *testing.T) {
	nodes := []Node{
		{AgentID: "edge-c", Revision: 3},
		{AgentID: "edge-a", Revision: 1},
		{AgentID: "edge-b", Revision: 2},
	}
	edges := []Edge{
		{FromAgentID: "edge-a", ToAgentID: "edge-c", Kind: EdgeKindEgressExecutor, Resource: "rule:2"},
		{FromAgentID: "edge-a", ToAgentID: "edge-b", Kind: EdgeKindRelayLayer, Resource: "rule:1"},
	}
	first, err := NewPlan("operation-deterministic", ActionApply, nodes, edges)
	if err != nil {
		t.Fatalf("NewPlan(first) error = %v", err)
	}
	second, err := NewPlan("operation-deterministic", ActionApply,
		[]Node{nodes[1], nodes[2], nodes[0]},
		[]Edge{edges[1], edges[0]},
	)
	if err != nil {
		t.Fatalf("NewPlan(second) error = %v", err)
	}
	wantPayload, err := first.Marshal()
	if err != nil {
		t.Fatalf("first Marshal() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		gotPayload, err := second.Marshal()
		if err != nil {
			t.Fatalf("second Marshal() iteration %d error = %v", i, err)
		}
		if string(gotPayload) != string(wantPayload) || second.Digest() != first.Digest() {
			t.Fatalf("iteration %d payload/digest is not deterministic", i)
		}
	}
}

func TestPlanEvaluationRemainsApplyingWhileIndependentFrontierExists(t *testing.T) {
	plan, err := NewPlan("operation-partial-progress", ActionApply, []Node{
		{AgentID: "edge-a", Revision: 1},
		{AgentID: "edge-b", Revision: 1},
	}, nil)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	evaluation := plan.Evaluate(map[string]State{
		"edge-a": StateFailed,
		"edge-b": StateWaiting,
	})
	if evaluation.Status != StatusApplying {
		t.Fatalf("status = %q, want %q", evaluation.Status, StatusApplying)
	}
	assertFrontier(t, evaluation, "edge-b")
}

func TestPlanEvaluationUsesForwardApplyReverseDeleteAndDegradedTerminal(t *testing.T) {
	nodes := []Node{
		{AgentID: "edge-a", Revision: 1},
		{AgentID: "edge-b", Revision: 1},
		{AgentID: "edge-c", Revision: 1},
		{AgentID: "edge-d", Revision: 1},
		{AgentID: "edge-independent", Revision: 1},
	}
	edges := []Edge{
		{FromAgentID: "edge-a", ToAgentID: "edge-b", Kind: EdgeKindRelayLayer},
		{FromAgentID: "edge-a", ToAgentID: "edge-c", Kind: EdgeKindRelayLayer},
		{FromAgentID: "edge-b", ToAgentID: "edge-d", Kind: EdgeKindRelayLayer},
		{FromAgentID: "edge-c", ToAgentID: "edge-d", Kind: EdgeKindRelayLayer},
	}
	apply, err := NewPlan("operation-apply", ActionApply, nodes, edges)
	if err != nil {
		t.Fatalf("NewPlan(apply) error = %v", err)
	}
	states := waitingStates(nodes)
	evaluation := apply.Evaluate(states)
	assertFrontier(t, evaluation, "edge-d", "edge-independent")

	states["edge-d"] = StateSucceeded
	states["edge-independent"] = StateSucceeded
	evaluation = apply.Evaluate(states)
	assertFrontier(t, evaluation, "edge-b", "edge-c")

	states["edge-b"] = StateSucceeded
	states["edge-c"] = StateSucceeded
	evaluation = apply.Evaluate(states)
	assertFrontier(t, evaluation, "edge-a")

	deletePlan, err := NewPlan("operation-delete", ActionDelete, nodes, edges)
	if err != nil {
		t.Fatalf("NewPlan(delete) error = %v", err)
	}
	deleteStates := waitingStates(nodes)
	deleteEvaluation := deletePlan.Evaluate(deleteStates)
	assertFrontier(t, deleteEvaluation, "edge-a", "edge-independent")
	deleteStates["edge-a"] = StateSucceeded
	deleteStates["edge-independent"] = StateSucceeded
	deleteEvaluation = deletePlan.Evaluate(deleteStates)
	assertFrontier(t, deleteEvaluation, "edge-b", "edge-c")

	states = waitingStates(nodes)
	states["edge-d"] = StateSucceeded
	states["edge-b"] = StateSucceeded
	states["edge-c"] = StateFailed
	states["edge-independent"] = StateSucceeded
	evaluation = apply.Evaluate(states)
	if evaluation.Status != StatusDegraded {
		t.Fatalf("degraded status = %q, want %q", evaluation.Status, StatusDegraded)
	}
	if len(evaluation.Frontier) != 0 {
		t.Fatalf("degraded frontier = %+v, want empty", evaluation.Frontier)
	}
	result, ok := evaluation.Result("edge-a")
	if !ok || !reflect.DeepEqual(result.BlockedBy, []string{"edge-c"}) {
		t.Fatalf("edge-a result = %+v, found %v", result, ok)
	}
	if states["edge-b"] != StateSucceeded || states["edge-d"] != StateSucceeded {
		t.Fatal("evaluation mutated successful node facts")
	}

	states = waitingStates(nodes)
	states["edge-d"] = StateFailed
	states["edge-independent"] = StateSucceeded
	evaluation = apply.Evaluate(states)
	if evaluation.Status != StatusDegraded || len(evaluation.Frontier) != 0 {
		t.Fatalf("transitive failure evaluation = %+v, want terminal degraded", evaluation)
	}
	for agentID, wantBlockedBy := range map[string][]string{
		"edge-b": {"edge-d"},
		"edge-c": {"edge-d"},
		"edge-a": {"edge-b", "edge-c"},
	} {
		result, ok := evaluation.Result(agentID)
		if !ok || !reflect.DeepEqual(result.BlockedBy, wantBlockedBy) {
			t.Fatalf("%s transitive result = %+v, found %v; want blocked_by %v", agentID, result, ok, wantBlockedBy)
		}
	}

	states = waitingStates(nodes)
	states["edge-c"] = StateFailed
	states["edge-d"] = StateFailed
	states["edge-independent"] = StateSucceeded
	evaluation = apply.Evaluate(states)
	result, ok = evaluation.Result("edge-a")
	if !ok || !reflect.DeepEqual(result.BlockedBy, []string{"edge-b", "edge-c"}) {
		t.Fatalf("mixed direct/transitive result = %+v, found %v; want edge-b and edge-c", result, ok)
	}
}

func assertEdge(t *testing.T, plan Plan, from, to string, kind EdgeKind) {
	t.Helper()
	for _, edge := range plan.Edges {
		if edge.FromAgentID == from && edge.ToAgentID == to && edge.Kind == kind {
			return
		}
	}
	t.Fatalf("edge %s -> %s (%s) not found in %+v", from, to, kind, plan.Edges)
}

func assertFrontier(t *testing.T, evaluation Evaluation, want ...string) {
	t.Helper()
	got := make([]string, len(evaluation.Frontier))
	for i, node := range evaluation.Frontier {
		got[i] = node.AgentID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontier = %v, want %v", got, want)
	}
}

func waitingStates(nodes []Node) map[string]State {
	states := make(map[string]State, len(nodes))
	for _, node := range nodes {
		states[node.AgentID] = StateWaiting
	}
	return states
}

func intPointer(value int) *int { return &value }
