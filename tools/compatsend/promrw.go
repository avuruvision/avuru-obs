package main

import (
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
	writev2 "github.com/prometheus/prometheus/prompb/io/prometheus/write/v2"
)

// Remote-write wire format (spec 2.0): snappy block compression, and the
// proto= content-type parameter is how the receiver tells v2 from v1.
const (
	promRWPath          = "/api/v1/write"
	promRWContentTypeV2 = "application/x-protobuf;proto=io.prometheus.write.v2.Request"
	promRWContentTypeV1 = "application/x-protobuf"
)

// sendPromRWv2 pushes one gauge sample as an io.prometheus.write.v2.Request.
// v2's symbol-table design: every string lives once in Symbols (index 0 is
// reserved for ""), and series reference label name/value pairs by index.
// The receiver maps job -> service.name, so -service becomes the job label.
func sendPromRWv2(endpoint, key, service, metric string) error {
	// Label order must be sorted by name: __name__ < instance < job.
	symbols := []string{"", "__name__", metric, "instance", "compat-1", "job", service}
	req := &writev2.Request{
		Symbols: symbols,
		Timeseries: []writev2.TimeSeries{{
			LabelsRefs: []uint32{1, 2, 3, 4, 5, 6},
			Metadata:   writev2.Metadata{Type: writev2.Metadata_METRIC_TYPE_GAUGE},
			Samples:    []writev2.Sample{{Value: 42.5, Timestamp: time.Now().UnixMilli()}},
		}},
	}
	raw, err := req.Marshal()
	if err != nil {
		return err
	}
	headers := map[string]string{
		"Content-Encoding":                  "snappy",
		"X-Prometheus-Remote-Write-Version": "2.0.0",
	}
	return postExpect2xx(endpoint+promRWPath, promRWContentTypeV2, key, headers, snappy.Encode(nil, raw))
}

// sendPromRWv1 deliberately pushes a v1 prompb.WriteRequest (no proto=
// content-type parameter, version header 0.1.0). The gateway's receiver is
// v2-only, so the EXPECTED outcome is HTTP 415 — callers assert the refusal.
func sendPromRWv1(endpoint, key, service, metric string) error {
	req := &prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{{
			Labels: []prompb.Label{
				{Name: "__name__", Value: metric},
				{Name: "instance", Value: "compat-1"},
				{Name: "job", Value: service},
			},
			Samples: []prompb.Sample{{Value: 42.5, Timestamp: time.Now().UnixMilli()}},
		}},
	}
	raw, err := req.Marshal()
	if err != nil {
		return err
	}
	headers := map[string]string{
		"Content-Encoding":                  "snappy",
		"X-Prometheus-Remote-Write-Version": "0.1.0",
	}
	return postExpect2xx(endpoint+promRWPath, promRWContentTypeV1, key, headers, snappy.Encode(nil, raw))
}
