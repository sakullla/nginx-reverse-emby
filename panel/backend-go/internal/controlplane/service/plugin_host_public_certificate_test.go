//go:build !integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type publicCertificateHostStore struct {
	binding    storage.PluginCapabilityTargetBinding
	found      bool
	bindingErr error
	rows       []storage.PublicCertificateProjection
	listErr    error
}

func (s *publicCertificateHostStore) ConsumeQuotaForResource(context.Context, string, string, string, string, string, int64) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

func (s *publicCertificateHostStore) AppendAuditEvent(context.Context, storage.AuditEventRow) error {
	return nil
}

func (s *publicCertificateHostStore) GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error) {
	return storage.InstalledPluginRow{}, false, nil
}

func (s *publicCertificateHostStore) GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error) {
	return storage.PluginPackageRow{}, false, nil
}

func (s *publicCertificateHostStore) ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error) {
	return nil, nil
}

func (s *publicCertificateHostStore) GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error) {
	return storage.PluginInstanceRow{}, false, nil
}

func (s *publicCertificateHostStore) PluginCapabilityTargetBinding(context.Context, string, string) (storage.PluginCapabilityTargetBinding, bool, error) {
	if s.bindingErr != nil {
		return storage.PluginCapabilityTargetBinding{}, false, s.bindingErr
	}
	return s.binding, s.found, nil
}

func (s *publicCertificateHostStore) ExecutePluginCapabilityResourceCall(context.Context, storage.PluginCapabilityTargetBinding, pluginsdk.RPCResourceCall) ([]byte, error) {
	return nil, errors.New("unexpected resource call")
}

func (s *publicCertificateHostStore) ClaimPluginCapabilityOperation(context.Context, string, string, string, string, string, time.Time, time.Time) (storage.IdempotencyRecordRow, bool, error) {
	return storage.IdempotencyRecordRow{}, false, errors.New("unexpected operation claim")
}

func (s *publicCertificateHostStore) RenewPluginCapabilityOperation(context.Context, string, string, string, string, time.Time) error {
	return errors.New("unexpected operation renew")
}

func (s *publicCertificateHostStore) CompletePluginCapabilityOperation(context.Context, string, string, string, string, string) error {
	return errors.New("unexpected operation complete")
}

func (s *publicCertificateHostStore) GetIdempotencyRecord(context.Context, string, string) (storage.IdempotencyRecordRow, bool, error) {
	return storage.IdempotencyRecordRow{}, false, nil
}

func (s *publicCertificateHostStore) ListPublicCertificateProjections(context.Context, string) ([]storage.PublicCertificateProjection, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]storage.PublicCertificateProjection(nil), s.rows...), nil
}

func publicCertificateHostCandidate(grants ...string) pluginhost.Candidate {
	return pluginhost.Candidate{
		InstanceID:      "anytls-1",
		ResourceGroupID: "group-a",
		Identity:        pluginhost.Identity{PluginID: "plugin-anytls", Generation: "gen-1"},
		Grants:          grants,
	}
}

func dispatchPublicCertificate(t *testing.T, manager *PluginCapabilityManager, candidate pluginhost.Candidate, operation string, payload []byte) pluginsdk.HostRuntimeResponse {
	t.Helper()
	return manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: operation, Payload: payload})
}

func TestPluginHostPublicCertificateProjection(t *testing.T) {
	store := &publicCertificateHostStore{
		binding: storage.PluginCapabilityTargetBinding{Kind: "agent", ID: "edge-a", ResourceGroupID: "group-a", Version: "v1"},
		found:   true,
		rows: []storage.PublicCertificateProjection{
			{ID: 2, Domain: "one.example", Enabled: true, MaterialAvailable: true},
			{ID: 1, Domain: "two.example", Enabled: false, MaterialAvailable: false},
		},
	}
	manager := &PluginCapabilityManager{store: store}
	candidate := publicCertificateHostCandidate(pluginsdk.PermissionPublicCertificateRead)
	response := dispatchPublicCertificate(t, manager, candidate, pluginsdk.HostRuntimePublicCertificate, []byte(`{"agent_id":"edge-a"}`))
	if response.Error != nil {
		t.Fatalf("projection error = %+v", response.Error)
	}
	assertPublicCertificatePayload(t, response.Payload)
	var listed pluginsdk.PublicCertificateListResponse
	if err := json.Unmarshal(response.Payload, &listed); err != nil {
		t.Fatal(err)
	}
	if err := listed.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(listed.Certificates) != 2 || listed.Certificates[0].ID != 2 || listed.Certificates[1].ID != 1 {
		t.Fatalf("certificates = %+v", listed.Certificates)
	}
	if !listed.Certificates[0].Enabled || !listed.Certificates[0].MaterialAvailable || listed.Certificates[0].Domain != "one.example" {
		t.Fatalf("available certificate = %+v", listed.Certificates[0])
	}
	if listed.Certificates[1].Enabled || listed.Certificates[1].MaterialAvailable || listed.Certificates[1].Domain != "two.example" {
		t.Fatalf("disabled certificate = %+v", listed.Certificates[1])
	}
}

