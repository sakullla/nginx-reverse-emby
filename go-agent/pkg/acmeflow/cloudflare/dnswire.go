package cloudflare

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

type RRType uint16

const (
	TypeNS    RRType = 2
	TypeCNAME RRType = 5
	TypeSOA   RRType = 6
	TypeTXT   RRType = 16

	dnsClassIN          = 1
	dnsHeaderSize       = 12
	dnsMaxRecords       = 4096
	dnsMaxPointerJumps  = 128
	dnsDefaultQueryTime = 5 * time.Second
	defaultResolvConf   = "/etc/resolv.conf"
)

var fallbackRecursiveServers = []string{"8.8.8.8:53", "8.8.4.4:53"}

type SOAData struct {
	MName   string
	RName   string
	Serial  uint32
	Refresh uint32
	Retry   uint32
	Expire  uint32
	Minimum uint32
}

type DNSRecord struct {
	Name  string
	Type  RRType
	TTL   uint32
	Value string
	Text  []string
	SOA   *SOAData
}

type DNSQuestion struct {
	Name string
	Type RRType
}

type DNSMessage struct {
	ID          uint16
	Truncated   bool
	RCode       int
	Questions   []DNSQuestion
	Answers     []DNSRecord
	Authorities []DNSRecord
	Additionals []DNSRecord
}

type DNSQuerier interface {
	Query(context.Context, string, string, RRType) (DNSMessage, error)
}

type WireResolverConfig struct {
	RecursiveServers []string
	QueryTimeout     time.Duration
	Dialer           *net.Dialer
	NextID           func() uint16
}

type WireResolver struct {
	recursiveServers []string
	queryTimeout     time.Duration
	dialer           *net.Dialer
	nextID           func() uint16
}

func NewWireResolver(config WireResolverConfig) (*WireResolver, error) {
	servers := append([]string(nil), config.RecursiveServers...)
	if len(servers) == 0 {
		servers = recursiveServersFromResolvConf(defaultResolvConf, fallbackRecursiveServers)
	}
	for index, server := range servers {
		normalized, err := normalizeDNSServer(server)
		if err != nil {
			return nil, providerError(acmeflow.CategoryProtocol, "dns_config", err)
		}
		servers[index] = normalized
	}
	queryTimeout := config.QueryTimeout
	if queryTimeout == 0 {
		queryTimeout = dnsDefaultQueryTime
	}
	if queryTimeout < 0 || queryTimeout > time.Minute {
		return nil, providerError(acmeflow.CategoryProtocol, "dns_config", errors.New("DNS query timeout is invalid"))
	}
	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	nextID := config.NextID
	if nextID == nil {
		nextID = randomDNSID
	}
	return &WireResolver{
		recursiveServers: servers,
		queryTimeout:     queryTimeout,
		dialer:           dialer,
		nextID:           nextID,
	}, nil
}

func (resolver *WireResolver) RecursiveServers() []string {
	if resolver == nil {
		return nil
	}
	return append([]string(nil), resolver.recursiveServers...)
}

