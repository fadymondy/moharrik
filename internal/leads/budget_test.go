package leads

import "testing"

func TestParseBudgetReadsOneAmountInTheWorkspaceCurrency(t *testing.T) {
	for _, c := range []struct {
		in, ws string
		want   int64
		ok     bool
	}{
		{"5000", "USD", 500000, true},
		{"5,000", "USD", 500000, true},
		{"$5,000", "USD", 500000, true},
		{"USD 12,500.50", "USD", 1250050, true},
		{"12500 usd", "USD", 1250000, true},
		{"5k", "USD", 500000, true},
		{"5 K", "USD", 500000, true},
		{"1.5m", "EGP", 150000000, true},
		{"٥٠٬٠٠٠ ج.م", "EGP", 5000000, true},
		{"50000 جنيه", "EGP", 5000000, true},
		{"1000", "KWD", 1000000, true},
		{"1000", "JPY", 1000, true},
		{"€900", "EUR", 90000, true},
		// Not one amount, or not in the workspace's currency: no value, never a guess.
		{"", "USD", 0, false},
		{"5k-10k", "USD", 0, false},
		{"between 5 and 10k", "USD", 0, false},
		{"EGP 50,000", "USD", 0, false},
		{"$5,000", "EGP", 0, false},
		{"USD 5000 EUR", "USD", 0, false},
		{"0", "USD", 0, false},
		{"-5", "USD", 0, false},
		{"lots", "USD", 0, false},
		{"1.2.3", "USD", 0, false},
	} {
		got, ok := ParseBudget(c.in, c.ws)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseBudget(%q, %s) = %d, %v; want %d, %v", c.in, c.ws, got, ok, c.want, c.ok)
		}
	}
}
