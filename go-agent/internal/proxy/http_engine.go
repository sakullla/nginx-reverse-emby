package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

func rewriteLocation(rawBackendLocation string, rawFrontendOrigin string) string {
	backendURL, err := url.Parse(rawBackendLocation)
	if err != nil {
		return rawBackendLocation
	}
	frontendURL, err := url.Parse(rawFrontendOrigin)
	if err != nil {
		return rawBackendLocation
	}
	if frontendURL.Scheme == "" || frontendURL.Host == "" {
		return rawBackendLocation
	}
	backendURL.Scheme = frontendURL.Scheme
	backendURL.Host = frontendURL.Host
	return backendURL.String()
}

func ApplyHeaderOverrides(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		if strings.EqualFold(key, "Host") {
			req.Host = value
		}
		req.Header.Set(key, value)
	}
}
