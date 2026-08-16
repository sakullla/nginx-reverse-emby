//go:build !integration

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestValidatorCLIRejectsConflictingSelectorsAndEncodesJSONFailures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--official-lock", "lock.json", "--market", "market.yaml"}, &stdout, &stderr); code != 2 {
		t.Fatalf("conflict exit=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--trusted-key", plugins.OfficialSignatureKeyID + "=AAAA"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "official signature root cannot be overridden") {
		t.Fatalf("official-root override exit=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--trusted-key", "not-a-key"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "key-id=base64-public-key") {
		t.Fatalf("trusted-key syntax exit=%d stderr=%s", code, stderr.String())
	}

	for _, args := range [][]string{
		{"--root", root},
		{"--market", "not-market.yaml"},
		{"--official-lock", "missing-lock.json"},
	} {
		stdout.Reset()
		stderr.Reset()
		code := run(args, &stdout, &stderr)
		var output validationOutput
		if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
			t.Fatalf("run(%v) json=%q err=%v", args, stdout.String(), err)
		}
		if code != 1 || output.Valid || output.Error == "" {
			t.Fatalf("run(%v) exit=%d output=%+v stderr=%s", args, code, output, stderr.String())
		}
	}
}
