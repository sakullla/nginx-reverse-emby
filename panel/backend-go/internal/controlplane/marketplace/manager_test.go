package marketplace

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestPackageAcquisitionSurvivesLeaderCancellationAndCoalescesRetry(t *testing.T) {
	manager := &Manager{leaseTTL: time.Second}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	acquire := func(ctx context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return "verified/cache/path", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := manager.coalescePackageAcquisition(leaderCtx, "source:digest:signer", acquire)
		leaderResult <- err
	}()
	<-started

	retryResult := make(chan struct {
		path string
		err  error
	}, 1)
	retryStarted := make(chan struct{})
	go func() {
		close(retryStarted)
		path, err := manager.coalescePackageAcquisition(context.Background(), "source:digest:signer", acquire)
		retryResult <- struct {
			path string
			err  error
		}{path: path, err: err}
	}()
	<-retryStarted
	time.Sleep(20 * time.Millisecond)
	cancelLeader()
	if err := <-leaderResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context canceled", err)
	}
	close(release)
	result := <-retryResult
	if result.err != nil || result.path != "verified/cache/path" {
		t.Fatalf("retry = %q, %v", result.path, result.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("acquisition calls = %d, want one shared download", calls.Load())
	}
}

func TestApplyPackageDisplayNamesCopiesSignedManifestNames(t *testing.T) {
	entries := []plugins.MarketEntry{
		{ID: "cloudflare-dns", Version: "0.1.5", PackageSHA256: "abc"},
		{ID: "waf", Version: "0.1.0", PackageSHA256: "def", Name: "keep-me"},
	}
	applyPackageDisplayNames(entries, []plugins.ValidatedPackage{
		{Digest: "ABC", Manifest: plugins.Manifest{ID: "cloudflare-dns", Version: "0.1.5", Name: "Cloudflare DNS"}},
	})
	if entries[0].Name != "Cloudflare DNS" {
		t.Fatalf("matched entry name = %q", entries[0].Name)
	}
	if entries[1].Name != "keep-me" {
		t.Fatalf("unmatched entry name = %q, want preserved index name", entries[1].Name)
	}
}
