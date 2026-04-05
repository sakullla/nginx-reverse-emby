package proxy

import (
	"net/http"
	"net/url"
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
	backendURL.Scheme = frontendURL.Scheme
	backendURL.Host = frontendURL.Host
	return backendURL.String()
}

func ApplyHeaderOverrides(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		req.Header.Set(key, value)
	}
}
