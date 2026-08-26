package core

import (
	"context"
	"errors"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var errPackageStagePending = errors.New("package staging is pending")

type packageStageState uint8

const (
	packageStageRunning packageStageState = iota
	packageStageReady
	packageStageActivating
	packageStageActivated
	packageStageFailed
)

// packageStageIdentity mirrors the immutable manifest identity. URL is only a
// locator, while desired version is checked from the current heartbeat when a
// ready path is activated.
type packageStageIdentity struct {
	Platform string
	SHA256   string
	Filename string
	Size     int64
}

type packageStageAttempt struct {
	identity   packageStageIdentity
	cancel     context.CancelFunc
	done       chan struct{}
	discarded  bool
	state      packageStageState
	stagedPath string
	err        error
}

// PackageStageCoordinator owns the one process-local package staging attempt.
// Stage runs outside the heartbeat call so slow downloads cannot occupy the
// App's periodic sync lock. Activation remains heartbeat-driven, which fences a
// completed attempt against the latest package target before cutover.
type PackageStageCoordinator struct {
	mu      sync.Mutex
	attempt *packageStageAttempt
	closed  bool
}

func NewPackageStageCoordinator() *PackageStageCoordinator {
	return &PackageStageCoordinator{}
}

func (c *PackageStageCoordinator) Handle(
	ctx context.Context,
	updater Updater,
	pkg model.VersionPackage,
	desiredVersion string,
) error {
	if c == nil {
		return errors.New("package stage coordinator is unavailable")
	}
	if updater == nil {
		return errors.New("updater unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := newPackageStageIdentity(pkg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	attempt := c.attempt
	if attempt != nil && (attempt.discarded || attempt.identity != identity) {
		// Keep the canceled attempt installed until its worker closes done. This
		// lets heartbeats remain non-blocking without overlapping Stage calls.
		attempt.discarded = true
		attempt.cancel()
		select {
		case <-attempt.done:
			c.attempt = nil
			attempt = nil
		default:
			c.mu.Unlock()
			return errPackageStagePending
		}
	}
	if attempt == nil {
		stageCtx, cancel := context.WithCancel(ctx)
		attempt = &packageStageAttempt{
			identity: identity,
			cancel:   cancel,
			done:     make(chan struct{}),
			state:    packageStageRunning,
		}
		c.attempt = attempt
		c.mu.Unlock()
		go c.stage(stageCtx, updater, pkg, attempt)
		return errPackageStagePending
	}

	switch attempt.state {
	case packageStageRunning, packageStageActivating:
		c.mu.Unlock()
		return errPackageStagePending
	case packageStageFailed:
		err := attempt.err
		c.attempt = nil
		c.mu.Unlock()
		return err
	case packageStageActivated:
		c.mu.Unlock()
		return ErrRestartRequested
	case packageStageReady:
		attempt.state = packageStageActivating
		stagedPath := attempt.stagedPath
		c.mu.Unlock()

		err := updater.Activate(ctx, stagedPath, desiredVersion)
		c.mu.Lock()
		if c.attempt == attempt {
			if err == nil || errors.Is(err, ErrRestartRequested) {
				attempt.state = packageStageActivated
				attempt.err = nil
			} else {
				// Preserve the existing retry behavior: the next heartbeat may
				// start a fresh attempt after this failed activation is reported.
				c.attempt = nil
			}
		}
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return ErrRestartRequested
	default:
		c.mu.Unlock()
		return errors.New("package stage attempt has an invalid state")
	}
}

func (c *PackageStageCoordinator) stage(
	ctx context.Context,
	updater Updater,
	pkg model.VersionPackage,
	attempt *packageStageAttempt,
) {
	defer close(attempt.done)
	stagedPath, err := updater.Stage(ctx, pkg)

	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt.state != packageStageRunning || attempt.discarded {
		return
	}
	if err != nil {
		attempt.state = packageStageFailed
		attempt.err = err
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		attempt.state = packageStageFailed
		attempt.err = ctxErr
		return
	}
	attempt.state = packageStageReady
	attempt.stagedPath = stagedPath
}

// Cancel isolates any completed result from a target that is no longer
// pending. Stage implementations receive cancellation through their context.
func (c *PackageStageCoordinator) Cancel() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempt == nil {
		return
	}
	c.attempt.discarded = true
	c.attempt.cancel()
	select {
	case <-c.attempt.done:
		c.attempt = nil
	default:
	}
}

func (c *PackageStageCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	attempt := c.attempt
	if attempt != nil {
		attempt.discarded = true
		attempt.cancel()
	}
	c.mu.Unlock()

	if attempt != nil {
		// Shutdown is the convergence boundary: temporary package resources may
		// not outlive the process-local coordinator that owns their worker.
		<-attempt.done
	}
	c.mu.Lock()
	if c.attempt == attempt {
		c.attempt = nil
	}
	c.mu.Unlock()
}

func newPackageStageIdentity(pkg model.VersionPackage) (packageStageIdentity, error) {
	manifest, err := versionPackageManifest(pkg, pkg.Platform)
	if err != nil {
		return packageStageIdentity{}, err
	}
	return packageStageIdentity{
		Platform: manifest.Platform,
		SHA256:   manifest.SHA256,
		Filename: manifest.Filename,
		Size:     manifest.Size,
	}, nil
}
