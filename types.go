package partner

import "encoding/json"

// API models, hand-written from the Partner API v2 reference. The connection
// lifecycle actions keep their v1.1 shapes. Nullable or optional response
// fields are pointers (nil = null or absent); timestamps are RFC 3339 strings.

// Mode is selected by the API key (bb_test_... / bb_live_...).
type Mode string

const (
	ModeSandbox Mode = "sandbox"
	ModeLive    Mode = "live"
)

// ProviderStatus is the effective visibility of a provider for this partner.
type ProviderStatus string

const (
	ProviderStatusActive   ProviderStatus = "Active"
	ProviderStatusInactive ProviderStatus = "Inactive"
	ProviderStatusHidden   ProviderStatus = "Hidden"
	ProviderStatusDisabled ProviderStatus = "Disabled"
)

// ProviderMode is how the provider is connected.
type ProviderMode string

const (
	ProviderModeAPI ProviderMode = "Api"
	ProviderModeWeb ProviderMode = "Web"
)

type Provider struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Code        *string         `json:"code"`
	CountryCode *string         `json:"countryCode"`
	LogoURL     *string         `json:"logoUrl"`
	BICCodes    []string        `json:"bicCodes"`
	Status      *ProviderStatus `json:"status"`
	Mode        *ProviderMode   `json:"mode"`
	TimeZone    *string         `json:"timeZone"`
}

