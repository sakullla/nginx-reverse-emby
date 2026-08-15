package rpc

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"testing"
)

const (
	acceleratorSourcesSDKPath    = "github.com/sakullla/nginx-reverse-emby/plugin-sdk"
	acceleratorSourcesSDKVersion = "v0.6.0"
)

func validateAcceleratorSourcesArtifact(path, expectedDigest string) (string, error) {
	expectedDigest = strings.ToLower(strings.TrimSpace(expectedDigest))
	decoded, err := hex.DecodeString(expectedDigest)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("accelerator artifact expected SHA-256 is invalid")
	}
	artifact, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, artifact)
	closeErr := artifact.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	actualDigest := hex.EncodeToString(hash.Sum(nil))
	if actualDigest != expectedDigest {
		return "", fmt.Errorf("accelerator artifact SHA-256 mismatch: got %s", actualDigest)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read accelerator artifact build info: %w", err)
	}
	if err := validateAcceleratorSourcesBuildInfo(info); err != nil {
		return "", err
	}
	return actualDigest, nil
}

func validateAcceleratorSourcesBuildInfo(info *debug.BuildInfo) error {
	if info == nil {
		return errors.New("accelerator artifact build info is missing")
	}
	for _, dependency := range info.Deps {
		if dependency == nil || dependency.Path != acceleratorSourcesSDKPath {
			continue
		}
		if dependency.Replace != nil {
			return errors.New("accelerator artifact plugin SDK must not use replace")
		}
		if dependency.Version != acceleratorSourcesSDKVersion {
			return fmt.Errorf("accelerator artifact plugin SDK = %q, want %q", dependency.Version, acceleratorSourcesSDKVersion)
		}
		return nil
	}
	return errors.New("accelerator artifact plugin SDK dependency is missing")
}

func TestAcceleratorSourcesBuildInfoRequiresExactReleasedSDK(t *testing.T) {
	valid := &debug.BuildInfo{Deps: []*debug.Module{{Path: acceleratorSourcesSDKPath, Version: acceleratorSourcesSDKVersion}}}
	if err := validateAcceleratorSourcesBuildInfo(valid); err != nil {
		t.Fatalf("valid SDK provenance rejected: %v", err)
	}
	for name, info := range map[string]*debug.BuildInfo{
		"stale SDK":       {Deps: []*debug.Module{{Path: acceleratorSourcesSDKPath, Version: "v0.2.0"}}},
		"development SDK": {Deps: []*debug.Module{{Path: acceleratorSourcesSDKPath, Version: "(devel)"}}},
		"replaced SDK":    {Deps: []*debug.Module{{Path: acceleratorSourcesSDKPath, Version: acceleratorSourcesSDKVersion, Replace: &debug.Module{Path: "../plugin-sdk", Version: "(devel)"}}}},
		"missing SDK":     {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAcceleratorSourcesBuildInfo(info); err == nil {
				t.Fatal("untrusted SDK provenance accepted")
			}
		})
	}
}

func TestAcceleratorSourcesArtifactRejectsWrongExpectedDigest(t *testing.T) {
	if _, err := validateAcceleratorSourcesArtifact(os.Args[0], strings.Repeat("0", sha256.Size*2)); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("wrong expected digest error = %v", err)
	}
}
