package alerting

import (
	"testing"
	"time"
)

func TestParseConfigEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "  ", "\n"} {
		got, err := ParseConfig([]byte(in))
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", in, err)
		}
		if got.EvalIntervalSec != 30 || got.WindowMinutes != 5 || len(got.Rules) != 0 {
			t.Errorf("ParseConfig(%q) = %+v, want default inert config", in, got)
		}
	}
}

func TestParseConfigFailLoud(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"unknown when", `{"channels":[{"name":"c","type":"webhook","url":"https://x"}],"rules":[{"name":"r","when":"flapping","selector":{"groups":["g"]},"channel":"c"}]}`},
		{"empty selector", `{"channels":[{"name":"c","type":"webhook","url":"https://x"}],"rules":[{"name":"r","when":"down","selector":{},"channel":"c"}]}`},
		{"empty rule channel", `{"rules":[{"name":"r","when":"down","selector":{"groups":["g"]},"channel":""}]}`},
		{"non-webhook channel", `{"channels":[{"name":"c","type":"slack","url":"https://x"}]}`},
		{"bad url", `{"channels":[{"name":"c","type":"webhook","url":"ftp://x"}]}`},
		{"empty url", `{"channels":[{"name":"c","type":"webhook","url":""}]}`},
		{"dup rule name", `{"channels":[{"name":"c","type":"webhook","url":"https://x"}],"rules":[{"name":"r","when":"down","selector":{"groups":["a"]},"channel":"c"},{"name":"r","when":"down","selector":{"groups":["b"]},"channel":"c"}]}`},
		{"dup channel name", `{"channels":[{"name":"c","type":"webhook","url":"https://x"},{"name":"c","type":"webhook","url":"https://y"}]}`},
		{"bad duration", `{"channels":[{"name":"c","type":"webhook","url":"https://x"}],"rules":[{"name":"r","when":"down","for":"5 apples","selector":{"groups":["g"]},"channel":"c"}]}`},
		{"unknown field", `{"bogus":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tt.json)); err == nil {
				t.Errorf("ParseConfig(%s) expected error, got nil", tt.json)
			}
		})
	}
}

func TestParseConfigValid(t *testing.T) {
	const cfg = `{
	  "evalIntervalSec": 15,
	  "windowMinutes": 10,
	  "channels": [{"name":"ops","type":"webhook","url":"https://hooks.example.com/x","secret":"s3cr3t"}],
	  "rules": [
	    {"name":"payments-down","when":"down","for":"5m","selector":{"groups":["payments"]},"channel":"ops"},
	    {"name":"t0-degraded","when":"not-healthy","selector":{"tiers":["T0"]},"channel":"ops"},
	    {"name":"ui-channel-ref","when":"down","selector":{"groups":["payments"]},"channel":"p1"}
	  ]
	}`
	c, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.Interval() != 15*time.Second || c.Window() != 10*time.Minute {
		t.Errorf("interval/window = %v/%v", c.Interval(), c.Window())
	}
	if len(c.Rules) != 3 || c.Rules[0].For.Std() != 5*time.Minute {
		t.Errorf("rules parsed wrong: %+v", c.Rules)
	}
	// A rule may reference a channel not declared in the file — channels can
	// be UI-managed and are resolved at delivery time.
	if c.Rules[2].Channel != "p1" {
		t.Errorf("ui-managed channel reference should parse, got %+v", c.Rules[2])
	}
	if ch, ok := c.ChannelByName("ops"); !ok || ch.Secret != "s3cr3t" {
		t.Errorf("channel not found")
	}
}
