package revision

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorCodeInvalidRequest ErrorCode = "invalid_request"
	ErrorCodeNotFound       ErrorCode = "not_found"
	ErrorCodeConflict       ErrorCode = "conflict"
	ErrorCodeUnprocessable  ErrorCode = "unprocessable"
	ErrorCodeInternal       ErrorCode = "internal"
)

type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewError(code ErrorCode, message string, cause error) error {
	if code == "" {
		code = ErrorCodeInternal
	}
	if message == "" && cause == nil {
		message = string(code)
	}
	return &Error{Code: code, Message: message, Cause: cause}
}

func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var mutationErr *Error
	if errors.As(err, &mutationErr) {
		return mutationErr.Code
	}
	return ErrorCodeInternal
}

func HTTPStatus(err error) int {
	switch ErrorCodeOf(err) {
	case ErrorCodeInvalidRequest:
		return 400
	case ErrorCodeNotFound:
		return 404
	case ErrorCodeConflict:
		return 409
	case ErrorCodeUnprocessable:
		return 422
	case "":
		return 0
	default:
		return 500
	}
}

func wrapError(code ErrorCode, format string, args ...any) error {
	return NewError(code, fmt.Sprintf(format, args...), nil)
}
