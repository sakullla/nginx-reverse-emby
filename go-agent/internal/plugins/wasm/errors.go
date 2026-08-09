package wasm

import (
	"errors"
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type ErrorCode string

const (
	ErrorInvalidArtifact    ErrorCode = "invalid_artifact"
	ErrorIncompatibleABI    ErrorCode = "incompatible_abi"
	ErrorInputBudget        ErrorCode = "input_budget_exceeded"
	ErrorOutputBudget       ErrorCode = "output_budget_exceeded"
	ErrorMemoryBudget       ErrorCode = "memory_budget_exceeded"
	ErrorConcurrencyBudget  ErrorCode = "concurrency_budget_exceeded"
	ErrorDeadline           ErrorCode = "deadline_exceeded"
	ErrorGenerationDraining ErrorCode = "generation_draining"
	ErrorGuest              ErrorCode = "guest_failure"
	ErrorHost               ErrorCode = "host_failure"
	ErrorOptionalDegraded   ErrorCode = "optional_stage_degraded"
)

// RuntimeError is safe to expose through Agent observability. Cause is kept
// for errors.Is/errors.As, while Error deliberately avoids guest stack dumps.
type RuntimeError struct {
	Code       ErrorCode
	Generation string
	Operation  string
	Cause      error
}

func (err *RuntimeError) Error() string {
	if err == nil {
		return ""
	}
	if err.Generation == "" {
		return fmt.Sprintf("wasm %s: %s", err.Operation, err.Code)
	}
	return fmt.Sprintf("wasm generation %q %s: %s", err.Generation, err.Operation, err.Code)
}

func (err *RuntimeError) Unwrap() error { return err.Cause }

func IsCode(err error, code ErrorCode) bool {
	var runtimeError *RuntimeError
	return errors.As(err, &runtimeError) && runtimeError.Code == code
}

type Event struct {
	Generation string
	Operation  string
	Code       ErrorCode
	Dimension  pluginsdk.BudgetDimension
}

type Observer interface {
	ObserveWASM(Event)
}

type ObserverFunc func(Event)

func (observer ObserverFunc) ObserveWASM(event Event) { observer(event) }

type discardObserver struct{}

func (discardObserver) ObserveWASM(Event) {}
