// Command conformance is the contract-test driver (contract-tests/PROTOCOL.md v1).
// Every step goes through the SDK's public surface. Modes: probe | scenario |
// webhooksig | events. stdout carries exactly one JSON document; logs go to stderr.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	partner "github.com/budgetbakers/partner-sdk-go"
)

type identity struct {
	Lang    string `json:"lang"`
	SDK     string `json:"sdk"`
	Version string `json:"version"`
}

var driverIdentity = identity{"go", "github.com/budgetbakers/partner-sdk-go", partner.Version}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "probe":
		return emit(struct {
			ProtocolVersion int `json:"protocolVersion"`
			identity
		}{1, driverIdentity})
	case "scenario":
		return scenarioMode()
	case "webhooksig":
		return webhooksigMode()
	case "events":
		return eventsMode()
	}
	emit(map[string]string{"unsupported": "mode " + mode})
	return 3
}

func emit(doc any) int {
	out, err := json.Marshal(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Stdout.Write(out)
	return 0
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(into)
}

// ---- scenario mode -----------------------------------------------------------

type driverConfig struct {
	RetryBaseMs    float64 `json:"retryBaseMs"`
	MaxRetries     int     `json:"maxRetries"`
	PollIntervalMs float64 `json:"pollIntervalMs"`
	MaxPolls       int     `json:"maxPolls"`
}

type step struct {
	ID   string            `json:"id"`
	Op   string            `json:"op"`
	Args map[string]any    `json:"args"`
	Save map[string]string `json:"save"`
}

type stepError struct {
	Code       partner.ErrorCode `json:"code"`
	HTTPStatus int               `json:"httpStatus"`
	RequestID  *string           `json:"requestId"`
}

type journalStep struct {
	ID      string          `json:"id"`
	Op      string          `json:"op"`
	Ok      json.RawMessage `json:"ok,omitempty"`
	Error   *stepError      `json:"error,omitempty"`
	Skipped string          `json:"skipped,omitempty"`
	Crash   string          `json:"crash,omitempty"`
}

type env struct {
	bb     *partner.BudgetBakers
	config driverConfig
}

func (e env) scope(args map[string]any) *partner.ClientScope {
	return e.bb.Client(str(args, "clientId"))
}

type opFunc func(ctx context.Context, e env, args map[string]any) (any, error)

type unresolvedVar struct{ name string }

func (u unresolvedVar) Error() string { return "unresolved var " + u.name }

func scenarioMode() int {
	file, baseURL, apiKey := os.Getenv("CT_SCENARIO_FILE"), os.Getenv("CT_BASE_URL"), os.Getenv("CT_API_KEY")
	if file == "" || baseURL == "" || apiKey == "" {
		fmt.Fprintln(os.Stderr, "CT_SCENARIO_FILE, CT_BASE_URL and CT_API_KEY are required")
		return 1
	}
	var fixture struct {
		ProtocolVersion json.Number       `json:"protocolVersion"`
		Name            string            `json:"name"`
		Vars            map[string]string `json:"vars"`
		Driver          struct {
			Config driverConfig `json:"config"`
			Steps  []step       `json:"steps"`
		} `json:"driver"`
	}
	if err := readJSON(file, &fixture); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if fixture.ProtocolVersion.String() != "1" {
		emit(map[string]string{"unsupported": "protocolVersion " + fixture.ProtocolVersion.String()})
		return 3
	}
	cfg := fixture.Driver.Config
	e := env{
		bb: partner.New(apiKey,
			partner.WithBaseURL(baseURL),
			partner.WithRetryBase(millis(cfg.RetryBaseMs)),
			partner.WithMaxRetries(cfg.MaxRetries),
		),
		config: cfg,
	}
	vars := map[string]string{}
	for k, v := range fixture.Vars {
		vars[k] = v
	}

	ctx := context.Background()
	steps := []journalStep{}
	for _, st := range fixture.Driver.Steps {
		fn, ok := ops[st.Op]
		if !ok {
			emit(map[string]string{"unsupported": st.Op})
			return 3
		}
		steps = append(steps, runStep(ctx, e, fn, st, vars))
	}
	return emit(struct {
		ProtocolVersion int           `json:"protocolVersion"`
		Driver          identity      `json:"driver"`
		Scenario        string        `json:"scenario"`
		Steps           []journalStep `json:"steps"`
	}{1, driverIdentity, fixture.Name, steps})
}

func runStep(ctx context.Context, e env, fn opFunc, st step, vars map[string]string) (entry journalStep) {
	entry = journalStep{ID: st.ID, Op: st.Op}
	defer func() {
		if r := recover(); r != nil {
			entry = journalStep{ID: st.ID, Op: st.Op, Crash: fmt.Sprint(r)}
		}
	}()
	args, err := interpolateArgs(st.Args, vars)
	var ok any
	if err == nil {
		ok, err = fn(ctx, e, args)
	}
	var unresolved unresolvedVar
	var apiErr *partner.APIError
	switch {
	case errors.As(err, &unresolved):
		entry.Skipped = unresolved.Error()
		return entry
	case errors.As(err, &apiErr):
		entry.Error = &stepError{Code: apiErr.Code, HTTPStatus: apiErr.HTTPStatus, RequestID: nullable(apiErr.RequestID)}
		return entry
	case err != nil:
		entry.Crash = err.Error()
		return entry
	}
	raw, err := json.Marshal(ok)
	if err != nil {
		entry.Crash = err.Error()
		return entry
	}
	entry.Ok = raw
	if len(st.Save) > 0 {
		var generic any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&generic); err == nil {
			for name, path := range st.Save {
				if v := extractPath(generic, path); v != nil {
					vars[name] = jsString(v)
				}
			}
		}
	}
	return entry
}

var varPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

func interpolateArgs(args map[string]any, vars map[string]string) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range args {
		s, isString := v.(string)
		if !isString {
			out[k] = v
			continue
		}
		var missing string
		out[k] = varPattern.ReplaceAllStringFunc(s, func(m string) string {
			name := varPattern.FindStringSubmatch(m)[1]
			resolved, ok := vars[name]
			if !ok && missing == "" {
				missing = name
			}
			return resolved
		})
		if missing != "" {
			return nil, unresolvedVar{missing}
		}
	}
	return out, nil
}

var pathToken = regexp.MustCompile(`[A-Za-z0-9_]+|\[\d+\]`)

// extractPath follows a save micro-path: dot fields and [n] indexes only.
func extractPath(value any, path string) any {
	current := value
	for _, token := range pathToken.FindAllString(path, -1) {
		if strings.HasPrefix(token, "[") {
			list, ok := current.([]any)
			if !ok {
				return nil
			}
			i, _ := strconv.Atoi(token[1 : len(token)-1])
			if i >= len(list) {
				return nil
			}
			current = list[i]
			continue
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = obj[token]
	}
	return current
}

// jsString stringifies a decoded JSON value the way JavaScript's String() does.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		if f, err := x.Float64(); err == nil && strings.ContainsAny(x.String(), ".eE") {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, el := range x {
			if el != nil {
				parts[i] = jsString(el)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// str is the driver's String(args[key]): a missing key reads "undefined".
func str(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return "undefined"
	}
	return jsString(v)
}

func opt(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	return jsString(v)
}

func optInt(args map[string]any, key string) *int64 {
	n, ok := args[key].(json.Number)
	if !ok {
		return nil
	}
	i, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil {
			return nil
		}
		i = int64(f)
	}
	return &i
}

func optLimit(args map[string]any) *int {
	n := optInt(args, "limit")
	if n == nil {
		return nil
	}
	return partner.Ptr(int(*n))
}

