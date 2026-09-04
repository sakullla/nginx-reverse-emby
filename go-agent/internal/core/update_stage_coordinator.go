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
	identity       packageStageIdentity
	ctx            context.Context
	cancel         context.CancelFunc
	stageDone      chan struct{}
	activationDone chan struct{}
	discarded      bool
	state          packageStageState
	stagedPath     string
	err            error
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
	if err := c.Ensure(ctx, updater, pkg); err != nil {
		return err
	}
	return c.Activate(ctx, updater, pkg, desiredVersion)
}

// Ensure starts or observes the process-local Stage attempt without activating
// it. Revision sync uses this before pulling an apply lease so package transfer
// time is never charged to that lease.
func (c *PackageStageCoordinator) Ensure(
	ctx context.Context,
	updater Updater,
	pkg model.VersionPackage,
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
		case <-attempt.stageDone:
			if attempt.activationDone != nil {
				select {
				case <-attempt.activationDone:
				default:
					c.mu.Unlock()
					return errPackageStagePending
				}
			}
			c.attempt = nil
			attempt = nil
		default:
			c.mu.Unlock()
			return errPackageStagePending
		}
	}
	if attempt == nil {
		attemptCtx, cancel := context.WithCancel(ctx)
		stageCtx, cancelStage := context.WithCancel(attemptCtx)
		attempt = &packageStageAttempt{
			identity:  identity,
			ctx:       attemptCtx,
			cancel:    cancel,
			stageDone: make(chan struct{}),
			state:     packageStageRunning,
		}
		c.attempt = attempt
		c.mu.Unlock()
		go c.stage(stageCtx, cancelStage, updater, pkg, attempt)
		return errPackageStagePending
	}

	switch attempt.state {
	case packageStageRunning, packageStageActivating:
		c.mu.Unlock()
		return errPackageStagePending
	case packageStageFailed:
		err := attempt.err
		c.attempt = nil
		attempt.cancel()
		c.mu.Unlock()
		return err
	case packageStageActivated:
		c.mu.Unlock()
		return nil
	case packageStageReady:
		c.mu.Unlock()
		return nil
	default:
		c.mu.Unlock()
		return errors.New("package stage attempt has an invalid state")
	}
}

// Activate consumes only a verified-ready attempt whose immutable identity
// still matches the current snapshot. desiredVersion is deliberately supplied
// at activation time rather than being part of the Stage identity.
func (c *PackageStageCoordinator) Activate(
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
	if attempt == nil || attempt.discarded || attempt.identity != identity {
		c.mu.Unlock()
		return errPackageStagePending
	}
	switch attempt.state {
	case packageStageRunning, packageStageActivating:
		c.mu.Unlock()
		return errPackageStagePending
	case packageStageFailed:
		err := attempt.err
		c.attempt = nil
		attempt.cancel()
		c.mu.Unlock()
		return err
	case packageStageActivated:
		c.mu.Unlock()
		return ErrRestartRequested
	case packageStageReady:
		attempt.state = packageStageActivating
		attempt.activationDone = make(chan struct{})
		stagedPath := attempt.stagedPath
		attemptCtx := attempt.ctx
		c.mu.Unlock()

		activationCtx, cancelActivation := context.WithCancel(ctx)
		stopAttemptCancellation := context.AfterFunc(attemptCtx, cancelActivation)
		err := updater.Activate(activationCtx, stagedPath, desiredVersion)
		stopAttemptCancellation()
		cancelActivation()
		c.mu.Lock()
		discarded := attempt.discarded
		if c.attempt == attempt {
			if discarded {
				c.attempt = nil
			} else if err == nil || errors.Is(err, ErrRestartRequested) {
				attempt.state = packageStageActivated
				attempt.err = nil
			} else {
				// Preserve the existing retry behavior: the next heartbeat may
				// start a fresh attempt after this failed activation is reported.
				c.attempt = nil
			}
		}
		close(attempt.activationDone)
		attempt.cancel()
		c.mu.Unlock()
		if discarded && (err == nil || errors.Is(err, ErrRestartRequested)) {
			return context.Canceled
		}
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
	cancelStage context.CancelFunc,
	updater Updater,
	pkg model.VersionPackage,
	attempt *packageStageAttempt,
) {
	defer close(attempt.stageDone)
	defer cancelStage()
	stagedPath, err := updater.Stage(ctx, pkg)

	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt.state != packageStageRunning || attempt.discarded {
		attempt.cancel()
		return
	}
	if err != nil {
		attempt.state = packageStageFailed
		attempt.err = err
		attempt.cancel()
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		attempt.state = packageStageFailed
		attempt.err = ctxErr
		attempt.cancel()
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
	case <-c.attempt.stageDone:
		if c.attempt.activationDone != nil {
			select {
			case <-c.attempt.activationDone:
				c.attempt = nil
			default:
			}
			return
		}
		c.attempt = nil
	default:
	}
}

// Pending reports that this process still owns an unfinished candidate package
// (download, verified-ready, or activating). Heartbeats must keep the running
// image identity until a new process takes over.
func (c *PackageStageCoordinator) Pending() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempt == nil || c.attempt.discarded {
		return false
	}
	switch c.attempt.state {
	case packageStageRunning, packageStageReady, packageStageActivating:
		return true
	default:
		return false
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
		<-attempt.stageDone
		if attempt.activationDone != nil {
			<-attempt.activationDone
		}
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
