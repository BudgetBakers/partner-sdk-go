package partner

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTP transport: auth headers, typed errors, and retries with exponential
// backoff and jitter on 429/5xx honouring Retry-After. A POST is retried only
// when an Idempotency-Key makes the replay safe; network errors are not retried.

type transport struct {
	baseURL    string
	apiKey     string
	retryBase  time.Duration
	maxRetries int
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
	jitter     func() float64
	newKey     func() string
}

type requestOptions struct {
	clientID       string
	query          url.Values
	body           any
	idempotencyKey string
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func isRetryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodDelete, http.MethodPatch, http.MethodPut:
		return true
	}
	return false
}

func allDigitsNonEmpty(s string) bool { return s != "" && allDigits(s) }

// do sends the request and decodes a 2xx body into out (nil discards it).
func (t *transport) do(ctx context.Context, method, path string, opts requestOptions, out any) error {
	target := strings.TrimRight(t.baseURL, "/") + path
	if len(opts.query) > 0 {
		target += "?" + opts.query.Encode()
	}
	var payload []byte
	if opts.body != nil {
		var err error
		if payload, err = json.Marshal(opts.body); err != nil {
			return err
		}
	}
	canRetry := isRetryableMethod(method) || opts.idempotencyKey != ""

	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("X-Api-Key", t.apiKey)
		if opts.clientID != "" {
			req.Header.Set("X-Client-Id", opts.clientID)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if opts.idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", opts.idempotencyKey)
		}

		res, err := t.httpClient.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return &UnreachableError{Err: err}
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return &UnreachableError{Err: err}
		}

		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			return json.Unmarshal(data, out)
		}

		retryable := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		if retryable && canRetry && attempt < t.maxRetries {
			var delay time.Duration
			if ra := res.Header.Get("Retry-After"); allDigitsNonEmpty(ra) {
				secs, _ := strconv.ParseInt(ra, 10, 64)
				delay = time.Duration(secs) * time.Second
			} else {
				// Exponential backoff with +-25 percent jitter against thundering herds.
				delay = time.Duration(float64(t.retryBase) * float64(int64(1)<<attempt) * (0.75 + t.jitter()*0.5))
			}
			if err := t.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		return parseErrorEnvelope(res.StatusCode, data, res.Header.Get("X-Request-Id"))
	}
}

// doData is do for v2 single-resource answers wrapped in {"data": ...}.
func (t *transport) doData(ctx context.Context, method, path string, opts requestOptions, out any) error {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := t.do(ctx, method, path, opts, &env); err != nil {
		return err
	}
	if env.Data == nil {
		return errors.New("partner: expected a { data } envelope")
	}
	return json.Unmarshal(env.Data, out)
}

func pathSegment(s string) string { return url.PathEscape(s) }

func setString(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func setInt[T int | int64](q url.Values, key string, value *T) {
	if value != nil {
		q.Set(key, strconv.FormatInt(int64(*value), 10))
	}
}

// iteratePages walks a cursor-paginated list until the cursor runs out.
func iteratePages[T any](ctx context.Context, fetch func(ctx context.Context, cursor string) (*Page[T], error)) iter.Seq2[*Page[T], error] {
	return func(yield func(*Page[T], error) bool) {
		cursor := ""
		for {
			page, err := fetch(ctx, cursor)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(page, nil) || page.NextCursor == nil || *page.NextCursor == "" {
				return
			}
			cursor = *page.NextCursor
		}
	}
}

func iterateItems[T any](pages iter.Seq2[*Page[T], error]) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for page, err := range pages {
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range page.Data {
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}
