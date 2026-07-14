package app

import (
	"context"
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
)

func (a *App) startHotRestartWithStreams(ctx context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
	if a == nil || a.processStreams == nil {
		return (hotrestart.Supervisor{}).Start(ctx, launch)
	}
	bundle, err := a.processStreams.Export()
	if err != nil {
		return nil, err
	}
	launch.StreamDescriptors = bundle.Descriptors
	launch.StreamFiles = bundle.Files
	process, startErr := (hotrestart.Supervisor{}).Start(ctx, launch)
	closeErr := bundle.Close()
	if startErr != nil {
		return nil, errors.Join(startErr, closeErr)
	}
	if closeErr != nil {
		_ = process.Abort()
		return nil, closeErr
	}
	return &hotRestartStreamProcess{hotRestartProcess: process, parent: a.processStreams}, nil
}

type hotRestartStreamProcess struct {
	hotRestartProcess
	parent processStreamAuthority
}

type processStreamAuthority interface {
	Pause() error
	Resume() error
}

func (p *hotRestartStreamProcess) Activate(ctx context.Context) error {
	if p == nil || p.hotRestartProcess == nil {
		return errors.New("hot restart stream process is required")
	}
	if p.parent != nil {
		if err := p.parent.Pause(); err != nil {
			return err
		}
	}
	if err := p.hotRestartProcess.Activate(ctx); err != nil {
		abortErr := p.hotRestartProcess.Abort()
		if p.parent != nil {
			return errors.Join(err, abortErr, p.parent.Resume())
		}
		return errors.Join(err, abortErr)
	}
	return nil
}

func (p *hotRestartStreamProcess) Abort() error {
	if p == nil || p.hotRestartProcess == nil {
		return nil
	}
	abortErr := p.hotRestartProcess.Abort()
	if p.parent == nil {
		return abortErr
	}
	return errors.Join(abortErr, p.parent.Resume())
}
