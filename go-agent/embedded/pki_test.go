package embedded

import (
	"context"
	"encoding/json"
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
	if _, exposed := storeType.MethodByName("Root"); exposed {
		t.Fatal("embedded credential facade exposes the private credential filesystem root")
	}
	if _, exposed := storeType.MethodByName("InstallTLSCertificate"); exposed {
		t.Fatal("embedded credential facade exposes the execution-plane TLS installation operation")
	}
	metadataType := reflect.TypeOf(PKICredentialMetadata{})
	for _, methodName := range []string{"ActivateCredential", "ActivateStagedRegistration", "LoadActiveCredential"} {
		method, ok := storeType.MethodByName(methodName)
		if !ok {
			t.Fatalf("embedded credential facade is missing %s", methodName)
		}
		if method.Type.NumOut() != 2 || method.Type.Out(0) != metadataType {
			t.Fatalf("%s returns %v, want PKICredentialMetadata", methodName, method.Type.Out(0))
		}
	}
	encoded, err := json.Marshal(PKICredentialMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), "PrivateKey") || strings.Contains(string(encoded), "TLSCertificate") {
		t.Fatalf("embedded metadata serialization contains a key-bearing field: %s", encoded)
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
