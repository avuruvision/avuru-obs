package topology

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseConfigEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\n"} {
		got, err := ParseConfig([]byte(in))
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", in, err)
		}
		if !reflect.DeepEqual(got, Default()) {
			t.Errorf("ParseConfig(%q) = %+v, want Default()", in, got)
		}
	}
}

func TestParseConfigRoundTrip(t *testing.T) {
	got, err := ParseConfig([]byte(`{"transport":["mesh-*"],"applications":["waypoint"]}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !reflect.DeepEqual(got.Transport, []string{"mesh-*"}) {
		t.Errorf("transport = %v", got.Transport)
	}
	if !reflect.DeepEqual(got.Applications, []string{"waypoint"}) {
		t.Errorf("applications = %v", got.Applications)
	}
	// Additions extend the built-ins rather than replacing them.
	patterns := got.TransportPatterns()
	if len(patterns) != len(builtinTransport)+1 {
		t.Errorf("TransportPatterns() length = %d, want %d", len(patterns), len(builtinTransport)+1)
	}
}

// Fail loud, like modules.Parse on a typo: a config that cannot be honoured
// must stop the hub, not run half-applied.
func TestParseConfigRejects(t *testing.T) {
	for name, in := range map[string]string{
		"unknown field":     `{"transports":["x"]}`,
		"malformed json":    `{"transport":`,
		"empty pattern":     `{"transport":["  "]}`,
		"bad glob":          `{"applications":["[unclosed"]}`,
		"disable with none": `{"disableDefaults":true}`,
	} {
		if _, err := ParseConfig([]byte(in)); err == nil {
			t.Errorf("%s: ParseConfig(%q) accepted, want error", name, in)
		}
	}
}

// Every built-in must itself be a valid glob — a typo here would silently
// classify nothing and there is no runtime signal for that.
func TestBuiltinPatternsAreValid(t *testing.T) {
	if err := (Config{Transport: builtinTransport}).Validate(); err != nil {
		t.Fatalf("built-in patterns: %v", err)
	}
	for _, p := range builtinTransport {
		if p != strings.ToLower(p) {
			t.Errorf("built-in %q is not lowercase; matching lowercases the name", p)
		}
	}
}
