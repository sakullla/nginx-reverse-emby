#!/bin/sh
set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)

cd "$repo_root/tests/internal-pki"

expected_tests='TestInternalPKIMultiProcessLifecycle
TestInternalPKIQUICRelayRoundTrip
TestInternalPKISnapshotDowngradeRecoversHighestDurableSecurityState
TestInternalPKIOfflineLastKnownGoodAndReconnectSafetyPriority
TestInternalPKIEmergencyRotateReenrollsRemoteListenerAndRestoresRelay
TestInternalPKIThirdLifetimeRenewalAndActivePointerCrash
TestInternalPKIForceRotateAndBlockedCAOverlap
TestInternalPKIRestoreCrashSelectsOnlyCompleteGeneration
TestInternalPKIMigrationEpochFenceAndSingleActive'
discovered_tests="$(go test -tags=integration -list '^TestInternalPKI' ./...)"
for test_name in $expected_tests; do
    if ! printf '%s\n' "$discovered_tests" | grep -Fxq "$test_name"; then
        printf 'internal-PKI integration gate did not discover %s\n' "$test_name" >&2
        exit 1
    fi
done

exec go test -tags=integration -count=1 ./...