func millis(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---- ops (one for one with sdks/typescript/src/conformance/cli.ts) ----------

type accountView struct {
	ID           string           `json:"id"`
	Type         *string          `json:"type"`
	Balance      *partner.Decimal `json:"balance"`
	CurrencyCode *string          `json:"currencyCode"`
	IBAN         *string          `json:"iban"`
}

type accountViewV2 struct {
	ID                 string           `json:"id"`
	Balance            *partner.Decimal `json:"balance"`
	SubscriptionStatus *string          `json:"subscriptionStatus"`
}

func toAccountView(a partner.Account) accountView {
	return accountView{a.ID, a.Type, a.Balance, a.CurrencyCode, a.IBAN}
}

func toAccountViewV2(a partner.Account) accountViewV2 {
	return accountViewV2{a.ID, a.Balance, nullable(string(a.SubscriptionStatus))}
}

func transactionParams(args map[string]any) partner.ListTransactionsParams {
	p := partner.ListTransactionsParams{
		Limit:           optLimit(args),
		Sort:            partner.TransactionSort(opt(args, "sort")),
		Order:           partner.SortOrder(opt(args, "order")),
		DateFrom:        opt(args, "dateFrom"),
		DateTo:          opt(args, "dateTo"),
		RecordState:     partner.RecordState(opt(args, "recordState")),
		SinceSeq:        optInt(args, "sinceSeq"),
		SinceCreatedSeq: optInt(args, "sinceCreatedSeq"),
	}
	if _, ok := args["variableSymbol"]; ok {
		p.VariableSymbol = []string{opt(args, "variableSymbol")}
	}
	return p
}

func walkAccounts(ctx context.Context, scope *partner.ClientScope, args map[string]any) ([]partner.Account, int, error) {
	accounts := []partner.Account{}
	pages := 0
	for page, err := range scope.Connections.AccountPages(ctx, str(args, "connectionId"), partner.ListAccountsParams{Limit: optLimit(args)}) {
		if err != nil {
			return nil, 0, err
		}
		pages++
		accounts = append(accounts, page.Data...)
	}
	return accounts, pages, nil
}

type transactionWalk struct {
	Count        int                `json:"count"`
	Pages        int                `json:"pages"`
	Amounts      []*partner.Decimal `json:"amounts"`
	Seqs         []int64            `json:"seqs"`
	SumAmount    partner.Decimal    `json:"sumAmount"`
	AnyRecurrent bool               `json:"anyRecurrent"`
}

func walkTransactions(ctx context.Context, scope *partner.ClientScope, args map[string]any) (*transactionWalk, error) {
	w := &transactionWalk{Amounts: []*partner.Decimal{}, Seqs: []int64{}}
	present := []partner.Decimal{}
	for page, err := range scope.Accounts.TransactionPages(ctx, str(args, "accountId"), transactionParams(args)) {
		if err != nil {
			return nil, err
		}
		w.Pages++
		w.Count += len(page.Data)
		for _, t := range page.Data {
			w.Amounts = append(w.Amounts, t.Amount)
			if t.Amount != nil {
				present = append(present, *t.Amount)
			}
			w.Seqs = append(w.Seqs, t.Seq)
			if t.Enrichment != nil && t.Enrichment.Recurrent {
				w.AnyRecurrent = true
			}
		}
	}
	sum, err := partner.SumDecimals(present...)
	if err != nil {
		return nil, err
	}
	w.SumAmount = sum
	return w, nil
}

func clientCreateRequest(args map[string]any) partner.ClientCreateRequest {
	return partner.ClientCreateRequest{
		Email:       opt(args, "email"),
		CountryCode: opt(args, "countryCode"),
		ExternalID:  opt(args, "externalId"),
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

var ops = map[string]opFunc{
	"partner.getConfig": func(ctx context.Context, e env, _ map[string]any) (any, error) {
		return e.bb.Partner.GetConfig(ctx)
	},

	"providers.listAll": func(ctx context.Context, e env, args map[string]any) (any, error) {
		ids := []string{}
		count, pages := 0, 0
		for page, err := range e.bb.Providers.Pages(ctx, partner.ListProvidersParams{Country: opt(args, "country"), Limit: optLimit(args)}) {
			if err != nil {
				return nil, err
			}
			pages++
			count += len(page.Data)
			for _, p := range page.Data {
				ids = append(ids, p.ID)
			}
		}
		return map[string]any{"count": count, "pages": pages, "ids": ids}, nil
	},

	"clients.create": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.bb.Clients.Create(ctx, clientCreateRequest(args))
	},
	"clients.get": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.bb.Clients.Get(ctx, str(args, "clientId"))
	},
	"clients.getByExternalId": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.bb.Clients.GetByExternalID(ctx, str(args, "externalId"))
	},
	"clients.delete": func(ctx context.Context, e env, args map[string]any) (any, error) {
		if err := e.scope(args).Delete(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	},

	"connectSessions.create": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.scope(args).ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{
			ReturnURL:      str(args, "returnUrl"),
			ProviderID:     opt(args, "providerId"),
			ConnectionID:   opt(args, "connectionId"),
			IdempotencyKey: opt(args, "idempotencyKey"),
		})
	},
	"connectSessions.get": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.scope(args).ConnectSessions.Get(ctx, str(args, "sessionId"))
	},
	"connectSessions.waitForTerminal": func(ctx context.Context, e env, args map[string]any) (any, error) {
		states := []*string{}
		session, err := e.scope(args).ConnectSessions.WaitForTerminal(ctx, str(args, "sessionId"),
			partner.WithPollInterval(millis(e.config.PollIntervalMs)),
			partner.WithMaxPolls(e.config.MaxPolls),
			partner.WithOnPoll(func(s *partner.ConnectSession) {
				states = append(states, nullable(string(s.State)))
			}),
		)
		if err != nil {
			return nil, err
		}
		return struct {
			States       []*string `json:"states"`
			State        *string   `json:"state"`
			ConnectionID any       `json:"connectionId"`
			ResultCode   any       `json:"resultCode"`
			Error        any       `json:"error"`
		}{states, nullable(string(session.State)), deref(session.ConnectionID), deref(session.ResultCode), deref(session.Error)}, nil
	},

	"connections.create": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.scope(args).Connections.Create(ctx, partner.ConnectionCreateParams{
			ProviderID:     str(args, "providerId"),
			IdempotencyKey: opt(args, "idempotencyKey"),
		})
	},
	"connections.get": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.scope(args).Connections.Get(ctx, str(args, "connectionId"))
	},
	"connections.delete": func(ctx context.Context, e env, args map[string]any) (any, error) {
		if err := e.scope(args).Connections.Delete(ctx, str(args, "connectionId")); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	},
	"connections.refresh": func(ctx context.Context, e env, args map[string]any) (any, error) {
		res, err := e.scope(args).Connections.Refresh(ctx, str(args, "connectionId"))
		if err != nil {
			return nil, err
		}
		return struct {
			Status                string  `json:"status"`
			NextRefreshPossibleAt *string `json:"nextRefreshPossibleAt"`
		}{res.Status, res.NextRefreshPossibleAt}, nil
	},
	"connections.reconnect": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return e.scope(args).Connections.Reconnect(ctx, str(args, "connectionId"), partner.ReconnectParams{
			IdempotencyKey: opt(args, "idempotencyKey"),
		})
	},
	"connections.revoke": func(ctx context.Context, e env, args map[string]any) (any, error) {
		if err := e.scope(args).Connections.Revoke(ctx, str(args, "connectionId")); err != nil {
			return nil, err
		}
		return map[string]bool{"revoked": true}, nil
	},

	"accounts.list": func(ctx context.Context, e env, args map[string]any) (any, error) {
		accounts, err := e.scope(args).Connections.ListAccounts(ctx, str(args, "connectionId"))
		if err != nil {
			return nil, err
		}
		views := make([]accountView, len(accounts))
		for i, a := range accounts {
			views[i] = toAccountView(a)
		}
		return struct {
			Count    int           `json:"count"`
			Accounts []accountView `json:"accounts"`
		}{len(accounts), views}, nil
	},
	"transactions.listAll": func(ctx context.Context, e env, args map[string]any) (any, error) {
		w, err := walkTransactions(ctx, e.scope(args), args)
		if err != nil {
			return nil, err
		}
		return struct {
			Count     int                `json:"count"`
			Pages     int                `json:"pages"`
			Amounts   []*partner.Decimal `json:"amounts"`
			SumAmount partner.Decimal    `json:"sumAmount"`
		}{w.Count, w.Pages, w.Amounts, w.SumAmount}, nil
	},

	// v2-shaped views: the same SDK calls, the normalization keeps the v2 fields.

	"clientsV2.create": func(ctx context.Context, e env, args map[string]any) (any, error) {
		c, err := e.bb.Clients.Create(ctx, clientCreateRequest(args))
		if err != nil {
			return nil, err
		}
		return struct {
			ID         string  `json:"id"`
			ExternalID *string `json:"externalId"`
		}{c.ID, c.ExternalID}, nil
	},
	"clientsV2.getByExternalId": func(ctx context.Context, e env, args map[string]any) (any, error) {
		c, err := e.bb.Clients.GetByExternalID(ctx, str(args, "externalId"))
		if err != nil {
			return nil, err
		}
		ids := []string{}
		if c != nil {
			ids = append(ids, c.ID)
		}
		return struct {
			Count int      `json:"count"`
			IDs   []string `json:"ids"`
		}{len(ids), ids}, nil
	},
	"connectionsV2.get": func(ctx context.Context, e env, args map[string]any) (any, error) {
		c, err := e.scope(args).Connections.Get(ctx, str(args, "connectionId"))
		if err != nil {
			return nil, err
		}
		return struct {
			ID               string                   `json:"id"`
			State            *partner.ConnectionState `json:"state"`
			ConsentExpiresAt *string                  `json:"consentExpiresAt"`
		}{c.ID, c.State, c.ConsentExpiresAt}, nil
	},
	"providersV2.listAll": func(ctx context.Context, e env, args map[string]any) (any, error) {
		codes := []*string{}
		statuses := []*partner.ProviderStatus{}
		count, pages := 0, 0
		params := partner.ListProvidersParams{Country: opt(args, "country"), Search: opt(args, "search"), Limit: optLimit(args)}
		for page, err := range e.bb.Providers.Pages(ctx, params) {
			if err != nil {
				return nil, err
			}
			pages++
			count += len(page.Data)
			for _, p := range page.Data {
				codes = append(codes, p.Code)
				statuses = append(statuses, p.Status)
			}
		}
		return struct {
			Count    int                       `json:"count"`
			Pages    int                       `json:"pages"`
			Codes    []*string                 `json:"codes"`
			Statuses []*partner.ProviderStatus `json:"statuses"`
		}{count, pages, codes, statuses}, nil
	},
	"accountsV2.list": func(ctx context.Context, e env, args map[string]any) (any, error) {
		accounts, pages, err := walkAccounts(ctx, e.scope(args), args)
		if err != nil {
			return nil, err
		}
		views := make([]accountViewV2, len(accounts))
		for i, a := range accounts {
			views[i] = toAccountViewV2(a)
		}
		return struct {
			Count    int             `json:"count"`
			Pages    int             `json:"pages"`
			Accounts []accountViewV2 `json:"accounts"`
		}{len(accounts), pages, views}, nil
	},
	"accountsV2.get": func(ctx context.Context, e env, args map[string]any) (any, error) {
		a, err := e.scope(args).Accounts.Get(ctx, str(args, "accountId"))
		if err != nil {
			return nil, err
		}
		return toAccountViewV2(*a), nil
	},
	"transactionsV2.listAll": func(ctx context.Context, e env, args map[string]any) (any, error) {
		return walkTransactions(ctx, e.scope(args), args)
	},
}

