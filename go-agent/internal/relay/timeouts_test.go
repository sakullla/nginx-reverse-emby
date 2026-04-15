package relay

import (
	"testing"
	"time"
)

func TestConfigureTimeoutsOverridesRelayPackageTimeouts(t *testing.T) {
	reset := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      9 * time.Second,
		HandshakeTimeout: 8 * time.Second,
		FrameTimeout:     7 * time.Second,
		IdleTimeout:      6 * time.Second,
	})
	defer reset()

	if relayDialTimeout != 9*time.Second {
		t.Fatalf("relayDialTimeout = %v", relayDialTimeout)
	}
	if relayIdleTimeout != 6*time.Second {
		t.Fatalf("relayIdleTimeout = %v", relayIdleTimeout)
	}
}

func TestConfigureTimeoutsAppliesNonZeroValuesAndResets(t *testing.T) {
	resetBase := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      4 * time.Second,
		HandshakeTimeout: 5 * time.Second,
		FrameTimeout:     6 * time.Second,
		IdleTimeout:      7 * time.Second,
	})
	defer resetBase()

	reset := ConfigureTimeouts(TimeoutConfig{
		DialTimeout: 11 * time.Second,
		IdleTimeout: 13 * time.Second,
	})
	if relayDialTimeout != 11*time.Second {
		t.Fatalf("relayDialTimeout = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 5*time.Second {
		t.Fatalf("relayHandshakeTimeout = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 6*time.Second {
		t.Fatalf("relayFrameTimeout = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 13*time.Second {
		t.Fatalf("relayIdleTimeout = %v", relayIdleTimeout)
	}

	reset()
	if relayDialTimeout != 4*time.Second {
		t.Fatalf("relayDialTimeout after reset = %v", relayDialTimeout)
	}
	if relayIdleTimeout != 7*time.Second {
		t.Fatalf("relayIdleTimeout after reset = %v", relayIdleTimeout)
	}
}
