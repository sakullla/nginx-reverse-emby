//go:build integration

package cloudflare

import (
	"context"
	"errors"
	"fmt"

	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestDNS01IntentLifecycleOwnershipWildcardAndCNAME(t *testing.T) {
	events := &eventLog{}
	api := newFakeCloudflareAPI(events)
	api.zone = Zone{ID: "zone-id", Name: "example.net", Status: "active"}
	api.records = []TXTRecord{{ID: "existing-id", Name: "delegate.example.net", Content: "user-owned", TTL: 300}}
	propagation := &fakeDNSPropagation{events: events, target: "delegate.example.net"}
	store := newFakeIntentStore(events)
	solver, err := NewDNS01Solver(DNS01Config{Client: api, Propagation: propagation, Intents: store})
	if err != nil {
		t.Fatalf("NewDNS01Solver() error = %v", err)
	}
	if solver.ChallengeType() != acmeflow.ChallengeDNS01 {
		t.Fatalf("ChallengeType() = %q", solver.ChallengeType())
	}
	challenge := testDNS01Challenge()
	if err := solver.Present(context.Background(), challenge); err != nil {
		t.Fatalf("Present() error = %v", err)
	}
	if propagation.resolvedName != "_acme-challenge.example.com" {
		t.Fatalf("ResolveCNAME name = %q", propagation.resolvedName)
	}
	if api.findName != "delegate.example.net" {
		t.Fatalf("FindZone name = %q", api.findName)
	}
	if api.created.Name != "delegate.example.net" || api.created.Content != challenge.DNSValue || api.created.TTL != DefaultRecordTTL {
		t.Fatalf("created record = %#v", api.created)
	}
	if got := events.snapshot(); !orderedEvents(got, "intent.save", "record.create", "intent.record_id") {
		t.Fatalf("create event order = %#v", got)
	}
	if err := solver.Wait(context.Background(), challenge); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if propagation.waitName != "delegate.example.net" || propagation.waitValue != challenge.DNSValue || propagation.waitZone != "example.net" {
		t.Fatalf("WaitTXT args = %q %q %q", propagation.waitName, propagation.waitValue, propagation.waitZone)
	}
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if fmt.Sprint(api.deletedIDs) != "[created-id]" {
		t.Fatalf("deleted IDs = %#v", api.deletedIDs)
	}
	if api.hasRecord("existing-id") == false {
		t.Fatal("existing same-name TXT was deleted")
	}
	if err := solver.Cleanup(context.Background(), challenge); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
	if len(api.deletedIDs) != 1 {
		t.Fatalf("second Cleanup deleted again: %#v", api.deletedIDs)
	}
	intents, err := store.ListChallengeIntents(context.Background())
	if err != nil {
		t.Fatalf("ListChallengeIntents() error = %v", err)
	}
	if len(intents) != 1 || intents[0].Status != acmeflow.ChallengeIntentCompleted || intents[0].RecordID != "created-id" {
		t.Fatalf("intent = %#v", intents)
	}
	serialized := fmt.Sprintf("%#v", intents)
	if strings.Contains(serialized, challenge.DNSValue) {
		t.Fatalf("intent leaked DNS challenge value: %s", serialized)
	}
}

func TestDNS01CrashRecoveryUsesUniqueExactHashAndPreservesExistingTXT(t *testing.T) {
	events := &eventLog{}
	api := newFakeCloudflareAPI(events)
	api.zone = Zone{ID: "zone-id", Name: "example.net", Status: "active"}
	api.records = []TXTRecord{
		{ID: "existing-id", Name: "delegate.example.net", Content: "user-owned", TTL: 300},
		{ID: "recovered-id", Name: "delegate.example.net", Content: "challenge-value", TTL: DefaultRecordTTL},
	}
	store := newFakeIntentStore(events)
	intent, err := acmeflow.NewChallengeIntent("example.net", "delegate.example.net", "challenge-value")
	if err != nil {
		t.Fatalf("NewChallengeIntent() error = %v", err)
	}
	store.intents[intent.ID] = intent
	solver, err := NewDNS01Solver(DNS01Config{Client: api, Propagation: &fakeDNSPropagation{target: "delegate.example.net"}, Intents: store})
	if err != nil {
		t.Fatalf("NewDNS01Solver() error = %v", err)
	}
	if err := solver.RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending() error = %v", err)
	}
	if fmt.Sprint(api.deletedIDs) != "[recovered-id]" {
		t.Fatalf("deleted IDs = %#v", api.deletedIDs)
	}
	if !api.hasRecord("existing-id") {
		t.Fatal("recovery deleted an unrelated same-name TXT")
	}
	stored := store.intents[intent.ID]
	if stored.RecordID != "recovered-id" || stored.Status != acmeflow.ChallengeIntentCompleted {
		t.Fatalf("recovered intent = %#v", stored)
	}
	if got := events.snapshot(); !orderedEvents(got, "record.list", "intent.record_id", "record.delete", "intent.complete") {
		t.Fatalf("recovery event order = %#v", got)
	}
}

