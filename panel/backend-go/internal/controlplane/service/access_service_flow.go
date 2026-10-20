package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (s *AccessService) Delete(ctx context.Context, id, key string, body []byte) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "delete", key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	opErr := s.deleteService(ctx, &row)
	return s.finish(ctx, "delete", key, body, row, opErr)
}

func (s *AccessService) Retire(ctx context.Context, id, key string, body []byte) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "retire", key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	opErr := s.retireOld(ctx, &row)
	return s.finish(ctx, "retire", key, body, row, opErr)
}

func (s *AccessService) Abandon(ctx context.Context, id, key string, body []byte) (AccessServiceView, error) {
	if view, err, ok := s.replay(ctx, "abandon", key, body); ok {
		return view, err
	}
	row, found, err := s.load(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = ErrAccessServiceNotFound
		}
		return AccessServiceView{}, err
	}
	opErr := s.abandonCutover(ctx, &row)
	return s.finish(ctx, "abandon", key, body, row, opErr)
}

func (s *AccessService) converge(ctx context.Context, row *storage.AccessServiceRow, retry bool) error {
	if row.DeletionPending {
		return nil
	}
	if err := s.refreshProbes(ctx, row, true); err != nil {
		return err
	}
	backend, err := parseAccessHTTPURL(row.BackendURL)
	if err != nil {
		return err
	}
	class := accessAddressClass(backend.Hostname())
	constrained := accessAddressConstrained(class)
	if !constrained && row.EntryAgentID != row.ServiceAgentID {
		if err := s.preparePublicRelayProbe(ctx, row); err != nil {
			return err
		}
	}
	probes := decodeAccessProbes(row.ProbeJSON)
	mode, pending := decideAccessPath(class, row.EntryAgentID, row.ServiceAgentID, probes)
	row.PathMode = mode
	if pending {
		row.PathMode = accessPathPending
		row.PathSummary = "待确认"
		if !constrained {
			return nil
		}
	}
	sameEntry := row.RuleID == 0 || row.RuleAgentID == "" || row.RuleAgentID == row.EntryAgentID
	if !sameEntry && row.SwitchPhase == "" {
		row.SwitchPhase = accessPhasePrepare
	}
	var listenerID int
	if mode == accessPathRelay {
		listener, shared, err := s.ensureListener(ctx, row)
		if err != nil {
			row.LastError = err.Error()
			if !sameEntry {
				row.SwitchPhase = accessPhasePrepare
			}
			return err
		}
		listenerID = listener.ID
		if !shared && row.DedicatedListenerID == 0 {
			row.DedicatedListenerID = listener.ID
			row.DedicatedListenerAgentID = listener.AgentID
		}
		row.PathSummary = row.EntryAgentID + " → " + row.ServiceAgentID + " → " + backend.Host
	} else if mode == accessPathDirect {
		row.PathSummary = "入口直连，由 " + row.ServiceAgentID + " 拨号"
		listenerID = 0
	}
	if pending {
		return nil
	}
	if sameEntry && row.RuleID > 0 {
		if err := s.updateRuleInPlace(ctx, row, listenerID); err != nil {
			row.LastError = err.Error()
			return err
		}
		row.SwitchPhase = ""
		row.LastError = ""
		return nil
	}
	if !sameEntry {
		row.SwitchPhase = accessPhasePrepare
		if err := s.writePendingRule(ctx, row, listenerID); err != nil {
			row.LastError = err.Error()
			return err
		}
		if row.PendingRuleID > 0 {
			row.SwitchPhase = accessPhaseWait
			row.LastError = ""
		}
		return nil
	}
	if err := s.writeCurrentRule(ctx, row, listenerID); err != nil {
		row.LastError = err.Error()
		return err
	}
	row.LastError = ""
	return nil
}

func (s *AccessService) preparePublicRelayProbe(ctx context.Context, row *storage.AccessServiceRow) error {
	probes := decodeAccessProbes(row.ProbeJSON)
	entryProbe := latestAccessProbe(probes, row.EntryAgentID, "backend", false)
	serviceProbe := latestAccessProbe(probes, row.ServiceAgentID, "backend", false)
	if entryProbe == nil || entryProbe.Status != "network_error" || serviceProbe == nil || !accessProbeReachable(serviceProbe.Status) {
		return nil
	}
	listener, _, err := s.ensureListener(ctx, row)
	if err != nil {
		row.LastError = err.Error()
		return err
	}
	if latestAccessProbe(probes, row.EntryAgentID, "listener", true) != nil {
		return nil
	}
	probe, err := s.dispatchProbe(ctx, row.EntryAgentID, row.BackendURL, "listener", listener.ID)
	if err != nil {
		return err
	}
	probes = append(probes, probe)
	encoded, err := json.Marshal(probes)
	if err != nil {
		return err
	}
	row.ProbeJSON = string(encoded)
	return nil
}

