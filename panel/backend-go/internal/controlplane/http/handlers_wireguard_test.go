package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	httpTestWireGuardPrivateKey   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	httpTestWireGuardPublicKey    = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	httpTestWireGuardPresharedKey = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="
	httpTestWireGuardRedacted     = "xxxxx"
)

func TestWireGuardURIParsePreviewRedactsSecretsAndRequiresPanelToken(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for _, prefix := range []string{"/api", "/panel-api"} {
		req := httptest.NewRequest(http.MethodPost, prefix+"/wireguard/parse-uri", bytes.NewBufferString(`{"uri":"`+validWireGuardHTTPURI()+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s/wireguard/parse-uri without token = %d, body=%s", prefix, resp.Code, resp.Body.String())
		}

		req = httptest.NewRequest(http.MethodPost, prefix+"/wireguard/parse-uri", bytes.NewBufferString(`{"uri":"`+validWireGuardHTTPURI()+`"}`))
		req.Header.Set("X-Panel-Token", "secret")
		req.Header.Set("Content-Type", "application/json")
		resp = httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("POST %s/wireguard/parse-uri = %d, body=%s", prefix, resp.Code, resp.Body.String())
		}
		assertWireGuardHTTPBodyDoesNotLeakURISecrets(t, resp.Body.String())

		var payload struct {
			OK      bool   `json:"ok"`
			URI     string `json:"uri"`
			Profile struct {
				Name       string   `json:"name"`
				Endpoint   string   `json:"endpoint"`
				PublicKey  string   `json:"public_key"`
				Addresses  []string `json:"addresses"`
				AllowedIPs []string `json:"allowed_ips"`
				DNS        []string `json:"dns"`
				MTU        int      `json:"mtu"`
			} `json:"profile"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if !payload.OK {
			t.Fatalf("ok = false in %s", resp.Body.String())
		}
		if payload.URI == "" || payload.Profile.PublicKey != httpTestWireGuardPublicKey || payload.Profile.Endpoint != "peer.example.com:51820" {
			t.Fatalf("payload = %+v", payload)
		}
		if payload.Profile.Name != "phone" || payload.Profile.MTU != 1420 {
			t.Fatalf("profile preview = %+v", payload.Profile)
		}
		if len(payload.Profile.Addresses) != 1 || payload.Profile.Addresses[0] != "10.44.0.2/32" {
			t.Fatalf("addresses = %+v", payload.Profile.Addresses)
		}
		if len(payload.Profile.AllowedIPs) != 2 || payload.Profile.AllowedIPs[0] != "0.0.0.0/0" || payload.Profile.AllowedIPs[1] != "::/0" {
			t.Fatalf("allowed_ips = %+v", payload.Profile.AllowedIPs)
		}
		if len(payload.Profile.DNS) != 2 || payload.Profile.DNS[0] != "1.1.1.1" || payload.Profile.DNS[1] != "2606:4700:4700::1111" {
			t.Fatalf("dns = %+v", payload.Profile.DNS)
		}
	}
}

func TestWireGuardURIImportCreatesRedactedProfileAndRejectsReserved(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles/import-uri", bytes.NewBufferString(`{
		"uri":"`+validWireGuardHTTPURI()+`",
		"name":"fallback-name"
	}`))
	req.Header.Set("X-Panel-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST import-uri = %d, body=%s", resp.Code, resp.Body.String())
	}
	assertWireGuardHTTPBodyDoesNotLeakURISecrets(t, resp.Body.String())
	profile := decodeWireGuardHTTPProfileResponse(t, resp.Body.Bytes(), "profile")
	assertWireGuardHTTPSecretsRedacted(t, profile)
	if profile.Name != "phone" || profile.Mode != "generic_wireguard" || profile.ListenPort != 0 || !profile.Enabled {
		t.Fatalf("profile core fields = %+v", profile)
	}
	if len(profile.Addresses) != 1 || profile.Addresses[0] != "10.44.0.2/32" {
		t.Fatalf("addresses = %+v", profile.Addresses)
	}
	if len(profile.Peers) != 1 || profile.Peers[0].PublicKey != httpTestWireGuardPublicKey || profile.Peers[0].Endpoint != "peer.example.com:51820" {
		t.Fatalf("peers = %+v", profile.Peers)
	}
	if len(profile.Peers[0].AllowedIPs) != 2 || profile.Peers[0].AllowedIPs[1] != "::/0" {
		t.Fatalf("allowed_ips = %+v", profile.Peers[0].AllowedIPs)
	}
	if len(profile.DNS) != 2 || profile.DNS[1] != "2606:4700:4700::1111" || profile.MTU != 1420 {
		t.Fatalf("dns/mtu = %+v/%d", profile.DNS, profile.MTU)
	}

	reservedReq := httptest.NewRequest(http.MethodPost, "/api/agents/local/wireguard-profiles/import-uri", bytes.NewBufferString(`{"uri":"`+validWireGuardHTTPURIWithReserved()+`"}`))
	reservedReq.Header.Set("X-Panel-Token", "secret")
	reservedReq.Header.Set("Content-Type", "application/json")
	reservedResp := httptest.NewRecorder()
	router.ServeHTTP(reservedResp, reservedReq)
	if reservedResp.Code != http.StatusBadRequest {
		t.Fatalf("POST import-uri reserved = %d, body=%s", reservedResp.Code, reservedResp.Body.String())
	}
}

func TestRouterWireGuardProfilesCreateAndListRedactsSecrets(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for i, prefix := range []string{"/api", "/panel-api"} {
		createReq := httptest.NewRequest(http.MethodPost, prefix+"/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPPayloadWithPort(51820+i)))
		createReq.Header.Set("X-Panel-Token", "secret")
		createReq.Header.Set("Content-Type", "application/json")
		createResp := httptest.NewRecorder()
		router.ServeHTTP(createResp, createReq)
		if createResp.Code != http.StatusCreated {
			t.Fatalf("POST %s/agents/local/wireguard-profiles = %d, body=%s", prefix, createResp.Code, createResp.Body.String())
		}
		created := decodeWireGuardHTTPProfileResponse(t, createResp.Body.Bytes(), "profile")
		assertWireGuardHTTPSecretsRedacted(t, created)

		getReq := httptest.NewRequest(http.MethodGet, prefix+"/agents/local/wireguard-profiles", nil)
		getReq.Header.Set("X-Panel-Token", "secret")
		getResp := httptest.NewRecorder()
		router.ServeHTTP(getResp, getReq)
		if getResp.Code != http.StatusOK {
			t.Fatalf("GET %s/agents/local/wireguard-profiles = %d, body=%s", prefix, getResp.Code, getResp.Body.String())
		}
		profiles := decodeWireGuardHTTPProfilesResponse(t, getResp.Body.Bytes())
		if len(profiles) == 0 {
			t.Fatalf("GET %s returned no profiles", prefix)
		}
		assertWireGuardHTTPSecretsRedacted(t, profiles[len(profiles)-1])
	}
}

func TestRouterWireGuardProfilesRejectsInvalidCIDR(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", bytes.NewBufferString(`{
		"name":"bad cidr",
		"mode":"generic_wireguard",
		"private_key":"`+httpTestWireGuardPrivateKey+`",
		"listen_port":51820,
		"addresses":["10.20.0.1"],
		"peers":[{"name":"peer-a","public_key":"`+httpTestWireGuardPublicKey+`","preshared_key":"`+httpTestWireGuardPresharedKey+`","allowed_ips":["10.20.0.2/32"]}]
	}`))
	req.Header.Set("X-Panel-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid CIDR = %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestRouterWireGuardProfilesDeleteRouteWorks(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPPayload()))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/wireguard-profiles = %d, body=%s", createResp.Code, createResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/wireguard-profiles/1", nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/wireguard-profiles/1 = %d, body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	deleted := decodeWireGuardHTTPProfileResponse(t, deleteResp.Body.Bytes(), "profile")
	assertWireGuardHTTPSecretsRedacted(t, deleted)
}

func TestRouterWireGuardProfilesUpdateClearsDNSAndTags(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPPayload()))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/wireguard-profiles = %d, body=%s", createResp.Code, createResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/wireguard-profiles/1", bytes.NewBufferString(`{
		"name":"wg-a",
		"mode":"generic_wireguard",
		"private_key":"`+httpTestWireGuardRedacted+`",
		"listen_port":51820,
		"addresses":["10.20.0.1/24"],
		"peers":[{"name":"peer-a","public_key":"`+httpTestWireGuardPublicKey+`","preshared_key":"`+httpTestWireGuardRedacted+`","endpoint":"peer.example.com:51820","allowed_ips":["10.20.0.2/32"],"persistent_keepalive_seconds":25}],
		"dns":[],
		"mtu":1420,
		"enabled":true,
		"tags":[]
	}`))
	updateReq.Header.Set("X-Panel-Token", "secret")
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/wireguard-profiles/1 = %d, body=%s", updateResp.Code, updateResp.Body.String())
	}
	updated := decodeWireGuardHTTPProfileResponse(t, updateResp.Body.Bytes(), "profile")
	if updated.DNS == nil || len(updated.DNS) != 0 {
		t.Fatalf("updated DNS = %+v, want explicit empty slice", updated.DNS)
	}
	if updated.Tags == nil || len(updated.Tags) != 0 {
		t.Fatalf("updated Tags = %+v, want explicit empty slice", updated.Tags)
	}
}

func TestRouterWireGuardProfilesMissingIDReturnsWireGuardNotFound(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for _, tc := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "update", method: http.MethodPut, body: validWireGuardHTTPPayload()},
		{name: "delete", method: http.MethodDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/panel-api/agents/local/wireguard-profiles/99", bytes.NewBufferString(tc.body))
			req.Header.Set("X-Panel-Token", "secret")
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("%s missing profile = %d, body=%s", tc.method, resp.Code, resp.Body.String())
			}

			var payload map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if payload["message"] != "wireguard profile not found" {
				t.Fatalf("payload = %+v", payload)
			}
		})
	}
}

func TestWireGuardProfileClientsRequirePanelTokenAndListInitiallyEmpty(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for i, prefix := range []string{"/api", "/panel-api"} {
		profile := createWireGuardHTTPClientProfile(t, router, prefix, 51820+i)

		req := httptest.NewRequest(http.MethodGet, prefix+"/agents/local/wireguard-profiles/"+strconv.Itoa(profile.ID)+"/clients", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s clients without token = %d, body=%s", prefix, resp.Code, resp.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, prefix+"/agents/local/wireguard-profiles/"+strconv.Itoa(profile.ID)+"/clients", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp = httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s clients = %d, body=%s", prefix, resp.Code, resp.Body.String())
		}
		clients := decodeWireGuardHTTPClientsResponse(t, resp.Body.Bytes())
		if len(clients) != 0 {
			t.Fatalf("clients = %+v, want empty", clients)
		}
	}
}

func TestWireGuardProfileClientRoutesRejectInvalidIDs(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "non-numeric profile id on collection",
			method: http.MethodPost,
			path:   "/panel-api/agents/local/wireguard-profiles/bad/clients",
			body:   `{"name":"phone"}`,
		},
		{
			name:   "zero profile id on collection",
			method: http.MethodPost,
			path:   "/panel-api/agents/local/wireguard-profiles/0/clients",
			body:   `{"name":"phone"}`,
		},
		{
			name:   "non-numeric client id on item",
			method: http.MethodDelete,
			path:   "/panel-api/agents/local/wireguard-profiles/1/clients/bad",
		},
		{
			name:   "non-numeric client id on config",
			method: http.MethodGet,
			path:   "/panel-api/agents/local/wireguard-profiles/1/clients/bad/config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("X-Panel-Token", "secret")
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s %s = %d, body=%s", tc.method, tc.path, resp.Code, resp.Body.String())
			}
		})
	}
}

func TestWireGuardProfileClientSensitiveRoutesRequirePanelToken(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
	basePath := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients"

	createReq := httptest.NewRequest(http.MethodPost, basePath, bytes.NewBufferString(`{"name":"phone"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST clients with token = %d, body=%s", createResp.Code, createResp.Body.String())
	}
	client := decodeWireGuardHTTPClientResponse(t, createResp.Body.Bytes())

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   basePath,
			body:   `{"name":"laptop"}`,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   basePath + "/" + strconv.Itoa(client.ID),
		},
		{
			name:   "config",
			method: http.MethodGet,
			path:   basePath + "/" + strconv.Itoa(client.ID) + "/config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without token = %d, body=%s", tc.method, tc.path, resp.Code, resp.Body.String())
			}
		})
	}
}

