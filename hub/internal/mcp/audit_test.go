package mcp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The audit obligation, asserted: the line says WHO read WHAT SHAPE of data,
// and carries none of the data. Logging the rows would put the log bodies this
// module exports into a second place they were never meant to be.
func TestAuditLineNamesTheReadAndNotTheRows(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := fakeWithServices("payment-api")
	f.Services[0] = storage.ServiceStats{Name: "payment-api", SpanCount: 10}
	s := serverWith(f)
	s.Actor = "bot@x.io"

	if _, isErr := callTool(t, s, "list_services", `{"window":"1h"}`); isErr {
		t.Fatal("call failed")
	}

	line := buf.String()
	for _, want := range []string{`"tool":"list_services"`, `"actor":"bot@x.io"`, `"rows":1`, `"project":"default"`} {
		if !strings.Contains(line, want) {
			t.Errorf("audit line is missing %s: %s", want, line)
		}
	}
	if !strings.Contains(line, "1h") {
		t.Errorf("audit line does not record the arguments: %s", line)
	}
	if strings.Contains(line, "payment-api") {
		t.Errorf("audit line carried a row it returned: %s", line)
	}
}
