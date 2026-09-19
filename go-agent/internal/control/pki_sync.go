package control

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// PKIHeartbeatState contains replay-safe public state prepared by the
// execution-plane credential store before an ordinary authenticated heartbeat.
// It deliberately contains neither private keys nor key-bearing TLS objects.
type PKIHeartbeatState struct {
	SecurityAcknowledgement *model.PKISecurityAcknowledgement
	EnrollmentRequests      []model.PKIEnrollmentRequest
}

// PKIHeartbeatReply is the PKI-only portion of the heartbeat response. The
// sync client consumes this envelope before decoding the ordinary runtime
// snapshot so transient security state never enters revision cloning or
// generation digest semantics.
type PKIHeartbeatReply struct {
	Security    *model.PKISecuritySnapshot   `json:"pki_security,omitempty"`
	Credentials []model.PKIControlCredential `json:"pki_credentials,omitempty"`
	Status      *model.PKIControlStatus      `json:"pki_status,omitempty"`
}

// PKIHeartbeatHandler bridges the existing token-authenticated control
// transport to an execution-plane-only PKI store.
type PKIHeartbeatHandler interface {
	PrepareHeartbeat(context.Context) (PKIHeartbeatState, error)
	ApplyHeartbeat(context.Context, PKIHeartbeatReply) error
}
