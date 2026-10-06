package partner_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	partner "github.com/budgetbakers/partner-sdk-go"
)

type reply struct {
	status  int
	body    string
	headers map[string]string
}

type recorded struct {
	method string
	path   string
	query  map[string][]string
	header http.Header
	body   string
}

// fakeAPI serves queued replies in order; an exhausted queue answers 599 so a
// test that makes an unexpected extra call fails loudly.
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	replies []reply
	calls   []recorded
	srv     *httptest.Server
}

func newFakeAPI(t *testing.T, replies ...reply) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, recorded{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
			header: r.Header.Clone(),
			body:   string(body),
		})
		var next reply
		if len(f.replies) > 0 {
			next, f.replies = f.replies[0], f.replies[1:]
		} else {
			next = reply{status: 599, body: `{"error":{"code":"internal_error","message":"unexpected call"},"requestId":"req_extra"}`}
		}
		f.mu.Unlock()
		for k, v := range next.headers {
			w.Header().Set(k, v)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(next.status)
		_, _ = io.WriteString(w, next.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.calls...)
}

// sleepLog records every wait handed to the sleep seam without sleeping.
type sleepLog struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleepLog) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleepLog) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

func newClient(f *fakeAPI, sl *sleepLog, opts ...partner.Option) *partner.BudgetBakers {
	base := []partner.Option{
		partner.WithBaseURL(f.srv.URL),
		partner.WithHTTPClient(f.srv.Client()),
		partner.WithSleep(sl.sleep),
		partner.WithJitter(func() float64 { return 0.5 }),
		partner.WithRetryBase(100 * time.Millisecond),
	}
	return partner.New("bb_test_key", append(base, opts...)...)
}

func ok(body string) reply { return reply{status: 200, body: body} }

const configBody = `{"partnerId":"p1","name":"Acme","mode":"sandbox","capabilities":{"refresh":true,"reconnect":true,"enrichment":false,"autoRevokeAfterCreate":false,"nonRegulatedProviders":false},"consentDuration":null,"countries":["CZ"],"webhook":{"signatureVersion":"v1"}}`

func envelope(code, message string) string {
	return `{"error":{"code":"` + code + `","message":"` + message + `"},"requestId":"req_body"}`
}
