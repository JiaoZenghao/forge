// Package apperror defines the stable machine-readable error contract.
package apperror

import "errors"

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Dependency string `json:"dependency,omitempty"`
	Path       string `json:"path,omitempty"`
	Command    string `json:"command,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
	Cause      error  `json:"-"`
}

func (e *Error) Error() string        { return e.Message }
func (e *Error) Unwrap() error        { return e.Cause }
func New(code, message string) *Error { return &Error{Code: code, Message: message} }
func Exit(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) && e.Code == "INVALID_ARGUMENT" {
		return 2
	}
	return 1
}
func Normalize(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: "GENERAL_ERROR", Message: err.Error(), Cause: err}
}
