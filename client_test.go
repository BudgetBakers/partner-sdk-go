package partner_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"testing"
	"time"

	partner "github.com/budgetbakers/partner-sdk-go"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const (
	clientJSON     = `{"id":"c1","externalId":"ext-1","email":"a@example.com","countryCode":"CZ"}`
	connectionJSON = `{"id":"conn1","state":"Active","providerId":"prov_xf","consentExpiresAt":"2027-01-01T00:00:00Z"}`
	accountJSON    = `{"id":"acc1","name":"Main","type":"Current","balance":"1234.56","currencyCode":"CZK","iban":null,"subscriptionStatus":"Active"}`
	createdJSON    = `{"connectionId":"conn1","redirectUrl":"https://bank.test/auth","expiresAt":"2026-10-01T10:00:00Z"}`
	sessionJSON    = `{"sessionId":"s1","hostedUrl":"https://aisp-connect.test.bbapi.dev/s1","expiresAt":"2026-10-01T10:00:00Z"}`
)

func session(state string) reply {
	return ok(`{"sessionId":"s1","state":"` + state + `","connectionId":null,"resultCode":null,"error":null}`)
}

func TestClientScopeSendsClientIDAndPartnerCallsDoNot(t *testing.T) {
	f := newFakeAPI(t, ok(configBody), ok(`{"data":`+connectionJSON+`}`))
	c := newClient(f, &sleepLog{})
	ctx := context.Background()
	if _, err := c.Partner.GetConfig(ctx); err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	scope := c.Client("c1")
	if scope.ClientID() != "c1" {
		t.Fatalf("ClientID() = %q", scope.ClientID())
	}
	if _, err := scope.Connections.Get(ctx, "conn1"); err != nil {
		t.Fatalf("Connections.Get: %v", err)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].path != "/v2/partner/config" {
		t.Errorf("GetConfig path = %q", calls[0].path)
	}
	if _, present := calls[0].header["X-Client-Id"]; present {
		t.Errorf("GetConfig sent X-Client-Id %q", calls[0].header.Get("X-Client-Id"))
	}
	if got := calls[1].header.Get("X-Client-Id"); got != "c1" {
		t.Errorf("Connections.Get X-Client-Id = %q, want c1", got)
	}
}