// ---- webhooksig and events modes ---------------------------------------------

func webhooksigMode() int {
	file := os.Getenv("CT_FIXTURE_FILE")
	if file == "" {
		fmt.Fprintln(os.Stderr, "CT_FIXTURE_FILE is required")
		return 1
	}
	var fixture struct {
		VerifyVectors []struct {
			Name    string      `json:"name"`
			Secrets []string    `json:"secrets"`
			Header  string      `json:"header"`
			Body    string      `json:"body"`
			Now     json.Number `json:"now"`
		} `json:"verifyVectors"`
	}
	if err := readJSON(file, &fixture); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	type result struct {
		Name   string               `json:"name"`
		Result partner.VerifyResult `json:"result"`
	}
	results := []result{}
	for _, v := range fixture.VerifyVectors {
		now, err := v.Now.Float64()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		at := time.Unix(int64(now), 0)
		results = append(results, result{v.Name, partner.VerifyAt(v.Secrets, v.Header, []byte(v.Body), at)})
	}
	return emit(struct {
		ProtocolVersion int      `json:"protocolVersion"`
		Driver          identity `json:"driver"`
		Mode            string   `json:"mode"`
		Results         []result `json:"results"`
	}{1, driverIdentity, "webhooksig", results})
}

type knownEventResult struct {
	Name         string                     `json:"name"`
	Kind         string                     `json:"kind"`
	Type         string                     `json:"type"`
	EventID      string                     `json:"eventId"`
	ClientID     string                     `json:"clientId"`
	ConnectionID string                     `json:"connectionId"`
	ReasonCode   *string                    `json:"reasonCode"`
	Extra        map[string]json.RawMessage `json:"extra"`
}

