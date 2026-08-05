package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentapp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	agentcore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

func TestEmbeddedTunnelCredentialStoreIsIndependentFromRemoteAgentRoot(t *testing.T) {
	previousNewEmbeddedApp := newEmbeddedApp
	t.Cleanup(func() { newEmbeddedApp = previousNewEmbeddedApp })
	newEmbeddedApp = func(agentapp.Config, agentcore.Store, agentapp.SyncClient) (embeddedAppRunner, error) {
		return pkiTestApp{}, nil
	}

	dataRoot := t.TempDir()
	runtime, err := New(Config{AgentID: "local", AgentName: "local", DataDir: dataRoot}, pkiTestSource{}, pkiTestSink{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	embeddedStore := runtime.TunnelCredentialStore()
	if embeddedStore == nil {
		t.Fatal("embedded tunnel credential store is nil")
	}
	wantEmbeddedRoot := filepath.Join(dataRoot, stateRootDir, "pki")
	if embeddedStore.delegate.Root() != wantEmbeddedRoot {
		t.Fatalf("embedded PKI root = %s, want %s", embeddedStore.delegate.Root(), wantEmbeddedRoot)
	}

	remoteStore, err := modulepki.NewStore(dataRoot)
	if err != nil {
		t.Fatalf("NewStore(remote) error = %v", err)
	}
	if remoteStore.Root() == embeddedStore.delegate.Root() {
		t.Fatalf("remote and embedded stores share root %s", remoteStore.Root())
	}
	spec := modulepki.EnrollmentSpec{
		StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent,
		Purpose: model.PKICertificatePurposeClient,
	}
	embeddedPending, err := embeddedStore.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("embedded PrepareEnrollment() error = %v", err)
	}
	remotePending, err := remoteStore.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("remote PrepareEnrollment() error = %v", err)
	}
	if embeddedPending.Request.RequestID == remotePending.Request.RequestID || embeddedPending.Request.CSRPEM == remotePending.Request.CSRPEM {
		t.Fatal("remote and embedded tunnel identities reused enrollment material")
	}
}

func TestEmbeddedTunnelCredentialStoreReplaysPendingEnrollmentAfterRuntimeRestart(t *testing.T) {
	previousNewEmbeddedApp := newEmbeddedApp
	t.Cleanup(func() { newEmbeddedApp = previousNewEmbeddedApp })
	newEmbeddedApp = func(agentapp.Config, agentcore.Store, agentapp.SyncClient) (embeddedAppRunner, error) {
		return pkiTestApp{}, nil
	}

	dataRoot := t.TempDir()
	config := Config{AgentID: "local", AgentName: "local", DataDir: dataRoot}
	firstRuntime, err := New(config, pkiTestSource{}, pkiTestSink{})
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	spec := PKIEnrollmentSpec{
		StorageIdentity: "agent",
		DomainID:        "domain-1",
		AgentID:         "local",
		Kind:            model.PKIIdentityKindAgent,
		Purpose:         model.PKICertificatePurposeClient,
	}
	first, err := firstRuntime.TunnelCredentialStore().PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("first PrepareEnrollment() error = %v", err)
	}
	privateKeyPath := filepath.Join(firstRuntime.TunnelCredentialStore().delegate.Root(), "identities", "agent", "pending", "private-key.pem")
	firstPrivateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatalf("read first pending private key: %v", err)
	}
	if err := firstRuntime.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	secondRuntime, err := New(config, pkiTestSource{}, pkiTestSink{})
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	t.Cleanup(func() { _ = secondRuntime.Close() })
	replayed, err := secondRuntime.TunnelCredentialStore().PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("replayed PrepareEnrollment() error = %v", err)
	}
	if replayed.Version != first.Version || replayed.StorageIdentity != first.StorageIdentity ||
		replayed.Request.RequestID != first.Request.RequestID || replayed.Request.CSRPEM != first.Request.CSRPEM ||
		replayed.RequestFingerprint != first.RequestFingerprint || replayed.PublicKeyFingerprint != first.PublicKeyFingerprint ||
		replayed.DomainID != first.DomainID || replayed.AgentID != first.AgentID || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("replayed pending enrollment changed across restart:\nfirst:  %+v\nsecond: %+v", first, replayed)
	}
	secondPrivateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatalf("read replayed pending private key: %v", err)
	}
	if !reflect.DeepEqual(secondPrivateKey, firstPrivateKey) {
		t.Fatal("embedded runtime generated a new private key instead of replaying the durable pending enrollment")
	}
	pending, err := secondRuntime.TunnelCredentialStore().PendingEnrollments()
	if err != nil {
		t.Fatalf("PendingEnrollments() error = %v", err)
	}
	if len(pending) != 1 || pending[0].Request.RequestID != first.Request.RequestID {
		t.Fatalf("pending enrollment replay set = %+v, want only request %q", pending, first.Request.RequestID)
	}
}

