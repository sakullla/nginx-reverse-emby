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
	pkg, needed := c.pendingUpdatePackage(snapshot)
	if !needed {
		return nil
	}
	if err := c.preflightPendingUpdate(snapshot); err != nil {
		return c.recordRuntimeError(err)
	}

	stagedPath, err := c.Updater.Stage(ctx, pkg)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.Updater.Activate(stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, ErrRestartRequested) {
			return err
		}
		return c.recordRuntimeError(err)
	}
	return ErrRestartRequested
}

func (c *SyncController) preflightPendingUpdate(snapshot model.Snapshot) error {
	pkg, needed := c.pendingUpdatePackage(snapshot)
	if !needed {
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
	return preflighter.Preflight(pkg)
}

func (c *SyncController) pendingUpdatePackage(snapshot model.Snapshot) (model.VersionPackage, bool) {
	if !HasValidPackage(snapshot.VersionPackage) {
		return model.VersionPackage{}, false
	}
	pkg := *snapshot.VersionPackage
	desiredSHA := strings.TrimSpace(pkg.SHA256)
	currentSHA := strings.TrimSpace(c.CurrentPackageSHA256)
	if currentSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
		return model.VersionPackage{}, false
	}
	return pkg, true
}
