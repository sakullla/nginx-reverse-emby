package embedded

import (
	"context"
	"path/filepath"
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
	if embeddedStore.Root() != wantEmbeddedRoot {
		t.Fatalf("embedded PKI root = %s, want %s", embeddedStore.Root(), wantEmbeddedRoot)
	}

	remoteStore, err := modulepki.NewStore(dataRoot)
	if err != nil {
		t.Fatalf("NewStore(remote) error = %v", err)
	}
	if remoteStore.Root() == embeddedStore.Root() {
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

type pkiTestSource struct{}

func (pkiTestSource) Sync(context.Context, SyncRequest) (Snapshot, error) { return Snapshot{}, nil }

type pkiTestSink struct{}

func (pkiTestSink) Save(context.Context, RuntimeState) error { return nil }

type pkiTestApp struct{}

func (pkiTestApp) Run(context.Context) error     { return nil }
func (pkiTestApp) SyncNow(context.Context) error { return nil }
func (pkiTestApp) Close() error                  { return nil }
