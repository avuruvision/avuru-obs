package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	model "github.com/jaegertracing/jaeger-idl/model/v1"
	"github.com/jaegertracing/jaeger-idl/proto-gen/api_v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// sendJaeger posts the fixture span over Jaeger's collector gRPC
// (api_v2.PostSpans). The otel.status_code=OK tag is how a Jaeger sender
// expresses span status; the receiver-side translator maps it to OTLP OK.
func sendJaeger(endpoint, key, service, traceIDHex string) error {
	traceID, spanID, err := jaegerIDs(traceIDHex)
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if key != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key)
	}

	span := &model.Span{
		TraceID:       traceID,
		SpanID:        spanID,
		OperationName: fixtureOperation,
		StartTime:     fixtureStart(),
		Duration:      fixtureDuration,
		Tags: []model.KeyValue{
			model.String("otel.status_code", "OK"),
			model.String("span.kind", "server"),
		},
	}
	_, err = api_v2.NewCollectorServiceClient(conn).PostSpans(ctx, &api_v2.PostSpansRequest{
		Batch: model.Batch{
			Spans:   []*model.Span{span},
			Process: &model.Process{ServiceName: service},
		},
	})
	return err
}

// jaegerIDs splits a 32-hex W3C trace id into Jaeger's high/low pair and
// derives the span id from its low half, keeping the fixture deterministic.
func jaegerIDs(traceIDHex string) (model.TraceID, model.SpanID, error) {
	raw, err := hex.DecodeString(traceIDHex)
	if err != nil || len(raw) != 16 {
		return model.TraceID{}, 0, fmt.Errorf("trace id must be 32 hex chars, got %q", traceIDHex)
	}
	high := binary.BigEndian.Uint64(raw[:8])
	low := binary.BigEndian.Uint64(raw[8:])
	return model.NewTraceID(high, low), model.NewSpanID(low), nil
}
