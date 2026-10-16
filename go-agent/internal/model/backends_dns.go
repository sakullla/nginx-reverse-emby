package model

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// dnsLookup publishes an immutable result when done closes. In-flight work is
// local to one cache generation and is deliberately not copied by Clone.
type dnsLookup struct {
	done chan struct{}
	ips  []string
	err  error
}

func (c *Cache) lookupHost(ctx context.Context, host string) ([]string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := c.now()
		c.mu.Lock()
		if entry, ok := c.dnsCache[host]; ok && now.Before(entry.expiresAt) {
			c.mu.Unlock()
			// Cache slices are immutable; Resolve only reads them while building
			// independently owned candidates.
			return entry.ips, nil
		}
		if pending := c.dnsLookups[host]; pending != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending.done:
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// The initiating caller owns its lookup deadline. Its cancellation
			// must not fail unrelated callers that still have time to resolve.
			if errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded) {
				continue
			}
			return pending.ips, pending.err
		}
		pending := &dnsLookup{done: make(chan struct{})}
		if c.dnsLookups == nil {
			c.dnsLookups = make(map[string]*dnsLookup)
		}
		c.dnsLookups[host] = pending
		c.mu.Unlock()

		ips, err := c.resolveHost(ctx, host)
		// Start TTL at completion, not before a potentially slow DNS lookup.
		now = c.now()
		c.mu.Lock()
		if err == nil {
			c.dnsCache[host] = dnsCacheEntry{
				ips: ips, expiresAt: now.Add(dnsCacheTTL),
				staleUntil: now.Add(dnsCacheTTL + dnsCacheStaleIfErrorTTL),
			}
		} else if ctx.Err() == nil {
			if entry, ok := c.dnsCache[host]; ok && len(entry.ips) > 0 && now.Before(entry.dnsRetainUntil()) {
				ips, err = entry.ips, nil
			}
		}
		pending.ips, pending.err = ips, err
		delete(c.dnsLookups, host)
		close(pending.done)
		c.mu.Unlock()
		return ips, err
	}
}

func (c *Cache) resolveHost(ctx context.Context, host string) ([]string, error) {
	resolved, err := c.resolver.LookupIPAddr(ctx, host)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no IPs resolved for host %q", host)
	}
	dedup := make(map[string]struct{}, len(resolved))
	ips := make([]string, 0, len(resolved))
	for _, candidate := range resolved {
		if candidate.IP == nil {
			continue
		}
		ip := candidate.IP.String()
		if _, exists := dedup[ip]; exists {
			continue
		}
		dedup[ip] = struct{}{}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no valid IPs resolved for host %q", host)
	}
	return ips, nil
}

func (e dnsCacheEntry) dnsRetainUntil() time.Time {
	if e.staleUntil.After(e.expiresAt) {
		return e.staleUntil
	}
	return e.expiresAt
}
