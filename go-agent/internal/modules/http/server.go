package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Server struct {
	routes            map[string][]*routeEntry
	policyEvaluator   policy.Evaluator
	trafficBlockState trafficBlockStateValue
}

type TLSMaterialProvider interface {
	ServerCertificateForHost(context.Context, string) (*tls.Certificate, error)
}

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type Providers struct {
	TLS                  TLSMaterialProvider
	Relay                RelayMaterialProvider
	EgressProfiles       []model.EgressProfile
	EgressResolver       module.EgressResolver
	FinalHopDialer       relay.FinalHopDialer
	PolicyEvaluator      policy.Evaluator
	HTTPBackendProviders HTTPBackendProviderResolver
	providerSessions     HTTPSessionRegistrar
	providerGeneration   string
	providerTracker      *httpSessionTracker
	providerIdleTimeout  time.Duration
}

type routeEntry struct {
	rule                       model.HTTPRule
	backends                   []httpBackend
	backendCache               *model.Cache
	transport                  *http.Transport
	directInteractiveTransport *http.Transport
	directBulkTransport        *http.Transport
	relayInteractiveTransport  *http.Transport
	relayBulkTransport         *http.Transport
	resilience                 StreamResilienceOptions
	modifyResp                 func(*http.Response) error
	selectionScope             string
	frontendPath               string
	providerSessions           HTTPSessionRegistrar
	providerGeneration         string
	providerTracker            *httpSessionTracker
	providerIdleTimeout        time.Duration
}

type HTTPBackendProviderResolver interface {
	Resolve(instanceID, providerID string) (HTTPBackendProvider, bool)
}

type HTTPBackendProvider interface {
	InstanceID() string
	ProviderID() string
	Generation() string
	Acquire() (io.Closer, error)
	RoundTrip(*http.Request, rpc.HTTPBackendProviderAuthority) (*http.Response, error)
}

type providerRequestLease struct {
	once               sync.Once
	local              io.Closer
	tracker            *httpSessionTracker
	session            *httpRequestSession
	releaseProgressive func()
}

func (lease *providerRequestLease) Close() error {
	if lease == nil {
		return nil
	}
	var err error
	lease.once.Do(func() {
		if lease.releaseProgressive != nil {
			lease.releaseProgressive()
		}
		if lease.tracker != nil {
			lease.tracker.finish(lease.session)
		}
		if lease.local != nil {
			err = lease.local.Close()
		}
	})
	return err
}

func (e *routeEntry) acquireProviderRequest(writer http.ResponseWriter, request *http.Request, candidate httpCandidate) (*http.Request, *providerRequestLease, *providerRequestScope, error) {
	if candidate.provider == nil {
		return request, nil, nil, nil
	}
	local, err := candidate.provider.Acquire()
	if err != nil {
		return nil, nil, nil, err
	}
	if e.providerSessions == nil || e.providerGeneration == "" {
		_ = local.Close()
		return nil, nil, nil, errors.New("HTTP backend provider session registrar is unavailable")
	}
	ctx, cancel := context.WithCancel(request.Context())
	tracker := e.providerTracker
	if tracker == nil {
		cancel()
		_ = local.Close()
		return nil, nil, nil, errors.New("HTTP backend provider session tracker is unavailable")
	}
	outer := httpRequestSessionFromContext(request.Context())
	if outer == nil {
		cancel()
		_ = local.Close()
		return nil, nil, nil, errors.New("HTTP backend provider outer session is unavailable")
	}
	entity := httpRuleEntityID(e.rule)
	lease := &providerRequestLease{local: local, tracker: tracker}
	lease.session = tracker.startModule("http", entity, cancel)
	lease.releaseProgressive = lease.session.retainProgressiveDrain()
	tracker.register(lease.session)
	lease.session.mu.Lock()
	registrationErr := lease.session.registrationErr
	lease.session.mu.Unlock()
	if registrationErr != nil {
		_ = lease.Close()
		return nil, nil, nil, fmt.Errorf("register HTTP backend provider session: %w", registrationErr)
	}
	scope := newProviderRequestScope(outer, writer, e.providerIdleTimeout)
	return scope.wrapRequest(request.WithContext(ctx)), lease, scope, nil
}

type httpBackend struct {
	target      *url.URL
	backendHost string
	provider    HTTPBackendProvider
	providerKey string
}

