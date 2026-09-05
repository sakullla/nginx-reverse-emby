package datasets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"google.golang.org/protobuf/encoding/protowire"
)

func testInput(format sdk.DatasetFormat, data []byte) Input {
	return Input{Source: sdk.DatasetSource{ID: "test-source", Name: "Test source", Format: format}, Revision: "fixed-revision", FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: digest(data), Data: data}
}
func compileTest(t *testing.T, format sdk.DatasetFormat, data []byte) *Index {
	t.Helper()
	index, err := Compile(t.Context(), testInput(format, data), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	assertIndexCatalogRoundtrip(t, index)
	return index
}

func assertIndexCatalogRoundtrip(t *testing.T, index *Index) {
	t.Helper()
	encoded, err := index.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex(t.Context(), encoded, Limits{})
	if err != nil {
		t.Fatalf("reload successful index: %v", err)
	}
	if loaded.Version() != index.Version() {
		t.Fatal("catalog validation changed immutable version")
	}
	for _, candidate := range []*Index{index, loaded} {
		historyRequest := sdk.DatasetCatalogRequest{SourceID: candidate.Version().SourceID, Limit: 1}
		history, err := candidate.Catalog(historyRequest)
		if err != nil {
			t.Fatal(err)
		}
		if err := history.ValidateFor(historyRequest); err != nil {
			t.Fatalf("successful index has invalid version catalog: %v", err)
		}
		request := sdk.DatasetCatalogRequest{SourceID: candidate.Version().SourceID, VersionDigest: candidate.Version().Digest, Limit: 1}
		count := 0
		for {
			page, err := candidate.Catalog(request)
			if err != nil {
				t.Fatalf("successful index has unavailable classification catalog: %v", err)
			}
			if err := page.ValidateFor(request); err != nil {
				t.Fatalf("successful index has invalid classification catalog: %v", err)
			}
			for _, entry := range page.Classifications {
				if err := entry.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			count += len(page.Classifications)
			if count > candidate.Version().ClassificationCount {
				t.Fatal("catalog repeated classifications")
			}
			if page.NextCursor == "" {
				break
			}
			request.Cursor = page.NextCursor
		}
		if count != candidate.Version().ClassificationCount {
			t.Fatal("catalog omitted classifications")
		}
	}
}

func TestCatalogMetadataRejectedBeforeCompileOrLoadSucceeds(t *testing.T) {
	document := CIDRDocument{Schema: CIDRSchema, Classifications: []CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{"192.0.2.0/24"}}}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	valid := compileTest(t, sdk.DatasetFormatCIDR, data)
	encoded, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := inflate(encoded, DefaultLimits().MaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wire indexWire
	if err := json.Unmarshal(decoded, &wire); err != nil {
		t.Fatal(err)
	}
	assertRejected := func(t *testing.T, index *Index, err error) {
		t.Helper()
		var failure *Error
		if index != nil || !errors.As(err, &failure) || failure.Code != sdk.DatasetFailureInvalidData || !strings.Contains(failure.Detail, "catalog metadata") {
			t.Fatalf("invalid published metadata was not rejected at construction: index=%v err=%v", index, err)
		}
	}
	for name, displayName := range map[string]string{
		"blank": " ", "leading space": " Province", "trailing space": "Province ",
		"tab only": "\t", "embedded tab": "Pro\tvince", "line break": "Pro\nvince",
		"carriage return": "Pro\rvince", "nul": "Pro\x00vince", "other control": "Pro\x1fvince",
		"unicode control": "Pro\u0085vince", "unicode whitespace": "\u2003", "oversize": strings.Repeat("x", 129),
	} {
		t.Run(name, func(t *testing.T) {
			document.Classifications[0].DisplayName = displayName
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			index, err := Compile(t.Context(), testInput(sdk.DatasetFormatCIDR, data), Limits{})
			assertRejected(t, index, err)
			// Recreate a correctly encoded artifact with invalid metadata. Its
			// rejection must precede the final canonical-byte comparison.
			wire.Groups[0].DisplayName = displayName
			var altered bytes.Buffer
			writer, err := gzip.NewWriterLevel(&altered, gzip.BestSpeed)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(writer).Encode(wire); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			index, err = LoadIndex(t.Context(), altered.Bytes(), Limits{})
			assertRejected(t, index, err)
		})
	}
}

func TestCatalogMetadataFallbackAndMutationIsolation(t *testing.T) {
	document := CIDRDocument{Schema: CIDRSchema}
	for i, name := range []string{"", "广东省", "Inner Mongolia", strings.Repeat("x", 128)} {
		document.Classifications = append(document.Classifications, CIDRClassification{Name: "category-" + strconv.Itoa(i), Kind: sdk.DatasetClassificationCIDR, DisplayName: name, CIDRs: []string{"192.0.2.0/24"}})
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	index := compileTest(t, sdk.DatasetFormatCIDR, data)
	request := sdk.DatasetCatalogRequest{SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest, Limit: 4}
	page, err := index.Catalog(request)
	if err != nil {
		t.Fatal(err)
	}
	if page.Classifications[0].DisplayName != "category-0" {
		t.Fatal("empty display name lost category fallback")
	}
	want := append([]sdk.DatasetCatalogEntry(nil), page.Classifications...)
	page.Classifications[0].DisplayName = "\t"
	page.Classifications[1].Classification.Name = ""
	page.Classifications[2].EntryCount = 0
	page.Classifications[3].Coverage.IPv4 = ""
	again, err := index.Catalog(request)
	if err != nil || again.ValidateFor(request) != nil || !reflect.DeepEqual(again.Classifications, want) {
		t.Fatal("catalog metadata mutation leaked into index")
	}
	assertIndexCatalogRoundtrip(t, index)
	// An omitted field has the same supported fallback as an explicit empty one.
	compileTest(t, sdk.DatasetFormatCIDR, []byte(`{"schema":"nre.cidr-dataset/v1","classifications":[{"name":"omitted","kind":"cidr","cidrs":["192.0.2.0/24"]}]}`))
}
func testQuery(index *Index, address, domain string, selectors ...sdk.DatasetClassification) sdk.DatasetQueryRequest {
	return sdk.DatasetQueryRequest{Reference: sdk.DatasetReference{Handle: strings.Repeat("a", 43), InstanceID: "instance-1", Generation: "generation-1", SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest}, Address: address, Domain: domain, Classifications: selectors, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: 2000, MaxResponseBytes: 32768}}
}
func fieldBytes(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}
func fieldNumber(number protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, number, protowire.VarintType), value)
}

