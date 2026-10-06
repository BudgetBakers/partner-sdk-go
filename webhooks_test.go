package partner_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	partner "github.com/budgetbakers/partner-sdk-go"
)

const fixtures = "../../contract-tests/fixtures"

// The vectors live in the monorepo only; a standalone module tree (mirror,
// source zip) skips these tests instead of failing.
func readFixture(t *testing.T, name string, v any) {
	t.Helper()
	if _, err := os.Stat(fixtures); errors.Is(err, os.ErrNotExist) {
		t.Skipf("contract-tests fixtures not present at %s (standalone module tree)", fixtures)
	}
	raw, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
}

type sigFixture struct {
	Header           string `json:"header"`
	ToleranceSeconds int    `json:"toleranceSeconds"`
	SignVectors      []struct {
		Name           string `json:"name"`
		Secret         string `json:"secret"`
		Timestamp      int64  `json:"timestamp"`
		Body           string `json:"body"`
		ExpectedHeader string `json:"expectedHeader"`
	} `json:"signVectors"`
	VerifyVectors []struct {
		Name    string   `json:"name"`
		Secrets []string `json:"secrets"`
		Header  string   `json:"header"`
		Body    string   `json:"body"`
		Now     int64    `json:"now"`
		Expect  string   `json:"expect"`
	} `json:"verifyVectors"`
}

func TestWebhookConstantsMatchFixture(t *testing.T) {
	var fx sigFixture
	readFixture(t, "webhooksig.json", &fx)
	if partner.SignatureHeader != fx.Header {
		t.Errorf("SignatureHeader = %q, want %q", partner.SignatureHeader, fx.Header)
	}
	if partner.Tolerance != time.Duration(fx.ToleranceSeconds)*time.Second {
		t.Errorf("Tolerance = %v, want %ds", partner.Tolerance, fx.ToleranceSeconds)
	}
}

func TestSignVectors(t *testing.T) {
	var fx sigFixture
	readFixture(t, "webhooksig.json", &fx)
	if len(fx.SignVectors) == 0 {
		t.Fatal("no signVectors")
	}
	for _, v := range fx.SignVectors {
		t.Run(v.Name, func(t *testing.T) {
			if got := partner.Sign(v.Secret, v.Timestamp, []byte(v.Body)); got != v.ExpectedHeader {
				t.Fatalf("Sign = %q, want %q", got, v.ExpectedHeader)
			}
		})
	}
}

func TestVerifyVectors(t *testing.T) {
	var fx sigFixture
	readFixture(t, "webhooksig.json", &fx)
	if len(fx.VerifyVectors) == 0 {
		t.Fatal("no verifyVectors")
	}
	for _, v := range fx.VerifyVectors {
		t.Run(v.Name, func(t *testing.T) {
			got := partner.VerifyAt(v.Secrets, v.Header, []byte(v.Body), time.Unix(v.Now, 0))
			if got != partner.VerifyResult(v.Expect) {
				t.Fatalf("VerifyAt = %q, want %q", got, v.Expect)
			}
		})
	}
}

func TestVerifyUsesCurrentTime(t *testing.T) {
	secret := "whsec_test_c8d2e4f6a8b0c2d4e6f8a0b2c4d6e8f0"
	body := []byte(`{"type":"ConnectionDeleted"}`)
	fresh := partner.Sign(secret, time.Now().Unix(), body)
	if got := partner.Verify([]string{secret}, fresh, body); got != partner.VerifyValid {
		t.Fatalf("Verify(fresh) = %q, want valid", got)
	}
	stale := partner.Sign(secret, time.Now().Add(-time.Hour).Unix(), body)
	if got := partner.Verify([]string{secret}, stale, body); got != partner.VerifyTimestampOutOfTolerance {
		t.Fatalf("Verify(stale) = %q, want timestamp_out_of_tolerance", got)
	}
}

