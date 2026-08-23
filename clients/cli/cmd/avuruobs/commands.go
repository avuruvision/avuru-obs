package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/clients/cli/internal/client"
	"github.com/avuru/avuru-obs/clients/cli/internal/config"
	"github.com/avuru/avuru-obs/clients/cli/internal/output"
)

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	hubURL := fs.String("url", "", "hub base URL, e.g. https://obs.example.com")
	token := fs.String("token", "", "personal API token (Settings → Access)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hubURL == "" || *token == "" {
		return fmt.Errorf("login needs --url and --token; create a token in Settings → Access")
	}
	if _, err := url.ParseRequestURI(*hubURL); err != nil {
		return fmt.Errorf("--url %q is not a URL: %w", *hubURL, err)
	}
	cfg := config.Config{URL: strings.TrimRight(*hubURL, "/"), Token: *token}

	// Prove the credential before writing it: a config file that stores a
	// token nobody accepts is worse than no config file, because the failure
	// then surfaces at the least convenient moment instead of now.
	c := client.New(cfg, 30*time.Second, "")
	var probe map[string]any
	if err := c.Get(context.Background(), "/api/v1/capabilities", nil, &probe); err != nil {
		return fmt.Errorf("not saving credentials — %w", err)
	}

	path, err := config.Save(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Signed in to %s (version %v). Credentials written to %s\n", cfg.URL, probe["version"], path)
	return nil
}

func cmdServices(args []string) error {
	fs := flag.NewFlagSet("services", flag.ContinueOnError)
	common := addCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := newClient(common)
	if err != nil {
		return err
	}
	var resp struct {
		Services []map[string]any `json:"services"`
	}
	if err := c.Get(context.Background(), "/api/v1/services", common.params(), &resp); err != nil {
		return err
	}
	cols := []output.Column{
		{Header: "SERVICE", Field: "name"},
		{Header: "RATE/S", Field: "ratePerSec", Format: output.Num(2)},
		{Header: "ERRORS", Field: "errorRate", Format: output.Percent},
		{Header: "P50", Field: "p50Ms", Format: output.Num(1)},
		{Header: "P95", Field: "p95Ms", Format: output.Num(1)},
		{Header: "SPANS", Field: "spanCount"},
	}
	return emit(common, resp, cols, resp.Services, "No services reported in this window.", "service")
}

func cmdHealth(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	common := addCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := newClient(common)
	if err != nil {
		return err
	}
	var resp struct {
		Overall  string           `json:"overall"`
		Groups   []map[string]any `json:"groups"`
		Warnings []string         `json:"warnings"`
	}
	if err := c.Get(context.Background(), "/api/v1/health/groups", common.params(), &resp); err != nil {
		return err
	}
	if *common.output != "json" {
		fmt.Printf("Overall: %s\n\n", resp.Overall)
		// Declarations fail soft server-side; a CLI that dropped the warnings
		// would be the one place an operator could never find out why a tier
		// was ignored.
		for _, w := range resp.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
	}
	cols := []output.Column{
		{Header: "GROUP", Field: "name"},
		{Header: "ENV", Field: "environment"},
		{Header: "TIER", Field: "tier"},
		{Header: "STATUS", Field: "status"},
		{Header: "RATE/S", Field: "ratePerSec", Format: output.Num(2)},
		{Header: "ERRORS", Field: "errorRate", Format: output.Percent},
		{Header: "P95", Field: "p95Ms", Format: output.Num(1)},
	}
	return emit(common, resp, cols, resp.Groups, "No service-health groups in this window.", "group")
}

