//go:build !integration

package ddns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPublicAPIExtractorFamilyFallbackAndDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v4":
			_, _ = w.Write([]byte("203.0.113.10"))
		case "/v6":
			_, _ = w.Write([]byte("2001:db8::10"))
		case "/error":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/slow":
			<-request.Context().Done()
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	family := model.DDNSFamily{Enabled: true, Source: sourcePublicAPI}

	for _, tc := range []struct {
		name   string
		wantV6 bool
		urls   string
		want   string
	}{
		{name: "IPv4 family", urls: server.URL + "/v4", want: "203.0.113.10"},
		{name: "IPv6 family", wantV6: true, urls: server.URL + "/v6", want: "2001:db8::10"},
		{name: "family mismatch falls back", urls: server.URL + "/v6," + server.URL + "/v4", want: "203.0.113.10"},
		{name: "endpoint failure falls back", urls: server.URL + "/error," + server.URL + "/v4", want: "203.0.113.10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			if tc.wantV6 {
				got = ExtractIPv6(t.Context(), family, server.Client(), tc.urls)
			} else {
				got = ExtractIPv4(t.Context(), family, server.Client(), tc.urls)
			}
			if got != tc.want {
				t.Fatalf("extracted address = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("fallbacks share caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()
		started := time.Now()
		got := ExtractIPv4(ctx, family, server.Client(), server.URL+"/slow,"+server.URL+"/slow")
		if elapsed := time.Since(started); got != "" || elapsed > 200*time.Millisecond {
			t.Fatalf("deadline extraction = %q in %s", got, elapsed)
		}
	})
}
