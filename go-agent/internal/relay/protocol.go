package relay

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

const maxRequestSize = 1 << 20
const relayMetadataTrafficClass = "traffic_class"

const (
	ListenerTransportModeTLSTCP = "tls_tcp"
	ListenerTransportModeQUIC   = "quic"
	RelayObfsModeOff            = "off"
	RelayObfsModeEarlyWindowV2  = "early_window_v2"
)

type relayRequest struct {
	Network string `json:"network"`
	Target  string `json:"target"`
	Chain   []Hop  `json:"chain,omitempty"`
}

type relayOpenFrame struct {
	Kind        string         `json:"kind"`
	Target      string         `json:"target"`
	Chain       []Hop          `json:"chain,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	InitialData []byte         `json:"initial_data,omitempty"`
}

type relayResponse struct {
	OK                 bool        `json:"ok"`
	Error              string      `json:"error,omitempty"`
	SelectedAddress    string      `json:"selected_address,omitempty"`
	ResolvedCandidates []string    `json:"resolved_candidates,omitempty"`
	HopTimings         []HopTiming `json:"hop_timings,omitempty"`
}

type muxOpenResult struct {
	OK                 bool        `json:"ok"`
	Error              string      `json:"error,omitempty"`
	SelectedAddress    string      `json:"selected_address,omitempty"`
	ResolvedCandidates []string    `json:"resolved_candidates,omitempty"`
	HopTimings         []HopTiming `json:"hop_timings,omitempty"`
}

func writeRelayRequest(w io.Writer, request relayRequest) error {
	return writeFrame(w, request)
}

func writeRelayResponse(w io.Writer, response relayResponse) error {
	return writeFrame(w, response)
}

func writeRelayOpenFrame(w io.Writer, frame relayOpenFrame) error {
	return writeFrame(w, frame)
}

func marshalMuxOpenPayload(frame relayOpenFrame) ([]byte, error) {
	return json.Marshal(frame)
}

func readMuxOpenPayload(payload []byte) (relayOpenFrame, error) {
	var frame relayOpenFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return relayOpenFrame{}, err
	}
	return frame, nil
}

func marshalMuxOpenResultPayload(result muxOpenResult) ([]byte, error) {
	return json.Marshal(result)
}

func readMuxOpenResultPayload(payload []byte) (muxOpenResult, error) {
	var result muxOpenResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return muxOpenResult{}, err
	}
	return result, nil
}

func unmarshalMuxOpenResult(payload []byte) error {
	_, err := readMuxOpenResultPayload(payload)
	return err
}

func writeFrame(w io.Writer, payloadValue any) error {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	if len(payload) > maxRequestSize {
		return fmt.Errorf("relay request exceeds %d bytes", maxRequestSize)
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func readRelayRequest(r io.Reader) (relayRequest, error) {
	payload, err := readFrame(r)
	if err != nil {
		return relayRequest{}, err
	}

	var request relayRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return relayRequest{}, err
	}
	return request, nil
}

func readRelayResponse(r io.Reader) (relayResponse, error) {
	payload, err := readFrame(r)
	if err != nil {
		return relayResponse{}, err
	}

	var response relayResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return relayResponse{}, err
	}
	return response, nil
}

func readRelayOpenFrame(r io.Reader) (relayOpenFrame, error) {
	payload, err := readFrame(r)
	if err != nil {
		return relayOpenFrame{}, err
	}

	var frame relayOpenFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return relayOpenFrame{}, err
	}
	return frame, nil
}

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxRequestSize {
		return nil, fmt.Errorf("invalid relay request size %d", size)
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func relayTrafficClassFromMetadata(metadata map[string]any) upstream.TrafficClass {
	if len(metadata) == 0 {
		return upstream.TrafficClassUnknown
	}
	raw, ok := metadata[relayMetadataTrafficClass]
	if !ok {
		return upstream.TrafficClassUnknown
	}
	value, ok := raw.(string)
	if !ok {
		return upstream.TrafficClassUnknown
	}
	switch upstream.TrafficClass(strings.ToLower(strings.TrimSpace(value))) {
	case upstream.TrafficClassInteractive:
		return upstream.TrafficClassInteractive
	case upstream.TrafficClassBulk:
		return upstream.TrafficClassBulk
	default:
		return upstream.TrafficClassUnknown
	}
}
