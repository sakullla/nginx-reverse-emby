package l4

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return traffic.CopyPreferReaderFrom(dst, src)
}
