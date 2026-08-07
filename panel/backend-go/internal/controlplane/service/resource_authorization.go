package service

import "context"

type resourceAuthorizerContextKey struct{}

type ResourceAuthorizer func(context.Context, string, string) error

func WithResourceAuthorizer(ctx context.Context, authorizer ResourceAuthorizer) context.Context {
	if authorizer == nil {
		return ctx
	}
	return context.WithValue(ctx, resourceAuthorizerContextKey{}, authorizer)
}

func authorizeReferencedResource(ctx context.Context, kind, id string) error {
	authorizer, ok := ctx.Value(resourceAuthorizerContextKey{}).(ResourceAuthorizer)
	if !ok || authorizer == nil {
		return nil
	}
	return authorizer(ctx, kind, id)
}
