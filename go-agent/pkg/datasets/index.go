package datasets

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type ipRange struct{ first, last netip.Addr }
type compiledRule struct {
	index int
	regex *regexp.Regexp
}
type compiledGroup struct {
	wire       groupWire
	ranges     []ipRange
	full, root map[string][]int
	other      []compiledRule
}

// Index is immutable and safe for concurrent queries. Returned metadata, pages,
// and artifact bytes are copies. It contains no raw source database or handles.
type Index struct {
	version    sdk.DatasetVersion
	stats      Stats
	encoded    []byte
	groups     []compiledGroup
	lookup     map[string]int
	regions    []ipRange
	regionIPv4 bool
	regionIPv6 bool
}

func (index *Index) Version() sdk.DatasetVersion { return index.version }
func (index *Index) Stats() Stats                { return index.stats }
func (index *Index) MarshalBinary() ([]byte, error) {
	return append([]byte(nil), index.encoded...), nil
}
func groupKey(kind sdk.DatasetClassificationKind, name string) string {
	return string(kind) + ":" + strings.ToLower(name)
}

func buildIndex(ctx context.Context, wire indexWire, limits Limits) (*Index, error) {
	if len(wire.Groups) == 0 || len(wire.Groups) > limits.MaxClassifications {
		return nil, exhausted("classification count")
	}
	if wire.Schema != indexSchema {
		return nil, invalid("index schema")
	}
	if err := wire.Provenance.Source.Validate(); err != nil {
		return nil, invalid("index provenance source: %v", err)
	}
	if wire.Provenance.Source.RefreshIntervalSeconds != 0 {
		return nil, invalid("mutable schedule in immutable index")
	}
	if len(wire.Provenance.Revision) == 0 || len(wire.Provenance.Revision) > 256 || strings.ContainsAny(wire.Provenance.Revision, "\r\n\x00") || strings.TrimSpace(wire.Provenance.Revision) != wire.Provenance.Revision {
		return nil, invalid("index revision")
	}
	if _, err := time.Parse(time.RFC3339Nano, wire.Provenance.FetchedAt); err != nil {
		return nil, invalid("index fetch time")
	}
	if !validDigest(wire.Provenance.RawDigest) {
		return nil, invalid("index raw digest")
	}
	// Sort the names that will actually be serialized. Sorting raw mixed-case
	// names first could make Compile emit an order that LoadIndex cannot retain.
	for i := range wire.Groups {
		wire.Groups[i].Name = strings.ToLower(wire.Groups[i].Name)
	}
	canonicalGroups(wire.Groups)
	index := &Index{lookup: make(map[string]int, len(wire.Groups))}
	regexes := make(map[string]*regexp.Regexp)
	for _, sourceGroup := range wire.Groups {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		group := compiledGroup{wire: sourceGroup}
		if err := (sdk.DatasetClassification{Name: group.wire.Name, Kind: group.wire.Kind}).Validate(); err != nil {
			return nil, invalid("classification: %v", err)
		}
		key := groupKey(group.wire.Kind, group.wire.Name)
		if _, exists := index.lookup[key]; exists {
			return nil, invalid("duplicate classification %s", key)
		}
		if group.wire.DisplayName == "" {
			group.wire.DisplayName = group.wire.Name
		}
		count := groupEntryCount(group.wire)
		if count == 0 {
			return nil, invalid("empty classification %s", key)
		}
		index.stats.EntryCount += count
		if index.stats.EntryCount > limits.MaxEntries {
			return nil, exhausted("expanded index entries")
		}
		index.stats.EstimatedMemoryBytes += 512
		group.wire.Coverage = sdk.DatasetAddressCoverage{IPv4: sdk.DatasetFamilyNone, IPv6: sdk.DatasetFamilyNone}
		if group.wire.Kind == sdk.DatasetClassificationDomain {
			if len(group.wire.Prefixes) > 0 || group.wire.Inverse {
				return nil, invalid("domain classification carries CIDR semantics")
			}
			group.full = make(map[string][]int)
			group.root = make(map[string][]int)
			for i := range group.wire.Domains {
				if i%1024 == 0 {
					if err := checkContext(ctx); err != nil {
						return nil, err
					}
				}
				rule := &group.wire.Domains[i]
				if err := validateDomainRule(*rule, limits); err != nil {
					return nil, err
				}
				sort.Slice(rule.Attributes, func(i, j int) bool { return rule.Attributes[i].Name < rule.Attributes[j].Name })
				index.stats.EstimatedMemoryBytes += int64(224 + len(rule.Value) + len(rule.Attributes)*96)
				if index.stats.EstimatedMemoryBytes > limits.MaxMemoryBytes {
					return nil, exhausted("retained domain index memory")
				}
				switch rule.Type {
				case "full":
					group.full[rule.Value] = append(group.full[rule.Value], i)
				case "domain":
					group.root[rule.Value] = append(group.root[rule.Value], i)
				case "keyword":
					group.other = append(group.other, compiledRule{index: i})
				case "regexp":
					compiled := regexes[rule.Value]
					if compiled == nil {
						parsed, err := syntax.Parse(rule.Value, syntax.Perl)
						if err != nil {
							return nil, invalid("regular expression")
						}
						program, err := syntax.Compile(parsed.Simplify())
						if err != nil || len(program.Inst) > limits.MaxRegexInstructions {
							return nil, exhausted("regex program")
						}
						compiled, err = regexp.Compile(rule.Value)
						if err != nil {
							return nil, invalid("regular expression")
						}
						regexes[rule.Value] = compiled
						index.stats.EstimatedMemoryBytes += int64(len(program.Inst)) * 128
					}
					group.other = append(group.other, compiledRule{index: i, regex: compiled})
				}
			}
		} else {
			if len(group.wire.Domains) > 0 {
				return nil, invalid("address classification carries domain rules")
			}
			for i, raw := range group.wire.Prefixes {
				if i%1024 == 0 {
					if err := checkContext(ctx); err != nil {
						return nil, err
					}
				}
				prefix, err := netip.ParsePrefix(raw)
				if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
					return nil, invalid("noncanonical CIDR %q", raw)
				}
				group.ranges = append(group.ranges, prefixRange(prefix))
				if prefix.Addr().Is4() {
					group.wire.Coverage.IPv4 = sdk.DatasetFamilyPartial
					index.stats.IPv4Prefixes++
				} else {
					group.wire.Coverage.IPv6 = sdk.DatasetFamilyPartial
					index.stats.IPv6Prefixes++
				}
				index.stats.EstimatedMemoryBytes += int64(128 + len(raw))
				if index.stats.EstimatedMemoryBytes > limits.MaxMemoryBytes {
					return nil, exhausted("retained address index memory")
				}
			}
			group.ranges = mergeRanges(group.ranges)
			if group.wire.Inverse {
				if group.wire.Kind == sdk.DatasetClassificationRegion {
					return nil, invalid("inverse province classification")
				}
				group.wire.Coverage = sdk.DatasetAddressCoverage{IPv4: sdk.DatasetFamilyComplete, IPv6: sdk.DatasetFamilyComplete}
			}
			if group.wire.Kind == sdk.DatasetClassificationRegion {
				index.regions = append(index.regions, group.ranges...)
			}
			sort.Strings(group.wire.Prefixes)
		}
		// Validate the exact public projection before this candidate can be
		// returned or persisted. Compile and LoadIndex share this boundary.
		if err := group.catalogEntry().Validate(); err != nil {
			return nil, invalid("classification catalog metadata: %v", err)
		}
		if index.stats.EstimatedMemoryBytes > limits.MaxMemoryBytes {
			return nil, exhausted("retained index memory")
		}
		index.lookup[key] = len(index.groups)
		index.groups = append(index.groups, group)
	}
	if err := validateRegionDisjointness(index.groups); err != nil {
		return nil, err
	}
	index.regions = mergeRanges(index.regions)
	for _, region := range index.regions {
		if region.first.Is4() {
			index.regionIPv4 = true
		} else {
			index.regionIPv6 = true
		}
	}
	index.stats.EstimatedMemoryBytes += int64(len(index.regions)) * 48
	wire.Groups = make([]groupWire, len(index.groups))
	for i := range index.groups {
		wire.Groups[i] = index.groups[i].wire
	}
	var encoded bytes.Buffer
	compressed, _ := gzip.NewWriterLevel(&encoded, gzip.BestSpeed)
	bounded := &boundedWriter{writer: compressed, remaining: limits.MaxExpandedBytes}
	if err := json.NewEncoder(bounded).Encode(wire); err != nil {
		compressed.Close()
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	if int64(encoded.Len()) > limits.MaxIndexBytes {
		return nil, exhausted("serialized index")
	}
	index.encoded = encoded.Bytes()
	index.stats.IndexBytes = int64(len(index.encoded))
	index.stats.EstimatedMemoryBytes += index.stats.IndexBytes
	if index.stats.EstimatedMemoryBytes > limits.MaxMemoryBytes {
		return nil, exhausted("retained index memory")
	}
	index.stats.ClassificationCount = len(index.groups)
	coverage := sdk.DatasetAddressCoverage{IPv4: sdk.DatasetFamilyNone, IPv6: sdk.DatasetFamilyNone}
	if index.stats.IPv4Prefixes > 0 {
		coverage.IPv4 = sdk.DatasetFamilyPartial
	}
	if index.stats.IPv6Prefixes > 0 {
		coverage.IPv6 = sdk.DatasetFamilyPartial
	}
	for _, group := range index.groups {
		if group.wire.Inverse {
			coverage = sdk.DatasetAddressCoverage{IPv4: sdk.DatasetFamilyComplete, IPv6: sdk.DatasetFamilyComplete}
			break
		}
	}
	source := wire.Provenance.Source
	index.version = sdk.DatasetVersion{SourceID: source.ID, SourceURL: source.URL, LicenseURL: source.LicenseURL, AttributionText: source.AttributionText, AttributionURL: source.AttributionURL, Revision: wire.Provenance.Revision, FetchedAt: wire.Provenance.FetchedAt, RawDigest: wire.Provenance.RawDigest, IndexDigest: digest(index.encoded), Format: source.Format, SemanticVersion: "nre-dataset-v1", ClassificationCount: len(index.groups), EntryCount: index.stats.EntryCount, IndexBytes: index.stats.IndexBytes, Coverage: coverage}
	versionBytes, _ := json.Marshal(index.version)
	index.version.Digest = digest(versionBytes)
	if err := index.version.Validate(); err != nil {
		return nil, invalid("version: %v", err)
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return index, nil
}

func prefixRange(prefix netip.Prefix) ipRange {
	first := prefix.Masked().Addr()
	bits := prefix.Bits()
	if first.Is4() {
		last := first.As4()
		for i := bits; i < 32; i++ {
			last[i/8] |= 1 << uint(7-i%8)
		}
		return ipRange{first, netip.AddrFrom4(last)}
	}
	last := first.As16()
	for i := bits; i < 128; i++ {
		last[i/8] |= 1 << uint(7-i%8)
	}
	return ipRange{first, netip.AddrFrom16(last)}
}
func mergeRanges(ranges []ipRange) []ipRange {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].first.Compare(ranges[j].first) < 0 })
	result := ranges[:0]
	for _, current := range ranges {
		if len(result) > 0 {
			previous := &result[len(result)-1]
			next := previous.last.Next()
			if previous.first.BitLen() == current.first.BitLen() && (current.first.Compare(previous.last) <= 0 || (next.IsValid() && current.first == next)) {
				if current.last.Compare(previous.last) > 0 {
					previous.last = current.last
				}
				continue
			}
		}
		result = append(result, current)
	}
	return result
}
func containsIP(ranges []ipRange, address netip.Addr) bool {
	at := sort.Search(len(ranges), func(i int) bool { return ranges[i].first.Compare(address) > 0 }) - 1
	return at >= 0 && ranges[at].first.BitLen() == address.BitLen() && address.Compare(ranges[at].last) <= 0
}

