package control

const (
	TaskTypeDiagnoseHTTPRule         = "diagnose_http_rule"
	TaskTypeDiagnoseL4TCPRule        = "diagnose_l4_tcp_rule"
	TaskTypePKISecurityUpdate        = "pki_security_update"
	TaskTypePKIForceRotation         = "pki_force_rotation"
	TaskTypePluginCall               = "plugin.call"
	TaskTypeProbeBackendReachability = "probe_backend_reachability"
)

const (
	ProbeStatusConnected    = "connected"
	ProbeStatusHTTPStatus   = "http_status"
	ProbeStatusNetworkError = "network_error"
	ProbeStatusUntested     = "untested"
)

const (
	ProbeResultAgentID         = "agent_id"
	ProbeResultTarget          = "target"
	ProbeResultCheckedAt       = "checked_at"
	ProbeResultStatus          = "status"
	ProbeResultHTTPStatusCode  = "http_status_code"
	ProbeResultRelayListenerID = "relay_listener_id"
	ProbeResultError           = "error"
)
