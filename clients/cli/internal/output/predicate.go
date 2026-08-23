// Package output turns API rows into something a person or a pipeline reads.
package output

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Predicate is one comparison against a field of each row — the CI gate.
//
// One comparison, not an expression language: a gate that needs boolean algebra
// is a gate whose failure will be hard to read at 3am in a CI log, and two
// `--fail-on` runs express the same thing legibly. If that turns out to be
// wrong, the parser is the only thing that has to change.
type Predicate struct {
	Field string
	Op    string
	Value string
}

// Operators are matched longest-first so >= is never read as > followed by =.
var operators = []string{">=", "<=", "!=", "==", ">", "<", "="}

// ParsePredicate reads `field<op>value`, e.g. `errorRate>0.05`.
func ParsePredicate(s string) (Predicate, error) {
	s = strings.TrimSpace(s)
	for _, op := range operators {
		if i := strings.Index(s, op); i > 0 {
			p := Predicate{
				Field: strings.TrimSpace(s[:i]),
				Op:    op,
				Value: strings.TrimSpace(s[i+len(op):]),
			}
			if p.Op == "=" {
				p.Op = "=="
			}
			if p.Field == "" || p.Value == "" {
				return Predicate{}, fmt.Errorf("predicate %q needs a field and a value, e.g. errorRate>0.05", s)
			}
			return p, nil
		}
	}
	return Predicate{}, fmt.Errorf("predicate %q has no comparison — use one of >= <= != == > <, e.g. errorRate>0.05", s)
}

// Matches reports whether the predicate holds for one row.
//
// A row missing the field never matches, and says so through ok=false: a gate
// silently passing because it was watching a field that does not exist is the
// worst outcome available, so the caller turns that into an error rather than a
// green build.
func (p Predicate) Matches(row map[string]any) (matched bool, ok bool) {
	v, present := row[p.Field]
	if !present {
		return false, false
	}
	if num, isNum := toFloat(v); isNum {
		want, err := strconv.ParseFloat(p.Value, 64)
		if err != nil {
			// A numeric field compared against a non-number: only equality
			// questions are meaningful, and both answers are honest.
			s := fmt.Sprint(v)
			return compareStrings(s, p.Op, p.Value), p.Op == "==" || p.Op == "!="
		}
		return compareFloats(num, p.Op, want), true
	}
	return compareStrings(fmt.Sprint(v), p.Op, p.Value), p.Op == "==" || p.Op == "!="
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func compareFloats(got float64, op string, want float64) bool {
	switch op {
	case ">":
		return got > want
	case ">=":
		return got >= want
	case "<":
		return got < want
	case "<=":
		return got <= want
	case "==":
		return got == want
	case "!=":
		return got != want
	}
	return false
}

func compareStrings(got, op, want string) bool {
	switch op {
	case "==":
		return got == want
	case "!=":
		return got != want
	}
	return false
}
