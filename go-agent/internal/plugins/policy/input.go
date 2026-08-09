package policy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

type SourceKind string

const (
	SourceDirect        SourceKind = "direct"
	SourceTrustedProxy  SourceKind = "trusted-proxy"
	SourceProxyProtocol SourceKind = "proxy-protocol"
	SourceRelay         SourceKind = "relay"
)

type CanonicalMetadata struct {
	peer       netip.AddrPort
	source     netip.AddrPort
	kind       SourceKind
	authorized bool
}

// TrustedPeerAllowlist is an immutable set of physical ingress peers allowed
// to authenticate forwarded source metadata. An empty allowlist trusts no
// peer; callers must not infer trust from the mere presence of XFF or PROXY.
type TrustedPeerAllowlist struct {
	prefixes []netip.Prefix
}

func NewTrustedPeerAllowlist(ranges []string) (TrustedPeerAllowlist, error) {
	allowlist := TrustedPeerAllowlist{prefixes: make([]netip.Prefix, 0, len(ranges))}
	for _, rawRange := range ranges {
		value := strings.TrimSpace(rawRange)
		if value == "" {
			return TrustedPeerAllowlist{}, errors.New("trusted peer range is empty")
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil {
				return TrustedPeerAllowlist{}, fmt.Errorf("parse trusted peer range %q: %w", rawRange, err)
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		prefix = prefix.Masked()
		if !prefix.IsValid() {
			return TrustedPeerAllowlist{}, fmt.Errorf("trusted peer range %q is invalid", rawRange)
		}
		allowlist.prefixes = append(allowlist.prefixes, prefix)
	}
	return allowlist, nil
}

func (allowlist TrustedPeerAllowlist) Contains(peer net.Addr) bool {
	address, err := canonicalAddrPort(peer)
	if err != nil {
		return false
	}
	for _, prefix := range allowlist.prefixes {
		if prefix.Contains(address.Addr()) {
			return true
		}
	}
	return false
}

func NewDirectMetadata(peer net.Addr) (CanonicalMetadata, error) {
	address, err := canonicalAddrPort(peer)
	if err != nil {
		return CanonicalMetadata{}, err
	}
	return CanonicalMetadata{peer: address, source: address, kind: SourceDirect, authorized: true}, nil
}

// NewAuthenticatedMetadata accepts a canonical client address only after the
// ingress host authenticated/authorized the physical connection's metadata.
// Ordinary HTTP forwarding headers must never call this constructor.
func NewAuthenticatedMetadata(kind SourceKind, physicalPeer, canonicalSource net.Addr) (CanonicalMetadata, error) {
	switch kind {
	case SourceTrustedProxy, SourceProxyProtocol, SourceRelay:
	default:
		return CanonicalMetadata{}, fmt.Errorf("source kind %q is not authenticated metadata", kind)
	}
	peer, err := canonicalAddrPort(physicalPeer)
	if err != nil {
		return CanonicalMetadata{}, fmt.Errorf("physical peer: %w", err)
	}
	source, err := canonicalAddrPort(canonicalSource)
	if err != nil {
		return CanonicalMetadata{}, fmt.Errorf("canonical source: %w", err)
	}
	return CanonicalMetadata{peer: peer, source: source, kind: kind, authorized: true}, nil
}

func (metadata CanonicalMetadata) Source() netip.AddrPort { return metadata.source }
func (metadata CanonicalMetadata) Peer() netip.AddrPort   { return metadata.peer }
func (metadata CanonicalMetadata) Kind() SourceKind       { return metadata.kind }

type BodySkipReason string

const (
	BodyNotSkipped    BodySkipReason = ""
	BodyLimitExceeded BodySkipReason = "limit-exceeded"
	BodyStreaming     BodySkipReason = "streaming"
	BodyLengthUnknown BodySkipReason = "length-unknown"
)

type BodyWindow struct {
	prefix     []byte
	complete   bool
	skipReason BodySkipReason
}

func NewBodyWindow(prefix []byte, complete bool, skipReason BodySkipReason) (BodyWindow, error) {
	if len(prefix) > MaxBodyPrefixBytes {
		return BodyWindow{}, fmt.Errorf("body prefix exceeds %d bytes", MaxBodyPrefixBytes)
	}
	if complete && skipReason != BodyNotSkipped {
		return BodyWindow{}, errors.New("complete body cannot have a skip reason")
	}
	if !complete && skipReason == BodyNotSkipped {
		return BodyWindow{}, errors.New("partial body requires a skip reason")
	}
	return BodyWindow{prefix: append([]byte(nil), prefix...), complete: complete, skipReason: skipReason}, nil
}

func (body BodyWindow) Prefix() []byte             { return append([]byte(nil), body.prefix...) }
func (body BodyWindow) Complete() bool             { return body.complete }
func (body BodyWindow) SkipReason() BodySkipReason { return body.skipReason }

type Input struct {
	extensionPoint string
	requestID      string
	metadata       CanonicalMetadata
	fields         map[string][]byte
	body           BodyWindow
}

func NewInput(extensionPoint, requestID string, metadata CanonicalMetadata, fields map[string][]byte, body BodyWindow) (Input, error) {
	if extensionPoint != ExtensionHTTP && extensionPoint != ExtensionL4 {
		return Input{}, fmt.Errorf("unsupported policy extension point %q", extensionPoint)
	}
	if !metadata.authorized || !metadata.source.IsValid() || !metadata.peer.IsValid() {
		return Input{}, errors.New("canonical source metadata is required")
	}
	requestID = strings.TrimSpace(requestID)
	if len(requestID) > MaxPolicyRequestIDBytes || strings.ContainsAny(requestID, "\r\n\x00") {
		return Input{}, errors.New("policy request id exceeds its canonical bound")
	}
	if len(body.prefix) > MaxBodyPrefixBytes || (!body.complete && body.skipReason == BodyNotSkipped) || (body.complete && body.skipReason != BodyNotSkipped) {
		return Input{}, errors.New("body window is not canonical")
	}
	cloned := make(map[string][]byte, len(fields))
	total := len(body.prefix)
	for rawName, value := range fields {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" || name != rawName {
			return Input{}, fmt.Errorf("field name %q is not canonical", rawName)
		}
		if strings.HasPrefix(name, "source.") || strings.HasPrefix(name, "body.") {
			return Input{}, fmt.Errorf("field %q is host-owned", name)
		}
		total += len(name) + len(value)
		if int64(total) > MaxPolicyInputBytes {
			return Input{}, fmt.Errorf("policy input exceeds %d bytes", MaxPolicyInputBytes)
		}
		cloned[name] = clonePresentBytes(value)
	}
	return Input{extensionPoint: extensionPoint, requestID: requestID, metadata: metadata, fields: cloned, body: body}, nil
}

func (input Input) ExtensionPoint() string      { return input.extensionPoint }
func (input Input) RequestID() string           { return input.requestID }
func (input Input) Metadata() CanonicalMetadata { return input.metadata }
func (input Input) Body() BodyWindow {
	return BodyWindow{prefix: input.body.Prefix(), complete: input.body.complete, skipReason: input.body.skipReason}
}
func (input Input) Fields() map[string][]byte {
	fields := make(map[string][]byte, len(input.fields))
	for name, value := range input.fields {
		fields[name] = clonePresentBytes(value)
	}
	return fields
}

func clonePresentBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func canonicalAddrPort(address net.Addr) (netip.AddrPort, error) {
	if address == nil {
		return netip.AddrPort{}, errors.New("address is missing")
	}
	var parsed netip.AddrPort
	switch value := address.(type) {
	case *net.TCPAddr:
		addr, ok := netip.AddrFromSlice(value.IP)
		if !ok || value.Port < 0 || value.Port > 65535 {
			return netip.AddrPort{}, fmt.Errorf("invalid TCP address %q", value)
		}
		parsed = netip.AddrPortFrom(addr.Unmap(), uint16(value.Port))
	case *net.UDPAddr:
		addr, ok := netip.AddrFromSlice(value.IP)
		if !ok || value.Port < 0 || value.Port > 65535 {
			return netip.AddrPort{}, fmt.Errorf("invalid UDP address %q", value)
		}
		parsed = netip.AddrPortFrom(addr.Unmap(), uint16(value.Port))
	default:
		parsedValue, err := netip.ParseAddrPort(address.String())
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("parse address %q: %w", address.String(), err)
		}
		parsed = netip.AddrPortFrom(parsedValue.Addr().Unmap(), parsedValue.Port())
	}
	if !parsed.Addr().IsValid() || parsed.Addr().IsUnspecified() {
		return netip.AddrPort{}, fmt.Errorf("address %q is not a canonical client address", address.String())
	}
	return parsed, nil
}
