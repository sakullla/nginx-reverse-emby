//go:build linux

package app

import (
	"fmt"
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
			name:    "repeated_fd_lifecycle",
			args:    []string{"./internal/hotrestart", "^TestPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline$|^TestSuccessfulWaitClosesParentControlDescriptors$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "repeated_protocol_handoff",
			args:    []string{"./internal/modules/wireguard", "^TestProcessWireGuardBindHandoffPinsOldAndForwardsNew$|^TestProcessWireGuardClassifierCleanupLinearizesRealBrokerReassociation$"},
			timeout: 3 * time.Minute,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcessCount(t, testCase, 2)
		})
	}
}

func runHotRestartPacketTestProcessCount(t *testing.T, testCase hotRestartPacketTestProcess, count int) {
	t.Helper()
	for iteration := 1; iteration <= count; iteration++ {
		t.Run(fmt.Sprintf("%s_%d", hotRestartPacketProcessDescription(testCase), iteration), func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}
