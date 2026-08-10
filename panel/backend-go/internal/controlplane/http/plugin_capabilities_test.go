package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type pluginCapabilityAPIFake struct {
	request service.PluginDynamicActionRequest
}

func (api *pluginCapabilityAPIFake) InvokeDynamicAction(_ context.Context, request service.PluginDynamicActionRequest) (service.PluginDynamicActionResult, error) {
	api.request = request
	return service.PluginDynamicActionResult{OperationID: request.OperationID}, nil
}

func TestPluginCapabilityHTTPDispatchesDeclarativeActionWithAuthenticatedActor(t *testing.T) {
	api := &pluginCapabilityAPIFake{}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.service/instances/instance-1/actions/rotate", bytes.NewBufferString(`{"operation_id":"action-op-1","target_kind":"relay","target_id":"relay-1","resource_group_id":"group-a"}`))
	request.SetPathValue("id", "official.service")
	request.SetPathValue("instance", "instance-1")
	request.SetPathValue("action", "rotate")
	actor := authz.Actor{ID: "operator", VisibleResourceGroups: []string{"group-a"}}
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor))
	response := httptest.NewRecorder()
	Dependencies{PluginCapabilityService: api}.handlePluginDynamicAction(response, request)
	if response.Code != http.StatusOK || api.request.OperationID != "action-op-1" || api.request.Actor.ID != actor.ID || api.request.PluginID != "official.service" || api.request.InstanceID != "instance-1" || api.request.ActionID != "rotate" || api.request.Target.ID != "relay-1" || api.request.Target.ResourceGroupID != "group-a" {
		t.Fatalf("HTTP action status=%d request=%+v body=%s", response.Code, api.request, response.Body.String())
	}
}

func TestPluginCapabilityHTTPRejectsMissingActorAndUnknownFields(t *testing.T) {
	api := &pluginCapabilityAPIFake{}
	for name, request := range map[string]*http.Request{
		"missing actor": httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"target_kind":"relay","target_id":"relay-1","resource_group_id":"group-a"}`)),
		"unknown field": httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"target_kind":"relay","target_id":"relay-1","resource_group_id":"group-a","script":"alert(1)"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			if name == "unknown field" {
				request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, authz.Actor{ID: "operator"}))
			}
			response := httptest.NewRecorder()
			Dependencies{PluginCapabilityService: api}.handlePluginDynamicAction(response, request)
			if response.Code < 400 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