func decideAccessPath(class, entryAgent, serviceAgent string, probes []AccessProbeFact) (string, bool) {
	if accessAddressConstrained(class) {
		if entryAgent == serviceAgent {
			return accessPathDirect, false
		}
		return accessPathRelay, false
	}
	if entryAgent == serviceAgent {
		probe := latestAccessProbe(probes, entryAgent, "backend", false)
		if probe == nil || probe.Status == "" || probe.Status == "untested" {
			return accessPathDirect, true
		}
		return accessPathDirect, false
	}
	entryProbe := latestAccessProbe(probes, entryAgent, "backend", false)
	serviceProbe := latestAccessProbe(probes, serviceAgent, "backend", false)
	if entryProbe == nil || entryProbe.Status == "" || entryProbe.Status == "untested" {
		return accessPathPending, true
	}
	if accessProbeReachable(entryProbe.Status) {
		return accessPathDirect, false
	}
	if entryProbe.Status != "network_error" {
		return accessPathPending, true
	}
	if serviceProbe == nil || serviceProbe.Status == "" || serviceProbe.Status == "untested" || !accessProbeReachable(serviceProbe.Status) {
		return accessPathPending, true
	}
	hop := latestAccessProbe(probes, entryAgent, "listener", true)
	if hop == nil || hop.Status == "" || hop.Status == "untested" || !accessProbeReachable(hop.Status) {
		return accessPathPending, true
	}
	return accessPathRelay, false
}

func (s *AccessService) refreshProbes(ctx context.Context, row *storage.AccessServiceRow, dispatch bool) error {
	probes := decodeAccessProbes(row.ProbeJSON)
	if s.probes != nil {
		changed := false
		for i := range probes {
			if probes[i].TaskID == "" {
				continue
			}
			result, done, err := s.probes.Result(ctx, probes[i].AgentID, probes[i].TaskID)
			if err != nil {
				return err
			}
			if !done {
				continue
			}
			probes[i] = mergeAccessProbe(probes[i], result)
			probes[i].TaskID = ""
			changed = true
		}
		if changed {
			encoded, err := json.Marshal(probes)
			if err != nil {
				return err
			}
			row.ProbeJSON = string(encoded)
		}
	}
	if !dispatch {
		return nil
	}
	backend, err := parseAccessHTTPURL(row.BackendURL)
	if err != nil {
		return err
	}
	if accessAddressConstrained(accessAddressClass(backend.Hostname())) {
		return nil
	}
	targets := []string{row.EntryAgentID}
	if row.ServiceAgentID != row.EntryAgentID {
		targets = append(targets, row.ServiceAgentID)
	}
	for _, agentID := range targets {
		idx := latestAccessProbeIndex(probes, agentID, "backend", false)
		if idx >= 0 && !(probes[idx].Status == "untested" && probes[idx].TaskID == "") {
			continue
		}
		probe, err := s.dispatchProbe(ctx, agentID, row.BackendURL, "backend", 0)
		if err != nil {
			return err
		}
		if idx >= 0 {
			probes[idx] = probe
		} else {
			probes = append(probes, probe)
		}
	}
	if row.EntryAgentID != row.ServiceAgentID {
		entryProbe := latestAccessProbe(probes, row.EntryAgentID, "backend", false)
		serviceProbe := latestAccessProbe(probes, row.ServiceAgentID, "backend", false)
		if entryProbe != nil && entryProbe.Status == "network_error" && serviceProbe != nil && accessProbeReachable(serviceProbe.Status) {
			if latestAccessProbe(probes, row.EntryAgentID, "listener", true) == nil && row.DedicatedListenerID > 0 {
				probe, err := s.dispatchProbe(ctx, row.EntryAgentID, row.BackendURL, "listener", row.DedicatedListenerID)
				if err != nil {
					return err
				}
				probes = append(probes, probe)
			}
		}
	}
	encoded, err := json.Marshal(probes)
	if err != nil {
		return err
	}
	row.ProbeJSON = string(encoded)
	return nil
}