func NewServer(listener model.HTTPListener) *Server {
	server, _ := newServer(listener, nil, Providers{}, model.NewCache(model.BackendCacheConfig{}), NewSharedTransport())
	return server
}

func newServer(
	listener model.HTTPListener,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *model.Cache,
	sharedTransport *http.Transport,
) (*Server, error) {
	return newServerWithResilience(listener, relayListeners, providers, backendCache, sharedTransport, StreamResilienceOptions{})
}

func newServerWithResilience(
	listener model.HTTPListener,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *model.Cache,
	sharedTransport *http.Transport,
	resilience StreamResilienceOptions,
) (*Server, error) {
	s := &Server{routes: make(map[string][]*routeEntry), policyEvaluator: providers.PolicyEvaluator}
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, relayListener := range relayListeners {
		relayListenersByID[relayListener.ID] = relayListener
	}
	egressResolver := egressResolverFromProviders(providers)
	egressDialer := moduleegress.Dialer{Resolver: egressResolver}
	directInteractiveTransport, directBulkTransport := NewClassedDirectTransports(sharedTransport)
	for _, rule := range listener.Rules {
		hostKey := HostFromRule(rule)
		if hostKey == "" {
			continue
		}
		targets, err := parseHTTPBackends(rule, providers.HTTPBackendProviders, providers.providerGeneration)
		if err != nil {
			var providerErr *httpBackendProviderResolutionError
			if errors.As(err, &providerErr) {
				return nil, fmt.Errorf("http rule %q: %w", rule.FrontendURL, providerErr)
			}
			continue
		}
		if len(targets) == 0 {
			continue
		}
		transport := sharedTransport
		entryDirectInteractiveTransport := directInteractiveTransport
		entryDirectBulkTransport := directBulkTransport
		var relayTransport *http.Transport
		var relayInteractiveTransport *http.Transport
		var relayBulkTransport *http.Transport
		if ruleUsesRelay(rule) {
			relayTransport, relayInteractiveTransport, relayBulkTransport, err = newRelayTransports(rule, relayListenersByID, providers.Relay, providers.FinalHopDialer, sharedTransport, backendCache)
			if err != nil {
				return nil, err
			}
			transport = relayTransport
			entryDirectInteractiveTransport = nil
			entryDirectBulkTransport = nil
		} else if rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			profile, err := httpRuleEgressProfile(rule, egressDialer)
			if err != nil {
				return nil, err
			}
			if !strings.EqualFold(strings.TrimSpace(profile.Type), "direct") {
				transport, entryDirectInteractiveTransport, entryDirectBulkTransport, err = newEgressTransports(rule, egressDialer, sharedTransport)
				if err != nil {
					return nil, err
				}
				if providers.FinalHopDialer != nil {
					configureEgressTransportWithFinalHop(transport, rule, egressDialer, providers.FinalHopDialer)
					configureEgressTransportWithFinalHop(entryDirectInteractiveTransport, rule, egressDialer, providers.FinalHopDialer)
					configureEgressTransportWithFinalHop(entryDirectBulkTransport, rule, egressDialer, providers.FinalHopDialer)
				}
			}
		}

		frontendBaseURL := FrontendOriginFromRule(rule)
		s.routes[hostKey] = append(s.routes[hostKey], &routeEntry{
			rule:                       rule,
			backends:                   targets,
			backendCache:               backendCache,
			transport:                  transport,
			directInteractiveTransport: entryDirectInteractiveTransport,
			directBulkTransport:        entryDirectBulkTransport,
			relayInteractiveTransport:  relayInteractiveTransport,
			relayBulkTransport:         relayBulkTransport,
			resilience:                 resilience,
			modifyResp:                 makeModifyResponse(frontendBaseURL, rule.ProxyRedirect, targets[0].backendHost, normalizeURLPath(targets[0].target.Path), nil),
			selectionScope:             strings.ToLower(strings.TrimSpace(rule.FrontendURL)),
			frontendPath:               FrontendPathFromRule(rule),
			providerSessions:           providers.providerSessions,
			providerGeneration:         providers.providerGeneration,
			providerTracker:            providers.providerTracker,
			providerIdleTimeout:        providers.providerIdleTimeout,
		})
	}

	return s, nil
}

