package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Special-use/platform endpoints are denied even with private-source consent.
// In particular, the shared-address block includes 100.100.100.200; Unmap makes
// IPv4-mapped IPv6 subject to the same policy before any socket is dialed.
var datasetDeniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("fd00:ec2::254/128"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}
var datasetPublicIPv6 = netip.MustParsePrefix("2000::/3")

func datasetFetchAddressAllowed(address netip.Addr, allowPrivate bool) bool {
	address = address.Unmap()
	if !address.IsValid() || address.Zone() != "" || address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, blocked := range datasetDeniedNetworks {
		if blocked.Contains(address) {
			return false
		}
	}
	if address.IsPrivate() || address.IsLoopback() {
		return allowPrivate
	}
	return address.IsGlobalUnicast() && (address.Is4() || datasetPublicIPv6.Contains(address))
}

// Each dial resolves and authorizes addresses before connecting directly to a
// selected IP. No environment proxy or second DNS resolution can bypass the
// check. Redirects require explicit host grants and never downgrade HTTPS.
func fetchDatasetCandidate(ctx context.Context, rawURL string, authority DatasetRetrieval, expected string) ([]byte, error) {
	payload, err := fetchDatasetPayload(ctx, rawURL, authority, pluginsdk.DatasetMaxDownloadBytes, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	if "sha256:"+hex.EncodeToString(sum[:]) != expected {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureDigest, Detail: "dataset download digest mismatch"}
	}
	return payload, nil
}

// fetchDatasetPayload is used for separately bounded untrusted checksum
// metadata and data bytes; data callers must always verify their captured hash.
func fetchDatasetPayload(ctx context.Context, rawURL string, authority DatasetRetrieval, maxBytes int64, timeout time.Duration) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > pluginsdk.DatasetMaxDownloadBytes {
		return nil, errPluginHostDenied
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errPluginHostDenied
	}
	allowed := map[string]bool{strings.ToLower(parsed.Hostname()): true}
	for _, host := range authority.RedirectHosts {
		allowed[host] = true
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errPluginHostDenied
		}
		if !allowed[strings.ToLower(host)] {
			return nil, errPluginHostDenied
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("dataset DNS lookup failed")
		}
		if len(addresses) == 0 {
			return nil, errors.New("dataset DNS lookup returned no addresses")
		}
		for _, ip := range addresses {
			if !datasetFetchAddressAllowed(ip, authority.AllowPrivate) {
				return nil, errPluginHostDenied
			}
		}
		var last error
		for _, ip := range addresses {
			connection, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
			last = err
		}
		return nil, last
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || request.URL.User != nil || request.URL.Fragment != "" || !allowed[strings.ToLower(request.URL.Hostname())] || (request.URL.Scheme != "https" && request.URL.Scheme != "http") || (parsed.Scheme == "https" && request.URL.Scheme != "https") {
			return errPluginHostDenied
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errPluginHostDenied
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errPluginHostDenied) {
			return nil, errPluginHostDenied
		}
		return nil, errors.New("dataset download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureDownload, Detail: "dataset response status or size is invalid"}
	}
	if response.ContentLength > maxBytes {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureBudget, Detail: "dataset response exceeds byte budget"}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("dataset download interrupted")
	}
	if int64(len(payload)) > maxBytes {
		return nil, &datasets.Error{Code: pluginsdk.DatasetFailureBudget, Detail: "dataset download byte budget exceeded"}
	}
	return payload, nil
}
