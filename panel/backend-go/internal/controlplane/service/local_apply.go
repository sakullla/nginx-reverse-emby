package service

import "context"

func wrapLocalApplyTrigger(trigger func(context.Context) error) func(context.Context) error {
	if trigger == nil {
		return nil
	}
	return func(ctx context.Context) error {
		if ctx == nil {
			ctx = context.Background()
		}
		return trigger(context.WithoutCancel(ctx))
	}
}
