package sentryreceiver

import (
	"encoding/hex"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// toLogs builds a one-record plog.Logs from a normalized error. The record is
// a standard OTel log with exception.* attributes and avuru.error.source, so
// the hub's logs materialized view derives an issue from it — no Sentry-aware
// code past this point.
func toLogs(n normalizedError) plog.Logs {
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()

	res := rl.Resource().Attributes()
	res.PutStr("service.name", n.ServiceName)
	if n.Environment != "" {
		res.PutStr("deployment.environment.name", n.Environment)
	}

	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("sentryreceiver")

	rec := sl.LogRecords().AppendEmpty()
	rec.SetTimestamp(pcommon.NewTimestampFromTime(n.Timestamp))
	rec.SetObservedTimestamp(pcommon.NewTimestampFromTime(n.Timestamp))
	rec.SetSeverityNumber(plog.SeverityNumber(n.SeverityNumber))
	rec.SetSeverityText(n.SeverityText)
	rec.Body().SetStr(n.Body)

	if tid, err := hex.DecodeString(n.TraceID); err == nil && len(tid) == 16 {
		var arr [16]byte
		copy(arr[:], tid)
		rec.SetTraceID(pcommon.TraceID(arr))
	}
	if sid, err := hex.DecodeString(n.SpanID); err == nil && len(sid) == 8 {
		var arr [8]byte
		copy(arr[:], sid)
		rec.SetSpanID(pcommon.SpanID(arr))
	}

	attrs := rec.Attributes()
	for k, v := range n.LogAttributes {
		attrs.PutStr(k, v)
	}
	return logs
}