func TestOperationRoutes(t *testing.T) {
	type op struct {
		name     string
		reply    reply
		call     func(context.Context, *partner.BudgetBakers) error
		method   string
		path     string
		clientID string
	}
	ops := []op{
		{"clients.create", reply{status: 201, body: `{"data":` + clientJSON + `}`}, func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Clients.Create(ctx, partner.ClientCreateRequest{Email: "a@example.com", CountryCode: "CZ", ExternalID: "ext-1"})
			return err
		}, "POST", "/v2/clients", ""},
		{"clients.get", ok(`{"data":` + clientJSON + `}`), func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Clients.Get(ctx, "c1")
			return err
		}, "GET", "/v2/clients/c1", "c1"},
		{"clients.delete", reply{status: 204}, func(ctx context.Context, c *partner.BudgetBakers) error {
			return c.Clients.Delete(ctx, "c1")
		}, "DELETE", "/v2/clients/c1", "c1"},
		{"scope.delete", reply{status: 204}, func(ctx context.Context, c *partner.BudgetBakers) error {
			return c.Client("c1").Delete(ctx)
		}, "DELETE", "/v2/clients/c1", "c1"},
		{"connections.create", reply{status: 201, body: createdJSON}, func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").Connections.Create(ctx, partner.ConnectionCreateParams{ProviderID: "prov_xf"})
			return err
		}, "POST", "/v1/connections", "c1"},
		{"connections.get", ok(`{"data":` + connectionJSON + `}`), func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").Connections.Get(ctx, "conn1")
			return err
		}, "GET", "/v2/connections/conn1", "c1"},
		{"connections.delete", reply{status: 204}, func(ctx context.Context, c *partner.BudgetBakers) error {
			return c.Client("c1").Connections.Delete(ctx, "conn1")
		}, "DELETE", "/v1/connections/conn1", "c1"},
		{"connections.refresh", reply{status: 202, body: `{"status":"accepted","nextRefreshPossibleAt":null}`}, func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").Connections.Refresh(ctx, "conn1")
			return err
		}, "POST", "/v1/connections/conn1/refresh", "c1"},
		{"connections.reconnect", reply{status: 201, body: createdJSON}, func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").Connections.Reconnect(ctx, "conn1", partner.ReconnectParams{})
			return err
		}, "POST", "/v1/connections/conn1/reconnect", "c1"},
		{"connections.revoke", ok(`{}`), func(ctx context.Context, c *partner.BudgetBakers) error {
			return c.Client("c1").Connections.Revoke(ctx, "conn1")
		}, "PATCH", "/v1/connections/conn1/revoke", "c1"},
		{"accounts.get", ok(`{"data":` + accountJSON + `}`), func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").Accounts.Get(ctx, "acc1")
			return err
		}, "GET", "/v2/accounts/acc1", "c1"},
		{"connectSessions.create", reply{status: 201, body: sessionJSON}, func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb"})
			return err
		}, "POST", "/v2/connect-sessions", "c1"},
		{"connectSessions.get", session("Fetching"), func(ctx context.Context, c *partner.BudgetBakers) error {
			_, err := c.Client("c1").ConnectSessions.Get(ctx, "s1")
			return err
		}, "GET", "/v2/connect-sessions/s1", "c1"},
	}
	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			f := newFakeAPI(t, o.reply)
			if err := o.call(context.Background(), newClient(f, &sleepLog{})); err != nil {
				t.Fatalf("call: %v", err)
			}
			calls := f.recorded()
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}
			if calls[0].method != o.method || calls[0].path != o.path {
				t.Errorf("%s %s, want %s %s", calls[0].method, calls[0].path, o.method, o.path)
			}
			if got := calls[0].header.Get("X-Client-Id"); got != o.clientID {
				t.Errorf("X-Client-Id = %q, want %q", got, o.clientID)
			}
			if got := calls[0].header.Get("X-Api-Key"); got != "bb_test_key" {
				t.Errorf("X-Api-Key = %q", got)
			}
		})
	}
}

