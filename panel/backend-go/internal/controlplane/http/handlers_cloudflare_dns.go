package http

import (
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

const (
	cloudflareDNSMountPath     = "/cloudflare-dns"
	cloudflareDNSActorHeader   = "X-NRE-Actor"
	cloudflareDNSGroupHeader   = "X-NRE-Resource-Group"
	cloudflareDNSActor         = "panel/admin"
	cloudflareDNSResourceGroup = "resource-group/cloudflare-dns"
)

func (d Dependencies) handleCloudflareDNS(w http.ResponseWriter, r *http.Request) {
	handler := pluginhost.CloudflareDNSHandler()
	if handler == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("cloudflare-dns plugin is unavailable"))
		return
	}

	forward := r.Clone(r.Context())
	forward.URL.Path = stripCloudflareDNSPrefix(r.URL.Path)
	if r.URL.RawPath != "" {
		forward.URL.RawPath = stripCloudflareDNSPrefix(r.URL.RawPath)
	}
	forward.Header.Set(cloudflareDNSActorHeader, cloudflareDNSActor)
	forward.Header.Set(cloudflareDNSGroupHeader, cloudflareDNSResourceGroup)
	handler.ServeHTTP(w, forward)
}

func stripCloudflareDNSPrefix(path string) string {
	for _, prefix := range []string{"/panel-api" + cloudflareDNSMountPath, "/api" + cloudflareDNSMountPath} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			if rest == "" {
				return "/"
			}
			return rest
		}
	}
	return path
}