func TestDNS01PresentKeepsIntentAfterAmbiguousCreateTimeout(t *testing.T) {
	events := &eventLog{}
	api := newFakeCloudflareAPI(events)
	api.zone = Zone{ID: "zone-id", Name: "example.net", Status: "active"}
	api.createErr = acmeflow.WrapError(acmeflow.CategoryTimeout, "cloudflare_record_create", context.DeadlineExceeded)
	store := newFakeIntentStore(events)
	solver, err := NewDNS01Solver(DNS01Config{
		Client:      api,
		Propagation: &fakeDNSPropagation{target: "delegate.example.net"},
		Intents:     store,
	})
	if err != nil {
		t.Fatalf("NewDNS01Solver() error = %v", err)
	}

	err = solver.Present(context.Background(), testDNS01Challenge())
	if got := acmeflow.ErrorCategoryOf(err); got != acmeflow.CategoryTimeout {
		t.Fatalf("Present() category = %q, want %q; err=%v", got, acmeflow.CategoryTimeout, err)
	}
	intents, listErr := store.ListChallengeIntents(context.Background())
	if listErr != nil || len(intents) != 1 {
		t.Fatalf("ListChallengeIntents() = %#v, %v", intents, listErr)
	}
	if intents[0].Status != acmeflow.ChallengeIntentPending || intents[0].RecordID != "" {
		t.Fatalf("ambiguous create intent = %#v, want pending without a record ID", intents[0])
	}
}

func TestDNS01RecoveryWithDurableStateStore(t *testing.T) {
	root := t.TempDir()
	store, err := acmeflow.OpenStateStore(root)
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	intent, err := acmeflow.NewChallengeIntent("example.net", "delegate.example.net", "challenge-value")
	if err != nil {
		t.Fatalf("NewChallengeIntent() error = %v", err)
	}
	if err := store.SaveChallengeIntent(context.Background(), intent); err != nil {
		t.Fatalf("SaveChallengeIntent() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := acmeflow.OpenStateStore(root)
	if err != nil {
		t.Fatalf("OpenStateStore(reopen) error = %v", err)
	}
	defer reopened.Close()
	api := newFakeCloudflareAPI(&eventLog{})
	api.zone = Zone{ID: "zone-id", Name: "example.net", Status: "active"}
	api.records = []TXTRecord{{ID: "crash-record-id", Name: "delegate.example.net", Content: "challenge-value", TTL: DefaultRecordTTL}}
	solver, err := NewDNS01Solver(DNS01Config{Client: api, Propagation: &fakeDNSPropagation{target: "delegate.example.net"}, Intents: reopened})
	if err != nil {
		t.Fatalf("NewDNS01Solver() error = %v", err)
	}
	if err := solver.RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending() error = %v", err)
	}
	intents, err := reopened.ListChallengeIntents(context.Background())
	if err != nil {
		t.Fatalf("ListChallengeIntents() error = %v", err)
	}
	if len(intents) != 1 || intents[0].Status != acmeflow.ChallengeIntentCompleted || intents[0].RecordID != "crash-record-id" {
		t.Fatalf("recovered durable intent = %#v", intents)
	}
	if fmt.Sprint(api.deletedIDs) != "[crash-record-id]" {
		t.Fatalf("deleted IDs = %#v", api.deletedIDs)
	}
}

func testDNS01Challenge() acmeflow.Challenge {
	return acmeflow.Challenge{
		Type:       acmeflow.ChallengeDNS01,
		Token:      "token-canary",
		Identifier: acmeflow.Identifier{Type: acmeflow.IdentifierDNS, Value: "*.Example.COM."},
		Wildcard:   true,
		DNSValue:   "challenge-value",
	}
}

type fakeCloudflareAPI struct {
	mu         sync.Mutex
	events     *eventLog
	zone       Zone
	findName   string
	records    []TXTRecord
	created    TXTRecord
	deletedIDs []string
	nextID     string
	createErr  error
}

func newFakeCloudflareAPI(events *eventLog) *fakeCloudflareAPI {
	return &fakeCloudflareAPI{events: events, nextID: "created-id"}
}

func (api *fakeCloudflareAPI) FindZone(_ context.Context, name string) (Zone, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.findName = name
	api.events.add("zone.find")
	return api.zone, nil
}

func (api *fakeCloudflareAPI) ListTXTRecords(_ context.Context, _ string, name string) ([]TXTRecord, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.events.add("record.list")
	var records []TXTRecord
	for _, record := range api.records {
		if record.Name == name {
			records = append(records, record)
		}
	}
	return records, nil
}

func (api *fakeCloudflareAPI) CreateTXTRecord(_ context.Context, _ string, name, content string) (TXTRecord, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.events.add("record.create")
	if api.createErr != nil {
		return TXTRecord{}, api.createErr
	}
	api.created = TXTRecord{ID: api.nextID, Name: name, Content: content, TTL: DefaultRecordTTL}
	api.records = append(api.records, api.created)
	return api.created, nil
}

func (api *fakeCloudflareAPI) DeleteRecord(_ context.Context, _, recordID string) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.events.add("record.delete")
	api.deletedIDs = append(api.deletedIDs, recordID)
	kept := api.records[:0]
	for _, record := range api.records {
		if record.ID != recordID {
			kept = append(kept, record)
		}
	}
	api.records = kept
	return nil
}

