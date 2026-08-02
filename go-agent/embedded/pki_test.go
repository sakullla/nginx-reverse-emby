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

func TestEmbeddedCredentialFacadeExposesOnlySerializationSafeMetadata(t *testing.T) {
	storeType := reflect.TypeOf((*CredentialStore)(nil))
	storeValueType := storeType.Elem()
	for index := 0; index < storeValueType.NumField(); index++ {
		if storeValueType.Field(index).IsExported() {
			t.Fatalf("embedded credential facade exports field %s", storeValueType.Field(index).Name)
		}
	}
	expectedReturns := map[string]reflect.Type{
		"ActivateCredential":         reflect.TypeOf(PKICredentialMetadata{}),
		"ActivateStagedRegistration": reflect.TypeOf(PKICredentialMetadata{}),
		"ApplySecuritySnapshot":      reflect.TypeOf(PKISecurityState{}),
		"LoadActiveCredential":       reflect.TypeOf(PKICredentialMetadata{}),
		"LoadPending":                reflect.TypeOf(PKIPendingEnrollment{}),
		"LoadSecuritySnapshot":       reflect.TypeOf(PKISecurityState{}),
		"PendingEnrollments":         reflect.TypeOf([]PKIPendingEnrollment(nil)),
		"PrepareEnrollment":          reflect.TypeOf(PKIPendingEnrollment{}),
		"SecurityAcknowledgement":    reflect.TypeOf(PKISecurityAcknowledgement{}),
	}
	if storeType.NumMethod() != len(expectedReturns) {
		t.Fatalf("embedded credential facade method count = %d, want exactly %d public-only methods", storeType.NumMethod(), len(expectedReturns))
	}
	for methodName, outputType := range expectedReturns {
		method, ok := storeType.MethodByName(methodName)
		if !ok {
			t.Fatalf("embedded credential facade is missing %s", methodName)
		}
		if method.Type.NumOut() != 2 || method.Type.Out(0) != outputType || !method.Type.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			t.Fatalf("%s outputs = %v, want (%v, error)", methodName, method.Type, outputType)
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
