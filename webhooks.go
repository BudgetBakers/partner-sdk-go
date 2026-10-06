package partner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Webhook signature (spec/webhooks-v2.yaml): X-BB-Signature: t=<unix-ts>,v1=<hex
// HMAC_SHA256(secret, "<t>." + raw body)>. Constant-time comparison against every
// active secret (two during rotation), +-300 s timestamp window, every v1 entry
// collected, unknown scheme keys ignored.

const (
	SignatureHeader = "X-BB-Signature"
	Tolerance       = 300 * time.Second
)

type VerifyResult string

const (
	VerifyValid                   VerifyResult = "valid"
	VerifyInvalidSignature        VerifyResult = "invalid_signature"
	VerifyTimestampOutOfTolerance VerifyResult = "timestamp_out_of_tolerance"
	VerifyMalformedHeader         VerifyResult = "malformed_header"
)

// Verify checks a delivery against all currently active secrets at the current time.
func Verify(secrets []string, header string, body []byte) VerifyResult {
	return VerifyAt(secrets, header, body, time.Now())
}

// VerifyAt is Verify with an explicit clock.
func VerifyAt(secrets []string, header string, body []byte, now time.Time) VerifyResult {
	ts, sigs, ok := parseSignatureHeader(header)
	if !ok {
		return VerifyMalformedHeader
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || t > maxSafeInteger {
		return VerifyMalformedHeader
	}
	skew := now.Unix() - t
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(Tolerance/time.Second) {
		return VerifyTimestampOutOfTolerance
	}
	for _, secret := range secrets {
		expected := digest(secret, ts, body)
		for _, sig := range sigs {
			if hmac.Equal(sig, expected) {
				return VerifyValid
			}
		}
	}
	return VerifyInvalidSignature
}

// Sign returns the full signature header value for body at unix time ts (tests and tooling).
func Sign(secret string, ts int64, body []byte) string {
	t := strconv.FormatInt(ts, 10)
	return "t=" + t + ",v1=" + hex.EncodeToString(digest(secret, t, body))
}

// maxSafeInteger keeps timestamp parsing in step with the other SDKs (2^53 - 1).
const maxSafeInteger = 1<<53 - 1

func digest(secret, ts string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return mac.Sum(nil)
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func parseSignatureHeader(header string) (ts string, sigs [][]byte, ok bool) {
	if header == "" {
		return "", nil, false
	}
	for _, element := range strings.Split(header, ",") {
		key, value, found := strings.Cut(element, "=")
		if !found || key == "" || value == "" {
			return "", nil, false
		}
		switch key {
		case "t":
			if ts != "" || !allDigits(value) {
				return "", nil, false
			}
			ts = value
		case "v1":
			if !isHex64(value) {
				return "", nil, false
			}
			sig, _ := hex.DecodeString(value)
			sigs = append(sigs, sig)
		}
		// Unknown scheme keys are ignored for forward compatibility.
	}
	if ts == "" || len(sigs) == 0 {
		return "", nil, false
	}
	return ts, sigs, true
}

type WebhookEventType string

const (
	EventAuthenticationStarted       WebhookEventType = "AuthenticationStarted"
	EventAuthenticationSuccess       WebhookEventType = "AuthenticationSuccess"
	EventAuthenticationFailed        WebhookEventType = "AuthenticationFailed"
	EventAuthenticationCanceled      WebhookEventType = "AuthenticationCanceled"
	EventAccountsFetchingStarted     WebhookEventType = "AccountsFetchingStarted"
	EventAccountsFetchingSuccess     WebhookEventType = "AccountsFetchingSuccess"
	EventAccountsFetchingFailed      WebhookEventType = "AccountsFetchingFailed"
	EventTransactionsFetchingStarted WebhookEventType = "TransactionsFetchingStarted"
	EventTransactionsFetchingSuccess WebhookEventType = "TransactionsFetchingSuccess"
	EventTransactionsFetchingFailed  WebhookEventType = "TransactionsFetchingFailed"
	EventConnectionCreateSuccess     WebhookEventType = "ConnectionCreateSuccess"
	EventConnectionCreateFailed      WebhookEventType = "ConnectionCreateFailed"
	EventConnectionReconnectSuccess  WebhookEventType = "ConnectionReconnectSuccess"
	EventConnectionReconnectFailed   WebhookEventType = "ConnectionReconnectFailed"
	EventConnectionRefreshSuccess    WebhookEventType = "ConnectionRefreshSuccess"
	EventConnectionRefreshFailed     WebhookEventType = "ConnectionRefreshFailed"
	EventConnectionDeleted           WebhookEventType = "ConnectionDeleted"
	EventConnectionConsentRevoked    WebhookEventType = "ConnectionConsentRevoked"
	EventConnectionConsentExpired    WebhookEventType = "ConnectionConsentExpired"
)

// WebhookReasonCode may grow; treat unknown values as opaque.
type WebhookReasonCode string

const (
	ReasonConsentExpired              WebhookReasonCode = "consent_expired"
	ReasonConsentRevoked              WebhookReasonCode = "consent_revoked"
	ReasonAuthenticationFailed        WebhookReasonCode = "authentication_failed"
	ReasonAuthenticationCanceled      WebhookReasonCode = "authentication_canceled"
	ReasonAuthenticationTimeout       WebhookReasonCode = "authentication_timeout"
	ReasonBackgroundRefreshNotAllowed WebhookReasonCode = "background_refresh_not_allowed"
	ReasonProviderError               WebhookReasonCode = "provider_error"
	ReasonInternalError               WebhookReasonCode = "internal_error"
)

type WebhookReason struct {
	Code    WebhookReasonCode `json:"code"`
	Message *string           `json:"message"`
}

// Event is a parsed delivery: *WebhookEvent or *UnknownEvent.
type Event interface {
	EventType() string
	isEvent()
}

// WebhookEvent is a known lifecycle event.
type WebhookEvent struct {
	Type         WebhookEventType
	EventID      string
	ClientID     string
	ConnectionID string
	CreatedAt    string
	Reason       *WebhookReason
	// Extra holds the remaining top-level fields (e.g. remainingDays) as raw JSON.
	Extra map[string]json.RawMessage
}

func (e *WebhookEvent) EventType() string { return string(e.Type) }
func (*WebhookEvent) isEvent()            {}

// UnknownEvent is an event type this SDK version does not know: answer 2xx and ignore it.
type UnknownEvent struct {
	Type string
	Raw  map[string]json.RawMessage
}

func (e *UnknownEvent) EventType() string { return e.Type }
func (*UnknownEvent) isEvent()            {}

// WebhookParseError reports a delivery body that is not a JSON object: answer 4xx and alert.
type WebhookParseError struct {
	Message string
}

func (e *WebhookParseError) Error() string { return "partner: webhook body: " + e.Message }

// ParseEvent parses a delivery body. Unknown types come back as *UnknownEvent;
// the only error is *WebhookParseError.
func ParseEvent(body []byte) (Event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, &WebhookParseError{Message: err.Error()}
	}
	if raw == nil {
		return nil, &WebhookParseError{Message: "webhook body is not a JSON object"}
	}
	var eventType string
	if json.Unmarshal(raw["type"], &eventType) != nil || !knownEventTypes[WebhookEventType(eventType)] {
		return &UnknownEvent{Type: eventType, Raw: raw}, nil
	}
	ev := &WebhookEvent{
		Type:         WebhookEventType(eventType),
		EventID:      fieldString(raw["eventId"]),
		ClientID:     fieldString(raw["clientId"]),
		ConnectionID: fieldString(raw["connectionId"]),
		CreatedAt:    fieldString(raw["createdAt"]),
		Extra:        map[string]json.RawMessage{},
	}
	var reason WebhookReason
	if r, ok := raw["reason"]; ok && string(r) != "null" && json.Unmarshal(r, &reason) == nil {
		ev.Reason = &reason
	}
	for k, v := range raw {
		switch k {
		case "eventId", "type", "clientId", "connectionId", "createdAt", "reason":
		default:
			ev.Extra[k] = v
		}
	}
	return ev, nil
}

var knownEventTypes = map[WebhookEventType]bool{
	EventAuthenticationStarted: true, EventAuthenticationSuccess: true,
	EventAuthenticationFailed: true, EventAuthenticationCanceled: true,
	EventAccountsFetchingStarted: true, EventAccountsFetchingSuccess: true,
	EventAccountsFetchingFailed: true, EventTransactionsFetchingStarted: true,
	EventTransactionsFetchingSuccess: true, EventTransactionsFetchingFailed: true,
	EventConnectionCreateSuccess: true, EventConnectionCreateFailed: true,
	EventConnectionReconnectSuccess: true, EventConnectionReconnectFailed: true,
	EventConnectionRefreshSuccess: true, EventConnectionRefreshFailed: true,
	EventConnectionDeleted: true, EventConnectionConsentRevoked: true,
	EventConnectionConsentExpired: true,
}

// fieldString reads a top-level field as text: strings as-is, absent or null
// as "", any other JSON value as its literal.
func fieldString(v json.RawMessage) string {
	if v == nil || string(v) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}
