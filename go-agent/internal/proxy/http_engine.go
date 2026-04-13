package proxy

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const internalRedirectPathSegment = "/__nre_redirect"

func rewriteLocation(rawBackendLocation string, rawFrontendBaseURL string, backendBasePath string) string {
	backendURL, err := url.Parse(rawBackendLocation)
	if err != nil {
		return rawBackendLocation
	}
	frontendURL, err := url.Parse(rawFrontendBaseURL)
	if err != nil {
		return rawBackendLocation
	}
	if frontendURL.Scheme == "" || frontendURL.Host == "" {
		return rawBackendLocation
	}
	backendURL.Scheme = frontendURL.Scheme
	backendURL.Host = frontendURL.Host
	backendURL.Path = translateURLPath(backendURL.Path, backendBasePath, normalizeURLPath(frontendURL.Path))
	backendURL.RawPath = ""
	return backendURL.String()
}

func rewriteExternalLocationToProxyPath(rawLocation string, rawFrontendBaseURL string) string {
	locationURL, err := url.Parse(rawLocation)
	if err != nil || locationURL.Scheme == "" || locationURL.Host == "" {
		return rawLocation
	}
	if !strings.EqualFold(locationURL.Scheme, "http") && !strings.EqualFold(locationURL.Scheme, "https") {
		return rawLocation
	}
	frontendURL, err := url.Parse(rawFrontendBaseURL)
	if err != nil || frontendURL.Scheme == "" || frontendURL.Host == "" {
		return rawLocation
	}
	targetScheme := locationURL.Scheme
	targetHost := locationURL.Host
	targetPath := locationURL.Path
	locationURL.Scheme = frontendURL.Scheme
	locationURL.Host = frontendURL.Host
	locationURL.Path = joinURLPath(
		normalizeURLPath(frontendURL.Path),
		internalRedirectPathSegment,
		"/"+url.PathEscape(targetScheme),
		"/"+url.PathEscape(targetHost),
		targetPath,
	)
	locationURL.RawPath = ""
	return locationURL.String()
}

func joinURLPath(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return "/"
	}
	joined := path.Join(filtered...)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return joined
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
	return frontendBaseURLFromURL(rule.FrontendURL)
}

func HostFromRule(rule model.HTTPRule) string {
	return hostFromFrontendURL(rule.FrontendURL)
}

func FrontendPathFromRule(rule model.HTTPRule) string {
	return frontendPathFromURL(rule.FrontendURL)
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

func frontendBaseURLFromURL(raw string) string {
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
	base := parsed.Scheme + "://" + parsed.Host
	path := normalizeURLPath(parsed.Path)
	if path == "/" {
		return base
	}
	return base + path
}

func frontendPathFromURL(raw string) string {
	if raw == "" {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	return normalizeURLPath(parsed.Path)
}

func normalizeURLPath(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "/"
	}
	cleaned := path.Clean(raw)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func translateURLPath(rawPath, fromBasePath, toBasePath string) string {
	incoming := normalizeURLPath(rawPath)
	fromBase := normalizeURLPath(fromBasePath)
	toBase := normalizeURLPath(toBasePath)

	suffix := incoming
	if pathHasPrefix(incoming, fromBase) {
		suffix = strings.TrimPrefix(incoming, fromBase)
		if suffix == "" {
			suffix = "/"
		} else if !strings.HasPrefix(suffix, "/") {
			suffix = "/" + suffix
		}
	}

	if toBase == "/" {
		return normalizeURLPath(suffix)
	}
	if suffix == "/" {
		return toBase
	}
	return strings.TrimRight(toBase, "/") + suffix
}

func rewriteRequestPath(incomingPath, frontendBasePath, backendBasePath string) string {
	return translateURLPath(incomingPath, frontendBasePath, backendBasePath)
}

func pathHasPrefix(rawPath, prefix string) bool {
	normalizedPath := normalizeURLPath(rawPath)
	normalizedPrefix := normalizeURLPath(prefix)
	if normalizedPrefix == "/" {
		return true
	}
	if normalizedPath == normalizedPrefix {
		return true
	}
	return strings.HasPrefix(normalizedPath, normalizedPrefix+"/")
}

func parseInternalRedirectTarget(rawPath, frontendBasePath string) (*url.URL, bool) {
	prefix := joinURLPath(normalizeURLPath(frontendBasePath), internalRedirectPathSegment)
	if !pathHasPrefix(rawPath, prefix) {
		return nil, false
	}
	remainder := strings.TrimPrefix(normalizeURLPath(rawPath), prefix)
	remainder = strings.TrimPrefix(remainder, "/")
	parts := strings.SplitN(remainder, "/", 3)
	if len(parts) < 2 {
		return nil, false
	}
	scheme, err := url.PathUnescape(parts[0])
	if err != nil {
		return nil, false
	}
	host, err := url.PathUnescape(parts[1])
	if err != nil {
		return nil, false
	}
	if !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
		return nil, false
	}
	targetPath := "/"
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		targetPath = "/" + parts[2]
	}
	return &url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   targetPath,
	}, true
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

func normalizeURLAuthority(target *url.URL) string {
	if target == nil {
		return ""
	}
	host := normalizeHost(target.Hostname())
	if host == "" {
		return ""
	}
	port := target.Port()
	if port == "" {
		port = defaultPortString(target.Scheme)
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
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
	}
	if port := forwardedPort(host, req, incomingScheme); port != "" {
		values["X-Forwarded-Port"] = port
	}
	ip := clientIP(req.RemoteAddr)
	if ip != "" {
		values["X-Forwarded-For"] = ip
		values["X-Real-IP"] = ip
	}
	scheme := strings.TrimSpace(incomingScheme)
	if scheme == "" {
		scheme = requestScheme(req)
	}
	values["X-Forwarded-Proto"] = scheme
	return values
}

func forwardedPort(host string, req *http.Request, incomingScheme string) string {
	if _, port, err := net.SplitHostPort(strings.TrimSpace(host)); err == nil && port != "" {
		return port
	}
	if req != nil && req.URL != nil && req.URL.Port() != "" {
		return req.URL.Port()
	}
	scheme := strings.ToLower(strings.TrimSpace(incomingScheme))
	if scheme == "" {
		scheme = requestScheme(req)
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
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

func makeModifyResponse(frontendBaseURL string, proxyRedirect bool, backendHost string, backendBasePath string) func(*http.Response) error {
	if !proxyRedirect {
		return nil
	}
	return func(resp *http.Response) error {
		location := resp.Header.Get("Location")
		if location == "" || frontendBaseURL == "" {
			return nil
		}
		if backendHost != "" && shouldRewriteLocation(location, backendHost) {
			resp.Header.Set("Location", rewriteLocation(location, frontendBaseURL, backendBasePath))
			return nil
		}
		resp.Header.Set("Location", rewriteExternalLocationToProxyPath(location, frontendBaseURL))
		return nil
	}
}

func shouldRewriteLocation(rawLocation, backendHost string) bool {
	parsed, err := url.Parse(rawLocation)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.Contains(backendHost, ":") {
		return normalizeURLAuthority(parsed) == strings.ToLower(backendHost)
	}
	return normalizeHost(parsed.Host) == normalizeHost(backendHost)
}