func (s *AccessService) dispatchProbe(ctx context.Context, agentID, target, purpose string, listenerID int) (AccessProbeFact, error) {
	fact := AccessProbeFact{
		AgentID: agentID, Target: target, Purpose: purpose, Status: "untested",
		CheckedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if listenerID > 0 {
		fact.HasListener = true
		fact.RelayListenerID = listenerID
	}
	if s.probes == nil {
		fact.Error = "probe task was not dispatched"
		return fact, nil
	}
	payload := map[string]any{"target_url": target}
	if listenerID > 0 {
		payload["relay_listener_id"] = listenerID
	}
	taskID, result, err := s.probes.Dispatch(ctx, agentID, payload)
	if err != nil {
		fact.Error = err.Error()
		return fact, nil
	}
	fact.TaskID = taskID
	if result != nil {
		fact = mergeAccessProbe(fact, result)
		fact.TaskID = ""
	}
	return fact, nil
}

func mergeAccessProbe(base AccessProbeFact, result map[string]any) AccessProbeFact {
	if result == nil {
		base.Status = "untested"
		return base
	}
	if agentID, _ := result["agent_id"].(string); strings.TrimSpace(agentID) != "" {
		base.AgentID = strings.TrimSpace(agentID)
	}
	if target, _ := result["target"].(string); strings.TrimSpace(target) != "" {
		base.Target = strings.TrimSpace(target)
	}
	if checked, _ := result["checked_at"].(string); checked != "" {
		base.CheckedAt = checked
	}
	status, _ := result["status"].(string)
	switch status {
	case "connected", "http_status", "network_error", "untested":
		base.Status = status
	default:
		base.Status = "untested"
	}
	base.HTTPStatusCode = 0
	if base.Status == "http_status" {
		base.HTTPStatusCode = accessInt(result["http_status_code"])
	}
	if id := accessInt(result["relay_listener_id"]); id > 0 {
		base.RelayListenerID = id
		base.HasListener = true
	}
	base.Error = ""
	if base.Status != "connected" && base.Status != "http_status" {
		if message, _ := result["error"].(string); message != "" {
			base.Error = message
		}
	}
	return base
}

func latestAccessProbe(probes []AccessProbeFact, agentID, purpose string, listener bool) *AccessProbeFact {
	idx := latestAccessProbeIndex(probes, agentID, purpose, listener)
	if idx < 0 {
		return nil
	}
	return &probes[idx]
}

func latestAccessProbeIndex(probes []AccessProbeFact, agentID, purpose string, listener bool) int {
	found := -1
	for i := range probes {
		probe := &probes[i]
		if probe.AgentID != agentID || probe.Purpose != purpose || probe.HasListener != listener {
			continue
		}
		if probe.Status == "" && probe.TaskID != "" {
			continue
		}
		found = i
	}
	return found
}

func decodeAccessProbes(raw string) []AccessProbeFact {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var probes []AccessProbeFact
	if err := json.Unmarshal([]byte(raw), &probes); err != nil {
		return nil
	}
	return probes
}

func (s *AccessService) ensureListener(ctx context.Context, row *storage.AccessServiceRow) (RelayListener, bool, error) {
	security, err := s.relays.AccessRelaySecurity(ctx)
	if err != nil {
		return RelayListener{}, false, err
	}
	if !security.Ready {
		reason := strings.TrimSpace(security.Reason)
		if reason == "" {
			reason = "internal PKI is not ready for the current relay security mode"
		}
		return RelayListener{}, false, fmt.Errorf("%w: %s", ErrInvalidArgument, reason)
	}
	listeners, err := s.relays.List(ctx, row.ServiceAgentID)
	if err != nil {
		return RelayListener{}, false, err
	}
	if row.DedicatedListenerID > 0 {
		if listener, ok := findAccessListener(listeners, row.DedicatedListenerID); ok && accessListenerMatches(listener, row, security) {
			return listener, false, nil
		}
	}
	for _, listener := range listeners {
		if listener.ID == row.DedicatedListenerID {
			continue
		}
		if listener.AgentID == row.ServiceAgentID && accessListenerMatches(listener, row, security) {
			return listener, true, nil
		}
	}
	if row.RelayListenPort <= 0 {
		return RelayListener{}, false, fmt.Errorf("%w: relay_listen_port is required to create the single service-side listener; existing shared listeners are not modified", ErrInvalidArgument)
	}
	name := "service-" + row.ID
	port := row.RelayListenPort
	publicHost := row.RelayPublicHost
	if publicHost == "" {
		publicHost = "0.0.0.0"
	}
	enabled := true
	mode := security.Mode
	transport := security.Transport
	if transport == "" {
		transport = "tls_tcp"
	}
	created, err := s.relays.Create(ctx, row.ServiceAgentID, RelayListenerInput{
		Name: &name, ListenPort: &port, PublicPort: &port, PublicHost: &publicHost,
		Enabled: &enabled, TLSMode: &mode, TransportMode: &transport,
	})
	if err != nil {
		return RelayListener{}, false, err
	}
	row.DedicatedListenerID = created.ID
	row.DedicatedListenerAgentID = created.AgentID
	return created, false, nil
}

func findAccessListener(listeners []RelayListener, id int) (RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == id {
			return listener, true
		}
	}
	return RelayListener{}, false
}

func accessListenerMatches(listener RelayListener, row *storage.AccessServiceRow, security AccessRelaySecurity) bool {
	if !listener.Enabled || listener.AgentID != row.ServiceAgentID {
		return false
	}
	transport := security.Transport
	if transport == "" {
		transport = "tls_tcp"
	}
	if listener.TransportMode != transport || listener.TLSMode != security.Mode {
		return false
	}
	if row.RelayListenPort > 0 && listener.ListenPort != row.RelayListenPort && listener.PublicPort != row.RelayListenPort {
		return false
	}
	if row.RelayPublicHost != "" && !strings.EqualFold(listener.PublicHost, row.RelayPublicHost) {
		return false
	}
	return true
}

