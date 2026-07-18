package core

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestMergeSnapshotPayloadPreservesDDNSConfig(t *testing.T) {
	previous := model.Snapshot{DDNSConfig: &model.DDNSExtractConfig{
		Enabled: true,
		Domain:  "media.example.com",
		IPv4:    model.DDNSFamily{Enabled: true, Source: "public_api"},
	}}
	merged := MergeSnapshotPayload(model.Snapshot{Revision: 7}, previous)
	if merged.DDNSConfig == nil || merged.DDNSConfig.Domain != "media.example.com" {
		t.Fatalf("merged DDNS config = %+v", merged.DDNSConfig)
	}

	cloned := cloneSnapshot(merged)
	cloned.DDNSConfig.Domain = "changed.example.com"
	if merged.DDNSConfig.Domain != "media.example.com" {
		t.Fatal("runtime snapshot clone retained the DDNS config pointer")
	}
}
