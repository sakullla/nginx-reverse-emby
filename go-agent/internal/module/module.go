package module

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Capability struct {
	Name     string
	Enabled  bool
	Metadata map[string]string
}

type Health struct {
	Status  string
	Message string
}

type Module interface {
	Name() string
	Capabilities() []Capability
	Health(context.Context) Health
	Start(context.Context, model.Snapshot) error
	Stop(context.Context) error
}

type Activator interface {
	Activate(context.Context, model.Snapshot) error
}
