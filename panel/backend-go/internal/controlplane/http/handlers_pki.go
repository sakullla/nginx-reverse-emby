package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) requirePKI(w http.ResponseWriter) (PKIService, bool) {
	if d.PKIService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("internal PKI service unavailable"))
		return nil, false
	}
	return d.PKIService, true
}

func (d Dependencies) handlePKIOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	value, err := pki.Overview(r.Context())
	d.writePKIResource(w, "overview", value, err)
}

func (d Dependencies) handlePKIAuthorities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	value, err := pki.Authorities(r.Context())
	d.writePKIResource(w, "authorities", value, err)
}

func (d Dependencies) handlePKIIdentities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	value, err := pki.Identities(r.Context())
	d.writePKIResource(w, "identities", value, err)
}

func (d Dependencies) handlePKICertificates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	value, err := pki.Certificates(r.Context())
	d.writePKIResource(w, "certificates", value, err)
}

func (d Dependencies) handlePKIEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	query := service.PKIEventQuery{
		Type: r.URL.Query().Get("type"), IdentityID: r.URL.Query().Get("identity_id"), SerialHex: r.URL.Query().Get("serial"),
		OperatorID: r.URL.Query().Get("operator_id"), Source: r.URL.Query().Get("source"), Result: r.URL.Query().Get("result"),
	}
	if value := strings.TrimSpace(r.URL.Query().Get("ca_generation")); value != "" {
		generation, err := strconv.ParseInt(value, 10, 64)
		if err != nil || generation <= 0 {
			writeJSON(w, http.StatusBadRequest, errorPayload("ca_generation must be a positive integer"))
			return
		}
		query.CAGeneration = &generation
	}
	parseBoundary := func(name string) (*time.Time, bool) {
		value := strings.TrimSpace(r.URL.Query().Get(name))
		if value == "" {
			return nil, true
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload(name+" must be RFC3339"))
			return nil, false
		}
		parsed = parsed.UTC()
		return &parsed, true
	}
	var okBoundary bool
	if query.From, okBoundary = parseBoundary("from"); !okBoundary {
		return
	}
	if query.To, okBoundary = parseBoundary("to"); !okBoundary {
		return
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		writeJSON(w, http.StatusBadRequest, errorPayload("from must not be after to"))
		return
	}
	value, err := pki.Events(r.Context(), query)
	d.writePKIResource(w, "events", value, err)
}

func (d Dependencies) handlePKIAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	value, err := pki.Alerts(r.Context())
	d.writePKIResource(w, "alerts", value, err)
}

func (d Dependencies) handlePKIEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	var body struct {
		Scope        string `json:"scope"`
		BoundAgentID string `json:"bound_agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	request := service.PKIEnrollmentTokenRequest{Scope: body.Scope, BoundAgentID: body.BoundAgentID}
	// The panel token authenticates this request; never trust a caller supplied
	// operator identifier in an enrollment secret request.
	request.CreatedBy = "panel"
	token, err := pki.CreateEnrollmentToken(r.Context(), request)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "enrollment_token": map[string]any{
		"token": token.Token, "scope": token.Scope, "bound_agent_id": token.BoundAgentID, "expires_at": token.ExpiresAt,
	}})
}

func (d Dependencies) handlePKIConfirmations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	var request service.PKIConfirmationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	confirmation, err := pki.IssueConfirmationNonce(r.Context(), request)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "confirmation": confirmation})
}

func (d Dependencies) handlePKIRevoke(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, r.PathValue("identityID"), func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.Revoke(r.Context(), request)
	})
}

func (d Dependencies) handlePKIForceRotate(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, r.PathValue("identityID"), func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.ForceRotate(r.Context(), request)
	})
}

func (d Dependencies) handlePKIRotateCA(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, "", func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.RotateCA(r.Context(), request)
	})
}

func (d Dependencies) handlePKIEmergencyRotateCA(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, "", func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.EmergencyRotateCA(r.Context(), request)
	})
}

func (d Dependencies) handlePKIProtectedExport(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, "", func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.ExportProtected(r.Context(), request)
	})
}

func (d Dependencies) handlePKIProtectedImport(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, "", func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.ImportProtected(r.Context(), request)
	})
}

func (d Dependencies) handlePKIActivation(w http.ResponseWriter, r *http.Request) {
	d.handlePKIAction(w, r, "", func(pki PKIService, request service.PKIActionRequest) (service.PKIOperation, error) {
		return pki.Activate(r.Context(), request)
	})
}

func (d Dependencies) handlePKIAction(
	w http.ResponseWriter,
	r *http.Request,
	targetID string,
	invoke func(PKIService, service.PKIActionRequest) (service.PKIOperation, error),
) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	var request service.PKIActionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	request.TargetID = strings.TrimSpace(targetID)
	operation, err := invoke(pki, request)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	prefix := "/panel-api"
	if strings.HasPrefix(r.URL.Path, "/api/") {
		prefix = "/api"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "operation_id": operation.ID, "status": operation.State,
		"status_url": prefix + "/pki/operations/" + operation.ID, "operation": operation,
	})
}

func (d Dependencies) handlePKIOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	pki, ok := d.requirePKI(w)
	if !ok {
		return
	}
	operation, err := pki.Operation(r.Context(), r.PathValue("operationID"))
	d.writePKIResource(w, "operation", operation, err)
}

func (d Dependencies) writePKIResource(w http.ResponseWriter, key string, value any, err error) {
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, key: value})
}
