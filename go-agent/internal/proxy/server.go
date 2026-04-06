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
	proxy       *httputil.ReverseProxy
	backendHost string
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
		targetHost := normalizeHost(target.Host)

		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			incomingHost := req.Host
			incomingScheme := requestScheme(req)
			director(req)
			if overrides := HeaderOverridesFromRule(rule, req, incomingHost, incomingScheme); len(overrides) > 0 {
				ApplyHeaderOverrides(req, overrides)
			}
		}

		frontendOrigin := FrontendOriginFromRule(rule)
		proxy.ModifyResponse = makeModifyResponse(frontendOrigin, rule.ProxyRedirect, targetHost)

		s.routes[hostKey] = &routeEntry{proxy: proxy, backendHost: targetHost}
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
