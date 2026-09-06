package datasets

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"path"
	"sort"
	"strings"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func FilesDigest(files map[string][]byte, limits Limits) (string, error) {
	limits, err := limits.normalized()
	if err != nil {
		return "", err
	}
	if len(files) == 0 || len(files) > limits.MaxClassifications {
		return "", exhausted("community file count")
	}
	names := make([]string, 0, len(files))
	var total int64
	for name, data := range files {
		if !validCommunityName(name) {
			return "", invalid("community file name")
		}
		names = append(names, name)
		total += int64(len(data))
		if total > limits.MaxDownloadBytes {
			return "", exhausted("community input bytes")
		}
	}
	sort.Strings(names)
	hash := sha256.New()
	var length [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(length[:], uint64(len(name)))
		hash.Write(length[:])
		hash.Write([]byte(name))
		binary.BigEndian.PutUint64(length[:], uint64(len(files[name])))
		hash.Write(length[:])
		hash.Write(files[name])
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
func validCommunityName(name string) bool {
	return name == strings.ToLower(name) && sdk.ValidateDatasetClassificationName(name) == nil && !strings.ContainsAny(name, "/@\\")
}

// CommunityFiles safely extracts the complete data/ collection from a pinned
// tar(.gz) or zip repository artifact. Traversal paths, links, duplicate names,
// missing data directories and expansion bombs fail the entire import.
func CommunityFiles(data []byte, limits Limits) (map[string][]byte, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limits.MaxDownloadBytes {
		return nil, exhausted("archive download bytes")
	}
	files := make(map[string][]byte)
	var expanded int64
	members := 0
	consume := func(name string, size int64, reader io.Reader, directory bool) error {
		members++
		if members > limits.MaxClassifications*8 {
			return exhausted("archive member count")
		}
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			return invalid("unsafe archive path")
		}
		clean := strings.TrimSuffix(name, "/")
		if clean == "" || path.Clean(clean) != clean {
			return invalid("noncanonical archive path")
		}
		for _, part := range strings.Split(clean, "/") {
			if part == ".." || part == "." {
				return invalid("archive traversal")
			}
		}
		if directory {
			return nil
		}
		if size < 0 || size > limits.MaxExpandedBytes-expanded {
			return exhausted("archive expanded bytes")
		}
		expanded += size
		parts := strings.Split(clean, "/")
		category := ""
		if len(parts) == 2 && parts[0] == "data" {
			category = parts[1]
		} else if len(parts) == 3 && parts[1] == "data" {
			category = parts[2]
		}
		if category == "" {
			copied, err := io.Copy(io.Discard, io.LimitReader(reader, size+1))
			if err != nil {
				return err
			}
			if copied != size {
				return invalid("archive member size")
			}
			return nil
		}
		if !validCommunityName(category) {
			return invalid("invalid community category path")
		}
		if _, exists := files[category]; exists {
			return invalid("duplicate community file")
		}
		if len(files) >= limits.MaxClassifications {
			return exhausted("community file count")
		}
		value, err := io.ReadAll(io.LimitReader(reader, size+1))
		if err != nil {
			return invalid("archive file: %v", err)
		}
		if int64(len(value)) != size {
			return invalid("archive file size")
		}
		files[category] = value
		return nil
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, invalid("zip: %v", err)
		}
		for _, file := range archive.File {
			if file.Mode()&(^file.Mode().Perm()) != 0 && !file.FileInfo().IsDir() {
				return nil, invalid("archive links/special files are forbidden: %s", file.Name)
			}
			if file.UncompressedSize64 > uint64(limits.MaxExpandedBytes) {
				return nil, exhausted("zip expanded bytes")
			}
			reader, err := file.Open()
			if err != nil {
				return nil, invalid("zip entry")
			}
			err = consume(file.Name, int64(file.UncompressedSize64), reader, file.FileInfo().IsDir())
			closeErr := reader.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, invalid("zip close")
			}
		}
	} else {
		var source io.Reader = bytes.NewReader(data)
		if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			compressed, err := gzip.NewReader(source)
			if err != nil {
				return nil, invalid("archive gzip")
			}
			defer compressed.Close()
			source = compressed
		}
		limited := &io.LimitedReader{R: source, N: limits.MaxExpandedBytes + 1}
		archive := tar.NewReader(limited)
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, invalid("tar: %v", err)
			}
			if header.Typeflag == tar.TypeXGlobalHeader {
				// Git repository archives carry the pinned commit as a global
				// PAX comment. Only inert metadata is accepted; path/size/link
				// overrides cannot change which collection is imported.
				for key, value := range header.PAXRecords {
					if len(value) > 4096 {
						return nil, exhausted("PAX metadata")
					}
					switch key {
					case "comment", "mtime", "atime", "ctime", "uid", "gid", "uname", "gname":
					default:
						return nil, invalid("unsafe global PAX metadata %s", key)
					}
				}
				continue
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
				return nil, invalid("archive type %d path %s is unsupported", header.Typeflag, header.Name)
			}
			if err := consume(header.Name, header.Size, archive, header.Typeflag == tar.TypeDir); err != nil {
				return nil, err
			}
		}
		// Consume through gzip EOF so checksum failures cannot hide after tar EOF.
		if _, err := io.Copy(io.Discard, limited); err != nil {
			return nil, invalid("archive trailer: %v", err)
		}
		if limited.N <= 0 {
			return nil, exhausted("tar expanded bytes")
		}
	}
	if len(files) == 0 {
		return nil, invalid("archive lacks complete data collection")
	}
	return files, nil
}

