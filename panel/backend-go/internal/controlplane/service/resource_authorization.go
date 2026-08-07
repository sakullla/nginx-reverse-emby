package service

import (
	"context"
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrMutationPrincipalRequired = errors.New("mutation principal required")

type resourceAuthorizerContextKey struct{}

type ResourceAuthorizer func(context.Context, string, string) error

func WithResourceAuthorizer(ctx context.Context, authorizer ResourceAuthorizer) context.Context {
	if authorizer == nil {
		return ctx
	}
	return context.WithValue(ctx, resourceAuthorizerContextKey{}, authorizer)
}

func authorizeReferencedResource(ctx context.Context, store any, kind, id string) error {
	authorizer, ok := ctx.Value(resourceAuthorizerContextKey{}).(ResourceAuthorizer)
	if !ok || authorizer == nil {
		if _, governed := store.(resourceQuotaStore); governed {
			return ErrMutationPrincipalRequired
		}
		return nil
	}
	return authorizer(ctx, kind, id)
}

func hasResourceAuthorizer(ctx context.Context) bool {
	authorizer, ok := ctx.Value(resourceAuthorizerContextKey{}).(ResourceAuthorizer)
	return ok && authorizer != nil
}

// WithSystemMutationPrincipal marks a non-interactive control-plane mutation
// explicitly. System principals may resolve dependency closure, while storage
// still evaluates resource-group quotas and emits actor-attributed audit rows.
func WithSystemMutationPrincipal(ctx context.Context, principalID string) context.Context {
	principalID = strings.TrimSpace(principalID)
	if principalID == "" {
		principalID = "system"
	}
	ctx = storage.WithQuotaActor(ctx, storage.QuotaActor{UserID: principalID, Bootstrap: true})
	return WithResourceAuthorizer(ctx, func(context.Context, string, string) error { return nil })
}
