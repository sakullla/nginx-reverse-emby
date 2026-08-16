//go:build integration

package l4

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestValidateRuleAcceptsRepresentativeUDPRelay(t *testing.T) {
	err := ValidateRule(Rule{
		Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 9000,
		RelayLayers: [][]int{{1}},
		Backends:    []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}
