package service

import (
	"errors"
	"fmt"
)

var ErrConflict = errors.New("conflict")

type conflictError struct {
	message string
}

func (e conflictError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInvalidArgument, e.message)
}

func (e conflictError) Unwrap() []error {
	return []error{ErrInvalidArgument, ErrConflict}
}

func newConflictError(format string, args ...any) error {
	return conflictError{message: fmt.Sprintf(format, args...)}
}
