// Package alerting turns service-health status into notifications. It is pure
// logic: an Evaluate state machine over a health.Report plus persisted state,
// and a Notifier that delivers. The background ticker and storage live in
// cmd/hub and internal/storage. See design/2026-07-19-alerting.md.
package alerting

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Condition values a rule can fire on, matched against a target's health status.
const (
	WhenDown       = "down"
	WhenDegraded   = "degraded"
	WhenNotHealthy = "not-healthy" // degraded OR down
)

var knownWhen = map[string]bool{WhenDown: true, WhenDegraded: true, WhenNotHealthy: true}

// Duration is a time.Duration that unmarshals from a Go duration string ("5m").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Selector matches a rule to targets. A target is present if its name is in the
// matching list; Tiers matches synthetic tier targets (worst-of the tier's
// groups). Empty selector matches nothing.
type Selector struct {
	Groups   []string `json:"groups,omitempty"`
	Services []string `json:"services,omitempty"`
	Tiers    []string `json:"tiers,omitempty"`
}

func (s Selector) empty() bool {
	return len(s.Groups) == 0 && len(s.Services) == 0 && len(s.Tiers) == 0
}

// Rule fires Channel when a matched target's health matches When for For.
type Rule struct {
	Name     string   `json:"name"`
	Selector Selector `json:"selector"`
	When     string   `json:"when"`
	For      Duration `json:"for"`
	Channel  string   `json:"channel"`
}

// Channel is a delivery destination. Only "webhook" is supported in v1.
type Channel struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

// Config is the whole alerting configuration, loaded from the mounted ConfigMap
// (AVURUOPS_ALERTS_CONFIG) or Default() when absent.
type Config struct {
	EvalIntervalSec int       `json:"evalIntervalSec,omitempty"`
	WindowMinutes   int       `json:"windowMinutes,omitempty"`
	Rules           []Rule    `json:"rules,omitempty"`
	Channels        []Channel `json:"channels,omitempty"`
}

const (
	defaultEvalIntervalSec = 30
	defaultWindowMinutes   = 5
)

// Default is the zero-config configuration: no rules (inert), 30s evaluation
// over a 5-minute window. Mirrors an empty AVURUOPS_ALERTS_CONFIG.
func Default() Config {
	return Config{EvalIntervalSec: defaultEvalIntervalSec, WindowMinutes: defaultWindowMinutes}
}

// ParseConfig unmarshals + validates fail-loud (a bad config crashes the hub at
// startup, like modules.Parse). Empty input yields Default().
func ParseConfig(data []byte) (Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parsing alerting config: %w", err)
	}
	if c.EvalIntervalSec == 0 {
		c.EvalIntervalSec = defaultEvalIntervalSec
	}
	if c.WindowMinutes == 0 {
		c.WindowMinutes = defaultWindowMinutes
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects the mistakes that would otherwise misfire or silently drop
// alerts: bad intervals, unknown conditions, empty selectors, and rules
// referencing an undeclared or non-webhook channel with a bad URL.
func (c Config) Validate() error {
	if c.EvalIntervalSec < 0 || c.WindowMinutes < 0 {
		return fmt.Errorf("evalIntervalSec and windowMinutes must be non-negative")
	}
	channels := map[string]bool{}
	for i, ch := range c.Channels {
		if strings.TrimSpace(ch.Name) == "" {
			return fmt.Errorf("channel #%d has an empty name", i)
		}
		if channels[ch.Name] {
			return fmt.Errorf("duplicate channel name %q", ch.Name)
		}
		channels[ch.Name] = true
		if ch.Type != "webhook" {
			return fmt.Errorf("channel %q has unsupported type %q (only webhook)", ch.Name, ch.Type)
		}
		if err := validateWebhookURL(ch.URL); err != nil {
			return fmt.Errorf("channel %q: %w", ch.Name, err)
		}
	}
	seen := map[string]bool{}
	for i, r := range c.Rules {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("rule #%d has an empty name", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("duplicate rule name %q", r.Name)
		}
		seen[r.Name] = true
		if !knownWhen[r.When] {
			return fmt.Errorf("rule %q has invalid when %q (down|degraded|not-healthy)", r.Name, r.When)
		}
		if r.Selector.empty() {
			return fmt.Errorf("rule %q has an empty selector", r.Name)
		}
		if r.For < 0 {
			return fmt.Errorf("rule %q has a negative for", r.Name)
		}
		if !channels[r.Channel] {
			return fmt.Errorf("rule %q references undeclared channel %q", r.Name, r.Channel)
		}
	}
	return nil
}

func validateWebhookURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url %q must be http(s)", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("url %q has no host", raw)
	}
	return nil
}

// Interval returns the evaluation tick.
func (c Config) Interval() time.Duration { return time.Duration(c.EvalIntervalSec) * time.Second }

// Window returns the trailing health window.
func (c Config) Window() time.Duration { return time.Duration(c.WindowMinutes) * time.Minute }

// channelByName indexes channels for delivery.
func (c Config) channelByName() map[string]Channel {
	m := make(map[string]Channel, len(c.Channels))
	for _, ch := range c.Channels {
		m[ch.Name] = ch
	}
	return m
}
