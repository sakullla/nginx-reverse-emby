package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestAccessAddressClassConstrainsLocalTargets(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":     accessAddressLoopback,
		"localhost":     accessAddressLoopback,
		"[::1]":         accessAddressLoopback,
		"169.254.1.5":   accessAddressLinkLocal,
		"fe80::1":       accessAddressLinkLocal,
		"10.1.2.3":      accessAddressPrivate,
		"192.168.1.9":   accessAddressPrivate,
		"fc00::1":       accessAddressPrivate,
		"0.0.0.0":       accessAddressUnspecified,
		"203.0.113.8":   accessAddressPublic,
		"media.example": accessAddressPublic,
	}
	for host, want := range cases {
		if got := accessAddressClass(host); got != want {
			t.Fatalf("accessAddressClass(%s) = %s, want %s", host, got, want)
		}
		if accessAddressConstrained(want) == (want == accessAddressPublic) {
			t.Fatalf("constrained class mismatch for %s (%s)", host, want)
		}
	}
}

func TestAccessServiceIdempotencyReplaysOriginalService(t *testing.T) {
	h := newAccessHarness(t)
	input := h.directInput("http://10.0.0.8:8096", "http://entry.example/emby")
	body := mustAccessBody(t, input)
	first, err := h.svc.Create(context.Background(), "create-1", body, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID == "create-1" || first.RuleID == 0 {
		t.Fatalf("created service = %#v", first)
	}
	replay, err := h.svc.Create(context.Background(), "create-1", body, input)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != first.ID || replay.RuleID != first.RuleID {
		t.Fatalf("replay = %#v, want id %s rule %d", replay, first.ID, first.RuleID)
	}
	changedRule := h.rules.rules[ruleKey(first.RuleAgentID, first.RuleID)]
	changedRule.Backends = []HTTPRuleBackend{{URL: "http://changed.example"}}
	h.rules.rules[ruleKey(first.RuleAgentID, first.RuleID)] = changedRule
	replay, err = h.svc.Create(context.Background(), "create-1", body, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Rules) != 1 || replay.Rules[0].Backends[0].URL != "http://10.0.0.8:8096" {
		t.Fatalf("replay rebuilt the live rule: %#v", replay.Rules)
	}
	restored := changedRule
	restored.Backends = []HTTPRuleBackend{{URL: "http://10.0.0.8:8096"}}
	h.rules.rules[ruleKey(first.RuleAgentID, first.RuleID)] = restored
	changed := input
	backend := "http://10.0.0.9:8096"
	changed.BackendURL = &backend
	changedBody := mustAccessBody(t, changed)
	if _, err := h.svc.Create(context.Background(), "create-1", changedBody, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key different body error = %v, want conflict", err)
	}
	if len(h.store.services) != 1 {
		t.Fatalf("services = %d, want 1", len(h.store.services))
	}
	next, err := h.svc.Update(context.Background(), first.ID, "update-2", changedBody, changed)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != first.ID || next.RuleID != first.RuleID || h.rules.creates != 1 {
		t.Fatalf("update created another service or rule: id %s rule %d creates %d", next.ID, next.RuleID, h.rules.creates)
	}
	if _, found, _ := h.store.GetAccessServiceOperation(context.Background(), first.ID); found {
		t.Fatal("service id was stored as an idempotency key")
	}
}

func TestAccessServiceConstrainedPathReusesOneListener(t *testing.T) {
	h := newAccessHarness(t)
	shared := RelayListener{
		ID: 9, AgentID: "service-a", Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
		ListenPort: 8443, PublicPort: 8443, PublicHost: accessTestRelayHost, Revision: 4, Name: "shared",
	}
	h.relays.items = []RelayListener{shared}
	input := h.relayInput("http://10.1.2.3:8096", "http://entry.example/emby", 8443)
	view, err := h.create("create-private", input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Path.Mode != accessPathRelay || view.RuleID == 0 || h.probes.calls != 0 || h.relays.creates != 0 {
		t.Fatalf("view=%#v probes=%d creates=%d", view.Path, h.probes.calls, h.relays.creates)
	}
	if !relayListenerFieldsEqual(h.relays.items[0], shared) || view.Path.Summary == "" {
		t.Fatalf("shared listener changed or path missing: %#v summary %q", h.relays.items[0], view.Path.Summary)
	}
	if row := h.row(t, view.ID); row.DedicatedListenerID != 0 {
		t.Fatalf("reused listener stored as dedicated: %d", row.DedicatedListenerID)
	}
	if got := h.rules.rules[ruleKey(view.RuleAgentID, view.RuleID)]; len(got.RelayLayers) != 1 || got.RelayLayers[0][0] != shared.ID {
		t.Fatalf("rule relay = %#v, want shared listener %d", got.RelayLayers, shared.ID)
	}

	h.relays.items = nil
	created, err := h.create("create-new-listener", h.relayInput("http://127.0.0.1:8096", "http://loop.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	if h.relays.creates != 1 || h.probes.calls != 0 || created.ID == view.ID {
		t.Fatalf("creates=%d probes=%d ids %s %s", h.relays.creates, h.probes.calls, created.ID, view.ID)
	}
	listener := h.relays.items[0]
	if listener.AgentID != "service-a" || listener.ListenPort != 9443 || listener.TLSMode != "pki_mtls" {
		t.Fatalf("created listener = %#v", listener)
	}
	againBody := mustAccessBody(t, h.relayInput("http://169.254.1.5:8096", "http://link.example/emby", 9443))
	againInput := h.relayInput("http://169.254.1.5:8096", "http://link.example/emby", 9443)
	third, err := h.svc.Create(context.Background(), "create-link", againBody, againInput)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := h.svc.Update(context.Background(), third.ID, "update-link", mustAccessBody(t, h.relayInput("http://0.0.0.0:8096", "http://unspec.example/emby", 9443)), h.relayInput("http://0.0.0.0:8096", "http://unspec.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != third.ID || h.relays.creates != 1 {
		t.Fatalf("forwarding update created an extra listener or service: creates %d id %s", h.relays.creates, updated.ID)
	}
}

func TestAccessServicePublicProbeChoosesPath(t *testing.T) {
	h := newAccessHarness(t)
	h.probes.pending = true
	view, err := h.create("public-untested", h.directInput("http://203.0.113.10:8096", "http://entry.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != accessSummaryPendingConfirm || view.RuleID != 0 || view.Path.Mode != accessPathPending {
		t.Fatalf("untested public = summary %s rule %d mode %s", view.Summary, view.RuleID, view.Path.Mode)
	}

	h.probes.pending = false
	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend": {status: "http_status", code: 401},
	}
	direct, err := h.create("public-direct", h.directInput("http://203.0.113.11:8096", "http://direct.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	if direct.Path.Mode != accessPathDirect || direct.RuleID == 0 || h.relays.creates != 0 {
		t.Fatalf("401 entry did not stay direct: %#v creates %d", direct.Path, h.relays.creates)
	}
	if len(direct.Probes) != 1 || direct.Probes[0].Status != "http_status" || direct.Probes[0].HTTPStatusCode != 401 {
		t.Fatalf("probe rewritten: %#v", direct.Probes)
	}

	h.agents.agents["entry-a"] = appliedAgent("entry-a", 1)
	h.agents.agents["service-a"] = appliedAgent("service-a", 1)
	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "http_status", code: 200},
		"entry-a|listener":  {status: "connected"},
	}
	relay, err := h.create("public-relay", h.relayInput("http://203.0.113.12:8096", "http://relay.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	if relay.Path.Mode != accessPathRelay || relay.RuleID == 0 || h.relays.creates != 1 {
		t.Fatalf("reachable hop did not choose one relay: mode %s rule %d creates %d err %s", relay.Path.Mode, relay.RuleID, h.relays.creates, relay.LastError)
	}
	if relay.Summary == accessSummaryFailed {
		t.Fatal("entry network_error marked the relay service failed")
	}

	before := h.relays.creates
	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "untested"},
	}
	pending, err := h.create("public-insufficient", h.relayInput("http://203.0.113.13:8096", "http://wait.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	if pending.Summary != accessSummaryPendingConfirm || pending.RuleID != 0 || h.relays.creates != before {
		t.Fatalf("insufficient evidence created a path: summary %s rule %d creates %d", pending.Summary, pending.RuleID, h.relays.creates)
	}

	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "http_status", code: 200},
		"entry-a|listener":  {status: "untested"},
	}
	stuck, err := h.create("public-hop-untested", h.relayInput("http://203.0.113.14:8096", "http://stuck.example/emby", 9543))
	if err != nil {
		t.Fatal(err)
	}
	if stuck.Summary != accessSummaryPendingConfirm || stuck.RuleID != 0 || len(listenerProbes(stuck.Probes)) != 1 {
		t.Fatalf("untested hop stopped the service: summary %s rule %d probes %#v", stuck.Summary, stuck.RuleID, stuck.Probes)
	}
	h.probes.script["entry-a|listener"] = accessProbeScript{status: "connected"}
	retried, err := h.svc.Retry(context.Background(), stuck.ID, "public-hop-retry", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if retried.Path.Mode != accessPathRelay || retried.RuleID == 0 || retried.Summary == accessSummaryFailed {
		t.Fatalf("retry left the untested hop in place: mode %s rule %d summary %s err %s", retried.Path.Mode, retried.RuleID, retried.Summary, retried.LastError)
	}
}

func TestAccessServiceRelayHostStaysUnconfirmedUntilDialable(t *testing.T) {
	h := newAccessHarness(t)
	shared := RelayListener{
		ID: 9, AgentID: "service-a", Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
		ListenPort: 8443, PublicPort: 8443, PublicHost: "0.0.0.0", Revision: 4, Name: "shared",
	}
	h.relays.items = []RelayListener{shared}
	missing := h.relayInput("http://10.1.2.3:8096", "http://entry.example/emby", 8443)
	missing.RelayPublicHost = nil
	view, err := h.create("relay-host-missing", missing)
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != accessSummaryPendingConfirm || view.RuleID != 0 || h.relays.creates != 0 || !strings.Contains(view.LastError, "relay_public_host") {
		t.Fatalf("empty host created a listener: summary %s rule %d creates %d err %s", view.Summary, view.RuleID, h.relays.creates, view.LastError)
	}
	if !relayListenerFieldsEqual(h.relays.items[0], shared) {
		t.Fatalf("shared listener changed: %#v", h.relays.items[0])
	}
	unspecified := "0.0.0.0"
	missing.RelayPublicHost = &unspecified
	missingEntry := "http://unspec-host.example/emby"
	missing.EntryURL = &missingEntry
	again, err := h.create("relay-host-unspecified", missing)
	if err != nil {
		t.Fatal(err)
	}
	if again.Summary != accessSummaryPendingConfirm || again.RuleID != 0 || h.relays.creates != 0 {
		t.Fatalf("unspecified host created a listener: summary %s rule %d creates %d", again.Summary, again.RuleID, h.relays.creates)
	}
	if !relayListenerFieldsEqual(h.relays.items[0], shared) {
		t.Fatalf("shared listener changed after unspecified host: %#v", h.relays.items[0])
	}
	dialable := shared
	dialable.ID = 11
	dialable.Name = "dialable"
	dialable.ListenPort = 9544
	dialable.PublicPort = 9544
	dialable.PublicHost = "203.0.113.11"
	h.relays.items = append(h.relays.items, dialable)
	reuse := h.relayInput("http://10.8.8.8:8096", "http://reuse-host.example/emby", 9544)
	reuse.RelayPublicHost = nil
	reused, err := h.create("relay-host-reuse", reuse)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Path.Mode != accessPathRelay || reused.RuleID == 0 || h.relays.creates != 0 || h.row(t, reused.ID).DedicatedListenerID != 0 {
		t.Fatalf("dialable listener was not reused: mode %s rule %d creates %d dedicated %d err %s", reused.Path.Mode, reused.RuleID, h.relays.creates, h.row(t, reused.ID).DedicatedListenerID, reused.LastError)
	}
	if !relayListenerFieldsEqual(h.relays.items[1], dialable) {
		t.Fatalf("reused listener changed: %#v", h.relays.items[1])
	}

	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "http_status", code: 200},
		"entry-a|listener":  {status: "connected"},
	}
	public := h.relayInput("http://203.0.113.20:8096", "http://public-host.example/emby", 8443)
	public.RelayPublicHost = nil
	calls := h.probes.calls
	publicView, err := h.create("relay-host-public-missing", public)
	if err != nil {
		t.Fatal(err)
	}
	if publicView.Summary != accessSummaryPendingConfirm || publicView.RuleID != 0 || h.relays.creates != 0 || h.probes.calls != calls+2 {
		t.Fatalf("public path probed a hop without a host: summary %s rule %d creates %d calls %d", publicView.Summary, publicView.RuleID, h.relays.creates, h.probes.calls)
	}

	host := "10.2.2.2"
	createdInput := h.relayInput("http://127.0.0.1:8096", "http://dialable.example/emby", 9443)
	createdInput.RelayPublicHost = &host
	created, err := h.create("relay-host-concrete", createdInput)
	if err != nil {
		t.Fatal(err)
	}
	if created.Path.Mode != accessPathRelay || created.RuleID == 0 || h.relays.creates != 1 {
		t.Fatalf("concrete host did not create one listener: mode %s rule %d creates %d err %s", created.Path.Mode, created.RuleID, h.relays.creates, created.LastError)
	}
	createdListener := h.relays.items[len(h.relays.items)-1]
	if createdListener.PublicHost != host || createdListener.PublicHost == "0.0.0.0" || h.row(t, created.ID).DedicatedListenerID != createdListener.ID {
		t.Fatalf("created listener = %#v dedicated %d", createdListener, h.row(t, created.ID).DedicatedListenerID)
	}
	if !relayListenerFieldsEqual(h.relays.items[0], shared) {
		t.Fatalf("creating a listener modified the shared one: %#v", h.relays.items[0])
	}
}

