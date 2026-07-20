//go:build linux

package app

import (
	"testing"
	"time"
)

func TestHotRestartPacketFailureAndRepeatMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("packet hot-restart failure and repeat matrix")
	}
	cases := []hotRestartPacketTestProcess{
		{
			name:    "process_crash_timeout_ack",
			args:    []string{"./internal/hotrestart", "^TestSupervisorRejectsChildCrashBeforeReadiness$|^TestSupervisorKillsChildOnReadinessTimeout$|^TestPostReadinessFailuresAbortChildAndRecoverParentAuthority$|^TestLostAcknowledgementsConvergeFromDurablePhases$|^TestChildRecoversActivationAndAuthorityAfterRealParentExit$|^TestConcurrentBrokenPipeTransitionSharesFailureAndRecoversParent$|^TestSupervisorPassesAuthenticatedPacketDescriptorFiles$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "packet_authority_barrier_rollback",
			args:    []string{"./internal/hotrestart", "^TestPacketHandoffGatesForwardingThenTakesPhysicalAuthority$|^TestPacketHandoffDrainsQueuedForwardingBeforeParentCrashTakeover$|^TestPacketHandoffRejectsDescriptorFileIndexAndIdentityTampering$|^TestPacketForwarderAppliesBoundedBackpressure$|^TestPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "registry_forwarding_and_recovery",
			args:    []string{"./internal/ingress", "^TestProcessPacketHandoffKeepsOldAssociationForwardsNewAndTransfersAuthority$|^TestProcessPacketForwardingRollbackRestoresParent$|^TestProcessPacketPauseWaitsForLastForwardBeforeBarrier$|^TestProcessPacketActivationAndPauseCompensateEarlierGates$|^TestProcessPacketAuthorityReservationPreventsPartialTakeover$|^TestPacketBrokerPinsAssociationAcrossCutover$|^TestPacketBrokerSelectorPinsAssociationsAndKeepsCandidateInvisible$|^TestPacketBrokerSelectorSwapIsRaceSafe$"},
			timeout: 2 * time.Minute,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}

func TestHotRestartPacketRepeatedUpgradeCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("repeated packet upgrade cleanup")
	}
	cases := []hotRestartPacketTestProcess{
		{
			name:    "repeated_fd_and_control_descriptor_lifecycle",
			args:    []string{"./internal/hotrestart", "^TestPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline$|^TestSuccessfulWaitClosesParentControlDescriptors$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "http3_parent_child_successor",
			args:    []string{"./internal/modules/http", "^TestHTTP3ProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "l4_udp_parent_child_successor",
			args:    []string{"./internal/modules/l4", "^TestL4UDPProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "relay_quic_parent_child_successor",
			args:    []string{"./internal/modules/relay", "^TestRelayQUICProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}