func egressResolverFromProviders(providers Providers) moduleegress.ProfileResolver {
	if providers.EgressResolver != nil {
		return moduleEgressResolver{resolver: providers.EgressResolver}
	}
	return moduleegress.NewResolver(providers.EgressProfiles)
}

type moduleEgressResolver struct {
	resolver module.EgressResolver
}

func (r moduleEgressResolver) Resolve(id *int, network string) (model.EgressProfile, bool, error) {
	return r.resolver.Resolve(id, network)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	if entry := s.routeFor(host, req.URL.Path); entry != nil {
		if state := s.currentTrafficBlockState(); state.Blocked {
			body := "traffic blocked"
			if state.Reason != "" {
				body = state.Reason
			}
			http.Error(w, body, http.StatusTooManyRequests)
			return
		}
		if decision, allowed := s.allowPolicyRequest(req, entry.rule); !allowed {
			writeHTTPPolicyDecision(w, decision)
			return
		}
		if err := entry.serveHTTP(w, req); err != nil {
			log.Printf("[proxy] bad gateway for %s %s (host=%s frontend=%s): %v", req.Method, req.URL.Path, host, entry.rule.FrontendURL, err)
			var startedErr *startedResponseError
			if errors.As(err, &startedErr) {
				return
			}
			http.Error(w, fmt.Sprintf("bad gateway: %v", err), http.StatusBadGateway)
		}
		return
	}
	http.NotFound(w, req)
}

func (s *Server) currentTrafficBlockState() TrafficBlockState {
	if s == nil {
		return TrafficBlockState{}
	}
	return s.trafficBlockState.Load()
}

func (s *Server) SetTrafficBlockState(state TrafficBlockState) {
	if s == nil {
		return
	}
	s.trafficBlockState.Store(state)
}

func (s *Server) routeFor(host string, requestPath string) *routeEntry {
	entries := s.routes[host]
	if len(entries) == 0 {
		return nil
	}

	normalizedPath := normalizeURLPath(requestPath)
	var best *routeEntry
	bestLen := -1
	for _, entry := range entries {
		if entry == nil || !pathHasPrefix(normalizedPath, entry.frontendPath) {
			continue
		}
		pathLen := len(entry.frontendPath)
		if pathLen > bestLen {
			best = entry
			bestLen = pathLen
		}
	}
	return best
}

