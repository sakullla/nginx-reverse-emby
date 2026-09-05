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

func datasetFetchAddressAllowed(address netip.Addr, allowPrivate bool) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	if !allowPrivate && (address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast()) {
		return false
	}
	return true
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