func (s *AccessService) writeCurrentRule(ctx context.Context, row *storage.AccessServiceRow, listenerID int) error {
	rule, err := s.rules.Create(ctx, row.EntryAgentID, accessRuleInput(row, listenerID))
	if err != nil {
		if errors.Is(err, ErrConflict) {
			row.SwitchPhase = ""
		}
		return err
	}
	row.RuleID = rule.ID
	row.RuleAgentID = rule.AgentID
	row.PendingRuleID = 0
	row.PendingRuleAgentID = ""
	row.SwitchPhase = ""
	return s.storeBaseline(row, rule)
}

func (s *AccessService) writePendingRule(ctx context.Context, row *storage.AccessServiceRow, listenerID int) error {
	if row.PendingRuleID > 0 && row.PendingRuleAgentID == row.EntryAgentID {
		return s.updateSpecificRule(ctx, row, row.PendingRuleAgentID, row.PendingRuleID, listenerID, true)
	}
	rule, err := s.rules.Create(ctx, row.EntryAgentID, accessRuleInput(row, listenerID))
	if err != nil {
		return err
	}
	row.PendingRuleID = rule.ID
	row.PendingRuleAgentID = rule.AgentID
	return s.storeBaseline(row, rule)
}

func (s *AccessService) updateRuleInPlace(ctx context.Context, row *storage.AccessServiceRow, listenerID int) error {
	return s.updateSpecificRule(ctx, row, row.RuleAgentID, row.RuleID, listenerID, false)
}

func (s *AccessService) updateSpecificRule(ctx context.Context, row *storage.AccessServiceRow, agentID string, ruleID, listenerID int, pending bool) error {
	current, err := s.rules.Get(ctx, agentID, ruleID)
	if err != nil {
		return err
	}
	if advanced, reason := accessRuleAdvanced(current, row.BaselineJSON); advanced {
		return newConflictError("simplified forwarding edit is disabled (%s); use the advanced rule editor", reason)
	}
	tags := ensureAccessServiceTag(current.Tags, row.ID)
	layers := accessRelayLayers(listenerID)
	backend := row.BackendURL
	entry := row.EntryURL
	backends := []HTTPRuleBackend{{URL: backend}}
	updated, err := s.rules.Update(ctx, agentID, ruleID, HTTPRuleInput{
		FrontendURL: &entry, BackendURL: &backend, Backends: &backends, RelayLayers: &layers, Tags: &tags,
	})
	if err != nil {
		return err
	}
	if pending {
		row.PendingRuleID = updated.ID
		row.PendingRuleAgentID = updated.AgentID
	} else {
		row.RuleID = updated.ID
		row.RuleAgentID = updated.AgentID
	}
	return s.storeBaseline(row, updated)
}

func accessRuleInput(row *storage.AccessServiceRow, listenerID int) HTTPRuleInput {
	backend := row.BackendURL
	entry := row.EntryURL
	enabled := true
	backends := []HTTPRuleBackend{{URL: backend}}
	layers := accessRelayLayers(listenerID)
	tags := []string{accessServiceTagPrefix + row.ID}
	return HTTPRuleInput{
		FrontendURL: &entry, BackendURL: &backend, Backends: &backends, Enabled: &enabled,
		RelayLayers: &layers, Tags: &tags,
	}
}

func accessRelayLayers(listenerID int) [][]int {
	if listenerID <= 0 {
		return [][]int{}
	}
	return [][]int{{listenerID}}
}

func ensureAccessServiceTag(tags []string, serviceID string) []string {
	want := accessServiceTagPrefix + serviceID
	for _, tag := range tags {
		if tag == want {
			return append([]string(nil), tags...)
		}
	}
	return append(append([]string(nil), tags...), want)
}

type accessRuleBaseline struct {
	Backends           []HTTPRuleBackend  `json:"backends"`
	RelayLayers        [][]int            `json:"relay_layers"`
	RelayObfs          bool               `json:"relay_obfs"`
	UserAgent          string             `json:"user_agent"`
	CustomHeaders      []HTTPCustomHeader `json:"custom_headers"`
	EgressProfileID    *int               `json:"egress_profile_id,omitempty"`
	TrustedProxyRanges []string           `json:"trusted_proxy_ranges,omitempty"`
	LoadBalancing      HTTPLoadBalancing  `json:"load_balancing"`
	PolicyRefID        string             `json:"policy_ref_id,omitempty"`
	ProxyRedirect      bool               `json:"proxy_redirect"`
	PassProxyHeaders   bool               `json:"pass_proxy_headers"`
}

func (s *AccessService) storeBaseline(row *storage.AccessServiceRow, rule HTTPRule) error {
	encoded, err := json.Marshal(accessBaselineFromRule(rule))
	if err != nil {
		return err
	}
	row.BaselineJSON = string(encoded)
	return nil
}

func accessBaselineFromRule(rule HTTPRule) accessRuleBaseline {
	policyID := ""
	if rule.PolicyRef != nil {
		policyID = rule.PolicyRef.ID
	}
	return normalizeAccessBaseline(accessRuleBaseline{
		Backends: rule.Backends, RelayLayers: rule.RelayLayers, RelayObfs: rule.RelayObfs,
		UserAgent: rule.UserAgent, CustomHeaders: rule.CustomHeaders, EgressProfileID: rule.EgressProfileID,
		TrustedProxyRanges: rule.TrustedProxyRanges, LoadBalancing: rule.LoadBalancing,
		PolicyRefID: policyID, ProxyRedirect: rule.ProxyRedirect, PassProxyHeaders: rule.PassProxyHeaders,
	})
}

