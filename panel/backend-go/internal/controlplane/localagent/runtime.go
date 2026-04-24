package localagent

import (
	"context"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Store interface {
	SnapshotStore
	RuntimeStateStore
}

type embeddedRuntimeRunner interface {
	Run(context.Context) error
	SyncNow(context.Context) error
}

var newEmbeddedRuntime = func(cfg goagentembedded.Config, source goagentembedded.SyncSource, sink goagentembedded.StateSink) (embeddedRuntimeRunner, error) {
	return goagentembedded.New(cfg, source, sink)
}

type Runtime struct {
	source  *SyncSource
	sink    *StateSink
	runtime embeddedRuntimeRunner
}

func NewRuntime(cfg config.Config, store Store) (*Runtime, error) {
	bridge := newSyncRequestBridge()
	source := newSyncSourceWithBridge(store, cfg.LocalAgentID, bridge)
	sink := newStateSinkWithBridge(store, cfg.LocalAgentID, bridge)

	runtime, err := newEmbeddedRuntime(
		goagentembedded.Config{
			AgentID:           cfg.LocalAgentID,
			AgentName:         cfg.LocalAgentName,
			DataDir:           cfg.DataDir,
			HeartbeatInterval: cfg.HeartbeatInterval,
			HTTP3Enabled:      cfg.LocalAgentHTTP3Enabled,
			HTTPTransport: goagentembedded.HTTPTransportConfig{
				DialTimeout:           cfg.LocalAgentHTTPTransport.DialTimeout,
				TLSHandshakeTimeout:   cfg.LocalAgentHTTPTransport.TLSHandshakeTimeout,
				ResponseHeaderTimeout: cfg.LocalAgentHTTPTransport.ResponseHeaderTimeout,
				IdleConnTimeout:       cfg.LocalAgentHTTPTransport.IdleConnTimeout,
				KeepAlive:             cfg.LocalAgentHTTPTransport.KeepAlive,
			},
			HTTPResilience: goagentembedded.HTTPResilienceConfig{
				ResumeEnabled:            cfg.LocalAgentHTTPResilience.ResumeEnabled,
				ResumeMaxAttempts:        cfg.LocalAgentHTTPResilience.ResumeMaxAttempts,
				SameBackendRetryAttempts: cfg.LocalAgentHTTPResilience.SameBackendRetryAttempts,
			},
			BackendFailures: goagentembedded.BackendFailureConfig{
				BackoffBase:  cfg.LocalAgentBackendFailures.BackoffBase,
				BackoffLimit: cfg.LocalAgentBackendFailures.BackoffLimit,
			},
			BackendFailuresExplicit: cfg.LocalAgentBackendFailuresExplicit,
			RelayTimeouts: goagentembedded.RelayTimeoutConfig{
				DialTimeout:      cfg.LocalAgentRelayTimeouts.DialTimeout,
				HandshakeTimeout: cfg.LocalAgentRelayTimeouts.HandshakeTimeout,
				FrameTimeout:     cfg.LocalAgentRelayTimeouts.FrameTimeout,
				IdleTimeout:      cfg.LocalAgentRelayTimeouts.IdleTimeout,
			},
		},
		syncSourceAdapter{source: source},
		stateSinkAdapter{sink: sink},
	)
	if err != nil {
		return nil, err
	}

	return &Runtime{
		source:  source,
		sink:    sink,
		runtime: runtime,
	}, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	return r.runtime.Run(ctx)
}

func (r *Runtime) SyncNow(ctx context.Context) error {
	return r.runtime.SyncNow(ctx)
}

func (r *Runtime) SyncSource() *SyncSource {
	return r.source
}

func (r *Runtime) StateSink() *StateSink {
	return r.sink
}

type syncSourceAdapter struct {
	source *SyncSource
}

func (a syncSourceAdapter) Sync(ctx context.Context, request goagentembedded.SyncRequest) (goagentembedded.Snapshot, error) {
	snapshot, err := a.source.Sync(ctx, fromEmbeddedSyncRequest(request))
	if err != nil {
		return goagentembedded.Snapshot{}, err
	}
	return toEmbeddedSnapshot(snapshot), nil
}

type stateSinkAdapter struct {
	sink *StateSink
}

func (a stateSinkAdapter) Save(ctx context.Context, state goagentembedded.RuntimeState) error {
	return a.sink.Save(ctx, fromEmbeddedRuntimeState(state))
}

func toEmbeddedSnapshot(snapshot Snapshot) goagentembedded.Snapshot {
	embedded := goagentembedded.Snapshot{
		DesiredVersion: snapshot.DesiredVersion,
		Revision:       snapshot.Revision,
	}
	if snapshot.VersionPackage != nil {
		embedded.VersionPackage = &goagentembedded.VersionPackage{
			URL:      snapshot.VersionPackage.URL,
			SHA256:   snapshot.VersionPackage.SHA256,
			Platform: snapshot.VersionPackage.Platform,
			Filename: snapshot.VersionPackage.Filename,
			Size:     snapshot.VersionPackage.Size,
		}
	}
	embedded.Rules = make([]goagentembedded.HTTPRule, 0, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		embedded.Rules = append(embedded.Rules, goagentembedded.HTTPRule{
			ID:               rule.ID,
			FrontendURL:      rule.FrontendURL,
			BackendURL:       rule.BackendURL,
			Backends:         toEmbeddedHTTPBackends(rule.Backends),
			LoadBalancing:    goagentembedded.LoadBalancing{Strategy: rule.LoadBalancing.Strategy},
			ProxyRedirect:    rule.ProxyRedirect,
			PassProxyHeaders: rule.PassProxyHeaders,
			UserAgent:        rule.UserAgent,
			CustomHeaders:    toEmbeddedHTTPHeaders(rule.CustomHeaders),
			RelayChain:       append([]int(nil), rule.RelayChain...),
			Revision:         rule.Revision,
		})
	}
	embedded.L4Rules = make([]goagentembedded.L4Rule, 0, len(snapshot.L4Rules))
	for _, rule := range snapshot.L4Rules {
		embedded.L4Rules = append(embedded.L4Rules, goagentembedded.L4Rule{
			ID:            rule.ID,
			Name:          rule.Name,
			Protocol:      rule.Protocol,
			ListenHost:    rule.ListenHost,
			ListenPort:    rule.ListenPort,
			UpstreamHost:  rule.UpstreamHost,
			UpstreamPort:  rule.UpstreamPort,
			Backends:      toEmbeddedL4Backends(rule.Backends),
			LoadBalancing: goagentembedded.LoadBalancing{Strategy: rule.LoadBalancing.Strategy},
			Tuning: goagentembedded.L4Tuning{
				ProxyProtocol: goagentembedded.L4ProxyProtocolTuning{
					Decode: rule.Tuning.ProxyProtocol.Decode,
					Send:   rule.Tuning.ProxyProtocol.Send,
				},
			},
			RelayChain: append([]int(nil), rule.RelayChain...),
			RelayObfs:  rule.RelayObfs,
			Revision:   rule.Revision,
		})
	}
	embedded.RelayListeners = make([]goagentembedded.RelayListener, 0, len(snapshot.RelayListeners))
	for _, listener := range snapshot.RelayListeners {
		embedded.RelayListeners = append(embedded.RelayListeners, goagentembedded.RelayListener{
			ID:                      listener.ID,
			AgentID:                 listener.AgentID,
			Name:                    listener.Name,
			ListenHost:              listener.ListenHost,
			BindHosts:               append([]string(nil), listener.BindHosts...),
			ListenPort:              listener.ListenPort,
			PublicHost:              listener.PublicHost,
			PublicPort:              listener.PublicPort,
			Enabled:                 listener.Enabled,
			CertificateID:           copyOptionalInt(listener.CertificateID),
			TLSMode:                 listener.TLSMode,
			TransportMode:           listener.TransportMode,
			AllowTransportFallback:  listener.AllowTransportFallback,
			ObfsMode:                listener.ObfsMode,
			PinSet:                  toEmbeddedRelayPins(listener.PinSet),
			TrustedCACertificateIDs: append([]int(nil), listener.TrustedCACertificateIDs...),
			AllowSelfSigned:         listener.AllowSelfSigned,
			Tags:                    append([]string(nil), listener.Tags...),
			Revision:                listener.Revision,
		})
	}
	embedded.Certificates = make([]goagentembedded.ManagedCertificateBundle, 0, len(snapshot.Certificates))
	for _, bundle := range snapshot.Certificates {
		embedded.Certificates = append(embedded.Certificates, goagentembedded.ManagedCertificateBundle{
			ID:       bundle.ID,
			Domain:   bundle.Domain,
			Revision: bundle.Revision,
			CertPEM:  bundle.CertPEM,
			KeyPEM:   bundle.KeyPEM,
		})
	}
	embedded.CertificatePolicies = make([]goagentembedded.ManagedCertificatePolicy, 0, len(snapshot.CertificatePolicies))
	for _, policy := range snapshot.CertificatePolicies {
		embedded.CertificatePolicies = append(embedded.CertificatePolicies, goagentembedded.ManagedCertificatePolicy{
			ID:          policy.ID,
			Domain:      policy.Domain,
			Enabled:     policy.Enabled,
			Scope:       policy.Scope,
			IssuerMode:  policy.IssuerMode,
			Status:      policy.Status,
			LastIssueAt: policy.LastIssueAt,
			LastError:   policy.LastError,
			ACMEInfo: goagentembedded.ManagedCertificateACMEInfo{
				MainDomain: policy.ACMEInfo.MainDomain,
				KeyLength:  policy.ACMEInfo.KeyLength,
				SANDomains: policy.ACMEInfo.SANDomains,
				Profile:    policy.ACMEInfo.Profile,
				CA:         policy.ACMEInfo.CA,
				Created:    policy.ACMEInfo.Created,
				Renew:      policy.ACMEInfo.Renew,
			},
			Tags:            append([]string(nil), policy.Tags...),
			Revision:        policy.Revision,
			Usage:           policy.Usage,
			CertificateType: policy.CertificateType,
			SelfSigned:      policy.SelfSigned,
		})
	}
	return embedded
}

func fromEmbeddedRuntimeState(state goagentembedded.RuntimeState) RuntimeState {
	copyValue := RuntimeState{
		NodeID:          state.NodeID,
		CurrentRevision: state.CurrentRevision,
		Status:          state.Status,
	}
	if state.Metadata == nil {
		return copyValue
	}

	copyValue.Metadata = make(map[string]string, len(state.Metadata))
	for key, value := range state.Metadata {
		copyValue.Metadata[key] = value
	}
	return copyValue
}

func fromEmbeddedSyncRequest(request goagentembedded.SyncRequest) SyncRequest {
	copyValue := SyncRequest{
		CurrentRevision:   request.CurrentRevision,
		LastApplyRevision: request.LastApplyRevision,
		LastApplyStatus:   request.LastApplyStatus,
		LastApplyMessage:  request.LastApplyMessage,
	}
	if len(request.ManagedCertificateReports) == 0 {
		return copyValue
	}

	copyValue.ManagedCertificateReports = make([]storage.ManagedCertificateReport, 0, len(request.ManagedCertificateReports))
	for _, report := range request.ManagedCertificateReports {
		copyValue.ManagedCertificateReports = append(copyValue.ManagedCertificateReports, storage.ManagedCertificateReport{
			ID:           report.ID,
			Domain:       report.Domain,
			Status:       report.Status,
			LastIssueAt:  report.LastIssueAt,
			LastError:    report.LastError,
			MaterialHash: report.MaterialHash,
			ACMEInfo: storage.ManagedCertificateACMEInfo{
				MainDomain: report.ACMEInfo.MainDomain,
				KeyLength:  report.ACMEInfo.KeyLength,
				SANDomains: report.ACMEInfo.SANDomains,
				Profile:    report.ACMEInfo.Profile,
				CA:         report.ACMEInfo.CA,
				Created:    report.ACMEInfo.Created,
				Renew:      report.ACMEInfo.Renew,
			},
			UpdatedAt: report.UpdatedAt,
		})
	}
	return copyValue
}

func toEmbeddedHTTPBackends(backends []storage.HTTPBackend) []goagentembedded.HTTPBackend {
	if len(backends) == 0 {
		return nil
	}
	embedded := make([]goagentembedded.HTTPBackend, 0, len(backends))
	for _, backend := range backends {
		embedded = append(embedded, goagentembedded.HTTPBackend{URL: backend.URL})
	}
	return embedded
}

func toEmbeddedHTTPHeaders(headers []storage.HTTPHeader) []goagentembedded.HTTPHeader {
	if len(headers) == 0 {
		return nil
	}
	embedded := make([]goagentembedded.HTTPHeader, 0, len(headers))
	for _, header := range headers {
		embedded = append(embedded, goagentembedded.HTTPHeader{Name: header.Name, Value: header.Value})
	}
	return embedded
}

func toEmbeddedL4Backends(backends []storage.L4Backend) []goagentembedded.L4Backend {
	if len(backends) == 0 {
		return nil
	}
	embedded := make([]goagentembedded.L4Backend, 0, len(backends))
	for _, backend := range backends {
		embedded = append(embedded, goagentembedded.L4Backend{Host: backend.Host, Port: backend.Port})
	}
	return embedded
}

func toEmbeddedRelayPins(pins []storage.RelayPin) []goagentembedded.RelayPin {
	if len(pins) == 0 {
		return nil
	}
	embedded := make([]goagentembedded.RelayPin, 0, len(pins))
	for _, pin := range pins {
		embedded = append(embedded, goagentembedded.RelayPin{Type: pin.Type, Value: pin.Value})
	}
	return embedded
}

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