func validateDomainRule(rule domainRule, limits Limits) error {
	if len(rule.Attributes) > sdk.DatasetMaxAttributes {
		return exhausted("domain attributes")
	}
	seen := make(map[string]bool)
	for _, attribute := range rule.Attributes {
		if err := attribute.Validate(); err != nil {
			return invalid("domain attribute: %v", err)
		}
		if attribute.Negate || seen[attribute.Name] {
			return invalid("duplicate/negative stored domain attribute")
		}
		seen[attribute.Name] = true
	}
	if rule.Value == "" || strings.ContainsAny(rule.Value, "\r\n\x00") {
		return invalid("empty/malformed domain pattern")
	}
	switch rule.Type {
	case "regexp":
		if len(rule.Value) > limits.MaxRegexBytes {
			return exhausted("regex length")
		}
		if _, err := syntax.Parse(rule.Value, syntax.Perl); err != nil {
			return invalid("unsupported regex: %v", err)
		}
	case "keyword":
		if len(rule.Value) > 253 || strings.ToLower(rule.Value) != rule.Value {
			return invalid("keyword")
		}
	case "full", "domain":
		if !canonicalDomain(rule.Value) {
			return invalid("domain %q", rule.Value)
		}
	default:
		return invalid("unsupported domain primitive")
	}
	return nil
}
func canonicalDomain(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.ToLower(value) != value {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[7:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (index *Index) Query(ctx context.Context, request sdk.DatasetQueryRequest) (sdk.DatasetQueryResponse, error) {
	if err := request.Validate(); err != nil {
		return sdk.DatasetQueryResponse{}, err
	}
	response := sdk.DatasetQueryResponse{Reference: request.Reference, Status: sdk.DatasetQueryOK}
	if request.Reference.SourceID != index.version.SourceID || request.Reference.VersionDigest != index.version.Digest {
		response.Status = sdk.DatasetQueryStaleReference
		return response, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.Budget.MaxDurationMicros)*time.Microsecond)
	defer cancel()
	address, _ := netip.ParseAddr(request.Address)
	address = address.Unmap()
	for position, selector := range request.Classifications {
		if ctx.Err() != nil {
			response.Status = sdk.DatasetQueryBudgetExceeded
			response.Matches = nil
			return response, nil
		}
		at, exists := index.lookup[groupKey(selector.Kind, selector.Name)]
		if !exists {
			response.Status = sdk.DatasetQueryMissingClassification
			response.Matches = nil
			return response, nil
		}
		group := &index.groups[at]
		match := sdk.DatasetMatch{Index: position, Coverage: sdk.DatasetCovered}
		if selector.Kind == sdk.DatasetClassificationDomain {
			matched, err := group.matchDomain(ctx, request.Domain, selector.Attributes)
			if err != nil {
				response.Status = sdk.DatasetQueryBudgetExceeded
				response.Matches = nil
				return response, nil
			}
			match.Matched = matched
		} else {
			match.Matched = containsIP(group.ranges, address)
			if group.wire.Inverse {
				match.Matched = !match.Matched
			}
			if selector.Kind == sdk.DatasetClassificationRegion && !containsIP(index.regions, address) {
				match.Coverage = sdk.DatasetUnknown
				if address.Is4() && !index.regionIPv4 || address.Is6() && !index.regionIPv6 {
					match.Coverage = sdk.DatasetUnsupportedFamily
				}
			}
		}
		response.Matches = append(response.Matches, match)
	}
	if ctx.Err() != nil {
		response.Status = sdk.DatasetQueryBudgetExceeded
		response.Matches = nil
	}
	if err := response.ValidateFor(request); err != nil {
		return sdk.DatasetQueryResponse{}, exhausted("query response frame")
	}
	if ctx.Err() != nil {
		response.Status = sdk.DatasetQueryBudgetExceeded
		response.Matches = nil
	}
	return response, nil
}

func validateRegionDisjointness(groups []compiledGroup) error {
	type regionRange struct {
		ipRange
		owner int
	}
	var regions []regionRange
	for i, group := range groups {
		if group.wire.Kind == sdk.DatasetClassificationRegion {
			for _, r := range group.ranges {
				regions = append(regions, regionRange{r, i})
			}
		}
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].first.Compare(regions[j].first) < 0 })
	for i := 1; i < len(regions); i++ {
		previous, current := regions[i-1], regions[i]
		if previous.owner != current.owner && previous.first.BitLen() == current.first.BitLen() && current.first.Compare(previous.last) <= 0 {
			return invalid("overlapping province classifications")
		}
	}
	return nil
}
func (group *compiledGroup) matchDomain(ctx context.Context, domain string, filters []sdk.DatasetAttribute) (bool, error) {
	check := func(indices []int) (bool, error) {
		for position, i := range indices {
			if position%16 == 0 && ctx.Err() != nil {
				return false, ctx.Err()
			}
			if attributesMatch(group.wire.Domains[i].Attributes, filters) {
				return true, nil
			}
		}
		return false, nil
	}
	if matched, err := check(group.full[domain]); matched || err != nil {
		return matched, err
	}
	for candidate := domain; candidate != ""; {
		if matched, err := check(group.root[candidate]); matched || err != nil {
			return matched, err
		}
		_, tail, found := strings.Cut(candidate, ".")
		if !found {
			break
		}
		candidate = tail
	}
	for _, compiled := range group.other {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		rule := group.wire.Domains[compiled.index]
		if !attributesMatch(rule.Attributes, filters) {
			continue
		}
		if compiled.regex != nil {
			if compiled.regex.MatchString(domain) {
				return true, nil
			}
		} else if strings.Contains(domain, rule.Value) {
			return true, nil
		}
	}
	return false, nil
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, exhausted("expanded index encoding")
	}
	written, err := writer.writer.Write(data)
	writer.remaining -= int64(written)
	return written, err
}
func attributesMatch(attributes, filters []sdk.DatasetAttribute) bool {
	for _, filter := range filters {
		matched := false
		for _, attribute := range attributes {
			if attribute.Name != filter.Name {
				continue
			}
			matched = attribute.Boolean != nil && filter.Boolean != nil && *attribute.Boolean == *filter.Boolean || attribute.Integer != nil && filter.Integer != nil && *attribute.Integer == *filter.Integer
			break
		}
		if filter.Negate {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

func (index *Index) Catalog(request sdk.DatasetCatalogRequest) (sdk.DatasetCatalogResponse, error) {
	if err := request.Validate(); err != nil {
		return sdk.DatasetCatalogResponse{}, err
	}
	response := sdk.DatasetCatalogResponse{SourceID: index.version.SourceID, VersionDigest: request.VersionDigest}
	if request.SourceID != index.version.SourceID {
		return response, invalid("catalog source mismatch")
	}
	if request.VersionDigest == "" {
		if request.Cursor != "" {
			return response, invalid("unexpected history cursor")
		}
		response.Versions = []sdk.DatasetVersion{index.version}
		return response, nil
	}
	if request.VersionDigest != index.version.Digest {
		return response, invalid("catalog version mismatch")
	}
	start := 0
	if request.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil {
			return response, invalid("catalog cursor")
		}
		prefix := index.version.Digest + ":"
		if !strings.HasPrefix(string(raw), prefix) {
			return response, invalid("foreign catalog cursor")
		}
		start, err = strconv.Atoi(strings.TrimPrefix(string(raw), prefix))
		if err != nil || start < 0 || start >= len(index.groups) {
			return response, invalid("catalog cursor offset")
		}
	}
	end := start + request.Limit
	if end > len(index.groups) {
		end = len(index.groups)
	}
	for _, group := range index.groups[start:end] {
		response.Classifications = append(response.Classifications, group.catalogEntry())
	}
	if end < len(index.groups) {
		response.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d", index.version.Digest, end)))
	}
	return response, response.ValidateFor(request)
}

func (group compiledGroup) catalogEntry() sdk.DatasetCatalogEntry {
	return sdk.DatasetCatalogEntry{
		Classification: sdk.DatasetClassification{Name: group.wire.Name, Kind: group.wire.Kind},
		DisplayName:    group.wire.DisplayName,
		EntryCount:     groupEntryCount(group.wire),
		Coverage:       group.wire.Coverage,
	}
}

func groupEntryCount(group groupWire) int {
	count := len(group.Prefixes) + len(group.Domains)
	// The complement of an empty IP set is one explicit match-all predicate,
	// not a missing classification. No synthetic prefix is inserted.
	if count == 0 && group.Inverse && group.Kind != sdk.DatasetClassificationDomain {
		return 1
	}
	return count
}
