package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Server struct {
	routes map[string]*routeEntry
}

type routeEntry struct {
	proxy          *httputil.ReverseProxy
	proxyRedirect  bool
	frontendOrigin string
}

func NewServer(listener model.HTTPListener) *Server {
	s := &Server{routes: make(map[string]*routeEntry)}
	defaults := listener.HTTPProxyConfig
	for _, rt := range listener.Routes {
		hostKey := normalizeHost(rt.Host)
		if hostKey == "" || rt.BackendURL == "" {
			continue
		}
		target, err := url.Parse(rt.BackendURL)
		if err != nil {
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		combinedHeaders := combineHeaderOverrides(defaults.HeaderOverrides, rt.HeaderOverrides)
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			director(req)
			if len(combinedHeaders) > 0 {
				ApplyHeaderOverrides(req, combinedHeaders)
			}
		}

		frontendOrigin := defaults.FrontendOrigin
		if rt.FrontendOrigin != "" {
			frontendOrigin = rt.FrontendOrigin
		}
		proxy.ModifyResponse = makeModifyResponse(frontendOrigin, rt.ProxyRedirect)

		s.routes[hostKey] = &routeEntry{
			proxy:          proxy,
			proxyRedirect:  rt.ProxyRedirect,
			frontendOrigin: frontendOrigin,
		}
	}

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry, ok := s.routes[host]; ok {
		entry.proxy.ServeHTTP(w, req)
		return
	}
	http.NotFound(w, req)
}

func combineHeaderOverrides(base, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

func normalizeHost(raw string) string {
	if raw == "" {
		return ""
	}
	host := strings.TrimSpace(raw)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func makeModifyResponse(frontendOrigin string, proxyRedirect bool) func(*http.Response) error {
	if !proxyRedirect {
		return nil
	}
	return func(resp *http.Response) error {
		location := resp.Header.Get("Location")
		if location == "" || frontendOrigin == "" {
			return nil
		}
		resp.Header.Set("Location", rewriteLocation(location, frontendOrigin))
		return nil
	}
}
