package l4

import (
	"fmt"
	"strings"
)

type Rule struct {
	Protocol   string
	RelayChain []int
}

func ValidateRule(rule Rule) error {
	if strings.EqualFold(rule.Protocol, "udp") && len(rule.RelayChain) > 0 {
		return fmt.Errorf("udp relay is not supported")
	}
	return nil
}
