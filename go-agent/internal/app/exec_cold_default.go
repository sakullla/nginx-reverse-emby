//go:build !linux

package app

import "errors"

func execColdReplacement(string, []string, []string) error {
	return errors.New("cold process replacement is only supported on linux")
}
