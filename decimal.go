package partner

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Decimal is an exact money amount, never a binary float: two fraction digits
// ("1234.56", "-0.10"), three only when the third carries value ("1.005").
// It decodes from a JSON string or a bare JSON number token without passing
// through float64.
type Decimal string

// ParseDecimal validates a decimal literal (at most three fraction digits, no
// exponent) and returns its canonical form.
func ParseDecimal(s string) (Decimal, error) {
	if strings.ContainsAny(s, "eE") {
		return "", fmt.Errorf("partner: amount with exponent notation is not supported: %q", s)
	}
	bare, negative := strings.CutPrefix(s, "-")
	whole, frac, _ := strings.Cut(bare, ".")
	if !allDigits(whole) || whole == "" || !allDigits(frac) || len(frac) > 3 {
		return "", fmt.Errorf("partner: not a valid amount literal: %q", s)
	}
	switch {
	case len(frac) == 3 && frac[2] == '0':
		frac = frac[:2]
	case len(frac) < 2:
		frac += strings.Repeat("0", 2-len(frac))
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return Decimal(sign + whole + "." + frac), nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FromCents renders an amount in cents as a Decimal.
func FromCents(cents int64) Decimal {
	return fromBigCents(big.NewInt(cents))
}

func fromBigCents(cents *big.Int) Decimal {
	abs := new(big.Int).Abs(cents)
	whole, rem := new(big.Int).QuoRem(abs, big.NewInt(100), new(big.Int))
	sign := ""
	if cents.Sign() < 0 {
		sign = "-"
	}
	return Decimal(fmt.Sprintf("%s%s.%02d", sign, whole.String(), rem.Int64()))
}

// Cents returns the amount in cents. A sub-cent amount ("1.005") has no exact
// cent value and is an error, never rounded.
func (d Decimal) Cents() (int64, error) {
	canonical, err := ParseDecimal(string(d))
	if err != nil {
		return 0, err
	}
	bare, negative := strings.CutPrefix(string(canonical), "-")
	whole, frac, _ := strings.Cut(bare, ".")
	if len(frac) != 2 {
		return 0, fmt.Errorf("partner: sub-cent amount has no exact cent value: %s", d)
	}
	digits := whole + frac
	if negative {
		digits = "-" + digits
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("partner: amount out of range: %s", d)
	}
	return cents, nil
}

// SumDecimals adds amounts exactly, in cents.
func SumDecimals(amounts ...Decimal) (Decimal, error) {
	total := new(big.Int)
	for _, a := range amounts {
		cents, err := a.Cents()
		if err != nil {
			return "", err
		}
		total.Add(total, big.NewInt(cents))
	}
	return fromBigCents(total), nil
}

func (d Decimal) String() string { return string(d) }

// UnmarshalJSON accepts a JSON string, a JSON number token or null (which
// leaves d unchanged).
func (d *Decimal) UnmarshalJSON(data []byte) error {
	literal := string(data)
	switch {
	case literal == "null":
		return nil
	case strings.HasPrefix(literal, `"`):
		if err := json.Unmarshal(data, &literal); err != nil {
			return err
		}
	case literal == "" || (literal[0] != '-' && (literal[0] < '0' || literal[0] > '9')):
		return errors.New("partner: amount must be a JSON string or number")
	}
	parsed, err := ParseDecimal(literal)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalJSON writes the amount as a JSON string.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(d))
}
