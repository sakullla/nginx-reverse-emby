package embedded

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func WriteObservabilityMetrics(w io.Writer) error {
	return observability.Default().WritePrometheus(w)
}
