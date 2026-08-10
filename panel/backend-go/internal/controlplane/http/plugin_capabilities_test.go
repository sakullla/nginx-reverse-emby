package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type pluginCapabilityAPIFake struct {
	request service.PluginDynamicActionRequest
}

type crossGroupCapabilityStore struct{}

func (crossGroupCapabilityStore) ConsumeQuotaForResource(context.Context, string, string, string, string, string, int64) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}
func (crossGroupCapabilityStore) AppendAuditEvent(context.Context, storage.AuditEventRow) error {
	return nil
}
func (crossGroupCapabilityStore) GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error) {
	return storage.InstalledPluginRow{}, false, nil
}
func (crossGroupCapabilityStore) GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error) {
	return storage.PluginPackageRow{}, false, nil
}
func (crossGroupCapabilityStore) ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error) {
	return nil, nil
}
func (crossGroupCapabilityStore) GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error) {
	return storage.PluginInstanceRow{ID: "instance-1", PluginID: "official.service", ResourceGroupID: "group-a", DesiredEnabled: true}, true, nil
}
func (crossGroupCapabilityStore) PluginCapabilityTargetBinding(_ context.Context, kind, id string) (storage.PluginCapabilityTargetBinding, bool, error) {
	return storage.PluginCapabilityTargetBinding{Kind: kind, ID: id, ResourceGroupID: "group-b", Version: strings.Repeat("a", 64)}, true, nil
}
func (crossGroupCapabilityStore) ExecutePluginCapabilityResourceCall(context.Context, storage.PluginCapabilityTargetBinding, pluginsdk.RPCResourceCall) ([]byte, error) {
	return nil, nil
}
func (crossGroupCapabilityStore) ClaimPluginCapabilityOperation(_ context.Context, scope, key, fingerprint, operationID, claimToken string, now, expires time.Time) (storage.IdempotencyRecordRow, bool, error) {
	return storage.IdempotencyRecordRow{Scope: scope, Key: key, RequestFingerprint: fingerprint, OperationID: operationID, ResponseJSON: `{"status":"pending"}`, CreatedAt: now, ExpiresAt: expires}, true, nil
}
func (crossGroupCapabilityStore) RenewPluginCapabilityOperation(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (crossGroupCapabilityStore) CompletePluginCapabilityOperation(context.Context, string, string, string, string, string) error {
	return nil
}

type crossGroupCapabilityAuthorizer struct{}

func (crossGroupCapabilityAuthorizer) AuthorizeResource(context.Context, authz.Actor, string, string, string) error {
	return nil
}

type crossGroupCapabilityRuntime struct{ calls int }

func (*crossGroupCapabilityRuntime) ActiveGeneration(string) (string, bool) {
	return "generation-1", true
}
func (*crossGroupCapabilityRuntime) PlanAction(context.Context, string, string, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	return pluginsdk.RPCActionPlanResponse{}, nil
}
func (runtime *crossGroupCapabilityRuntime) InvokeAction(context.Context, string, string, pluginsdk.RPCActionRequest) error {
	runtime.calls++
	return nil
}
func (*crossGroupCapabilityRuntime) QueryAction(_ context.Context, _, _ string, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	return pluginsdk.RPCActionResponse{OperationID: request.OperationID, Missing: true}, nil
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

func TestPluginCapabilityHTTPRejectsCrossGroupSpoofBeforeGuestDispatch(t *testing.T) {
	runtime := &crossGroupCapabilityRuntime{}
	manager, err := service.NewPluginCapabilityManager(crossGroupCapabilityStore{}, crossGroupCapabilityAuthorizer{}, runtime, &service.PluginService{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.service/instances/instance-1/actions/rotate", bytes.NewBufferString(`{"operation_id":"action-op-1","target_kind":"relay","target_id":"relay-1","resource_group_id":"group-a"}`))
	request.SetPathValue("id", "official.service")
	request.SetPathValue("instance", "instance-1")
	request.SetPathValue("action", "rotate")
	actor := authz.Actor{ID: "operator", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{"group-a", "group-b"}}
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor))
	response := httptest.NewRecorder()
	Dependencies{PluginCapabilityService: manager}.handlePluginDynamicAction(response, request)
	if response.Code < 400 || runtime.calls != 0 {
		t.Fatalf("cross-group status=%d calls=%d body=%s", response.Code, runtime.calls, response.Body.String())
	}
}