func TestWireGuardProfileClientLifecycleAndConfig(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
	basePath := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients"

	createReq := httptest.NewRequest(http.MethodPost, basePath, bytes.NewBufferString(`{"name":"phone"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST clients = %d, body=%s", createResp.Code, createResp.Body.String())
	}
	assertWireGuardHTTPClientBodyDoesNotLeakSecrets(t, createResp.Body.String())
	client := decodeWireGuardHTTPClientResponse(t, createResp.Body.Bytes())
	if client.Name != "phone" || client.Address != "10.8.0.2/32" || client.ProfileID != profile.ID {
		t.Fatalf("created client = %+v", client)
	}

	listReq := httptest.NewRequest(http.MethodGet, basePath, nil)
	listReq.Header.Set("X-Panel-Token", "secret")
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET clients after create = %d, body=%s", listResp.Code, listResp.Body.String())
	}
	assertWireGuardHTTPClientBodyDoesNotLeakSecrets(t, listResp.Body.String())
	clients := decodeWireGuardHTTPClientsResponse(t, listResp.Body.Bytes())
	if len(clients) != 1 || clients[0].ID != client.ID || clients[0].Address != "10.8.0.2/32" {
		t.Fatalf("clients after create = %+v", clients)
	}

	configReq := httptest.NewRequest(http.MethodGet, basePath+"/"+strconv.Itoa(client.ID)+"/config", nil)
	configReq.Header.Set("X-Panel-Token", "secret")
	configResp := httptest.NewRecorder()
	router.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusOK {
		t.Fatalf("GET client config = %d, body=%s", configResp.Code, configResp.Body.String())
	}
	if contentType := configResp.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", contentType)
	}
	if disposition := configResp.Header().Get("Content-Disposition"); !strings.Contains(disposition, "wireguard-client-"+strconv.Itoa(client.ID)+".conf") {
		t.Fatalf("Content-Disposition = %q", disposition)
	}
	for _, want := range []string{"[Interface]", "Endpoint = wg.example.com:51820", "Address = 10.8.0.2/32"} {
		if !strings.Contains(configResp.Body.String(), want) {
			t.Fatalf("config missing %q:\n%s", want, configResp.Body.String())
		}
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, basePath+"/"+strconv.Itoa(client.ID), nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE client = %d, body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	deleted := decodeWireGuardHTTPClientResponse(t, deleteResp.Body.Bytes())
	if deleted.ID != client.ID {
		t.Fatalf("deleted client = %+v, want id %d", deleted, client.ID)
	}

	listReq = httptest.NewRequest(http.MethodGet, basePath, nil)
	listReq.Header.Set("X-Panel-Token", "secret")
	listResp = httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET clients after delete = %d, body=%s", listResp.Code, listResp.Body.String())
	}
	clients = decodeWireGuardHTTPClientsResponse(t, listResp.Body.Bytes())
	if len(clients) != 0 {
		t.Fatalf("clients after delete = %+v, want empty", clients)
	}

	configReq = httptest.NewRequest(http.MethodGet, basePath+"/"+strconv.Itoa(client.ID)+"/config", nil)
	configReq.Header.Set("X-Panel-Token", "secret")
	configResp = httptest.NewRecorder()
	router.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusNotFound {
		t.Fatalf("GET deleted client config = %d, body=%s", configResp.Code, configResp.Body.String())
	}
}

func TestWireGuardProfileClientPatchUpdatesEnabled(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
	basePath := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients"

	createReq := httptest.NewRequest(http.MethodPost, basePath, bytes.NewBufferString(`{"name":"phone"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST clients = %d, body=%s", createResp.Code, createResp.Body.String())
	}
	client := decodeWireGuardHTTPClientResponse(t, createResp.Body.Bytes())

	patchReq := httptest.NewRequest(http.MethodPatch, basePath+"/"+strconv.Itoa(client.ID), bytes.NewBufferString(`{"enabled":false}`))
	patchReq.Header.Set("X-Panel-Token", "secret")
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp := httptest.NewRecorder()
	router.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("PATCH client = %d, body=%s", patchResp.Code, patchResp.Body.String())
	}
	updated := decodeWireGuardHTTPClientResponse(t, patchResp.Body.Bytes())
	if updated.ID != client.ID || updated.Enabled {
		t.Fatalf("patched client = %+v, want id %d enabled false", updated, client.ID)
	}

	listReq := httptest.NewRequest(http.MethodGet, basePath, nil)
	listReq.Header.Set("X-Panel-Token", "secret")
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET clients after patch = %d, body=%s", listResp.Code, listResp.Body.String())
	}
	clients := decodeWireGuardHTTPClientsResponse(t, listResp.Body.Bytes())
	if len(clients) != 1 || clients[0].Enabled {
		t.Fatalf("clients after patch = %+v, want disabled client", clients)
	}
}