func TestAccessServiceLocalEntryDoesNotRepeatUntestableHop(t *testing.T) {
	h := newAccessHarness(t)
	entry := appliedAgent("entry-a", 1)
	entry.IsLocal = true
	h.agents.agents["entry-a"] = entry
	h.agents.agents["service-a"] = appliedAgent("service-a", 1)
	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "http_status", code: 200},
		"entry-a|listener":  {status: "connected"},
	}
	view, err := h.create("local-hop", h.relayInput("http://203.0.113.21:8096", "http://local-entry.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	hops := listenerProbes(view.Probes)
	if view.Summary != accessSummaryPendingConfirm || view.RuleID != 0 || len(hops) != 1 || hops[0].Error != accessLocalHopUntestable || hops[0].Status == "connected" {
		t.Fatalf("local hop = summary %s rule %d probes %#v", view.Summary, view.RuleID, view.Probes)
	}
	calls := h.probes.calls
	retried, err := h.svc.Retry(context.Background(), view.ID, "local-hop-retry", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if retried.RuleID != 0 || h.probes.calls != calls || len(listenerProbes(retried.Probes)) != 1 {
		t.Fatalf("retry repeated the local hop: rule %d calls %d probes %#v", retried.RuleID, h.probes.calls, retried.Probes)
	}
}

func TestAccessServicePublicRelayWaitsForEntrySnapshot(t *testing.T) {
	h := newAccessHarness(t)
	h.agents.agents["entry-a"] = appliedAgent("entry-a", 1)
	h.agents.agents["service-a"] = appliedAgent("service-a", 1)
	h.svc.store = &accessSnapshotGate{accessRecordStore: h.store, agents: h.agents}
	h.probes.script = map[string]accessProbeScript{
		"entry-a|backend":   {status: "network_error"},
		"service-a|backend": {status: "http_status", code: 200},
		"entry-a|listener":  {status: "connected"},
	}
	view, err := h.create("snapshot-wait", h.relayInput("http://203.0.113.22:8096", "http://snapshot.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	gate := h.svc.store.(*accessSnapshotGate)
	if view.Summary != accessSummaryPendingConfirm || view.RuleID != 0 || h.relays.creates != 1 || gate.publishes != 1 || len(listenerProbes(view.Probes)) != 0 {
		t.Fatalf("hop dispatched before apply: summary %s rule %d creates %d publishes %d probes %#v err %s", view.Summary, view.RuleID, h.relays.creates, gate.publishes, view.Probes, view.LastError)
	}
	if !strings.Contains(h.row(t, view.ID).ProbeJSON, `"purpose":"snapshot"`) || view.LastError != accessEntryListenerUnapplied {
		t.Fatalf("snapshot fact or error missing: err %s probes %s", view.LastError, h.row(t, view.ID).ProbeJSON)
	}
	for _, probe := range view.Probes {
		if probe.Purpose == accessProbePurposeSnapshot {
			t.Fatalf("snapshot fact was shown as a reachability probe: %#v", view.Probes)
		}
	}
	waiting, err := h.svc.Retry(context.Background(), view.ID, "snapshot-wait-retry", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if waiting.RuleID != 0 || gate.publishes != 1 || len(listenerProbes(waiting.Probes)) != 0 || h.relays.creates != 1 {
		t.Fatalf("retry dispatched early: rule %d publishes %d creates %d probes %#v", waiting.RuleID, gate.publishes, h.relays.creates, waiting.Probes)
	}
	h.agents.agents["entry-a"] = appliedAgent("entry-a", h.agents.agents["entry-a"].DesiredRevision)
	ready, err := h.svc.Retry(context.Background(), view.ID, "snapshot-ready", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if ready.Path.Mode != accessPathRelay || ready.RuleID == 0 || gate.publishes != 1 || len(listenerProbes(ready.Probes)) != 1 || listenerProbes(ready.Probes)[0].Status != "connected" {
		t.Fatalf("applied snapshot did not release the hop: mode %s rule %d publishes %d probes %#v err %s", ready.Path.Mode, ready.RuleID, gate.publishes, ready.Probes, ready.LastError)
	}
}

func TestAccessServiceCertificateAndApplySummary(t *testing.T) {
	h := newAccessHarness(t)
	httpView, err := h.create("http-entry", h.directInput("http://10.0.0.8:8096", "http://entry.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	if httpView.Certificate.State != "none" || httpView.Certificate.NotAfter != "" || httpView.HTTPSReady {
		t.Fatalf("http certificate = %#v", httpView.Certificate)
	}
	if httpView.Summary != accessSummaryPendingApply {
		t.Fatalf("unapplied rule summary = %s", httpView.Summary)
	}

	h.certs.certs = []ManagedCertificate{{
		ID: 3, Domain: "secure.example", Enabled: true, Scope: "domain", Status: "error",
		LastError: "issuer failed", NotAfter: "2099-01-01T00:00:00Z",
	}}
	httpsInput := h.directInput("http://10.0.0.8:8096", "https://secure.example/emby")
	httpsView, err := h.create("https-error", httpsInput)
	if err != nil {
		t.Fatal(err)
	}
	if httpsView.EntryURL != "https://secure.example/emby" || httpsView.HTTPSReady || httpsView.Certificate.State != "error" || httpsView.Certificate.NotAfter != "" {
		t.Fatalf("https failure = url %s ready %v cert %#v", httpsView.EntryURL, httpsView.HTTPSReady, httpsView.Certificate)
	}
	if httpsView.Summary != accessSummaryFailed {
		t.Fatalf("https error summary = %s", httpsView.Summary)
	}

	h.certs.certs[0].Status = "active"
	h.agents.agents["entry-a"] = appliedAgent("entry-a", 1)
	ready, err := h.svc.Get(context.Background(), httpsView.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ready.HTTPSReady || ready.Certificate.NotAfter == "" || ready.Summary != accessSummaryReady {
		t.Fatalf("active https = ready %v summary %s cert %#v", ready.HTTPSReady, ready.Summary, ready.Certificate)
	}
}

func TestAccessServiceCrossNodeCutoverKeepsOldRule(t *testing.T) {
	h := newAccessHarness(t)
	h.agents.agents["entry-b"] = appliedAgent("entry-b", 0)
	initial, err := h.create("cutover-create", h.directInput("http://10.0.0.8:8096", "http://old.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	nextInput := h.directInput("http://10.0.0.8:8096", "http://new.example/emby")
	entry := "entry-b"
	port := 9443
	nextInput.EntryAgentID = &entry
	nextInput.RelayListenPort = &port
	host := accessTestRelayHost
	nextInput.RelayPublicHost = &host
	moved, err := h.svc.Update(context.Background(), initial.ID, "cutover-move", mustAccessBody(t, nextInput), nextInput)
	if err != nil {
		t.Fatal(err)
	}
	if moved.RuleID != initial.RuleID || moved.PendingRuleID == 0 || moved.SwitchPhase != accessPhaseWait {
		t.Fatalf("cutover = rule %d pending %d phase %s", moved.RuleID, moved.PendingRuleID, moved.SwitchPhase)
	}
	if _, err := h.svc.Retry(context.Background(), initial.ID, "cutover-retry", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Retire(context.Background(), initial.ID, "cutover-early", []byte(`{}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("early retire = %v", err)
	}
	if len(h.rules.deletes) != 0 {
		t.Fatalf("old rule deletion submitted early: %#v", h.rules.deletes)
	}
	if _, err := h.svc.Abandon(context.Background(), initial.ID, "cutover-abandon", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	abandoned, err := h.svc.Get(context.Background(), initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.RuleID != initial.RuleID || abandoned.PendingRuleID != 0 || abandoned.SwitchPhase != "" {
		t.Fatalf("abandon = %#v", abandoned)
	}

	moved, err = h.svc.Correct(context.Background(), initial.ID, "cutover-move-2", mustAccessBody(t, nextInput), nextInput)
	if err != nil {
		t.Fatal(err)
	}
	h.agents.agents["entry-b"] = appliedAgent("entry-b", 1)
	h.agents.agents["service-a"] = appliedAgent("service-a", 1)
	h.agents.agents["entry-a"] = appliedAgent("entry-a", 1)
	ready, err := h.svc.Get(context.Background(), moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready.SwitchPhase != accessPhaseRetire || ready.RuleID != initial.RuleID {
		t.Fatalf("ready phase = %s rule %d", ready.SwitchPhase, ready.RuleID)
	}
	deletedBefore := len(h.rules.deletes)
	retired, err := h.svc.Retire(context.Background(), moved.ID, "cutover-retire", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if retired.RuleID != 0 || retired.SwitchPhase != accessPhaseRetire || len(h.rules.deletes) != deletedBefore+1 || h.rules.deletes[deletedBefore] != initial.RuleID {
		t.Fatalf("retire = rule %d phase %s deletes %#v", retired.RuleID, retired.SwitchPhase, h.rules.deletes)
	}
	if _, ok := h.rules.rules[ruleKey(moved.PendingRuleAgentID, moved.PendingRuleID)]; !ok {
		t.Fatal("retire deleted the new rule")
	}
	if _, ok := h.rules.rules[ruleKey(initial.RuleAgentID, initial.RuleID)]; ok {
		t.Fatal("retire left the old rule in place")
	}
	promoted, err := h.svc.Get(context.Background(), moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.RuleID != moved.PendingRuleID || promoted.SwitchPhase != "" || promoted.PendingRuleID != 0 {
		t.Fatalf("promoted = rule %d pending %d phase %q", promoted.RuleID, promoted.PendingRuleID, promoted.SwitchPhase)
	}
}

func TestAccessServiceSameNodeUpdatesRuleInPlace(t *testing.T) {
	h := newAccessHarness(t)
	view, err := h.create("same-node", h.directInput("http://10.0.0.8:8096", "http://entry.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	input := h.directInput("http://10.0.0.9:8096", "http://entry.example/emby")
	updated, err := h.svc.Update(context.Background(), view.ID, "same-node-update", mustAccessBody(t, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuleID != view.RuleID || updated.PendingRuleID != 0 || updated.SwitchPhase != "" || h.rules.creates != 1 {
		t.Fatalf("in-place update = rule %d pending %d phase %q creates %d", updated.RuleID, updated.PendingRuleID, updated.SwitchPhase, h.rules.creates)
	}
	name := "library"
	nameInput := AccessServiceInput{Name: &name}
	named, err := h.svc.Update(context.Background(), view.ID, "name-only", mustAccessBody(t, nameInput), nameInput)
	if err != nil {
		t.Fatal(err)
	}
	if named.Name != "library" || named.RuleID != view.RuleID || h.rules.updates != 1 {
		t.Fatalf("name-only changed forwarding: name %s updates %d", named.Name, h.rules.updates)
	}
}

func TestAccessServiceDeleteRemovesOnlyOwnedResources(t *testing.T) {
	h := newAccessHarness(t)
	view, err := h.create("delete-owned", h.relayInput("http://10.1.2.3:8096", "http://entry.example/emby", 9443))
	if err != nil {
		t.Fatal(err)
	}
	listenerID := h.row(t, view.ID).DedicatedListenerID
	h.rules.rules[ruleKey("entry-a", 80)] = HTTPRule{ID: 80, AgentID: "entry-a", FrontendURL: "http://other.example", Backends: []HTTPRuleBackend{{URL: "http://10.9.9.9"}}}
	deleted, err := h.svc.Delete(context.Background(), view.ID, "delete-1", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Summary != accessSummaryPendingApply || deleted.Deletion == nil || !deleted.Deletion.Pending {
		t.Fatalf("deletion view = %#v", deleted.Deletion)
	}
	if len(h.relays.deletes) != 1 || h.relays.deletes[0] != listenerID {
		t.Fatalf("listener deletes = %#v, want %d", h.relays.deletes, listenerID)
	}
	if _, ok := h.rules.rules[ruleKey("entry-a", 80)]; !ok {
		t.Fatal("delete removed a rule from another service")
	}
	foundRetained := map[string]bool{}
	for _, item := range deleted.Deletion.Retained {
		foundRetained[item.Kind] = item.Reason != ""
	}
	if !foundRetained["certificate"] || !foundRetained["agent"] {
		t.Fatalf("retained = %#v", deleted.Deletion.Retained)
	}
	visible, err := h.svc.Get(context.Background(), view.ID)
	if err != nil || visible.Summary != accessSummaryPendingApply {
		t.Fatalf("pending deletion hidden: %v %#v", err, visible)
	}
	h.agents.agents["entry-a"] = appliedAgent("entry-a", visible.Deletion.RequiredRevision["entry-a"])
	h.agents.agents["service-a"] = appliedAgent("service-a", visible.Deletion.RequiredRevision["service-a"])
	if _, err := h.svc.Get(context.Background(), view.ID); !errors.Is(err, ErrAccessServiceNotFound) {
		t.Fatalf("applied deletion get = %v", err)
	}

	shared := RelayListener{
		ID: 4, AgentID: "service-a", Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
		ListenPort: 8443, PublicPort: 8443, PublicHost: accessTestRelayHost, Revision: 2,
	}
	h.relays.items = []RelayListener{shared}
	h.relays.deletes = nil
	sharedView, err := h.create("delete-shared", h.relayInput("http://10.1.2.4:8096", "http://shared.example/emby", 8443))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Delete(context.Background(), sharedView.ID, "delete-shared-op", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(h.relays.deletes) != 0 || !relayListenerFieldsEqual(h.relays.items[0], shared) {
		t.Fatalf("shared listener deleted or modified: deletes %#v item %#v", h.relays.deletes, h.relays.items[0])
	}
}

func TestAccessServiceAdvancedEditRejectsSimplifiedForwarding(t *testing.T) {
	h := newAccessHarness(t)
	view, err := h.create("advanced", h.directInput("http://10.0.0.8:8096", "http://entry.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	key := ruleKey(view.RuleAgentID, view.RuleID)
	rule := h.rules.rules[key]
	rule.Backends = []HTTPRuleBackend{{URL: "http://10.0.0.8:8096"}, {URL: "http://10.0.0.9:8096"}}
	h.rules.rules[key] = rule
	input := h.directInput("http://10.0.0.10:8096", "http://entry.example/emby")
	_, err = h.svc.Update(context.Background(), view.ID, "advanced-edit", mustAccessBody(t, input), input)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "simplified forwarding edit is disabled") {
		t.Fatalf("advanced edit error = %v", err)
	}
	got, err := h.svc.Get(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rules) != 1 || len(got.Rules[0].Backends) != 2 {
		t.Fatalf("advanced view backends = %#v", got.Rules)
	}
	name := "kept"
	nameInput := AccessServiceInput{Name: &name}
	named, err := h.svc.Update(context.Background(), view.ID, "advanced-name", mustAccessBody(t, nameInput), nameInput)
	if err != nil || named.Name != "kept" {
		t.Fatalf("name-only advanced edit = %v %#v", err, named)
	}
	rule.Backends = []HTTPRuleBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider, PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "instance", ProviderID: "provider"}}}
	advanced, _ := accessRuleAdvanced(rule, h.row(t, view.ID).BaselineJSON)
	if !advanced {
		t.Fatal("plugin backend was not advanced")
	}
}

func TestAccessServiceTrafficOmitsMissingSamples(t *testing.T) {
	h := newAccessHarness(t)
	h.traffic.sample = AccessServiceTraffic{State: accessTrafficUnavailable}
	view, err := h.create("traffic-missing", h.directInput("http://10.0.0.8:8096", "http://entry.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	if view.Traffic.State != accessTrafficUnavailable || view.Traffic.RXBytes != nil || view.Traffic.TXBytes != nil || view.Traffic.AccountedBytes != nil {
		t.Fatalf("missing traffic = %#v", view.Traffic)
	}
	rx, tx, accounted := uint64(5), uint64(7), uint64(12)
	h.traffic.sample = AccessServiceTraffic{State: accessTrafficRule, ScopeType: "http_rule", ScopeID: "1", Label: "该规则在当前周期的用量", RXBytes: &rx, TXBytes: &tx, AccountedBytes: &accounted}
	labeled, err := h.svc.Get(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if labeled.Traffic.ScopeType != "http_rule" || labeled.Traffic.Label != "该规则在当前周期的用量" {
		t.Fatalf("rule traffic = %#v", labeled.Traffic)
	}
	h.traffic.sample.State = accessTrafficEntryAgent
	h.traffic.sample.ScopeType = "host_total"
	h.traffic.sample.Label = "入口节点流量或额度，不是该服务的用量"
	entryTraffic, err := h.svc.Get(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entryTraffic.Traffic.Label != "入口节点流量或额度，不是该服务的用量" {
		t.Fatalf("entry traffic label = %q", entryTraffic.Traffic.Label)
	}

	disabled, err := (&trafficService{enabled: false}).ServiceTraffic(context.Background(), "entry-a", 1)
	if err != nil || disabled.State != accessTrafficUnavailable || disabled.RXBytes != nil {
		t.Fatalf("disabled stats = %#v err %v", disabled, err)
	}
	store := &accessTrafficStub{rows: map[string][]storage.TrafficBucketRow{
		"hour|http_rule":  {{AgentID: "entry-a", ScopeType: "http_rule", ScopeID: "other", RXBytes: 9, TXBytes: 9}},
		"day|http_rule":   {{AgentID: "entry-a", ScopeType: "http_rule", ScopeID: "4", RXBytes: 3, TXBytes: 4}},
		"hour|host_total": {{AgentID: "entry-a", ScopeType: "host_total", ScopeID: "node", RXBytes: 8, TXBytes: 1}},
	}}
	reader := &trafficService{enabled: true, now: func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }, tz: time.UTC, store: store}
	sample, err := reader.ServiceTraffic(context.Background(), "entry-a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if sample.ScopeType != "http_rule" || sample.Label != "该规则在当前周期的用量" || sample.RXBytes == nil || *sample.RXBytes != 3 || *sample.TXBytes != 4 {
		t.Fatalf("scoped sample = %#v", sample)
	}
	missing, err := reader.ServiceTraffic(context.Background(), "entry-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if missing.ScopeType != "host_total" || missing.Label != "入口节点流量或额度，不是该服务的用量" || missing.RXBytes == nil || *missing.RXBytes != 8 {
		t.Fatalf("entry sample = %#v", missing)
	}
	store.rows = nil
	empty, err := reader.ServiceTraffic(context.Background(), "entry-a", 4)
	if err != nil || empty.State != accessTrafficUnavailable || empty.RXBytes != nil || empty.TXBytes != nil {
		t.Fatalf("empty sample = %#v err %v", empty, err)
	}
}

func TestAccessServiceIgnoresHistoricalRules(t *testing.T) {
	h := newAccessHarness(t)
	h.rules.rules[ruleKey("entry-a", 3)] = HTTPRule{ID: 3, AgentID: "entry-a", FrontendURL: "http://old.example", Backends: []HTTPRuleBackend{{URL: "http://10.0.0.8"}}}
	views, err := h.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("historical rules listed as services: %#v", views)
	}
}

func TestAccessServiceValidationDoesNotPersist(t *testing.T) {
	h := newAccessHarness(t)
	input := h.directInput("ftp://files.example", "http://entry.example")
	if _, err := h.create("bad-url", input); err == nil {
		t.Fatal("bad url was accepted")
	}
	if len(h.store.services) != 0 || len(h.store.ops) != 0 {
		t.Fatal("validation failure persisted a service")
	}
}

func TestProbeTaskTypeIsDispatchable(t *testing.T) {
	if !isAllowedTaskType(TaskTypeProbeBackendReachability) {
		t.Fatal("probe_backend_reachability is not an allowed task type")
	}
}

const accessTestRelayHost = "203.0.113.40"

func listenerProbes(probes []AccessProbeFact) []AccessProbeFact {
	var hops []AccessProbeFact
	for _, probe := range probes {
		if probe.Purpose == "listener" && probe.HasListener {
			hops = append(hops, probe)
		}
	}
	return hops
}

type accessSnapshotGate struct {
	accessRecordStore
	agents    *fakeAccessAgents
	publishes int
}

func (g *accessSnapshotGate) PublishEntryRelayListener(_ context.Context, entryAgentID string) (int, error) {
	g.publishes++
	agent, ok := g.agents.agents[entryAgentID]
	if !ok {
		return 0, fmt.Errorf("entry agent %s was not found", entryAgentID)
	}
	if !agent.IsLocal && agent.DesiredRevision <= agent.LastApplyRevision {
		next := agent.DesiredRevision
		if next < agent.LastApplyRevision {
			next = agent.LastApplyRevision
		}
		agent.DesiredRevision = next + 1
		g.agents.agents[entryAgentID] = agent
	}
	return agent.DesiredRevision, nil
}

type accessHarness struct {
	svc     *AccessService
	store   *memAccessStore
	rules   *fakeAccessRules
	relays  *fakeAccessRelays
	agents  *fakeAccessAgents
	certs   *fakeAccessCerts
	probes  *fakeAccessProbes
	traffic *fakeAccessTraffic
}

func newAccessHarness(t *testing.T) *accessHarness {
	t.Helper()
	h := &accessHarness{
		store:  &memAccessStore{},
		rules:  &fakeAccessRules{rules: map[string]HTTPRule{}},
		relays: &fakeAccessRelays{security: AccessRelaySecurity{Ready: true, Mode: "pki_mtls", Transport: "tls_tcp"}},
		agents: &fakeAccessAgents{agents: map[string]AgentSummary{
			"entry-a":   {ID: "entry-a", DesiredRevision: 1},
			"service-a": {ID: "service-a", DesiredRevision: 1},
		}},
		certs:   &fakeAccessCerts{},
		probes:  &fakeAccessProbes{script: map[string]accessProbeScript{}},
		traffic: &fakeAccessTraffic{sample: AccessServiceTraffic{State: accessTrafficUnavailable}},
	}
	h.svc = NewAccessService(h.store, h.rules, nil, h.relays, h.agents, h.certs, nil, h.traffic)
	h.svc.probes = h.probes
	h.svc.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
	nextID := 0
	h.svc.newID = func() (string, error) {
		nextID++
		return fmt.Sprintf("svc-%d", nextID), nil
	}
	return h
}

func (h *accessHarness) directInput(backend, entry string) AccessServiceInput {
	serviceAgent := "entry-a"
	entryAgent := "entry-a"
	return AccessServiceInput{BackendURL: &backend, EntryURL: &entry, ServiceAgentID: &serviceAgent, EntryAgentID: &entryAgent}
}

func (h *accessHarness) relayInput(backend, entry string, port int) AccessServiceInput {
	input := h.directInput(backend, entry)
	serviceAgent := "service-a"
	entryAgent := "entry-a"
	host := accessTestRelayHost
	input.ServiceAgentID = &serviceAgent
	input.EntryAgentID = &entryAgent
	input.RelayListenPort = &port
	input.RelayPublicHost = &host
	return input
}

func (h *accessHarness) create(key string, input AccessServiceInput) (AccessServiceView, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return AccessServiceView{}, err
	}
	return h.svc.Create(context.Background(), key, body, input)
}

func (h *accessHarness) row(t *testing.T, id string) storage.AccessServiceRow {
	t.Helper()
	row, found, err := h.store.GetAccessService(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("service %s missing: %v", id, err)
	}
	return row
}

func mustAccessBody(t *testing.T, input AccessServiceInput) []byte {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func appliedAgent(id string, revision int) AgentSummary {
	return AgentSummary{ID: id, DesiredRevision: revision, LastApplyRevision: revision, LastApplyStatus: "success"}
}

func ruleKey(agentID string, id int) string {
	return fmt.Sprintf("%s/%d", agentID, id)
}

func relayListenerFieldsEqual(left, right RelayListener) bool {
	return left.ID == right.ID && left.AgentID == right.AgentID && left.Name == right.Name &&
		left.ListenHost == right.ListenHost && left.ListenPort == right.ListenPort &&
		left.PublicHost == right.PublicHost && left.PublicPort == right.PublicPort &&
		left.Enabled == right.Enabled && left.TLSMode == right.TLSMode &&
		left.TransportMode == right.TransportMode && left.Revision == right.Revision
}

type memAccessStore struct {
	services map[string]storage.AccessServiceRow
	ops      map[string]storage.AccessServiceOperationRow
}

func (m *memAccessStore) ListAccessServices(context.Context) ([]storage.AccessServiceRow, error) {
	rows := make([]storage.AccessServiceRow, 0, len(m.services))
	for _, row := range m.services {
		rows = append(rows, row)
	}
	return rows, nil
}

func (m *memAccessStore) GetAccessService(_ context.Context, id string) (storage.AccessServiceRow, bool, error) {
	row, ok := m.services[id]
	return row, ok, nil
}

func (m *memAccessStore) SaveAccessService(_ context.Context, row storage.AccessServiceRow) error {
	if m.services == nil {
		m.services = map[string]storage.AccessServiceRow{}
	}
	m.services[row.ID] = row
	return nil
}

func (m *memAccessStore) DeleteAccessService(_ context.Context, id string) error {
	delete(m.services, id)
	return nil
}

func (m *memAccessStore) GetAccessServiceOperation(_ context.Context, key string) (storage.AccessServiceOperationRow, bool, error) {
	row, ok := m.ops[key]
	return row, ok, nil
}

func (m *memAccessStore) SaveAccessServiceOperation(_ context.Context, row storage.AccessServiceOperationRow) error {
	if m.ops == nil {
		m.ops = map[string]storage.AccessServiceOperationRow{}
	}
	m.ops[row.IdempotencyKey] = row
	return nil
}

type fakeAccessRules struct {
	rules   map[string]HTTPRule
	next    int
	creates int
	updates int
	deletes []int
}

func (f *fakeAccessRules) Get(_ context.Context, agentID string, id int) (HTTPRule, error) {
	rule, ok := f.rules[ruleKey(agentID, id)]
	if !ok {
		return HTTPRule{}, ErrRuleNotFound
	}
	return rule, nil
}

func (f *fakeAccessRules) List(_ context.Context, agentID string) ([]HTTPRule, error) {
	var rules []HTTPRule
	for _, rule := range f.rules {
		if rule.AgentID == agentID {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func (f *fakeAccessRules) Create(_ context.Context, agentID string, input HTTPRuleInput) (HTTPRule, error) {
	f.creates++
	f.next++
	rule := HTTPRule{ID: f.next, AgentID: agentID, Revision: 1, Enabled: true, Tags: []string{}, RelayLayers: [][]int{}, Backends: []HTTPRuleBackend{}}
	applyFakeRuleInput(&rule, input)
	f.rules[ruleKey(agentID, rule.ID)] = rule
	return rule, nil
}

func (f *fakeAccessRules) Update(_ context.Context, agentID string, id int, input HTTPRuleInput) (HTTPRule, error) {
	rule, err := f.Get(context.Background(), agentID, id)
	if err != nil {
		return HTTPRule{}, err
	}
	f.updates++
	applyFakeRuleInput(&rule, input)
	f.rules[ruleKey(agentID, id)] = rule
	return rule, nil
}

func (f *fakeAccessRules) Delete(_ context.Context, agentID string, id int) (HTTPRule, error) {
	rule, err := f.Get(context.Background(), agentID, id)
	if err != nil {
		return HTTPRule{}, err
	}
	f.deletes = append(f.deletes, id)
	delete(f.rules, ruleKey(agentID, id))
	return rule, nil
}

func applyFakeRuleInput(rule *HTTPRule, input HTTPRuleInput) {
	if input.FrontendURL != nil {
		rule.FrontendURL = *input.FrontendURL
	}
	if input.BackendURL != nil {
		rule.BackendURL = *input.BackendURL
	}
	if input.Backends != nil {
		rule.Backends = append([]HTTPRuleBackend(nil), (*input.Backends)...)
	}
	if input.RelayLayers != nil {
		rule.RelayLayers = append([][]int(nil), (*input.RelayLayers)...)
	}
	if input.Tags != nil {
		rule.Tags = append([]string(nil), (*input.Tags)...)
	}
	if input.Enabled != nil {
		rule.Enabled = *input.Enabled
	}
}

type fakeAccessRelays struct {
	security AccessRelaySecurity
	items    []RelayListener
	next     int
	creates  int
	deletes  []int
}

func (f *fakeAccessRelays) List(_ context.Context, agentID string) ([]RelayListener, error) {
	var items []RelayListener
	for _, item := range f.items {
		if item.AgentID == agentID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *fakeAccessRelays) Create(_ context.Context, agentID string, input RelayListenerInput) (RelayListener, error) {
	f.creates++
	f.next++
	listener := RelayListener{ID: f.next, AgentID: agentID, Enabled: true, Revision: 1}
	if input.Name != nil {
		listener.Name = *input.Name
	}
	if input.ListenPort != nil {
		listener.ListenPort = *input.ListenPort
	}
	if input.PublicPort != nil {
		listener.PublicPort = *input.PublicPort
	}
	if input.PublicHost != nil {
		listener.PublicHost = *input.PublicHost
	}
	if input.TLSMode != nil {
		listener.TLSMode = *input.TLSMode
	}
	if input.TransportMode != nil {
		listener.TransportMode = *input.TransportMode
	}
	f.items = append(f.items, listener)
	return listener, nil
}

func (f *fakeAccessRelays) Delete(_ context.Context, agentID string, id int) (RelayListener, error) {
	for i, item := range f.items {
		if item.AgentID == agentID && item.ID == id {
			f.deletes = append(f.deletes, id)
			f.items = append(f.items[:i], f.items[i+1:]...)
			return item, nil
		}
	}
	return RelayListener{}, ErrRelayListenerNotFound
}

func (f *fakeAccessRelays) AccessRelaySecurity(context.Context) (AccessRelaySecurity, error) {
	return f.security, nil
}

type fakeAccessAgents struct {
	agents map[string]AgentSummary
}

func (f *fakeAccessAgents) Get(_ context.Context, id string) (AgentSummary, error) {
	agent, ok := f.agents[id]
	if !ok {
		return AgentSummary{}, ErrAgentNotFound
	}
	return agent, nil
}

func (f *fakeAccessAgents) List(context.Context) ([]AgentSummary, error) {
	agents := make([]AgentSummary, 0, len(f.agents))
	for _, agent := range f.agents {
		agents = append(agents, agent)
	}
	return agents, nil
}

type fakeAccessCerts struct {
	certs []ManagedCertificate
}

func (f *fakeAccessCerts) List(context.Context, string) ([]ManagedCertificate, error) {
	return append([]ManagedCertificate(nil), f.certs...), nil
}

type accessProbeScript struct {
	status  string
	code    int
	pending bool
}

type fakeAccessProbes struct {
	script  map[string]accessProbeScript
	pending bool
	calls   int
	next    int
	done    map[string]map[string]any
}

func (f *fakeAccessProbes) Dispatch(_ context.Context, agentID string, payload map[string]any) (string, map[string]any, error) {
	f.calls++
	f.next++
	taskID := fmt.Sprintf("probe-%d", f.next)
	listenerID := accessInt(payload["relay_listener_id"])
	kind := "backend"
	if listenerID > 0 {
		kind = "listener"
	}
	script, ok := f.script[agentID+"|"+kind]
	if f.pending || (ok && script.pending) || !ok && f.pending {
		return taskID, nil, nil
	}
	if !ok {
		script = accessProbeScript{status: "untested"}
	}
	result := map[string]any{"agent_id": agentID, "target": payload["target_url"], "status": script.status}
	if script.status == "http_status" {
		result["http_status_code"] = script.code
	}
	if listenerID > 0 {
		result["relay_listener_id"] = listenerID
	}
	if f.done == nil {
		f.done = map[string]map[string]any{}
	}
	f.done[taskID] = result
	return taskID, result, nil
}

func (f *fakeAccessProbes) Result(_ context.Context, _, taskID string) (map[string]any, bool, error) {
	result, ok := f.done[taskID]
	return result, ok, nil
}

type fakeAccessTraffic struct {
	sample AccessServiceTraffic
}

func (f *fakeAccessTraffic) ServiceTraffic(context.Context, string, int) (AccessServiceTraffic, error) {
	return f.sample, nil
}

type accessTrafficStub struct {
	rows map[string][]storage.TrafficBucketRow
}

func (s *accessTrafficStub) GetTrafficPolicy(context.Context, string) (storage.AgentTrafficPolicyRow, error) {
	return storage.AgentTrafficPolicyRow{Direction: "both", CycleStartDay: 1}, nil
}

func (s *accessTrafficStub) ListTrafficPolicies(context.Context) ([]storage.AgentTrafficPolicyRow, error) {
	return nil, nil
}

func (s *accessTrafficStub) SaveTrafficPolicy(context.Context, storage.AgentTrafficPolicyRow) error {
	return nil
}

func (s *accessTrafficStub) GetTrafficBaseline(context.Context, string, string) (storage.AgentTrafficBaselineRow, bool, error) {
	return storage.AgentTrafficBaselineRow{}, false, nil
}

func (s *accessTrafficStub) SaveTrafficBaseline(context.Context, storage.AgentTrafficBaselineRow) error {
	return nil
}

func (s *accessTrafficStub) GetTrafficCursor(context.Context, string, string, string) (storage.AgentTrafficRawCursorRow, bool, error) {
	return storage.AgentTrafficRawCursorRow{}, false, nil
}

func (s *accessTrafficStub) SaveTrafficCursor(context.Context, storage.AgentTrafficRawCursorRow) error {
	return nil
}

func (s *accessTrafficStub) IncrementTrafficBuckets(context.Context, storage.TrafficDelta) error {
	return nil
}

func (s *accessTrafficStub) ListTrafficTrend(_ context.Context, query storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error) {
	if s.rows == nil {
		return nil, nil
	}
	return s.rows[query.Granularity+"|"+query.ScopeType], nil
}

func (s *accessTrafficStub) ListTrafficBreakdown(_ context.Context, query storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error) {
	return s.ListTrafficTrend(context.Background(), query)
}

func (s *accessTrafficStub) DeleteTrafficBefore(context.Context, string, storage.TrafficCleanupCutoff) (int64, error) {
	return 0, nil
}

func (s *accessTrafficStub) DeleteTrafficBucketsByAgentInWindow(context.Context, string, time.Time, time.Time) (int64, error) {
	return 0, nil
}
