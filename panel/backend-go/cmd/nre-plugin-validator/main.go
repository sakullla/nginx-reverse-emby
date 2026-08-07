package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type validationOutput struct {
	Valid     bool     `json:"valid"`
	Kind      string   `json:"kind"`
	ID        string   `json:"id,omitempty"`
	Version   string   `json:"version,omitempty"`
	Digest    string   `json:"digest,omitempty"`
	Packages  int      `json:"packages,omitempty"`
	FileCount int      `json:"file_count,omitempty"`
	Size      int64    `json:"size,omitempty"`
	Error     string   `json:"error,omitempty"`
	Codes     []string `json:"codes,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("nre-plugin-validator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "plugin package root")
	marketPath := flags.String("market", "", "path to a market.yaml file")
	official := flags.Bool("official", true, "validate market entries as the built-in official source")
	expectedID := flags.String("id", "", "expected plugin id")
	expectedVersion := flags.String("version", "", "expected plugin version")
	expectedDigest := flags.String("sha256", "", "expected package digest")
	hostVersion := flags.String("host-version", "", "control-plane version used for compatibility validation")
	agentVersion := flags.String("agent-version", "", "Agent version used for compatibility validation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}

	validator := plugins.NewValidator(plugins.ValidatorOptions{HostVersion: *hostVersion, AgentVersion: *agentVersion})
	output := validationOutput{Valid: true, Kind: "package"}
	var validationErr error
	if *marketPath != "" {
		output.Kind = "market"
		marketRoot, err := marketRootFromPath(*marketPath)
		if err != nil {
			validationErr = err
		} else {
			var result plugins.ValidatedMarket
			result, validationErr = validator.ValidateMarket(marketRoot, *official)
			if validationErr == nil {
				output.Packages = len(result.Packages)
			}
		}
	} else {
		result, err := validator.ValidatePackage(*root, plugins.PackageExpectation{ID: *expectedID, Version: *expectedVersion, SHA256: *expectedDigest})
		validationErr = err
		if validationErr == nil {
			output.ID, output.Version, output.Digest = result.Manifest.ID, result.Manifest.Version, result.Digest
			output.FileCount, output.Size = result.FileCount, result.Size
		}
	}
	if validationErr != nil {
		output.Valid = false
		output.Error = validationErr.Error()
		var typed *plugins.ValidationError
		if errors.As(validationErr, &typed) {
			output.Codes = []string{typed.Code}
		}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if validationErr != nil {
		return 1
	}
	return 0
}

func marketRootFromPath(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if filepath.Base(absolute) != plugins.MarketManifestFile {
		return "", fmt.Errorf("--market must name %s", plugins.MarketManifestFile)
	}
	return filepath.Dir(absolute), nil
}
