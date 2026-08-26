//go:build !fast && !integration

package pki

import (
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// runBackendProductionSnapshotProbe executes the real backend projected
// signer/canonical marshaler from a temporary internal-package probe. This is
// deliberately cross-module: copying the producer payload shape into this
// package would let producer and consumer drift while both unit suites pass.

func testAgentExpectation(_ time.Time) CredentialExpectation {
	return CredentialExpectation{DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
}
