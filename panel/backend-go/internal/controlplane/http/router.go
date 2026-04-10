package http

import (
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

type Dependencies struct {
	Config config.Config
}

func NewRouter(Dependencies) (http.Handler, error) {
	return http.NewServeMux(), nil
}