func TestSingleResourcesUnwrapData(t *testing.T) {
	ctx := context.Background()

	f := newFakeAPI(t, ok(`{"data":`+clientJSON+`}`))
	cl, err := newClient(f, &sleepLog{}).Clients.Get(ctx, "c1")
	if err != nil || cl == nil || cl.ID != "c1" || cl.ExternalID == nil || *cl.ExternalID != "ext-1" {
		t.Fatalf("Clients.Get = %+v, %v", cl, err)
	}

	f = newFakeAPI(t, reply{status: 201, body: `{"data":` + clientJSON + `}`})
	cl, err = newClient(f, &sleepLog{}).Clients.Create(ctx, partner.ClientCreateRequest{Email: "a@example.com", CountryCode: "CZ"})
	if err != nil || cl == nil || cl.ID != "c1" {
		t.Fatalf("Clients.Create = %+v, %v", cl, err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(f.recorded()[0].body), &sent); err != nil {
		t.Fatalf("create body %q: %v", f.recorded()[0].body, err)
	}
	if sent["email"] != "a@example.com" || sent["countryCode"] != "CZ" {
		t.Fatalf("create body = %v", sent)
	}
	if _, present := sent["externalId"]; present {
		t.Fatalf("create body carries an empty externalId: %v", sent)
	}

	f = newFakeAPI(t, ok(`{"data":`+connectionJSON+`}`))
	conn, err := newClient(f, &sleepLog{}).Client("c1").Connections.Get(ctx, "conn1")
	if err != nil || conn == nil || conn.ID != "conn1" || conn.State == nil || *conn.State != partner.ConnectionStateActive {
		t.Fatalf("Connections.Get = %+v, %v", conn, err)
	}

	f = newFakeAPI(t, ok(`{"data":`+accountJSON+`}`))
	acc, err := newClient(f, &sleepLog{}).Client("c1").Accounts.Get(ctx, "acc1")
	if err != nil || acc == nil || acc.ID != "acc1" || acc.Balance == nil || *acc.Balance != "1234.56" || acc.IBAN != nil {
		t.Fatalf("Accounts.Get = %+v, %v", acc, err)
	}
	if acc.SubscriptionStatus != partner.SubscriptionActive {
		t.Fatalf("SubscriptionStatus = %q", acc.SubscriptionStatus)
	}
}

func TestUnwrappedResponsesStayFlat(t *testing.T) {
	ctx := context.Background()

	f := newFakeAPI(t, ok(configBody))
	cfg, err := newClient(f, &sleepLog{}).Partner.GetConfig(ctx)
	if err != nil || cfg == nil || cfg.PartnerID != "p1" || cfg.Mode != partner.ModeSandbox || !cfg.Capabilities.Refresh || cfg.Webhook.SignatureVersion != "v1" {
		t.Fatalf("GetConfig = %+v, %v", cfg, err)
	}

	f = newFakeAPI(t, reply{status: 201, body: sessionJSON})
	cs, err := newClient(f, &sleepLog{}).Client("c1").ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb"})
	if err != nil || cs == nil || cs.SessionID != "s1" || cs.HostedURL == "" {
		t.Fatalf("ConnectSessions.Create = %+v, %v", cs, err)
	}

	f = newFakeAPI(t, ok(`{"sessionId":"s1","state":"Completed","connectionId":"conn1","resultCode":"Ok","error":null}`))
	s, err := newClient(f, &sleepLog{}).Client("c1").ConnectSessions.Get(ctx, "s1")
	if err != nil || s == nil || s.State != partner.SessionCompleted || s.ConnectionID == nil || *s.ConnectionID != "conn1" || s.ResultCode == nil || *s.ResultCode != partner.ResultOk {
		t.Fatalf("ConnectSessions.Get = %+v, %v", s, err)
	}

	f = newFakeAPI(t, reply{status: 201, body: createdJSON})
	cr, err := newClient(f, &sleepLog{}).Client("c1").Connections.Create(ctx, partner.ConnectionCreateParams{ProviderID: "prov_xf"})
	if err != nil || cr == nil || cr.ConnectionID != "conn1" || cr.RedirectURL == "" {
		t.Fatalf("Connections.Create = %+v, %v", cr, err)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(f.recorded()[0].body), &sent)
	if sent["providerId"] != "prov_xf" {
		t.Fatalf("create body = %s", f.recorded()[0].body)
	}

	f = newFakeAPI(t, reply{status: 202, body: `{"status":"accepted","nextRefreshPossibleAt":"2026-10-01T12:00:00Z"}`})
	ra, err := newClient(f, &sleepLog{}).Client("c1").Connections.Refresh(ctx, "conn1")
	if err != nil || ra == nil || ra.Status != "accepted" || ra.NextRefreshPossibleAt == nil {
		t.Fatalf("Refresh = %+v, %v", ra, err)
	}
}

func TestGetByExternalID(t *testing.T) {
	ctx := context.Background()
	f := newFakeAPI(t,
		ok(`{"limit":1,"nextCursor":null,"data":[`+clientJSON+`]}`),
		ok(`{"limit":1,"nextCursor":null,"data":[]}`),
	)
	c := newClient(f, &sleepLog{})
	cl, err := c.Clients.GetByExternalID(ctx, "ext-1")
	if err != nil || cl == nil || cl.ID != "c1" {
		t.Fatalf("GetByExternalID(hit) = %+v, %v", cl, err)
	}
	cl, err = c.Clients.GetByExternalID(ctx, "nobody")
	if err != nil || cl != nil {
		t.Fatalf("GetByExternalID(miss) = %+v, %v, want nil, nil", cl, err)
	}
	calls := f.recorded()
	if calls[0].method != "GET" || calls[0].path != "/v2/clients" || !slices.Equal(calls[0].query["externalId"], []string{"ext-1"}) {
		t.Fatalf("request = %s %s %v", calls[0].method, calls[0].path, calls[0].query)
	}
	if _, present := calls[0].header["X-Client-Id"]; present {
		t.Fatalf("GetByExternalID sent X-Client-Id")
	}
}

func TestConnectSessionBody(t *testing.T) {
	f := newFakeAPI(t, reply{status: 201, body: sessionJSON}, reply{status: 201, body: sessionJSON})
	scope := newClient(f, &sleepLog{}).Client("c1")
	ctx := context.Background()
	if _, err := scope.ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb", ConnectionID: "conn1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := scope.ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb", ProviderID: "prov_xf"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	calls := f.recorded()
	var first, second map[string]any
	_ = json.Unmarshal([]byte(calls[0].body), &first)
	_ = json.Unmarshal([]byte(calls[1].body), &second)
	if first["returnUrl"] != "https://app.example.com/cb" || first["connectionId"] != "conn1" {
		t.Errorf("reconnect session body = %s", calls[0].body)
	}
	if _, present := first["providerId"]; present {
		t.Errorf("reconnect session body carries providerId: %s", calls[0].body)
	}
	if second["providerId"] != "prov_xf" {
		t.Errorf("session body = %s", calls[1].body)
	}
	if _, present := second["connectionId"]; present {
		t.Errorf("session body carries connectionId: %s", calls[1].body)
	}
}

func TestIdempotencyKeys(t *testing.T) {
	ctx := context.Background()
	f := newFakeAPI(t,
		reply{status: 201, body: createdJSON},
		reply{status: 201, body: createdJSON},
		reply{status: 201, body: sessionJSON},
		reply{status: 201, body: createdJSON},
		reply{status: 201, body: createdJSON},
		reply{status: 201, body: sessionJSON},
	)
	n := 0
	gen := func() string { n++; return "gen-" + string(rune('0'+n)) }
	c := newClient(f, &sleepLog{}, partner.WithIdempotencyKeyGenerator(gen)).Client("c1")
	mustNil := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustNil(c.Connections.Create(ctx, partner.ConnectionCreateParams{ProviderID: "prov_xf"}))
	mustNil(c.Connections.Reconnect(ctx, "conn1", partner.ReconnectParams{}))
	mustNil(c.ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb"}))
	mustNil(c.Connections.Create(ctx, partner.ConnectionCreateParams{ProviderID: "prov_xf", IdempotencyKey: "mine-1"}))
	mustNil(c.Connections.Reconnect(ctx, "conn1", partner.ReconnectParams{IdempotencyKey: "mine-2"}))
	mustNil(c.ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{ReturnURL: "https://app.example.com/cb", IdempotencyKey: "mine-3"}))

	f2 := newFakeAPI(t, reply{status: 201, body: `{"data":` + clientJSON + `}`})
	mustNil(newClient(f2, &sleepLog{}, partner.WithIdempotencyKeyGenerator(gen)).Clients.Create(ctx, partner.ClientCreateRequest{Email: "a@example.com", CountryCode: "CZ"}))

	calls := f.recorded()
	want := []string{"gen-1", "gen-2", "gen-3", "mine-1", "mine-2", "mine-3"}
	for i, w := range want {
		if got := calls[i].header.Get("Idempotency-Key"); got != w {
			t.Errorf("call %d (%s %s) Idempotency-Key = %q, want %q", i, calls[i].method, calls[i].path, got, w)
		}
	}
	if _, present := f2.recorded()[0].header["Idempotency-Key"]; present {
		t.Errorf("Clients.Create sent Idempotency-Key %q", f2.recorded()[0].header.Get("Idempotency-Key"))
	}
}

func TestIdempotencyKeyDefaultsToUUID(t *testing.T) {
	f := newFakeAPI(t, reply{status: 201, body: createdJSON}, reply{status: 201, body: createdJSON})
	scope := newClient(f, &sleepLog{}).Client("c1")
	for range 2 {
		if _, err := scope.Connections.Create(context.Background(), partner.ConnectionCreateParams{ProviderID: "prov_xf"}); err != nil {
			t.Fatal(err)
		}
	}
	calls := f.recorded()
	k0, k1 := calls[0].header.Get("Idempotency-Key"), calls[1].header.Get("Idempotency-Key")
	if !uuidRE.MatchString(k0) || !uuidRE.MatchString(k1) {
		t.Fatalf("keys %q, %q, want UUID v4", k0, k1)
	}
	if k0 == k1 {
		t.Fatalf("two creates shared key %q", k0)
	}
}

func TestReadsCarryNoIdempotencyKey(t *testing.T) {
	f := newFakeAPI(t, ok(configBody), reply{status: 202, body: `{"status":"accepted","nextRefreshPossibleAt":null}`})
	c := newClient(f, &sleepLog{}, partner.WithIdempotencyKeyGenerator(func() string { return "gen" }))
	ctx := context.Background()
	if _, err := c.Partner.GetConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Client("c1").Connections.Refresh(ctx, "conn1"); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.recorded() {
		if _, present := call.header["Idempotency-Key"]; present {
			t.Errorf("%s %s sent Idempotency-Key", call.method, call.path)
		}
	}
}

func TestProvidersWalkConcatenatesPages(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":2,"nextCursor":"cur2","data":[{"id":"p1","name":"One","bicCodes":[]},{"id":"p2","name":"Two","bicCodes":[]}]}`),
		ok(`{"limit":2,"nextCursor":null,"data":[{"id":"p3","name":"Three","bicCodes":["KOMBCZPP"]}]}`),
	)
	var ids []string
	for p, err := range newClient(f, &sleepLog{}).Providers.List(context.Background(), partner.ListProvidersParams{Country: "CZ", Limit: partner.Ptr(2)}) {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"p1", "p2", "p3"}) {
		t.Fatalf("ids = %v", ids)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for i, call := range calls {
		if call.method != "GET" || call.path != "/v2/providers" {
			t.Errorf("call %d = %s %s", i, call.method, call.path)
		}
		if !slices.Equal(call.query["country"], []string{"CZ"}) || !slices.Equal(call.query["limit"], []string{"2"}) {
			t.Errorf("call %d query = %v", i, call.query)
		}
		if _, present := call.header["X-Client-Id"]; present {
			t.Errorf("providers call sent X-Client-Id")
		}
	}
	if _, present := calls[0].query["nextCursor"]; present {
		t.Errorf("first page sent nextCursor %v", calls[0].query["nextCursor"])
	}
	if !slices.Equal(calls[1].query["nextCursor"], []string{"cur2"}) {
		t.Errorf("second page query = %v, want nextCursor=cur2", calls[1].query)
	}
}