func TestNativeRegionCoverageAndImmutableRoundtrip(t *testing.T) {
	document := CIDRDocument{Schema: CIDRSchema, Classifications: []CIDRClassification{
		{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{"192.0.2.0/24"}},
		{Name: "cn-32", Kind: sdk.DatasetClassificationRegion, DisplayName: "江苏省", CIDRs: []string{"198.51.100.0/24"}},
		{Name: "cn", Kind: sdk.DatasetClassificationCountry, DisplayName: "中国", CIDRs: []string{"192.0.2.0/24", "198.51.100.0/24", "2001:db8::/32"}},
	}}
	data, _ := json.Marshal(document)
	index := compileTest(t, sdk.DatasetFormatCIDR, data)
	for _, test := range []struct {
		address  string
		matched  bool
		coverage sdk.DatasetMatchCoverage
	}{{"192.0.2.1", true, sdk.DatasetCovered}, {"198.51.100.1", false, sdk.DatasetCovered}, {"203.0.113.1", false, sdk.DatasetUnknown}, {"2001:db8::1", false, sdk.DatasetUnsupportedFamily}} {
		response, err := index.Query(t.Context(), testQuery(index, test.address, "", sdk.DatasetClassification{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}))
		if err != nil || response.Status != sdk.DatasetQueryOK || response.Matches[0].Matched != test.matched || response.Matches[0].Coverage != test.coverage {
			t.Fatalf("%s => %+v %v", test.address, response, err)
		}
	}
	encoded, _ := index.MarshalBinary()
	if digest(encoded) != index.Version().IndexDigest {
		t.Fatal("artifact digest is not complete byte hash")
	}
	reloaded, err := LoadIndex(t.Context(), encoded, Limits{})
	if err != nil || reloaded.Version() != index.Version() {
		t.Fatalf("immutable version changed: %v", err)
	}
	encoded[0] ^= 1
	if _, err := LoadIndex(t.Context(), encoded, Limits{}); err == nil {
		t.Fatal("damaged artifact accepted")
	}
	if index.Version().IndexDigest == digest(encoded) {
		t.Fatal("artifact bytes exposed mutable backing storage")
	}
	query := testQuery(index, "192.0.2.1", "", sdk.DatasetClassification{Name: "missing", Kind: sdk.DatasetClassificationRegion})
	response, err := index.Query(t.Context(), query)
	if err != nil || response.Status != sdk.DatasetQueryMissingClassification || len(response.Matches) != 0 {
		t.Fatalf("missing category became ordinary non-match: %+v %v", response, err)
	}
	query.Reference.VersionDigest = "sha256:" + strings.Repeat("0", 64)
	response, _ = index.Query(t.Context(), query)
	if response.Status != sdk.DatasetQueryStaleReference {
		t.Fatal("foreign snapshot accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	query.Reference.VersionDigest = index.Version().Digest
	response, _ = index.Query(ctx, query)
	if response.Status != sdk.DatasetQueryBudgetExceeded {
		t.Fatal("cancellation ignored")
	}
	page, err := index.Catalog(sdk.DatasetCatalogRequest{SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest, Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("catalog paging: %+v %v", page, err)
	}
	page2, err := index.Catalog(sdk.DatasetCatalogRequest{SourceID: index.Version().SourceID, VersionDigest: index.Version().Digest, Limit: 1, Cursor: page.NextCursor})
	if err != nil || page.Classifications[0].Classification.Name == page2.Classifications[0].Classification.Name {
		t.Fatal("catalog cursor did not advance")
	}
	document.Classifications[1].CIDRs = []string{"192.0.2.128/25"}
	data, _ = json.Marshal(document)
	if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatCIDR, data), Limits{}); err == nil {
		t.Fatal("ambiguous overlapping provinces accepted")
	}
}

func TestGeoIPInverseAndMalformedWire(t *testing.T) {
	cidr := append(fieldBytes(1, []byte{192, 0, 2, 0}), fieldNumber(2, 24)...)
	entry := append(fieldBytes(1, []byte("CN")), fieldBytes(2, cidr)...)
	entry = append(entry, fieldNumber(3, 1)...)
	index := compileTest(t, sdk.DatasetFormatGeoIP, fieldBytes(1, entry))
	for _, test := range []struct {
		address string
		matched bool
	}{{"192.0.2.1", false}, {"198.51.100.1", true}, {"2001:db8::1", true}} {
		response, err := index.Query(t.Context(), testQuery(index, test.address, "", sdk.DatasetClassification{Name: "cn", Kind: sdk.DatasetClassificationCountry}))
		if err != nil || response.Matches[0].Matched != test.matched {
			t.Fatalf("inverse %s: %+v %v", test.address, response, err)
		}
	}
	for _, data := range [][]byte{fieldBytes(1, append(entry, fieldNumber(3, 0)...)), fieldBytes(1, append(fieldBytes(1, []byte("cn")), fieldBytes(2, fieldBytes(1, []byte{1, 2, 3}))...)), {0x0a, 0xff}} {
		if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatGeoIP, data), Limits{}); err == nil {
			t.Fatal("malformed GeoIP accepted")
		}
	}
	plain := compileTest(t, sdk.DatasetFormatGeoIP, fieldBytes(1, append(fieldBytes(1, []byte("cn")), fieldBytes(2, cidr)...)))
	response, err := plain.Query(t.Context(), testQuery(plain, "2001:db8::1", "", sdk.DatasetClassification{Name: "cn", Kind: sdk.DatasetClassificationCountry}))
	if err != nil || response.Matches[0].Matched || response.Matches[0].Coverage != sdk.DatasetCovered {
		t.Fatal("GeoIP missing family is not an ordinary empty set")
	}
	emptyInverse := compileTest(t, sdk.DatasetFormatGeoIP, fieldBytes(1, append(fieldBytes(1, []byte("empty-inverse")), fieldNumber(3, 1)...)))
	response, err = emptyInverse.Query(t.Context(), testQuery(emptyInverse, "2001:db8::1", "", sdk.DatasetClassification{Name: "empty-inverse", Kind: sdk.DatasetClassificationCIDR}))
	if err != nil || !response.Matches[0].Matched || response.Matches[0].Coverage != sdk.DatasetCovered {
		t.Fatal("empty-set complement lost match-all semantics")
	}
}

