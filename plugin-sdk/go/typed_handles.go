package pluginsdk

// Typed Docker/Compose, HTTP-rule, and dynamic-UI effects are granted only as
// host-mediated handles. Plugin processes must not open a Docker socket,
// execute docker/compose, or own HTTP routing resources.
const (
	PermissionContainerCompose = string(CapabilityContainerCompose)
	PermissionHTTPRule         = string(CapabilityHTTPRule)
	PermissionUIDynamic        = string(CapabilityUIDynamic)
)
