package partner

import (
	"encoding/json"
	"fmt"
)

// ErrorCode is the stable machine-readable error code; branch on it only,
// never on the message text.
type ErrorCode string

const (
	CodeValidationError                 ErrorCode = "validation_error"
	CodeUnauthorized                    ErrorCode = "unauthorized"
	CodeCapabilityDisabled              ErrorCode = "capability_disabled"
	CodeOperationTemporarilyUnavailable ErrorCode = "operation_temporarily_unavailable"
	CodeConnectionNotRecoverable        ErrorCode = "connection_not_recoverable"
	CodeConsentInactive                 ErrorCode = "consent_inactive"
	CodeNotFound                        ErrorCode = "not_found"
	CodeRefreshInProgress               ErrorCode = "refresh_in_progress"
	CodeRefreshCooldown                 ErrorCode = "refresh_cooldown"
	CodeRefreshQuotaExceeded            ErrorCode = "refresh_quota_exceeded"
	CodeBackgroundRefreshNotAllowed     ErrorCode = "background_refresh_not_allowed"
	CodeRateLimited                     ErrorCode = "rate_limited"
	CodeInternalError                   ErrorCode = "internal_error"
)

// APIError is a non-2xx answer from the Partner API. An unknown or missing
// code falls back to one derived from the HTTP status.
type APIError struct {
	Code       ErrorCode
	HTTPStatus int
	// RequestID is the X-Request-Id header, else the body requestId; empty when neither is present.
	RequestID string
	Message   string
	// NextRefreshPossibleAt is set on refresh_cooldown and refresh_quota_exceeded.
	NextRefreshPossibleAt string
}

func (e *APIError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("partner: %s (HTTP %d, request %s): %s", e.Code, e.HTTPStatus, e.RequestID, e.Message)
	}
	return fmt.Sprintf("partner: %s (HTTP %d): %s", e.Code, e.HTTPStatus, e.Message)
}

// UnreachableError is a network-level failure: the endpoint was not reachable at all.
type UnreachableError struct {
	Err error
}

func (e *UnreachableError) Error() string {
	return "partner: Partner API endpoint is not reachable: " + e.Err.Error()
}

func (e *UnreachableError) Unwrap() error { return e.Err }

var knownCodes = map[ErrorCode]bool{
	CodeValidationError: true, CodeUnauthorized: true, CodeCapabilityDisabled: true,
	CodeOperationTemporarilyUnavailable: true, CodeConnectionNotRecoverable: true,
	CodeConsentInactive: true, CodeNotFound: true, CodeRefreshInProgress: true,
	CodeRefreshCooldown: true, CodeRefreshQuotaExceeded: true,
	CodeBackgroundRefreshNotAllowed: true, CodeRateLimited: true, CodeInternalError: true,
}

// statusFallback covers gateway-shaped errors that lack the envelope (401/429 from Kong).
func statusFallback(status int) ErrorCode {
	switch status {
	case 401:
		return CodeUnauthorized
	case 404:
		return CodeNotFound
	case 429:
		return CodeRateLimited
	}
	return CodeInternalError
}

func parseErrorEnvelope(status int, body []byte, headerRequestID string) *APIError {
	e := &APIError{
		Code:       statusFallback(status),
		HTTPStatus: status,
		RequestID:  headerRequestID,
		Message:    fmt.Sprintf("HTTP %d", status),
	}
	var env map[string]any
	if json.Unmarshal(body, &env) != nil {
		return e
	}
	if detail, ok := env["error"].(map[string]any); ok {
		if code, ok := detail["code"].(string); ok && knownCodes[ErrorCode(code)] {
			e.Code = ErrorCode(code)
		}
		if msg, ok := detail["message"].(string); ok {
			e.Message = msg
		}
		if next, ok := detail["nextRefreshPossibleAt"].(string); ok {
			e.NextRefreshPossibleAt = next
		}
	}
	if id, ok := env["requestId"].(string); ok && e.RequestID == "" {
		e.RequestID = id
	}
	return e
}
