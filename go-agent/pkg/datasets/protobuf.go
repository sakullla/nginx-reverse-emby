package datasets

import (
	"context"
	"net/netip"
	"strings"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"google.golang.org/protobuf/encoding/protowire"
)

// V2Ray routercommon is a source data format, not an executable plugin ABI.
// Parse bounded records directly to avoid bringing in the V2Ray runtime module.
func walkProto(data []byte, visit func(protowire.Number, protowire.Type, []byte, uint64) error) error {
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return invalid("protobuf tag")
		}
		data = data[n:]
		var raw []byte
		var value uint64
		switch kind {
		case protowire.BytesType:
			raw, n = protowire.ConsumeBytes(data)
		case protowire.VarintType:
			value, n = protowire.ConsumeVarint(data)
		default:
			return invalid("unsupported protobuf wire type")
		}
		if n < 0 {
			return invalid("truncated protobuf field")
		}
		if err := visit(number, kind, raw, value); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
func protoBytes(kind protowire.Type) error {
	if kind != protowire.BytesType {
		return invalid("protobuf field requires bytes")
	}
	return nil
}
func protoScalar(kind protowire.Type) error {
	if kind != protowire.VarintType {
		return invalid("protobuf field requires varint")
	}
	return nil
}

func parseGeoIP(ctx context.Context, data []byte, limits Limits) ([]groupWire, error) {
	var groups []groupWire
	entries := 0
	err := walkProto(data, func(number protowire.Number, kind protowire.Type, raw []byte, _ uint64) error {
		if number != 1 || kind != protowire.BytesType {
			return invalid("GeoIPList entry")
		}
		if len(groups) >= limits.MaxClassifications {
			return exhausted("GeoIP classifications")
		}
		if err := checkContext(ctx); err != nil {
			return err
		}
		group := groupWire{}
		seen := map[protowire.Number]bool{}
		err := walkProto(raw, func(number protowire.Number, kind protowire.Type, raw []byte, value uint64) error {
			if number != 2 && seen[number] {
				return invalid("duplicate GeoIP scalar")
			}
			seen[number] = true
			switch number {
			case 1:
				if err := protoBytes(kind); err != nil {
					return err
				}
				group.Name = strings.ToLower(string(raw))
			case 2:
				if err := protoBytes(kind); err != nil {
					return err
				}
				entries++
				if entries%1024 == 0 {
					if err := checkContext(ctx); err != nil {
						return err
					}
				}
				if entries > limits.MaxEntries {
					return exhausted("GeoIP CIDRs")
				}
				prefix, err := parseProtoCIDR(raw)
				if err != nil {
					return err
				}
				group.Prefixes = append(group.Prefixes, prefix.String())
			case 3:
				if err := protoScalar(kind); err != nil {
					return err
				}
				if value > 1 {
					return invalid("GeoIP inverse boolean")
				}
				group.Inverse = value == 1
			case 4, 5:
				if err := protoBytes(kind); err != nil {
					return err
				}
				if len(raw) > 512 {
					return exhausted("GeoIP metadata")
				}
			default:
				return invalid("unsupported GeoIP field %d", number)
			}
			return nil
		})
		if err != nil {
			return err
		}
		group.Kind = sdk.DatasetClassificationCIDR
		if len(group.Name) == 2 && group.Name[0] >= 'a' && group.Name[0] <= 'z' && group.Name[1] >= 'a' && group.Name[1] <= 'z' {
			group.Kind = sdk.DatasetClassificationCountry
		}
		groups = append(groups, group)
		return nil
	})
	return groups, err
}
func parseProtoCIDR(data []byte) (netip.Prefix, error) {
	var address netip.Addr
	bits := 0
	seen := map[protowire.Number]bool{}
	err := walkProto(data, func(number protowire.Number, kind protowire.Type, raw []byte, value uint64) error {
		if seen[number] {
			return invalid("duplicate CIDR scalar")
		}
		seen[number] = true
		switch number {
		case 1:
			if err := protoBytes(kind); err != nil {
				return err
			}
			var ok bool
			address, ok = netip.AddrFromSlice(raw)
			if !ok || address.Is4In6() {
				return invalid("CIDR address bytes")
			}
		case 2:
			if err := protoScalar(kind); err != nil {
				return err
			}
			if value > 128 {
				return invalid("CIDR prefix width")
			}
			bits = int(value)
		default:
			return invalid("unsupported CIDR field")
		}
		return nil
	})
	if err != nil {
		return netip.Prefix{}, err
	}
	if !address.IsValid() || bits > address.BitLen() {
		return netip.Prefix{}, invalid("CIDR address/family prefix")
	}
	return netip.PrefixFrom(address, bits).Masked(), nil
}
func parseGeoSite(ctx context.Context, data []byte, limits Limits) ([]groupWire, error) {
	var groups []groupWire
	entries := 0
	var parsedMemory int64
	err := walkProto(data, func(number protowire.Number, kind protowire.Type, raw []byte, _ uint64) error {
		if number != 1 || kind != protowire.BytesType {
			return invalid("GeoSiteList entry")
		}
		if len(groups) >= limits.MaxClassifications {
			return exhausted("GeoSite classifications")
		}
		if err := checkContext(ctx); err != nil {
			return err
		}
		group := groupWire{Kind: sdk.DatasetClassificationDomain}
		seen := map[protowire.Number]bool{}
		err := walkProto(raw, func(number protowire.Number, kind protowire.Type, raw []byte, _ uint64) error {
			if err := protoBytes(kind); err != nil {
				return err
			}
			if number != 2 && seen[number] {
				return invalid("duplicate GeoSite scalar")
			}
			seen[number] = true
			switch number {
			case 1:
				group.Name = strings.ToLower(string(raw))
			case 2:
				entries++
				if entries%1024 == 0 {
					if err := checkContext(ctx); err != nil {
						return err
					}
				}
				if entries > limits.MaxEntries {
					return exhausted("GeoSite domains")
				}
				rule, err := parseProtoDomain(raw, limits)
				if err != nil {
					return err
				}
				parsedMemory += int64(224 + len(rule.Value) + len(rule.Attributes)*96)
				if parsedMemory > limits.MaxMemoryBytes {
					return exhausted("GeoSite parsed records")
				}
				group.Domains = append(group.Domains, rule)
			case 3, 4:
				if len(raw) > 512 {
					return exhausted("GeoSite metadata")
				}
			default:
				return invalid("unsupported GeoSite field")
			}
			return nil
		})
		if err != nil {
			return err
		}
		groups = append(groups, group)
		return nil
	})
	return groups, err
}
func parseProtoDomain(data []byte, limits Limits) (domainRule, error) {
	rule := domainRule{Type: "keyword"}
	seen := map[protowire.Number]bool{}
	err := walkProto(data, func(number protowire.Number, kind protowire.Type, raw []byte, value uint64) error {
		if number != 3 && seen[number] {
			return invalid("duplicate domain scalar")
		}
		seen[number] = true
		switch number {
		case 1:
			if err := protoScalar(kind); err != nil {
				return err
			}
			switch value {
			case 0:
				rule.Type = "keyword"
			case 1:
				rule.Type = "regexp"
			case 2:
				rule.Type = "domain"
			case 3:
				rule.Type = "full"
			default:
				return invalid("unsupported domain type")
			}
		case 2:
			if err := protoBytes(kind); err != nil {
				return err
			}
			rule.Value = string(raw)
		case 3:
			if err := protoBytes(kind); err != nil {
				return err
			}
			if len(rule.Attributes) >= sdk.DatasetMaxAttributes {
				return exhausted("domain attributes")
			}
			attribute, err := parseProtoAttribute(raw)
			if err != nil {
				return err
			}
			rule.Attributes = append(rule.Attributes, attribute)
		default:
			return invalid("unsupported domain field")
		}
		return nil
	})
	if err != nil {
		return rule, err
	}
	if rule.Type != "regexp" {
		rule.Value = strings.ToLower(rule.Value)
	}
	return rule, validateDomainRule(rule, limits)
}
func parseProtoAttribute(data []byte) (sdk.DatasetAttribute, error) {
	attribute := sdk.DatasetAttribute{}
	seen := map[protowire.Number]bool{}
	typed := false
	err := walkProto(data, func(number protowire.Number, kind protowire.Type, raw []byte, value uint64) error {
		if seen[number] {
			return invalid("duplicate attribute field")
		}
		seen[number] = true
		switch number {
		case 1:
			if err := protoBytes(kind); err != nil {
				return err
			}
			attribute.Name = strings.ToLower(string(raw))
		case 2, 3:
			if err := protoScalar(kind); err != nil {
				return err
			}
			if typed {
				return invalid("conflicting attribute types")
			}
			typed = true
			if number == 2 {
				if value > 1 {
					return invalid("attribute boolean")
				}
				v := value == 1
				attribute.Boolean = &v
			} else {
				v := int64(value)
				attribute.Integer = &v
			}
		default:
			return invalid("unsupported attribute field")
		}
		return nil
	})
	if err != nil {
		return attribute, err
	}
	if err := attribute.Validate(); err != nil {
		return attribute, invalid("attribute %q: %v", attribute.Name, err)
	}
	return attribute, nil
}
