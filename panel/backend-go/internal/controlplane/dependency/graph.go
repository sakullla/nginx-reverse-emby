package dependency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrInvalidPlan       = errors.New("invalid dependency plan")
	ErrMissingDependency = errors.New("missing dependency")
	ErrCycle             = errors.New("dependency cycle")
)

type Action string

const (
	ActionApply  Action = "apply"
	ActionDelete Action = "delete"
)

type EdgeKind string

const (
	EdgeKindRelayLayer     EdgeKind = "relay_layer"
	EdgeKindEgressExecutor EdgeKind = "egress_executor"
)

type Node struct {
	AgentID  string `json:"agent_id"`
	Revision int64  `json:"revision"`
}

type Edge struct {
	FromAgentID string   `json:"from_agent_id"`
	ToAgentID   string   `json:"to_agent_id"`
	Kind        EdgeKind `json:"kind"`
	Resource    string   `json:"resource,omitempty"`
}

type Plan struct {
	Version     int    `json:"version"`
	OperationID string `json:"operation_id"`
	Action      Action `json:"action"`
	Nodes       []Node `json:"nodes"`
	Edges       []Edge `json:"edges"`
}

func NewPlan(operationID string, action Action, nodes []Node, edges []Edge) (Plan, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return Plan{}, fmt.Errorf("%w: operation id is required", ErrInvalidPlan)
	}
	if action != ActionApply && action != ActionDelete {
		return Plan{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, action)
	}
	canonicalNodes := append([]Node(nil), nodes...)
	known := make(map[string]Node, len(canonicalNodes))
	for i := range canonicalNodes {
		canonicalNodes[i].AgentID = strings.TrimSpace(canonicalNodes[i].AgentID)
		node := canonicalNodes[i]
		if node.AgentID == "" || node.Revision <= 0 {
			return Plan{}, fmt.Errorf("%w: node identity is invalid", ErrInvalidPlan)
		}
		if _, exists := known[node.AgentID]; exists {
			return Plan{}, fmt.Errorf("%w: agent %q appears more than once", ErrInvalidPlan, node.AgentID)
		}
		known[node.AgentID] = node
	}
	if len(canonicalNodes) == 0 {
		return Plan{}, fmt.Errorf("%w: at least one node is required", ErrInvalidPlan)
	}
	sort.Slice(canonicalNodes, func(i, j int) bool { return canonicalNodes[i].AgentID < canonicalNodes[j].AgentID })

	canonicalEdges := make([]Edge, 0, len(edges))
	seenEdges := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		edge.FromAgentID = strings.TrimSpace(edge.FromAgentID)
		edge.ToAgentID = strings.TrimSpace(edge.ToAgentID)
		edge.Resource = strings.TrimSpace(edge.Resource)
		if edge.FromAgentID == "" || edge.ToAgentID == "" || edge.Kind == "" {
			return Plan{}, fmt.Errorf("%w: edge identity is invalid", ErrInvalidPlan)
		}
		switch edge.Kind {
		case EdgeKindRelayLayer, EdgeKindEgressExecutor:
		default:
			return Plan{}, fmt.Errorf("%w: unsupported edge kind %q", ErrInvalidPlan, edge.Kind)
		}
		if _, ok := known[edge.FromAgentID]; !ok {
			return Plan{}, fmt.Errorf("%w: edge source agent %q is absent", ErrMissingDependency, edge.FromAgentID)
		}
		if _, ok := known[edge.ToAgentID]; !ok {
			return Plan{}, fmt.Errorf("%w: edge dependency agent %q is absent", ErrMissingDependency, edge.ToAgentID)
		}
		if edge.FromAgentID == edge.ToAgentID {
			continue
		}
		key := edgeKey(edge)
		if _, exists := seenEdges[key]; exists {
			continue
		}
		seenEdges[key] = struct{}{}
		canonicalEdges = append(canonicalEdges, edge)
	}
	sort.Slice(canonicalEdges, func(i, j int) bool {
		left, right := canonicalEdges[i], canonicalEdges[j]
		if left.FromAgentID != right.FromAgentID {
			return left.FromAgentID < right.FromAgentID
		}
		if left.ToAgentID != right.ToAgentID {
			return left.ToAgentID < right.ToAgentID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Resource < right.Resource
	})
	if cycle := findCycle(canonicalNodes, canonicalEdges); len(cycle) > 0 {
		return Plan{}, fmt.Errorf("%w: %s", ErrCycle, strings.Join(cycle, " -> "))
	}
	return Plan{
		Version: 1, OperationID: operationID, Action: action,
		Nodes: canonicalNodes, Edges: canonicalEdges,
	}, nil
}