func cmdTraces(args []string) error {
	fs := flag.NewFlagSet("traces", flag.ContinueOnError)
	common := addCommon(fs)
	service := fs.String("service", "", "only traces a given service took part in")
	status := fs.String("status", "", "ok|error")
	limit := fs.Int("limit", 20, "maximum traces to return")
	tags := fs.String("tags", "", "attribute filters, e.g. 'avuru.tag.team=payments'")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := newClient(common)
	if err != nil {
		return err
	}
	params := common.params()
	setIf(params, "service", *service)
	setIf(params, "status", *status)
	setIf(params, "tags", *tags)
	params.Set("limit", strconv.Itoa(*limit))

	var resp struct {
		Traces []map[string]any `json:"traces"`
	}
	if err := c.Get(context.Background(), "/api/v1/traces", params, &resp); err != nil {
		return err
	}
	cols := []output.Column{
		{Header: "TRACE", Field: "traceId"},
		{Header: "SERVICE", Field: "rootService"},
		{Header: "OPERATION", Field: "rootOperation"},
		{Header: "MS", Field: "durationMs", Format: output.Num(1)},
		{Header: "SPANS", Field: "spanCount"},
		{Header: "ERRORS", Field: "errorCount"},
		{Header: "STARTED", Field: "startTime"},
	}
	return emit(common, resp, cols, resp.Traces, "No traces matched.", "trace")
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	common := addCommon(fs)
	service := fs.String("service", "", "only this service's logs")
	severity := fs.String("severity", "", "minimum severity, e.g. WARN")
	query := fs.String("query", "", "case-insensitive substring of the message")
	tags := fs.String("tags", "", "attribute filters, e.g. 'avuru.tag.team=payments'")
	limit := fs.Int("limit", 50, "maximum records to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := newClient(common)
	if err != nil {
		return err
	}
	params := common.params()
	setIf(params, "service", *service)
	setIf(params, "severity", *severity)
	setIf(params, "q", *query)
	setIf(params, "tags", *tags)
	params.Set("limit", strconv.Itoa(*limit))

	var resp struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := c.Get(context.Background(), "/api/v1/logs", params, &resp); err != nil {
		return err
	}
	cols := []output.Column{
		{Header: "TIME", Field: "timestamp"},
		{Header: "SEVERITY", Field: "severity"},
		{Header: "SERVICE", Field: "service"},
		{Header: "TRACE", Field: "traceId"},
		{Header: "MESSAGE", Field: "body"},
	}
	return emit(common, resp, cols, resp.Logs, "No log records matched.", "log record")
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	common := addCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := newClient(common)
	if err != nil {
		return err
	}
	var resp struct {
		Components []map[string]any `json:"components"`
	}
	if err := c.Get(context.Background(), "/api/v1/system/status", nil, &resp); err != nil {
		return err
	}
	cols := []output.Column{
		{Header: "COMPONENT", Field: "name"},
		{Header: "STATUS", Field: "status"},
		{Header: "DETAIL", Field: "detail"},
	}
	return emit(common, resp, cols, resp.Components, "The hub reported no components.", "component")
}

// emit renders a response and applies --fail-on. JSON output is the whole
// response, not the rows the table happens to show, so a script never has to
// choose between machine-readable and complete.
func emit(c commonFlags, full any, cols []output.Column, rows []map[string]any, empty, noun string) error {
	switch *c.output {
	case "json":
		if err := output.JSON(os.Stdout, full); err != nil {
			return err
		}
	case "table":
		if err := output.Table(os.Stdout, cols, rows, empty); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown output format %q — use table or json", *c.output)
	}
	return checkPredicate(*c.failOn, rows, noun)
}

func checkPredicate(expr string, rows []map[string]any, noun string) error {
	if expr == "" {
		return nil
	}
	p, err := output.ParsePredicate(expr)
	if err != nil {
		return err
	}
	var matched []string
	comparable := false
	for _, row := range rows {
		hit, ok := p.Matches(row)
		if ok {
			comparable = true
		}
		if hit {
			matched = append(matched, describe(row))
		}
	}
	// A gate that silently passes because it watched a field nothing has is
	// the worst outcome available — louder than a false alarm, and quieter
	// than it should be.
	if len(rows) > 0 && !comparable {
		return fmt.Errorf("no %s carries a field named %q — check the spelling against `-o json`", noun, p.Field)
	}
	if len(matched) > 0 {
		return &predicateMatched{detail: fmt.Sprintf(
			"%d %s(s) matched %s: %s", len(matched), noun, expr, strings.Join(matched, ", "))}
	}
	return nil
}

// describe names a row for the failure message, preferring the fields people
// actually recognise.
func describe(row map[string]any) string {
	for _, k := range []string{"name", "service", "rootService", "traceId"} {
		if v, ok := row[k]; ok {
			if s := fmt.Sprint(v); s != "" {
				return s
			}
		}
	}
	return "?"
}

func setIf(v url.Values, key, val string) {
	if val != "" {
		v.Set(key, val)
	}
}
