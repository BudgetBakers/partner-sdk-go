package partner_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	partner "github.com/budgetbakers/partner-sdk-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetryAfterSecondsWinsOverBackoff(t *testing.T) {
	f := newFakeAPI(t,
		reply{status: 429, body: envelope("rate_limited", "slow down"), headers: map[string]string{"Retry-After": "1"}},
		ok(configBody),
	)
	sl := &sleepLog{}
	cfg, err := newClient(f, sl).Partner.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg == nil || cfg.Name != "Acme" {
		t.Fatalf("config = %+v, want the second reply", cfg)
	}
	if got := sl.all(); len(got) != 1 || got[0] != time.Second {
		t.Fatalf("waits = %v, want [1s]", got)
	}
	if n := len(f.recorded()); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func TestRetryAfterNotAllDigitsFallsBackToBackoff(t *testing.T) {
	f := newFakeAPI(t,
		reply{status: 429, body: envelope("rate_limited", "slow down"), headers: map[string]string{"Retry-After": "Wed, 21 Oct 2026 07:28:00 GMT"}},
		ok(configBody),
	)
	sl := &sleepLog{}
	if _, err := newClient(f, sl).Partner.GetConfig(context.Background()); err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got := sl.all(); len(got) != 1 || got[0] != 100*time.Millisecond {
		t.Fatalf("waits = %v, want [100ms] (base backoff)", got)
	}
}

func TestBackoffDoublesPerAttempt(t *testing.T) {
	f := newFakeAPI(t,
		reply{status: 503, body: envelope("internal_error", "down")},
		reply{status: 503, body: envelope("internal_error", "down")},
		ok(configBody),
	)
	sl := &sleepLog{}
	if _, err := newClient(f, sl).Partner.GetConfig(context.Background()); err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	got := sl.all()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("waits = %v, want %v (jitter 0.5 is the midpoint)", got, want)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	base := 100 * time.Millisecond
	for _, j := range []float64{0, 0.25, 0.999} {
		f := newFakeAPI(t,
			reply{status: 503, body: envelope("internal_error", "down")},
			reply{status: 502, body: envelope("internal_error", "down")},
			ok(configBody),
		)
		sl := &sleepLog{}
		jitter := j
		c := newClient(f, sl, partner.WithJitter(func() float64 { return jitter }))
		if _, err := c.Partner.GetConfig(context.Background()); err != nil {
			t.Fatalf("jitter %v: GetConfig: %v", j, err)
		}
		got := sl.all()
		if len(got) != 2 {
			t.Fatalf("jitter %v: waits = %v, want 2", j, got)
		}
		for i, d := range got {
			nominal := base << i
			lo, hi := nominal*3/4, nominal*5/4
			if d < lo || d > hi {
				t.Errorf("jitter %v: wait %d = %v, want within [%v, %v]", j, i, d, lo, hi)
			}
		}
		if got[0] >= got[1] {
			t.Errorf("jitter %v: waits %v do not grow", j, got)
		}
	}
}

func TestRetriesExhaustIntoAPIError(t *testing.T) {
	var replies []reply
	for range 4 {
		replies = append(replies, reply{status: 503, body: envelope("internal_error", "down")})
	}
	f := newFakeAPI(t, replies...)
	sl := &sleepLog{}
	_, err := newClient(f, sl).Partner.GetConfig(context.Background())
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.HTTPStatus != 503 || apiErr.Code != partner.CodeInternalError {
		t.Fatalf("err = %+v", apiErr)
	}
	if n := len(f.recorded()); n != 4 {
		t.Fatalf("calls = %d, want 4 (initial + 3 default retries)", n)
	}
}

