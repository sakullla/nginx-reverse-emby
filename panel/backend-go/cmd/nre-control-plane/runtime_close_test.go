package main

import (
	"context"
	"errors"
	"testing"
)

type scriptedRuntimeCloser struct {
	failures int
	calls    int
}

func (c *scriptedRuntimeCloser) Close(context.Context) error {
	c.calls++
	if c.calls <= c.failures {
		return errors.New("runtime close failed")
	}
	return nil
}

func TestCloseRuntimeWithRetry(t *testing.T) {
	runtime := &scriptedRuntimeCloser{failures: 2}
	if err := closeRuntimeWithRetry(runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 3 {
		t.Fatalf("close calls = %d, want 3", runtime.calls)
	}
	runtime = &scriptedRuntimeCloser{failures: 3}
	if err := closeRuntimeWithRetry(runtime); err == nil || runtime.calls != 3 {
		t.Fatalf("exhausted retry result: calls=%d err=%v", runtime.calls, err)
	}
}
