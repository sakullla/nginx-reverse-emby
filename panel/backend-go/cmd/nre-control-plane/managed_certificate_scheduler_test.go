package main

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestManagedCertificateAutoRenewLoopWakesForPersistedRetry(t *testing.T) {
	originalPass := runManagedCertificateRenewalPass
	originalRetryAt := nextManagedCertificateRenewalRetryAt
	originalInitialDelay := managedCertificateAutoRenewInitialDelay
	originalPollInterval := managedCertificateRetryPollInterval
	t.Cleanup(func() {
		runManagedCertificateRenewalPass = originalPass
		nextManagedCertificateRenewalRetryAt = originalRetryAt
		managedCertificateAutoRenewInitialDelay = originalInitialDelay
		managedCertificateRetryPollInterval = originalPollInterval
	})

	managedCertificateAutoRenewInitialDelay = 0
	managedCertificateRetryPollInterval = time.Hour
	firstRun := make(chan time.Time, 1)
	secondRun := make(chan time.Time, 1)
	runCount := 0
	stopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	runManagedCertificateRenewalPass = func(context.Context, config.Config, *service.PluginDNSTokenResolver) error {
		runCount++
		if runCount == 1 {
			firstRun <- time.Now()
		} else {
			secondRun <- time.Now()
			stop()
		}
		return nil
	}
	nextManagedCertificateRenewalRetryAt = func(context.Context, config.Config, *service.PluginDNSTokenResolver) (time.Time, error) {
		return time.Now().Add(25 * time.Millisecond), nil
	}

	startManagedCertificateAutoRenewLoop(stopCtx, config.Config{
		ManagedDNSCertificatesEnabled:   true,
		ManagedCertificateRenewInterval: time.Hour,
	}, log.New(io.Discard, "", 0), nil)

	select {
	case first := <-firstRun:
		select {
		case second := <-secondRun:
			if second.Sub(first) >= time.Hour {
				t.Fatalf("second renewal run started after %s, want persisted retry before interval", second.Sub(first))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("renewal loop did not wake for persisted retry")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("renewal loop did not run initial pass")
	}
}
