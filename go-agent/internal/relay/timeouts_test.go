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

func TestConfigureTimeoutsResetDoesNotOverwriteNewerConfiguration(t *testing.T) {
	resetOuter := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      4 * time.Second,
		HandshakeTimeout: 5 * time.Second,
		FrameTimeout:     6 * time.Second,
		IdleTimeout:      7 * time.Second,
	})
	defer resetOuter()

	resetInner := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      11 * time.Second,
		HandshakeTimeout: 12 * time.Second,
		FrameTimeout:     13 * time.Second,
		IdleTimeout:      14 * time.Second,
	})

	resetOuter()
	if relayDialTimeout != 11*time.Second {
		t.Fatalf("relayDialTimeout after stale reset = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 12*time.Second {
		t.Fatalf("relayHandshakeTimeout after stale reset = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 13*time.Second {
		t.Fatalf("relayFrameTimeout after stale reset = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 14*time.Second {
		t.Fatalf("relayIdleTimeout after stale reset = %v", relayIdleTimeout)
	}

	resetInner()
	if relayDialTimeout != 5*time.Second {
		t.Fatalf("relayDialTimeout after inner reset = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 5*time.Second {
		t.Fatalf("relayHandshakeTimeout after inner reset = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 5*time.Second {
		t.Fatalf("relayFrameTimeout after inner reset = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 2*time.Minute {
		t.Fatalf("relayIdleTimeout after inner reset = %v", relayIdleTimeout)
	}
}
