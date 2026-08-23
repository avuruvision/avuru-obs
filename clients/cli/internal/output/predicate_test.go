package output

import "testing"

func TestParsePredicate(t *testing.T) {
	tests := []struct {
		in             string
		field, op, val string
		wantErr        bool
	}{
		{in: "errorRate>0.05", field: "errorRate", op: ">", val: "0.05"},
		{in: "p95Ms >= 800", field: "p95Ms", op: ">=", val: "800"},
		// >= must not be read as > followed by =, which would compare against
		// "=800" and silently never match.
		{in: "status!=healthy", field: "status", op: "!=", val: "healthy"},
		{in: "status=healthy", field: "status", op: "==", val: "healthy"},
		{in: "tier==T0", field: "tier", op: "==", val: "T0"},
		{in: "errorRate", wantErr: true},
		{in: ">0.05", wantErr: true},
		{in: "errorRate>", wantErr: true},
	}
	for _, tt := range tests {
		p, err := ParsePredicate(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParsePredicate(%q) = %+v, want an error", tt.in, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePredicate(%q): %v", tt.in, err)
			continue
		}
		if p.Field != tt.field || p.Op != tt.op || p.Value != tt.val {
			t.Errorf("ParsePredicate(%q) = %+v, want {%s %s %s}", tt.in, p, tt.field, tt.op, tt.val)
		}
	}
}

func TestPredicateMatches(t *testing.T) {
	row := map[string]any{
		"name":      "checkout",
		"errorRate": 0.12,
		"p95Ms":     420.0,
		"status":    "degraded",
		"spanCount": float64(9000),
	}
	tests := []struct {
		expr         string
		want, wantOK bool
	}{
		{"errorRate>0.05", true, true},
		{"errorRate>0.5", false, true},
		{"errorRate>=0.12", true, true},
		{"p95Ms<800", true, true},
		{"status!=healthy", true, true},
		{"status==healthy", false, true},
		{"name==checkout", true, true},
		{"spanCount>10000", false, true},
		// The field does not exist: never a match, and never silently OK — a
		// gate watching a misspelled field must not read as "all clear".
		{"erorRate>0.05", false, false},
		// Ordering against a non-numeric value is not a question with an
		// answer; equality still is.
		{"status>healthy", false, false},
	}
	for _, tt := range tests {
		p, err := ParsePredicate(tt.expr)
		if err != nil {
			t.Fatalf("ParsePredicate(%q): %v", tt.expr, err)
		}
		got, ok := p.Matches(row)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%q = (%v, %v), want (%v, %v)", tt.expr, got, ok, tt.want, tt.wantOK)
		}
	}
}
