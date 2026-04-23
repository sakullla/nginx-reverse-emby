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
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	switch method {
	case http.MethodGet:
		path := strings.ToLower(req.URL.Path)
		if strings.Contains(path, "/stream") || strings.Contains(path, "/download") {
			return TrafficClassBulk
		}
		return TrafficClassInteractive
	case http.MethodHead, http.MethodOptions:
		return TrafficClassInteractive
	}
	if req.ContentLength < 0 {
		return TrafficClassUnknown
	}
	if req.ContentLength >= 0 && req.ContentLength <= 64*1024 {
		return TrafficClassInteractive
	}
	return TrafficClassBulk
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
