package network

import sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"

const MaxActiveOperations = 128
const MaxReadOperations = 2*sdk.ManagedNetworkMaxFlows + 64
const MaxControlOperations = 64

// Admission separates idle socket polls from work and resource reclamation.
// A proxy flow can hold an inbound and an outbound read for its entire lifetime.
// Those waits must not consume the slots needed to dial, write or close it.
type Admission struct {
	reads, work, control chan struct{}
}

func NewAdmission() *Admission {
	return &Admission{reads: make(chan struct{}, MaxReadOperations), work: make(chan struct{}, MaxActiveOperations), control: make(chan struct{}, MaxControlOperations)}
}

func (a *Admission) TryAcquire(action string) (func(), bool) {
	gate := a.work
	switch action {
	case sdk.ManagedNetworkRead, sdk.ManagedNetworkReceive, sdk.ManagedNetworkAccept:
		gate = a.reads
	case sdk.ManagedNetworkClose, sdk.ManagedNetworkHalfClose, sdk.ManagedNetworkCancel:
		gate = a.control
	}
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, true
	default:
		return nil, false
	}
}
