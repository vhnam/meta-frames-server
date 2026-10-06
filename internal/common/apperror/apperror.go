// Package apperror defines the errors that services return and controllers translate to HTTP.
package apperror

import "errors"

type Kind int

const (
	KindNotFound Kind = iota + 1
	KindConflict
	KindUnprocessable
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
