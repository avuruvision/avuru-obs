//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

const sentryURL = "http://localhost:4319"

type errorIssue struct {
	Fingerprint string `json:"fingerprint"`
	Service     string `json:"service"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Count       int    `json:"count"`
}

type errorIssuesResp struct {
	Issues []errorIssue `json:"issues"`
}

func issuesQuery() string {
	now := time.Now().UTC()
	q := url.Values{}
	q.Set("start", now.Add(-2*time.Hour).Format(time.RFC3339))
	q.Set("end", now.Add(time.Hour).Format(time.RFC3339))
	q.Set("status", "all")
	return "/api/v1/errors/issues?" + q.Encode()
}

func TestDerivedIssues(t *testing.T) {
	want := map[string]string{
		"java.lang.NullPointerException": "log",
		"ConnectionError":                "span",
	}
	poll(t, 30*time.Second, func() error {
		var resp errorIssuesResp
		if err := getJSON(issuesQuery(), &resp); err != nil {
			return err
		}
		got := map[string]string{}
		for _, i := range resp.Issues {
			got[i.Type] = i.Source
		}
		for typ, src := range want {
			if got[typ] != src {
				return fmt.Errorf("issue %q (source %q) not derived yet; have %v", typ, src, got)
			}
		}
		return nil
	})
}

func TestSentryEnvelopeIngest(t *testing.T) {
	payload := `{"event_id":"aaaabbbbccccddddeeeeffff00001111","platform":"javascript","level":"error",` +
		`"environment":"production","sdk":{"name":"sentry.javascript.browser","version":"7.100.0"},` +
		`"exception":{"values":[{"type":"E2ESentryError","value":"envelope path works",` +
		`"stacktrace":{"frames":[{"filename":"app.js","function":"main","lineno":1,"colno":1}]}}]}}`
	envelope := fmt.Sprintf("{\"event_id\":\"aaaabbbbccccddddeeeeffff00001111\"}\n{\"type\":\"event\",\"length\":%d}\n%s", len(payload), payload)

	resp, err := http.Post(sentryURL+"/api/1/envelope/?sentry_key=e2e", "application/x-sentry-envelope", bytes.NewReader([]byte(envelope)))
	if err != nil {
		t.Fatalf("POST envelope: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope POST: got %s, want 200", resp.Status)
	}

	poll(t, 30*time.Second, func() error {
		var r errorIssuesResp
		if err := getJSON(issuesQuery(), &r); err != nil {
			return err
		}
		for _, i := range r.Issues {
			if i.Type == "E2ESentryError" {
				if i.Source != "sentry" {
					return fmt.Errorf("issue source = %q, want sentry", i.Source)
				}
				if i.Service != "sentry-demo" {
					return fmt.Errorf("service = %q, want sentry-demo (from project config)", i.Service)
				}
				return nil
			}
		}
		return fmt.Errorf("sentry-sourced issue not derived yet")
	})
}

func TestTriageStatus(t *testing.T) {
	var resp errorIssuesResp
	poll(t, 30*time.Second, func() error {
		if err := getJSON(issuesQuery(), &resp); err != nil {
			return err
		}
		if len(resp.Issues) == 0 {
			return fmt.Errorf("no issues yet")
		}
		return nil
	})
	fp := resp.Issues[0].Fingerprint

	body := bytes.NewReader([]byte(`{"status":"resolved"}`))
	r, err := http.Post(hubURL+"/api/v1/errors/issues/"+fp+"/status", "application/json", body)
	if err != nil {
		t.Fatalf("POST status: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status POST: got %s, want 200", r.Status)
	}

	var issue errorIssue
	if err := getJSON("/api/v1/errors/issues/"+fp, &issue); err != nil {
		t.Fatalf("get issue after resolve: %v", err)
	}
	if issue.Status != "resolved" {
		t.Errorf("status = %q after resolve, want resolved", issue.Status)
	}
}
