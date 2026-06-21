package http

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func (d Dependencies) staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		path := r.URL.Path
		if strings.HasPrefix(path, "/panel-api/") || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/agent-api/") {
			http.NotFound(w, r)
			return
		}

		panelPublicPath := strings.TrimRight(strings.TrimSpace(d.Config.PanelPublicPath), "/")
		if panelPublicPath != "" {
			switch {
			case path == panelPublicPath || strings.HasPrefix(path, panelPublicPath+"/"):
				path = strings.TrimPrefix(path, panelPublicPath)
				if path == "" {
					path = "/"
				}
			case strings.HasPrefix(path, "/assets/") || path == "/favicon.ico":
			default:
				http.NotFound(w, r)
				return
			}
		}

		distRoot := filepath.Clean(d.Config.FrontendDistDir)
		if distRoot == "" {
			http.NotFound(w, r)
			return
		}

		relativePath := strings.TrimPrefix(filepath.Clean("/"+path), "/")
		if relativePath == "" || relativePath == "." {
			relativePath = "index.html"
		}

		requestedFile := filepath.Join(distRoot, filepath.FromSlash(relativePath))
		if !isWithinRoot(distRoot, requestedFile) {
			writeJSON(w, http.StatusForbidden, errorPayload("forbidden"))
			return
		}

		if info, err := os.Stat(requestedFile); err == nil && !info.IsDir() {
			if relativePath == "index.html" {
				d.serveIndexFile(w, r, requestedFile)
				return
			}
			serveFile(w, r, requestedFile, staticContentType(requestedFile), map[string]string{
				"Cache-Control": "public, max-age=300",
			})
			return
		}

		if filepath.Ext(relativePath) != "" {
			http.NotFound(w, r)
			return
		}

		indexFile := filepath.Join(distRoot, "index.html")
		if _, err := os.Stat(indexFile); err != nil {
			http.NotFound(w, r)
			return
		}

		d.serveIndexFile(w, r, indexFile)
	})
}

func (d Dependencies) serveIndexFile(w http.ResponseWriter, r *http.Request, indexFile string) {
	body, err := os.ReadFile(indexFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body = d.injectPanelBase(body)
	w.Header().Set("Cache-Control", "no-store")
	writeBody(w, r, http.StatusOK, body, "text/html; charset=utf-8")
}

func (d Dependencies) injectPanelBase(body []byte) []byte {
	panelPublicPath := strings.TrimRight(strings.TrimSpace(d.Config.PanelPublicPath), "/")
	if panelPublicPath == "" {
		return body
	}
	baseScript := `<script>window.__NRE_PANEL_BASE__=` + strconv.Quote(panelPublicPath+"/") + `;</script>`
	marker := []byte("</head>")
	if idx := bytes.Index(body, marker); idx >= 0 {
		out := make([]byte, 0, len(body)+len(baseScript))
		out = append(out, body[:idx]...)
		out = append(out, baseScript...)
		out = append(out, body[idx:]...)
		return out
	}
	return append([]byte(baseScript), body...)
}

func (d Dependencies) buildJoinAgentScript(r *http.Request) (string, error) {
	scriptPath, err := d.joinAgentScriptPath()
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}

	baseURL := d.requestBaseURL(r)
	assetBaseURL := baseURL + "/panel-api/public/agent-assets"
	replacer := strings.NewReplacer(
		"__DEFAULT_MASTER_URL__", escapeForDoubleQuotedShell(baseURL),
		"__DEFAULT_ASSET_BASE_URL__", escapeForDoubleQuotedShell(assetBaseURL),
	)
	return replacer.Replace(string(content)), nil
}

func (d Dependencies) joinAgentScriptPath() (string, error) {
	candidates := []string{
		filepath.Join(d.Config.FrontendDistDir, "..", "..", "..", "scripts", "join-agent.sh"),
		filepath.Join("scripts", "join-agent.sh"),
		"/opt/nginx-reverse-emby/scripts/join-agent.sh",
	}

	if _, filePath, _, ok := runtime.Caller(0); ok {
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filePath), "..", "..", "..", "..", ".."))
		candidates = append(candidates, filepath.Join(repoRoot, "scripts", "join-agent.sh"))
	}

	for _, candidate := range candidates {
		cleaned := filepath.Clean(candidate)
		if info, err := os.Stat(cleaned); err == nil && !info.IsDir() {
			return cleaned, nil
		}
	}

	return "", os.ErrNotExist
}

func (d Dependencies) requestBaseURL(r *http.Request) string {
	if publicURL := strings.TrimRight(strings.TrimSpace(d.Config.PublicURL), "/"); publicURL != "" {
		return publicURL
	}
	return requestBaseURL(r, d.Config.TrustForwardedHeaders)
}

func requestBaseURL(r *http.Request, trustForwardedHeaders bool) string {
	proto := "http"
	forwardedHost := ""
	if trustForwardedHeaders {
		proto = firstHeaderValue(r.Header.Get("X-Forwarded-Proto"), "http")
		forwardedHost = firstHeaderValue(r.Header.Get("X-Forwarded-Host"), "")
	}
	requestHost := firstHeaderValue(r.Host, "")
	host := forwardedHost
	if host == "" {
		host = requestHost
	}

	if host == "" {
		return proto + "://127.0.0.1"
	}
	if strings.Contains(host, ":") {
		return proto + "://" + host
	}

	forwardedPort := ""
	if trustForwardedHeaders {
		forwardedPort = firstHeaderValue(r.Header.Get("X-Forwarded-Port"), "")
	}
	switch forwardedPort {
	case "", "80":
		if proto == "http" {
			return proto + "://" + host
		}
	case "443":
		if proto == "https" {
			return proto + "://" + host
		}
	default:
		return proto + "://" + host + ":" + forwardedPort
	}

	return proto + "://" + host
}

func firstHeaderValue(raw string, fallback string) string {
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func escapeForDoubleQuotedShell(value string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`"`, `\"`,
		`$`, `\$`,
		"`", "\\`",
	)
	return replacer.Replace(value)
}

func staticContentType(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".ico":
		return "image/x-icon"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func isWithinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, "..") && relative != "")
}

func serveFile(w http.ResponseWriter, r *http.Request, filePath string, contentType string, headers map[string]string) {
	f, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	for key, value := range headers {
		w.Header().Set(key, value)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

func writeText(w http.ResponseWriter, status int, body string, contentType string) {
	writeBody(w, nil, status, []byte(body), contentType)
}

func writeBody(w http.ResponseWriter, r *http.Request, status int, body []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
