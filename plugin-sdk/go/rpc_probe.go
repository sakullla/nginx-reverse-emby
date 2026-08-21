package pluginsdk

import (
	"errors"
	"strings"
)

// RPCHandshakeProbeFlag selects the canonical build-time RPC identity probe.
// A publisher may append the manifest plugin id and version so the compiled
// guest proves that it accepts the identity carried by the package manifest.
const RPCHandshakeProbeFlag = "--nre-ci-rpc-handshake"

// ResolveRPCHandshakeProbe recognizes the canonical build-time probe and
// returns the Host identity that the guest must negotiate. With no explicit
// identity arguments it preserves the declaration for backwards-compatible
// local self-checks. Publishers should always supply the signed manifest id
// and version as the two trailing arguments.
func ResolveRPCHandshakeProbe(args []string, declaration RPCPluginDeclaration) (RPCPluginDeclaration, bool, error) {
	if len(args) == 0 || args[0] != RPCHandshakeProbeFlag {
		return declaration, false, nil
	}
	if len(args) != 1 && len(args) != 3 {
		return RPCPluginDeclaration{}, true, errors.New("RPC handshake probe requires an optional plugin id and version")
	}
	resolved := declaration
	if len(args) == 3 {
		resolved.PluginID = args[1]
		resolved.PluginVersion = args[2]
	}
	if strings.TrimSpace(resolved.PluginID) == "" || resolved.PluginID != strings.TrimSpace(resolved.PluginID) ||
		strings.TrimSpace(resolved.PluginVersion) == "" || resolved.PluginVersion != strings.TrimSpace(resolved.PluginVersion) {
		return RPCPluginDeclaration{}, true, errors.New("RPC handshake probe identity is required and must be canonical")
	}
	return resolved, true, nil
}
