package sentryreceiver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

const maxBodyBytes = 5 << 20 // 5 MiB — a generous ceiling for one envelope.

// sentryReceiver serves the Sentry ingest API and forwards translated events
// to the logs consumer.
type sentryReceiver struct {
	cfg      *Config
	consumer consumer.Logs
	settings receiver.Settings
	server   *http.Server
	now      func() time.Time
}

func newReceiver(cfg *Config, set receiver.Settings, next consumer.Logs) *sentryReceiver {
	return &sentryReceiver{cfg: cfg, consumer: next, settings: set, now: time.Now}
}

func (r *sentryReceiver) Start(ctx context.Context, host component.Host) error {
	mux := http.NewServeMux()
	// Envelope API (current) and the legacy store API (older SDKs).
	mux.HandleFunc("POST /api/{project}/envelope/", r.handleEnvelope)
	mux.HandleFunc("POST /api/{project}/store/", r.handleStore)

	srv, err := r.cfg.ServerConfig.ToServer(ctx, host.GetExtensions(), r.settings.TelemetrySettings, mux)
	if err != nil {
		return err
	}
	r.server = srv

	ln, err := r.cfg.ServerConfig.ToListener(ctx)
	if err != nil {
		return err
	}
	go func() {
		if err := r.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			r.settings.Logger.Error("sentry receiver server error", zap.Error(err))
		}
	}()
	return nil
}

func (r *sentryReceiver) Shutdown(ctx context.Context) error {
	if r.server == nil {
		return nil
	}
	return r.server.Shutdown(ctx)
}

// handleEnvelope parses a Sentry envelope and consumes its event items. It
// answers 200 even on partial content — Sentry SDKs retry aggressively on
// 4xx/5xx, so only a truly unreadable request is rejected.
func (r *sentryReceiver) handleEnvelope(w http.ResponseWriter, req *http.Request) {
	project := req.PathValue("project")
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	events, err := parseEnvelope(body)
	if err != nil {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	r.consumeEvents(req.Context(), project, events)
	writeAccepted(w, "")
}

// handleStore accepts the legacy store API: the body is a bare event JSON.
func (r *sentryReceiver) handleStore(w http.ResponseWriter, req *http.Request) {
	project := req.PathValue("project")
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	var ev sentryEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	r.consumeEvents(req.Context(), project, []sentryEvent{ev})
	writeAccepted(w, ev.EventID)
}

func (r *sentryReceiver) consumeEvents(ctx context.Context, project string, events []sentryEvent) {
	for _, ev := range events {
		svc := r.serviceName(project, ev)
		logs := toLogs(normalizeEvent(ev, svc, r.now().UTC()))
		if err := r.consumer.ConsumeLogs(ctx, logs); err != nil {
			r.settings.Logger.Warn("failed to consume sentry event", zap.Error(err))
		}
	}
}

// serviceName resolves the resource service.name: project config, then the
// event's server_name, then a stable per-project fallback.
func (r *sentryReceiver) serviceName(project string, ev sentryEvent) string {
	if pc, ok := r.cfg.Projects[project]; ok && pc.ServiceName != "" {
		return pc.ServiceName
	}
	if ev.ServerName != "" {
		return ev.ServerName
	}
	return "sentry-project-" + project
}

func writeAccepted(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

var _ receiver.Logs = (*sentryReceiver)(nil)
