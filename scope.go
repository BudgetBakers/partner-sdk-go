package partner

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"time"
)

// ClientScope holds everything bound to one end user; every call carries X-Client-Id.
type ClientScope struct {
	Connections     *ConnectionsService
	Accounts        *AccountsService
	ConnectSessions *ConnectSessionsService

	t        *transport
	clientID string
}

func (c *ClientScope) ClientID() string { return c.clientID }

func (c *ClientScope) do(ctx context.Context, method, path string, opts requestOptions, out any) error {
	opts.clientID = c.clientID
	return c.t.do(ctx, method, path, opts, out)
}

func (c *ClientScope) doData(ctx context.Context, method, path string, opts requestOptions, out any) error {
	opts.clientID = c.clientID
	return c.t.doData(ctx, method, path, opts, out)
}

func (c *ClientScope) idempotencyKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return c.t.newKey()
}

// Delete deletes this client and cascades to their connections, accounts and transactions.
func (c *ClientScope) Delete(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v2/clients/"+pathSegment(c.clientID), requestOptions{}, nil)
}

// ConnectionsService: the lifecycle actions (create, delete, refresh, reconnect, revoke) live on /v1.
type ConnectionsService struct {
	scope *ClientScope
}

func (s *ConnectionsService) Create(ctx context.Context, params ConnectionCreateParams) (*ConnectionCreateResponse, error) {
	var out ConnectionCreateResponse
	opts := requestOptions{
		body:           map[string]string{"providerId": params.ProviderID},
		idempotencyKey: s.scope.idempotencyKey(params.IdempotencyKey),
	}
	if err := s.scope.do(ctx, http.MethodPost, "/v1/connections", opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns the stored connection record: state, provider, consent expiry.
func (s *ConnectionsService) Get(ctx context.Context, connectionID string) (*Connection, error) {
	var out Connection
	if err := s.scope.doData(ctx, http.MethodGet, "/v2/connections/"+pathSegment(connectionID), requestOptions{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ConnectionsService) Delete(ctx context.Context, connectionID string) error {
	return s.scope.do(ctx, http.MethodDelete, "/v1/connections/"+pathSegment(connectionID), requestOptions{}, nil)
}

func (s *ConnectionsService) Refresh(ctx context.Context, connectionID string) (*RefreshAccepted, error) {
	var out RefreshAccepted
	if err := s.scope.do(ctx, http.MethodPost, "/v1/connections/"+pathSegment(connectionID)+"/refresh", requestOptions{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ConnectionsService) Reconnect(ctx context.Context, connectionID string, params ReconnectParams) (*ConnectionCreateResponse, error) {
	var out ConnectionCreateResponse
	opts := requestOptions{
		body:           struct{}{},
		idempotencyKey: s.scope.idempotencyKey(params.IdempotencyKey),
	}
	if err := s.scope.do(ctx, http.MethodPost, "/v1/connections/"+pathSegment(connectionID)+"/reconnect", opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Revoke is idempotent: revoking an already Inactive connection succeeds.
func (s *ConnectionsService) Revoke(ctx context.Context, connectionID string) error {
	return s.scope.do(ctx, http.MethodPatch, "/v1/connections/"+pathSegment(connectionID)+"/revoke", requestOptions{}, nil)
}

// ListAccounts returns every account of the connection, all pages walked, Disabled ones included.
func (s *ConnectionsService) ListAccounts(ctx context.Context, connectionID string) ([]Account, error) {
	accounts := []Account{}
	for page, err := range s.AccountPages(ctx, connectionID, ListAccountsParams{}) {
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, page.Data...)
	}
	return accounts, nil
}

func (s *ConnectionsService) AccountPages(ctx context.Context, connectionID string, params ListAccountsParams) iter.Seq2[*AccountPage, error] {
	path := "/v2/connections/" + pathSegment(connectionID) + "/accounts"
	return iteratePages(ctx, func(ctx context.Context, cursor string) (*AccountPage, error) {
		q := url.Values{}
		setInt(q, "limit", params.Limit)
		setString(q, "nextCursor", cursor)
		var page AccountPage
		if err := s.scope.do(ctx, http.MethodGet, path, requestOptions{query: q}, &page); err != nil {
			return nil, err
		}
		return &page, nil
	})
}

type AccountsService struct {
	scope *ClientScope
}

func (s *AccountsService) Get(ctx context.Context, accountID string) (*Account, error) {
	var out Account
	if err := s.scope.doData(ctx, http.MethodGet, "/v2/accounts/"+pathSegment(accountID), requestOptions{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Transactions iterates every transaction across pages.
func (s *AccountsService) Transactions(ctx context.Context, accountID string, params ListTransactionsParams) iter.Seq2[Transaction, error] {
	return iterateItems(s.TransactionPages(ctx, accountID, params))
}

func (s *AccountsService) TransactionPages(ctx context.Context, accountID string, params ListTransactionsParams) iter.Seq2[*TransactionPage, error] {
	path := "/v2/accounts/" + pathSegment(accountID) + "/transactions"
	return iteratePages(ctx, func(ctx context.Context, cursor string) (*TransactionPage, error) {
		q := url.Values{}
		setInt(q, "limit", params.Limit)
		setString(q, "sort", string(params.Sort))
		setString(q, "order", string(params.Order))
		setString(q, "dateFrom", params.DateFrom)
		setString(q, "dateTo", params.DateTo)
		setString(q, "recordState", string(params.RecordState))
		for _, vs := range params.VariableSymbol {
			q.Add("variableSymbol", vs)
		}
		setInt(q, "sinceSeq", params.SinceSeq)
		setInt(q, "sinceCreatedSeq", params.SinceCreatedSeq)
		setString(q, "nextCursor", cursor)
		var page TransactionPage
		if err := s.scope.do(ctx, http.MethodGet, path, requestOptions{query: q}, &page); err != nil {
			return nil, err
		}
		return &page, nil
	})
}

type ConnectSessionsService struct {
	scope *ClientScope
}

// Create starts a hosted connect session; open HostedURL in the user's browser.
func (s *ConnectSessionsService) Create(ctx context.Context, params ConnectSessionCreateParams) (*ConnectSessionCreateResponse, error) {
	body := map[string]string{"returnUrl": params.ReturnURL}
	if params.ProviderID != "" {
		body["providerId"] = params.ProviderID
	}
	if params.ConnectionID != "" {
		body["connectionId"] = params.ConnectionID
	}
	var out ConnectSessionCreateResponse
	opts := requestOptions{body: body, idempotencyKey: s.scope.idempotencyKey(params.IdempotencyKey)}
	if err := s.scope.do(ctx, http.MethodPost, "/v2/connect-sessions", opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ConnectSessionsService) Get(ctx context.Context, sessionID string) (*ConnectSession, error) {
	var out ConnectSession
	if err := s.scope.do(ctx, http.MethodGet, "/v2/connect-sessions/"+pathSegment(sessionID), requestOptions{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WaitOption configures WaitForTerminal.
type WaitOption func(*waitConfig)

type waitConfig struct {
	pollInterval time.Duration
	maxPolls     int
	onPoll       func(*ConnectSession)
}

// WithPollInterval sets the delay between polls (default 2 s).
func WithPollInterval(d time.Duration) WaitOption {
	return func(c *waitConfig) { c.pollInterval = d }
}

// WithMaxPolls caps the number of polls (default 150, about 5 minutes at the default interval).
func WithMaxPolls(n int) WaitOption {
	return func(c *waitConfig) { c.maxPolls = n }
}

// WithOnPoll is called with every polled session.
func WithOnPoll(f func(*ConnectSession)) WaitOption {
	return func(c *waitConfig) { c.onPoll = f }
}

// WaitForTerminal polls the session until it reaches a terminal state and returns it.
// The first poll is immediate. When maxPolls runs out it returns the last session
// seen with a nil error; maxPolls below 1 is an error.
func (s *ConnectSessionsService) WaitForTerminal(ctx context.Context, sessionID string, opts ...WaitOption) (*ConnectSession, error) {
	cfg := waitConfig{pollInterval: 2 * time.Second, maxPolls: 150}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxPolls < 1 {
		return nil, errors.New("partner: WaitForTerminal needs maxPolls >= 1")
	}
	var session *ConnectSession
	for poll := 0; poll < cfg.maxPolls; poll++ {
		if poll > 0 {
			if err := s.scope.t.sleep(ctx, cfg.pollInterval); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if session, err = s.Get(ctx, sessionID); err != nil {
			return nil, err
		}
		if cfg.onPoll != nil {
			cfg.onPoll(session)
		}
		if session.State.IsTerminal() {
			return session, nil
		}
	}
	return session, nil
}