type unknownEventResult struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Type string `json:"type"`
}

type parseErrorResult struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func eventsMode() int {
	file := os.Getenv("CT_FIXTURE_FILE")
	if file == "" {
		fmt.Fprintln(os.Stderr, "CT_FIXTURE_FILE is required")
		return 1
	}
	var fixture struct {
		Vectors []struct {
			Name string `json:"name"`
			Body string `json:"body"`
		} `json:"vectors"`
	}
	if err := readJSON(file, &fixture); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	results := []any{}
	for _, v := range fixture.Vectors {
		parsed, err := partner.ParseEvent([]byte(v.Body))
		switch ev := parsed.(type) {
		case *partner.WebhookEvent:
			var reasonCode *string
			if ev.Reason != nil {
				reasonCode = partner.Ptr(string(ev.Reason.Code))
			}
			results = append(results, knownEventResult{
				v.Name, "event", string(ev.Type), ev.EventID, ev.ClientID, ev.ConnectionID, reasonCode, ev.Extra,
			})
		case *partner.UnknownEvent:
			results = append(results, unknownEventResult{v.Name, "unknown", ev.Type})
		default:
			if err == nil {
				err = errors.New("ParseEvent returned neither an event nor an error")
			}
			fmt.Fprintln(os.Stderr, v.Name+":", err)
			results = append(results, parseErrorResult{v.Name, "parse_error"})
		}
	}
	return emit(struct {
		ProtocolVersion int      `json:"protocolVersion"`
		Driver          identity `json:"driver"`
		Mode            string   `json:"mode"`
		Results         []any    `json:"results"`
	}{1, driverIdentity, "events", results})
}
