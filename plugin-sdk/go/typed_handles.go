package pluginsdk

// Typed HTTP-rule and dynamic-UI effects are granted only as host-mediated
// handles. Plugin processes must not open a Docker socket, execute
// docker/compose, or own HTTP routing resources.
//
// Node address projection and dedicated dual-stack listens are likewise
// Host-owned: see NodeAddressSource and DualStackListener.
const (
	PermissionHTTPRule  = string(CapabilityHTTPRule)
	PermissionUIDynamic = string(CapabilityUIDynamic)
)
