package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// serviceAccountTokenPath is where the in-cluster ServiceAccount mounts the
// projected token — the standard Kubernetes convention every
// ServiceAccount-bound pod gets automatically.
const serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// podIdentity is the (name, namespace) pair carried on the estimator's
// pod_name/pod_namespace labels — matching Kepler's own labels exactly (AEP
// metric table), so the hub's existing pod->workload join needs no changes.
type podIdentity struct {
	Name      string
	Namespace string
}

// kubeletPodList is the minimal shape read from the kubelet's own /pods
// endpoint — deliberately NOT k8s.io/api/core/v1.PodList: the estimator stays
// dependency-free (stdlib only), and only two fields are ever needed.
type kubeletPodList struct {
	Items []struct {
		Metadata struct {
			UID       string `json:"uid"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	} `json:"items"`
}

// fetchPods calls the kubelet's /pods endpoint (baseURL like
// "https://node-1:10250") and returns a UID -> identity map for every pod the
// kubelet reports — which, by construction, is only ever pods on THIS node
// (no field-selector filtering needed, unlike the API server's pod list).
func fetchPods(client *http.Client, baseURL, token string) (map[string]podIdentity, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/pods", nil)
	if err != nil {
		return nil, fmt.Errorf("building kubelet /pods request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling kubelet /pods: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kubelet /pods returned %s", resp.Status)
	}

	var list kubeletPodList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding kubelet /pods response: %w", err)
	}

	out := make(map[string]podIdentity, len(list.Items))
	for _, item := range list.Items {
		out[item.Metadata.UID] = podIdentity{Name: item.Metadata.Name, Namespace: item.Metadata.Namespace}
	}
	return out, nil
}

// newKubeletClient builds the in-cluster HTTPS client for the kubelet API:
// self-signed serving certs are the norm per node, so verification is
// skipped — the exact trust boundary the otel-agent's kubeletstats receiver
// already accepts for the same endpoint (sensor-config.yaml, "kubelet
// serving certs are typically self-signed per node").
func newKubeletClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // matches kubeletstats's accepted trust model
		},
	}
}

// readServiceAccountToken reads the projected ServiceAccount token every
// in-cluster pod gets automatically — no Secret, no client-go, just a file
// read, refreshed on each call since Kubernetes rotates this token.
func readServiceAccountToken() (string, error) {
	b, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// kubeletBaseURL is the kubelet's HTTPS endpoint on this node — the same
// host:port the otel-agent's kubeletstats receiver targets.
func kubeletBaseURL(nodeName string) string {
	return "https://" + nodeName + ":10250"
}
