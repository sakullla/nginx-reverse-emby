package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Server struct {
	routes map[string]*routeEntry
}

type routeEntry struct {
	proxy *httputil.ReverseProxy
}

func NewServer(listener model.HTTPListener) *Server {
	s := &Server{routes: make(map[string]*routeEntry)}
	for _, rule := range listener.Rules {
		hostKey := HostFromRule(rule)
		if hostKey == "" || rule.BackendURL == "" {
			continue
		}
		target, err := url.Parse(rule.BackendURL)
		if err != nil {
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			director(req)
			if overrides := HeaderOverridesFromRule(rule, req); len(overrides) > 0 {
				ApplyHeaderOverrides(req, overrides)
			}
		}

		frontendOrigin := FrontendOriginFromRule(rule)
		proxy.ModifyResponse = makeModifyResponse(frontendOrigin, rule.ProxyRedirect)

		s.routes[hostKey] = &routeEntry{proxy: proxy}
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
