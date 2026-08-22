package storage

import (
	"testing"
)

func TestHeartbeatDesiredRevisionUsesCoordinatorPointerAsFloor(t *testing.T) {
	tests := []struct {
		name         string
		agentDesired int
		pointer      AgentRevisionPointerRow
		found        bool
		want         int
	}{
		{name: "coordinator ahead", agentDesired: 2, pointer: AgentRevisionPointerRow{DesiredRevision: 420}, found: true, want: 420},
		{name: "agent ahead", agentDesired: 421, pointer: AgentRevisionPointerRow{DesiredRevision: 420}, found: true, want: 421},
		{name: "no pointer", agentDesired: 7, pointer: AgentRevisionPointerRow{DesiredRevision: 99}, found: false, want: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := heartbeatDesiredRevision(test.agentDesired, test.pointer, test.found); got != test.want {
				t.Fatalf("heartbeatDesiredRevision() = %d, want %d", got, test.want)
			}
		})
	}
}