func normalizeAccessBaseline(baseline accessRuleBaseline) accessRuleBaseline {
	if baseline.Backends == nil {
		baseline.Backends = []HTTPRuleBackend{}
	}
	if baseline.RelayLayers == nil {
		baseline.RelayLayers = [][]int{}
	}
	for i := range baseline.RelayLayers {
		if baseline.RelayLayers[i] == nil {
			baseline.RelayLayers[i] = []int{}
		}
	}
	if baseline.CustomHeaders == nil {
		baseline.CustomHeaders = []HTTPCustomHeader{}
	}
	if baseline.TrustedProxyRanges == nil {
		baseline.TrustedProxyRanges = []string{}
	}
	return baseline
}

func accessRuleAdvanced(rule HTTPRule, baselineJSON string) (bool, string) {
	if len(rule.Backends) > 1 {
		return true, "multiple backends"
	}
	for _, backend := range rule.Backends {
		if backend.Kind == pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider != nil {
			return true, "plugin backend"
		}
	}
	if len(rule.RelayLayers) > 1 {
		return true, "multi-layer relay"
	}
	for _, layer := range rule.RelayLayers {
		if len(layer) > 1 {
			return true, "multi-listener relay layer"
		}
	}
	if strings.TrimSpace(baselineJSON) == "" {
		return false, ""
	}
	var baseline accessRuleBaseline
	if err := json.Unmarshal([]byte(baselineJSON), &baseline); err != nil {
		return true, "rule baseline is unreadable"
	}
	current, _ := json.Marshal(accessBaselineFromRule(rule))
	expected, _ := json.Marshal(normalizeAccessBaseline(baseline))
	if string(current) != string(expected) {
		return true, "forwarding fields differ from the service-created rule"
	}
	return false, ""
}

func (s *AccessService) retireOld(ctx context.Context, row *storage.AccessServiceRow) error {
	if row.RuleID <= 0 || row.PendingRuleID <= 0 {
		return newConflictError("old rule deletion was not submitted because the new entry is not ready")
	}
	ready, err := s.newEntryReady(ctx, row)
	if err != nil {
		return err
	}
	if !ready {
		return newConflictError("old rule deletion was not submitted because the new entry is not ready")
	}
	if _, err := s.rules.Delete(ctx, row.RuleAgentID, row.RuleID); err != nil {
		return err
	}
	agent, err := s.agents.Get(ctx, row.RuleAgentID)
	if err != nil {
		return err
	}
	row.RetiredRuleAgentID = row.RuleAgentID
	row.RetiredRevision = agent.DesiredRevision
	row.RuleID = 0
	row.RuleAgentID = ""
	row.SwitchPhase = accessPhaseRetire
	return nil
}

func (s *AccessService) abandonCutover(ctx context.Context, row *storage.AccessServiceRow) error {
	if row.RuleID <= 0 && row.RetiredRevision > 0 {
		return newConflictError("old rule deletion was already submitted")
	}
	if row.PendingRuleID > 0 {
		if _, err := s.rules.Delete(ctx, row.PendingRuleAgentID, row.PendingRuleID); err != nil && !errors.Is(err, ErrRuleNotFound) {
			return err
		}
	}
	row.PendingRuleID = 0
	row.PendingRuleAgentID = ""
	row.SwitchPhase = ""
	row.LastError = ""
	return nil
}

