package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestEgressProfileServiceCreateRedactsProxyURLInOutput(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)

	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
		Enabled:  boolPtrEgress(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("ProxyURL = %q, want redacted password", profile.ProxyURL)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored profile count = %d, want 1", len(rows))
	}
	if rows[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("stored ProxyURL = %q, want raw secret preserved", rows[0].ProxyURL)
	}
}

func TestEgressProfileServiceCreateAndNoOpUpdateUseRevisionMutation(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baselineRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(t.Context(), EgressProfileInput{
		Name:    stringPtrEgress("uow direct"),
		Type:    stringPtrEgress("direct"),
		Enabled: boolPtrEgress(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls after create = %d, want 0", applyCalls)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after create error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+1 {
		t.Fatalf("revision count after create = %d, want baseline + 1 (%d)", len(revisions), len(baselineRevisions)+1)
	}
	storedRevision := created.Revision

	updated, err := svc.Update(t.Context(), created.ID, EgressProfileInput{})
	if err != nil {
		t.Fatalf("no-op Update() error = %v", err)
	}
	if updated.Revision != storedRevision {
		t.Fatalf("no-op Update() revision = %d, want persisted revision %d", updated.Revision, storedRevision)
	}
	revisions, err = store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after no-op update error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+1 {
		t.Fatalf("revision count after no-op update = %d, want baseline + 1 (%d)", len(revisions), len(baselineRevisions)+1)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls after no-op update = %d, want 0", applyCalls)
	}
	if _, err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	revisions, err = store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after delete error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+2 {
		t.Fatalf("revision count after delete = %d, want baseline + 2 (%d)", len(revisions), len(baselineRevisions)+2)
	}
}

func TestEgressProfileServiceCreateUsesGlobalRevisionFloor(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:              "edge-a",
		Name:            "edge-a",
		DesiredRevision: 50,
		CurrentRevision: 50,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewEgressProfileService(store)

	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if profile.Revision <= 50 {
		t.Fatalf("Revision = %d, want above global agent revision floor 50", profile.Revision)
	}
}

func TestEgressProfileServiceCreateValidatesProfileTypesAndSchemes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    EgressProfileInput
		wantType string
	}{
		{
			name: "direct clears transport-specific fields",
			input: EgressProfileInput{
				Name:     stringPtrEgress("direct"),
				Type:     stringPtrEgress("direct"),
				ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
			},
			wantType: "direct",
		},
		{
			name: "socks accepts socks scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "socks accepts socks5 scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks5 proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "socks accepts socks5h scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks5h proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5h://127.0.0.1:1080"),
			},
			wantType: "socks",
		},
		{
			name: "http accepts http scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("http proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http://proxy.example.com:8080"),
			},
			wantType: "http",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			profile, err := svc.Create(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if profile.Type != tc.wantType {
				t.Fatalf("Type = %q, want %q", profile.Type, tc.wantType)
			}
			if profile.Type == "direct" && profile.ProxyURL != "" {
				t.Fatalf("direct profile retained transport fields: %+v", profile)
			}
		})
	}
}