type eventVector struct {
	Name   string `json:"name"`
	Body   string `json:"body"`
	Expect struct {
		Kind         string                     `json:"kind"`
		Type         *string                    `json:"type"`
		EventID      *string                    `json:"eventId"`
		ClientID     *string                    `json:"clientId"`
		ConnectionID *string                    `json:"connectionId"`
		ReasonCode   json.RawMessage            `json:"reasonCode"`
		Extra        map[string]json.RawMessage `json:"extra"`
	} `json:"expect"`
}

func TestParseEventVectors(t *testing.T) {
	var fx struct {
		Vectors []eventVector `json:"vectors"`
	}
	readFixture(t, "events.json", &fx)
	if len(fx.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range fx.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			ev, err := partner.ParseEvent([]byte(v.Body))
			switch v.Expect.Kind {
			case "parse_error":
				var pe *partner.WebhookParseError
				if !errors.As(err, &pe) {
					t.Fatalf("err = %v, want *WebhookParseError", err)
				}
				if pe.Error() == "" {
					t.Fatal("parse error has no message")
				}
			case "unknown":
				if err != nil {
					t.Fatalf("err = %v, want nil for an unknown type", err)
				}
				u, ok := ev.(*partner.UnknownEvent)
				if !ok {
					t.Fatalf("event = %T, want *UnknownEvent", ev)
				}
				if v.Expect.Type != nil && (u.Type != *v.Expect.Type || u.EventType() != *v.Expect.Type) {
					t.Fatalf("type = %q / %q, want %q", u.Type, u.EventType(), *v.Expect.Type)
				}
				if len(u.Raw) == 0 {
					t.Fatal("Raw is empty")
				}
			case "event":
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				e, ok := ev.(*partner.WebhookEvent)
				if !ok {
					t.Fatalf("event = %T, want *WebhookEvent", ev)
				}
				x := v.Expect
				if x.Type != nil && (string(e.Type) != *x.Type || e.EventType() != *x.Type) {
					t.Errorf("type = %q, want %q", e.Type, *x.Type)
				}
				if x.EventID != nil && e.EventID != *x.EventID {
					t.Errorf("eventId = %q, want %q", e.EventID, *x.EventID)
				}
				if x.ClientID != nil && e.ClientID != *x.ClientID {
					t.Errorf("clientId = %q, want %q", e.ClientID, *x.ClientID)
				}
				if x.ConnectionID != nil && e.ConnectionID != *x.ConnectionID {
					t.Errorf("connectionId = %q, want %q", e.ConnectionID, *x.ConnectionID)
				}
				if e.CreatedAt == "" {
					t.Errorf("createdAt is empty")
				}
				if len(x.ReasonCode) > 0 {
					if string(x.ReasonCode) == "null" {
						if e.Reason != nil {
							t.Errorf("reason = %+v, want nil", e.Reason)
						}
					} else {
						var want string
						_ = json.Unmarshal(x.ReasonCode, &want)
						if e.Reason == nil || string(e.Reason.Code) != want {
							t.Errorf("reason = %+v, want code %q", e.Reason, want)
						}
					}
				}
				for k, want := range x.Extra {
					got, ok := e.Extra[k]
					if !ok || string(got) != string(want) {
						t.Errorf("extra[%s] = %s, want %s", k, got, want)
					}
				}
			default:
				t.Fatalf("unknown expected kind %q", v.Expect.Kind)
			}
		})
	}
}

func TestParseEventEdgeCases(t *testing.T) {
	for _, body := range []string{``, `[1,2]`, `"str"`, `null`} {
		var pe *partner.WebhookParseError
		if _, err := partner.ParseEvent([]byte(body)); !errors.As(err, &pe) {
			t.Errorf("ParseEvent(%q) err = %v, want *WebhookParseError", body, err)
		}
	}
	ev, err := partner.ParseEvent([]byte(`{"type":123}`))
	if err != nil {
		t.Fatalf("non-string type: err = %v, want nil", err)
	}
	if _, ok := ev.(*partner.UnknownEvent); !ok {
		t.Fatalf("non-string type: event = %T, want *UnknownEvent", ev)
	}
}
