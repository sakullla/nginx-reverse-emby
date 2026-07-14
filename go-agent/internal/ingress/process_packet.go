package ingress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
)

type processPacketGate interface {
	Activate() error
	Pause() error
	Resume() error
	PrepareAuthority() error
	TakeAuthority() error
	Physical() net.PacketConn
}

type processPacketClaim struct {
	broker *PacketBroker
	gate   processPacketGate
}

type ProcessPacketRegistry struct {
	mu sync.Mutex

	brokers         map[string]*PacketBroker
	imported        *hotrestart.PacketSet
	claimed         []processPacketClaim
	forwarders      map[string]*hotrestart.PacketForwarder
	strict          bool
	importValidated bool
	forwarding      bool
}

func NewProcessPacketRegistry() *ProcessPacketRegistry {
	return &ProcessPacketRegistry{brokers: make(map[string]*PacketBroker)}
}

func (r *ProcessPacketRegistry) Import(descriptors []hotrestart.PacketDescriptor, files []*os.File) (*hotrestart.PacketSet, error) {
	if r == nil {
		return nil, errors.New("process packet registry is required")
	}
	set, err := hotrestart.ImportPacketConns(descriptors, files)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.strict || len(r.brokers) != 0 || len(r.forwarders) != 0 {
		r.mu.Unlock()
		_ = set.Close()
		return nil, errors.New("process packet registry is already initialized")
	}
	r.strict = true
	r.imported = set
	r.mu.Unlock()
	return set, nil
}

func (r *ProcessPacketRegistry) NewBroker(
	ctx context.Context,
	id string,
	network string,
	listen func(context.Context) (net.PacketConn, error),
	classifiers ...PacketClassifier,
) (*PacketBroker, error) {
	if r == nil {
		return nil, errors.New("process packet registry is required")
	}
	id = strings.TrimSpace(id)
	network = strings.TrimSpace(network)
	if id == "" || network == "" {
		return nil, errors.New("process packet binding identity and network are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokers[id] != nil {
		return nil, fmt.Errorf("process packet binding %q is already registered", id)
	}
	var conn net.PacketConn
	var gate processPacketGate
	if r.strict {
		var inherited *hotrestart.GatedPacketConn
		if r.imported != nil {
			inherited = r.imported.Conns[id]
		}
		if inherited == nil {
			return nil, fmt.Errorf("inherited packet descriptor %q is missing", id)
		}
		if inherited.LocalAddr() == nil || inherited.LocalAddr().Network() != network {
			return nil, fmt.Errorf("inherited packet descriptor %q network does not match", id)
		}
		delete(r.imported.Conns, id)
		conn = inherited
		gate = inherited
	} else {
		if listen == nil {
			return nil, errors.New("packet listen callback is required")
		}
		physical, listenErr := listen(ctx)
		if listenErr != nil {
			return nil, listenErr
		}
		managed := hotrestart.NewAuthorityPacketConn(physical)
		if managed == nil {
			_ = physical.Close()
			return nil, errors.New("create process packet authority connection")
		}
		conn = managed
		gate = managed
	}
	broker := newPacketBroker(conn, network, classifiers...)
	if broker == nil {
		_ = conn.Close()
		return nil, errors.New("create process packet broker")
	}
	broker.processRegistry = r
	broker.processID = id
	r.brokers[id] = broker
	r.claimed = append(r.claimed, processPacketClaim{broker: broker, gate: gate})
	return broker, nil
}

func (r *ProcessPacketRegistry) ValidateImported() error {
	if r == nil {
		return errors.New("process packet registry is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.strict {
		if r.importValidated {
			return nil
		}
		return errors.New("process packet registry is not in child import mode")
	}
	if r.imported == nil || len(r.imported.Conns) == 0 {
		r.strict = false
		r.importValidated = true
		return nil
	}
	ids := make([]string, 0, len(r.imported.Conns))
	for id := range r.imported.Conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return fmt.Errorf("inherited packet descriptors were not consumed: %s", strings.Join(ids, ", "))
}

func (r *ProcessPacketRegistry) ImportPending() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	pending := r.strict
	r.mu.Unlock()
	return pending
}

func (r *ProcessPacketRegistry) ActivateImported() error {
	if err := r.ValidateImported(); err != nil {
		return err
	}
	claims := r.snapshotClaims()
	activated := make([]processPacketClaim, 0, len(claims))
	for _, claim := range claims {
		if err := activatePacketClaim(claim); err != nil {
			activationErr := err
			for index := len(activated) - 1; index >= 0; index-- {
				activationErr = errors.Join(activationErr, pausePacketClaim(activated[index]))
			}
			return activationErr
		}
		activated = append(activated, claim)
	}
	return nil
}

func (r *ProcessPacketRegistry) TakeAuthorityImported() error {
	claims := r.snapshotClaims()
	for _, claim := range claims {
		if err := claim.gate.PrepareAuthority(); err != nil {
			return err
		}
	}
	var transferErr error
	for _, claim := range claims {
		transferErr = errors.Join(transferErr, claim.gate.TakeAuthority())
	}
	return transferErr
}

func (r *ProcessPacketRegistry) Export() (*hotrestart.PacketBundle, error) {
	if r == nil {
		return &hotrestart.PacketBundle{}, nil
	}
	r.mu.Lock()
	if r.strict {
		r.mu.Unlock()
		return nil, errors.New("cannot export packet connections before child import validation completes")
	}
	if len(r.forwarders) != 0 || r.forwarding {
		r.mu.Unlock()
		return nil, errors.New("packet handoff export is already active")
	}
	conns := make(map[string]net.PacketConn, len(r.brokers))
	for id, broker := range r.brokers {
		if broker == nil {
			continue
		}
		gate, ok := broker.conn.(processPacketGate)
		if !ok || gate.Physical() == nil {
			r.mu.Unlock()
			return nil, fmt.Errorf("process packet binding %q has no exportable connection", id)
		}
		conns[id] = gate.Physical()
	}
	r.mu.Unlock()
	bundle, err := hotrestart.ExportPacketConns(conns)
	if err != nil {
		return nil, err
	}
	forwarders := bundle.TakeForwarders()
	r.mu.Lock()
	if len(r.forwarders) != 0 || r.forwarding {
		r.mu.Unlock()
		for _, forwarder := range forwarders {
			_ = forwarder.Close()
		}
		_ = bundle.Close()
		return nil, errors.New("packet handoff export raced with another export")
	}
	r.forwarders = forwarders
	r.mu.Unlock()
	return bundle, nil
}

func (r *ProcessPacketRegistry) BeginForwarding() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.forwarding {
		r.mu.Unlock()
		return nil
	}
	ids := sortedPacketBrokerIDs(r.brokers)
	brokers := make([]*PacketBroker, 0, len(ids))
	forwarders := make([]*hotrestart.PacketForwarder, 0, len(ids))
	for _, id := range ids {
		forwarder := r.forwarders[id]
		if forwarder == nil {
			r.mu.Unlock()
			return fmt.Errorf("packet forwarding channel %q is missing", id)
		}
		brokers = append(brokers, r.brokers[id])
		forwarders = append(forwarders, forwarder)
	}
	r.mu.Unlock()
	started := make([]*PacketBroker, 0, len(brokers))
	for index, broker := range brokers {
		if err := broker.startProcessForwarding(forwarders[index]); err != nil {
			for _, prior := range started {
				prior.stopProcessForwarding()
			}
			return err
		}
		started = append(started, broker)
	}
	r.mu.Lock()
	r.forwarding = true
	r.mu.Unlock()
	return nil
}

