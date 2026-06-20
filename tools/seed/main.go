// seed POSTs deterministic OTLP/JSON fixtures to the gateway, rebasing every
// *UnixNano timestamp onto now-baseOffset so relative offsets/durations stay
// exact while the data lands inside the UI's default time window. Going
// through the gateway (not direct ClickHouse inserts) exercises the real
// ingest path.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	endpoint := flag.String("endpoint", "http://localhost:4318", "OTLP/HTTP endpoint")
	fixtures := flag.String("fixtures", "deploy/compose/seed/fixtures", "fixtures directory")
	offset := flag.Duration("offset", 5*time.Minute, "rebase fixtures to now minus this offset")
	flag.Parse()

	base := time.Now().Add(-*offset).UnixNano()

	entries, err := os.ReadDir(*fixtures)
	if err != nil {
		log.Fatalf("reading fixtures dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		var signal string
		switch {
		case strings.HasPrefix(name, "traces_"):
			signal = "v1/traces"
		case strings.HasPrefix(name, "logs_"):
			signal = "v1/logs"
		case strings.HasPrefix(name, "metrics_"):
			signal = "v1/metrics"
		default:
			log.Printf("skipping %s (no signal prefix)", name)
			continue
		}
		if err := send(*endpoint+"/"+signal, filepath.Join(*fixtures, name), base); err != nil {
			log.Fatalf("seeding %s: %v", name, err)
		}
		log.Printf("seeded %s -> %s", name, signal)
	}
}

func send(url, path string, base int64) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	rebase(doc, base)
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway answered %s", resp.Status)
	}
	return nil
}

// rebase walks the JSON tree adding base to every string field whose key
// ends in "UnixNano" (OTLP/JSON encodes 64-bit ints as strings).
func rebase(node any, base int64) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if strings.HasSuffix(k, "UnixNano") {
				if s, ok := child.(string); ok {
					ns, err := strconv.ParseInt(s, 10, 64)
					if err == nil {
						v[k] = strconv.FormatInt(base+ns, 10)
					}
				}
				continue
			}
			rebase(child, base)
		}
	case []any:
		for _, child := range v {
			rebase(child, base)
		}
	}
}
