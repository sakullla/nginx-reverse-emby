package moduleutil

import (
	"context"
	"reflect"
)

type RollbackRestorer interface {
	RestorePreviousRuntimeForRollback(context.Context) error
}

func RestoreProviderForRollback(ctx context.Context, provider any) error {
	restorer, ok := provider.(RollbackRestorer)
	if !ok || restorer == nil {
		return nil
	}
	return restorer.RestorePreviousRuntimeForRollback(ctx)
}

func SameProvider(left, right any) bool {
	if left == nil || right == nil {
		return false
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() || !leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}
