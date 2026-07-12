package revision

import (
	"context"
	"strings"
	"sync"
)

type MutationContextOptions struct {
	OperationID      string
	IdempotencyScope string
	IdempotencyKey   string
}

type MutationCapture struct {
	mu      sync.Mutex
	results []MutationResult
}

type mutationContext struct {
	options MutationContextOptions
	capture *MutationCapture
}

type mutationContextKey struct{}

func WithMutationContext(ctx context.Context, options MutationContextOptions) (context.Context, *MutationCapture) {
	if ctx == nil {
		ctx = context.Background()
	}
	capture := &MutationCapture{}
	value := mutationContext{
		options: MutationContextOptions{
			OperationID:      strings.TrimSpace(options.OperationID),
			IdempotencyScope: strings.TrimSpace(options.IdempotencyScope),
			IdempotencyKey:   strings.TrimSpace(options.IdempotencyKey),
		},
		capture: capture,
	}
	return context.WithValue(ctx, mutationContextKey{}, value), capture
}

func MutationCaptureFromContext(ctx context.Context) (*MutationCapture, bool) {
	value, ok := mutationContextFromContext(ctx)
	return value.capture, ok && value.capture != nil
}

func (c *MutationCapture) Result() (MutationResult, bool) {
	if c == nil {
		return MutationResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.results) == 0 {
		return MutationResult{}, false
	}
	return c.results[len(c.results)-1], true
}

func applyMutationContext(ctx context.Context, request MutationRequest) MutationRequest {
	value, ok := mutationContextFromContext(ctx)
	if !ok {
		return request
	}
	if strings.TrimSpace(request.OperationID) == "" {
		request.OperationID = value.options.OperationID
	}
	if strings.TrimSpace(request.IdempotencyScope) == "" {
		request.IdempotencyScope = value.options.IdempotencyScope
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		request.IdempotencyKey = value.options.IdempotencyKey
	}
	return request
}

func publishMutationResult(ctx context.Context, result MutationResult) MutationResult {
	value, ok := mutationContextFromContext(ctx)
	if !ok || value.capture == nil {
		return result
	}
	value.capture.mu.Lock()
	value.capture.results = append(value.capture.results, result)
	value.capture.mu.Unlock()
	return result
}

func mutationContextFromContext(ctx context.Context) (mutationContext, bool) {
	if ctx == nil {
		return mutationContext{}, false
	}
	value, ok := ctx.Value(mutationContextKey{}).(mutationContext)
	return value, ok
}
