package http

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
)

type providerSessionRegistrar struct {
	mu     sync.Mutex
	seen   map[string]struct{}
	reject bool
}

func (registrar *providerSessionRegistrar) RegisterSession(generationID string, entity generation.EntityKey, sessionID string, _ generation.Session) (*generation.SessionHandle, error) {
	if registrar.reject {
		return nil, errors.New("registration rejected")
	}
	key := generationID + "\x00" + entity.Module + "\x00" + entity.ID + "\x00" + sessionID
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if _, duplicate := registrar.seen[key]; duplicate {
		return nil, fmt.Errorf("duplicate session %s", sessionID)
	}
	registrar.seen[key] = struct{}{}
	return nil, nil
}

func TestHTTPProviderGenerationTrackerRegistersConcurrentSessionsUniquely(t *testing.T) {
	registrar := &providerSessionRegistrar{seen: make(map[string]struct{})}
	tracker := newHTTPSessionTracker("generation-1", registrar, true)
	const count = 100
	var wait sync.WaitGroup
	wait.Add(count)
	errorsCh := make(chan error, count)
	for index := 0; index < count; index++ {
		go func() {
			defer wait.Done()
			session := tracker.startModule("http-provider", "rule-1:instance-1:default", func() {})
			session.mu.Lock()
			err := session.registrationErr
			session.mu.Unlock()
			if err != nil {
				errorsCh <- err
			}
			tracker.finish(session)
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	registrar.mu.Lock()
	registered := len(registrar.seen)
	registrar.mu.Unlock()
	if registered != count {
		t.Fatalf("registered sessions = %d, want %d", registered, count)
	}
}

func TestHTTPProviderGenerationTrackerExposesSynchronousRegistrationFailure(t *testing.T) {
	tracker := newHTTPSessionTracker("generation-1", &providerSessionRegistrar{reject: true}, true)
	canceled := false
	session := tracker.startModule("http-provider", "rule-1:instance-1:default", func() { canceled = true })
	session.mu.Lock()
	err := session.registrationErr
	session.mu.Unlock()
	if err == nil {
		t.Fatal("provider registration failure was not visible before dispatch")
	}
	tracker.finish(session)
	if !canceled {
		t.Fatal("failed provider session did not cancel its request")
	}
}
