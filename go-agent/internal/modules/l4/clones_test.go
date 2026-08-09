package l4

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestCloneL4RulesDeepClonesTrustedProxyPeers(t *testing.T) {
	rules := []model.L4Rule{{Tuning: model.L4Tuning{ProxyProtocol: model.L4ProxyProtocolTuning{
		TrustedPeers: []string{"198.51.100.10"},
	}}}}
	cloned := cloneL4Rules(rules)
	rules[0].Tuning.ProxyProtocol.TrustedPeers[0] = "203.0.113.10"
	if got := cloned[0].Tuning.ProxyProtocol.TrustedPeers[0]; got != "198.51.100.10" {
		t.Fatalf("cloned trusted PROXY peer = %q", got)
	}
	cloned[0].Tuning.ProxyProtocol.TrustedPeers[0] = "192.0.2.10"
	if got := rules[0].Tuning.ProxyProtocol.TrustedPeers[0]; got != "203.0.113.10" {
		t.Fatalf("source trusted PROXY peer = %q", got)
	}
}
