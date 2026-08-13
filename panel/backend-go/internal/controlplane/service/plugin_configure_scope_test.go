package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestAuthorizePluginConfigureScopeAllowsInGroupWriterSave(t *testing.T) {
	actor := authz.Actor{
		ID:                    "operator",
		Permissions:           []string{authz.PermissionResourceWrite},
		VisibleResourceGroups: []string{"group-a"},
	}
	request := PluginConfigureRequest{ResourceGroupID: "group-a", ActorID: actor.ID, Actor: actor}
	instance := storage.PluginInstanceRow{ID: "instance-a", ResourceGroupID: "group-a"}
	if err := authorizePluginConfigureScope(request, true, instance); err != nil {
		t.Fatalf("in-group writer save = %v", err)
	}
}

func TestAuthorizePluginConfigureScopeRejectsCreateAndCrossGroup(t *testing.T) {
	actor := authz.Actor{
		ID:                    "operator",
		Permissions:           []string{authz.PermissionResourceWrite},
		VisibleResourceGroups: []string{"group-a"},
	}
	create := PluginConfigureRequest{ResourceGroupID: "group-a", ActorID: actor.ID, Actor: actor}
	if err := authorizePluginConfigureScope(create, false, storage.PluginInstanceRow{}); err == nil {
		t.Fatal("writer create was allowed")
	}
	cross := PluginConfigureRequest{ResourceGroupID: "group-b", ActorID: actor.ID, Actor: actor}
	instance := storage.PluginInstanceRow{ID: "instance-a", ResourceGroupID: "group-a"}
	if err := authorizePluginConfigureScope(cross, true, instance); err == nil {
		t.Fatal("cross-group retarget was allowed")
	}
	hidden := PluginConfigureRequest{ResourceGroupID: "group-b", ActorID: actor.ID, Actor: actor}
	if err := authorizePluginConfigureScope(hidden, true, storage.PluginInstanceRow{ID: "hidden", ResourceGroupID: "group-b"}); err == nil {
		t.Fatal("hidden-group save was allowed")
	}
}

func TestAuthorizePluginConfigureScopeAllowsAdminCreate(t *testing.T) {
	actor := authz.Actor{ID: "admin", Permissions: []string{authz.PermissionAll}}
	request := PluginConfigureRequest{ResourceGroupID: "group-b", ActorID: actor.ID, Actor: actor}
	if err := authorizePluginConfigureScope(request, false, storage.PluginInstanceRow{}); err != nil {
		t.Fatalf("admin create = %v", err)
	}
}
