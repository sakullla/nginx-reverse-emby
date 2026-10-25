//go:build !linux

package network

import "testing"

func assertPlainTCPListener(t *testing.T, manager *Manager, key string) {
	t.Helper()
	manager.mu.Lock()
	shared := manager.listeners[key]
	missing := shared == nil || shared.tcp == nil
	manager.mu.Unlock()
	if missing {
		t.Fatalf("missing managed TCP listener %s", key)
	}
}
