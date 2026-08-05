package service

import (
	"fmt"
	"os"
)

type pkiPublishConflictError struct {
	destination string
}

func (err *pkiPublishConflictError) Error() string {
	return fmt.Sprintf("publish restricted PKI file %s: destination exists", err.destination)
}

func (err *pkiPublishConflictError) Is(target error) bool {
	return target == os.ErrExist
}

func newPKIPublishConflict(destination string) error {
	return &pkiPublishConflictError{destination: destination}
}

func isPurePKIPublishConflict(err error) bool {
	_, ok := err.(*pkiPublishConflictError)
	return ok
}
