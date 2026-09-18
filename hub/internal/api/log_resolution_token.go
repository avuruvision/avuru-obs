package api

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	logResolutionTokenTTL        = 30 * time.Minute
	maxLogResolutionTokenBytes   = 3072
	maxLogResolutionDecodedBytes = 256 << 10
)

type logResolutionTokenPayload struct {
	Scope       string                         `json:"s"`
	Expires     int64                          `json:"e"`
	Resolutions []logResolutionTokenResolution `json:"r"`
}

type logResolutionTokenResolution struct {
	Service            string                           `json:"s,omitempty"`
	Namespace          string                           `json:"n,omitempty"`
	Workload           string                           `json:"w,omitempty"`
	ProxiesUnavailable string                           `json:"u,omitempty"`
	ProxiesMatchedBy   string                           `json:"m,omitempty"`
	ProxiesFallback    string                           `json:"f,omitempty"`
	Branches           []logResolutionTokenSourceBranch `json:"b"`
}

type logResolutionTokenSourceBranch struct {
	Category string     `json:"c"`
	Services []string   `json:"s"`
	BodyAll  [][]string `json:"b,omitempty"`
}

// encodeLogResolutionToken returns the token and the exact resolutions that
// must be used for the first page. If a large pod snapshot would make the URL
// unsafe, it first switches every proxy branch to the existing stable
// workload+namespace fallback. That keeps page one and later pages identical.
func encodeLogResolutionToken(key []byte, scope string, resolutions []logResolutionDTO, now time.Time) (string, []logResolutionDTO, error) {
	token, decodedBytes, err := marshalLogResolutionToken(key, scope, resolutions, now)
	if err != nil {
		return "", nil, err
	}
	if len(token) <= maxLogResolutionTokenBytes && decodedBytes <= maxLogResolutionDecodedBytes {
		return token, resolutions, nil
	}
	compact := compactLogResolutions(resolutions)
	token, decodedBytes, err = marshalLogResolutionToken(key, scope, compact, now)
	if err != nil {
		return "", nil, err
	}
	if len(token) > maxLogResolutionTokenBytes || decodedBytes > maxLogResolutionDecodedBytes {
		return "", nil, fmt.Errorf("log resolution token is too large; select fewer services or workloads")
	}
	return token, compact, nil
}

func marshalLogResolutionToken(key []byte, scope string, resolutions []logResolutionDTO, now time.Time) (string, int, error) {
	payload := logResolutionTokenPayload{Scope: scope, Expires: now.Add(logResolutionTokenTTL).Unix(), Resolutions: tokenResolutions(resolutions)}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}
	var compressed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return "", 0, err
	}
	if _, err = zw.Write(raw); err != nil {
		return "", 0, err
	}
	if err = zw.Close(); err != nil {
		return "", 0, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(compressed.Bytes())
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), len(raw), nil
}

func decodeLogResolutionToken(key []byte, token, scope string, now time.Time) ([]logResolutionDTO, error) {
	// This token preserves query shape only; it is never an authorization
	// credential. projectTenants is still resolved from the authenticated
	// request and applied independently to every storage query.
	if token == "" || len(token) > maxLogResolutionTokenBytes {
		return nil, errors.New("invalid log resolution token")
	}
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errors.New("invalid log resolution token")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid log resolution token")
	}
	wantMAC, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, errors.New("invalid log resolution token")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(compressed)
	if !hmac.Equal(wantMAC, mac.Sum(nil)) {
		return nil, errors.New("invalid log resolution token")
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("invalid log resolution token")
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxLogResolutionDecodedBytes+1))
	closeErr := zr.Close()
	if err != nil || closeErr != nil || len(raw) > maxLogResolutionDecodedBytes {
		return nil, errors.New("invalid log resolution token")
	}
	var payload logResolutionTokenPayload
	if json.Unmarshal(raw, &payload) != nil || payload.Scope != scope || payload.Expires < now.Unix() {
		return nil, errors.New("invalid or expired log resolution token")
	}
	resolutions := dtoResolutions(payload.Resolutions)
	for _, resolution := range resolutions {
		if resolutionKey(resolution.Service, resolution.Namespace, resolution.Workload) == "" || !validSourceBranches(resolution.SourceBranches) {
			return nil, errors.New("invalid log resolution token")
		}
	}
	return resolutions, nil
}

