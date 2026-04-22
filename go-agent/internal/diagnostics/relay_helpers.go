package diagnostics

import (
	"context"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

var diagnosticRelayDialWithResult = func(
	ctx context.Context,
	network string,
	target string,
	chain []relay.Hop,
	provider relay.TLSMaterialProvider,
	opts ...relay.DialOptions,
) (net.Conn, relay.DialResult, error) {
	return relay.DialWithResult(ctx, network, target, chain, provider, opts...)
}

var diagnosticRelayResolveCandidates = func(
	ctx context.Context,
	target string,
	chain []relay.Hop,
	provider relay.TLSMaterialProvider,
) ([]string, error) {
	return relay.ResolveCandidates(ctx, target, chain, provider)
}
