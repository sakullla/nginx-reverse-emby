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
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

type processDeadlineListener interface {
	SetDeadline(time.Time) error
}

type processStreamListener struct {
	listener net.Listener

	mu        sync.Mutex
	cond      *sync.Cond
	active    bool
	accepting bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

type processStreamGate interface {
	pause() error
	resume() error
}

func newProcessStreamListener(listener net.Listener, active bool) *processStreamListener {
	l := &processStreamListener{listener: listener, active: active}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *processStreamListener) Accept() (net.Conn, error) {
	if l == nil || l.listener == nil {
		return nil, net.ErrClosed
	}
	l.mu.Lock()
	for !l.active && !l.closed {
		l.cond.Wait()
	}
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.accepting = true
	l.mu.Unlock()

	conn, err := l.listener.Accept()

	l.mu.Lock()
	l.accepting = false
	l.cond.Broadcast()
	l.mu.Unlock()
	return conn, err
}

func (l *processStreamListener) Addr() net.Addr {
	if l == nil || l.listener == nil {
		return nil
	}
	return l.listener.Addr()
}

func (l *processStreamListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.active = false
		l.cond.Broadcast()
		l.mu.Unlock()
		if l.listener != nil {
			l.closeErr = l.listener.Close()
		}
	})
	return l.closeErr
}

func (l *processStreamListener) pause() error {
	if l == nil || l.listener == nil {
		return net.ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return net.ErrClosed
	}
	l.active = false
	if !l.accepting {
		return nil
	}
	deadline, ok := l.listener.(processDeadlineListener)
	if !ok {
		l.active = true
		l.cond.Broadcast()
		return errors.New("stream listener cannot interrupt an active accept")
	}
	if err := deadline.SetDeadline(time.Now()); err != nil {
		l.active = true
		l.cond.Broadcast()
		return err
	}
	for l.accepting && !l.closed {
		l.cond.Wait()
	}
	if l.closed {
		return net.ErrClosed
	}
	return nil
}

func (l *processStreamListener) resume() error {
	if l == nil || l.listener == nil {
		return net.ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return net.ErrClosed
	}
	if deadline, ok := l.listener.(processDeadlineListener); ok {
		if err := deadline.SetDeadline(time.Time{}); err != nil {
			return err
		}
	}
	l.active = true
	l.cond.Broadcast()
	return nil
}

func (l *processStreamListener) physical() net.Listener {
	if l == nil {
		return nil
	}
	return l.listener
}

type ProcessStreamRegistry struct {
	mu sync.Mutex

	brokers         map[string]*StreamBroker
	imported        *hotrestart.StreamSet
	claimed         []processStreamGate
	exportSources   map[string]net.Listener
	strict          bool
	importValidated bool
}

func NewProcessStreamRegistry() *ProcessStreamRegistry {
	return &ProcessStreamRegistry{brokers: make(map[string]*StreamBroker), exportSources: make(map[string]net.Listener)}
}

func (r *ProcessStreamRegistry) Import(descriptors []hotrestart.StreamDescriptor, files []*os.File) (*hotrestart.StreamSet, error) {
	if r == nil {
		return nil, errors.New("process stream registry is required")
	}
	exportSources := make(map[string]net.Listener, len(descriptors))
	closeSources := func() {
		for _, listener := range exportSources {
			_ = listener.Close()
		}
	}
	for _, descriptor := range descriptors {
		id := strings.TrimSpace(descriptor.ID)
		if id == "" || descriptor.FileIndex < 0 || descriptor.FileIndex >= len(files) || files[descriptor.FileIndex] == nil {
			closeSources()
			return nil, fmt.Errorf("stream descriptor %q cannot retain an export source", id)
		}
		if exportSources[id] != nil {
			closeSources()
			return nil, fmt.Errorf("duplicate stream descriptor %q", id)
		}
		listener, err := platform.ListenerFromFile(files[descriptor.FileIndex])
		if err != nil {
			closeSources()
			return nil, fmt.Errorf("retain stream listener %q for future export: %w", id, err)
		}
		if listener.Addr() == nil || listener.Addr().Network() != descriptor.Network || listener.Addr().String() != descriptor.Address {
			_ = listener.Close()
			closeSources()
			return nil, fmt.Errorf("retained stream listener %q does not match descriptor identity", id)
		}
		exportSources[id] = listener
	}
	set, err := hotrestart.ImportStreamListeners(descriptors, files)
	if err != nil {
		closeSources()
		return nil, err
	}
	r.mu.Lock()
	if r.strict || len(r.brokers) != 0 {
		r.mu.Unlock()
		_ = set.Close()
		closeSources()
		return nil, errors.New("process stream registry is already initialized")
	}
	r.strict = true
	r.imported = set
	r.exportSources = exportSources
	r.mu.Unlock()
	return set, nil
}