func TestEmbeddedCredentialFacadeExposesOnlySerializationSafeMetadata(t *testing.T) {
	storeType := reflect.TypeOf((*CredentialStore)(nil))
	storeValueType := storeType.Elem()
	for index := 0; index < storeValueType.NumField(); index++ {
		if storeValueType.Field(index).IsExported() {
			t.Fatalf("embedded credential facade exports field %s", storeValueType.Field(index).Name)
		}
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	expectedReturns := map[string][]reflect.Type{
		"ActivateCredential":             {reflect.TypeOf(PKICredentialMetadata{}), errorType},
		"ActivateRegistrationCredential": {reflect.TypeOf(PKICredentialMetadata{}), errorType},
		"ActivateStagedRegistration":     {reflect.TypeOf(PKICredentialMetadata{}), errorType},
		"ApplySecuritySnapshot":          {reflect.TypeOf(PKISecurityState{}), errorType},
		"LoadActiveCredential":           {reflect.TypeOf(PKICredentialMetadata{}), errorType},
		"LoadPending":                    {reflect.TypeOf(PKIPendingEnrollment{}), errorType},
		"LoadSecuritySnapshot":           {reflect.TypeOf(PKISecurityState{}), errorType},
		"PendingEnrollments":             {reflect.TypeOf([]PKIPendingEnrollment(nil)), errorType},
		"PrepareEnrollment":              {reflect.TypeOf(PKIPendingEnrollment{}), errorType},
		"RejectPendingEnrollment":        {errorType},
		"SecurityAcknowledgement":        {reflect.TypeOf(PKISecurityAcknowledgement{}), errorType},
	}
	if storeType.NumMethod() != len(expectedReturns) {
		t.Fatalf("embedded credential facade method count = %d, want exactly %d public-only methods", storeType.NumMethod(), len(expectedReturns))
	}
	for methodName, outputTypes := range expectedReturns {
		method, ok := storeType.MethodByName(methodName)
		if !ok {
			t.Fatalf("embedded credential facade is missing %s", methodName)
		}
		if method.Type.NumOut() != len(outputTypes) {
			t.Fatalf("%s outputs = %v, want %v", methodName, method.Type, outputTypes)
		}
		for index, outputType := range outputTypes {
			if method.Type.Out(index) != outputType {
				t.Fatalf("%s outputs = %v, want %v", methodName, method.Type, outputTypes)
			}
		}
	}
	assertExactEmbeddedFields(t, reflect.TypeOf(PKICredentialMetadata{}), []string{"Manifest", "Security"})
	assertExactEmbeddedFields(t, reflect.TypeOf(PKICredentialManifest{}), []string{
		"Version", "Generation", "RequestID", "RequestFingerprint", "Credential", "PKIDomainID", "PKIEpoch",
		"SecurityRevision", "SecuritySnapshotHash", "Expectation", "ActivatedAt",
	})

	delegate, err := modulepki.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	facade := &CredentialStore{delegate: delegate}
	pending, err := facade.PrepareEnrollment(context.Background(), PKIEnrollmentSpec{
		StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	privatePEM, err := os.ReadFile(filepath.Join(delegate.Root(), "identities", "agent", "pending", "private-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	encodedStore, err := json.Marshal(facade)
	if err != nil {
		t.Fatal(err)
	}
	if string(encodedStore) != "{}" {
		t.Fatalf("embedded credential facade JSON = %s, want {}", encodedStore)
	}
	for label, value := range map[string]any{
		"facade":   facade,
		"pending":  pending,
		"metadata": PKICredentialMetadata{},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), "PrivateKey") ||
			strings.Contains(string(encoded), "TLSCertificate") || strings.Contains(string(encoded), base64.StdEncoding.EncodeToString(privatePEM)) {
			t.Fatalf("embedded %s serialization contains private-key material: %s", label, encoded)
		}
	}
}

func assertExactEmbeddedFields(t *testing.T, value reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.IsExported() {
			actual = append(actual, field.Name)
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s exported fields = %v, want exact safe schema %v", value, actual, expected)
	}
}

type pkiTestSource struct{}

func (pkiTestSource) Sync(context.Context, SyncRequest) (Snapshot, error) { return Snapshot{}, nil }

type pkiTestSink struct{}

func (pkiTestSink) Save(context.Context, RuntimeState) error { return nil }

type pkiTestApp struct{}

func (pkiTestApp) Run(context.Context) error     { return nil }
func (pkiTestApp) SyncNow(context.Context) error { return nil }
func (pkiTestApp) Close() error                  { return nil }
