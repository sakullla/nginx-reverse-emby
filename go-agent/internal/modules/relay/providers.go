package relay

import (
	"context"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func FinalHopDialerFromProvider(provider any) FinalHopDialer {
	if dialer, ok := provider.(FinalHopDialer); ok {
		return dialer
	}
	if dialer, ok := provider.(module.FinalHopDialer); ok {
		return moduleFinalHopDialer{dialer: dialer}
	}
	return nil
}

func finalHopDialerFromProvider(provider any) FinalHopDialer {
	return FinalHopDialerFromProvider(provider)
}

type rollbackFinalHopProvider interface {
	PreviousFinalHopDialerForRollback() any
}

func finalHopProviderForRollback(provider any) any {
	rollbackProvider, ok := provider.(rollbackFinalHopProvider)
	if !ok || rollbackProvider == nil {
		return provider
	}
	previous := rollbackProvider.PreviousFinalHopDialerForRollback()
	if previous == nil {
		return provider
	}
	return previous
}

type moduleFinalHopDialer struct {
	dialer module.FinalHopDialer
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, profileID)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}