func TestMaxRetriesZeroDisablesRetries(t *testing.T) {
	f := newFakeAPI(t, reply{status: 503, body: envelope("internal_error", "down")})
	sl := &sleepLog{}
	_, err := newClient(f, sl, partner.WithMaxRetries(0)).Partner.GetConfig(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
	if got := sl.all(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestNon429ClientErrorNotRetried(t *testing.T) {
	f := newFakeAPI(t, reply{status: 406, body: envelope("background_refresh_not_allowed", "no")})
	sl := &sleepLog{}
	_, err := newClient(f, sl).Client("c1").Connections.Refresh(context.Background(), "conn1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != partner.CodeBackgroundRefreshNotAllowed || apiErr.HTTPStatus != 406 {
		t.Fatalf("err = %v, want *APIError background_refresh_not_allowed 406", err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestPostWithoutIdempotencyKeyNotRetried(t *testing.T) {
	f := newFakeAPI(t, reply{status: 503, body: envelope("internal_error", "down")})
	sl := &sleepLog{}
	_, err := newClient(f, sl).Clients.Create(context.Background(), partner.ClientCreateRequest{Email: "a@example.com", CountryCode: "CZ"})
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if k := calls[0].header.Get("Idempotency-Key"); k != "" {
		t.Fatalf("Clients.Create sent Idempotency-Key %q", k)
	}
	if got := sl.all(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestPostWithIdempotencyKeyRetried(t *testing.T) {
	f := newFakeAPI(t,
		reply{status: 503, body: envelope("internal_error", "down")},
		reply{status: 201, body: `{"connectionId":"conn1","redirectUrl":"https://bank.test/auth","expiresAt":"2026-10-01T10:00:00Z"}`},
	)
	sl := &sleepLog{}
	res, err := newClient(f, sl).Client("c1").Connections.Create(context.Background(), partner.ConnectionCreateParams{ProviderID: "prov_xf"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res == nil || res.ConnectionID != "conn1" {
		t.Fatalf("res = %+v", res)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	k0, k1 := calls[0].header.Get("Idempotency-Key"), calls[1].header.Get("Idempotency-Key")
	if k0 == "" || k0 != k1 {
		t.Fatalf("Idempotency-Key %q then %q, want the same non-empty key on the replay", k0, k1)
	}
}

func TestRequestIDHeaderPreferredOverBody(t *testing.T) {
	f := newFakeAPI(t, reply{status: 404, body: envelope("not_found", "no such connection"), headers: map[string]string{"X-Request-Id": "req_hdr"}})
	sl := &sleepLog{}
	_, err := newClient(f, sl).Client("c1").Connections.Get(context.Background(), "conn1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.RequestID != "req_hdr" {
		t.Fatalf("RequestID = %q, want req_hdr", apiErr.RequestID)
	}
	if apiErr.Code != partner.CodeNotFound || apiErr.Message != "no such connection" {
		t.Fatalf("err = %+v", apiErr)
	}
}

func TestRequestIDFromBodyWithoutHeader(t *testing.T) {
	f := newFakeAPI(t, reply{status: 404, body: envelope("not_found", "gone")})
	sl := &sleepLog{}
	_, err := newClient(f, sl).Client("c1").Connections.Get(context.Background(), "conn1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) || apiErr.RequestID != "req_body" {
		t.Fatalf("err = %#v, want RequestID req_body", err)
	}
}

var errDial = errors.New("dial tcp: connection refused")

func TestNetworkErrorIsUnreachableAndNotRetried(t *testing.T) {
	var attempts atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, errDial
	})}
	sl := &sleepLog{}
	c := partner.New("bb_test_key",
		partner.WithBaseURL("http://partner.invalid"),
		partner.WithHTTPClient(hc),
		partner.WithSleep(sl.sleep),
	)
	_, err := c.Partner.GetConfig(context.Background())
	var unreachable *partner.UnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("err = %v, want *UnreachableError", err)
	}
	if !errors.Is(err, errDial) {
		t.Fatalf("err = %v does not unwrap to the transport error", err)
	}
	var apiErr *partner.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("err is also *APIError: %+v", apiErr)
	}
	if n := attempts.Load(); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	if got := sl.all(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestCancelledContextIsContextError(t *testing.T) {
	f := newFakeAPI(t, ok(configBody))
	sl := &sleepLog{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(f, sl).Partner.GetConfig(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is context.Canceled", err)
	}
	var unreachable *partner.UnreachableError
	if errors.As(err, &unreachable) {
		t.Fatalf("err = %v, must not be *UnreachableError", err)
	}
}

func TestContextCancelledDuringBackoff(t *testing.T) {
	f := newFakeAPI(t,
		reply{status: 503, body: envelope("internal_error", "down")},
		ok(configBody),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := newClient(f, &sleepLog{}, partner.WithSleep(sleep)).Partner.GetConfig(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is context.Canceled", err)
	}
	var unreachable *partner.UnreachableError
	if errors.As(err, &unreachable) {
		t.Fatalf("err = %v, must not be *UnreachableError", err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("calls = %d, want 1 (no attempt after the cancel)", n)
	}
}

func TestStandardHeaders(t *testing.T) {
	f := newFakeAPI(t, ok(configBody))
	if _, err := newClient(f, &sleepLog{}).Partner.GetConfig(context.Background()); err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	h := calls[0].header
	for name, want := range map[string]string{
		"X-Api-Key":  "bb_test_key",
		"Accept":     "application/json",
		"User-Agent": "budgetbakers-partner-sdk-go/" + partner.Version,
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if partner.UserAgent != "budgetbakers-partner-sdk-go/"+partner.Version {
		t.Errorf("UserAgent constant = %q", partner.UserAgent)
	}
}