func TestEgressProfileServiceCreateRejectsProxyURLsUnsupportedByAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input EgressProfileInput
	}{
		{
			name: "http rejects https scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("https proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("https://proxy.example.com:443"),
			},
		},
		{
			name: "http rejects missing port",
			input: EgressProfileInput{
				Name:     stringPtrEgress("http proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http://proxy.example.com"),
			},
		},
		{
			name: "socks rejects missing port",
			input: EgressProfileInput{
				Name:     stringPtrEgress("socks proxy"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("socks5://proxy.example.com"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			_, err := svc.Create(t.Context(), tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestEgressProfileServiceCreateRejectsInvalidProfileTypesAndSchemes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input EgressProfileInput
	}{
		{
			name: "unknown type",
			input: EgressProfileInput{
				Name: stringPtrEgress("bad"),
				Type: stringPtrEgress("ssh"),
			},
		},
		{
			name: "missing proxy url",
			input: EgressProfileInput{
				Name: stringPtrEgress("missing"),
				Type: stringPtrEgress("socks"),
			},
		},
		{
			name: "socks rejects http scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("wrong socks"),
				Type:     stringPtrEgress("socks"),
				ProxyURL: stringPtrEgress("http://proxy.example.com"),
			},
		},
		{
			name: "http rejects socks scheme",
			input: EgressProfileInput{
				Name:     stringPtrEgress("wrong http"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
			},
		},
		{
			name: "proxy url requires host",
			input: EgressProfileInput{
				Name:     stringPtrEgress("bad proxy"),
				Type:     stringPtrEgress("http"),
				ProxyURL: stringPtrEgress("http:///missing-host"),
			},
		},
		{
			name: "removed wireguard type",
			input: EgressProfileInput{
				Name: stringPtrEgress("wg"),
				Type: stringPtrEgress("wireguard"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)

			_, err := svc.Create(t.Context(), tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestEgressProfileServiceDeleteRejectsReferencesRegardlessOfEnabledState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seed func(t *testing.T, store *storage.SQLiteStore, profileID int)
		want string
	}{
		{
			name: "enabled http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
					ID:              20,
					AgentID:         "local",
					FrontendURL:     "http://example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 20",
		},
		{
			name: "disabled http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
					ID:              22,
					AgentID:         "local",
					FrontendURL:     "http://example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         false,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 22",
		},
		{
			name: "enabled l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
					ID:              21,
					AgentID:         "local",
					Name:            "enabled l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      8443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 21",
		},
		{
			name: "disabled l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
					ID:              23,
					AgentID:         "local",
					Name:            "disabled l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      8443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         false,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 23",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)
			profile := createTestEgressProfile(t, svc)
			tc.seed(t, store, profile.ID)

			_, err := svc.Delete(t.Context(), profile.ID)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Delete() error = %v, want ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Delete() error = %v, want reference %q", err, tc.want)
			}
		})
	}
}

func TestEgressProfileServiceUpdateRejectsDisablingReferencedProfile(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
		ID:              41,
		AgentID:         "local",
		Name:            "enabled l4",
		Protocol:        "tcp",
		ListenHost:      "0.0.0.0",
		ListenPort:      9443,
		BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	_, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		Enabled: boolPtrEgress(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "l4 rule 41") {
		t.Fatalf("Update() error = %v, want l4 rule reference", err)
	}

	got, err := svc.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Enabled {
		t.Fatalf("profile after rejected disable = %+v, want still enabled", got)
	}
}

func TestEgressProfileServiceUpdateRejectsHTTPTypeWhenReferencedByEnabledUDPL4Rule(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveL4Rules(t.Context(), "local", []storage.L4RuleRow{{
		ID:              42,
		AgentID:         "local",
		Name:            "udp l4",
		Protocol:        "udp",
		ListenHost:      "0.0.0.0",
		ListenPort:      5353,
		BackendsJSON:    `[{"host":"127.0.0.1","port":53}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	_, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		Type:     stringPtrEgress("http"),
		ProxyURL: stringPtrEgress("http://127.0.0.1:8080"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "UDP") || !strings.Contains(err.Error(), "l4 rule 42") {
		t.Fatalf("Update() error = %v, want UDP l4 rule reference", err)
	}

	got, err := svc.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Type != "socks" || got.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("profile after rejected type change = %+v, want original socks profile", got)
	}
}

func TestEgressProfileServiceUpdateBumpsReferencedRemoteAgentRevision(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		CapabilitiesJSON: `["egress_profiles"]`,
		DesiredRevision:  50,
		CurrentRevision:  50,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveHTTPRules(t.Context(), "edge-a", []storage.HTTPRuleRow{{
		ID:              51,
		AgentID:         "edge-a",
		FrontendURL:     "http://edge.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	updated, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Revision <= 50 {
		t.Fatalf("updated Revision = %d, want above synced agent revision 50", updated.Revision)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].DesiredRevision != 50 {
		t.Fatalf("agent rows after update = %+v, want legacy desired revision unchanged at 50", agents)
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-a")
	if err != nil {
		t.Fatalf("GetAgentRevisionPointer() error = %v", err)
	}
	if !found || pointer.DesiredRevision != int64(updated.Revision) {
		t.Fatalf("revision pointer after update = %+v, found=%v, want desired revision %d", pointer, found, updated.Revision)
	}
	snapshot, err := store.LoadAgentSnapshot(t.Context(), "edge-a", storage.AgentSnapshotInput{
		DesiredRevision: int(updated.Revision),
		CurrentRevision: 50,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != int64(updated.Revision) {
		t.Fatalf("snapshot Revision = %d, want %d", snapshot.Revision, updated.Revision)
	}
}

func TestEgressProfileServiceUpdateDoesNotUseSaveAgentCompatibilityHook(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		CapabilitiesJSON: `["egress_profiles"]`,
		DesiredRevision:  50,
		CurrentRevision:  50,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveHTTPRules(t.Context(), "edge-a", []storage.HTTPRuleRow{{
		ID:              51,
		AgentID:         "edge-a",
		FrontendURL:     "http://edge.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	failingStore := &failingSaveAgentEgressProfileStore{
		SQLiteStore:   store,
		saveAgentErrs: []error{errors.New("save agent failed")},
	}
	failingSvc := NewEgressProfileService(failingStore)
	updated, err := failingSvc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 || rows[0].ProxyURL != "socks5://127.0.0.1:2080" {
		t.Fatalf("egress profiles after update = %+v, want committed proxy URL", rows)
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-a")
	if err != nil {
		t.Fatalf("GetAgentRevisionPointer() error = %v", err)
	}
	if !found || pointer.DesiredRevision != int64(updated.Revision) {
		t.Fatalf("revision pointer = %+v, found=%v, want desired revision %d", pointer, found, updated.Revision)
	}
}

func TestEgressProfileServiceUpdateDoesNotPartiallyWriteAgentRows(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	for _, row := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", CapabilitiesJSON: `["egress_profiles"]`, DesiredRevision: 50, CurrentRevision: 50},
		{ID: "edge-b", Name: "edge-b", CapabilitiesJSON: `["egress_profiles"]`, DesiredRevision: 60, CurrentRevision: 60},
	} {
		if err := store.SaveAgent(t.Context(), row); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", row.ID, err)
		}
	}
	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	for i, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveHTTPRules(t.Context(), agentID, []storage.HTTPRuleRow{{
			ID:              51 + i,
			AgentID:         agentID,
			FrontendURL:     "http://" + agentID + ".example.com",
			BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
			EgressProfileID: &profile.ID,
			Enabled:         true,
			Revision:        2,
		}}); err != nil {
			t.Fatalf("SaveHTTPRules(%s) error = %v", agentID, err)
		}
	}

	failingStore := &failingSaveAgentEgressProfileStore{
		SQLiteStore:   store,
		saveAgentErrs: []error{nil, errors.New("save second agent failed")},
	}
	failingSvc := NewEgressProfileService(failingStore)
	updated, err := failingSvc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	desired := map[string]int{}
	for _, row := range agents {
		desired[row.ID] = row.DesiredRevision
	}
	if desired["edge-a"] != 50 || desired["edge-b"] != 60 {
		t.Fatalf("agent DesiredRevision after update = %+v, want edge-a=50 edge-b=60", desired)
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		pointer, found, err := store.GetAgentRevisionPointer(t.Context(), agentID)
		if err != nil {
			t.Fatalf("GetAgentRevisionPointer(%s) error = %v", agentID, err)
		}
		if !found || pointer.DesiredRevision <= 0 || pointer.DesiredRevision > int64(updated.Revision) {
			t.Fatalf("revision pointer %s = %+v, found=%v, want committed desired revision", agentID, pointer, found)
		}
	}
	edgeARevisions, err := store.ListAgentRevisions(t.Context(), "edge-a")
	if err != nil {
		t.Fatalf("ListAgentRevisions(edge-a) error = %v", err)
	}
	edgeBRevisions, err := store.ListAgentRevisions(t.Context(), "edge-b")
	if err != nil {
		t.Fatalf("ListAgentRevisions(edge-b) error = %v", err)
	}
	edgeARevision := edgeARevisions[len(edgeARevisions)-1]
	edgeBRevision := edgeBRevisions[len(edgeBRevisions)-1]
	if edgeARevision.OperationID == "" || edgeBRevision.OperationID != edgeARevision.OperationID {
		t.Fatalf("operation ids edge-a=%q edge-b=%q, want one multi-agent operation", edgeARevision.OperationID, edgeBRevision.OperationID)
	}
	if _, found, err := store.GetOperationDependencyArtifact(t.Context(), edgeARevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", err)
	} else if !found {
		t.Fatal("egress update dependency plan artifact was not persisted")
	}
}

func TestEgressProfileServiceUpdateUsesCompleteRelayClosureAndRejectsMissingListener(t *testing.T) {
	t.Parallel()
	store, observer := newDependencyLifecycleAuditStore(t)
	for _, row := range []storage.AgentRow{
		{ID: "rule-owner", Name: "rule-owner", Platform: "linux-amd64", CapabilitiesJSON: `["http_rules","egress_profiles"]`},
		{ID: "relay-mid", Name: "relay-mid", Platform: "linux-amd64", CapabilitiesJSON: `["egress_profiles"]`},
		{ID: "relay-final", Name: "relay-final", Platform: "linux-amd64", CapabilitiesJSON: `["egress_profiles"]`},
	} {
		if err := store.SaveAgent(t.Context(), row); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", row.ID, err)
		}
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-mid", []storage.RelayListenerRow{{
		ID: 101, AgentID: "relay-mid", Name: "relay-mid", ListenHost: "127.0.0.1", ListenPort: 17101,
		PublicHost: "relay-mid.example.test", PublicPort: 17101, Enabled: true, TransportMode: "tls_tcp", Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-mid) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-final", []storage.RelayListenerRow{{
		ID: 102, AgentID: "relay-final", Name: "relay-final", ListenHost: "127.0.0.1", ListenPort: 17102,
		PublicHost: "relay-final.example.test", PublicPort: 17102, Enabled: true, TransportMode: "tls_tcp", Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-final) error = %v", err)
	}

	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveHTTPRules(t.Context(), "rule-owner", []storage.HTTPRuleRow{{
		ID: 201, AgentID: "rule-owner", FrontendURL: "http://egress-relay.example.test:18091",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, EgressProfileID: &profile.ID,
		RelayLayersJSON: `[[101],[102]]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(rule-owner) error = %v", err)
	}
	updated, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	operationID := ""
	for _, agentID := range []string{"rule-owner", "relay-mid", "relay-final"} {
		revisions, listErr := store.ListAgentRevisions(t.Context(), agentID)
		if listErr != nil {
			t.Fatalf("ListAgentRevisions(%s) error = %v", agentID, listErr)
		}
		if len(revisions) == 0 {
			t.Fatalf("no revision recorded for %s", agentID)
		}
		if operationID == "" {
			operationID = revisions[len(revisions)-1].OperationID
		} else if revisions[len(revisions)-1].OperationID != operationID {
			t.Fatalf("operation id for %s = %q, want %q", agentID, revisions[len(revisions)-1].OperationID, operationID)
		}
	}
	artifact, found, err := store.GetOperationDependencyArtifact(t.Context(), operationID)
	if err != nil || !found {
		t.Fatalf("GetOperationDependencyArtifact() found=%v error=%v", found, err)
	}
	plan, err := dependency.ParsePlan(artifact.Payload)
	if err != nil {
		t.Fatalf("ParsePlan() error = %v", err)
	}
	wantEdges := [][2]string{
		{"relay-mid", "relay-final"},
		{"rule-owner", "relay-final"},
		{"rule-owner", "relay-mid"},
	}
	gotEdges := make([][2]string, 0, len(plan.Edges))
	for _, edge := range plan.Edges {
		gotEdges = append(gotEdges, [2]string{edge.FromAgentID, edge.ToAgentID})
	}
	if !reflect.DeepEqual(gotEdges, wantEdges) {
		t.Fatalf("dependency edges = %+v, want %+v", gotEdges, wantEdges)
	}

	if err := store.SaveHTTPRules(t.Context(), "rule-owner", []storage.HTTPRuleRow{{
		ID: 201, AgentID: "rule-owner", FrontendURL: "http://egress-relay.example.test:18091",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, EgressProfileID: &profile.ID,
		RelayLayersJSON: `[[101],[999]]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(missing listener) error = %v", err)
	}
	before := dependencyLifecycleTableCounts(t, observer)
	_, err = svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:3080"),
	})
	if err == nil {
		t.Fatal("Update() with missing relay listener error = nil")
	}
	after := dependencyLifecycleTableCounts(t, observer)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("table counts changed after missing dependency: before=%v after=%v", before, after)
	}
	stored, err := svc.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.ProxyURL != updated.ProxyURL {
		t.Fatalf("profile after missing dependency = %+v, want prior update %+v", stored, updated)
	}
}

func TestEgressProfileServiceUpdateRollsBackOnRevisionMutationFailure(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		CapabilitiesJSON: `["egress_profiles"]`,
		DesiredRevision:  50,
		CurrentRevision:  50,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewEgressProfileService(store)
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveHTTPRules(t.Context(), "edge-a", []storage.HTTPRuleRow{{
		ID:              51,
		AgentID:         "edge-a",
		FrontendURL:     "http://edge.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	failingStore := &failingSaveAgentEgressProfileStore{
		SQLiteStore:         store,
		revisionMutationErr: errors.New("revision mutation failed"),
	}
	failingSvc := NewEgressProfileService(failingStore)
	_, err := failingSvc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	})
	if err == nil || !strings.Contains(err.Error(), "revision mutation failed") {
		t.Fatalf("Update() error = %v, want revision mutation failure", err)
	}
	rows, listErr := store.ListEgressProfiles(t.Context())
	if listErr != nil {
		t.Fatalf("ListEgressProfiles() error = %v", listErr)
	}
	if len(rows) != 1 || rows[0].ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("egress profiles after failed UoW = %+v, want original proxy URL", rows)
	}
}

func TestEgressProfileServiceUpdateDoesNotTriggerLocalApplyWhenLocalExecutorUsesProfile(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return nil
	})
	profile := createTestEgressProfile(t, svc)
	if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
		ID:              61,
		AgentID:         "local",
		FrontendURL:     "http://local.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &profile.ID,
		Enabled:         true,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	if _, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:2080"),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("local apply calls = %d, want 0", applyCalls)
	}
}

func TestEgressProfileServiceUpdateRejectsMismatchedBodyIDAndPreservesProfile(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ID:   intPtrEgress(profile.ID + 1),
		Name: stringPtrEgress("mutated"),
		Type: stringPtrEgress("direct"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}

	got, err := svc.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != profile.ID || got.Name != profile.Name || got.Type != profile.Type || got.ProxyURL != profile.ProxyURL {
		t.Fatalf("profile after rejected update = %+v, want unchanged %+v", got, profile)
	}
	profiles, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Fatalf("profiles after rejected update = %+v, want only original profile", profiles)
	}
}

func TestEgressProfileServiceDeleteRejectsOrphanedAgentReferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seed func(t *testing.T, store *storage.SQLiteStore, profileID int)
		want string
	}{
		{
			name: "orphaned http rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveHTTPRules(t.Context(), "orphan-agent", []storage.HTTPRuleRow{{
					ID:              30,
					AgentID:         "orphan-agent",
					FrontendURL:     "http://orphan.example.com",
					BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveHTTPRules() error = %v", err)
				}
			},
			want: "HTTP rule 30",
		},
		{
			name: "orphaned l4 rule",
			seed: func(t *testing.T, store *storage.SQLiteStore, profileID int) {
				t.Helper()
				if err := store.SaveL4Rules(t.Context(), "orphan-agent", []storage.L4RuleRow{{
					ID:              31,
					AgentID:         "orphan-agent",
					Name:            "orphan l4",
					Protocol:        "tcp",
					ListenHost:      "0.0.0.0",
					ListenPort:      9443,
					BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
					EgressProfileID: &profileID,
					Enabled:         true,
					Revision:        1,
				}}); err != nil {
					t.Fatalf("SaveL4Rules() error = %v", err)
				}
			},
			want: "l4 rule 31",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newEgressProfileTestStore(t)
			svc := NewEgressProfileService(store)
			profile := createTestEgressProfile(t, svc)
			tc.seed(t, store, profile.ID)

			_, err := svc.Delete(t.Context(), profile.ID)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Delete() error = %v, want ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Delete() error = %v, want reference %q", err, tc.want)
			}
		})
	}
}

func TestEgressProfileServiceListAndGetRedactProxySecrets(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)

	proxyProfile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create(proxy) error = %v", err)
	}
	profiles, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("List() count = %d, want 1", len(profiles))
	}
	if profiles[0].ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("List()[0].ProxyURL = %q, want redacted password", profiles[0].ProxyURL)
	}
	gotProxyProfile, err := svc.Get(t.Context(), proxyProfile.ID)
	if err != nil {
		t.Fatalf("Get(proxy) error = %v", err)
	}
	if gotProxyProfile.ProxyURL != "socks5://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("Get(proxy).ProxyURL = %q, want redacted password", gotProxyProfile.ProxyURL)
	}
}

func TestEgressProfileServiceUpdatePreservesProxyPasswordOnRedactedInput(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress(profile.ProxyURL),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ProxyURL != profile.ProxyURL {
		t.Fatalf("ProxyURL = %q, want %q", updated.ProxyURL, profile.ProxyURL)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("stored ProxyURL = %q, want raw secret preserved", rows[0].ProxyURL)
	}
}

func TestEgressProfileServiceUpdateRejectsEditedRedactedProxyURL(t *testing.T) {
	t.Parallel()
	store := newEgressProfileTestStore(t)
	svc := NewEgressProfileService(store)
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://user:secret@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Update(t.Context(), profile.ID, EgressProfileInput{
		ProxyURL: stringPtrEgress("socks5://user:xxxxx@10.0.0.2:1080"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}

	rows, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(rows) != 1 || rows[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("stored rows after rejected update = %+v", rows)
	}
}

func newEgressProfileTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return store
}

type failingSaveAgentEgressProfileStore struct {
	*storage.SQLiteStore
	saveAgentErrs         []error
	saveEgressProfileErrs []error
	revisionMutationErr   error
}

func (s *failingSaveAgentEgressProfileStore) WithRevisionMutation(ctx context.Context, mutate storage.RevisionMutationFunc) error {
	if s.revisionMutationErr != nil {
		return s.revisionMutationErr
	}
	return s.SQLiteStore.WithRevisionMutation(ctx, mutate)
}

func (s *failingSaveAgentEgressProfileStore) SaveAgent(ctx context.Context, row storage.AgentRow) error {
	if err := popRuleStoreError(&s.saveAgentErrs); err != nil {
		return err
	}
	return s.SQLiteStore.SaveAgent(ctx, row)
}

func (s *failingSaveAgentEgressProfileStore) SaveEgressProfiles(ctx context.Context, rows []storage.EgressProfileRow) error {
	if err := popRuleStoreError(&s.saveEgressProfileErrs); err != nil {
		return err
	}
	return s.SQLiteStore.SaveEgressProfiles(ctx, rows)
}

func createTestEgressProfile(t *testing.T, svc *egressProfileService) EgressProfile {
	t.Helper()
	profile, err := svc.Create(t.Context(), EgressProfileInput{
		Name:     stringPtrEgress("office socks"),
		Type:     stringPtrEgress("socks"),
		ProxyURL: stringPtrEgress("socks5://127.0.0.1:1080"),
		Enabled:  boolPtrEgress(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return profile
}

func stringPtrEgress(value string) *string {
	return &value
}

func boolPtrEgress(value bool) *bool {
	return &value
}

func intPtrEgress(value int) *int {
	return &value
}
