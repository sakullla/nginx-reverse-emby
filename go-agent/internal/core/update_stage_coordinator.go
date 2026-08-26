package core

import (
	"context"
	"errors"
	"strings"
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

type packageStageTarget struct {
	URL            string
	SHA256         string
	Platform       string
	Filename       string
	Size           int64
	DesiredVersion string
}

type packageStageAttempt struct {
	target     packageStageTarget
	cancel     context.CancelFunc
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
	target := newPackageStageTarget(pkg, desiredVersion)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	attempt := c.attempt
	if attempt == nil || attempt.target != target {
		if attempt != nil {
			attempt.cancel()
		}
		stageCtx, cancel := context.WithCancel(ctx)
		attempt = &packageStageAttempt{target: target, cancel: cancel, state: packageStageRunning}
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
	stagedPath, err := updater.Stage(ctx, pkg)

	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt.state != packageStageRunning {
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
	if c.attempt != nil {
		c.attempt.cancel()
		c.attempt = nil
	}
}

func (c *PackageStageCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.attempt != nil {
		c.attempt.cancel()
		c.attempt = nil
	}
}

func newPackageStageTarget(pkg model.VersionPackage, desiredVersion string) packageStageTarget {
	return packageStageTarget{
		URL:            strings.TrimSpace(pkg.URL),
		SHA256:         strings.ToLower(strings.TrimSpace(pkg.SHA256)),
		Platform:       strings.ToLower(strings.TrimSpace(pkg.Platform)),
		Filename:       strings.TrimSpace(pkg.Filename),
		Size:           pkg.Size,
		DesiredVersion: strings.TrimSpace(desiredVersion),
	}
}
