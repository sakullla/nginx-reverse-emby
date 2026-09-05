//go:build integration

package datasets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Every fixture is a real pinned upstream artifact, checked against a fixed
// SHA-256. Missing input/network access fails this explicitly selected suite.
func TestIntegrationDatasetSources(t *testing.T) {
	for _, fixture := range []struct {
		name, url, revision, sha string
		format                   sdk.DatasetFormat
	}{
		{"geoip.dat", "https://github.com/v2fly/geoip/releases/download/202609040609/geoip.dat", "202609040609", "1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482", sdk.DatasetFormatGeoIP},
		{"dlc.dat", "https://github.com/v2fly/domain-list-community/releases/download/20260904020013/dlc.dat", "20260904020013", "f82f26c015f9726c763d96a5f658e5b31b285dc094a985e718051e421f350ed6", sdk.DatasetFormatGeoSite},
		{"dlc.tar.gz", "https://codeload.github.com/v2fly/domain-list-community/tar.gz/cb663f66025ef3be1c1c7eb367dfac5f46645ffc", "cb663f66025ef3be1c1c7eb367dfac5f46645ffc", "ce78b02633037eb64b034564bb7c8244e90d9b1335298b7b91057ff9f8d5ab25", sdk.DatasetFormatCommunity},
		{"dlc.zip", "https://codeload.github.com/v2fly/domain-list-community/zip/cb663f66025ef3be1c1c7eb367dfac5f46645ffc", "cb663f66025ef3be1c1c7eb367dfac5f46645ffc", "842ab69a418901bfa34c32c465bdf5cb8af935a474fcbc6854dc0325ab381a30", sdk.DatasetFormatCommunity},
		{"loyalsoldier-geoip.dat", "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202609042338/geoip.dat", "202609042338", "4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721", sdk.DatasetFormatGeoIP},
		{"loyalsoldier-geosite.dat", "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202609042338/geosite.dat", "202609042338", "bca29c80611ee4b909ecc0bd531cf05901b1502998d88bf01580152ffc9e260b", sdk.DatasetFormatGeoSite},
		{"dbip-city-lite-2026-09.mmdb.gz", "https://download.db-ip.com/free/dbip-city-lite-2026-09.mmdb.gz", "2026-09", "c5d05b35a45c3eea0cadc728c8f5ad751693d4e270529b731442172a73f05954", sdk.DatasetFormatGeoMMDB},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			data := integrationData(t, fixture.name, fixture.url, "sha256:"+fixture.sha)
			source := sdk.DatasetSource{ID: "integration-source", Name: fixture.name, URL: fixture.url, Format: fixture.format}
			if strings.HasPrefix(fixture.name, "loyalsoldier-") {
				source.ID = strings.TrimSuffix(fixture.name, ".dat")
				source.LicenseURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/blob/202609042338/LICENSE"
				source.AttributionText = "Loyalsoldier/v2ray-rules-dat and upstream data contributors"
				source.AttributionURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/tree/202609042338"
			}
			if fixture.format == sdk.DatasetFormatGeoMMDB {
				source.LicenseURL = "https://creativecommons.org/licenses/by/4.0/"
				source.AttributionText = "IP Geolocation by DB-IP"
				source.AttributionURL = "https://db-ip.com"
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			index, err := Compile(t.Context(), Input{Source: source, Revision: fixture.revision, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + fixture.sha, Data: data}, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start)
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(data)
			t.Logf("source=%s raw=%d stats=%+v import=%s retained-heap-delta=%d", fixture.name, len(data), index.Stats(), elapsed, int64(after.HeapAlloc)-int64(before.HeapAlloc))
			if index.Version().Revision != fixture.revision || index.Version().RawDigest != "sha256:"+fixture.sha {
				t.Fatal("immutable provenance changed")
			}
			if index.Version().SourceID != source.ID || index.Version().SourceURL != source.URL || index.Version().LicenseURL != source.LicenseURL || index.Version().AttributionText != source.AttributionText || index.Version().AttributionURL != source.AttributionURL {
				t.Fatal("provider/license provenance changed")
			}
			encoded, _ := index.MarshalBinary()
			reloaded, err := LoadIndex(t.Context(), encoded, Limits{})
			if err != nil || reloaded.Version() != index.Version() {
				t.Fatalf("index artifact reload: %v", err)
			}
			switch fixture.format {
			case sdk.DatasetFormatGeoSite, sdk.DatasetFormatCommunity:
				response, err := index.Query(t.Context(), testQuery(index, "", "api.openai.com", sdk.DatasetClassification{Name: "category-ai-!cn", Kind: sdk.DatasetClassificationDomain}))
				if err != nil || response.Status != sdk.DatasetQueryOK || !response.Matches[0].Matched {
					t.Fatalf("real AI category: %+v %v", response, err)
				}
			case sdk.DatasetFormatGeoIP:
				response, err := index.Query(t.Context(), testQuery(index, "223.5.5.5", "", sdk.DatasetClassification{Name: "cn", Kind: sdk.DatasetClassificationCountry}))
				if err != nil || response.Status != sdk.DatasetQueryOK || !response.Matches[0].Matched {
					t.Fatalf("real CN GeoIP: %+v %v", response, err)
				}
			case sdk.DatasetFormatGeoMMDB:
				if index.Version().AttributionURL != "https://db-ip.com" {
					t.Fatal("source attribution lost")
				}
				for _, province := range Provinces() {
					position, exists := index.lookup[groupKey(sdk.DatasetClassificationRegion, province.Classification)]
					if !exists {
						t.Fatalf("missing real province %s", province.Name)
					}
					group := index.groups[position]
					if len(group.ranges) == 0 {
						t.Fatal("empty real province")
					}
					address := group.ranges[0].first.String()
					response, err := index.Query(t.Context(), testQuery(index, address, "", sdk.DatasetClassification{Name: province.Classification, Kind: sdk.DatasetClassificationRegion}))
					if err != nil || !response.Matches[0].Matched {
						t.Fatalf("province %s: %+v %v", province.Name, response, err)
					}
					t.Logf("province=%s %s sample=%s coverage=%+v", province.Classification, province.Name, address, group.wire.Coverage)
				}
				response, err := index.Query(t.Context(), testQuery(index, "2001:db8::1", "", sdk.DatasetClassification{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}))
				if err != nil || response.Matches[0].Matched || response.Matches[0].Coverage == sdk.DatasetCovered {
					t.Fatalf("unknown IPv6 became covered: %+v %v", response, err)
				}
			}
		})
	}
}

func integrationData(t *testing.T, name, url, expected string) []byte {
	t.Helper()
	var data []byte
	var err error
	if cache := os.Getenv("NRE_DATASET_SOURCE_CACHE"); cache != "" {
		data, err = os.ReadFile(filepath.Join(cache, name))
		if err != nil {
			t.Fatalf("required pinned source %s unavailable: %v", name, err)
		}
	} else {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		client := http.Client{Timeout: 60 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
			host := request.URL.Hostname()
			if request.URL.Scheme != "https" || len(via) > 5 || !(host == "github.com" || host == "release-assets.githubusercontent.com" || host == "objects.githubusercontent.com" || host == "codeload.github.com" || host == "download.db-ip.com") {
				return fmt.Errorf("unapproved source redirect host %s", host)
			}
			return nil
		}}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("required source returned HTTP %d", response.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, sdk.DatasetMaxDownloadBytes+1))
		if err != nil {
			t.Fatal(err)
		}
	}
	if int64(len(data)) > sdk.DatasetMaxDownloadBytes || digest(data) != strings.ToLower(expected) {
		t.Fatalf("pinned source digest/size mismatch for %s: got %s", name, digest(data))
	}
	return data
}