func TestWireGuardProfileClientPatchRejectsMissingEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing", body: `{}`},
		{name: "null", body: `{"enabled":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, cleanup := newWireGuardHTTPTestRouter(t)
			defer cleanup()

			profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
			basePath := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients"

			createReq := httptest.NewRequest(http.MethodPost, basePath, bytes.NewBufferString(`{"name":"phone"}`))
			createReq.Header.Set("X-Panel-Token", "secret")
			createReq.Header.Set("Content-Type", "application/json")
			createResp := httptest.NewRecorder()
			router.ServeHTTP(createResp, createReq)
			if createResp.Code != http.StatusCreated {
				t.Fatalf("POST clients = %d, body=%s", createResp.Code, createResp.Body.String())
			}
			client := decodeWireGuardHTTPClientResponse(t, createResp.Body.Bytes())

			patchReq := httptest.NewRequest(http.MethodPatch, basePath+"/"+strconv.Itoa(client.ID), bytes.NewBufferString(tc.body))
			patchReq.Header.Set("X-Panel-Token", "secret")
			patchReq.Header.Set("Content-Type", "application/json")
			patchResp := httptest.NewRecorder()
			router.ServeHTTP(patchResp, patchReq)
			if patchResp.Code != http.StatusBadRequest {
				t.Fatalf("PATCH client %s enabled = %d, body=%s", tc.name, patchResp.Code, patchResp.Body.String())
			}

			listReq := httptest.NewRequest(http.MethodGet, basePath, nil)
			listReq.Header.Set("X-Panel-Token", "secret")
			listResp := httptest.NewRecorder()
			router.ServeHTTP(listResp, listReq)
			if listResp.Code != http.StatusOK {
				t.Fatalf("GET clients after rejected patch = %d, body=%s", listResp.Code, listResp.Body.String())
			}
			clients := decodeWireGuardHTTPClientsResponse(t, listResp.Body.Bytes())
			if len(clients) != 1 || !clients[0].Enabled {
				t.Fatalf("clients after rejected patch = %+v, want unchanged enabled client", clients)
			}
		})
	}
}

func TestWireGuardProfileClientPatchMissingReturnsNotFound(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
	path := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients/404"
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewBufferString(`{"enabled":false}`))
	req.Header.Set("X-Panel-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("PATCH missing client = %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["message"] != "wireguard client not found" {
		t.Fatalf("payload = %+v", payload)
	}
}

func newWireGuardHTTPTestRouter(t *testing.T) (http.Handler, func()) {
	t.Helper()

	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	cfg := config.Config{PanelToken: "secret", LocalAgentID: "local", EnableLocalAgent: true}
	router, err := NewRouter(Dependencies{
		Config:                  cfg,
		SystemService:           fakeSystemService{},
		AgentService:            fakeAgentService{},
		RuleService:             fakeRuleService{},
		L4RuleService:           fakeL4RuleService{},
		VersionPolicyService:    fakeVersionPolicyService{},
		RelayListenerService:    fakeRelayListenerService{},
		CertificateService:      fakeCertificateService{},
		TaskService:             fakeTaskService{},
		BackupService:           fakeBackupService{},
		TrafficService:          unavailableTrafficService{},
		WireGuardProfileService: service.NewWireGuardProfileService(cfg, store),
		WireGuardClientService:  service.NewWireGuardClientService(cfg, store),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router, func() { _ = store.Close() }
}

func createWireGuardHTTPClientProfile(t *testing.T, router http.Handler, prefix string, listenPort int) service.WireGuardProfile {
	t.Helper()
	createReq := httptest.NewRequest(http.MethodPost, prefix+"/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPClientProfilePayload(listenPort)))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST %s client profile = %d, body=%s", prefix, createResp.Code, createResp.Body.String())
	}
	return decodeWireGuardHTTPProfileResponse(t, createResp.Body.Bytes(), "profile")
}

func validWireGuardHTTPPayload() string {
	return validWireGuardHTTPPayloadWithPort(51820)
}

func validWireGuardHTTPPayloadWithPort(listenPort int) string {
	return `{
		"name":"wg-a",
		"mode":"generic_wireguard",
		"private_key":"` + httpTestWireGuardPrivateKey + `",
		"listen_port":` + strconv.Itoa(listenPort) + `,
		"addresses":["10.20.0.1/24"],
		"peers":[{"name":"peer-a","public_key":"` + httpTestWireGuardPublicKey + `","preshared_key":"` + httpTestWireGuardPresharedKey + `","endpoint":"peer.example.com:51820","allowed_ips":["10.20.0.2/32"],"persistent_keepalive_seconds":25}],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["edge"]
	}`
}

func validWireGuardHTTPClientProfilePayload(listenPort int) string {
	return `{
		"name":"wg-clients",
		"mode":"generic_wireguard",
		"private_key":"` + httpTestWireGuardPrivateKey + `",
		"listen_port":` + strconv.Itoa(listenPort) + `,
		"public_endpoint":"wg.example.com:51820",
		"addresses":["10.8.0.1/24"],
		"peers":[{"name":"manual-peer","public_key":"` + httpTestWireGuardPublicKey + `","preshared_key":"` + httpTestWireGuardPresharedKey + `","allowed_ips":["10.8.0.254/32"]}],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["edge"]
	}`
}

func validWireGuardHTTPURI() string {
	return "wireguard://" + httpTestWireGuardPrivateKey + "@peer.example.com:51820/?" +
		"publickey=" + httpTestWireGuardPublicKey +
		"&psk=" + httpTestWireGuardPresharedKey +
		"&address=10.44.0.2%2F32" +
		"&allowedips=0.0.0.0%2F0%2C%3A%3A%2F0" +
		"&dns=1.1.1.1%2C2606%3A4700%3A4700%3A%3A1111" +
		"&mtu=1420#phone"
}

func validWireGuardHTTPURIWithReserved() string {
	return "wireguard://" + httpTestWireGuardPrivateKey + "@peer.example.com:51820/?" +
		"publickey=" + httpTestWireGuardPublicKey +
		"&psk=" + httpTestWireGuardPresharedKey +
		"&address=10.44.0.2%2F32" +
		"&allowedips=0.0.0.0%2F0%2C%3A%3A%2F0" +
		"&dns=1.1.1.1%2C2606%3A4700%3A4700%3A%3A1111" +
		"&mtu=1420" +
		"&reserved=1,2,3#phone"
}

func decodeWireGuardHTTPProfileResponse(t *testing.T, body []byte, field string) service.WireGuardProfile {
	t.Helper()
	var payload struct {
		OK      bool                     `json:"ok"`
		Profile service.WireGuardProfile `json:"profile"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	if field != "profile" {
		t.Fatalf("unsupported field %q", field)
	}
	return payload.Profile
}