func TestGeoSiteAllPrimitivesAndTypedAttributes(t *testing.T) {
	var rules [][]byte
	for _, rule := range []struct {
		kind  uint64
		value string
	}{{0, "literal"}, {1, `^rx[0-9]+\.example$`}, {2, "root.example"}, {3, "full.example"}} {
		rules = append(rules, append(fieldNumber(1, rule.kind), fieldBytes(2, []byte(rule.value))...))
	}
	attr := append(fieldBytes(1, []byte("rank")), fieldNumber(3, 7)...)
	rules[3] = append(rules[3], fieldBytes(3, attr)...)
	entry := fieldBytes(1, []byte("CATEGORY-AI-!CN"))
	for _, rule := range rules {
		entry = append(entry, fieldBytes(2, rule)...)
	}
	index := compileTest(t, sdk.DatasetFormatGeoSite, fieldBytes(1, entry))
	for domain, want := range map[string]bool{"literal.example": true, "rx12.example": true, "sub.root.example": true, "badroot.example": false, "full.example": true, "sub.full.example": false} {
		response, err := index.Query(t.Context(), testQuery(index, "", domain, sdk.DatasetClassification{Name: "category-ai-!cn", Kind: sdk.DatasetClassificationDomain}))
		if err != nil || response.Matches[0].Matched != want {
			t.Fatalf("%s: %+v %v", domain, response, err)
		}
	}
	seven := int64(7)
	response, err := index.Query(t.Context(), testQuery(index, "", "full.example", sdk.DatasetClassification{Name: "category-ai-!cn", Kind: sdk.DatasetClassificationDomain, Attributes: []sdk.DatasetAttribute{{Name: "rank", Integer: &seven}}}))
	if err != nil || !response.Matches[0].Matched {
		t.Fatal("integer attribute lost")
	}
	badRule := append(fieldNumber(1, 1), fieldBytes(2, []byte(`(?<=bad)lookbehind`))...)
	bad := append(fieldBytes(1, []byte("bad")), fieldBytes(2, badRule)...)
	if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatGeoSite, fieldBytes(1, bad)), Limits{}); err == nil {
		t.Fatal("unsupported regex silently omitted")
	}
	attr = append(attr, fieldNumber(2, 1)...)
	if _, err := parseProtoAttribute(attr); err == nil {
		t.Fatal("conflicting typed attribute accepted")
	}
}

