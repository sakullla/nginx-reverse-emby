package pluginsdk

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	HeaderPluginActor             = "X-NRE-Actor"
	HeaderPluginOperationKey      = "X-NRE-Operation-Key"
	HeaderPluginResourceGroup     = "X-NRE-Resource-Group"
	PluginUIContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// SetPluginUIResponseHeaders applies the baseline browser isolation policy for
// plugin-owned pages and APIs.
func SetPluginUIResponseHeaders(header http.Header) {
	SetPluginUIResponseHeadersWithPolicy(header, PluginUIContentSecurityPolicy)
}

// SetPluginUIResponseHeadersWithPolicy applies the baseline isolation headers
// with a plugin-specific CSP when the page needs additional local media types.
func SetPluginUIResponseHeadersWithPolicy(header http.Header, policy string) {
	header.Set("Content-Security-Policy", policy)
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

// ServePluginUIAsset serves the page root or one package-local top-level asset
// from root. Nested and API paths are left to the plugin's business handler.
// The bool reports whether the request was handled.
func ServePluginUIAsset(writer http.ResponseWriter, request *http.Request, assets fs.FS, root string) bool {
	if writer == nil || request == nil || assets == nil || !fs.ValidPath(root) {
		return false
	}
	name := strings.TrimPrefix(request.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) || strings.Contains(name, "/") {
		return false
	}
	target := path.Join(root, name)
	info, err := fs.Stat(assets, target)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	http.ServeFileFS(writer, request, assets, target)
	return true
}

// PluginUIActor returns the canonical Host-projected actor header.
func PluginUIActor(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	actor := strings.TrimSpace(request.Header.Get(HeaderPluginActor))
	return actor, actor != ""
}

// WritePluginUIJSON writes one JSON response using the canonical content type.
func WritePluginUIJSON(writer http.ResponseWriter, status int, payload any) error {
	if writer == nil {
		return errors.New("plugin UI response writer is required")
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	return json.NewEncoder(writer).Encode(payload)
}