type inclusion struct {
	name    string
	filters []sdk.DatasetAttribute
}
type communityList struct {
	direct   []domainRule
	includes []inclusion
	resolved []domainRule
	state    int
}

func parseCommunity(ctx context.Context, files map[string][]byte, limits Limits) ([]groupWire, error) {
	if _, err := FilesDigest(files, limits); err != nil {
		return nil, err
	}
	lists := make(map[string]*communityList)
	get := func(name string) *communityList {
		if lists[name] == nil {
			lists[name] = &communityList{}
		}
		return lists[name]
	}
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	inputEntries := 0
	directReferences := 0
	var parsedMemory int64
	for _, name := range keys {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		list := get(name)
		scanner := bufio.NewScanner(bytes.NewReader(files[name]))
		scanner.Buffer(make([]byte, 4096), limits.MaxRegexBytes+4096)
		for scanner.Scan() {
			line, _, _ := strings.Cut(scanner.Text(), "#")
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}
			inputEntries++
			if inputEntries%1024 == 0 {
				if err := checkContext(ctx); err != nil {
					return nil, err
				}
			}
			if inputEntries > limits.MaxEntries {
				return nil, exhausted("community source entries")
			}
			kind, value, found := strings.Cut(parts[0], ":")
			if !found {
				kind, value = "domain", kind
			}
			kind = strings.ToLower(kind)
			if kind == "include" {
				value = strings.ToLower(value)
				if !validCommunityName(value) {
					return nil, invalid("include target in %s", name)
				}
				inc := inclusion{name: value}
				for _, token := range parts[1:] {
					if !strings.HasPrefix(token, "@") || len(token) < 2 {
						return nil, invalid("include attribute in %s", name)
					}
					attr := strings.ToLower(token[1:])
					negative := strings.HasPrefix(attr, "-")
					attr = strings.TrimPrefix(strings.TrimPrefix(attr, "-"), "+")
					yes := true
					filter := sdk.DatasetAttribute{Name: attr, Boolean: &yes, Negate: negative}
					if err := filter.Validate(); err != nil {
						return nil, invalid("include filter")
					}
					inc.filters = append(inc.filters, filter)
				}
				if len(inc.filters) > sdk.DatasetMaxAttributes {
					return nil, exhausted("include filters")
				}
				list.includes = append(list.includes, inc)
				continue
			}
			rule := domainRule{Type: kind, Value: value}
			if kind != "regexp" {
				rule.Value = strings.ToLower(value)
			}
			var affiliations []string
			for _, token := range parts[1:] {
				if len(token) < 2 {
					return nil, invalid("community attribute/affiliation")
				}
				switch token[0] {
				case '@':
					yes := true
					rule.Attributes = append(rule.Attributes, sdk.DatasetAttribute{Name: strings.ToLower(token[1:]), Boolean: &yes})
				case '&':
					aff := strings.ToLower(token[1:])
					if !validCommunityName(aff) {
						return nil, invalid("affiliation target")
					}
					affiliations = append(affiliations, aff)
				default:
					return nil, invalid("unknown community token")
				}
			}
			sort.Slice(rule.Attributes, func(i, j int) bool { return rule.Attributes[i].Name < rule.Attributes[j].Name })
			rule.Attributes = deduplicateAttributes(rule.Attributes)
			if err := validateDomainRule(rule, limits); err != nil {
				return nil, err
			}
			directReferences += 1 + len(affiliations)
			parsedMemory += int64((1+len(affiliations))*128 + len(rule.Value) + len(rule.Attributes)*96)
			if directReferences > limits.MaxEntries || parsedMemory > limits.MaxMemoryBytes {
				return nil, exhausted("community parsed records")
			}
			list.direct = append(list.direct, rule)
			for _, aff := range affiliations {
				target := get(aff)
				target.direct = append(target.direct, rule)
			}
			if len(lists) > limits.MaxClassifications {
				return nil, exhausted("affiliation classifications")
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, invalid("community line exceeds limit")
		}
	}
	keys = keys[:0]
	for name := range lists {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	steps, expanded := 0, 0
	var resolve func(string, int) ([]domainRule, error)
	resolve = func(name string, depth int) ([]domainRule, error) {
		if depth > limits.MaxDependencyDepth {
			return nil, exhausted("include dependency depth")
		}
		list := lists[name]
		if list == nil {
			return nil, invalid("missing include dependency %s", name)
		}
		if list.state == 1 {
			return nil, invalid("include cycle at %s", name)
		}
		if list.state == 2 {
			return list.resolved, nil
		}
		list.state = 1
		unique := make(map[string]domainRule)
		add := func(rule domainRule) error {
			steps++
			if steps > limits.MaxScanRecords {
				return exhausted("include expansion work")
			}
			encoded, _ := json.Marshal(rule)
			unique[string(encoded)] = rule
			if len(unique) > limits.MaxEntries {
				return exhausted("expanded community classification")
			}
			return nil
		}
		for _, rule := range list.direct {
			if err := add(rule); err != nil {
				return nil, err
			}
		}
		for _, inc := range list.includes {
			if err := checkContext(ctx); err != nil {
				return nil, err
			}
			rules, err := resolve(inc.name, depth+1)
			if err != nil {
				return nil, err
			}
			for _, rule := range rules {
				if attributesMatch(rule.Attributes, inc.filters) {
					if err := add(rule); err != nil {
						return nil, err
					}
				}
			}
		}
		ruleKeys := make([]string, 0, len(unique))
		for key := range unique {
			ruleKeys = append(ruleKeys, key)
		}
		sort.Strings(ruleKeys)
		for _, key := range ruleKeys {
			list.resolved = append(list.resolved, unique[key])
		}
		expanded += len(list.resolved)
		if expanded > limits.MaxEntries {
			return nil, exhausted("expanded community entries")
		}
		list.state = 2
		return list.resolved, nil
	}
	groups := make([]groupWire, 0, len(keys))
	for _, name := range keys {
		rules, err := resolve(name, 0)
		if err != nil {
			return nil, err
		}
		groups = append(groups, groupWire{Name: name, Kind: sdk.DatasetClassificationDomain, Domains: rules})
	}
	return groups, nil
}
func deduplicateAttributes(attributes []sdk.DatasetAttribute) []sdk.DatasetAttribute {
	result := attributes[:0]
	for _, attribute := range attributes {
		if len(result) == 0 || result[len(result)-1].Name != attribute.Name {
			result = append(result, attribute)
		}
	}
	return result
}
