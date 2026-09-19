package core

import (
	"context"
	"errors"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

type pluginRuntimeLogSink struct {
	outbox PluginLogBackpressureStore
}

func NewPluginRuntimeLogSink(store Store) pluginprocess.RuntimeLogSink {
	outbox, ok := store.(PluginLogBackpressureStore)
	if !ok || outbox == nil {
		return nil
	}
	return &pluginRuntimeLogSink{outbox: outbox}
}

func (sink *pluginRuntimeLogSink) CaptureRuntimeLogEvent(ctx context.Context, event pluginprocess.RuntimeLogEvent) error {
	if sink == nil || sink.outbox == nil {
		return errors.New("plugin runtime log durable sink is unavailable")
	}
	if !validPluginLogOutboxID(event.CaptureID) {
		return errors.New("plugin runtime log capture identity is invalid")
	}
	draft := model.PluginRuntimeLogReport{
		Revision: event.Identity.Revision, GenerationID: event.Identity.ProviderGenerationID,
		InstanceID: event.Identity.InstanceID, PluginID: event.Identity.PluginID, AgentID: event.Identity.AgentID,
		PackageDigest: event.Identity.PackageDigest, ArtifactDigest: event.Identity.ArtifactDigest,
		Entries: []model.PluginRuntimeLogEntry{{Level: event.Entry.Level, Message: event.Entry.Message, Truncated: event.Entry.Truncated}},
	}
	if ctx == nil {
		ctx = context.Background()
	}
	backoff := 10 * time.Millisecond
	for {
		if _, err := sink.outbox.EnqueuePluginLogReports(event.CaptureID, []model.PluginRuntimeLogReport{draft}); err == nil {
			return nil
		} else if errors.Is(err, ErrPluginLogOutboxFull) {
			if waitErr := sink.outbox.WaitForPluginLogCapacity(ctx); waitErr != nil {
				return errors.Join(err, waitErr)
			}
		} else {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(err, ctx.Err())
			case <-timer.C:
			}
			if backoff < 250*time.Millisecond {
				backoff *= 2
				if backoff > 250*time.Millisecond {
					backoff = 250 * time.Millisecond
				}
			}
		}
	}
}

type pluginRuntimeLogFenceRetirer struct {
	store PluginLogFenceRetirementStore
}

func NewPluginRuntimeLogFenceRetirer(store Store) PluginLogFenceRetirementStore {
	retirement, ok := store.(PluginLogFenceRetirementStore)
	if !ok || retirement == nil {
		return nil
	}
	return &pluginRuntimeLogFenceRetirer{store: retirement}
}

func (retirer *pluginRuntimeLogFenceRetirer) RetirePluginRuntimeLogFence(identity pluginprocess.RuntimeLogIdentity) error {
	if retirer == nil || retirer.store == nil {
		return errors.New("plugin runtime log fence retirement is unavailable")
	}
	return retirer.store.RetirePluginRuntimeLogFence(identity)
}

var _ pluginprocess.RuntimeLogSink = (*pluginRuntimeLogSink)(nil)
