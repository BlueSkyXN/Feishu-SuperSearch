package kernel

import (
	"errors"
	"fmt"
)

type ErrorType string

const (
	ErrInvalidRequest      ErrorType = "invalid_request"
	ErrUnsupported         ErrorType = "unsupported"
	ErrMissingScope        ErrorType = "missing_scope"
	ErrIdentityRequired    ErrorType = "identity_required"
	ErrNotFound            ErrorType = "not_found"
	ErrRateLimited         ErrorType = "rate_limited"
	ErrUpstreamTransient   ErrorType = "upstream_transient"
	ErrUpstreamPermanent   ErrorType = "upstream_permanent"
	ErrParse               ErrorType = "parse_error"
	ErrVersionIncompatible ErrorType = "version_incompatible"
	ErrDeadlineExceeded    ErrorType = "deadline_exceeded"
	ErrBudgetExhausted     ErrorType = "budget_exhausted"
	ErrCancelled           ErrorType = "cancelled"
	ErrInternal            ErrorType = "internal"
)

type ErrorDetail struct {
	Type       ErrorType      `json:"type"`
	Subtype    string         `json:"subtype,omitempty"`
	Code       int            `json:"code,omitempty"`
	Message    string         `json:"message"`
	Hint       string         `json:"hint,omitempty"`
	Retryable  bool           `json:"retryable,omitempty"`
	ProviderID ProviderID     `json:"provider_id,omitempty"`
	Source     SourceID       `json:"source,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e ErrorDetail) Error() string {
	if e.ProviderID != "" {
		return fmt.Sprintf("%s/%s: %s", e.ProviderID, e.Type, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

func DetailFromError(err error) *ErrorDetail {
	if err == nil {
		return nil
	}
	var d *ErrorDetail
	if errors.As(err, &d) {
		return d
	}
	var dv ErrorDetail
	if errors.As(err, &dv) {
		return &dv
	}
	return &ErrorDetail{Type: ErrInternal, Message: err.Error()}
}

func NewError(t ErrorType, message string) *ErrorDetail {
	return &ErrorDetail{Type: t, Message: message}
}
