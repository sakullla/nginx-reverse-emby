package runtime

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

type Runtime struct {
	HTTPProxy model.HTTPProxyConfig
}

func New() Runtime {
	return Runtime{}
}
