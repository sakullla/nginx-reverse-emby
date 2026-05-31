package l4

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
)

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyPreferReaderFrom(dst, src)
}
