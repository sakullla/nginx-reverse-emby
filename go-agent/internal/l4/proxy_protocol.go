package l4

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

var proxyProtocolV2Signature = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}

type proxyInfo struct {
	Source      *net.TCPAddr
	Destination *net.TCPAddr
	Version     int
}

func parseProxyHeader(r io.Reader) (*proxyInfo, []byte, error) {
	reader, ok := r.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(r)
	}

	if header, err := reader.Peek(len(proxyProtocolV2Signature)); err == nil && bytes.Equal(header, proxyProtocolV2Signature) {
		return parseProxyProtocolV2(reader)
	}
	if header, err := reader.Peek(6); err == nil && string(header) == "PROXY " {
		return parseProxyProtocolV1(reader)
	}

	buffered, err := bufferedReaderBytes(reader)
	return nil, buffered, err
}

func parseProxyProtocolV1(reader *bufio.Reader) (*proxyInfo, []byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("read proxy protocol v1 header: %w", err)
	}
	if !strings.HasSuffix(line, "\r\n") {
		return nil, nil, fmt.Errorf("invalid proxy protocol v1 header terminator")
	}

	fields := strings.Fields(strings.TrimSuffix(line, "\r\n"))
	if len(fields) != 6 || fields[0] != "PROXY" {
		return nil, nil, fmt.Errorf("invalid proxy protocol v1 header")
	}
	if fields[1] == "UNKNOWN" {
		buffered, err := bufferedReaderBytes(reader)
		return nil, buffered, err
	}

	source, destination, err := parseProxyAddresses(fields[1], fields[2], fields[3], fields[4], fields[5])
	if err != nil {
		return nil, nil, err
	}
	buffered, err := bufferedReaderBytes(reader)
	if err != nil {
		return nil, nil, err
	}
	return &proxyInfo{
		Source:      source,
		Destination: destination,
		Version:     1,
	}, buffered, nil
}

func parseProxyProtocolV2(reader *bufio.Reader) (*proxyInfo, []byte, error) {
	header := make([]byte, 16)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, nil, fmt.Errorf("read proxy protocol v2 header: %w", err)
	}
	if !bytes.Equal(header[:12], proxyProtocolV2Signature) {
		return nil, nil, fmt.Errorf("invalid proxy protocol v2 signature")
	}
	if header[12]>>4 != 0x2 {
		return nil, nil, fmt.Errorf("unsupported proxy protocol version %d", header[12]>>4)
	}

	length := int(binary.BigEndian.Uint16(header[14:16]))
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, nil, fmt.Errorf("read proxy protocol v2 payload: %w", err)
	}

	command := header[12] & 0x0f
	if command == 0x0 {
		buffered, err := bufferedReaderBytes(reader)
		return nil, buffered, err
	}
	if command != 0x1 {
		return nil, nil, fmt.Errorf("unsupported proxy protocol v2 command %d", command)
	}

	family := header[13] >> 4
	protocol := header[13] & 0x0f
	if protocol != 0x1 {
		return nil, nil, fmt.Errorf("unsupported proxy protocol v2 protocol %d", protocol)
	}

	source, destination, err := parseProxyV2Addresses(family, payload)
	if err != nil {
		return nil, nil, err
	}
	if source == nil || destination == nil {
		buffered, err := bufferedReaderBytes(reader)
		return nil, buffered, err
	}
	buffered, err := bufferedReaderBytes(reader)
	if err != nil {
		return nil, nil, err
	}
	return &proxyInfo{
		Source:      source,
		Destination: destination,
		Version:     2,
	}, buffered, nil
}

func parseProxyAddresses(network, sourceIP, destinationIP, sourcePort, destinationPort string) (*net.TCPAddr, *net.TCPAddr, error) {
	switch network {
	case "TCP4":
	case "TCP6":
	default:
		return nil, nil, fmt.Errorf("unsupported proxy protocol network %q", network)
	}

	sourcePortNumber, err := strconv.Atoi(sourcePort)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid proxy protocol source port %q", sourcePort)
	}
	destinationPortNumber, err := strconv.Atoi(destinationPort)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid proxy protocol destination port %q", destinationPort)
	}

	source := net.ParseIP(sourceIP)
	if source == nil {
		return nil, nil, fmt.Errorf("invalid proxy protocol source ip %q", sourceIP)
	}
	destination := net.ParseIP(destinationIP)
	if destination == nil {
		return nil, nil, fmt.Errorf("invalid proxy protocol destination ip %q", destinationIP)
	}

	return &net.TCPAddr{IP: source, Port: sourcePortNumber}, &net.TCPAddr{IP: destination, Port: destinationPortNumber}, nil
}

