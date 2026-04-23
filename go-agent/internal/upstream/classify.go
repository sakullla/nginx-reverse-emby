package upstream

import (
	"net/http"
	"strings"
)

func ClassifyHTTPRequest(req *http.Request) TrafficClass {
	if req == nil {
		return TrafficClassUnknown
	}
	if strings.TrimSpace(req.Header.Get("Range")) != "" {
		return TrafficClassBulk
	}
	return TrafficClassInteractive
}

func ClassifyL4(protocol string, bytesTransferred int64, durationSeconds int64) TrafficClass {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "udp":
		return TrafficClassBulk
	case "tcp":
		if bytesTransferred >= 128*1024 || durationSeconds >= 5 {
			return TrafficClassBulk
		}
		return TrafficClassInteractive
	default:
		return TrafficClassUnknown
	}
}
