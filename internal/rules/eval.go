package rules

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// text renders an event value the way conditions compare it and templates
// print it: numbers without trailing zeros, booleans as true/false, and a
// missing field as "".
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprint(x)
	}
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Test reports whether one condition holds for data. Text comparisons ignore
// case and surrounding space: "Error" and "error " are the same severity.
func Test(c RuleCondition, data map[string]any) (bool, string) {
	raw := data[c.Field]
	actual := text(raw)
	a := strings.ToLower(strings.TrimSpace(actual))
	want := strings.ToLower(strings.TrimSpace(c.Value))
	switch c.Op {
	case "equals":
		if x, ok := number(raw); ok && isNumber(c.Value) {
			y, _ := strconv.ParseFloat(want, 64)
			return x == y, actual
		}
		return a == want, actual
	case "not_equals":
		ok, _ := Test(RuleCondition{Field: c.Field, Op: "equals", Value: c.Value}, data)
		return !ok, actual
	case "contains":
		return want != "" && strings.Contains(a, want), actual
	case "not_contains":
		return want == "" || !strings.Contains(a, want), actual
	case "starts_with":
		return want != "" && strings.HasPrefix(a, want), actual
	case "ends_with":
		return want != "" && strings.HasSuffix(a, want), actual
	case "gt", "gte", "lt", "lte":
		x, ok := number(raw)
		y, err := strconv.ParseFloat(want, 64)
		if !ok || err != nil {
			return false, actual
		}
		switch c.Op {
		case "gt":
			return x > y, actual
		case "gte":
			return x >= y, actual
		case "lt":
			return x < y, actual
		default:
			return x <= y, actual
		}
	case "in", "not_in":
		found := false
		for _, v := range list(c.Value) {
			if v == a {
				found = true
				break
			}
		}
		if c.Op == "in" {
			return found, actual
		}
		return !found, actual
	case "is_empty":
		return a == "", actual
	case "is_not_empty":
		return a != "", actual
	}
	return false, actual
}

// Evaluate runs every condition and reports whether the rule matches: all of
// them (match "all") or at least one (match "any"). No conditions = matches.
func Evaluate(match string, conds []RuleCondition, data map[string]any) (bool, []RuleConditionResult) {
	results := make([]RuleConditionResult, 0, len(conds))
	passed := 0
	for _, c := range conds {
		ok, actual := Test(c, data)
		if ok {
			passed++
		}
		results = append(results, RuleConditionResult{Field: c.Field, Op: c.Op, Value: c.Value, Actual: clip(actual, 200), Passed: ok})
	}
	if len(conds) == 0 {
		return true, results
	}
	if match == "any" {
		return passed > 0, results
	}
	return passed == len(conds), results
}

var placeholder = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

// Render fills {{field}} placeholders from data. An unknown or missing field
// becomes "" rather than staying as braces a customer would read.
func Render(tpl string, data map[string]any) string {
	return placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		return text(data[name])
	})
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
