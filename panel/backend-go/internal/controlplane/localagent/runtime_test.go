package localagent

import (
	"context"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	var started bool
	application := app.New(
		config.Config{
			ListenAddr:       "127.0.0.1:0",
			EnableLocalAgent: true,
		},
		http.NewServeMux(),
		nil,
		func(context.Context) error {
			started = true
			return nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = application.Run(ctx)

	if !started {
		t.Fatal("embedded local agent did not start")
	}
}
