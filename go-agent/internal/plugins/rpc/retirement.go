package rpc

// RetireForReplacement stops new plugin network admission and process retries
// after a hot-restart child owns authority. Existing TCP I/O remains available
// until the generation drain destroys each instance.
func (h *Host) RetireForReplacement() {
	if h == nil {
		return
	}
	h.mu.Lock()
	for instance := range h.instances {
		instance.mu.Lock()
		instance.retired = true
		if instance.restartCancel != nil {
			instance.restartCancel()
		}
		instance.mu.Unlock()
	}
	h.mu.Unlock()
	if h.managed != nil {
		h.managed.RetireForReplacement()
	}
}
