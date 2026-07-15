package modules

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"empty means all", "", []string{"core", "logs", "infra-metrics", "profiling"}, false},
		{"whitespace means all", "  ", []string{"core", "logs", "infra-metrics", "profiling"}, false},
		{"explicit subset", "core,logs", []string{"core", "logs"}, false},
		{"core is forced on", "logs", []string{"core", "logs"}, false},
		{"spaces and blanks tolerated", " logs , profiling ,", []string{"core", "logs", "profiling"}, false},
		{"registry order regardless of input order", "profiling,logs", []string{"core", "logs", "profiling"}, false},
		{"unknown name fails loudly", "core,profilling", nil, true},
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
