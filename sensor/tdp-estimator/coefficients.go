package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Coefficients is one node's resolved power-curve inputs plus provenance —
// Tier/Provenance ride into the metric's resource attributes (metrics.go) so
// the hub and the CSRD export can cite per-node sourcing without a
// side-channel (AEP §Coefficients).
type Coefficients struct {
	IdleWatts, MaxWatts float64
	Tier                string // "annotation" | "values" | "table" | "fallback"
	Provenance          string
}

// archPatterns maps a raw /proc/cpuinfo "model name" substring pattern to an
// archCoefficients key. Matching is heuristic (CPU marketing model numbers,
// not codenames, appear in /proc/cpuinfo) and deliberately conservative: an
// unrecognized model correctly falls through to the generic fallback tier
// rather than guessing — this list is NOT exhaustive of every SKU ever
// released, only the common families the bundled table covers.
//
// AMD EPYC generations 1-3 (the "7xxx" model-number space) are deliberately
// NOT matched here by numeric range: AMD reused overlapping 4-digit ranges
// across Naples/Rome/Milan (e.g. "7301" is 1st-gen while "7302" is 2nd-gen —
// verified via public sources, not a typo), so a range regex would silently
// misattribute many real SKUs. See epycSKUGeneration below for the
// exact-match table used instead.
var archPatterns = []struct {
	re  *regexp.Regexp
	key string
}{
	{regexp.MustCompile(`Xeon\(R\)\s+(?:Platinum|Gold)\s+8[0-3]\d\d`), "CASCADE_LAKE"}, // Platinum 82xx/83xx, Gold 80xx-83xx
	{regexp.MustCompile(`Xeon\(R\)\s+(?:Platinum|Gold)\s+6[23]\d\d`), "SKYLAKE"},       // Gold/Platinum 62xx/63xx
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v4`), "BROADWELL"},             // E5-26xx v4
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v3`), "HASWELL"},               // E5-26xx v3
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v2`), "IVY_BRIDGE"},            // E5-26xx v2
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+0\s+@`), "SANDY_BRIDGE"},       // E5-26xx (no v-suffix)
	{regexp.MustCompile(`Xeon\(R\)\s+Platinum\s+84\d\d`), "SAPPHIRE_RAPIDS"},           // Platinum 84xx
	{regexp.MustCompile(`Xeon\(R\)\s+6\d\d\d[NP]?\s*$`), "ICELAKE"},                    // Gen-3 Xeon Scalable "6xxx" (bare-metal naming)
	{regexp.MustCompile(`EPYC\s+9[0-9]{3}`), "AMD_EPYC_4TH_GEN"},                       // EPYC 9004 series (Genoa) — the leading "9" doesn't overlap the 7xxx space, safe as a range
	{regexp.MustCompile(`Graviton2`), "AWS_GRAVITON_2"},
	{regexp.MustCompile(`Graviton3E`), "AWS_GRAVITON_3E"},
	{regexp.MustCompile(`Graviton3`), "AWS_GRAVITON_3"},
	{regexp.MustCompile(`Graviton4`), "AWS_GRAVITON_4"},
	{regexp.MustCompile(`Apple\s+M\d`), "APPLE"},
}

// epycSKUNumber extracts the 4-digit model number after "EPYC" (e.g. "7551"
// from "AMD EPYC 7551 32-Core Processor"), ignoring any letter suffix.
var epycSKUNumber = regexp.MustCompile(`EPYC\s+(7[0-9]{3})`)

// epycSKUGeneration is an EXACT-match lookup (never a numeric range — see
// archPatterns' comment on why) from a specific, publicly-documented EPYC
// 7xxx-series SKU number to its generation. Deliberately small and
// non-exhaustive: an unlisted SKU falls through to the generic fallback tier
// rather than guessing at a range that's known to overlap across
// generations. Verified against public sources (2026-07-30): 7551 =
// 1st-gen/Naples, 7742 = 2nd-gen/Rome, 7763 = 3rd-gen/Milan.
var epycSKUGeneration = map[string]string{
	"7551": "AMD_EPYC_1ST_GEN",
	"7601": "AMD_EPYC_1ST_GEN",
	"7501": "AMD_EPYC_1ST_GEN",
	"7401": "AMD_EPYC_1ST_GEN",
	"7351": "AMD_EPYC_1ST_GEN",
	"7301": "AMD_EPYC_1ST_GEN",
	"7251": "AMD_EPYC_1ST_GEN",
	"7742": "AMD_EPYC_2ND_GEN",
	"7702": "AMD_EPYC_2ND_GEN",
	"7552": "AMD_EPYC_2ND_GEN",
	"7502": "AMD_EPYC_2ND_GEN",
	"7452": "AMD_EPYC_2ND_GEN",
	"7402": "AMD_EPYC_2ND_GEN",
	"7302": "AMD_EPYC_2ND_GEN",
	"7763": "AMD_EPYC_3RD_GEN",
	"7713": "AMD_EPYC_3RD_GEN",
	"7543": "AMD_EPYC_3RD_GEN",
	"7513": "AMD_EPYC_3RD_GEN",
	"7453": "AMD_EPYC_3RD_GEN",
	"7413": "AMD_EPYC_3RD_GEN",
	"7313": "AMD_EPYC_3RD_GEN",
}

