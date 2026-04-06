package relay

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxRequestSize = 1 << 20

type relayRequest struct {
	Network string `json:"network"`
	Target  string `json:"target"`
	Chain   []Hop  `json:"chain,omitempty"`
}

func writeRelayRequest(w io.Writer, request relayRequest) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(payload) > maxRequestSize {
		return fmt.Errorf("relay request exceeds %d bytes", maxRequestSize)
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readRelayRequest(r io.Reader) (relayRequest, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return relayRequest{}, err
	}

	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxRequestSize {
		return relayRequest{}, fmt.Errorf("invalid relay request size %d", size)
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return relayRequest{}, err
	}

	var request relayRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return relayRequest{}, err
	}
	return request, nil
}