// Page is one page of a cursor-paginated list. NextCursor is nil on the last page.
type Page[T any] struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"nextCursor"`
	Data       []T     `json:"data"`
}

type (
	ProviderPage    = Page[Provider]
	ClientPage      = Page[Client]
	AccountPage     = Page[Account]
	TransactionPage = Page[Transaction]
)

// Client is one end user of the partner.
type Client struct {
	ID          string  `json:"id"`
	ExternalID  *string `json:"externalId"`
	Email       *string `json:"email"`
	CountryCode *string `json:"countryCode"`
}

type ClientCreateRequest struct {
	Email       string `json:"email"`
	CountryCode string `json:"countryCode"`
	// ExternalID is your own user id; re-posting an existing one returns the existing client.
	ExternalID string `json:"externalId,omitempty"`
}

type ConnectionState string

const (
	ConnectionStatePending  ConnectionState = "Pending"
	ConnectionStateActive   ConnectionState = "Active"
	ConnectionStateInactive ConnectionState = "Inactive"
	ConnectionStateDisabled ConnectionState = "Disabled"
)

type Connection struct {
	ID               string           `json:"id"`
	State            *ConnectionState `json:"state"`
	ProviderID       *string          `json:"providerId"`
	ConsentExpiresAt *string          `json:"consentExpiresAt"`
}

type ConnectionCreateResponse struct {
	ConnectionID string `json:"connectionId"`
	RedirectURL  string `json:"redirectUrl"`
	ExpiresAt    string `json:"expiresAt"`
}

type RefreshAccepted struct {
	Status                string  `json:"status"`
	NextRefreshPossibleAt *string `json:"nextRefreshPossibleAt"`
}

type ConnectSessionState string

const (
	SessionAwaitingBankSelection    ConnectSessionState = "AwaitingBankSelection"
	SessionRedirectedToBank         ConnectSessionState = "RedirectedToBank"
	SessionFetching                 ConnectSessionState = "Fetching"
	SessionAwaitingAccountSelection ConnectSessionState = "AwaitingAccountSelection"
	SessionCompleted                ConnectSessionState = "Completed"
	SessionFailed                   ConnectSessionState = "Failed"
	SessionCancelled                ConnectSessionState = "Cancelled"
	SessionExpired                  ConnectSessionState = "Expired"
)

// IsTerminal reports whether the session can no longer change state.
func (s ConnectSessionState) IsTerminal() bool {
	switch s {
	case SessionCompleted, SessionFailed, SessionCancelled, SessionExpired:
		return true
	}
	return false
}

type ResultCode string

const (
	ResultOk        ResultCode = "Ok"
	ResultError     ResultCode = "Error"
	ResultCancelled ResultCode = "Cancelled"
)

type ConnectSession struct {
	SessionID    string              `json:"sessionId"`
	State        ConnectSessionState `json:"state"`
	ConnectionID *string             `json:"connectionId"`
	ResultCode   *ResultCode         `json:"resultCode"`
	Error        *string             `json:"error"`
}

type ConnectSessionCreateResponse struct {
	// SessionID is opaque; never parse it.
	SessionID string `json:"sessionId"`
	// HostedURL is opened in the user's browser as-is.
	HostedURL string `json:"hostedUrl"`
	ExpiresAt string `json:"expiresAt"`
}

// SubscriptionStatus: Active accounts receive transactions; Disabled ones were
// left unselected in the account picker.
type SubscriptionStatus string

const (
	SubscriptionActive       SubscriptionStatus = "Active"
	SubscriptionUnsubscribed SubscriptionStatus = "Unsubscribed"
	SubscriptionInactive     SubscriptionStatus = "Inactive"
	SubscriptionDisabled     SubscriptionStatus = "Disabled"
)

type Account struct {
	ID                 string             `json:"id"`
	Name               *string            `json:"name"`
	Type               *string            `json:"type"`
	Balance            *Decimal           `json:"balance"`
	CurrencyCode       *string            `json:"currencyCode"`
	IBAN               *string            `json:"iban"`
	SubscriptionStatus SubscriptionStatus `json:"subscriptionStatus"`
}

type EnrichmentCategory struct {
	CategoryID      int64   `json:"categoryId"`
	CategoryName    *string `json:"categoryName"`
	SubcategoryID   *int64  `json:"subcategoryId"`
	SubcategoryName *string `json:"subcategoryName"`
}

type Merchant struct {
	ID      *string `json:"id"`
	Name    *string `json:"name"`
	LogoURI *string `json:"logoUri"`
}

// Enrichment is nil on a transaction when enrichment is disabled for the partner.
type Enrichment struct {
	Category  *EnrichmentCategory `json:"category"`
	Merchant  *Merchant           `json:"merchant"`
	Recurrent bool                `json:"recurrent"`
}

type TransactionDetails struct {
	VariableSymbol       *string                    `json:"variableSymbol"`
	ConstantSymbol       *string                    `json:"constantSymbol"`
	SpecificSymbol       *string                    `json:"specificSymbol"`
	TransactionCode      *string                    `json:"transactionCode"`
	CreditorReference    *string                    `json:"creditorReference"`
	ExternalCategoryName *string                    `json:"externalCategoryName"`
	MCCCode              *string                    `json:"mccCode"`
	Others               map[string]json.RawMessage `json:"others"`
}

type RecordState string

const (
	RecordStateCleared   RecordState = "Cleared"
	RecordStateUncleared RecordState = "Uncleared"
)

type Transaction struct {
	ID string `json:"id"`
	// Seq is the change sequence for SinceSeq delta sync; re-issued on every server-side modification.
	Seq int64 `json:"seq"`
	// CreatedSeq is the insert-order sequence for the SinceCreatedSeq feed; it never changes.
	CreatedSeq   int64               `json:"createdSeq"`
	Amount       *Decimal            `json:"amount"`
	CurrencyCode *string             `json:"currencyCode"`
	RecordState  *RecordState        `json:"recordState"`
	RecordDate   string              `json:"recordDate"`
	Note         *string             `json:"note"`
	CounterParty *string             `json:"counterParty"`
	Enrichment   *Enrichment         `json:"enrichment"`
	Details      *TransactionDetails `json:"details"`
}

type PartnerCapabilities struct {
	Refresh               bool `json:"refresh"`
	Reconnect             bool `json:"reconnect"`
	Enrichment            bool `json:"enrichment"`
	AutoRevokeAfterCreate bool `json:"autoRevokeAfterCreate"`
	NonRegulatedProviders bool `json:"nonRegulatedProviders"`
}

type WebhookConfig struct {
	SignatureVersion string `json:"signatureVersion"`
}

type PartnerConfigResponse struct {
	PartnerID       string              `json:"partnerId"`
	Name            string              `json:"name"`
	Mode            Mode                `json:"mode"`
	Capabilities    PartnerCapabilities `json:"capabilities"`
	ConsentDuration *string             `json:"consentDuration"`
	Countries       []string            `json:"countries"`
	Webhook         WebhookConfig       `json:"webhook"`
}

// TransactionSort orders a transaction listing; an id tiebreak makes every sort total.
type TransactionSort string

const (
	SortRecordDate TransactionSort = "recordDate"
	SortAmount     TransactionSort = "amount"
)

type SortOrder string

const (
	OrderAsc  SortOrder = "asc"
	OrderDesc SortOrder = "desc"
)

// Optional numeric parameters are pointers so an explicit zero is sent; see Ptr.

type ListProvidersParams struct {
	Country string
	Search  string
	Limit   *int
}

type ListAccountsParams struct {
	Limit *int
}

type ListTransactionsParams struct {
	Limit *int
	Sort  TransactionSort
	Order SortOrder
	// DateFrom and DateTo are inclusive UTC calendar-date bounds on recordDate, YYYY-MM-DD.
	DateFrom    string
	DateTo      string
	RecordState RecordState
	// VariableSymbol filters by details.variableSymbol (at most 20 values; leading zeros ignored).
	VariableSymbol []string
	// SinceSeq and SinceCreatedSeq select a delta feed; each combines only with Limit.
	SinceSeq        *int64
	SinceCreatedSeq *int64
}

type ConnectionCreateParams struct {
	ProviderID string
	// IdempotencyKey defaults to a fresh UUID.
	IdempotencyKey string
}

type ReconnectParams struct {
	// IdempotencyKey defaults to a fresh UUID.
	IdempotencyKey string
}

type ConnectSessionCreateParams struct {
	ReturnURL  string
	ProviderID string
	// ConnectionID reconnects an existing connection instead of creating one;
	// the bank picker is then skipped, so do not also set ProviderID.
	ConnectionID string
	// IdempotencyKey defaults to a fresh UUID.
	IdempotencyKey string
}

// Ptr returns a pointer to v, for the optional numeric parameters.
func Ptr[T any](v T) *T { return &v }
