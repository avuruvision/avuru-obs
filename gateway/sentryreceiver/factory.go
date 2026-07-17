// Package sentryreceiver is a custom OpenTelemetry Collector receiver that
// accepts the Sentry ingest protocol (envelope + legacy store API) and emits
// standard OTel log records carrying exception.* semconv attributes and
// avuru.error.source=sentry. Downstream, the hub's logs materialized view
// derives error issues from those records — the same path as any ERROR log.
// See design/2026-07-16-error-tracking.md.
package sentryreceiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

// typeStr is the component type as used in the collector config ("sentry").
var typeStr = component.MustNewType("sentry")

// NewFactory builds the sentryreceiver factory (referenced by the OCB manifest).
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		typeStr,
		createDefaultConfig,
		receiver.WithLogs(createLogsReceiver, component.StabilityLevelAlpha),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ServerConfig: confighttp.ServerConfig{
			NetAddr: confignet.AddrConfig{Endpoint: "0.0.0.0:4319", Transport: "tcp"},
		},
		Projects: map[string]ProjectConfig{},
	}
}

func createLogsReceiver(
	_ context.Context,
	set receiver.Settings,
	cfg component.Config,
	next consumer.Logs,
) (receiver.Logs, error) {
	return newReceiver(cfg.(*Config), set, next), nil
}
