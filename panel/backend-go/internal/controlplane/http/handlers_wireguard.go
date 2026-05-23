package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type wireGuardURIRequest struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type wireGuardURIProfilePreview struct {
	Name       string   `json:"name,omitempty"`
	Endpoint   string   `json:"endpoint"`
	PublicKey  string   `json:"public_key"`
	Addresses  []string `json:"addresses"`
	AllowedIPs []string `json:"allowed_ips"`
	DNS        []string `json:"dns,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
}

func (d Dependencies) handleWireGuardURIParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var payload wireGuardURIRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	parsed, err := service.ParseWireGuardURI(payload.URI)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	redactedURI, err := service.RedactWireGuardURIPreview(payload.URI)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"uri": redactedURI,
		"profile": wireGuardURIProfilePreview{
			Name:       parsed.Name,
			Endpoint:   parsed.Endpoint,
			PublicKey:  parsed.PublicKey,
			Addresses:  parsed.Addresses,
			AllowedIPs: parsed.AllowedIPs,
			DNS:        parsed.DNS,
			MTU:        parsed.MTU,
		},
	})
}

func (d Dependencies) handleWireGuardProfileImportURI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var payload wireGuardURIRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	parsed, err := service.ParseWireGuardURI(payload.URI)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	input, err := service.WireGuardProfileInputFromURI(parsed, payload.Name)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	profile, err := d.WireGuardProfileService.Create(r.Context(), r.PathValue("agentID"), input)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"profile": profile,
	})
}

func (d Dependencies) handleWireGuardProfiles(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		profiles, err := d.WireGuardProfileService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"profiles": profiles,
		})
	case http.MethodPost:
		var payload service.WireGuardProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.WireGuardProfileService.Create(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleWireGuardProfile(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	profileID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || profileID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid wireguard profile id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.WireGuardProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.WireGuardProfileService.Update(r.Context(), agentID, profileID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	case http.MethodDelete:
		profile, err := d.WireGuardProfileService.Delete(r.Context(), agentID, profileID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleWireGuardProfileClients(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	profileID, ok := parseWireGuardProfilePathID(w, r.PathValue("profileID"))
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		clients, err := d.WireGuardClientService.ListClients(r.Context(), agentID, profileID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"clients": clients,
		})
	case http.MethodPost:
		var payload service.WireGuardClientInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		client, err := d.WireGuardClientService.CreateClient(r.Context(), agentID, profileID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":     true,
			"client": client,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleWireGuardProfileClient(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	profileID, ok := parseWireGuardProfilePathID(w, r.PathValue("profileID"))
	if !ok {
		return
	}
	clientID, ok := parseWireGuardClientPathID(w, r.PathValue("clientID"))
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var payload service.WireGuardClientInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		client, err := d.WireGuardClientService.UpdateClient(r.Context(), agentID, profileID, clientID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"client": client,
		})
	case http.MethodDelete:
		client, err := d.WireGuardClientService.DeleteClient(r.Context(), agentID, profileID, clientID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"client": client,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleWireGuardProfileClientConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agentID := r.PathValue("agentID")
	profileID, ok := parseWireGuardProfilePathID(w, r.PathValue("profileID"))
	if !ok {
		return
	}
	clientID, ok := parseWireGuardClientPathID(w, r.PathValue("clientID"))
	if !ok {
		return
	}
	configText, err := d.WireGuardClientService.ClientConfig(r.Context(), agentID, profileID, clientID)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="wireguard-client-%d.conf"`, clientID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(configText))
}

func (d Dependencies) handleWireGuardProfileClientURI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agentID := r.PathValue("agentID")
	profileID, ok := parseWireGuardProfilePathID(w, r.PathValue("profileID"))
	if !ok {
		return
	}
	clientID, ok := parseWireGuardClientPathID(w, r.PathValue("clientID"))
	if !ok {
		return
	}
	reserved, err := parseWireGuardClientURIReserved(r.URL.Query().Get("reserved"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	uriText, err := d.WireGuardClientService.ClientURI(r.Context(), agentID, profileID, clientID, reserved)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(uriText))
}

func parseWireGuardClientURIReserved(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 3 {
		return nil, fmt.Errorf("%w: reserved accepts at most 3 bytes", service.ErrInvalidArgument)
	}
	reserved := make([]byte, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 || value > 255 {
			return nil, fmt.Errorf("%w: reserved bytes must be between 0 and 255", service.ErrInvalidArgument)
		}
		reserved = append(reserved, byte(value))
	}
	return reserved, nil
}

func parseWireGuardProfilePathID(w http.ResponseWriter, raw string) (int, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid wireguard profile id"))
		return 0, false
	}
	return id, true
}

func parseWireGuardClientPathID(w http.ResponseWriter, raw string) (int, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid wireguard client id"))
		return 0, false
	}
	return id, true
}
