package upstream

type TrafficClass string

const (
	TrafficClassUnknown     TrafficClass = "unknown"
	TrafficClassInteractive TrafficClass = "interactive"
	TrafficClassBulk        TrafficClass = "bulk"
)
