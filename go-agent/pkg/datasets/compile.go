package datasets

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Compile validates an entire candidate before publishing any Index. ExpectedDigest
// is mandatory and checks the original bytes (including compression if supplied).
func Compile(ctx context.Context, input Input, limits Limits) (*Index, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.MaxImportDuration)
	defer cancel()
	if err := input.Source.Validate(); err != nil {
		return nil, invalid("source: %v", err)
	}
	if input.Revision == "" || len(input.Revision) > 256 || strings.TrimSpace(input.Revision) != input.Revision || strings.ContainsAny(input.Revision, "\x00\r\n") {
		return nil, invalid("revision required")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.FetchedAt); err != nil {
		return nil, invalid("fetch timestamp required")
	}
	if (len(input.Data) == 0) == (len(input.Files) == 0) {
		return nil, invalid("exactly one complete data artifact or file collection is required")
	}
	var rawDigest string
	if len(input.Files) > 0 {
		if input.Source.Format != sdk.DatasetFormatCommunity {
			return nil, invalid("file collection requires community format")
		}
		rawDigest, err = FilesDigest(input.Files, limits)
	} else {
		if int64(len(input.Data)) > limits.MaxDownloadBytes {
			return nil, exhausted("download bytes")
		}
		rawDigest = digest(input.Data)
	}
	if err != nil {
		return nil, err
	}
	if input.ExpectedDigest != rawDigest {
		return nil, &Error{Code: sdk.DatasetFailureDigest, Detail: "candidate raw digest mismatch"}
	}
	data := input.Data
	if input.Source.Format != sdk.DatasetFormatCommunity && len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		data, err = inflate(data, limits.MaxExpandedBytes)
		if err != nil {
			return nil, err
		}
	}
	if int64(len(data)) > limits.MaxExpandedBytes {
		return nil, exhausted("expanded bytes")
	}
	var groups []groupWire
	scanned := 0
	switch input.Source.Format {
	case sdk.DatasetFormatGeoIP:
		groups, err = parseGeoIP(ctx, data, limits)
	case sdk.DatasetFormatGeoSite:
		groups, err = parseGeoSite(ctx, data, limits)
	case sdk.DatasetFormatCommunity:
		files := input.Files
		if files == nil {
			files, err = CommunityFiles(data, limits)
		}
		if err == nil {
			groups, err = parseCommunity(ctx, files, limits)
		}
	case sdk.DatasetFormatCIDR:
		groups, err = parseCIDR(data, limits)
	case sdk.DatasetFormatGeoMMDB:
		groups, scanned, err = parseMMDB(ctx, data, limits)
	default:
		err = invalid("unsupported format")
	}
	if err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	// Fetch scheduling is mutable control configuration, not immutable data
	// provenance. Pin the source fields actually used for this candidate.
	source := input.Source
	source.RefreshIntervalSeconds = 0
	wire := indexWire{Schema: indexSchema, Provenance: provenance{Source: source, Revision: input.Revision, FetchedAt: input.FetchedAt, RawDigest: rawDigest}, Groups: groups}
	index, err := buildIndex(ctx, wire, limits)
	if err != nil {
		return nil, err
	}
	index.stats.ScannedRecords = scanned
	return index, nil
}

func inflate(data []byte, max int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, invalid("gzip: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, invalid("gzip: %v", err)
	}
	if int64(len(decoded)) > max {
		return nil, exhausted("expanded bytes")
	}
	return decoded, nil
}

func decodeJSON(data []byte, target any, limits Limits) error {
	if err := validateJSONCandidate(data, limits); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalid("JSON: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return invalid("trailing JSON")
	}
	return nil
}

func parseCIDR(data []byte, limits Limits) ([]groupWire, error) {
	var document CIDRDocument
	if err := decodeJSON(data, &document, limits); err != nil {
		return nil, err
	}
	if document.Schema != CIDRSchema || len(document.Classifications) == 0 || len(document.Classifications) > limits.MaxClassifications {
		return nil, invalid("native CIDR schema/classification count")
	}
	groups := make([]groupWire, 0, len(document.Classifications))
	for _, classification := range document.Classifications {
		if classification.Kind == sdk.DatasetClassificationDomain {
			return nil, invalid("domain kind in CIDR input")
		}
		groups = append(groups, groupWire{Name: classification.Name, Kind: classification.Kind, DisplayName: classification.DisplayName, Prefixes: classification.CIDRs})
	}
	return groups, nil
}

// LoadIndex verifies and reconstructs the canonical immutable artifact. Callers
// must independently compare the downloaded artifact digest to the desired one.
func LoadIndex(ctx context.Context, encoded []byte, limits Limits) (*Index, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > limits.MaxIndexBytes {
		return nil, exhausted("index bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, limits.MaxImportDuration)
	defer cancel()
	data, err := inflate(encoded, limits.MaxExpandedBytes)
	if err != nil {
		return nil, err
	}
	var wire indexWire
	if err := decodeJSON(data, &wire, limits); err != nil {
		return nil, err
	}
	if wire.Schema != indexSchema {
		return nil, invalid("index schema")
	}
	index, err := buildIndex(ctx, wire, limits)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(index.encoded, encoded) {
		return nil, invalid("noncanonical or altered index encoding")
	}
	return index, nil
}

// Inspect complexity before typed decoding can allocate unbounded slices/maps.
// The limits include duplicate-key rejection and bounded string tokens.
func validateJSONCandidate(data []byte, limits Limits) error {
	if !utf8.Valid(data) {
		return invalid("JSON is not UTF-8")
	}
	inString, escaped, start := false, false, 0
	for i, value := range data {
		if !inString {
			if value == '"' {
				inString = true
				start = i
			}
			continue
		}
		if i-start > limits.MaxRegexBytes*6 {
			return exhausted("JSON string token")
		}
		if escaped {
			escaped = false
			continue
		}
		if value == '\\' {
			escaped = true
			continue
		}
		if value == '"' {
			inString = false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	remaining := limits.MaxMemoryBytes - int64(len(data))
	objects := 0
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return exhausted("JSON nesting")
		}
		token, err := decoder.Token()
		if err != nil {
			return invalid("JSON: %v", err)
		}
		remaining -= 32
		if text, ok := token.(string); ok {
			remaining -= int64(len(text))
		}
		if remaining < 0 {
			return exhausted("JSON decoded memory")
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return nil
		}
		objects++
		if objects > limits.MaxEntries+limits.MaxClassifications*8 {
			return exhausted("JSON container count")
		}
		remaining -= 128
		switch delimiter {
		case '{':
			keys := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return invalid("JSON object key")
				}
				name, ok := key.(string)
				if !ok || keys[name] || len(keys) >= 64 {
					return invalid("duplicate/excessive JSON object keys")
				}
				keys[name] = true
				remaining -= int64(32 + len(name))
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			count := 0
			for decoder.More() {
				count++
				if count > limits.MaxEntries {
					return exhausted("JSON array entries")
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return invalid("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		if err != nil {
			return invalid("JSON container close")
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid("trailing JSON")
	}
	return nil
}

func canonicalGroups(groups []groupWire) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Kind != groups[j].Kind {
			return groups[i].Kind < groups[j].Kind
		}
		return groups[i].Name < groups[j].Name
	})
}
