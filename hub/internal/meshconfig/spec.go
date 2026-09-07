package meshconfig

import "strings"

// Readers for a spec as the decoder left it: []any and map[string]any all the
// way down, with every field optional. Each one answers "nothing" for a shape
// it did not expect, so a check never has to guard a type assertion.

// splitHost pulls name and namespace out of a cluster-shaped host.
func splitHost(host string) (name, namespace string, ok bool) {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func slice(v any) []any {
	s, _ := v.([]any)
	return s
}

func stringSlice(v any) []string {
	var out []string
	for _, item := range slice(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mapSlice(v any) []map[string]any {
	var out []map[string]any
	for _, item := range slice(v) {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func nestedMap(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		next, ok := m[p].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}
