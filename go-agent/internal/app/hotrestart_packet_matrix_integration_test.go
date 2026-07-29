//go:build integration && linux

package app

import (
	"testing"
	"time"
)

func TestIntegrationHotRestartPacketFailureAndRepeatMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("packet hot-restart failure and repeat matrix")
	}
	cases := []hotRestartPacketTestProcess{
		{
			name:    "process_crash_timeout_ack",
			args:    []string{"./internal/hotrestart", "^TestIntegrationSupervisorRejectsChildCrashBeforeReadiness$|^TestIntegrationSupervisorKillsChildOnReadinessTimeout$|^TestIntegrationPostReadinessFailuresAbortChildAndRecoverParentAuthority$|^TestIntegrationLostAcknowledgementsConvergeFromDurablePhases$|^TestIntegrationChildRecoversActivationAndAuthorityAfterRealParentExit$|^TestIntegrationConcurrentBrokenPipeTransitionSharesFailureAndRecoversParent$|^TestIntegrationSupervisorPassesAuthenticatedPacketDescriptorFiles$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "packet_authority_barrier_rollback",
			args:    []string{"./internal/hotrestart", "^TestIntegrationPacketHandoffGatesForwardingThenTakesPhysicalAuthority$|^TestIntegrationPacketHandoffDrainsQueuedForwardingBeforeParentCrashTakeover$|^TestIntegrationPacketHandoffRejectsDescriptorFileIndexAndIdentityTampering$|^TestIntegrationPacketForwarderAppliesBoundedBackpressure$|^TestIntegrationPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "registry_forwarding_and_recovery",
			args:    []string{"./internal/ingress", "^TestIntegrationProcessPacketHandoffKeepsOldAssociationForwardsNewAndTransfersAuthority$|^TestIntegrationProcessPacketForwardingRollbackRestoresParent$|^TestIntegrationProcessPacketPauseWaitsForLastForwardBeforeBarrier$|^TestProcessPacketActivationAndPauseCompensateEarlierGates$|^TestProcessPacketAuthorityReservationPreventsPartialTakeover$|^TestPacketBrokerPinsAssociationAcrossCutover$|^TestPacketBrokerSelectorPinsAssociationsAndKeepsCandidateInvisible$|^TestPacketBrokerSelectorSwapIsRaceSafe$"},
			timeout: 2 * time.Minute,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}

func TestIntegrationHotRestartPacketRepeatedUpgradeCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("repeated packet upgrade cleanup")
	}
	cases := []hotRestartPacketTestProcess{
		{
			name:    "repeated_fd_and_control_descriptor_lifecycle",
			args:    []string{"./internal/hotrestart", "^TestIntegrationPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline$|^TestIntegrationSuccessfulWaitClosesParentControlDescriptors$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "http3_parent_child_successor",
			args:    []string{"./internal/modules/http", "^TestIntegrationHTTP3ProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "l4_udp_parent_child_successor",
			args:    []string{"./internal/modules/l4", "^TestIntegrationL4UDPProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
		{
			name:    "relay_quic_parent_child_successor",
			args:    []string{"./internal/modules/relay", "^TestIntegrationRelayQUICProcessPacketHandoffRoutesOldNewAndAbort$"},
			timeout: 3 * time.Minute,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}
