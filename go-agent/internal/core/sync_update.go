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
	if !HasValidPackage(snapshot.VersionPackage) {
		return nil
	}
	desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
	if desiredSHA == "" {
		return nil
	}
	currentSHA := strings.TrimSpace(c.CurrentPackageSHA256)
	if currentSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
		return nil
	}
	if c.Updater == nil {
		return c.recordRuntimeError(errors.New("updater unavailable"))
	}

	stagedPath, err := c.Updater.Stage(ctx, *snapshot.VersionPackage)
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
