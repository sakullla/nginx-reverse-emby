package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// managedCertificateSignFunc performs one background certificate issuance for
// the given certificate ID. It is the async counterpart of the historical
// synchronous issue path and is injected once the certificate mutation entry
// points (Create / Update / Issue) stop blocking on the issuer.
//
// Contract for the injected implementation:
//   - Acquire issuanceLock(certID) and re-read the fresh certificate row from
//     storage after acquiring the lock (another goroutine or the periodic
//     renewal loop may have changed it).
//   - Perform the ACME issue and persist the outcome: status "active" with
//     material and a revision bump on success, status "error" with backoff
//     fields on failure (see failManagedCertificateIssue).
//   - Respect ctx for cancellation (shutdown).
//
// The dispatcher itself does NOT hold issuanceLock; the sign function owns it,
// mirroring renewSingleCertificate, so there is no double-locking. Until a sign
// function is wired via SetSignFunc, Submit is a safe no-op that releases its
// in-flight slot immediately.
type managedCertificateSignFunc func(ctx context.Context, certID int) error

// managedCertificateDispatcher dispatches per-certificate background issuance
// goroutines with in-flight de-duplication, so concurrent submit requests for
// the same certificate never spawn duplicate ACME orders. It is a package-level
// singleton because the HTTP entry points and the periodic renewal loop use
// separate certificateService instances (each opens its own store), while the
// in-flight set must be shared process-wide. The in-flight map is in-memory and
// lost on restart; Recover rebuilds it from persisted status == "issuing".
type managedCertificateDispatcher struct {
	mu sync.Mutex

	inFlight map[int]struct{}
	sign     managedCertificateSignFunc
	baseCtx  context.Context
	logger   *log.Logger

	wg sync.WaitGroup
}

func newManagedCertificateDispatcher() *managedCertificateDispatcher {
	return &managedCertificateDispatcher{
		inFlight: make(map[int]struct{}),
		baseCtx:  context.Background(),
		logger:   log.Default(),
	}
}

var globalManagedCertificateDispatcher = newManagedCertificateDispatcher()

// ManagedCertificateDispatcher returns the process-wide dispatcher used by the
// HTTP entry points and startup recovery. The returned type is unexported on
// purpose: callers interact with it only through its methods.
func ManagedCertificateDispatcher() *managedCertificateDispatcher {
	return globalManagedCertificateDispatcher
}

// SetSignFunc registers the background issuance implementation. Intended to be
// called once during wiring; calling it again replaces the previous function.
func (d *managedCertificateDispatcher) SetSignFunc(fn managedCertificateSignFunc) {
	d.mu.Lock()
	d.sign = fn
	d.mu.Unlock()
}

// SetBaseContext sets the context handed to dispatched goroutines. It should be
// the application shutdown context so in-flight issuances are cancelled on
// SIGTERM/SIGINT. Defaults to context.Background() until wired.
func (d *managedCertificateDispatcher) SetBaseContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	d.mu.Lock()
	d.baseCtx = ctx
	d.mu.Unlock()
}

// SetLogger overrides the dispatcher logger (defaults to log.Default).
func (d *managedCertificateDispatcher) SetLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	d.mu.Lock()
	d.logger = logger
	d.mu.Unlock()
}

// Submit dispatches a background issuance for certID. It returns true when a new
// goroutine was started and false when certID already has an issuance in flight
// (de-duplicated), so callers avoid queuing redundant work or duplicate ACME
// orders. The goroutine is detached from the caller's request lifecycle: it
// runs under the dispatcher base context so issuance survives the HTTP
// response.
func (d *managedCertificateDispatcher) Submit(certID int) bool {
	d.mu.Lock()
	if _, ok := d.inFlight[certID]; ok {
		d.mu.Unlock()
		return false
	}
	d.inFlight[certID] = struct{}{}
	sign := d.sign
	baseCtx := d.baseCtx
	logger := d.logger
	d.mu.Unlock()

	d.wg.Add(1)
	go d.run(baseCtx, certID, sign, logger)
	return true
}

func (d *managedCertificateDispatcher) run(ctx context.Context, certID int, sign managedCertificateSignFunc, logger *log.Logger) {
	defer d.wg.Done()
	defer d.release(certID)

	if sign == nil {
		// Sign body not wired yet (pre-wiring); leave the certificate for the
		// periodic renewal loop or a later submit. Safe no-op.
		return
	}
	if err := sign(ctx, certID); err != nil && logger != nil {
		logger.Printf("[cert] background issue for certificate %d failed: %v", certID, err)
	}
}

func (d *managedCertificateDispatcher) release(certID int) {
	d.mu.Lock()
	delete(d.inFlight, certID)
	d.mu.Unlock()
}

// InFlight reports whether a background issuance goroutine is currently running
// for certID. Intended for tests and observability.
func (d *managedCertificateDispatcher) InFlight(certID int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.inFlight[certID]
	return ok
}

// Wait blocks until every dispatched issuance goroutine has finished. Tests use
// it to observe post-issue state; production does not block on it.
func (d *managedCertificateDispatcher) Wait() {
	d.wg.Wait()
}

// WaitWithTimeout blocks until every dispatched issuance goroutine has finished or the
// timeout elapses, returning true only if all finished within the timeout. Intended for
// graceful shutdown: application.Run returns once the shutdown context is cancelled, but
// background issuance goroutines may still be finishing — this gives them a bounded window
// to observe cancellation and persist their outcome instead of racing process exit.
func (d *managedCertificateDispatcher) WaitWithTimeout(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// managedCertificateListStore is the narrow store view Recover needs.
type managedCertificateListStore interface {
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
}

// Recover resumes background issuance for every managed certificate whose
// persisted status is "issuing". Called once at startup so issuances that were
// in flight when the process restarted are re-dispatched. Returns the number of
// certificates dispatched (already in-flight ones are de-duplicated).
// Certificates whose status is not "issuing" are left untouched; their retry
// cadence is owned by the periodic renewal loop with backoff.
func (d *managedCertificateDispatcher) Recover(ctx context.Context, store managedCertificateListStore) (int, error) {
	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		return 0, err
	}
	dispatched := 0
	for _, row := range rows {
		if managedCertificateFromRow(row).Status != "issuing" {
			continue
		}
		if d.Submit(row.ID) {
			dispatched++
		}
	}
	return dispatched, nil
}
