package partner_test

import (
	"encoding/json"
	"testing"

	partner "github.com/budgetbakers/partner-sdk-go"
)

func TestParseDecimalCanonicalForm(t *testing.T) {
	cases := map[string]partner.Decimal{
		"816":               "816.00",
		"1490.1":            "1490.10",
		"120450.50":         "120450.50",
		"-2500":             "-2500.00",
		"0":                 "0.00",
		"-0.10":             "-0.10",
		"1.005":             "1.005",
		"816.000":           "816.00",
		"90071992547409.93": "90071992547409.93",
	}
	for in, want := range cases {
		got, err := partner.ParseDecimal(in)
		if err != nil {
			t.Errorf("ParseDecimal(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDecimal(%q) = %q, want %q", in, got, want)
		}
		if got.String() != string(want) {
			t.Errorf("ParseDecimal(%q).String() = %q, want %q", in, got.String(), want)
		}
	}
}

func TestParseDecimalRejects(t *testing.T) {
	if _, err := partner.ParseDecimal("1.00"); err != nil {
		t.Fatalf("control ParseDecimal(1.00): %v", err)
	}
	for _, in := range []string{"1e2", "1E2", "1.2345", "abc", "", ".5", "--1", "1,00", "+1.00", " 1.00"} {
		if got, err := partner.ParseDecimal(in); err == nil {
			t.Errorf("ParseDecimal(%q) = %q, want an error", in, got)
		}
	}
}

func TestSumDecimalsExact(t *testing.T) {
	got, err := partner.SumDecimals("0.10", "0.20")
	if err != nil || got != "0.30" {
		t.Fatalf("SumDecimals(0.10, 0.20) = %q, %v, want 0.30", got, err)
	}
	got, err = partner.SumDecimals("0.10", "0.20", "90071992547409.93")
	if err != nil || got != "90071992547410.23" {
		t.Fatalf("sum with the 2^53/100 trap = %q, %v, want 90071992547410.23", got, err)
	}
	got, err = partner.SumDecimals()
	if err != nil || got != "0.00" {
		t.Fatalf("empty sum = %q, %v, want 0.00", got, err)
	}
	if got, err := partner.SumDecimals("1.00", "1.005"); err == nil {
		t.Fatalf("sum with a sub-cent amount = %q, want an error", got)
	}
}

func TestCents(t *testing.T) {
	cases := map[partner.Decimal]int64{
		"1234.56":           123456,
		"-0.10":             -10,
		"0.00":              0,
		"90071992547409.93": 9007199254740993,
	}
	for d, want := range cases {
		got, err := d.Cents()
		if err != nil || got != want {
			t.Errorf("%q.Cents() = %d, %v, want %d", d, got, err, want)
		}
	}
	for _, d := range []partner.Decimal{"1.005", "-0.001", "12.345"} {
		if got, err := d.Cents(); err == nil {
			t.Errorf("%q.Cents() = %d, want an error (sub-cent)", d, got)
		}
	}
}

func TestFromCents(t *testing.T) {
	cases := map[int64]partner.Decimal{
		9007199254740993: "90071992547409.93",
		-10:              "-0.10",
		0:                "0.00",
		5:                "0.05",
		123456:           "1234.56",
	}
	for c, want := range cases {
		if got := partner.FromCents(c); got != want {
			t.Errorf("FromCents(%d) = %q, want %q", c, got, want)
		}
	}
}

func TestDecimalUnmarshalKeepsToken(t *testing.T) {
	cases := []struct {
		raw  string
		want partner.Decimal
	}{
		{`"0.10"`, "0.10"},
		{`0.10`, "0.10"},
		{`"90071992547409.93"`, "90071992547409.93"},
		{`90071992547409.93`, "90071992547409.93"},
		{`"1.005"`, "1.005"},
		{`1.005`, "1.005"},
		{`-2326.00`, "-2326.00"},
	}
	for _, tc := range cases {
		var d partner.Decimal
		if err := json.Unmarshal([]byte(tc.raw), &d); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.raw, err)
			continue
		}
		if d != tc.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tc.raw, d, tc.want)
		}
	}
}

func TestDecimalUnmarshalRejects(t *testing.T) {
	var control partner.Decimal
	if err := json.Unmarshal([]byte(`"1.00"`), &control); err != nil {
		t.Fatalf("control Unmarshal(\"1.00\"): %v", err)
	}
	for _, raw := range []string{`1e2`, `"1e2"`, `"1.2345"`, `"abc"`, `true`, `{}`} {
		var d partner.Decimal
		if err := json.Unmarshal([]byte(raw), &d); err == nil {
			t.Errorf("Unmarshal(%s) = %q, want an error", raw, d)
		}
	}
}

func TestDecimalUnmarshalNull(t *testing.T) {
	d := partner.Decimal("7.00")
	if err := json.Unmarshal([]byte(`null`), &d); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if d != "7.00" {
		t.Fatalf("null changed d to %q", d)
	}

	var tx partner.Transaction
	if err := json.Unmarshal([]byte(`{"id":"t1","amount":null,"recordDate":"2026-09-30"}`), &tx); err != nil {
		t.Fatalf("Unmarshal transaction: %v", err)
	}
	if tx.Amount != nil {
		t.Fatalf("Amount = %q, want nil", *tx.Amount)
	}
}

func TestDecimalMarshalAsString(t *testing.T) {
	out, err := json.Marshal(struct {
		A partner.Decimal  `json:"a"`
		B *partner.Decimal `json:"b"`
	}{A: "90071992547409.93", B: partner.Ptr(partner.Decimal("0.30"))})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != `{"a":"90071992547409.93","b":"0.30"}` {
		t.Fatalf("Marshal = %s", out)
	}
}

func TestDecimalRoundTripThroughTransaction(t *testing.T) {
	raw := `{"id":"t1","seq":12,"createdSeq":3,"amount":90071992547409.93,"recordDate":"2026-09-30"}`
	var tx partner.Transaction
	if err := json.Unmarshal([]byte(raw), &tx); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if tx.Amount == nil || *tx.Amount != "90071992547409.93" {
		t.Fatalf("Amount = %v, want 90071992547409.93 byte-exact", tx.Amount)
	}
	out, err := json.Marshal(*tx.Amount)
	if err != nil || string(out) != `"90071992547409.93"` {
		t.Fatalf("Marshal = %s, %v", out, err)
	}
}