func parseProxyV2Addresses(family byte, payload []byte) (*net.TCPAddr, *net.TCPAddr, error) {
	switch family {
	case 0x1:
		if len(payload) < 12 {
			return nil, nil, fmt.Errorf("invalid proxy protocol v2 ipv4 payload length %d", len(payload))
		}
		return &net.TCPAddr{
				IP:   net.IP(append([]byte(nil), payload[:4]...)),
				Port: int(binary.BigEndian.Uint16(payload[8:10])),
			}, &net.TCPAddr{
				IP:   net.IP(append([]byte(nil), payload[4:8]...)),
				Port: int(binary.BigEndian.Uint16(payload[10:12])),
			}, nil
	case 0x2:
		if len(payload) < 36 {
			return nil, nil, fmt.Errorf("invalid proxy protocol v2 ipv6 payload length %d", len(payload))
		}
		return &net.TCPAddr{
				IP:   net.IP(append([]byte(nil), payload[:16]...)),
				Port: int(binary.BigEndian.Uint16(payload[32:34])),
			}, &net.TCPAddr{
				IP:   net.IP(append([]byte(nil), payload[16:32]...)),
				Port: int(binary.BigEndian.Uint16(payload[34:36])),
			}, nil
	default:
		return nil, nil, nil
	}
}

func buildProxyHeader(info proxyInfo) ([]byte, error) {
	if info.Source == nil || info.Destination == nil {
		return nil, fmt.Errorf("proxy source and destination are required")
	}

	version := info.Version
	if version == 0 {
		version = 1
	}

	switch version {
	case 1:
		return buildProxyProtocolV1(info)
	case 2:
		return buildProxyProtocolV2(info)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol version %d", version)
	}
}

func buildProxyProtocolV1(info proxyInfo) ([]byte, error) {
	network, sourceIP, destinationIP, err := proxyAddressFamily(info)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(
		"PROXY %s %s %s %d %d\r\n",
		network,
		sourceIP.String(),
		destinationIP.String(),
		info.Source.Port,
		info.Destination.Port,
	)), nil
}

func buildProxyProtocolV2(info proxyInfo) ([]byte, error) {
	family, sourceIP, destinationIP, err := proxyAddressFamily(info)
	if err != nil {
		return nil, err
	}

	header := append([]byte(nil), proxyProtocolV2Signature...)
	header = append(header, 0x21)

	var addressBytes []byte
	switch family {
	case "TCP4":
		header = append(header, 0x11)
		addressBytes = make([]byte, 12)
		copy(addressBytes[:4], sourceIP.To4())
		copy(addressBytes[4:8], destinationIP.To4())
		binary.BigEndian.PutUint16(addressBytes[8:10], uint16(info.Source.Port))
		binary.BigEndian.PutUint16(addressBytes[10:12], uint16(info.Destination.Port))
	case "TCP6":
		header = append(header, 0x21)
		addressBytes = make([]byte, 36)
		copy(addressBytes[:16], sourceIP.To16())
		copy(addressBytes[16:32], destinationIP.To16())
		binary.BigEndian.PutUint16(addressBytes[32:34], uint16(info.Source.Port))
		binary.BigEndian.PutUint16(addressBytes[34:36], uint16(info.Destination.Port))
	default:
		return nil, fmt.Errorf("unsupported proxy protocol family %q", family)
	}

	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(addressBytes)))
	header = append(header, length[:]...)
	header = append(header, addressBytes...)
	return header, nil
}

func proxyAddressFamily(info proxyInfo) (string, net.IP, net.IP, error) {
	sourceIPv4 := info.Source.IP.To4()
	destinationIPv4 := info.Destination.IP.To4()
	if sourceIPv4 != nil && destinationIPv4 != nil {
		return "TCP4", sourceIPv4, destinationIPv4, nil
	}

	sourceIPv6 := info.Source.IP.To16()
	destinationIPv6 := info.Destination.IP.To16()
	if sourceIPv6 != nil && destinationIPv6 != nil && sourceIPv4 == nil && destinationIPv4 == nil {
		return "TCP6", sourceIPv6, destinationIPv6, nil
	}

	return "", nil, nil, fmt.Errorf("proxy protocol requires matching ip families")
}

func bufferedReaderBytes(reader *bufio.Reader) ([]byte, error) {
	if reader.Buffered() == 0 {
		return nil, nil
	}
	buffered, err := reader.Peek(reader.Buffered())
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buffered...), nil
}
