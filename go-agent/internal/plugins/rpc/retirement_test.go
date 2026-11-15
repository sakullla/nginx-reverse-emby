package rpc

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestHostRetirementFencesRestartsAndNetworkAdmission(t *testing.T) {
	manager := managed.NewManager()
	defer manager.Close()
	authority := managed.Authority{InstanceID: "ss", Generation: "old", Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}, Admit: func(context.Context, sdk.ManagedSourceMetadata) error { return nil }}
	owner, err := manager.Stage(authority)
	if err != nil {
		t.Fatal(err)
	}
	owner.Activate()
	restartCtx, cancelRestart := context.WithCancel(t.Context())
	defer cancelRestart()
	instance := &HostedInstance{restartCancel: cancelRestart}
	host := &Host{managed: manager, instances: map[*HostedInstance]struct{}{instance: {}}}
	host.RetireForReplacement()
	if !instance.retired || restartCtx.Err() == nil {
		t.Fatal("retired plugin can restart in the old process")
	}
	if _, err := manager.Stage(authority); err == nil {
		t.Fatal("late process attempt created a network owner")
	}
	if _, err := owner.Handle(t.Context(), sdk.ManagedNetworkRequest{
		Action: sdk.ManagedNetworkListen, Binding: owner.Binding(), RequestID: "retirement-probe", Protocol: "tcp",
		Endpoint: &sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: 26966}, MaxFlows: 4, IdleMS: 30000,
	}); err == nil {
		t.Fatal("retired owner reopened its listener")
	}
	host.RetireForReplacement()
}