func TestProviderPagesYieldsPages(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":1,"nextCursor":"cur2","data":[{"id":"p1","name":"One","bicCodes":[]}]}`),
		ok(`{"limit":1,"nextCursor":null,"data":[{"id":"p2","name":"Two","bicCodes":[]}]}`),
	)
	var pages []*partner.ProviderPage
	for p, err := range newClient(f, &sleepLog{}).Providers.Pages(context.Background(), partner.ListProvidersParams{}) {
		if err != nil {
			t.Fatalf("Pages: %v", err)
		}
		pages = append(pages, p)
	}
	if len(pages) != 2 || pages[0].NextCursor == nil || *pages[0].NextCursor != "cur2" || pages[1].NextCursor != nil || pages[0].Limit != 1 {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestIteratorStopsOnFirstError(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":1,"nextCursor":"cur2","data":[{"id":"p1","name":"One","bicCodes":[]}]}`),
		reply{status: 500, body: envelope("internal_error", "boom")},
		ok(`{"limit":1,"nextCursor":null,"data":[{"id":"p3","name":"Three","bicCodes":[]}]}`),
	)
	var ids []string
	var errs []error
	for p, err := range newClient(f, &sleepLog{}, partner.WithMaxRetries(0)).Providers.List(context.Background(), partner.ListProvidersParams{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"p1"}) {
		t.Fatalf("ids = %v, want [p1]", ids)
	}
	if len(errs) != 1 {
		t.Fatalf("errors yielded = %d (%v), want exactly 1", len(errs), errs)
	}
	var apiErr *partner.APIError
	if !errors.As(errs[0], &apiErr) || apiErr.HTTPStatus != 500 {
		t.Fatalf("err = %v, want *APIError 500", errs[0])
	}
	if n := len(f.recorded()); n != 2 {
		t.Fatalf("calls = %d, want 2 (no page after the error)", n)
	}
}

