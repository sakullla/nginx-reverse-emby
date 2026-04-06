package proxy

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

func FrontendOriginFromRule(rule model.HTTPRule) string {
	return frontendOriginFromURL(rule.FrontendURL)
}

func HostFromRule(rule model.HTTPRule) string {
	return hostFromFrontendURL(rule.FrontendURL)
}

func HeaderOverridesFromRule(rule model.HTTPRule, req *http.Request, incomingHost, incomingScheme string) map[string]string {
	if req == nil {
		return nil
	}
	overrides := make(map[string]string)
	if rule.UserAgent != "" {
		overrides["User-Agent"] = rule.UserAgent
	}
	for _, hdr := range rule.CustomHeaders {
		overrides[hdr.Name] = hdr.Value
	}
	if rule.PassProxyHeaders {
		for key, value := range passProxyHeaders(req, incomingHost, incomingScheme) {
			overrides[key] = value
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

func hostFromFrontendURL(raw string) string {
	if raw == "" {
		return ""
	}
	url, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return normalizeHost(url.Host)
}

func frontendOriginFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func normalizeHost(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func passProxyHeaders(req *http.Request, incomingHost, incomingScheme string) map[string]string {
	values := make(map[string]string)
	if req == nil {
		return values
	}
	host := strings.TrimSpace(incomingHost)
	if host == "" {
		host = strings.TrimSpace(req.Host)
		if host == "" && req.URL != nil {
			host = strings.TrimSpace(req.URL.Host)
		}
	}
	if host != "" {
		values["X-Forwarded-Host"] = host
		if _, port, err := net.SplitHostPort(host); err == nil {
			values["X-Forwarded-Port"] = port
		}
	}
	ip := clientIP(req.RemoteAddr)
	if ip != "" {
		xfwd := req.Header.Get("X-Forwarded-For")
		if xfwd == "" {
			values["X-Forwarded-For"] = ip
		} else {
			values["X-Forwarded-For"] = xfwd + ", " + ip
		}
		values["X-Real-IP"] = ip
	}
	scheme := strings.TrimSpace(incomingScheme)
	if scheme == "" {
		scheme = requestScheme(req)
	}
	values["X-Forwarded-Proto"] = scheme
	return values
}

func clientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func requestScheme(req *http.Request) string {
	if req == nil {
		return "http"
	}
	if req.TLS != nil {
		return "https"
	}
	if req.URL != nil && req.URL.Scheme != "" {
		return strings.ToLower(req.URL.Scheme)
	}
	return "http"
}

func makeModifyResponse(frontendOrigin string, proxyRedirect bool, backendHost string) func(*http.Response) error {
	if !proxyRedirect {
		return nil
	}
	return func(resp *http.Response) error {
		location := resp.Header.Get("Location")
		if location == "" || frontendOrigin == "" || backendHost == "" {
			return nil
		}
		if shouldRewriteLocation(location, backendHost) {
			resp.Header.Set("Location", rewriteLocation(location, frontendOrigin))
		}
		return nil
	}
}

func shouldRewriteLocation(rawLocation, backendHost string) bool {
	parsed, err := url.Parse(rawLocation)
	if err != nil || parsed.Host == "" {
		return false
	}
	return normalizeHost(parsed.Host) == normalizeHost(backendHost)
}
