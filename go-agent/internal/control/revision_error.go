package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const revisionLeaseConflictCode = "revision_lease_conflict"

// ErrRevisionLeaseConflict identifies a coordinator response that makes the
// current revision lease permanently unusable. Retrying the same request
// cannot succeed; the caller must poll for the current desired revision.
var ErrRevisionLeaseConflict = errors.New("revision lease conflict")

type revisionRequestError struct {
	path       string
	status     string
	statusCode int
	body       string
	code       string
}

func newRevisionRequestError(path, status string, statusCode int, body []byte) error {
	trimmedBody := strings.TrimSpace(string(body))
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &payload)
	return &revisionRequestError{
		path:       path,
		status:     status,
		statusCode: statusCode,
		body:       trimmedBody,
		code:       strings.TrimSpace(payload.Code),
	}
}

func (e *revisionRequestError) Error() string {
	return fmt.Sprintf("revision request %s failed: %s: %s", e.path, e.status, e.body)
}

func (e *revisionRequestError) Unwrap() error {
	if e.statusCode == 409 && e.code == revisionLeaseConflictCode {
		return ErrRevisionLeaseConflict
	}
	return nil
}
