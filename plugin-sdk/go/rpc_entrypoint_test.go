package pluginsdk

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type entrypointLifecycleFixture struct {
	request RPCHandshakeRequest
}

func (fixture *entrypointLifecycleFixture) Handshake(_ context.Context, request RPCHandshakeRequest) (RPCHandshakeResponse, error) {
	fixture.request = request
	return RPCHandshakeResponse{ABI: RPCABIV1, Features: append([]string(nil), request.RequiredFeatures...)}, nil
}

func (*entrypointLifecycleFixture) Prepare(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{}
}

func (*entrypointLifecycleFixture) Activate(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{}
}

func (*entrypointLifecycleFixture) Stop(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{}
}

func TestRunRPCEntrypointOwnsCanonicalProbe(t *testing.T) {
	fixture := &entrypointLifecycleFixture{}
	var output bytes.Buffer
	err := RunRPCEntrypoint(t.Context(), []string{RPCHandshakeProbeFlag, "manifest-id", "2.0.0"}, &output, RPCEntrypointConfig{
		Declaration: RPCPluginDeclaration{
			PluginID: "example", PluginVersion: "1.0.0",
			RequiredCapabilities: []string{"resource.read"}, SupportedFeatures: []string{RPCFeatureDurableActionsV1},
		},
		NewProbeLifecycle: func(RPCHandshakeRequest) (RPCLifecycle, error) { return fixture, nil },
		Run:               func(context.Context) error { return errors.New("runtime must not start") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != RPCABIV1 || fixture.request.PluginID != "manifest-id" || fixture.request.PluginVersion != "2.0.0" {
		t.Fatalf("probe output=%q request=%#v", output.String(), fixture.request)
	}
	if fixture.request.PackageDigest != rpcProbePackageDigest || fixture.request.ArtifactDigest != rpcProbeArtifactDigest ||
		len(fixture.request.GrantedScopes) != 1 || len(fixture.request.RequiredFeatures) != 1 {
		t.Fatalf("probe request is not canonical: %#v", fixture.request)
	}
}

func TestRunRPCEntrypointRunsOnlyArgumentFreeRuntime(t *testing.T) {
	want := errors.New("runtime stopped")
	config := RPCEntrypointConfig{
		Declaration:       RPCPluginDeclaration{PluginID: "example", PluginVersion: "1.0.0"},
		NewProbeLifecycle: func(RPCHandshakeRequest) (RPCLifecycle, error) { return &entrypointLifecycleFixture{}, nil },
		Run:               func(context.Context) error { return want },
	}
	if err := RunRPCEntrypoint(t.Context(), nil, &bytes.Buffer{}, config); !errors.Is(err, want) {
		t.Fatalf("runtime error = %v", err)
	}
	if err := RunRPCEntrypoint(t.Context(), []string{"extra"}, &bytes.Buffer{}, config); err == nil || !strings.Contains(err.Error(), "unexpected example arguments") {
		t.Fatalf("unexpected argument error = %v", err)
	}
}

func TestRunRPCPluginServicesCancelsSiblingsAndJoinsResults(t *testing.T) {
	want := errors.New("first failed")
	canceled := make(chan struct{})
	err := runRPCPluginServices(t.Context(), []func(context.Context) error{
		func(context.Context) error { return want },
		func(ctx context.Context) error {
			<-ctx.Done()
			close(canceled)
			return nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("service result = %v", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("sibling service was not canceled")
	}
}
