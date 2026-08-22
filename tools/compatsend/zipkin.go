package main

import (
	"encoding/json"
	"fmt"
)

// sendZipkin posts the fixture span as a Zipkin v2 JSON array
// (POST /api/v2/spans). STATUS_CODE_OK is the tag value the receiver-side
// translator maps to OTLP status OK; span id is the trace id's low half,
// matching the jaeger sender so the two fixtures stay one logical span.
func sendZipkin(endpoint, key, service, traceIDHex string) error {
	if len(traceIDHex) != 32 {
		return fmt.Errorf("trace id must be 32 hex chars, got %q", traceIDHex)
	}
	span := []map[string]any{{
		"traceId":       traceIDHex,
		"id":            traceIDHex[16:],
		"name":          fixtureOperation,
		"kind":          "SERVER",
		"timestamp":     fixtureStart().UnixMicro(),
		"duration":      fixtureDuration.Microseconds(),
		"localEndpoint": map[string]string{"serviceName": service},
		"tags":          map[string]string{"otel.status_code": "STATUS_CODE_OK"},
	}}
	body, err := json.Marshal(span)
	if err != nil {
		return err
	}
	return postExpect2xx(endpoint+"/api/v2/spans", "application/json", key, nil, body)
}
