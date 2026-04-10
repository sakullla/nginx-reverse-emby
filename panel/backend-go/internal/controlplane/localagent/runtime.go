package localagent

type Store interface {
	SnapshotStore
	RuntimeStateStore
}

type Runtime struct {
	source *SyncSource
	sink   *StateSink
}

func NewRuntime(store Store, agentID string) *Runtime {
	return &Runtime{
		source: NewSyncSource(store, agentID),
		sink:   NewStateSink(store, agentID),
	}
}

func (r *Runtime) SyncSource() *SyncSource {
	return r.source
}

func (r *Runtime) StateSink() *StateSink {
	return r.sink
}