func (r *ProcessStreamRegistry) NewBroker(ctx context.Context, id string, listen func(context.Context) (net.Listener, error), inheritedAliases ...string) (*StreamBroker, error) {
	if r == nil {
		return nil, errors.New("process stream registry is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("process stream binding identity is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokers[id] != nil {
		return nil, fmt.Errorf("process stream binding %q is already registered", id)
	}
	var listener net.Listener
	inherited := false
	processID := id
	var err error
	if r.strict {
		candidateIDs := append([]string{id}, inheritedAliases...)
		var gated *hotrestart.GatedListener
		var source net.Listener
		processID, gated, source = findInheritedProcessStreamDescriptor(r.imported, r.exportSources, candidateIDs)
		if gated == nil || source == nil {
			return nil, fmt.Errorf("inherited stream descriptor %q is missing", id)
		}
		delete(r.imported.Listeners, processID)
		delete(r.exportSources, processID)
		_ = gated.Close()
		listener = source
		inherited = true
	} else {
		if listen == nil {
			return nil, errors.New("stream listen callback is required")
		}
		listener, err = listen(ctx)
		if err != nil {
			return nil, err
		}
	}
	if r.brokers[processID] != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, fmt.Errorf("process stream binding %q is already registered", processID)
	}
	broker := newStreamBroker(listener, !inherited)
	if broker == nil {
		_ = listener.Close()
		return nil, errors.New("create process stream broker")
	}
	broker.processRegistry = r
	broker.processID = processID
	r.brokers[processID] = broker
	if inherited {
		r.claimed = append(r.claimed, broker.listener.(*processStreamListener))
	}
	return broker, nil
}

func findInheritedProcessStreamDescriptor(imported *hotrestart.StreamSet, sources map[string]net.Listener, candidateIDs []string) (string, *hotrestart.GatedListener, net.Listener) {
	if imported == nil {
		return "", nil, nil
	}
	for _, candidateID := range candidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			continue
		}
		gated := imported.Listeners[candidateID]
		source := sources[candidateID]
		if gated != nil && source != nil {
			return candidateID, gated, source
		}
	}
	return "", nil, nil
}

// BindingID returns the descriptor identity under which broker is registered.
func (r *ProcessStreamRegistry) BindingID(broker *StreamBroker) string {
	if r == nil || broker == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokers[broker.processID] != broker {
		return ""
	}
	return broker.processID
}

func (r *ProcessStreamRegistry) ValidateImported() error {
	if r == nil {
		return errors.New("process stream registry is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.strict {
		if r.importValidated {
			return nil
		}
		return errors.New("process stream registry is not in child import mode")
	}
	if r.imported == nil || len(r.imported.Listeners) == 0 {
		r.strict = false
		r.importValidated = true
		return nil
	}
	ids := make([]string, 0, len(r.imported.Listeners))
	for id := range r.imported.Listeners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return fmt.Errorf("inherited stream descriptors were not consumed: %s", strings.Join(ids, ", "))
}

func (r *ProcessStreamRegistry) ImportPending() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	pending := r.strict
	r.mu.Unlock()
	return pending
}

func (r *ProcessStreamRegistry) ActivateImported() error {
	if err := r.ValidateImported(); err != nil {
		return err
	}
	r.mu.Lock()
	claimed := append([]processStreamGate(nil), r.claimed...)
	r.mu.Unlock()
	resumed := make([]processStreamGate, 0, len(claimed))
	for _, gate := range claimed {
		if err := gate.resume(); err != nil {
			activationErr := err
			for index := len(resumed) - 1; index >= 0; index-- {
				activationErr = errors.Join(activationErr, resumed[index].pause())
			}
			return activationErr
		}
		resumed = append(resumed, gate)
	}
	return nil
}

func (r *ProcessStreamRegistry) Export() (*hotrestart.StreamBundle, error) {
	if r == nil {
		return &hotrestart.StreamBundle{}, nil
	}
	r.mu.Lock()
	if r.strict {
		r.mu.Unlock()
		return nil, errors.New("cannot export stream listeners before child import validation completes")
	}
	listeners := make(map[string]net.Listener, len(r.brokers))
	for id, broker := range r.brokers {
		if broker == nil {
			continue
		}
		physical, ok := broker.listener.(*processStreamListener)
		if !ok || physical.physical() == nil {
			r.mu.Unlock()
			return nil, fmt.Errorf("process stream binding %q has no exportable listener", id)
		}
		listeners[id] = physical.physical()
	}
	r.mu.Unlock()
	return hotrestart.ExportStreamListeners(listeners)
}

func (r *ProcessStreamRegistry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	sources := r.exportSources
	r.exportSources = make(map[string]net.Listener)
	r.mu.Unlock()
	var closeErr error
	for _, listener := range sources {
		closeErr = errors.Join(closeErr, listener.Close())
	}
	return closeErr
}

func (r *ProcessStreamRegistry) Pause() error {
	if r == nil {
		return nil
	}
	brokers := r.snapshotBrokers()
	paused := make([]*StreamBroker, 0, len(brokers))
	for _, broker := range brokers {
		physical, ok := broker.listener.(*processStreamListener)
		if !ok {
			continue
		}
		if err := physical.pause(); err != nil {
			for index := len(paused) - 1; index >= 0; index-- {
				if prior, ok := paused[index].listener.(*processStreamListener); ok {
					_ = prior.resume()
				}
			}
			return err
		}
		paused = append(paused, broker)
	}
	return nil
}

func (r *ProcessStreamRegistry) Resume() error {
	if r == nil {
		return nil
	}
	var resumeErr error
	for _, broker := range r.snapshotBrokers() {
		if physical, ok := broker.listener.(*processStreamListener); ok {
			resumeErr = errors.Join(resumeErr, physical.resume())
		}
	}
	return resumeErr
}

func (r *ProcessStreamRegistry) snapshotBrokers() []*StreamBroker {
	r.mu.Lock()
	ids := make([]string, 0, len(r.brokers))
	for id := range r.brokers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	brokers := make([]*StreamBroker, 0, len(ids))
	for _, id := range ids {
		brokers = append(brokers, r.brokers[id])
	}
	r.mu.Unlock()
	return brokers
}

func (r *ProcessStreamRegistry) remove(id string, broker *StreamBroker) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.brokers[id] == broker {
		delete(r.brokers, id)
	}
	r.mu.Unlock()
}

var _ net.Listener = (*processStreamListener)(nil)
