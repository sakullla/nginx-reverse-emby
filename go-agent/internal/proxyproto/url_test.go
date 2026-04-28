package proxyproto

import "testing"

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ProxyURL
	}{
		{
			name: "socks alias uses SOCKS5 defaults",
			raw:  "socks://user:pass@127.0.0.1:1080",
			want: ProxyURL{
				Scheme:       "socks",
				Address:      "127.0.0.1:1080",
				Username:     "user",
				Password:     "pass",
				SOCKSVersion: 5,
				Original:     "socks://user:pass@127.0.0.1:1080",
			},
		},
		{
			name: "socks4 accepts username and local DNS",
			raw:  "socks4://user@127.0.0.1:1080",
			want: ProxyURL{
				Scheme:       "socks4",
				Address:      "127.0.0.1:1080",
				Username:     "user",
				SOCKSVersion: 4,
				Original:     "socks4://user@127.0.0.1:1080",
			},
		},
		{
			name: "socks4a resolves through proxy",
			raw:  "socks4a://user@proxy.local:1080",
			want: ProxyURL{
				Scheme:       "socks4a",
				Address:      "proxy.local:1080",
				Username:     "user",
				RemoteDNS:    true,
				SOCKSVersion: 4,
				Original:     "socks4a://user@proxy.local:1080",
			},
		},
		{
			name: "socks5 supports username and password",
			raw:  "socks5://user:pass@127.0.0.1:1080",
			want: ProxyURL{
				Scheme:       "socks5",
				Address:      "127.0.0.1:1080",
				Username:     "user",
				Password:     "pass",
				SOCKSVersion: 5,
				Original:     "socks5://user:pass@127.0.0.1:1080",
			},
		},
		{
			name: "socks5h resolves through proxy",
			raw:  "socks5h://user:pass@proxy.local:1080",
			want: ProxyURL{
				Scheme:       "socks5h",
				Address:      "proxy.local:1080",
				Username:     "user",
				Password:     "pass",
				RemoteDNS:    true,
				SOCKSVersion: 5,
				Original:     "socks5h://user:pass@proxy.local:1080",
			},
		},
		{
			name: "http uses CONNECT",
			raw:  "http://user:pass@proxy.local:8080",
			want: ProxyURL{
				Scheme:      "http",
				Address:     "proxy.local:8080",
				Username:    "user",
				Password:    "pass",
				HTTPConnect: true,
				Original:    "http://user:pass@proxy.local:8080",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProxyURL(tt.raw)
			if err != nil {
				t.Fatalf("ParseProxyURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseProxyURL() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseProxyURLRejectsInvalidInput(t *testing.T) {
	tests := []string{
		"",
		"ftp://proxy.local:21",
		"socks5://proxy.local",
		"socks5://:1080",
		"socks5://proxy.local:",
		"socks5://proxy.local:notaport",
		"socks5://proxy.local:0",
		"socks5://proxy.local:65536",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := ParseProxyURL(raw); err == nil {
				t.Fatalf("ParseProxyURL() = %#v, want error", got)
			}
		})
	}
}

func TestRedactProxyURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{
			raw:  "socks://user:pass@127.0.0.1:1080",
			want: "socks://user:xxxxx@127.0.0.1:1080",
		},
		{
			raw:  "socks4://user@127.0.0.1:1080",
			want: "socks4://user@127.0.0.1:1080",
		},
		{
			raw:  "http://proxy.local:8080",
			want: "http://proxy.local:8080",
		},
		{
			raw:  "ftp://user:pass@proxy.local:21",
			want: "ftp://user:xxxxx@proxy.local:21",
		},
		{
			raw:  "not a url",
			want: "not a url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := RedactProxyURL(tt.raw); got != tt.want {
				t.Fatalf("RedactProxyURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