func (p Plan) Marshal() ([]byte, error) {
	canonical, err := NewPlan(p.OperationID, p.Action, p.Nodes, p.Edges)
	if err != nil {
		return nil, err
	}
	return json.Marshal(canonical)
}

func ParsePlan(payload []byte) (Plan, error) {
	var plan Plan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return Plan{}, fmt.Errorf("%w: decode plan: %v", ErrInvalidPlan, err)
	}
	if plan.Version != 1 {
		return Plan{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidPlan, plan.Version)
	}
	return NewPlan(plan.OperationID, plan.Action, plan.Nodes, plan.Edges)
}

func (p Plan) Digest() string {
	payload, err := p.Marshal()
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

type State string

const (
	StateWaiting    State = "waiting"
	StateRunning    State = "running"
	StateSucceeded  State = "succeeded"
	StateFailed     State = "failed"
	StateSuperseded State = "superseded"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusApplying   Status = "applying"
	StatusApplied    Status = "applied"
	StatusFailed     Status = "failed"
	StatusDegraded   Status = "degraded"
	StatusSuperseded Status = "superseded"
)

type NodeResult struct {
	Node      Node     `json:"node"`
	State     State    `json:"state"`
	BlockedBy []string `json:"blocked_by"`
}

type Evaluation struct {
	Status   Status       `json:"status"`
	Frontier []Node       `json:"frontier"`
	Nodes    []NodeResult `json:"nodes"`
}

func (e Evaluation) Result(agentID string) (NodeResult, bool) {
	agentID = strings.TrimSpace(agentID)
	for _, result := range e.Nodes {
		if result.Node.AgentID == agentID {
			return result, true
		}
	}
	return NodeResult{}, false
}

func (p Plan) Evaluate(states map[string]State) Evaluation {
	prerequisites := make(map[string][]string, len(p.Nodes))
	for _, edge := range p.Edges {
		if p.Action == ActionDelete {
			prerequisites[edge.ToAgentID] = appendUnique(prerequisites[edge.ToAgentID], edge.FromAgentID)
		} else {
			prerequisites[edge.FromAgentID] = appendUnique(prerequisites[edge.FromAgentID], edge.ToAgentID)
		}
	}
	for agentID := range prerequisites {
		sort.Strings(prerequisites[agentID])
	}

	normalized := make(map[string]State, len(p.Nodes))
	for _, node := range p.Nodes {
		state := states[node.AgentID]
		if state == "" {
			state = StateWaiting
		}
		normalized[node.AgentID] = state
	}
	blockedBy := make(map[string][]string, len(p.Nodes))
	blockedByFailure := make(map[string]bool, len(p.Nodes))
	blockedBySuperseded := make(map[string]bool, len(p.Nodes))
	for changed := true; changed; {
		changed = false
		for _, node := range p.Nodes {
			if normalized[node.AgentID] != StateWaiting {
				continue
			}
			nextBlockedBy := make([]string, 0, len(prerequisites[node.AgentID]))
			nextFailure := false
			nextSuperseded := false
			for _, prerequisite := range prerequisites[node.AgentID] {
				state := normalized[prerequisite]
				failure := state == StateFailed || blockedByFailure[prerequisite]
				superseded := state == StateSuperseded || blockedBySuperseded[prerequisite]
				if !failure && !superseded {
					continue
				}
				nextBlockedBy = append(nextBlockedBy, prerequisite)
				nextFailure = nextFailure || failure
				nextSuperseded = nextSuperseded || superseded
			}
			sort.Strings(nextBlockedBy)
			if !equalStrings(blockedBy[node.AgentID], nextBlockedBy) ||
				blockedByFailure[node.AgentID] != nextFailure ||
				blockedBySuperseded[node.AgentID] != nextSuperseded {
				blockedBy[node.AgentID] = nextBlockedBy
				blockedByFailure[node.AgentID] = nextFailure
				blockedBySuperseded[node.AgentID] = nextSuperseded
				changed = true
			}
		}
	}

	evaluation := Evaluation{Nodes: make([]NodeResult, 0, len(p.Nodes))}
	succeeded, failed, superseded, running := 0, 0, 0, 0
	failureBlockedCount, supersededBlockedCount := 0, 0
	for _, node := range p.Nodes {
		state := normalized[node.AgentID]
		result := NodeResult{Node: node, State: state, BlockedBy: append([]string(nil), blockedBy[node.AgentID]...)}
		switch state {
		case StateSucceeded:
			succeeded++
		case StateFailed:
			failed++
		case StateSuperseded:
			superseded++
		case StateRunning:
			running++
		case StateWaiting:
			if blockedByFailure[node.AgentID] {
				failureBlockedCount++
			}
			if blockedBySuperseded[node.AgentID] {
				supersededBlockedCount++
			}
			if len(result.BlockedBy) > 0 {
				break
			}
			ready := true
			for _, prerequisite := range prerequisites[node.AgentID] {
				if normalized[prerequisite] != StateSucceeded {
					ready = false
					break
				}
			}
			if ready {
				evaluation.Frontier = append(evaluation.Frontier, node)
			}
		default:
			failed++
		}
		evaluation.Nodes = append(evaluation.Nodes, result)
	}

	sort.Slice(evaluation.Frontier, func(i, j int) bool { return evaluation.Frontier[i].AgentID < evaluation.Frontier[j].AgentID })
	total := len(p.Nodes)
	switch {
	case succeeded == total:
		evaluation.Status = StatusApplied
	case running > 0 || len(evaluation.Frontier) > 0:
		if running > 0 || succeeded > 0 || failed > 0 || superseded > 0 ||
			failureBlockedCount > 0 || supersededBlockedCount > 0 {
			evaluation.Status = StatusApplying
		} else {
			evaluation.Status = StatusPending
		}
	case failed+failureBlockedCount > 0:
		if succeeded > 0 {
			evaluation.Status = StatusDegraded
		} else {
			evaluation.Status = StatusFailed
		}
	case superseded+supersededBlockedCount > 0:
		evaluation.Status = StatusSuperseded
	default:
		evaluation.Status = StatusPending
	}
	return evaluation
}

func findCycle(nodes []Node, edges []Edge) []string {
	adjacency := make(map[string][]string, len(nodes))
	for _, edge := range edges {
		adjacency[edge.FromAgentID] = appendUnique(adjacency[edge.FromAgentID], edge.ToAgentID)
	}
	for agentID := range adjacency {
		sort.Strings(adjacency[agentID])
	}
	state := make(map[string]uint8, len(nodes))
	stack := make([]string, 0, len(nodes))
	stackIndex := make(map[string]int, len(nodes))
	var visit func(string) []string
	visit = func(agentID string) []string {
		state[agentID] = 1
		stackIndex[agentID] = len(stack)
		stack = append(stack, agentID)
		for _, dependency := range adjacency[agentID] {
			switch state[dependency] {
			case 0:
				if cycle := visit(dependency); len(cycle) > 0 {
					return cycle
				}
			case 1:
				start := stackIndex[dependency]
				cycle := append([]string(nil), stack[start:]...)
				return append(cycle, dependency)
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackIndex, agentID)
		state[agentID] = 2
		return nil
	}
	for _, node := range nodes {
		if state[node.AgentID] == 0 {
			if cycle := visit(node.AgentID); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func edgeKey(edge Edge) string {
	return edge.FromAgentID + "\x00" + edge.ToAgentID + "\x00" + string(edge.Kind) + "\x00" + edge.Resource
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