func deriveLogResolutionKey(secret string) []byte {
	if secret == "" {
		// Register is called directly by API tests and embedders. The shipped
		// cmd/hub always supplies the shared storage credential at minimum.
		secret = "avuruobs-development-resolution-key"
	}
	sum := sha256.Sum256([]byte("avuruobs/log-resolution/v1\x00" + secret))
	return sum[:]
}

func compactLogResolutions(resolutions []logResolutionDTO) []logResolutionDTO {
	out := make([]logResolutionDTO, len(resolutions))
	for i, resolution := range resolutions {
		out[i] = resolution
		out[i].SourceBranches = make([]logSourceBranchDTO, len(resolution.SourceBranches))
		for j, branch := range resolution.SourceBranches {
			out[i].SourceBranches[j] = branch
			if len(branch.BodyAll) > 0 && (branch.Category == logSourceZtunnel || branch.Category == logSourceWaypoint) {
				out[i].SourceBranches[j].BodyAll = fallbackLogBodyAll(resolution.Namespace, resolution.Workload)
				out[i].ProxiesFallback = "proxy logs use workload and namespace matching because the resolved pod set was too large for pagination"
			}
		}
	}
	return out
}

func tokenResolutions(resolutions []logResolutionDTO) []logResolutionTokenResolution {
	out := make([]logResolutionTokenResolution, 0, len(resolutions))
	for _, resolution := range resolutions {
		tokenResolution := logResolutionTokenResolution{Service: resolution.Service, Namespace: resolution.Namespace, Workload: resolution.Workload, ProxiesUnavailable: resolution.ProxiesUnavailable, ProxiesMatchedBy: resolution.ProxiesMatchedBy, ProxiesFallback: resolution.ProxiesFallback, Branches: make([]logResolutionTokenSourceBranch, 0, len(resolution.SourceBranches))}
		for _, branch := range resolution.SourceBranches {
			tokenResolution.Branches = append(tokenResolution.Branches, logResolutionTokenSourceBranch(branch))
		}
		out = append(out, tokenResolution)
	}
	return out
}

func dtoResolutions(resolutions []logResolutionTokenResolution) []logResolutionDTO {
	out := make([]logResolutionDTO, 0, len(resolutions))
	for _, resolution := range resolutions {
		dto := logResolutionDTO{Service: resolution.Service, Namespace: resolution.Namespace, Workload: resolution.Workload, ProxiesUnavailable: resolution.ProxiesUnavailable, ProxiesMatchedBy: resolution.ProxiesMatchedBy, ProxiesFallback: resolution.ProxiesFallback, SourceBranches: make([]logSourceBranchDTO, 0, len(resolution.Branches))}
		for _, branch := range resolution.Branches {
			dto.SourceBranches = append(dto.SourceBranches, logSourceBranchDTO(branch))
		}
		out = append(out, dto)
	}
	return out
}

func validSourceBranches(sources []logSourceBranchDTO) bool {
	for _, source := range sources {
		if len(source.Services) == 0 {
			return false
		}
		switch source.Category {
		case "application", logSourceZtunnel, logSourceWaypoint:
		default:
			return false
		}
	}
	return true
}

func makeLogResolutionScope(tenant string, tenants, services, workloads []string, wanted map[string]bool, trStart, trEnd time.Time) string {
	parts := []string{tenant, trStart.UTC().Format(time.RFC3339Nano), trEnd.UTC().Format(time.RFC3339Nano)}
	for _, values := range [][]string{tenants, services, workloads} {
		copyValues := append([]string(nil), values...)
		sort.Strings(copyValues)
		parts = append(parts, strings.Join(copyValues, "\x1f"))
	}
	for _, source := range []string{logSourceApp, logSourceZtunnel, logSourceWaypoint, logSourceOther} {
		if wanted[source] {
			parts = append(parts, source)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
