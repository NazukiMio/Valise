// Package valise holds the error type shared by every Valise component.
package valise

import (
	"encoding/json"
	"errors"
)

// UserError is the single error shape shown to users: a reason, a suggested
// next step, and technical detail that UIs fold by default. Detail must never
// contain file contents or credentials.
type UserError struct {
	Code       string `json:"code"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
	Detail     string `json:"detail,omitempty"`
}

// New returns a UserError without detail.
func New(code, reason, suggestion string) *UserError {
	return &UserError{Code: code, Reason: reason, Suggestion: suggestion}
}

// Error returns the JSON encoding, so the structure survives transports that
// only carry error text.
func (e *UserError) Error() string {
	b, err := json.Marshal(e)
	if err != nil {
		return e.Reason
	}
	return string(b)
}

// WithDetail returns a copy of e with Detail set.
func (e *UserError) WithDetail(detail string) *UserError {
	c := *e
	c.Detail = detail
	return &c
}

// As reports whether err is or wraps a UserError.
func As(err error) (*UserError, bool) {
	var ue *UserError
	if errors.As(err, &ue) {
		return ue, true
	}
	return nil, false
}

// Parse decodes the text produced by Error. It returns false if text is not a
// UserError.
func Parse(text string) (*UserError, bool) {
	var ue UserError
	if err := json.Unmarshal([]byte(text), &ue); err != nil || ue.Code == "" || ue.Reason == "" {
		return nil, false
	}
	return &ue, true
}