func (resolver *WireResolver) Query(ctx context.Context, server, name string, recordType RRType) (DNSMessage, error) {
	const operation = "dns_query"
	if resolver == nil {
		return DNSMessage{}, providerError(acmeflow.CategoryProtocol, operation, errors.New("DNS resolver is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return DNSMessage{}, err
	}
	server, err := normalizeDNSServer(server)
	if err != nil {
		return DNSMessage{}, providerError(acmeflow.CategoryProtocol, operation, err)
	}
	id := resolver.nextID()
	query, err := encodeDNSQuery(id, name, recordType)
	if err != nil {
		return DNSMessage{}, providerError(acmeflow.CategoryChallenge, operation, err)
	}
	queryContext, cancel := context.WithTimeout(ctx, resolver.queryTimeout)
	defer cancel()
	response, err := resolver.exchange(queryContext, "udp", server, query)
	if err != nil {
		return DNSMessage{}, providerError(acmeflow.CategoryNetwork, operation, err)
	}
	message, err := decodeDNSMessage(response, id)
	if err != nil {
		return DNSMessage{}, providerError(acmeflow.CategoryProtocol, operation, errDNSResponse)
	}
	if message.Truncated {
		response, err = resolver.exchange(queryContext, "tcp", server, query)
		if err != nil {
			return DNSMessage{}, providerError(acmeflow.CategoryNetwork, operation, err)
		}
		message, err = decodeDNSMessage(response, id)
		if err != nil || message.Truncated {
			return DNSMessage{}, providerError(acmeflow.CategoryProtocol, operation, errDNSResponse)
		}
	}
	expectedName, _ := normalizeDNSName(name)
	if len(message.Questions) != 1 || message.Questions[0].Name != expectedName || message.Questions[0].Type != recordType {
		return DNSMessage{}, providerError(acmeflow.CategoryProtocol, operation, errDNSResponse)
	}
	if message.RCode != 0 && message.RCode != 3 {
		return DNSMessage{}, providerError(acmeflow.CategoryNetwork, operation, errors.New("DNS query was not successful"))
	}
	return message, nil
}

func (resolver *WireResolver) exchange(ctx context.Context, network, server string, query []byte) ([]byte, error) {
	connection, err := resolver.dialer.DialContext(ctx, network, server)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("DNS connection failed")
	}
	defer connection.Close()
	stopWatcher := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopWatcher:
		}
	}()
	defer func() {
		close(stopWatcher)
		<-watcherDone
	}()

	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, errors.New("DNS deadline could not be set")
		}
	}
	if network == "tcp" {
		if len(query) > 65535 {
			return nil, errors.New("DNS query is too large")
		}
		framed := make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(framed[:2], uint16(len(query)))
		copy(framed[2:], query)
		if err := writeDNSStream(connection, framed); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("DNS query write failed")
		}
		var length [2]byte
		if _, err := io.ReadFull(connection, length[:]); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("DNS response length read failed")
		}
		responseLength := int(binary.BigEndian.Uint16(length[:]))
		if responseLength < dnsHeaderSize {
			return nil, errors.New("DNS response is too short")
		}
		response := make([]byte, responseLength)
		if _, err := io.ReadFull(connection, response); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("DNS response read failed")
		}
		return response, nil
	}

	count, err := connection.Write(query)
	if err != nil || count != len(query) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("DNS query write failed")
	}
	buffer := make([]byte, 65535)
	count, err = connection.Read(buffer)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("DNS response read failed")
	}
	return append([]byte(nil), buffer[:count]...), nil
}

func encodeDNSQuery(id uint16, name string, recordType RRType) ([]byte, error) {
	if !supportedQueryType(recordType) {
		return nil, errors.New("DNS query type is unsupported")
	}
	encodedName, err := encodeDNSName(name)
	if err != nil {
		return nil, err
	}
	query := make([]byte, dnsHeaderSize)
	binary.BigEndian.PutUint16(query[0:2], id)
	binary.BigEndian.PutUint16(query[2:4], 0x0100)
	binary.BigEndian.PutUint16(query[4:6], 1)
	query = append(query, encodedName...)
	query = binary.BigEndian.AppendUint16(query, uint16(recordType))
	query = binary.BigEndian.AppendUint16(query, dnsClassIN)
	return query, nil
}

func encodeDNSName(name string) ([]byte, error) {
	normalized, err := normalizeDNSName(name)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, len(normalized)+2)
	for _, label := range strings.Split(normalized, ".") {
		encoded = append(encoded, byte(len(label)))
		encoded = append(encoded, label...)
	}
	encoded = append(encoded, 0)
	return encoded, nil
}

