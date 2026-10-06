package partner_test

import (
	"context"
	"errors"
	"testing"

	partner "github.com/budgetbakers/partner-sdk-go"
)

func TestErrorEnvelopeWithNextRefresh(t *testing.T) {
	body := `{"error":{"code":"refresh_cooldown","message":"wait a bit","nextRefreshPossibleAt":"2026-10-01T12:00:00Z"},"requestId":"req_cool"}`
	f := newFakeAPI(t, reply{status: 429, body: body})
	_, err := newClient(f, &sleepLog{}, partner.WithMaxRetries(0)).Client("c1").Connections.Refresh(context.Background(), "conn1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	want := partner.APIError{
		Code:                  partner.CodeRefreshCooldown,
		HTTPStatus:            429,
		RequestID:             "req_cool",
		Message:               "wait a bit",
		NextRefreshPossibleAt: "2026-10-01T12:00:00Z",
	}
	if *apiErr != want {
		t.Fatalf("err = %+v\nwant  %+v", *apiErr, want)
	}
	if apiErr.Error() == "" {
		t.Fatal("Error() is empty")
	}
}

func TestErrorTopLevelNextRefreshIgnored(t *testing.T) {
	body := `{"error":{"code":"refresh_quota_exceeded","message":"quota"},"nextRefreshPossibleAt":"2026-10-02T00:00:00Z","requestId":"req_q"}`
	f := newFakeAPI(t, reply{status: 429, body: body})
	_, err := newClient(f, &sleepLog{}, partner.WithMaxRetries(0)).Client("c1").Connections.Refresh(context.Background(), "conn1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != partner.CodeRefreshQuotaExceeded || apiErr.NextRefreshPossibleAt != "" {
		t.Fatalf("err = %+v, want refresh_quota_exceeded with NextRefreshPossibleAt read only from error{}", apiErr)
	}
}

func TestErrorCodeFallsBackToStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   partner.ErrorCode
	}{
		{"unknown code 404", 404, envelope("some_future_code", "x"), partner.CodeNotFound},
		{"unknown code 400", 400, envelope("some_future_code", "x"), partner.CodeInternalError},
		{"missing code 401", 401, `{"error":{"message":"no key"},"requestId":"r"}`, partner.CodeUnauthorized},
		{"non-JSON 401", 401, `Unauthorized`, partner.CodeUnauthorized},
		{"non-JSON 429", 429, `<html>slow down</html>`, partner.CodeRateLimited},
		{"non-JSON 502", 502, `<html>Bad Gateway</html>`, partner.CodeInternalError},
		{"empty 500", 500, ``, partner.CodeInternalError},
		{"known code kept", 400, envelope("validation_error", "bad"), partner.CodeValidationError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, reply{status: tc.status, body: tc.body, headers: map[string]string{"Content-Type": "text/html"}})
			_, err := newClient(f, &sleepLog{}, partner.WithMaxRetries(0)).Client("c1").Connections.Get(context.Background(), "conn1")
			var apiErr *partner.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Code != tc.want || apiErr.HTTPStatus != tc.status {
				t.Fatalf("code %q status %d, want %q %d", apiErr.Code, apiErr.HTTPStatus, tc.want, tc.status)
			}
		})
	}
}

func TestUnreachableErrorMessage(t *testing.T) {
	e := &partner.UnreachableError{Err: errDial}
	if !errors.Is(e, errDial) {
		t.Fatal("Unwrap does not expose Err")
	}
	if e.Error() == "" {
		t.Fatal("Error() is empty")
	}
}