// matchArchitecture maps a raw /proc/cpuinfo "model name" string to an
// archCoefficients key, or "" if nothing matches (caller falls through to
// the generic fallback tier). EPYC 7xxx SKUs are resolved via the exact-match
// table (epycSKUGeneration) BEFORE the general regex patterns, since no
// regex range can distinguish AMD's overlapping generations.
func matchArchitecture(cpuModel string) string {
	if m := epycSKUNumber.FindStringSubmatch(cpuModel); m != nil {
		if key, ok := epycSKUGeneration[m[1]]; ok {
			return key
		}
	}
	for _, p := range archPatterns {
		if p.re.MatchString(cpuModel) {
			return p.key
		}
	}
	return ""
}

// Resolve applies the AEP's four-tier precedence: node annotation > Helm
// values > bundled table (by /proc/cpuinfo model match) > generic per-core
// fallback. The fallback tier is loud (AEP: "allowed but loud") — logged as
// a warning since its error band is the widest of the four.
func Resolve(nodeAnnotations map[string]string, valuesIdle, valuesMax float64, cpuModel string) Coefficients {
	if nodeAnnotations != nil {
		idleStr, hasIdle := nodeAnnotations["obs.avuru.io/power-idle-watts"]
		maxStr, hasMax := nodeAnnotations["obs.avuru.io/power-max-watts"]
		if hasIdle && hasMax {
			idle, errIdle := strconv.ParseFloat(idleStr, 64)
			max, errMax := strconv.ParseFloat(maxStr, 64)
			if errIdle == nil && errMax == nil {
				return Coefficients{IdleWatts: idle, MaxWatts: max, Tier: "annotation", Provenance: "node annotation obs.avuru.io/power-{idle,max}-watts"}
			}
		}
	}
	if valuesIdle > 0 && valuesMax > 0 {
		return Coefficients{IdleWatts: valuesIdle, MaxWatts: valuesMax, Tier: "values", Provenance: "sensor.green.estimation.{idleWatts,maxWatts} (Helm values)"}
	}
	if key := matchArchitecture(cpuModel); key != "" {
		c := archCoefficients[key]
		c.Tier = "table"
		c.Provenance = "bundled table (" + coefficientDataset + "), matched architecture " + key
		return c
	}
	slog.Warn("no coefficient match for this CPU model, using the generic per-core fallback (widest error band)",
		"cpuModel", cpuModel, "idleWatts", genericFallback.IdleWatts, "maxWatts", genericFallback.MaxWatts)
	c := genericFallback
	c.Tier = "fallback"
	c.Provenance = fmt.Sprintf("generic per-core fallback (no table match for %q)", cpuModel)
	return c
}

// cpuModelName reads /proc/cpuinfo's first "model name" line, or "" if
// unreadable (Resolve then always falls through to the fallback tier).
func cpuModelName() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// nodeAnnotations fetches this node's annotations from the Kubernetes API
// server's Node object — NOT the kubelet, which exposes pod/stats data but
// not Node object metadata (the Downward API doesn't expose node
// annotations either; it's pod/container-scoped only). Needs no new RBAC:
// sensor-rbac.yaml already grants "nodes" get/list/watch cluster-wide
// (originally for OBI's/Kepler's own informers), so this reuses that
// existing grant. Unlike the kubelet's self-signed serving cert (kubelet.go
// skips verification to match kubeletstats's accepted trust model), the API
// server's serving cert IS signed by the cluster CA mounted alongside the
// token, so this client verifies it properly. Best-effort: any failure
// (network, RBAC, decode) returns nil, and Resolve falls through to the next
// tier — a missing annotation is normal, not an error.
func nodeAnnotations(nodeName string) map[string]string {
	token, err := readServiceAccountToken()
	if err != nil {
		return nil
	}
	caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	apiServer := "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT")
	req, err := http.NewRequest(http.MethodGet, apiServer+"/api/v1/nodes/"+nodeName, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var node struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return nil
	}
	return node.Metadata.Annotations
}