func TestIteratorEarlyBreak(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":2,"nextCursor":"cur2","data":[{"id":"p1","name":"One","bicCodes":[]},{"id":"p2","name":"Two","bicCodes":[]}]}`),
		ok(`{"limit":2,"nextCursor":null,"data":[]}`),
	)
	var ids []string
	for p, err := range newClient(f, &sleepLog{}).Providers.List(context.Background(), partner.ListProvidersParams{}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
		break
	}
	if !slices.Equal(ids, []string{"p1"}) {
		t.Fatalf("ids = %v", ids)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("calls = %d, want 1 (no fetch after break)", n)
	}
}

func TestTransactionsWalkAndFilters(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":2,"nextCursor":"c2","data":[{"id":"t1","seq":1,"createdSeq":1,"amount":"0.10","recordDate":"2026-09-01"},{"id":"t2","seq":2,"createdSeq":2,"amount":0.20,"recordDate":"2026-09-02"}]}`),
		ok(`{"limit":2,"nextCursor":null,"data":[{"id":"t3","seq":3,"createdSeq":3,"amount":"90071992547409.93","recordDate":"2026-09-03"},{"id":"t4","seq":4,"createdSeq":4,"amount":null,"recordDate":"2026-09-04","enrichment":null}]}`),
	)
	params := partner.ListTransactionsParams{
		Limit:           partner.Ptr(2),
		Sort:            partner.SortAmount,
		Order:           partner.OrderDesc,
		DateFrom:        "2026-09-01",
		DateTo:          "2026-09-30",
		RecordState:     partner.RecordStateCleared,
		VariableSymbol:  []string{"123", "456"},
		SinceCreatedSeq: partner.Ptr(int64(7)),
	}
	var amounts []string
	for tx, err := range newClient(f, &sleepLog{}).Client("c1").Accounts.Transactions(context.Background(), "acc1", params) {
		if err != nil {
			t.Fatalf("Transactions: %v", err)
		}
		if tx.Amount == nil {
			amounts = append(amounts, "<nil>")
		} else {
			amounts = append(amounts, string(*tx.Amount))
		}
	}
	if !slices.Equal(amounts, []string{"0.10", "0.20", "90071992547409.93", "<nil>"}) {
		t.Fatalf("amounts = %v", amounts)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	q := calls[0].query
	if calls[0].path != "/v2/accounts/acc1/transactions" || calls[0].header.Get("X-Client-Id") != "c1" {
		t.Fatalf("request = %s %v", calls[0].path, calls[0].header)
	}
	for k, want := range map[string][]string{
		"limit":           {"2"},
		"sort":            {"amount"},
		"order":           {"desc"},
		"dateFrom":        {"2026-09-01"},
		"dateTo":          {"2026-09-30"},
		"recordState":     {"Cleared"},
		"variableSymbol":  {"123", "456"},
		"sinceCreatedSeq": {"7"},
	} {
		if !slices.Equal(q[k], want) {
			t.Errorf("query %s = %v, want %v", k, q[k], want)
		}
	}
	for _, k := range []string{"sinceSeq", "nextCursor"} {
		if _, present := q[k]; present {
			t.Errorf("first page sent %s = %v", k, q[k])
		}
	}
	if !slices.Equal(calls[1].query["nextCursor"], []string{"c2"}) {
		t.Errorf("second page query = %v", calls[1].query)
	}
}

