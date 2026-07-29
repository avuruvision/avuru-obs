// Package tenantfromauth is a custom OpenTelemetry Collector processor that
// stamps the avuru.tenant resource attribute from the ingest key's validated
// project. The avuruingestauth server-auth extension attaches that project as
// client auth data (in enforce mode only); this processor reads it via
// client.FromContext and overwrites avuru.tenant on every record — so the key's
// project wins over any client-supplied tenant. When no such attribute is
// present (auth off, or log mode) it is a pass-through, leaving the existing
// resource/tenant + transform/tenant processors as the tenant source.
//
// IMPORTANT pipeline placement: this processor MUST run before the batch
// processor. The batch processor discards the request context (see the
// collector `client` package docs), so the auth data is only visible upstream
// of it.
package tenantfromauth

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

var typeStr = component.MustNewType("tenantfromauth")

// NewFactory builds the tenantfromauth factory (referenced by the OCB manifest).
func NewFactory() processor.Factory {
	return processor.NewFactory(
		typeStr,
		func() component.Config { return &Config{} },
		processor.WithTraces(createTraces, component.StabilityLevelAlpha),
		processor.WithMetrics(createMetrics, component.StabilityLevelAlpha),
		processor.WithLogs(createLogs, component.StabilityLevelAlpha),
	)
}

// mutates records that we overwrite a resource attribute in place.
var mutates = processorhelper.WithCapabilities(consumer.Capabilities{MutatesData: true})

func createTraces(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	return processorhelper.NewTraces(ctx, set, cfg, next, newProc().processTraces, mutates)
}

func createMetrics(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	return processorhelper.NewMetrics(ctx, set, cfg, next, newProc().processMetrics, mutates)
}

func createLogs(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	return processorhelper.NewLogs(ctx, set, cfg, next, newProc().processLogs, mutates)
}