func (e *routeEntry) serveHTTP(w http.ResponseWriter, req *http.Request) error {
	recorder := traffic.NewHTTPRuleRecorder(e.rule.ID)
	body, err := prepareReusableBody(req, e.sameBackendRetryMaxAttempts(req), recorder)
	if err != nil {
		log.Printf("[proxy] read body error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	defer body.Close()
	candidates, err := e.candidates(req.Context())
	if err != nil {
		log.Printf("[proxy] candidates error for %s: %v", e.rule.FrontendURL, err)
		return err
	}
	// A retry-safe method whose body was too large to buffer is streamed once
	// and cannot be replayed: once Open() hands the stream to a request the
	// underlying stream is consumed, so a second Open() yields an empty body.
	// When more than one attempt is planned, restrict it to a single attempt so
	// retry and failover never silently change the request. Single-attempt
	// methods keep their existing failover behavior.
	maxSameBackendAttempts := e.sameBackendRetryMaxAttempts(req)
	singleShotBody := maxSameBackendAttempts > 1 && !body.Replayable()
	if singleShotBody {
		maxSameBackendAttempts = 1
	}
	for _, candidate := range candidates {
		// Evaluate backoff before cloneProxyRequest consumes the request body.
		// A non-replayable (one-shot) body can be opened once: if this candidate
		// entered backoff after candidates() built the list, consuming its stream
		// here would force the singleShotBody break below and abandon later
		// healthy candidates. Skip the candidate without opening the body instead.
		actualDialAddress := candidateDialAddress(req, candidate, e.frontendPath)
		backoffAddr := actualDialAddress
		if ruleUsesRelay(e.rule) {
			backoffAddr = model.RelayBackoffKeyForLayers(nil, e.rule.RelayLayers, actualDialAddress)
		}
		if e.backendCache.IsInBackoff(backoffAddr) {
			continue
		}
		for attempt := 0; attempt < maxSameBackendAttempts; attempt++ {
			attemptReq, err := cloneProxyRequest(req, body, candidate, e.rule, e.frontendPath, recorder)
			if err != nil {
				log.Printf("[proxy] clone request error for %s -> %s: %v", e.rule.FrontendURL, candidate.target, err)
				return err
			}
			// Re-check backoff between same-backend retries: a failed attempt
			// marks the address failed and may flip it into backoff, in which
			// case the remaining attempts should bail out rather than redial.
			if e.backendCache.IsInBackoff(backoffAddr) {
				break
			}
			attemptReq, providerLease, providerScope, err := e.acquireProviderRequest(w, attemptReq, candidate)
			if err != nil {
				return err
			}
			if providerScope != nil {
				defer providerScope.Close()
			}
			start := time.Now()
			var resp *http.Response
			if candidate.provider != nil {
				resp, err = candidate.provider.RoundTrip(attemptReq, rpc.HTTPBackendProviderAuthority{
					Scheme: requestScheme(req), Host: req.Host, ClientAddress: req.RemoteAddr,
				})
			} else {
				resp, err = e.transportForRequest(attemptReq).RoundTrip(attemptReq)
			}
			if err != nil {
				if providerScope != nil {
					providerScope.Close()
				}
				if providerLease != nil {
					_ = providerLease.Close()
				}
				log.Printf("[proxy] roundtrip error for %s -> %s: %v", e.rule.FrontendURL, candidate.target, err)
				if !isBackendRetryable(attemptReq, err) {
					return backendRetryError(attemptReq, err)
				}
				if attempt+1 < maxSameBackendAttempts {
					continue
				}
				if candidate.backendObservationKey != "" {
					e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
				}
				e.markCandidateFailure(candidate, attemptReq, backoffAddr)
				break
			}
			if providerScope != nil {
				providerScope.wrapResponse(resp)
			}
			if providerLease != nil {
				rpc.WrapHTTPBackendProviderResponseLease(attemptReq.Context(), resp, providerLease)
			}
			headerLatency := time.Since(start)
			if e.modifyResp != nil && candidate.provider == nil {
				var relativeLocationBase *url.URL
				if _, ok := parseInternalRedirectTarget(req.URL.Path, e.frontendPath); ok {
					relativeLocationBase = attemptReq.URL
				}
				modify := makeModifyResponse(FrontendOriginFromRule(e.rule), e.rule.ProxyRedirect, candidate.backendHost, normalizeURLPath(candidate.target.Path), relativeLocationBase)
				if err := modify(resp); err != nil {
					_ = resp.Body.Close()
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
					log.Printf("[proxy] modify response error for %s: %v", e.rule.FrontendURL, err)
					return err
				}
			}
			if resp.StatusCode == http.StatusSwitchingProtocols {
				responseWriter := w
				if providerScope != nil {
					responseWriter = providerScope.responseWriter(w)
				}
				if err := handleUpgradeResponse(responseWriter, attemptReq, resp, recorder); err != nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
					return err
				}
				e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), 0)
				return nil
			}
			if candidate.provider == nil {
				if state, ok := e.shouldResumeResponse(attemptReq, resp); ok {
					written, err := e.copyResumableResponse(w, attemptReq, resp, state, recorder)
					if err != nil {
						if attemptReq.Context().Err() == nil {
							if candidate.backendObservationKey != "" {
								e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
							}
							e.markCandidateFailure(candidate, attemptReq, backoffAddr)
						}
						return err
					}
					e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), written)
					return nil
				}
			}
			responseWriter := w
			if providerScope != nil {
				responseWriter = providerScope.responseWriter(w)
			}
			written, err := copyResponse(responseWriter, resp, recorder)
			if err != nil {
				if attemptReq.Context().Err() == nil {
					if candidate.backendObservationKey != "" {
						e.backendCache.ObserveBackendFailure(candidate.backendObservationKey)
					}
					e.markCandidateFailure(candidate, attemptReq, backoffAddr)
				}
				return newStartedResponseError(err)
			}
			e.observeSuccessfulBackend(candidate, attemptReq, backoffAddr, headerLatency, time.Since(start), written)
			return nil
		}
		// The one-shot body was consumed by the attempt above, so stop before
		// failover to another candidate reuses it with an empty body.
		if singleShotBody {
			break
		}
	}
	return fmt.Errorf("all backends failed for %s", e.rule.FrontendURL)
}
