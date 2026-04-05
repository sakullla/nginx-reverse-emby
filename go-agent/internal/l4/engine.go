package l4

import "fmt"

type Rule struct {
	Protocol   string
	RelayChain []int
}

func ValidateRule(rule Rule) error {
	if rule.Protocol == "udp" && len(rule.RelayChain) > 0 {
		return fmt.Errorf("udp relay is not supported")
	}
	return nil
}
