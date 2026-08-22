package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sendLoki pushes one log line via Loki's JSON push API
// (POST /loki/api/v1/push). The stream label lands as a log-RECORD attribute
// on the OTLP side (not a resource attribute — Loki labels don't carry
// resource identity), which is exactly what the compat e2e asserts.
func sendLoki(endpoint, key, labelKV, line string) error {
	name, value, ok := strings.Cut(labelKV, "=")
	if !ok || name == "" {
		return fmt.Errorf("-label must be key=value, got %q", labelKV)
	}
	payload := map[string]any{
		"streams": []map[string]any{{
			"stream": map[string]string{name: value},
			// Values are [<unix nanos as string>, <line>] pairs; the gateway
			// keeps this timestamp (use_incoming_timestamp: true).
			"values": [][]string{{strconv.FormatInt(time.Now().UnixNano(), 10), line}},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return postExpect2xx(endpoint+"/loki/api/v1/push", "application/json", key, nil, body)
}