func (api *fakeCloudflareAPI) hasRecord(id string) bool {
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, record := range api.records {
		if record.ID == id {
			return true
		}
	}
	return false
}

type fakeDNSPropagation struct {
	events       *eventLog
	target       string
	resolvedName string
	waitName     string
	waitValue    string
	waitZone     string
}

func (propagation *fakeDNSPropagation) ResolveCNAME(_ context.Context, name string) (string, error) {
	propagation.resolvedName = name
	if propagation.events != nil {
		propagation.events.add("cname.resolve")
	}
	if propagation.target == "" {
		return name, nil
	}
	return propagation.target, nil
}

func (propagation *fakeDNSPropagation) WaitTXT(_ context.Context, name, value, zone string) error {
	propagation.waitName = name
	propagation.waitValue = value
	propagation.waitZone = zone
	if propagation.events != nil {
		propagation.events.add("txt.wait")
	}
	return nil
}

type fakeIntentStore struct {
	mu              sync.Mutex
	events          *eventLog
	intents         map[string]acmeflow.ChallengeIntent
	failSetRecordID bool
}

func newFakeIntentStore(events *eventLog) *fakeIntentStore {
	return &fakeIntentStore{events: events, intents: make(map[string]acmeflow.ChallengeIntent)}
}

func (store *fakeIntentStore) SaveChallengeIntent(_ context.Context, intent acmeflow.ChallengeIntent) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.events.add("intent.save")
	if existing, ok := store.intents[intent.ID]; ok && existing != intent {
		return errors.New("intent conflict")
	}
	store.intents[intent.ID] = intent
	return nil
}

func (store *fakeIntentStore) SetChallengeRecordID(_ context.Context, id, recordID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.events.add("intent.record_id")
	if store.failSetRecordID {
		return errors.New("injected record ID persistence failure")
	}
	intent, ok := store.intents[id]
	if !ok {
		return acmeflow.ErrChallengeIntentNotFound
	}
	intent.RecordID = recordID
	store.intents[id] = intent
	return nil
}

func (store *fakeIntentStore) CompleteChallengeIntent(_ context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.events.add("intent.complete")
	intent, ok := store.intents[id]
	if !ok {
		return acmeflow.ErrChallengeIntentNotFound
	}
	intent.Status = acmeflow.ChallengeIntentCompleted
	store.intents[id] = intent
	return nil
}

func (store *fakeIntentStore) ListChallengeIntents(context.Context) ([]acmeflow.ChallengeIntent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	intents := make([]acmeflow.ChallengeIntent, 0, len(store.intents))
	for _, intent := range store.intents {
		intents = append(intents, intent)
	}
	return intents, nil
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) add(event string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *eventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

func orderedEvents(events []string, expected ...string) bool {
	position := 0
	for _, event := range events {
		if position < len(expected) && event == expected[position] {
			position++
		}
	}
	return position == len(expected)
}
