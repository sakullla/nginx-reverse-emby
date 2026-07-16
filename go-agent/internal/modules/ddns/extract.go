package ddns

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	// defaultExtractTimeout bounds a single public echo probe so a hung upstream
	// cannot stall the heartbeat apply chain.
	defaultExtractTimeout = 8 * time.Second
	// publicAPIBodyLimit caps how many bytes we read from an echo endpoint.
	publicAPIBodyLimit = 64

	sourcePublicAPI = "public_api"
	sourceInterface = "interface"
)

// ExtractIPv4 resolves the agent's IPv4 address for the given family config.
// Returns "" when disabled, unsupported, or on any failure — extraction is
// best-effort and must never panic.
func ExtractIPv4(ctx context.Context, family model.DDNSFamily, client *http.Client, publicAPIURL string) string {
	return extractFamily(ctx, family, client, publicAPIURL, false)
}

// ExtractIPv6 resolves the agent's IPv6 address for the given family config.
// Same best-effort contract as ExtractIPv4.
func ExtractIPv6(ctx context.Context, family model.DDNSFamily, client *http.Client, publicAPIURL string) string {
	return extractFamily(ctx, family, client, publicAPIURL, true)
}

func extractFamily(ctx context.Context, family model.DDNSFamily, client *http.Client, publicAPIURL string, wantV6 bool) string {
	if !family.Enabled {
		return ""
	}
	switch family.Source {
	case "", sourcePublicAPI:
		return extractPublicAPI(ctx, client, publicAPIURL, wantV6)
	case sourceInterface:
		return extractInterface(family.Interface, wantV6)
	default:
		return ""
	}
}

func extractPublicAPI(ctx context.Context, client *http.Client, urlsCSV string, wantV6 bool) string {
	if client == nil {
		return ""
	}
	// Try each endpoint in the caller's priority order; the first to return a
	// valid IP for the requested family wins. A hung/garbage upstream simply
	// yields "" so we fall through to the next, giving single-point resilience
	// when multiple URLs are configured (comma-separated).
	for _, url := range splitPublicAPIURLs(urlsCSV) {
		if ip := probePublicAPI(ctx, client, url, wantV6); ip != "" {
			return ip
		}
	}
	return ""
}

// probePublicAPI hits a single echo endpoint and returns the validated IP for
// the requested family, or "" on any failure. Best-effort and timeout-bounded
// so a slow upstream never stalls the heartbeat apply chain.
func probePublicAPI(ctx context.Context, client *http.Client, url string, wantV6 bool) string {
	if strings.TrimSpace(url) == "" {
		return ""
	}
	reqCtx, cancel := context.WithTimeout(ctx, defaultExtractTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, publicAPIBodyLimit))
	if err != nil {
		return ""
	}
	candidate := strings.TrimSpace(string(body))
	if !isValidIPFamily(candidate, wantV6) {
		return ""
	}
	return candidate
}

// splitPublicAPIURLs parses a comma-separated list of public echo endpoints,
// trimming whitespace, dropping empties, and de-duplicating while preserving the
// caller's priority order. A single URL (the common case) yields a one-element
// slice. Examples:
//
//	"https://a, https://b"      -> ["https://a", "https://b"]
//	"https://a,, https://a"     -> ["https://a"]
//	"  "                        -> []
func splitPublicAPIURLs(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		url := strings.TrimSpace(p)
		if url == "" {
			continue
		}
		if _, dup := seen[url]; dup {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, url)
	}
	return out
}

func extractInterface(name string, wantV6 bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ip := addrIP(addr)
		if ip == nil {
			continue
		}
		if !isFamily(ip, wantV6) {
			continue
		}
		if isUnusable(ip) {
			continue
		}
		return ip.String()
	}
	return ""
}

// addrIP extracts the net.IP from an interface address, tolerating both
// *net.IPNet (interface addresses) and *net.IPAddr forms.
func addrIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

func isFamily(ip net.IP, wantV6 bool) bool {
	if wantV6 {
		return ip.To4() == nil && ip.To16() != nil
	}
	return ip.To4() != nil
}

func isValidIPFamily(value string, wantV6 bool) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return false
	}
	return isFamily(ip, wantV6)
}

// isUnusable skips addresses that are never useful as a DDNS target: loopback,
// unspecified, multicast, and link-local (e.g. IPv4 169.254.0.0/16, IPv6
// fe80::/10). Private/RFC1918 addresses are intentionally retained — on a NAT
// box the operator may deliberately publish the LAN-facing interface address.
func isUnusable(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast()
}
