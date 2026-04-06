package l4

import (
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Rule = model.L4Rule

func ValidateRule(rule Rule) error {
	if strings.EqualFold(rule.Protocol, "udp") && len(rule.RelayChain) > 0 {
		return fmt.Errorf("udp relay is not supported")
	}
	return nil
}
