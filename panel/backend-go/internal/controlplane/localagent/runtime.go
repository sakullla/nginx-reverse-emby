package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Store interface {
	SnapshotStore
	RuntimeStateStore
	tunnelPKICredentialTargetStore
}

type tunnelPKICredentialTargetStore interface {
	LoadRelayListenerCredentialTargets(context.Context, string) ([]storage.RelayListener, error)
}

type embeddedRuntimeRunner interface {
	Run(context.Context) error
	SyncNow(context.Context) error
	ApplyRevision(context.Context, goagentembedded.Snapshot) error
	ApplyRevisionWithDrainTimeout(context.Context, goagentembedded.Snapshot, time.Duration) error
	DiagnoseSnapshot(context.Context, goagentembedded.Snapshot, goagentembedded.DiagnosticRequest) (map[string]any, error)
}

var newEmbeddedRuntime = func(cfg goagentembedded.Config, source goagentembedded.SyncSource, sink goagentembedded.StateSink) (embeddedRuntimeRunner, error) {
	return goagentembedded.New(cfg, source, sink)
}

type Runtime struct {
	source            *SyncSource
	sink              *StateSink
	runtime           embeddedRuntimeRunner
	agentID           string
	heartbeatInterval time.Duration
	credentials       tunnelCredentialStore
	credentialTargets tunnelPKICredentialTargetStore
	pkiMu             sync.RWMutex
	tunnelPKI         TunnelPKIService
	pkiReconcileMu    sync.Mutex
	now               func() time.Time
}

