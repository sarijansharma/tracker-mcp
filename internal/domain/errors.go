package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind classifies a failure so every transport can react to it consistently.
type ErrorKind string

const (
	ErrorKindNotFound      ErrorKind = "not_found"
	ErrorKindAlreadyExists ErrorKind = "already_exists"
	ErrorKindInvalidInput  ErrorKind = "invalid_input"
	ErrorKindConflict      ErrorKind = "conflict"
)

// Error is a failure meant to be read by a non-technical person (usually relayed by an AI
// assistant). Message explains what went wrong; Suggestions holds "did you mean" candidates.
type Error struct {
	Kind        ErrorKind
	Message     string
	Suggestions []string
}

func (e *Error) Error() string {
	if len(e.Suggestions) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s Did you mean: %s?", e.Message, strings.Join(e.Suggestions, ", "))
}

// WithSuggestions returns the error with "did you mean" candidates attached.
func (e *Error) WithSuggestions(suggestions ...string) *Error {
	e.Suggestions = append(e.Suggestions, suggestions...)
	return e
}

func NotFoundf(format string, args ...any) *Error {
	return &Error{Kind: ErrorKindNotFound, Message: fmt.Sprintf(format, args...)}
}

func AlreadyExistsf(format string, args ...any) *Error {
	return &Error{Kind: ErrorKindAlreadyExists, Message: fmt.Sprintf(format, args...)}
}

func InvalidInputf(format string, args ...any) *Error {
	return &Error{Kind: ErrorKindInvalidInput, Message: fmt.Sprintf(format, args...)}
}

func Conflictf(format string, args ...any) *Error {
	return &Error{Kind: ErrorKindConflict, Message: fmt.Sprintf(format, args...)}
}

// IsKind reports whether err is a domain Error of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var domainErr *Error
	return errors.As(err, &domainErr) && domainErr.Kind == kind
}

// ValidationErrors collects several problems (e.g. one per row) so the caller can fix
// everything in one go instead of discovering issues one at a time.
type ValidationErrors struct {
	problems []string
}

const maxReportedProblems = 25

func (v *ValidationErrors) Addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *ValidationErrors) HasProblems() bool {
	return len(v.problems) > 0
}

// AsError returns nil when there are no problems, otherwise one invalid-input Error.
func (v *ValidationErrors) AsError(summary string) error {
	if !v.HasProblems() {
		return nil
	}
	shown := v.problems
	suffix := ""
	if len(shown) > maxReportedProblems {
		suffix = fmt.Sprintf("\n- ...and %d more", len(shown)-maxReportedProblems)
		shown = shown[:maxReportedProblems]
	}
	return InvalidInputf("%s\n- %s%s", summary, strings.Join(shown, "\n- "), suffix)
}
