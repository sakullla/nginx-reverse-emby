package network

import sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"

// StreamReadSizer bounds temporary TCP read storage. Its zero value starts
// small, grows for bulk transfers, and resets after an empty poll. Use only
// under the stream's read gate; datagram reads must preserve packet boundaries.
type StreamReadSizer struct{ next int }

func (s *StreamReadSizer) Size(maximum int) int {
	return min(maximum, max(4<<10, s.next))
}

func (s *StreamReadSizer) Observe(n, size int) {
	if n == 0 {
		s.next = 0
	} else if n == size && size >= 4<<10 {
		s.next = min(2*size, sdk.ManagedNetworkMaxChunkBytes)
	}
}
