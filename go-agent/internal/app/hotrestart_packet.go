package app

import (
	"context"
	"errors"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
)

func (a *App) startHotRestartWithResources(ctx context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
	if a == nil || a.processStreams == nil || a.processPackets == nil {
		return nil, errors.New("hot restart resource registries are required")
	}
	streamBundle, err := a.processStreams.Export()
	if err != nil {
		return nil, err
	}
	packetBundle, err := a.processPackets.Export()
	if err != nil {
		_ = streamBundle.Close()
		return nil, err
	}
	launch.StreamDescriptors = streamBundle.Descriptors
	launch.StreamFiles = streamBundle.Files
	launch.PacketDescriptors = packetBundle.Descriptors
	launch.PacketFiles = packetBundle.Files
	process, startErr := (hotrestart.Supervisor{}).Start(ctx, launch)
	closeErr := errors.Join(streamBundle.Close(), packetBundle.Close())
	if startErr != nil {
		return nil, errors.Join(startErr, closeErr, a.processPackets.Resume())
	}
	if closeErr != nil {
		_ = process.Abort()
		return nil, errors.Join(closeErr, a.processPackets.Resume())
	}
	return &hotRestartResourceProcess{
		hotRestartProcess: process,
		streams:           a.processStreams,
		packets:           a.processPackets,
	}, nil
}

type hotRestartResourceProcess struct {
	hotRestartProcess
	streams processStreamAuthority
	packets processPacketAuthority

	mu                 sync.Mutex
	authorityCommitted bool
	cleanupErr         error
	resumeOnce         sync.Once
	resumeErr          error
}

type processPacketAuthority interface {
	BeginForwarding() error
	Pause() error
	FlushForwarding() error
	Resume() error
	FinalizeForwarding() error
}

func (p *hotRestartResourceProcess) Activate(ctx context.Context) error {
	if p == nil || p.hotRestartProcess == nil {
		return errors.New("hot restart resource process is required")
	}
	if p.streams != nil {
		if err := p.streams.Pause(); err != nil {
			return p.abortAndResume(err)
		}
	}
	if p.packets != nil {
		if err := p.packets.BeginForwarding(); err != nil {
			return p.abortAndResume(err)
		}
	}
	if err := p.hotRestartProcess.Activate(ctx); err != nil {
		return errors.Join(err, p.hotRestartProcess.Abort(), p.resumeParent())
	}
	return nil
}

func (p *hotRestartResourceProcess) TransferAuthority(ctx context.Context) error {
	if p == nil || p.hotRestartProcess == nil {
		return errors.New("hot restart resource process is required")
	}
	if p.packets != nil {
		if err := p.packets.Pause(); err != nil {
			return p.abortAndResume(err)
		}
		if err := p.packets.FlushForwarding(); err != nil {
			return p.abortAndResume(err)
		}
	}
	if err := p.hotRestartProcess.TransferAuthority(ctx); err != nil {
		return errors.Join(err, p.hotRestartProcess.Abort(), p.resumeParent())
	}
	p.mu.Lock()
	p.authorityCommitted = true
	p.mu.Unlock()
	if p.packets != nil {
		cleanupErr := p.packets.FinalizeForwarding()
		p.mu.Lock()
		p.cleanupErr = errors.Join(p.cleanupErr, cleanupErr)
		p.mu.Unlock()
	}
	return nil
}

func (p *hotRestartResourceProcess) abortAndResume(stageErr error) error {
	return errors.Join(stageErr, p.hotRestartProcess.Abort(), p.resumeParent())
}

func (p *hotRestartResourceProcess) Abort() error {
	if p == nil || p.hotRestartProcess == nil {
		return nil
	}
	abortErr := p.hotRestartProcess.Abort()
	p.mu.Lock()
	committed := p.authorityCommitted
	cleanupErr := p.cleanupErr
	p.mu.Unlock()
	if committed {
		return errors.Join(abortErr, cleanupErr)
	}
	return errors.Join(abortErr, p.resumeParent())
}

func (p *hotRestartResourceProcess) Wait() error {
	if p == nil || p.hotRestartProcess == nil {
		return nil
	}
	waitErr := p.hotRestartProcess.Wait()
	p.mu.Lock()
	cleanupErr := p.cleanupErr
	p.mu.Unlock()
	return errors.Join(waitErr, cleanupErr)
}

func (p *hotRestartResourceProcess) resumeParent() error {
	p.resumeOnce.Do(func() {
		if p.packets != nil {
			p.resumeErr = errors.Join(p.resumeErr, p.packets.Resume())
		}
		if p.streams != nil {
			p.resumeErr = errors.Join(p.resumeErr, p.streams.Resume())
		}
	})
	return p.resumeErr
}

func (a *App) activateHotRestartChildResources() error {
	if a == nil || a.processStreams == nil || a.processPackets == nil {
		return errors.New("hot restart child resource registries are required")
	}
	if err := a.processStreams.ActivateImported(); err != nil {
		return err
	}
	if err := a.processPackets.ActivateImported(); err != nil {
		return errors.Join(err, a.processStreams.Pause())
	}
	return nil
}