func TestPluginHostPublicCertificateErrorsHideMaterial(t *testing.T) {
	secret := "-----BEGIN PRIVATE KEY-----\nSUPER-PRIVATE-KEY-MATERIAL"
	store := &publicCertificateHostStore{
		binding: storage.PluginCapabilityTargetBinding{Kind: "agent", ID: "edge-a", ResourceGroupID: "group-a"},
		found:   true,
		listErr: errors.New("load failed: " + secret),
	}
	manager := &PluginCapabilityManager{store: store}
	candidate := publicCertificateHostCandidate(pluginsdk.PermissionPublicCertificateRead)
	response := dispatchPublicCertificate(t, manager, candidate, pluginsdk.HostRuntimePublicCertificate, []byte(`{"agent_id":"edge-a"}`))
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorUnavailable || response.Error.Message != "host resource operation failed" || len(response.Payload) != 0 {
		t.Fatalf("lister error = %+v %s", response.Error, response.Payload)
	}
	assertNoHostCertificateMaterial(t, response.Error.Message)

	mismatched := &publicCertificateHostStore{
		binding: storage.PluginCapabilityTargetBinding{Kind: "agent", ID: "edge-a", ResourceGroupID: "other-group"},
		found:   true,
	}
	denied := dispatchPublicCertificate(t, &PluginCapabilityManager{store: mismatched}, candidate, pluginsdk.HostRuntimePublicCertificate, []byte(`{"agent_id":"edge-a"}`))
	if denied.Error == nil || denied.Error.Code != pluginsdk.ErrorPermissionDenied || denied.Error.Message != "host resource operation failed" {
		t.Fatalf("resource group = %+v", denied.Error)
	}

	missing := dispatchPublicCertificate(t, manager, publicCertificateHostCandidate(), pluginsdk.HostRuntimePublicCertificate, []byte(`{"agent_id":"edge-a"}`))
	if missing.Error == nil || missing.Error.Code != pluginsdk.ErrorPermissionDenied || missing.Error.Message != "host resource permission was not granted" {
		t.Fatalf("missing grant = %+v", missing.Error)
	}

	pemPayload := []byte(`{"agent_id":"edge-a","cert_pem":"` + secret + `"}`)
	invalid := dispatchPublicCertificate(t, manager, candidate, pluginsdk.HostRuntimePublicCertificate, pemPayload)
	if invalid.Error == nil || invalid.Error.Code != pluginsdk.ErrorInvalidArgument || invalid.Error.Message != "host resource operation failed" || len(invalid.Payload) != 0 {
		t.Fatalf("extra field = %+v %s", invalid.Error, invalid.Payload)
	}
	assertNoHostCertificateMaterial(t, invalid.Error.Message)

	tlsPayload := []byte(`{"certificate_id":1,"key_pem":"` + secret + `"}`)
	terminated := dispatchPublicCertificate(t, manager, publicCertificateHostCandidate(pluginsdk.PermissionPublicCertificateTerminate), pluginsdk.HostRuntimePublicCertificateTLS, tlsPayload)
	if terminated.Error == nil || terminated.Error.Code != pluginsdk.ErrorInvalidArgument || terminated.Error.Message != "host resource operation is unsupported" || len(terminated.Payload) != 0 {
		t.Fatalf("control-plane tls = %+v %s", terminated.Error, terminated.Payload)
	}
	assertNoHostCertificateMaterial(t, terminated.Error.Message)
	if strings.Contains(terminated.Error.Message, "SUPER") || strings.Contains(terminated.Error.Message, secret) {
		t.Fatal(terminated.Error.Message)
	}
}

func assertPublicCertificatePayload(t *testing.T, payload []byte) {
	t.Helper()
	assertNoHostCertificateMaterial(t, string(payload))
	for _, hidden := range []string{"cert_pem", "key_pem", "PRIVATE"} {
		if strings.Contains(string(payload), hidden) {
			t.Fatalf("projection leaked %s: %s", hidden, payload)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var envelope struct {
		Certificates []json.RawMessage `json:"certificates"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Certificates) != 2 {
		t.Fatalf("certificates = %s", payload)
	}
	for _, raw := range envelope.Certificates {
		itemDecoder := json.NewDecoder(bytes.NewReader(raw))
		itemDecoder.DisallowUnknownFields()
		var item struct {
			ID                int    `json:"id"`
			Domain            string `json:"domain"`
			Enabled           bool   `json:"enabled"`
			MaterialAvailable bool   `json:"material_available"`
		}
		if err := itemDecoder.Decode(&item); err != nil {
			t.Fatalf("certificate item %s: %v", raw, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"id", "domain", "enabled", "material_available"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("certificate item omitted %s: %s", key, raw)
			}
		}
		if len(fields) != 4 {
			t.Fatalf("certificate item fields = %s", raw)
		}
	}
	if !strings.Contains(string(payload), `"enabled":false`) || !strings.Contains(string(payload), `"material_available":false`) {
		t.Fatalf("false flags were omitted: %s", payload)
	}
}

func assertNoHostCertificateMaterial(t *testing.T, value string) {
	t.Helper()
	upper := strings.ToUpper(value)
	if strings.Contains(upper, "PRIVATE KEY") || strings.Contains(value, "BEGIN ") {
		t.Fatalf("host response carried certificate material: %s", value)
	}
}
