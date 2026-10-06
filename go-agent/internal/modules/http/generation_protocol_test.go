//go:build integration

package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestIntegrationHTTPGenerationSwitchesProtocolOnSamePort(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real listeners and TLS")
	}
	for _, processRegistry := range []bool{false, true} {
		for _, http3 := range []bool{false, true} {
			t.Run(fmt.Sprintf("process_registry=%t/http3=%t", processRegistry, http3), func(t *testing.T) {
				backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
					_, _ = io.WriteString(w, "ok")
				}))
				defer backend.Close()
				port := pickFreeTCPUDPPort(t)
				host := "protocol.example.test"
				mod := NewModule(Config{HTTP3Enabled: http3})
				defer mod.Close()
				if processRegistry {
					mod.SetProcessStreamRegistry(ingress.NewProcessStreamRegistry())
				}
				resolver := generationTestResolver{module.ProviderTLSMaterial: &testTLSProvider{certificates: map[string]tls.Certificate{
					host: mustIssueProxyTLSCertificate(t, host),
				}}}
				transport := &stdhttp.Transport{
					DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
					},
					TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // Test-only certificate.
					DisableKeepAlives: true,
				}
				defer transport.CloseIdleConnections()
				client := &stdhttp.Client{Transport: transport, Timeout: 3 * time.Second}
				check := func(scheme string) {
					t.Helper()
					response, err := client.Get(fmt.Sprintf("%s://%s:%d/", scheme, host, port))
					if err != nil {
						t.Fatalf("%s request: %v", scheme, err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != stdhttp.StatusOK || string(body) != "ok" {
						t.Fatalf("%s response: status=%d body=%q error=%v", scheme, response.StatusCode, body, err)
					}
				}
				previous := model.Snapshot{}
				previousScheme := ""
				for i, scheme := range []string{"http", "https", "http", "https"} {
					next := model.Snapshot{Revision: int64(i + 1), Rules: []model.HTTPRule{{
						ID: 1, Enabled: true, FrontendURL: fmt.Sprintf("%s://%s:%d", scheme, host, port),
						Backends: []model.HTTPBackend{{URL: backend.URL}},
					}}}
					tx := prepareHTTPGenerationForTest(t, mod, resolver, previous, next)
					if previousScheme != "" {
						check(previousScheme)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						check(previousScheme)
						tx = prepareHTTPGenerationForTest(t, mod, resolver, previous, next)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					tx.FinalizeCommitSuccess()
					check(scheme)
					previous, previousScheme = next, scheme
				}
			})
		}
	}
}
