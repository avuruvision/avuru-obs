package alerting

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Notifier delivers a notification over a channel.
type Notifier interface {
	Send(ctx context.Context, ch Channel, n Notification) error
}

// webhookPayload is the JSON body POSTed to a webhook.
type webhookPayload struct {
	Rule    string `json:"rule"`
	Target  string `json:"target"`
	Kind    string `json:"kind"`   // fired | resolved
	Status  string `json:"status"` // health status
	Reason  string `json:"reason"`
	Tenant  string `json:"tenant"` // project the alert evaluated in (e.g. production/staging)
	FiredAt string `json:"firedAt"`
}

// webhookNotifier POSTs JSON to a channel URL. It is the hub's first
// outbound-to-the-world path, so it is deliberately conservative: an SSRF guard
// (enforced at dial time against the resolved IP, defeating DNS rebinding), a
// hard timeout, capped retries, and HMAC signing when a channel secret is set.
type webhookNotifier struct {
	client     *http.Client
	maxRetries int
}

// NewWebhookNotifier builds a notifier whose dialer refuses private/loopback/
// link-local/metadata targets unless their IP is inside an allow CIDR.
func NewWebhookNotifier(timeout time.Duration, maxRetries int, allow []*net.IPNet) Notifier {
	dialer := &net.Dialer{Timeout: timeout, Control: ssrfControl(allow)}
	return &webhookNotifier{
		client: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{DialContext: dialer.DialContext},
		},
		maxRetries: maxRetries,
	}
}

func (w *webhookNotifier) Send(ctx context.Context, ch Channel, n Notification) error {
	body, err := json.Marshal(webhookPayload{
		Rule: n.Rule, Target: n.Target, Kind: n.Kind,
		Status: n.Status, Reason: n.Reason, Tenant: n.Tenant,
		FiredAt: n.At.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		lastErr = w.post(ctx, ch, body)
		if lastErr == nil {
			return nil
		}
		if !retryable(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("webhook %q failed after %d attempts: %w", ch.Name, w.maxRetries+1, lastErr)
}

// retryError marks a failure worth retrying (network error or 5xx). A 4xx is
// terminal (bad request — retrying won't help).
type retryError struct{ err error }

func (e retryError) Error() string { return e.err.Error() }
func retryable(err error) bool     { _, ok := err.(retryError); return ok }

func (w *webhookNotifier) post(ctx context.Context, ch Channel, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.URL, bytes.NewReader(body))
	if err != nil {
		return err // terminal: a bad URL won't fix itself
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "avuru-obs-alerting")
	if ch.Secret != "" {
		mac := hmac.New(sha256.New, []byte(ch.Secret))
		mac.Write(body)
		req.Header.Set("X-Avuru-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return retryError{err} // network/dial error (incl. SSRF block) — retry
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return retryError{fmt.Errorf("webhook %q: status %d", ch.Name, resp.StatusCode)}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %q: status %d", ch.Name, resp.StatusCode)
	}
	return nil
}

// ssrfControl returns a Dialer.Control that blocks the resolved IP unless it is
// loopback/link-local/private/unspecified (blocked) — checked AFTER DNS
// resolution, so a hostname that rebinds to 169.254.169.254 is still refused.
// An allow CIDR overrides the block (self-hosted receivers on a private net).
func ssrfControl(allow []*net.IPNet) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("unresolved dial host %q", host)
		}
		for _, cidr := range allow {
			if cidr.Contains(ip) {
				return nil
			}
		}
		if blockedIP(ip) {
			return fmt.Errorf("blocked webhook target %s (loopback/link-local/private/metadata)", ip)
		}
		return nil
	}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified()
}
