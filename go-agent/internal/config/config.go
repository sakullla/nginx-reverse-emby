package config

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"

type Config = app.Config

func Default() Config {
	return app.Config{AgentID: "bootstrap"}
}
