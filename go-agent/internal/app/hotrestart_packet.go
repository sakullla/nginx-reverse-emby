package app

import (
	"context"
	"errors"

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
		abortErr := p.hotRestartProcess.Abort()
		if abortErr != nil {
			return errors.Join(err, abortErr)
		}
		return errors.Join(err, p.resumeParent())
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
		abortErr := p.hotRestartProcess.Abort()
		if abortErr != nil {
			return errors.Join(err, abortErr)
		}
		return errors.Join(err, p.resumeParent())
	}
	if p.packets != nil {
		return p.packets.FinalizeForwarding()
	}
	return nil
}

func (p *hotRestartResourceProcess) abortAndResume(stageErr error) error {
	abortErr := p.hotRestartProcess.Abort()
	if abortErr != nil {
		return errors.Join(stageErr, abortErr)
	}
	return errors.Join(stageErr, p.resumeParent())
}

func (p *hotRestartResourceProcess) Abort() error {
	if p == nil || p.hotRestartProcess == nil {
		return nil
	}
	abortErr := p.hotRestartProcess.Abort()
	if abortErr != nil {
		return abortErr
	}
	return p.resumeParent()
}

func (p *hotRestartResourceProcess) resumeParent() error {
	var resumeErr error
	if p.packets != nil {
		resumeErr = errors.Join(resumeErr, p.packets.Resume())
	}
	if p.streams != nil {
		resumeErr = errors.Join(resumeErr, p.streams.Resume())
	}
	return resumeErr
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
