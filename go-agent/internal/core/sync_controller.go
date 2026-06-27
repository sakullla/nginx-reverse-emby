package core

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type SyncClient interface {
	Sync(context.Context, control.SyncRequest) (model.Snapshot, error)
}

type Updater interface {
	Stage(context.Context, model.VersionPackage) (string, error)
	Activate(stagedPath string, desiredVersion string) error
}

type TrafficReporter interface {
	TrafficReport(context.Context, map[string]string) (TrafficReport, error)
}

type TrafficReport struct {
	Stats           map[string]any
	StatsPresent    bool
	RuntimeMetadata map[string]string
}

type HostMetricsReporter interface {
	HostMetricsReport(context.Context) (HostMetricsReport, error)
}

type HostMetricsReport struct {
	Stats        map[string]any
	StatsPresent bool
}

type SyncPlan struct {
	Request         control.SyncRequest
	RuntimeMetadata map[string]string
}

type ManagedCertificateReporter interface {
	ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error)
}

type SyncController struct {
	Store                Store
	Runtime              *Runtime
	SyncClient           SyncClient
	Updater              Updater
	Traffic              TrafficReporter
	HostMetrics          HostMetricsReporter
	CertReports          ManagedCertificateReporter
	CurrentPackageSHA256 string
}