func TestTransactionPagesSinceSeq(t *testing.T) {
	f := newFakeAPI(t, ok(`{"limit":50,"nextCursor":null,"data":[{"id":"t9","seq":10,"createdSeq":4,"amount":"-2326.00","recordDate":"2026-09-01"}]}`))
	var pages int
	for p, err := range newClient(f, &sleepLog{}).Client("c1").Accounts.TransactionPages(context.Background(), "acc1", partner.ListTransactionsParams{SinceSeq: partner.Ptr(int64(9))}) {
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(p.Data) != 1 || p.Data[0].Seq != 10 || p.Data[0].CreatedSeq != 4 {
			t.Fatalf("page = %+v", p)
		}
	}
	if pages != 1 {
		t.Fatalf("pages = %d", pages)
	}
	q := f.recorded()[0].query
	if !slices.Equal(q["sinceSeq"], []string{"9"}) {
		t.Fatalf("query = %v", q)
	}
	for _, k := range []string{"limit", "sort", "order", "variableSymbol", "sinceCreatedSeq"} {
		if _, present := q[k]; present {
			t.Errorf("unset %s sent as %v", k, q[k])
		}
	}
}

func TestListAccountsWalksPages(t *testing.T) {
	f := newFakeAPI(t,
		ok(`{"limit":1,"nextCursor":"a2","data":[`+accountJSON+`]}`),
		ok(`{"limit":1,"nextCursor":null,"data":[{"id":"acc2","balance":null,"subscriptionStatus":"Disabled"}]}`),
	)
	accs, err := newClient(f, &sleepLog{}).Client("c1").Connections.ListAccounts(context.Background(), "conn1")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accs) != 2 || accs[0].ID != "acc1" || accs[1].ID != "acc2" || accs[1].Balance != nil || accs[1].SubscriptionStatus != partner.SubscriptionDisabled {
		t.Fatalf("accounts = %+v", accs)
	}
	calls := f.recorded()
	if calls[0].path != "/v2/connections/conn1/accounts" || !slices.Equal(calls[1].query["nextCursor"], []string{"a2"}) {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestAccountPagesLimit(t *testing.T) {
	f := newFakeAPI(t, ok(`{"limit":5,"nextCursor":null,"data":[]}`))
	for _, err := range newClient(f, &sleepLog{}).Client("c1").Connections.AccountPages(context.Background(), "conn1", partner.ListAccountsParams{Limit: partner.Ptr(5)}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	calls := f.recorded()
	if len(calls) != 1 || !slices.Equal(calls[0].query["limit"], []string{"5"}) {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestWaitForTerminalStopsOnFirstTerminal(t *testing.T) {
	f := newFakeAPI(t, session("AwaitingBankSelection"), session("Fetching"), session("Completed"), session("Fetching"))
	sl := &sleepLog{}
	var seen []partner.ConnectSessionState
	s, err := newClient(f, sl).Client("c1").ConnectSessions.WaitForTerminal(context.Background(), "s1",
		partner.WithPollInterval(250*time.Millisecond),
		partner.WithOnPoll(func(s *partner.ConnectSession) { seen = append(seen, s.State) }),
	)
	if err != nil {
		t.Fatalf("WaitForTerminal: %v", err)
	}
	if s == nil || s.State != partner.SessionCompleted {
		t.Fatalf("session = %+v, want Completed", s)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("polls = %d, want 3", n)
	}
	if !slices.Equal(seen, []partner.ConnectSessionState{"AwaitingBankSelection", "Fetching", "Completed"}) {
		t.Fatalf("OnPoll saw %v", seen)
	}
	want := []time.Duration{250 * time.Millisecond, 250 * time.Millisecond}
	if got := sl.all(); !slices.Equal(got, want) {
		t.Fatalf("waits = %v, want %v (first poll immediate)", got, want)
	}
	for _, call := range f.recorded() {
		if call.path != "/v2/connect-sessions/s1" || call.header.Get("X-Client-Id") != "c1" {
			t.Fatalf("poll = %s %v", call.path, call.header)
		}
	}
}

func TestWaitForTerminalEveryTerminalState(t *testing.T) {
	for _, st := range []string{"Failed", "Cancelled", "Expired"} {
		f := newFakeAPI(t, session(st))
		s, err := newClient(f, &sleepLog{}).Client("c1").ConnectSessions.WaitForTerminal(context.Background(), "s1")
		if err != nil || s == nil || string(s.State) != st {
			t.Errorf("%s: session = %+v, %v", st, s, err)
		}
		if n := len(f.recorded()); n != 1 {
			t.Errorf("%s: polls = %d, want 1", st, n)
		}
	}
}

func TestWaitForTerminalMaxPolls(t *testing.T) {
	f := newFakeAPI(t, session("AwaitingBankSelection"), session("RedirectedToBank"), session("Fetching"), session("Completed"))
	sl := &sleepLog{}
	s, err := newClient(f, sl).Client("c1").ConnectSessions.WaitForTerminal(context.Background(), "s1", partner.WithMaxPolls(3))
	if err != nil {
		t.Fatalf("WaitForTerminal: %v, want nil when maxPolls runs out", err)
	}
	if s == nil || s.State != partner.SessionFetching {
		t.Fatalf("session = %+v, want the last one seen (Fetching)", s)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("polls = %d, want 3", n)
	}
	if got := sl.all(); !slices.Equal(got, []time.Duration{2 * time.Second, 2 * time.Second}) {
		t.Fatalf("waits = %v, want two default 2s intervals", got)
	}
}

func TestWaitForTerminalMaxPollsZero(t *testing.T) {
	f := newFakeAPI(t, session("Fetching"))
	sessions := newClient(f, &sleepLog{}).Client("c1").ConnectSessions
	if s, err := sessions.WaitForTerminal(context.Background(), "s1", partner.WithMaxPolls(1)); err != nil || s == nil {
		t.Fatalf("control maxPolls 1 = %+v, %v, want the one session seen", s, err)
	}
	s, err := sessions.WaitForTerminal(context.Background(), "s1", partner.WithMaxPolls(0))
	if err == nil {
		t.Fatalf("WaitForTerminal(maxPolls 0) = %+v, nil, want an error", s)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("polls = %d, want 1 (none for maxPolls 0)", n)
	}
}

func TestWaitForTerminalPropagatesPollError(t *testing.T) {
	f := newFakeAPI(t, session("Fetching"), reply{status: 404, body: envelope("not_found", "gone")})
	_, err := newClient(f, &sleepLog{}).Client("c1").ConnectSessions.WaitForTerminal(context.Background(), "s1")
	var apiErr *partner.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != partner.CodeNotFound {
		t.Fatalf("err = %v, want *APIError not_found", err)
	}
	if n := len(f.recorded()); n != 2 {
		t.Fatalf("polls = %d, want 2", n)
	}
}

func TestWaitForTerminalHonoursContext(t *testing.T) {
	f := newFakeAPI(t, session("Fetching"), session("Fetching"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := newClient(f, &sleepLog{}, partner.WithSleep(sleep)).Client("c1").ConnectSessions.WaitForTerminal(ctx, "s1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("polls = %d, want 1", n)
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := map[partner.ConnectSessionState]bool{
		partner.SessionAwaitingBankSelection:    false,
		partner.SessionRedirectedToBank:         false,
		partner.SessionFetching:                 false,
		partner.SessionAwaitingAccountSelection: false,
		partner.SessionCompleted:                true,
		partner.SessionFailed:                   true,
		partner.SessionCancelled:                true,
		partner.SessionExpired:                  true,
	}
	for s, want := range terminal {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%s.IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestDefaults(t *testing.T) {
	if partner.DefaultBaseURL != "https://aisp-partner.bbapi.io" {
		t.Errorf("DefaultBaseURL = %q", partner.DefaultBaseURL)
	}
	if partner.Version != "0.2.0" {
		t.Errorf("Version = %q", partner.Version)
	}
	var host string
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		host = r.URL.Scheme + "://" + r.URL.Host
		return nil, errDial
	})}
	_, _ = partner.New("bb_test_key", partner.WithHTTPClient(hc), partner.WithSleep((&sleepLog{}).sleep)).Partner.GetConfig(context.Background())
	if host != partner.DefaultBaseURL {
		t.Errorf("default request host = %q, want %q", host, partner.DefaultBaseURL)
	}
}

func TestDefaultRetryBase(t *testing.T) {
	f := newFakeAPI(t, reply{status: 503, body: envelope("internal_error", "down")}, ok(configBody))
	sl := &sleepLog{}
	c := partner.New("bb_test_key",
		partner.WithBaseURL(f.srv.URL),
		partner.WithHTTPClient(f.srv.Client()),
		partner.WithSleep(sl.sleep),
		partner.WithJitter(func() float64 { return 0.5 }),
	)
	if _, err := c.Partner.GetConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sl.all(); !slices.Equal(got, []time.Duration{500 * time.Millisecond}) {
		t.Fatalf("waits = %v, want [500ms]", got)
	}
}
