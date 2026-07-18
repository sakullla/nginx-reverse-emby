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
		return nil
	}
	if err := c.preflightPendingUpdate(snapshot); err != nil {
		return c.recordRuntimeError(err)
	}

	stagedPath, err := c.Updater.Stage(ctx, *pkg)
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