func NewRuntime(cfg config.Config, store Store) (*Runtime, error) {
	bridge := newSyncRequestBridge()
	source := newSyncSourceWithBridge(store, cfg.LocalAgentID, bridge)
	sink := newStateSinkWithBridge(store, cfg.LocalAgentID, bridge)

	runtime, err := newEmbeddedRuntime(
		goagentembedded.Config{
			AgentID:              cfg.LocalAgentID,
			AgentName:            cfg.LocalAgentName,
			DataDir:              cfg.DataDir,
			HeartbeatInterval:    cfg.HeartbeatInterval,
			DDNSIPProbeInterval:  cfg.LocalAgentDDNSIPProbeInterval,
			HTTP3Enabled:         cfg.LocalAgentHTTP3Enabled,
			TrafficStatsEnabled:  cfg.LocalAgentTrafficStatsEnabled,
			TrafficStatsExplicit: cfg.LocalAgentTrafficStatsExplicit,
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
	var credentials tunnelCredentialStore
	if owner, ok := runtime.(interface {
		TunnelCredentialStore() *goagentembedded.CredentialStore
	}); ok {
		credentials = owner.TunnelCredentialStore()
	}

	return &Runtime{
		source: source, sink: sink, runtime: runtime,
		agentID: cfg.LocalAgentID, heartbeatInterval: cfg.HeartbeatInterval,
		credentials: credentials, credentialTargets: store, now: time.Now,
	}, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	if !r.tunnelPKIConfigured() {
		return r.runtime.Run(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.runTunnelPKIReconciler(runCtx)
	return r.runtime.Run(runCtx)
}

func (r *Runtime) SyncNow(ctx context.Context) error {
	return r.runtime.SyncNow(ctx)
}

func (r *Runtime) GenerationDrainSnapshot() goagentembedded.GenerationDrainSnapshot {
	if r == nil || r.runtime == nil {
		return goagentembedded.GenerationDrainSnapshot{}
	}
	reader, ok := r.runtime.(interface {
		GenerationDrainSnapshot() goagentembedded.GenerationDrainSnapshot
	})
	if !ok {
		return goagentembedded.GenerationDrainSnapshot{}
	}
	return reader.GenerationDrainSnapshot()
}

func (r *Runtime) ApplyRevision(ctx context.Context, snapshot Snapshot) error {
	return r.ApplyRevisionWithDrainTimeout(ctx, snapshot, 0)
}

func (r *Runtime) ApplyRevisionWithDrainTimeout(ctx context.Context, snapshot Snapshot, drainTimeout time.Duration) error {
	if r == nil || r.runtime == nil {
		return errors.New("embedded runtime is not initialized")
	}
	if snapshotRequiresTunnelPKI(snapshot) && r.tunnelPKIConfigured() {
		if err := r.ReconcileTunnelPKI(ctx); err != nil {
			return fmt.Errorf("reconcile embedded tunnel PKI before revision apply: %w", err)
		}
	}
	return r.runtime.ApplyRevisionWithDrainTimeout(ctx, toEmbeddedSnapshot(snapshot), drainTimeout)
}

func snapshotRequiresTunnelPKI(snapshot Snapshot) bool {
	for _, listener := range snapshot.RelayListeners {
		if listener.Enabled && strings.EqualFold(strings.TrimSpace(listener.TLSMode), "pki_mtls") {
			return true
		}
	}
	return false
}

func (r *Runtime) SyncSource() *SyncSource {
	return r.source
}

func (r *Runtime) StateSink() *StateSink {
	return r.sink
}

func (r *Runtime) DiagnoseSnapshot(ctx context.Context, snapshot Snapshot, envelope service.TaskEnvelope) (map[string]any, error) {
	if r == nil || r.runtime == nil {
		return nil, errors.New("embedded runtime is not initialized")
	}
	ruleID, err := taskRuleID(envelope.Payload)
	if err != nil {
		return nil, err
	}
	return r.runtime.DiagnoseSnapshot(ctx, toEmbeddedSnapshot(snapshot), goagentembedded.DiagnosticRequest{
		TaskType: envelope.Type,
		RuleID:   ruleID,
	})
}

type syncSourceAdapter struct {
	source *SyncSource
}

func (a syncSourceAdapter) Sync(ctx context.Context, request goagentembedded.SyncRequest) (goagentembedded.Snapshot, error) {
	snapshot, err := a.source.Sync(ctx, fromEmbeddedSyncRequest(request))
	if err != nil {
		return goagentembedded.Snapshot{}, err
	}
	if len(request.PluginLogs) > 0 && request.PluginLogsAcknowledged != nil {
		if err := request.PluginLogsAcknowledged(); err != nil {
			return goagentembedded.Snapshot{}, err
		}
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
		DesiredVersion:     snapshot.DesiredVersion,
		Revision:           snapshot.Revision,
		PluginGenerations:  toEmbeddedPluginGenerations(snapshot.PluginGenerations),
		PluginDependencies: toEmbeddedPluginDependencies(snapshot.PluginDependencies),
		PluginPolicies:     toEmbeddedPluginPolicies(snapshot.PluginPolicies),
		AgentConfig: goagentembedded.AgentConfig{
			OutboundProxyURL:     snapshot.AgentConfig.OutboundProxyURL,
			TrafficStatsInterval: snapshot.AgentConfig.TrafficStatsInterval,
			TrafficStatsEnabled:  snapshot.AgentConfig.TrafficStatsEnabled,
			TrafficBlocked:       snapshot.AgentConfig.TrafficBlocked,
			TrafficBlockReason:   snapshot.AgentConfig.TrafficBlockReason,
		},
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
	if snapshot.DDNSConfig != nil {
		embedded.DDNSConfig = &goagentembedded.DDNSExtractConfig{
			Enabled: snapshot.DDNSConfig.Enabled,
			Domain:  snapshot.DDNSConfig.Domain,
			IPv4: goagentembedded.DDNSFamily{
				Enabled: snapshot.DDNSConfig.IPv4.Enabled, Source: snapshot.DDNSConfig.IPv4.Source,
				Interface: snapshot.DDNSConfig.IPv4.Interface,
			},
			IPv6: goagentembedded.DDNSFamily{
				Enabled: snapshot.DDNSConfig.IPv6.Enabled, Source: snapshot.DDNSConfig.IPv6.Source,
				Interface: snapshot.DDNSConfig.IPv6.Interface,
			},
		}
	}
	// Snapshot rules are already runtime-filtered by storage. Their backend
	// types intentionally omit Enabled, so every included rule must remain
	// enabled when it crosses into the embedded agent model.
	embedded.Rules = make([]goagentembedded.HTTPRule, 0, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		embedded.Rules = append(embedded.Rules, goagentembedded.HTTPRule{
			ID:                 rule.ID,
			Enabled:            true,
			FrontendURL:        rule.FrontendURL,
			Backends:           toEmbeddedHTTPBackends(rule.Backends),
			LoadBalancing:      goagentembedded.LoadBalancing{Strategy: rule.LoadBalancing.Strategy},
			ProxyRedirect:      rule.ProxyRedirect,
			PassProxyHeaders:   rule.PassProxyHeaders,
			UserAgent:          rule.UserAgent,
			CustomHeaders:      toEmbeddedHTTPHeaders(rule.CustomHeaders),
			TrustedProxyRanges: append([]string(nil), rule.TrustedProxyRanges...),
			PolicyRef:          toEmbeddedPolicyRef(rule.PolicyRef),
			RelayLayers:        cloneRelayLayers(rule.RelayLayers),
			Revision:           rule.Revision,
		})
	}
	embedded.L4Rules = make([]goagentembedded.L4Rule, 0, len(snapshot.L4Rules))
	for _, rule := range snapshot.L4Rules {
		embedded.L4Rules = append(embedded.L4Rules, goagentembedded.L4Rule{
			ID:            rule.ID,
			Enabled:       true,
			Name:          rule.Name,
			Protocol:      rule.Protocol,
			ListenHost:    rule.ListenHost,
			ListenPort:    rule.ListenPort,
			Backends:      toEmbeddedL4Backends(rule.Backends),
			LoadBalancing: goagentembedded.LoadBalancing{Strategy: rule.LoadBalancing.Strategy},
			Tuning: goagentembedded.L4Tuning{
				ProxyProtocol: goagentembedded.L4ProxyProtocolTuning{
					Decode:       rule.Tuning.ProxyProtocol.Decode,
					Send:         rule.Tuning.ProxyProtocol.Send,
					TrustedPeers: append([]string(nil), rule.Tuning.ProxyProtocol.TrustedPeers...),
				},
			},
			RelayLayers:     cloneRelayLayers(rule.RelayLayers),
			RelayObfs:       rule.RelayObfs,
			ListenMode:      rule.ListenMode,
			EgressProfileID: copyOptionalInt(rule.EgressProfileID),
			ProxyEntryAuth: goagentembedded.L4ProxyEntryAuth{
				Enabled:  rule.ProxyEntryAuth.Enabled,
				Username: rule.ProxyEntryAuth.Username,
				Password: rule.ProxyEntryAuth.Password,
			},
			PolicyRef: toEmbeddedPolicyRef(rule.PolicyRef),
			Revision:  rule.Revision,
		})
	}
	embedded.RelayListeners = make([]goagentembedded.RelayListener, 0, len(snapshot.RelayListeners))
	for _, listener := range snapshot.RelayListeners {
		embedded.RelayListeners = append(embedded.RelayListeners, goagentembedded.RelayListener{
			ID:                      listener.ID,
			AgentID:                 listener.AgentID,
			AgentName:               listener.AgentName,
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
			PKIIdentityID:           listener.PKIIdentityID,
			PKIIdentityState:        listener.PKIIdentityState,
			PKICertificateID:        listener.PKICertificateID,
			Tags:                    append([]string(nil), listener.Tags...),
			Revision:                listener.Revision,
		})
	}
	copyEmbeddedEgressProfiles(&embedded, snapshot.EgressProfiles)
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

func toEmbeddedPolicyRef(ref *storage.PolicyRef) *goagentembedded.PolicyRef {
	if ref == nil {
		return nil
	}
	return &goagentembedded.PolicyRef{ID: ref.ID, Overlay: append(json.RawMessage(nil), ref.Overlay...)}
}

func toEmbeddedPluginPolicies(policies []storage.PluginPolicy) []goagentembedded.PluginPolicy {
	if policies == nil {
		return nil
	}
	data, err := json.Marshal(policies)
	if err != nil {
		return nil
	}
	var embedded []goagentembedded.PluginPolicy
	if err := json.Unmarshal(data, &embedded); err != nil {
		return nil
	}
	return embedded
}

func toEmbeddedPluginGenerations(generations []storage.PluginGeneration) []goagentembedded.PluginGeneration {
	if generations == nil {
		return nil
	}
	data, err := json.Marshal(generations)
	if err != nil {
		return nil
	}
	var embedded []goagentembedded.PluginGeneration
	if err := json.Unmarshal(data, &embedded); err != nil {
		return nil
	}
	return embedded
}

func toEmbeddedPluginDependencies(dependencies []storage.PluginDependencyEdge) []goagentembedded.PluginDependencyEdge {
	if dependencies == nil {
		return nil
	}
	data, err := json.Marshal(dependencies)
	if err != nil {
		return nil
	}
	var embedded []goagentembedded.PluginDependencyEdge
	if err := json.Unmarshal(data, &embedded); err != nil {
		return nil
	}
	return embedded
}

func copyEmbeddedEgressProfiles(embedded *goagentembedded.Snapshot, profiles []storage.EgressProfile) {
	data, err := json.Marshal(profiles)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &embedded.EgressProfiles)
}

func fromEmbeddedRuntimeState(state goagentembedded.RuntimeState) RuntimeState {
	copyValue := RuntimeState{
		NodeID:          state.NodeID,
		CurrentRevision: state.CurrentRevision,
		Status:          state.Status,
	}
	if data, err := json.Marshal(state.PluginStatuses); err == nil {
		_ = json.Unmarshal(data, &copyValue.PluginStatuses)
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
	statsPresent := request.StatsPresent || len(request.Stats) > 0
	copyValue := SyncRequest{
		CurrentRevision:   request.CurrentRevision,
		LastApplyRevision: request.LastApplyRevision,
		LastApplyStatus:   request.LastApplyStatus,
		LastApplyMessage:  request.LastApplyMessage,
		LastSeenIPv4:      request.LastSeenIPv4,
		LastSeenIPv6:      request.LastSeenIPv6,
		StatsPresent:      statsPresent,
	}
	if data, err := json.Marshal(request.PluginStatuses); err == nil {
		_ = json.Unmarshal(data, &copyValue.PluginStatuses)
	}
	if data, err := json.Marshal(request.PluginLogs); err == nil {
		_ = json.Unmarshal(data, &copyValue.PluginLogs)
	}
	if statsPresent {
		if data, err := json.Marshal(request.Stats); err == nil {
			var stats map[string]any
			if json.Unmarshal(data, &stats) == nil {
				copyValue.Stats = stats
			}
		}
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
			NotAfter:     report.NotAfter,
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

func cloneRelayLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = append([]int(nil), layer...)
	}
	return cloned
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
