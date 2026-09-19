//go:build !integration

package main

import (
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func controlPlanePKIRuntimeClock() time.Time {
	return time.Now()
}

func controlPlanePKIAuthorityHeartbeatInterval() time.Duration {
	return 0
}

func controlPlanePKIRestoreHooks() storage.PKISQLiteRestoreHooks {
	return storage.PKISQLiteRestoreHooks{}
}
