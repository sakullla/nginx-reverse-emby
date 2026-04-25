package relayplan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

var ErrNoRelayPathSucceeded = errors.New("no relay path succeeded")

type Path struct {
	IDs  []int
	Hops []relay.Hop
	Key  string
}

type Request struct {
	Network string
	Target  string
	Paths   []Path
	Options []relay.DialOptions
}

type Result struct {
	Conn       net.Conn
	Selected   Path
	DialResult relay.DialResult
	Attempts   []Attempt
}

type Attempt struct {
	Path     Path
	Success  bool
	Latency  time.Duration
	Error    string
	Canceled bool
}

type Dialer interface {
	DialPath(ctx context.Context, req Request, path Path) (net.Conn, relay.DialResult, error)
}

type Racer struct {
	Dialer      Dialer
	Cache       *backends.Cache
	Concurrency int
	MaxPaths    int
}

type raceAttemptResult struct {
	attempt    Attempt
	conn       net.Conn
	dialResult relay.DialResult
}

func (r Racer) Race(ctx context.Context, req Request) (Result, error) {
	if r.Dialer == nil {
		return Result{}, fmt.Errorf("relay path dialer is required")
	}
	paths := r.orderPaths(req)
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("relay paths are required")
	}
	if r.MaxPaths > 0 && len(paths) > r.MaxPaths {
		return Result{}, fmt.Errorf("relay paths exceed maximum %d", r.MaxPaths)
	}
	concurrency := r.Concurrency
	if concurrency <= 0 || concurrency > len(paths) {
		concurrency = len(paths)
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan raceAttemptResult, len(paths))
	var wg sync.WaitGroup
	nextPath := 0
	running := 0
	attempts := make([]Attempt, 0, len(paths))
	failures := make([]string, 0, len(paths))

	start := func(path Path) {
		running++
		wg.Add(1)
		go func() {
			defer wg.Done()
			startedAt := time.Now()
			conn, dialResult, err := r.Dialer.DialPath(raceCtx, req, path)
			attempt := Attempt{Path: path, Latency: time.Since(startedAt)}
			if err != nil {
				attempt.Error = err.Error()
				attempt.Canceled = errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && raceCtx.Err() != nil
				results <- raceAttemptResult{attempt: attempt}
				return
			}
			attempt.Success = true
			results <- raceAttemptResult{attempt: attempt, conn: conn, dialResult: dialResult}
		}()
	}

	for running < concurrency && nextPath < len(paths) {
		start(paths[nextPath])
		nextPath++
	}

	for running > 0 {
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return Result{}, ctx.Err()
		case result := <-results:
			running--
			attempts = append(attempts, result.attempt)
			r.observeAttempt(req, result.attempt)
			if result.attempt.Success {
				cancel()
				go func() {
					wg.Wait()
					close(results)
					for loser := range results {
						if loser.conn != nil {
							_ = loser.conn.Close()
						}
						r.observeAttempt(req, loser.attempt)
					}
				}()
				return Result{Conn: result.conn, Selected: result.attempt.Path, DialResult: result.dialResult, Attempts: attempts}, nil
			}
			if result.attempt.Error != "" && !result.attempt.Canceled {
				failures = append(failures, result.attempt.Error)
			}
			if nextPath < len(paths) {
				start(paths[nextPath])
				nextPath++
			}
		}
	}

	if len(failures) == 0 {
		return Result{Attempts: attempts}, ErrNoRelayPathSucceeded
	}
	return Result{Attempts: attempts}, fmt.Errorf("%w: %s", ErrNoRelayPathSucceeded, strings.Join(failures, "; "))
}

func (r Racer) observeAttempt(req Request, attempt Attempt) {
	if r.Cache == nil || attempt.Canceled {
		return
	}
	key := attempt.Path.Key
	if strings.TrimSpace(key) == "" {
		key = PathKey("relay_path", attempt.Path.IDs, req.Target)
	}
	observationKey := backends.BackendObservationKey(relayPathScope(req.Target), key)
	if attempt.Success {
		r.Cache.ObserveBackendSuccess(observationKey, attempt.Latency, attempt.Latency, 0)
		return
	}
	if attempt.Error != "" {
		r.Cache.ObserveBackendFailure(observationKey)
	}
}

func (r Racer) orderPaths(req Request) []Path {
	paths := append([]Path(nil), req.Paths...)
	if r.Cache == nil || len(paths) <= 1 {
		return paths
	}
	candidates := make([]backends.Candidate, 0, len(paths))
	pathsByKey := make(map[string]Path, len(paths))
	for _, path := range paths {
		key := path.Key
		if strings.TrimSpace(key) == "" {
			key = PathKey("relay_path", path.IDs, req.Target)
		}
		pathsByKey[key] = path
		candidates = append(candidates, backends.Candidate{Address: key})
	}
	scope := relayPathScope(req.Target)
	ordered := r.Cache.Order(scope, backends.StrategyAdaptive, candidates)
	out := make([]Path, 0, len(ordered))
	for _, candidate := range ordered {
		path, ok := pathsByKey[candidate.Address]
		if !ok {
			continue
		}
		out = append(out, path)
	}
	if len(out) != len(paths) {
		return paths
	}
	return out
}

func relayPathScope(target string) string {
	return "relay_path|" + strings.TrimSpace(target)
}