func (s *AccessService) deleteService(ctx context.Context, row *storage.AccessServiceRow) error {
	stopped := make([]AccessStoppedEntry, 0, 2)
	required := map[string]int{}
	for _, ref := range []struct {
		agentID string
		ruleID  int
	}{{row.RuleAgentID, row.RuleID}, {row.PendingRuleAgentID, row.PendingRuleID}} {
		if ref.ruleID <= 0 || ref.agentID == "" {
			continue
		}
		rule, err := s.rules.Get(ctx, ref.agentID, ref.ruleID)
		if err != nil {
			if errors.Is(err, ErrRuleNotFound) {
				continue
			}
			return err
		}
		if _, err := s.rules.Delete(ctx, ref.agentID, ref.ruleID); err != nil && !errors.Is(err, ErrRuleNotFound) {
			return err
		}
		agent, err := s.agents.Get(ctx, ref.agentID)
		if err != nil {
			return err
		}
		required[ref.agentID] = agent.DesiredRevision
		stopped = append(stopped, AccessStoppedEntry{AgentID: ref.agentID, RuleID: ref.ruleID, URL: rule.FrontendURL})
	}
	retained := []AccessRetainedResource{
		{Kind: "certificate", Reason: "共享证书和不是本服务单独拥有的证书会保留"},
		{Kind: "agent", Reason: "节点会保留"},
	}
	var deletedListeners []int
	if row.DedicatedListenerID > 0 {
		referenced, err := s.listenerReferenced(ctx, row.DedicatedListenerID)
		if err != nil {
			return err
		}
		if referenced {
			retained = append(retained, AccessRetainedResource{
				Kind: "relay_listener", ID: strconv.Itoa(row.DedicatedListenerID),
				Reason: "专用监听器仍被其他规则引用，因此保留",
			})
		} else {
			if _, err := s.relays.Delete(ctx, row.DedicatedListenerAgentID, row.DedicatedListenerID); err != nil && !errors.Is(err, ErrRelayListenerNotFound) {
				return err
			}
			agent, err := s.agents.Get(ctx, row.DedicatedListenerAgentID)
			if err != nil {
				return err
			}
			required[row.DedicatedListenerAgentID] = agent.DesiredRevision
			deletedListeners = []int{row.DedicatedListenerID}
			row.DedicatedListenerID = 0
			row.DedicatedListenerAgentID = ""
		}
	}
	state := AccessDeletionState{
		Pending: true, RequiredRevision: required, StoppedEntries: stopped,
		DeletedListenerIDs: deletedListeners, Retained: retained,
		Note: "删除尚未在节点生效",
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	row.DeletionPending = true
	row.DeletionJSON = string(encoded)
	row.RuleID = 0
	row.RuleAgentID = ""
	row.PendingRuleID = 0
	row.PendingRuleAgentID = ""
	return nil
}

func (s *AccessService) listenerReferenced(ctx context.Context, listenerID int) (bool, error) {
	agents, err := s.agents.List(ctx)
	if err != nil {
		return false, err
	}
	for _, agent := range agents {
		rules, err := s.rules.List(ctx, agent.ID)
		if err != nil {
			return false, err
		}
		for _, rule := range rules {
			for _, id := range flattenRelayLayers(rule.RelayLayers) {
				if id == listenerID {
					return true, nil
				}
			}
		}
		if s.l4 == nil {
			continue
		}
		l4Rules, err := s.l4.List(ctx, agent.ID)
		if err != nil {
			return false, err
		}
		for _, rule := range l4Rules {
			for _, id := range flattenRelayLayers(rule.RelayLayers) {
				if id == listenerID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (s *AccessService) observe(ctx context.Context, row storage.AccessServiceRow) (storage.AccessServiceRow, error) {
	if err := s.refreshProbes(ctx, &row, false); err != nil {
		return row, err
	}
	if err := s.syncPhase(ctx, &row); err != nil {
		return row, err
	}
	if row.DeletionPending {
		done, err := s.deletionApplied(ctx, row)
		if err != nil {
			return row, err
		}
		if done {
			if err := s.store.DeleteAccessService(ctx, row.ID); err != nil {
				return row, err
			}
			return storage.AccessServiceRow{}, nil
		}
	}
	row.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	if err := s.store.SaveAccessService(ctx, row); err != nil {
		return row, err
	}
	return row, nil
}

func (s *AccessService) syncPhase(ctx context.Context, row *storage.AccessServiceRow) error {
	if row.DeletionPending || row.PendingRuleID <= 0 || row.RuleID <= 0 {
		if row.SwitchPhase == accessPhaseRetire && row.RuleID == 0 && row.PendingRuleID > 0 && row.RetiredRevision > 0 {
			agent, err := s.agents.Get(ctx, row.RetiredRuleAgentID)
			if err != nil {
				return err
			}
			if revisionApplied(agent, row.RetiredRevision) {
				row.RuleID = row.PendingRuleID
				row.RuleAgentID = row.PendingRuleAgentID
				row.PendingRuleID = 0
				row.PendingRuleAgentID = ""
				row.SwitchPhase = ""
				row.RetiredRevision = 0
				row.RetiredRuleAgentID = ""
			}
		}
		return nil
	}
	ready, err := s.newEntryReady(ctx, row)
	if err != nil {
		return err
	}
	if row.PendingRuleID > 0 && row.SwitchPhase == accessPhasePrepare {
		row.SwitchPhase = accessPhaseWait
	}
	if ready && (row.SwitchPhase == accessPhaseWait || row.SwitchPhase == accessPhasePrepare) {
		row.SwitchPhase = accessPhaseRetire
	}
	return nil
}

func (s *AccessService) newEntryReady(ctx context.Context, row *storage.AccessServiceRow) (bool, error) {
	if row.PendingRuleID <= 0 {
		return false, nil
	}
	rule, err := s.rules.Get(ctx, row.PendingRuleAgentID, row.PendingRuleID)
	if err != nil {
		return false, err
	}
	agent, err := s.agents.Get(ctx, rule.AgentID)
	if err != nil {
		return false, err
	}
	if !revisionApplied(agent, rule.Revision) {
		return false, nil
	}
	if len(flattenRelayLayers(rule.RelayLayers)) > 0 {
		listenerID := flattenRelayLayers(rule.RelayLayers)[0]
		listener, ok, err := s.findListener(ctx, listenerID)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		listenerAgent, err := s.agents.Get(ctx, listener.AgentID)
		if err != nil {
			return false, err
		}
		if !revisionApplied(listenerAgent, listener.Revision) {
			return false, nil
		}
	}
	entry, err := url.Parse(row.EntryURL)
	if err != nil {
		return false, err
	}
	if strings.EqualFold(entry.Scheme, "https") {
		state, err := s.certificateState(ctx, row.EntryURL, row.EntryAgentID)
		if err != nil {
			return false, err
		}
		if !state.HTTPSReady {
			return false, nil
		}
	}
	return true, nil
}

func (s *AccessService) deletionApplied(ctx context.Context, row storage.AccessServiceRow) (bool, error) {
	var state AccessDeletionState
	if strings.TrimSpace(row.DeletionJSON) == "" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(row.DeletionJSON), &state); err != nil {
		return false, err
	}
	if len(state.RequiredRevision) == 0 {
		return true, nil
	}
	for agentID, revision := range state.RequiredRevision {
		agent, err := s.agents.Get(ctx, agentID)
		if err != nil {
			return false, err
		}
		if !revisionApplied(agent, revision) {
			return false, nil
		}
	}
	return true, nil
}

func revisionApplied(agent AgentSummary, revision int) bool {
	if revision <= 0 {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(agent.LastApplyStatus))
	if status != "success" && status != "applied" {
		return false
	}
	return agent.LastApplyRevision > 0 && revision <= agent.LastApplyRevision
}

func (s *AccessService) present(ctx context.Context, row storage.AccessServiceRow) (AccessServiceView, error) {
	rules := make([]HTTPRule, 0, 2)
	hasPrimary := false
	for _, ref := range []struct {
		agentID string
		ruleID  int
	}{{row.RuleAgentID, row.RuleID}, {row.PendingRuleAgentID, row.PendingRuleID}} {
		if ref.ruleID <= 0 || ref.agentID == "" || s.rules == nil {
			continue
		}
		rule, err := s.rules.Get(ctx, ref.agentID, ref.ruleID)
		if err != nil {
			if errors.Is(err, ErrRuleNotFound) {
				continue
			}
			return AccessServiceView{}, err
		}
		rules = append(rules, rule)
		hasPrimary = true
	}
	advanced := false
	reason := ""
	for _, rule := range rules {
		if isAdvanced, why := accessRuleAdvanced(rule, row.BaselineJSON); isAdvanced {
			advanced = true
			reason = why
			break
		}
	}
	cert, err := s.certificateState(ctx, row.EntryURL, row.EntryAgentID)
	if err != nil {
		return AccessServiceView{}, err
	}
	apply, err := s.applyState(ctx, row, rules, cert)
	if err != nil {
		return AccessServiceView{}, err
	}
	traffic := AccessServiceTraffic{State: accessTrafficUnavailable}
	if s.traffic != nil {
		ruleID := row.RuleID
		if ruleID == 0 {
			ruleID = row.PendingRuleID
		}
		traffic, err = s.traffic.ServiceTraffic(ctx, row.EntryAgentID, ruleID)
		if err != nil {
			return AccessServiceView{}, err
		}
	}
	var confirmation *AccessClientConfirmation
	if strings.TrimSpace(row.ClientConfirmJSON) != "" {
		var decoded AccessClientConfirmation
		if err := json.Unmarshal([]byte(row.ClientConfirmJSON), &decoded); err != nil {
			return AccessServiceView{}, err
		}
		confirmation = &decoded
	}
	var deletion *AccessDeletionState
	if strings.TrimSpace(row.DeletionJSON) != "" {
		var decoded AccessDeletionState
		if err := json.Unmarshal([]byte(row.DeletionJSON), &decoded); err != nil {
			return AccessServiceView{}, err
		}
		deletion = &decoded
	}
	view := AccessServiceView{
		ID: row.ID, Name: row.Name, EntryAgentID: row.EntryAgentID, ServiceAgentID: row.ServiceAgentID,
		BackendURL: row.BackendURL, EntryURL: row.EntryURL, RuleAgentID: row.RuleAgentID, RuleID: row.RuleID,
		PendingRuleAgentID: row.PendingRuleAgentID, PendingRuleID: row.PendingRuleID, SwitchPhase: row.SwitchPhase,
		HTTPSReady: cert.HTTPSReady, Advanced: advanced, AdvancedReason: reason, LastError: row.LastError,
		Path:        AccessPathState{Mode: row.PathMode, Summary: row.PathSummary, Pending: row.PathMode == accessPathPending},
		Certificate: cert, Probes: decodeAccessProbes(row.ProbeJSON), Rules: rules, Traffic: traffic,
		ClientConfirmation: confirmation, Deletion: deletion, Apply: apply,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if view.Rules == nil {
		view.Rules = []HTTPRule{}
	}
	if view.Probes == nil {
		view.Probes = []AccessProbeFact{}
	}
	view.Summary = accessSummary(view, hasPrimary)
	return view, nil
}

func accessChosenProbeFailed(view AccessServiceView) bool {
	agentID := view.EntryAgentID
	if view.Path.Mode == accessPathRelay {
		agentID = view.ServiceAgentID
	} else if view.Path.Mode != accessPathDirect {
		return false
	}
	probe := latestAccessProbe(view.Probes, agentID, "backend", false)
	return probe != nil && probe.Status == "network_error"
}

func accessSummary(view AccessServiceView, hasRule bool) string {
	if view.Deletion != nil && view.Deletion.Pending {
		return accessSummaryPendingApply
	}
	if view.Certificate.State == "error" || accessChosenProbeFailed(view) {
		return accessSummaryFailed
	}
	if view.Path.Pending || view.Path.Mode == accessPathPending {
		return accessSummaryPendingConfirm
	}
	if view.LastError != "" && !hasRule {
		return accessSummaryFailed
	}
	if hasRule && view.Apply.Pending {
		return accessSummaryPendingApply
	}
	if view.SwitchPhase != "" {
		return view.SwitchPhase
	}
	if strings.HasPrefix(strings.ToLower(view.EntryURL), "https:") && !view.HTTPSReady {
		return accessSummaryPartial
	}
	if !hasRule {
		return accessSummaryFailed
	}
	return accessSummaryReady
}

func (s *AccessService) certificateState(ctx context.Context, entryURL, agentID string) (AccessCertificateState, error) {
	parsed, err := url.Parse(strings.TrimSpace(entryURL))
	if err != nil || parsed == nil {
		return AccessCertificateState{State: "none"}, nil
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return AccessCertificateState{State: "none"}, nil
	}
	if s.certs == nil {
		return AccessCertificateState{State: "pending"}, nil
	}
	certs, err := s.certs.List(ctx, "")
	if err != nil {
		return AccessCertificateState{}, err
	}
	rows := make([]storage.ManagedCertificateRow, 0, len(certs))
	for _, cert := range certs {
		rows = append(rows, managedCertificateToRow(cert))
	}
	scope := "domain"
	host := parsed.Hostname()
	if netHostIsIP(host) {
		scope = "ip"
	}
	cert, _, found := findBestManagedCertificateForHost(rows, agentID, host, scope)
	if !found {
		return AccessCertificateState{State: "pending"}, nil
	}
	switch strings.ToLower(strings.TrimSpace(cert.Status)) {
	case "active":
		state := AccessCertificateState{State: "ready", HTTPSReady: true, NotAfter: cert.NotAfter}
		return state, nil
	case "error":
		return AccessCertificateState{State: "error", LastError: cert.LastError, HTTPSReady: false}, nil
	case "issuing":
		return AccessCertificateState{State: "issuing", HTTPSReady: false}, nil
	default:
		return AccessCertificateState{State: "pending", HTTPSReady: false}, nil
	}
}

func netHostIsIP(host string) bool {
	return net.ParseIP(strings.Trim(strings.TrimSpace(host), "[]")) != nil
}

func (s *AccessService) findListener(ctx context.Context, listenerID int) (RelayListener, bool, error) {
	agents, err := s.agents.List(ctx)
	if err != nil {
		return RelayListener{}, false, err
	}
	for _, agent := range agents {
		listeners, err := s.relays.List(ctx, agent.ID)
		if err != nil {
			return RelayListener{}, false, err
		}
		if listener, ok := findAccessListener(listeners, listenerID); ok {
			return listener, true, nil
		}
	}
	return RelayListener{}, false, nil
}

func (s *AccessService) applyState(ctx context.Context, row storage.AccessServiceRow, rules []HTTPRule, cert AccessCertificateState) (AccessApplyState, error) {
	state := AccessApplyState{CertificateReady: !strings.EqualFold(urlScheme(row.EntryURL), "https") || cert.HTTPSReady}
	pending := false
	for _, rule := range rules {
		agent, err := s.agents.Get(ctx, rule.AgentID)
		if err != nil {
			return state, err
		}
		applied := revisionApplied(agent, rule.Revision)
		if rule.ID == row.PendingRuleID {
			state.NewEntryApplied = applied
		}
		if !applied {
			pending = true
		}
		if len(flattenRelayLayers(rule.RelayLayers)) > 0 && s.relays != nil {
			listener, ok, err := s.findListener(ctx, flattenRelayLayers(rule.RelayLayers)[0])
			if err != nil {
				return state, err
			}
			if ok {
				listenerAgent, err := s.agents.Get(ctx, listener.AgentID)
				if err != nil {
					return state, err
				}
				state.ListenerApplied = revisionApplied(listenerAgent, listener.Revision)
				if !state.ListenerApplied {
					pending = true
				}
			}
		}
	}
	if row.SwitchPhase == accessPhaseRetire && row.RetiredRevision > 0 {
		agent, err := s.agents.Get(ctx, row.RetiredRuleAgentID)
		if err != nil {
			return state, err
		}
		state.OldEntryMayReceive = !revisionApplied(agent, row.RetiredRevision)
		if state.OldEntryMayReceive {
			pending = true
		}
	}
	state.Pending = pending
	return state, nil
}

func urlScheme(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Scheme
}

func accessInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}
