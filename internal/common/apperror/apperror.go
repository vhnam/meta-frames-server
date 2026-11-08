// Package apperror defines the errors that services return and controllers translate to HTTP.
package apperror

import "errors"

type Kind int

const (
	KindNotFound Kind = iota + 1
	KindConflict
	KindUnprocessable
	KindBadRequest
	KindPreconditionFailed
)

// Error is a business-level failure with a stable machine-readable code.
type Error struct {
	Kind    Kind
	Code    string
	Message string
}

func (appError *Error) Error() string { return appError.Code + ": " + appError.Message }

func NotFound(what string) error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: what + " not found"}
}

// Conflict reports that the request clashes with the current state of a record.
func Conflict(code, message string) error {
	return &Error{Kind: KindConflict, Code: code, Message: message}
}

// Unprocessable reports input that is well-formed but breaks a business rule.
func Unprocessable(code, message string) error {
	return &Error{Kind: KindUnprocessable, Code: code, Message: message}
}

// BadRequest reports a request the schema allows but the server cannot read, such as a
// malformed precondition header.
func BadRequest(code, message string) error {
	return &Error{Kind: KindBadRequest, Code: code, Message: message}
}

// PreconditionFailed reports that a precondition header (If-Match, If-None-Match) did not hold,
// so nothing was written.
func PreconditionFailed(code, message string) error {
	return &Error{Kind: KindPreconditionFailed, Code: code, Message: message}
}

// CreateOnly adapts the error of a create made for a create-only PUT (If-None-Match: *): the
// "id already taken" conflict becomes 412 already_exists, so nothing was changed. A malformed
// header value is refused rather than ignored.
func CreateOnly(err error) error {
	if appError, ok := From(err); ok && appError.Kind == KindConflict && appError.Code == "already_exists" {
		return PreconditionFailed(appError.Code, appError.Message)
	}
	return err
}

// InvalidCreateOnly is the error for an If-None-Match value other than "*".
func InvalidCreateOnly() error {
	return BadRequest("invalid_precondition", `If-None-Match must be "*"`)
}

// From extracts an *Error from err, if there is one.
func From(err error) (*Error, bool) {
	var appError *Error
	if errors.As(err, &appError) {
		return appError, true
	}
	return nil, false
}

// IsNotFound reports whether err is a "record not found" business error.
func IsNotFound(err error) bool {
	appError, ok := From(err)
	return ok && appError.Kind == KindNotFound
}