func decodeWireGuardHTTPProfilesResponse(t *testing.T, body []byte) []service.WireGuardProfile {
	t.Helper()
	var payload struct {
		OK       bool                       `json:"ok"`
		Profiles []service.WireGuardProfile `json:"profiles"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	return payload.Profiles
}

func decodeWireGuardHTTPClientResponse(t *testing.T, body []byte) service.WireGuardClient {
	t.Helper()
	var payload struct {
		OK     bool                    `json:"ok"`
		Client service.WireGuardClient `json:"client"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	return payload.Client
}

func decodeWireGuardHTTPClientsResponse(t *testing.T, body []byte) []service.WireGuardClient {
	t.Helper()
	var payload struct {
		OK      bool                      `json:"ok"`
		Clients []service.WireGuardClient `json:"clients"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	return payload.Clients
}

func assertWireGuardHTTPSecretsRedacted(t *testing.T, profile service.WireGuardProfile) {
	t.Helper()
	if profile.PrivateKey != httpTestWireGuardRedacted {
		t.Fatalf("private_key = %q, want redacted", profile.PrivateKey)
	}
	if len(profile.Peers) != 1 {
		t.Fatalf("peers length = %d, want 1", len(profile.Peers))
	}
	if profile.Peers[0].PresharedKey != httpTestWireGuardRedacted {
		t.Fatalf("peer preshared_key = %q, want redacted", profile.Peers[0].PresharedKey)
	}
}

func assertWireGuardHTTPBodyDoesNotLeakURISecrets(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{httpTestWireGuardPrivateKey, httpTestWireGuardPresharedKey} {
		if bytes.Contains([]byte(body), []byte(secret)) {
			t.Fatalf("response leaked secret %q in body %s", secret, body)
		}
	}
}

func assertWireGuardHTTPClientBodyDoesNotLeakSecrets(t *testing.T, body string) {
	t.Helper()
	for _, field := range []string{"private_key", "preshared_key"} {
		if strings.Contains(body, field) {
			t.Fatalf("response included secret field %q in body %s", field, body)
		}
	}
}
