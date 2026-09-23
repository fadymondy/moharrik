package leads

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// currencyDigits is the number of minor-unit digits for the currencies whose
// count is not 2 (ISO 4217). The web formats with Intl, which agrees.
var currencyDigits = map[string]int{
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	"JPY": 0, "KRW": 0, "CLP": 0, "ISK": 0, "VND": 0, "UGX": 0, "XAF": 0, "XOF": 0,
}

func minorDigits(currency string) int {
	if d, ok := currencyDigits[strings.ToUpper(currency)]; ok {
		return d
	}
	return 2
}

// Symbols a visitor types instead of a code. Only the unambiguous ones: "$"
// alone is read as USD because that is what it means on a form, but a symbol
// the workspace does not use makes the budget a different currency (below).
var currencySymbols = map[string]string{"$": "USD", "US$": "USD", "€": "EUR", "£": "GBP", "¥": "JPY"}

// Arabic currency words and abbreviations seen on inquiry forms in the region.
var currencyWords = map[string]string{
	"ج.م": "EGP", "جنيه": "EGP", "جم": "EGP",
	"ر.س": "SAR", "ريال": "SAR", "رس": "SAR",
	"د.إ": "AED", "درهم": "AED",
	"دولار": "USD",
}

/*
ParseBudget reads what a visitor typed as a budget into minor units of the
workspace currency. ok is false unless the text is ONE amount that is either
unmarked or marked with the workspace's own currency:

	"5000", "5,000", "$5,000", "5k", "1.5m", "USD 12,500.50", "٥٠٠٠"  → an amount
	"5k-10k", "between 5 and 10k", "EGP 50,000" in a USD workspace   → not an amount

A range is not guessed at and a foreign currency is not converted: the deal is
left without a value and the lead keeps the text, which a person can read.
*/
func ParseBudget(text, workspaceCurrency string) (minor int64, ok bool) {
	s := strings.TrimSpace(toASCIIDigits(text))
	if s == "" {
		return 0, false
	}
	ws := strings.ToUpper(strings.TrimSpace(workspaceCurrency))
	currency := ""
	mark := func(c string) bool {
		if currency != "" && currency != c {
			return false // two different currencies
		}
		currency = c
		return true
	}

	// Words and symbols first (longest symbols first so "US$" wins over "$").
	for _, w := range []string{"US$", "$", "€", "£", "¥"} {
		if strings.Contains(s, w) {
			if !mark(currencySymbols[w]) {
				return 0, false
			}
			s = strings.ReplaceAll(s, w, " ")
		}
	}
	for w, c := range currencyWords {
		if strings.Contains(s, w) {
			if !mark(c) {
				return 0, false
			}
			s = strings.ReplaceAll(s, w, " ")
		}
	}

	var number string
	mult := 1.0
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) }) {
		t := strings.Trim(tok, "/.:")
		if t == "" {
			continue
		}
		up := strings.ToUpper(t)
		if len(up) == 3 && isLetters(up) {
			if !mark(up) {
				return 0, false
			}
			continue
		}
		if number != "" {
			// "5 k": a magnitude on its own, straight after the number.
			if mult == 1 && (up == "K" || up == "M") {
				mult = map[string]float64{"K": 1e3, "M": 1e6}[up]
				continue
			}
			return 0, false // a second number, or words: not one amount
		}
		n, m, good := splitMagnitude(t) // 5000, 5,000, 5k, 1.5m
		if !good {
			return 0, false
		}
		number, mult = n, m
	}
	if number == "" {
		return 0, false
	}
	if currency != "" && currency != ws {
		return 0, false
	}

	number = strings.ReplaceAll(number, ",", "")
	if strings.Count(number, ".") > 1 || strings.HasPrefix(number, "-") {
		return 0, false
	}
	v, err := strconv.ParseFloat(number, 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	v *= mult * math.Pow10(minorDigits(ws))
	if v > float64(math.MaxInt64/2) {
		return 0, false
	}
	return int64(math.Round(v)), true
}

// splitMagnitude separates "5k" into ("5", 1000). It refuses anything that is
// not digits, separators and at most one trailing k/m.
func splitMagnitude(t string) (string, float64, bool) {
	mult := 1.0
	switch last := t[len(t)-1]; last {
	case 'k', 'K':
		mult, t = 1e3, t[:len(t)-1]
	case 'm', 'M':
		mult, t = 1e6, t[:len(t)-1]
	}
	if t == "" {
		return "", 0, false
	}
	for _, r := range t {
		if !(r >= '0' && r <= '9') && r != ',' && r != '.' {
			return "", 0, false
		}
	}
	return t, mult, true
}

func isLetters(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// toASCIIDigits maps Arabic-Indic and Eastern Arabic digits, and the Arabic
// thousands and decimal separators, to their ASCII forms.
func toASCIIDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r == '٬':
			b.WriteRune(',')
		case r == '٫':
			b.WriteRune('.')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
