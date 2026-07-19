package core

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
)

func TestGenerationRegistrationCompletesBeforePublicationCanBegin(t *testing.T) {
	registrar := &blockingGenerationRegistrar{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager := &GenerationManager{
		drain:    NewGenerationDrain(nil),
		sessions: registrar,
	}
	registerDone := make(chan error, 1)
	go func() {
		_, err := manager.RegisterSession("old", generation.EntityKey{Module: "http", ID: "1"}, "session", generationTestSession{})
		registerDone <- err
	}()
	<-registrar.started

	publicationStarted := make(chan chan struct{}, 1)
	go func() { publicationStarted <- manager.beginPublication("next") }()
	select {
	case <-publicationStarted:
		t.Fatal("publication began while a nil-barrier registration was in progress")
	case <-time.After(20 * time.Millisecond):
	}

	close(registrar.release)
	if err := <-registerDone; err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}
	done := <-publicationStarted
	manager.endPublication(done)
}

func TestGenerationRegistrationRechecksConsecutivePublications(t *testing.T) {
	registrar := &blockingGenerationRegistrar{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager := &GenerationManager{sessions: registrar}
	first := make(chan struct{})
	second := make(chan struct{})
	manager.publicationDone = first
	manager.publicationID = "next"

	registerDone := make(chan error, 1)
	go func() {
		_, err := manager.RegisterSession("next", generation.EntityKey{Module: "http", ID: "1"}, "session", generationTestSession{})
		registerDone <- err
	}()

	manager.publicationMu.Lock()
	manager.publicationDone = second
	manager.publicationID = "next"
	close(first)
	manager.publicationMu.Unlock()
	select {
	case <-registrar.started:
		t.Fatal("registration did not wait for the consecutive publication")
	case <-time.After(20 * time.Millisecond):
	}

	manager.publicationMu.Lock()
	manager.publicationDone = nil
	manager.publicationID = ""
	close(second)
	manager.publicationMu.Unlock()
	<-registrar.started
	close(registrar.release)
	if err := <-registerDone; err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}
}

type blockingGenerationRegistrar struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingGenerationRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	close(r.started)
	<-r.release
	return &generation.SessionHandle{}, nil
}

type generationTestSession struct{}

func (generationTestSession) ForceClose(context.Context, string) error { return nil }
