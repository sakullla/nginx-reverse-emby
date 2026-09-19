package app

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func (a *App) syncController() *core.SyncController {
	return &core.SyncController{
		Store:                a.store,
		Runtime:              a.runtime,
		SyncClient:           a.relayMTLSSyncClient(),
		Updater:              a.updater,
		PackageStages:        a.packageStages,
		Traffic:              a.trafficReporter(),
		HostMetrics:          a.hostMetricsReporter(),
		CertReports:          a.certReports,
		DDNSReporter:         a.ddns,
		CurrentPackageSHA256: a.cfg.RuntimePackageSHA256,
	}
}

func (a *App) trafficReporter() core.TrafficReporter {
	if a == nil || a.trafficReports == nil {
		return moduletraffic.NewReporter(moduletraffic.ReporterConfig{})
	}
	return a.trafficReports
}

func (a *App) hostMetricsReporter() core.HostMetricsReporter {
	if a == nil || a.hostMetricsReports == nil {
		return nil
	}
	return a.hostMetricsReports
}