func (r *ProcessPacketRegistry) Pause() error {
	if r == nil {
		return nil
	}
	claims := r.snapshotClaims()
	paused := make([]processPacketClaim, 0, len(claims))
	for _, claim := range claims {
		if err := pausePacketClaim(claim); err != nil {
			pauseErr := err
			for index := len(paused) - 1; index >= 0; index-- {
				pauseErr = errors.Join(pauseErr, resumePacketClaim(paused[index]))
			}
			return pauseErr
		}
		paused = append(paused, claim)
	}
	return nil
}

func (r *ProcessPacketRegistry) FlushForwarding() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	ids := make([]string, 0, len(r.forwarders))
	for id := range r.forwarders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	forwarders := make([]*hotrestart.PacketForwarder, 0, len(ids))
	for _, id := range ids {
		forwarders = append(forwarders, r.forwarders[id])
	}
	r.mu.Unlock()
	for _, forwarder := range forwarders {
		if err := forwarder.Barrier(); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProcessPacketRegistry) Resume() error {
	if r == nil {
		return nil
	}
	stopErr := r.stopForwarding()
	for _, claim := range r.snapshotClaims() {
		stopErr = errors.Join(stopErr, resumePacketClaim(claim))
	}
	return stopErr
}

func (r *ProcessPacketRegistry) FinalizeForwarding() error {
	return r.stopForwarding()
}

func (r *ProcessPacketRegistry) stopForwarding() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	brokers := make([]*PacketBroker, 0, len(r.brokers))
	for _, id := range sortedPacketBrokerIDs(r.brokers) {
		brokers = append(brokers, r.brokers[id])
	}
	forwarders := r.forwarders
	r.forwarders = nil
	r.forwarding = false
	r.mu.Unlock()
	for _, broker := range brokers {
		broker.stopProcessForwarding()
	}
	var closeErr error
	for _, forwarder := range forwarders {
		closeErr = errors.Join(closeErr, forwarder.Close())
	}
	return closeErr
}

func (r *ProcessPacketRegistry) Close() error {
	if r == nil {
		return nil
	}
	closeErr := r.stopForwarding()
	r.mu.Lock()
	imported := r.imported
	r.imported = nil
	r.mu.Unlock()
	if imported != nil {
		closeErr = errors.Join(closeErr, imported.Close())
	}
	return closeErr
}

func (r *ProcessPacketRegistry) snapshotClaims() []processPacketClaim {
	r.mu.Lock()
	claims := append([]processPacketClaim(nil), r.claimed...)
	r.mu.Unlock()
	return claims
}

func pausePacketClaim(claim processPacketClaim) error {
	if claim.broker != nil {
		return claim.broker.pauseProcessReads(claim.gate)
	}
	return claim.gate.Pause()
}

func activatePacketClaim(claim processPacketClaim) error {
	if claim.broker != nil {
		return claim.broker.resumeProcessReads(claim.gate)
	}
	return claim.gate.Activate()
}

func resumePacketClaim(claim processPacketClaim) error {
	if claim.broker != nil {
		return claim.broker.resumeProcessReads(claim.gate)
	}
	return claim.gate.Resume()
}

func (r *ProcessPacketRegistry) remove(id string, broker *PacketBroker) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.brokers[id] == broker {
		delete(r.brokers, id)
	}
	for index := range r.claimed {
		if r.claimed[index].broker == broker {
			r.claimed = append(r.claimed[:index], r.claimed[index+1:]...)
			break
		}
	}
	r.mu.Unlock()
}

func sortedPacketBrokerIDs(brokers map[string]*PacketBroker) []string {
	ids := make([]string, 0, len(brokers))
	for id := range brokers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
