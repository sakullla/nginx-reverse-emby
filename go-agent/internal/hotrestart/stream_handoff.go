package hotrestart

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

type StreamDescriptor struct {
	ID        string `json:"id"`
	Network   string `json:"network"`
	Address   string `json:"address"`
	FileIndex int    `json:"file_index"`
}

type StreamBundle struct {
	Descriptors []StreamDescriptor
	Files       []*os.File
}

func ExportStreamListeners(listeners map[string]net.Listener) (*StreamBundle, error) {
	ids := make([]string, 0, len(listeners))
	for id := range listeners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bundle := &StreamBundle{}
	for _, id := range ids {
		listener := listeners[id]
		if strings.TrimSpace(id) == "" || listener == nil || listener.Addr() == nil {
			_ = bundle.Close()
			return nil, errors.New("stream listener id, listener, and address are required")
		}
		file, err := platform.ListenerFile(listener)
		if err != nil {
			_ = bundle.Close()
			return nil, fmt.Errorf("export stream listener %q: %w", id, err)
		}
		bundle.Descriptors = append(bundle.Descriptors, StreamDescriptor{
			ID: id, Network: listener.Addr().Network(), Address: listener.Addr().String(), FileIndex: len(bundle.Files),
		})
		bundle.Files = append(bundle.Files, file)
	}
	return bundle, nil
}

func (b *StreamBundle) Close() error {
	if b == nil {
		return nil
	}
	var closeErr error
	for _, file := range b.Files {
		if file != nil {
			closeErr = errors.Join(closeErr, file.Close())
		}
	}
	b.Files = nil
	return closeErr
}

type GatedListener struct {
	listener net.Listener
	active   chan struct{}
	closed   chan struct{}
	activate sync.Once
	close    sync.Once
	closeErr error
}

func newGatedListener(listener net.Listener) *GatedListener {
	return &GatedListener{listener: listener, active: make(chan struct{}), closed: make(chan struct{})}
}

func (l *GatedListener) Activate() {
	if l != nil {
		l.activate.Do(func() { close(l.active) })
	}
}

func (l *GatedListener) Accept() (net.Conn, error) {
	if l == nil || l.listener == nil {
		return nil, net.ErrClosed
	}
	select {
	case <-l.active:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	return l.listener.Accept()
}

func (l *GatedListener) Addr() net.Addr {
	if l == nil || l.listener == nil {
		return nil
	}
	return l.listener.Addr()
}

func (l *GatedListener) Close() error {
	if l == nil {
		return nil
	}
	l.close.Do(func() {
		close(l.closed)
		if l.listener != nil {
			l.closeErr = l.listener.Close()
		}
	})
	return l.closeErr
}

type StreamSet struct {
	Listeners map[string]*GatedListener
	closeOnce sync.Once
	closeErr  error
}

func ImportStreamListeners(descriptors []StreamDescriptor, files []*os.File) (*StreamSet, error) {
	defer func() {
		for index, file := range files {
			if file != nil {
				_ = file.Close()
				files[index] = nil
			}
		}
	}()
	set := &StreamSet{Listeners: make(map[string]*GatedListener, len(descriptors))}
	usedFiles := make(map[int]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		id := strings.TrimSpace(descriptor.ID)
		if id == "" || descriptor.Network == "" || descriptor.Address == "" {
			_ = set.Close()
			return nil, errors.New("stream descriptor identity is incomplete")
		}
		if _, exists := set.Listeners[id]; exists {
			_ = set.Close()
			return nil, fmt.Errorf("duplicate stream descriptor %q", id)
		}
		if descriptor.FileIndex < 0 || descriptor.FileIndex >= len(files) {
			_ = set.Close()
			return nil, fmt.Errorf("stream descriptor %q has invalid file index", id)
		}
		if _, exists := usedFiles[descriptor.FileIndex]; exists {
			_ = set.Close()
			return nil, fmt.Errorf("stream descriptor %q reuses a file index", id)
		}
		listener, err := platform.ListenerFromFile(files[descriptor.FileIndex])
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("import stream listener %q: %w", id, err)
		}
		if listener.Addr() == nil || listener.Addr().Network() != descriptor.Network || listener.Addr().String() != descriptor.Address {
			_ = listener.Close()
			_ = set.Close()
			return nil, fmt.Errorf("stream descriptor %q does not match inherited listener identity", id)
		}
		usedFiles[descriptor.FileIndex] = struct{}{}
		set.Listeners[id] = newGatedListener(listener)
	}
	return set, nil
}

func (s *StreamSet) ActivateAll() {
	if s == nil {
		return
	}
	for _, listener := range s.Listeners {
		listener.Activate()
	}
}

func (s *StreamSet) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		for _, listener := range s.Listeners {
			s.closeErr = errors.Join(s.closeErr, listener.Close())
		}
	})
	return s.closeErr
}

var _ net.Listener = (*GatedListener)(nil)
