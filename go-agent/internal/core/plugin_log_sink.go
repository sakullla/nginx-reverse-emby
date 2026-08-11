package core

import (
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

type pluginRuntimeLogSink struct {
	outbox PluginLogOutboxStore
}

func NewPluginRuntimeLogSink(store Store) pluginprocess.RuntimeLogSink {
	outbox, ok := store.(PluginLogOutboxStore)
	if !ok || outbox == nil {
		return nil
	}
	return &pluginRuntimeLogSink{outbox: outbox}
}

func (sink *pluginRuntimeLogSink) CaptureRuntimeLogEvent(event pluginprocess.RuntimeLogEvent) error {
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
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := sink.outbox.EnqueuePluginLogReports(event.CaptureID, []model.PluginRuntimeLogReport{draft}); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

var _ pluginprocess.RuntimeLogSink = (*pluginRuntimeLogSink)(nil)
