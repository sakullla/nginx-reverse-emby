package core

import (
	"context"
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func (c *SyncController) HandlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error {
	return c.handlePendingUpdate(ctx, snapshot)
}

func (c *SyncController) handlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error {
	pkg, pending := c.pendingUpdatePackage(snapshot)
	if !pending {
		if c.PackageStages != nil {
			c.PackageStages.Cancel()
		}
		return nil
	}
	if err := c.preflightPendingUpdate(snapshot); err != nil {
		return c.recordRuntimeError(err)
	}
	if c.PackageStages != nil {
		err := c.PackageStages.Ensure(ctx, c.Updater, *pkg)
		if errors.Is(err, errPackageStagePending) {
			return err
		}
		if err != nil {
			return c.recordRuntimeError(err)
		}
		err = c.PackageStages.Activate(ctx, c.Updater, *pkg, snapshot.DesiredVersion)
		if errors.Is(err, errPackageStagePending) || errors.Is(err, ErrRestartRequested) {
			return err
		}
		if err != nil {
			return c.recordRuntimeError(err)
		}
		return ErrRestartRequested
	}

	stagedPath, err := c.Updater.Stage(ctx, *pkg)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.Updater.Activate(ctx, stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, ErrRestartRequested) {
			return err
		}
		return c.recordRuntimeError(err)
	}
	return ErrRestartRequested
}

// ensurePendingUpdate advances package acquisition without activating it.
// Production revision sync calls this before PullRevision so a slow Stage never
// starts or consumes an apply lease. Controllers without a long-lived
// coordinator retain the legacy synchronous path in handlePendingUpdate.
func (c *SyncController) ensurePendingUpdate(ctx context.Context, snapshot model.Snapshot) error {
	pkg, pending := c.pendingUpdatePackage(snapshot)
	if !pending {
		if c.PackageStages != nil {
			c.PackageStages.Cancel()
		}
		return nil
	}
	if c.PackageStages == nil {
		return nil
	}
	if err := c.preflightPendingUpdate(snapshot); err != nil {
		return err
	}
	return c.PackageStages.Ensure(ctx, c.Updater, *pkg)
}

func (c *SyncController) preflightPendingUpdate(snapshot model.Snapshot) error {
	pkg, pending := c.pendingUpdatePackage(snapshot)
	if !pending {
		return nil
	}
	if c.Updater == nil {
		return errors.New("updater unavailable")
	}
	preflighter, ok := c.Updater.(interface {
		Preflight(model.VersionPackage) error
	})
	if !ok {
		return errors.New("updater does not support package preflight")
	}
	return preflighter.Preflight(*pkg)
}

func (c *SyncController) pendingUpdatePackage(snapshot model.Snapshot) (*model.VersionPackage, bool) {
	if snapshot.VersionPackage == nil {
		return nil, false
	}
	desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
	currentSHA := strings.TrimSpace(c.CurrentPackageSHA256)
	if currentSHA != "" && desiredSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
		return snapshot.VersionPackage, false
	}
	return snapshot.VersionPackage, true
}