func decodeDNSMessage(packet []byte, expectedID uint16) (DNSMessage, error) {
	var message DNSMessage
	if len(packet) < dnsHeaderSize {
		return message, errors.New("DNS packet is too short")
	}
	message.ID = binary.BigEndian.Uint16(packet[0:2])
	if message.ID != expectedID {
		return DNSMessage{}, errors.New("DNS response identifier does not match")
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	if flags&0x8000 == 0 || flags&0x7800 != 0 || flags&0x0040 != 0 {
		return DNSMessage{}, errors.New("DNS packet is not a response")
	}
	message.Truncated = flags&0x0200 != 0
	message.RCode = int(flags & 0x000f)
	questionCount := int(binary.BigEndian.Uint16(packet[4:6]))
	answerCount := int(binary.BigEndian.Uint16(packet[6:8]))
	authorityCount := int(binary.BigEndian.Uint16(packet[8:10]))
	additionalCount := int(binary.BigEndian.Uint16(packet[10:12]))
	if questionCount+answerCount+authorityCount+additionalCount > dnsMaxRecords {
		return DNSMessage{}, errors.New("DNS packet contains too many records")
	}
	offset := dnsHeaderSize
	for index := 0; index < questionCount; index++ {
		name, next, err := decodeDNSName(packet, offset)
		if err != nil || next+4 > len(packet) {
			return DNSMessage{}, errors.New("DNS question is invalid")
		}
		recordType := RRType(binary.BigEndian.Uint16(packet[next : next+2]))
		class := binary.BigEndian.Uint16(packet[next+2 : next+4])
		if class != dnsClassIN {
			return DNSMessage{}, errors.New("DNS question class is invalid")
		}
		message.Questions = append(message.Questions, DNSQuestion{Name: name, Type: recordType})
		offset = next + 4
	}
	var err error
	message.Answers, offset, err = decodeDNSRecords(packet, offset, answerCount)
	if err != nil {
		return DNSMessage{}, err
	}
	message.Authorities, offset, err = decodeDNSRecords(packet, offset, authorityCount)
	if err != nil {
		return DNSMessage{}, err
	}
	message.Additionals, offset, err = decodeDNSRecords(packet, offset, additionalCount)
	if err != nil {
		return DNSMessage{}, err
	}
	if offset != len(packet) {
		return DNSMessage{}, errors.New("DNS packet contains trailing data")
	}
	return message, nil
}

func decodeDNSRecords(packet []byte, offset, count int) ([]DNSRecord, int, error) {
	records := make([]DNSRecord, 0, count)
	for index := 0; index < count; index++ {
		name, next, err := decodeDNSName(packet, offset)
		if err != nil || next+10 > len(packet) {
			return nil, offset, errors.New("DNS record header is invalid")
		}
		offset = next
		recordType := RRType(binary.BigEndian.Uint16(packet[offset : offset+2]))
		class := binary.BigEndian.Uint16(packet[offset+2 : offset+4])
		ttl := binary.BigEndian.Uint32(packet[offset+4 : offset+8])
		dataLength := int(binary.BigEndian.Uint16(packet[offset+8 : offset+10]))
		dataStart := offset + 10
		dataEnd := dataStart + dataLength
		if dataEnd < dataStart || dataEnd > len(packet) {
			return nil, offset, errors.New("DNS record data is truncated")
		}
		offset = dataEnd
		if class != dnsClassIN {
			continue
		}
		record := DNSRecord{Name: name, Type: recordType, TTL: ttl}
		switch recordType {
		case TypeCNAME, TypeNS:
			value, valueEnd, err := decodeDNSName(packet, dataStart)
			if err != nil || valueEnd != dataEnd {
				return nil, offset, errors.New("DNS name record data is invalid")
			}
			record.Value = value
		case TypeSOA:
			mname, soaOffset, err := decodeDNSName(packet, dataStart)
			if err != nil {
				return nil, offset, errors.New("DNS SOA primary name is invalid")
			}
			rname, soaOffset, err := decodeDNSName(packet, soaOffset)
			if err != nil || soaOffset+20 != dataEnd {
				return nil, offset, errors.New("DNS SOA data is invalid")
			}
			record.SOA = &SOAData{
				MName:   mname,
				RName:   rname,
				Serial:  binary.BigEndian.Uint32(packet[soaOffset : soaOffset+4]),
				Refresh: binary.BigEndian.Uint32(packet[soaOffset+4 : soaOffset+8]),
				Retry:   binary.BigEndian.Uint32(packet[soaOffset+8 : soaOffset+12]),
				Expire:  binary.BigEndian.Uint32(packet[soaOffset+12 : soaOffset+16]),
				Minimum: binary.BigEndian.Uint32(packet[soaOffset+16 : soaOffset+20]),
			}
		case TypeTXT:
			textOffset := dataStart
			for textOffset < dataEnd {
				partLength := int(packet[textOffset])
				textOffset++
				if textOffset+partLength > dataEnd {
					return nil, offset, errors.New("DNS TXT data is invalid")
				}
				record.Text = append(record.Text, string(packet[textOffset:textOffset+partLength]))
				textOffset += partLength
			}
			record.Value = strings.Join(record.Text, "")
		}
		records = append(records, record)
	}
	return records, offset, nil
}

func decodeDNSName(packet []byte, offset int) (string, int, error) {
	if offset < 0 || offset >= len(packet) {
		return "", offset, errors.New("DNS name offset is invalid")
	}
	current := offset
	next := -1
	visited := make(map[int]struct{})
	labels := make([]string, 0, 8)
	for jumps := 0; ; jumps++ {
		if jumps > dnsMaxPointerJumps || current < 0 || current >= len(packet) {
			return "", offset, errors.New("DNS compression pointer is invalid")
		}
		length := int(packet[current])
		switch {
		case length&0xc0 == 0xc0:
			if current+1 >= len(packet) {
				return "", offset, errors.New("DNS compression pointer is truncated")
			}
			pointer := (length&0x3f)<<8 | int(packet[current+1])
			if pointer >= len(packet) || pointer >= current {
				return "", offset, errors.New("DNS compression pointer is out of range")
			}
			if _, exists := visited[pointer]; exists {
				return "", offset, errors.New("DNS compression pointer loop detected")
			}
			visited[pointer] = struct{}{}
			if next < 0 {
				next = current + 2
			}
			current = pointer
		case length&0xc0 != 0:
			return "", offset, errors.New("DNS label encoding is invalid")
		case length == 0:
			if next < 0 {
				next = current + 1
			}
			if len(labels) == 0 {
				return "", next, errors.New("DNS root name is unsupported")
			}
			name, err := normalizeDNSName(strings.Join(labels, "."))
			if err != nil {
				return "", offset, err
			}
			return name, next, nil
		default:
			if length > 63 || current+1+length > len(packet) {
				return "", offset, errors.New("DNS label is invalid")
			}
			labels = append(labels, string(packet[current+1:current+1+length]))
			if len(strings.Join(labels, ".")) > 253 {
				return "", offset, errors.New("DNS name is too long")
			}
			current += 1 + length
		}
	}
}

func normalizeDNSName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 || strings.ContainsAny(value, " /\\\t\r\n\x00*") {
		return "", errors.New("DNS name is invalid")
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("DNS name is invalid")
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
				continue
			}
			return "", errors.New("DNS name is invalid")
		}
	}
	return value, nil
}

