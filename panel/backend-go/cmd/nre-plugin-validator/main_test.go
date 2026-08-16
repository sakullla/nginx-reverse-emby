//go:build !integration

package main

import (
	"bytes"
	"testing"
)

func TestValidatorCLIRejectsConflictingSelectors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--official-lock", "lock.json", "--market", "market.yaml"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
}
