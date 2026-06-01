package app

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func (a *App) syncController() *core.SyncController {
	controller := &core.SyncController{
		Store:                a.store,
		Runtime:              a.runtime,
		SyncClient:           a.syncClient,
		Updater:              a.updater,
		Traffic:              a.trafficReporter(),
		CurrentPackageSHA256: a.cfg.RuntimePackageSHA256,
	}
	controller.CertReports = a.certReports
	return controller
}

func (a *App) trafficReporter() core.TrafficReporter {
	if a == nil || a.trafficReports == nil {
		return moduletraffic.NewReporter(moduletraffic.ReporterConfig{})
	}
	return a.trafficReports
}