func TestCommunityDependencyFiltersAffiliationsAndBounds(t *testing.T) {
	files := map[string][]byte{"vendor": []byte("domain:cloud.example @cn\nfull:global.example @global &affiliated\nkeyword:global-keyword\n"), "category-ai-!cn": []byte("include:vendor @-cn\ninclude:affiliated\n")}
	rawDigest, _ := FilesDigest(files, Limits{})
	input := Input{Source: sdk.DatasetSource{ID: "test-source", Name: "Community", Format: sdk.DatasetFormatCommunity}, Revision: "fixed", FetchedAt: "2026-09-05T00:00:00Z", Files: files, ExpectedDigest: rawDigest}
	index, err := Compile(t.Context(), input, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	assertIndexCatalogRoundtrip(t, index)
	for domain, want := range map[string]bool{"cloud.example": false, "global.example": true, "global-keyword.example": true} {
		response, err := index.Query(t.Context(), testQuery(index, "", domain, sdk.DatasetClassification{Name: "category-ai-!cn", Kind: sdk.DatasetClassificationDomain}))
		if err != nil || response.Matches[0].Matched != want {
			t.Fatalf("include semantics %s: %+v %v", domain, response, err)
		}
	}
	for name, bad := range map[string]map[string][]byte{"missing": {"a": []byte("include:missing")}, "cycle": {"a": []byte("include:b"), "b": []byte("include:a")}, "unsupported": {"a": []byte("regexp:(?=a)")}} {
		t.Run(name, func(t *testing.T) {
			input.Files = bad
			input.ExpectedDigest, _ = FilesDigest(bad, Limits{})
			if _, err := Compile(t.Context(), input, Limits{}); err == nil {
				t.Fatal("incomplete candidate accepted")
			}
		})
	}
	limits := DefaultLimits()
	limits.MaxEntries = 1
	input.Files = files
	input.ExpectedDigest = rawDigest
	if _, err := Compile(t.Context(), input, limits); err == nil {
		t.Fatal("over-budget includes accepted")
	}
	input.ExpectedDigest = "sha256:" + strings.Repeat("0", 64)
	_, err = Compile(t.Context(), input, Limits{})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != sdk.DatasetFailureDigest {
		t.Fatalf("digest failure: %v", err)
	}
}

func TestCommunityArchiveSafety(t *testing.T) {
	makeArchive := func(name string, kind byte) []byte {
		var buffer bytes.Buffer
		writer := tar.NewWriter(&buffer)
		data := []byte("domain:example.com\n")
		header := &tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: kind}
		if kind == tar.TypeSymlink {
			header.Size = 0
			header.Linkname = "/etc/passwd"
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			writer.Write(data)
		}
		writer.Close()
		return buffer.Bytes()
	}
	valid := makeArchive("repo/data/example", tar.TypeReg)
	files, err := CommunityFiles(valid, Limits{})
	if err != nil || !reflect.DeepEqual(files, map[string][]byte{"example": []byte("domain:example.com\n")}) {
		t.Fatalf("valid repository archive: %v", err)
	}
	for _, archive := range [][]byte{makeArchive("../data/example", tar.TypeReg), makeArchive("repo/data/example", tar.TypeSymlink), makeArchive("repo/README", tar.TypeReg)} {
		if _, err := CommunityFiles(archive, Limits{}); err == nil {
			t.Fatal("unsafe or incomplete archive accepted")
		}
	}
	limits := DefaultLimits()
	limits.MaxExpandedBytes = 8
	if _, err := CommunityFiles(valid, limits); err == nil {
		t.Fatal("expanded archive budget ignored")
	}
}

