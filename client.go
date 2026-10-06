// Package partner is the BudgetBakers Partner API server SDK.
//
// It follows the Partner API v2 reference; the five connection lifecycle
// actions (create, delete, refresh, reconnect, revoke) follow the v1.1
// reference. Calls bound to one end user go through [BudgetBakers.Client],
// which sets the X-Client-Id header. Lists are iterators that walk every page,
// with a Pages variant for page-level control. Failures are *[APIError]
// (branch on its Code) or *[UnreachableError]. Money is [Decimal], never a
// float.
//
//	bb := partner.New(os.Getenv("BB_API_KEY"))
//	session, err := bb.Client(clientID).ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{
//		ReturnURL: "https://app.example.com/bb-callback",
//	})
package partner

import (
	"context"
	"iter"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"
)

const (
	DefaultBaseURL = "https://aisp-partner.bbapi.io"
	UserAgent      = "budgetbakers-partner-sdk-go/" + Version
)

// Option configures a BudgetBakers client.
type Option func(*BudgetBakers)

// WithBaseURL overrides the API base URL; the API key, not the host, selects sandbox or live.
func WithBaseURL(baseURL string) Option {
	return func(b *BudgetBakers) { b.t.baseURL = baseURL }
}

// WithRetryBase sets the backoff base for 429/5xx retries (default 500 ms).
func WithRetryBase(d time.Duration) Option {
	return func(b *BudgetBakers) { b.t.retryBase = d }
}

// WithMaxRetries sets the retries after the initial attempt (default 3; 0 disables retries).
func WithMaxRetries(n int) Option {
	return func(b *BudgetBakers) { b.t.maxRetries = n }
}

// WithHTTPClient replaces the HTTP client (default http.DefaultClient).
func WithHTTPClient(c *http.Client) Option {
	return func(b *BudgetBakers) { b.t.httpClient = c }
}

// WithSleep replaces the wait between retries and polls; it must return ctx.Err() once ctx is done.
func WithSleep(sleep func(ctx context.Context, d time.Duration) error) Option {
	return func(b *BudgetBakers) { b.t.sleep = sleep }
}

// WithJitter replaces the random source of the backoff jitter; f returns a value in [0, 1).
func WithJitter(f func() float64) Option {
	return func(b *BudgetBakers) { b.t.jitter = f }
}

// WithIdempotencyKeyGenerator replaces the UUID generator for automatic Idempotency-Keys.
func WithIdempotencyKeyGenerator(f func() string) Option {
	return func(b *BudgetBakers) { b.t.newKey = f }
}

// BudgetBakers is the Partner API client. It is safe for concurrent use.
type BudgetBakers struct {
	Partner   *PartnerService
	Providers *ProvidersService
	Clients   *ClientsService

	t *transport
}

// New creates a client for the given partner API key (bb_test_... / bb_live_...).
func New(apiKey string, opts ...Option) *BudgetBakers {
	b := &BudgetBakers{t: &transport{
		baseURL:    DefaultBaseURL,
		apiKey:     apiKey,
		retryBase:  500 * time.Millisecond,
		maxRetries: 3,
		httpClient: http.DefaultClient,
		sleep:      sleepContext,
		jitter:     rand.Float64,
		newKey:     newUUID,
	}}
	for _, o := range opts {
		o(b)
	}
	if b.t.httpClient == nil {
		b.t.httpClient = http.DefaultClient
	}
	b.Partner = &PartnerService{t: b.t}
	b.Providers = &ProvidersService{t: b.t}
	b.Clients = &ClientsService{b: b}
	return b
}

// Client scopes every client-bound call to one end user.
func (b *BudgetBakers) Client(clientID string) *ClientScope {
	c := &ClientScope{t: b.t, clientID: clientID}
	c.Connections = &ConnectionsService{scope: c}
	c.Accounts = &AccountsService{scope: c}
	c.ConnectSessions = &ConnectSessionsService{scope: c}
	return c
}

type PartnerService struct {
	t *transport
}

// GetConfig is capability discovery: the calling partner and its key mode.
func (s *PartnerService) GetConfig(ctx context.Context) (*PartnerConfigResponse, error) {
	var out PartnerConfigResponse
	if err := s.t.do(ctx, http.MethodGet, "/v2/partner/config", requestOptions{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ProvidersService struct {
	t *transport
}

// List iterates every provider across pages.
func (s *ProvidersService) List(ctx context.Context, params ListProvidersParams) iter.Seq2[Provider, error] {
	return iterateItems(s.Pages(ctx, params))
}

func (s *ProvidersService) Pages(ctx context.Context, params ListProvidersParams) iter.Seq2[*ProviderPage, error] {
	return iteratePages(ctx, func(ctx context.Context, cursor string) (*ProviderPage, error) {
		q := url.Values{}
		setString(q, "country", params.Country)
		setString(q, "search", params.Search)
		setInt(q, "limit", params.Limit)
		setString(q, "nextCursor", cursor)
		var page ProviderPage
		if err := s.t.do(ctx, http.MethodGet, "/v2/providers", requestOptions{query: q}, &page); err != nil {
			return nil, err
		}
		return &page, nil
	})
}

type ClientsService struct {
	b *BudgetBakers
}

// Create upserts by ExternalID: an existing ExternalID returns the existing client.
func (s *ClientsService) Create(ctx context.Context, req ClientCreateRequest) (*Client, error) {
	var out Client
	if err := s.b.t.doData(ctx, http.MethodPost, "/v2/clients", requestOptions{body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ClientsService) Get(ctx context.Context, clientID string) (*Client, error) {
	var out Client
	opts := requestOptions{clientID: clientID}
	if err := s.b.t.doData(ctx, http.MethodGet, "/v2/clients/"+pathSegment(clientID), opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetByExternalID matches your ExternalID exactly; it returns nil, nil when no client carries it.
func (s *ClientsService) GetByExternalID(ctx context.Context, externalID string) (*Client, error) {
	var page ClientPage
	opts := requestOptions{query: url.Values{"externalId": {externalID}}}
	if err := s.b.t.do(ctx, http.MethodGet, "/v2/clients", opts, &page); err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	return &page.Data[0], nil
}

// Delete is Client(clientID).Delete.
func (s *ClientsService) Delete(ctx context.Context, clientID string) error {
	return s.b.Client(clientID).Delete(ctx)
}
