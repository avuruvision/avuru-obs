package modules

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"empty means all", "", []string{"core", "logs", "infra-metrics", "profiling", "error-tracking", "service-health", "alerting", "mesh", "green"}, false},
		{"whitespace means all", "  ", []string{"core", "logs", "infra-metrics", "profiling", "error-tracking", "service-health", "alerting", "mesh", "green"}, false},
		{"explicit subset", "core,logs", []string{"core", "logs"}, false},
		{"core is forced on", "logs", []string{"core", "logs"}, false},
		{"spaces and blanks tolerated", " logs , profiling ,", []string{"core", "logs", "profiling"}, false},
		{"registry order regardless of input order", "profiling,logs", []string{"core", "logs", "profiling"}, false},
		{"unknown name fails loudly", "core,profilling", nil, true},
		{"green with infra-metrics", "green,infra-metrics", []string{"core", "infra-metrics", "green"}, false},
		{"green without infra-metrics fails loudly", "green", nil, true},
		{"green needs infra-metrics even with others", "logs,green", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if !reflect.DeepEqual(got.Names(), tt.want) {
				t.Errorf("Parse(%q) = %v, want %v", tt.in, got.Names(), tt.want)
			}
		})
	}
}

// TestParseGreenDependencyError pins the fail-loud contract: the error must
// name the missing dependency so the operator knows what to add.
func TestParseGreenDependencyError(t *testing.T) {
	_, err := Parse("green")
	if err == nil {
		t.Fatal("Parse(\"green\") should fail without infra-metrics")
	}
	if !strings.Contains(err.Error(), string(InfraMetrics)) {
		t.Errorf("error should name the missing dependency %q: %v", InfraMetrics, err)
	}
}

func TestEnabled(t *testing.T) {
	set, err := Parse("logs")
	if err != nil {
		t.Fatal(err)
	}
	if !set.Enabled(Core) || !set.Enabled(Logs) {
		t.Errorf("core+logs should be enabled: %v", set.Names())
	}
	if set.Enabled(Profiling) || set.Enabled(InfraMetrics) {
		t.Errorf("profiling/infra-metrics should be disabled: %v", set.Names())
	}
}
