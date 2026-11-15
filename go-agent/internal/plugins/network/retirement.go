package network

// RetireForReplacement releases process-local listen endpoints after authority
// moves to another Agent process. Accepted TCP streams can drain normally.
// Inbound UDP flows share their listening socket, so they must end here to let
// the replacement bind that port. Ordinary in-process generation replacement
// continues to use Owner.Retire and shares the UDP socket with its successor.
func (manager *Manager) RetireForReplacement() {
	manager.mu.Lock()
	manager.closed = true // Fence late Stage/listen calls, including restart races.
	owners := make([]*Owner, 0, len(manager.owners))
	for owner := range manager.owners {
		owners = append(owners, owner)
	}
	manager.mu.Unlock()
	for _, owner := range owners {
		owner.Retire()
		owner.mu.Lock()
		handles := owner.copyHandles()
		owner.mu.Unlock()
		for _, handle := range handles {
			if handle.handle.Kind == "datagram" && handle.listener != nil {
				handle.close()
			}
		}
	}
}
