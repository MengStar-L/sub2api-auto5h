package sub2api

import "fmt"

type ErrorKind string

const (
	ErrorTransient  ErrorKind = "transient"
	ErrorAuth       ErrorKind = "auth"
	ErrorCompliance ErrorKind = "compliance"
	ErrorNotFound   ErrorKind = "not_found"
	ErrorSchema     ErrorKind = "schema"
	ErrorRejected   ErrorKind = "rejected"
)

type APIError struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	Message    string
	Cause      error
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("sub2api %s (%s): %s", e.Kind, e.Code, e.Message)
	}
	return fmt.Sprintf("sub2api %s: %s", e.Kind, e.Message)
}

func (e *APIError) Unwrap() error { return e.Cause }

func IsTransient(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Kind == ErrorTransient
}

func classifyStatus(status int, code, message string) *APIError {
	kind := ErrorRejected
	switch {
	case status == 401 || status == 403:
		kind = ErrorAuth
	case status == 404:
		kind = ErrorNotFound
	case status == 423:
		kind = ErrorCompliance
	case status == 408 || status == 429 || status >= 500:
		kind = ErrorTransient
	}
	return &APIError{Kind: kind, StatusCode: status, Code: code, Message: message}
}
