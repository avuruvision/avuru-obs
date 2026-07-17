package sentryreceiver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// envelopeHeader is the first line of a Sentry envelope.
type envelopeHeader struct {
	EventID string `json:"event_id"`
	DSN     string `json:"dsn"`
}

// itemHeader precedes each envelope item. Length, when present, is the exact
// payload byte count; when absent the payload runs to the next newline.
type itemHeader struct {
	Type   string `json:"type"`
	Length *int   `json:"length"`
}

// parseEnvelope extracts the "event" items from a Sentry envelope (v7). Other
// item types (session, transaction, attachment, client_report, check_in) are
// skipped, never errored — SDKs retry aggressively on 4xx, so a tolerant parse
// is a feature. A malformed structure returns an error; malformed individual
// event payloads are skipped.
func parseEnvelope(body []byte) ([]sentryEvent, error) {
	r := bufio.NewReader(bytes.NewReader(body))

	// Envelope header line (parsed for validation; we don't need its fields).
	headerLine, err := readLine(r)
	if err != nil {
		return nil, fmt.Errorf("reading envelope header: %w", err)
	}
	var hdr envelopeHeader
	if err := json.Unmarshal(headerLine, &hdr); err != nil {
		return nil, fmt.Errorf("invalid envelope header: %w", err)
	}

	var events []sentryEvent
	for {
		itemHeaderLine, err := readLine(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading item header: %w", err)
		}
		if len(bytes.TrimSpace(itemHeaderLine)) == 0 {
			continue
		}
		var ih itemHeader
		if err := json.Unmarshal(itemHeaderLine, &ih); err != nil {
			return nil, fmt.Errorf("invalid item header: %w", err)
		}

		payload, err := readItemPayload(r, ih.Length)
		if err != nil {
			return nil, fmt.Errorf("reading item payload: %w", err)
		}
		if ih.Type != "event" {
			continue // silently drop non-event items
		}
		var ev sentryEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			continue // skip a malformed event, don't fail the whole envelope
		}
		events = append(events, ev)
	}
	return events, nil
}

// readLine reads through the next '\n', returning the line without it.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return nil, err
	}
	line = bytes.TrimRight(line, "\n")
	if err != nil && err != io.EOF {
		return nil, err
	}
	return line, nil
}

// readItemPayload reads a length-delimited payload when length is set,
// otherwise the payload up to (and consuming) the next newline.
func readItemPayload(r *bufio.Reader, length *int) ([]byte, error) {
	if length != nil {
		buf := make([]byte, *length)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		// A trailing newline after the payload is optional — consume it if present.
		if b, err := r.ReadByte(); err == nil && b != '\n' {
			_ = r.UnreadByte()
		}
		return buf, nil
	}
	line, err := readLine(r)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return line, nil
}
