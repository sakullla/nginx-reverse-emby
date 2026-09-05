package rpc

import (
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestLifecycleErrorPolicyProtectsScopedCapabilitiesAndLegacySecrets(t *testing.T) {
	original := errors.New("sensitive callback material")
	candidates := []HostCandidate{
		{Scopes: []string{sdk.PermissionScopedSecretRead}},
		{Scopes: []string{sdk.PermissionScopedSecretWrite}},
		{SecretHandles: []model.PluginSecretHandle{{}}},
	}
	for _, candidate := range candidates {
		for _, phase := range []string{"handshake", "prepare", "activate", "stop"} {
			err := pluginLifecycleCallError(phase, candidateHasSecretCapability(candidate), original)
			if err == nil || strings.Contains(err.Error(), original.Error()) || errors.Is(err, original) {
				t.Fatalf("%s error retained sensitive cause", phase)
			}
			if !strings.Contains(err.Error(), phase) {
				t.Fatal("safe error lost lifecycle phase")
			}
		}
	}
	ordinary := pluginLifecycleCallError("prepare", candidateHasSecretCapability(HostCandidate{Scopes: []string{"agent.read"}}), original)
	if !errors.Is(ordinary, original) {
		t.Fatal("ordinary candidate lost diagnostic cause")
	}
	if pluginLifecycleCallError("prepare", true, nil) != nil {
		t.Fatal("nil lifecycle error became failure")
	}
}
