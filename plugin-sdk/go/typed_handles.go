package pluginsdk

// Typed HTTP-rule, L4-rule, reverse-channel, and dynamic-UI effects are
// granted only as host-mediated handles. Plugin processes must not open a
// Docker socket, execute docker/compose, or own HTTP/L4 routing resources.
//
// Node address projection and dedicated dual-stack listens are likewise
// Host-owned: see NodeAddressSource and DualStackListener.
const (
	PermissionHTTPRule       = string(CapabilityHTTPRule)
	PermissionL4Rule         = string(CapabilityL4Rule)
	PermissionChannelReverse = string(CapabilityChannelReverse)
	PermissionUIDynamic      = string(CapabilityUIDynamic)
)