func TestProvinceOptionsAreStableAndIndependent(t *testing.T) {
	options := Provinces()
	if len(options) != 31 {
		t.Fatal("province count")
	}
	for _, option := range options {
		if len(option.Code) != 6 || option.Name == "" || !strings.HasPrefix(option.Classification, "cn-") {
			t.Fatalf("province option: %+v", option)
		}
	}
	options[0].Name = "changed"
	if Provinces()[0].Name == "changed" {
		t.Fatal("province catalog is mutable")
	}
}

func TestIndexPublicResultsAndCompileInputAreIsolated(t *testing.T) {
	files := map[string][]byte{"vendor": []byte("full:example.com @!cn\n")}
	rawDigest, _ := FilesDigest(files, Limits{})
	input := Input{Source: sdk.DatasetSource{ID: "test-source", Name: "Original source", Format: sdk.DatasetFormatCommunity, AttributionText: "Original credit", AttributionURL: "https://example.com/credit"}, Revision: "fixed", FetchedAt: "2026-09-05T00:00:00Z", Files: files, ExpectedDigest: rawDigest}
	index, err := Compile(t.Context(), input, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	assertIndexCatalogRoundtrip(t, index)
	originalVersion := index.Version()
	version := originalVersion
	version.AttributionText = "changed"
	files["vendor"][0] = 'X'
	input.Source.AttributionText = "changed"
	yes := true
	selector := sdk.DatasetClassification{Name: "vendor", Kind: sdk.DatasetClassificationDomain, Attributes: []sdk.DatasetAttribute{{Name: "!cn", Boolean: &yes}}}
	query := testQuery(index, "", "example.com", selector)
	response, err := index.Query(t.Context(), query)
	if err != nil || !response.Matches[0].Matched {
		t.Fatalf("caller file mutation reached index: %+v %v", response, err)
	}
	response.Matches[0].Matched = false
	page, err := index.Catalog(sdk.DatasetCatalogRequest{SourceID: originalVersion.SourceID, VersionDigest: originalVersion.Digest, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	page.Classifications[0].Classification.Name = "changed"
	page.Classifications[0].DisplayName = "changed"
	history, err := index.Catalog(sdk.DatasetCatalogRequest{SourceID: originalVersion.SourceID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	history.Versions[0].AttributionText = "changed"
	again, err := index.Query(t.Context(), query)
	if err != nil || !again.Matches[0].Matched || index.Version() != originalVersion {
		t.Fatal("public query/catalog/version result mutated the index")
	}
	encoded, _ := index.MarshalBinary()
	loaded, err := LoadIndex(t.Context(), encoded, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] ^= 1
	loadedResult, err := loaded.Query(t.Context(), query)
	if err != nil || !loadedResult.Matches[0].Matched || loaded.Version() != originalVersion {
		t.Fatal("LoadIndex retained caller artifact memory")
	}
}

func TestNativeJSONRejectsAmbiguityAndAllocationBudgets(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{"schema":"nre.cidr-dataset/v1","schema":"nre.cidr-dataset/v1","classifications":[]}`), []byte(`{"schema":"nre.cidr-dataset/v1","classifications":[],"unknown":true}`), append([]byte(`{"schema":"`), 0xff)} {
		if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatCIDR, data), Limits{}); err == nil {
			t.Fatal("ambiguous/malformed JSON candidate accepted")
		}
	}
	document := CIDRDocument{Schema: CIDRSchema, Classifications: []CIDRClassification{{Name: "test", Kind: sdk.DatasetClassificationCIDR, CIDRs: []string{"192.0.2.0/24", "198.51.100.0/24"}}}}
	data, _ := json.Marshal(document)
	limits := DefaultLimits()
	limits.MaxEntries = 1
	if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatCIDR, data), limits); err == nil {
		t.Fatal("oversized JSON array reached typed decoding")
	}
	limits = DefaultLimits()
	limits.MaxMemoryBytes = 128
	if _, err := Compile(t.Context(), testInput(sdk.DatasetFormatCIDR, data), limits); err == nil {
		t.Fatal("JSON memory budget ignored")
	}
}
