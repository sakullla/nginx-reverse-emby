package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const (
	HostRuntimePublicCertificate    = "certificate.public"
	HostRuntimePublicCertificateTLS = "certificate.public.tls"

	PermissionPublicCertificateRead      = "certificate.public.read"
	PermissionPublicCertificateTerminate = "certificate.public.terminate"
)

const (
	CapabilityPublicCertificateRead      HostCapability = PermissionPublicCertificateRead
	CapabilityPublicCertificateTerminate HostCapability = PermissionPublicCertificateTerminate
)

// PublicCertificateListRequest asks the control plane for the public
// certificates already offered to one agent. It carries no certificate material.
type PublicCertificateListRequest struct {
	AgentID string `json:"agent_id"`
}

func (request PublicCertificateListRequest) Validate() error {
	if ValidatePolicyIdentity(request.AgentID) != nil {
		return errors.New("public certificate agent is invalid")
	}
	return nil
}

// PublicCertificateProjection is the only certificate view a plugin host call
// may receive. It identifies the certificate and whether its current material
// can be used. Private keys and PEM bytes are never part of this value.
type PublicCertificateProjection struct {
	ID                int    `json:"id"`
	Domain            string `json:"domain"`
	Enabled           bool   `json:"enabled"`
	MaterialAvailable bool   `json:"material_available"`
}

func (projection PublicCertificateProjection) Validate() error {
	if projection.ID <= 0 || !publicCertificateDomain(projection.Domain) {
		return errors.New("public certificate projection is invalid")
	}
	return nil
}

type PublicCertificateListResponse struct {
	Certificates []PublicCertificateProjection `json:"certificates"`
}

func (response PublicCertificateListResponse) Validate() error {
	if response.Certificates == nil {
		return errors.New("public certificate list is invalid")
	}
	for _, projection := range response.Certificates {
		if err := projection.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// PublicCertificateTLSRequest completes a server handshake on a TCP stream the
// plugin has already accepted. Handle must be that stream. The host must use
// only CertificateID and must not select another certificate.
type PublicCertificateTLSRequest struct {
	CertificateID int                  `json:"certificate_id"`
	Handle        ManagedNetworkHandle `json:"handle"`
	RequestID     string               `json:"request_id"`
}

func (request PublicCertificateTLSRequest) Validate() error {
	if request.CertificateID <= 0 {
		return errors.New("public certificate id is invalid")
	}
	if err := request.Handle.Validate(); err != nil || request.Handle.Kind != "stream" || request.Handle.Protocol != "tcp" {
		return errors.New("public certificate stream is invalid")
	}
	if ValidatePolicyIdentity(request.RequestID) != nil {
		return errors.New("public certificate request identity is invalid")
	}
	return nil
}

// PublicCertificateTLSResponse returns the post-handshake byte stream. The
// handle is used with the existing managed-network read and write operations.
type PublicCertificateTLSResponse struct {
	Handle ManagedNetworkHandle `json:"handle"`
}

func (response PublicCertificateTLSResponse) Validate() error {
	if err := response.Handle.Validate(); err != nil || response.Handle.Kind != "stream" || response.Handle.Protocol != "tcp" {
		return errors.New("public certificate stream is invalid")
	}
	return nil
}

func DecodePublicCertificateListRequest(payload json.RawMessage) (PublicCertificateListRequest, error) {
	var request PublicCertificateListRequest
	if err := decodeManagedPayload(payload, &request); err != nil {
		return request, err
	}
	return request, request.Validate()
}

func DecodePublicCertificateTLSRequest(payload json.RawMessage) (PublicCertificateTLSRequest, error) {
	var request PublicCertificateTLSRequest
	if err := decodeManagedPayload(payload, &request); err != nil {
		return request, err
	}
	return request, request.Validate()
}

func (client *HostRuntimeClient) ListPublicCertificates(ctx context.Context, agentID string) (PublicCertificateListResponse, error) {
	request := PublicCertificateListRequest{AgentID: agentID}
	if err := request.Validate(); err != nil {
		return PublicCertificateListResponse{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return PublicCertificateListResponse{}, err
	}
	var response PublicCertificateListResponse
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimePublicCertificate, Payload: payload}, &response); err != nil {
		return PublicCertificateListResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return PublicCertificateListResponse{}, err
	}
	return response, nil
}

func (client *HostRuntimeClient) TerminatePublicCertificateTLS(ctx context.Context, request PublicCertificateTLSRequest) (PublicCertificateTLSResponse, error) {
	if err := request.Validate(); err != nil {
		return PublicCertificateTLSResponse{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return PublicCertificateTLSResponse{}, err
	}
	var response PublicCertificateTLSResponse
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimePublicCertificateTLS, OperationID: request.RequestID, Payload: payload}, &response); err != nil {
		return PublicCertificateTLSResponse{}, err
	}
	if err := response.Validate(); err != nil || response.Handle.Binding != request.Handle.Binding || response.Handle.Token == request.Handle.Token {
		return PublicCertificateTLSResponse{}, errors.New("public certificate stream is invalid")
	}
	return response, nil
}

func publicCertificateDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\r\n\x00") {
		return false
	}
	upper := strings.ToUpper(domain)
	return !strings.Contains(upper, "PRIVATE KEY") && !strings.Contains(upper, "BEGIN ")
}
