# partner-sdk-go

Server SDK for the BudgetBakers Partner AISP API, for Go. Standard library
only; Go 1.24 or newer; module `github.com/budgetbakers/partner-sdk-go`,
package `partner`. Default base URL: `https://aisp-partner.bbapi.io` (the API
key selects sandbox vs live).

## Install

```sh
go get github.com/budgetbakers/partner-sdk-go
```

Until the module is published, download the source zip from the
[downloads page](https://aisp-docs.bbapi.io/guide/downloads), unpack it next
to your module and point the import path at it:

```sh
go mod edit -replace github.com/budgetbakers/partner-sdk-go=../partner-sdk-go
go mod tidy
```

## Example

```go
import partner "github.com/budgetbakers/partner-sdk-go"

bb := partner.New(os.Getenv("BB_API_KEY"))

// Capability discovery (mode = sandbox|live, decided by the key).
config, err := bb.Partner.GetConfig(ctx)

// Clients upsert by ExternalID.
client, err := bb.Clients.Create(ctx, partner.ClientCreateRequest{
	ExternalID: "user-42", Email: "u42@example.com", CountryCode: "CZ",
})

// Client-scoped calls hide the X-Client-Id header.
scope := bb.Client(client.ID)

// Hosted connect flow: open HostedURL in the user's browser, then poll.
session, err := scope.ConnectSessions.Create(ctx, partner.ConnectSessionCreateParams{
	ReturnURL: "https://app.example.com/bb-callback",
})
done, err := scope.ConnectSessions.WaitForTerminal(ctx, session.SessionID)

// Accounts: every page walked (Disabled, unselected accounts included).
accounts, err := scope.Connections.ListAccounts(ctx, *done.ConnectionID)

// Cursor pagination as iterators; filters and delta sync as parameters.
for tx, err := range scope.Accounts.Transactions(ctx, accounts[0].ID, partner.ListTransactionsParams{
	SinceSeq: partner.Ptr(int64(0)),
}) {
	if err != nil {
		return err
	}
	_ = tx.Amount // *partner.Decimal - money is never a float.
}
```

Every list has a `Pages` variant (`Providers.Pages`,
`Connections.AccountPages`, `Accounts.TransactionPages`) when you need the
cursor or the page boundaries.

## Webhook verification

```go
// Constant-time verification against ALL active secrets (+-300 s),
// typed events; unknown types pass through, never fail (respond 2xx).
body, _ := io.ReadAll(r.Body)
if partner.Verify(secrets, r.Header.Get(partner.SignatureHeader), body) != partner.VerifyValid {
	w.WriteHeader(http.StatusUnauthorized)
	return
}
event, err := partner.ParseEvent(body)
```

Verify over the raw request body bytes, before any JSON parsing.

## Behavior

- **Typed errors** - `*partner.APIError` (use `errors.As`) carries `Code`,
  the stable machine code (`error.code`); branch on it, never on messages.
  `RequestID` carries the `X-Request-Id` correlation id. A network failure
  is `*partner.UnreachableError`; a cancelled `context.Context` returns the
  context's error.
- **Retries** - exponential backoff + jitter on 429/5xx honoring
  `Retry-After`; POST retries only under an `Idempotency-Key`
  (auto-UUID on creates, explicit `IdempotencyKey` override). Tune with
  `partner.WithRetryBase` and `partner.WithMaxRetries`.
- **Money** - `partner.Decimal` keeps the exact decimal digits from the wire;
  `Cents` and `SumDecimals` do exact arithmetic, never through a float.
- **Nullability** - only `ID` is guaranteed on Client/Connection/Account
  payloads (plus `SubscriptionStatus` on accounts and `Seq`/`CreatedSeq`/
  `RecordDate` on transactions); every other field is a pointer.
- **Paths** - reads and creates call `/v2` and unwrap the `{"data": ...}`
  envelope; the connection lifecycle actions (`Connections.Create/Delete/
  Refresh/Reconnect/Revoke`) call `/v1`, where they live today.
  `Clients.GetByExternalID` returns `nil, nil` when nothing matches.

## Documentation

Guides and the API reference: <https://aisp-docs.bbapi.io>. Package
documentation: `go doc github.com/budgetbakers/partner-sdk-go`. Questions:
[integration@budgetbakers.com](mailto:integration@budgetbakers.com).

## Licence

Apache-2.0 (see `LICENSE` and `NOTICE`). Access to the Partner API itself is
governed by the BudgetBakers Partner Terms of Service.
