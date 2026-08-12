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

type egressProfileReadStore struct {
	egressProfileStore
	rows []storage.EgressProfileRow
}

func (s *egressProfileReadStore) ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error) {
	return append([]storage.EgressProfileRow(nil), s.rows...), nil
}

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