func normalizeDNSServer(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("DNS server address is invalid")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if host == "" || port == "" {
			return "", errors.New("DNS server address is invalid")
		}
		portNumber, portErr := strconv.Atoi(port)
		if portErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("DNS server address is invalid")
		}
		return net.JoinHostPort(host, port), nil
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), nil
	}
	if name, err := normalizeDNSName(value); err == nil {
		return net.JoinHostPort(name, "53"), nil
	}
	return "", errors.New("DNS server address is invalid")
}

func supportedQueryType(recordType RRType) bool {
	switch recordType {
	case TypeCNAME, TypeSOA, TypeNS, TypeTXT:
		return true
	default:
		return false
	}
}

func recursiveServersFromResolvConf(path string, fallback []string) []string {
	file, err := os.Open(path)
	if err != nil {
		return append([]string(nil), fallback...)
	}
	defer file.Close()
	servers := make([]string, 0, 3)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	for scanner.Scan() && len(servers) < 16 {
		line := scanner.Text()
		if comment := strings.IndexAny(line, "#;"); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		server, normalizeErr := normalizeDNSServer(fields[1])
		if normalizeErr != nil {
			continue
		}
		if _, exists := seen[server]; exists {
			continue
		}
		seen[server] = struct{}{}
		servers = append(servers, server)
	}
	if len(servers) == 0 {
		return append([]string(nil), fallback...)
	}
	return servers
}

func writeDNSStream(connection net.Conn, data []byte) error {
	for len(data) > 0 {
		count, err := connection.Write(data)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(data) {
			return io.ErrUnexpectedEOF
		}
		data = data[count:]
	}
	return nil
}

func randomDNSID() uint16 {
	var bytes [2]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return uint16(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint16(bytes[:])
}
