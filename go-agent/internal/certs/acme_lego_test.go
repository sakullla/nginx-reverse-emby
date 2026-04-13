package certs

import (
	"testing"

	"github.com/go-acme/lego/v4/lego"
)

func TestConfigureLegoClientConfigDisablesCommonNameForIPCertificates(t *testing.T) {
	config := lego.NewConfig(&legoUser{})

	configureLegoClientConfig(config, acmeIssueRequest{
		Domain:       "140.235.9.54",
		Scope:        "ip",
		DirectoryURL: "https://acme.example.test/directory",
	})

	if got := config.CADirURL; got != "https://acme.example.test/directory" {
		t.Fatalf("CADirURL = %q", got)
	}
	if !config.Certificate.DisableCommonName {
		t.Fatal("expected DisableCommonName for IP certificates")
	}
}

func TestConfigureLegoClientConfigKeepsCommonNameForDomainCertificates(t *testing.T) {
	config := lego.NewConfig(&legoUser{})

	configureLegoClientConfig(config, acmeIssueRequest{
		Domain: "media.example.com",
		Scope:  "domain",
	})

	if config.Certificate.DisableCommonName {
		t.Fatal("expected DisableCommonName to remain false for domain certificates")
	}
}
