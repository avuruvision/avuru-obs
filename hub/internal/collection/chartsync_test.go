package collection

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedChartInSync fails when the committed copy under
// internal/collection/chart/ drifts from deploy/helm/avuruobs. The applier
// renders sensor manifests from the embedded copy, so a drift would make
// runtime toggles apply STALE config. Fix with: make sync-hub-chart.
func TestEmbeddedChartInSync(t *testing.T) {
	chartRoot := filepath.Join("..", "..", "..", "deploy", "helm", "avuruobs")
	if _, err := os.Stat(chartRoot); err != nil {
		t.Skipf("chart source not present at %s (building outside the monorepo?): %v", chartRoot, err)
	}
	pairs := []struct{ src, embedded string }{
		{filepath.Join(chartRoot, "Chart.yaml"), filepath.Join("chart", "Chart.yaml")},
		{filepath.Join(chartRoot, "values.yaml"), filepath.Join("chart", "values.yaml")},
		{filepath.Join(chartRoot, "templates", "_helpers.tpl"), filepath.Join("chart", "templates", "_helpers.tpl")},
		{filepath.Join(chartRoot, "templates", "sensor-config.yaml"), filepath.Join("chart", "templates", "sensor-config.yaml")},
		{filepath.Join(chartRoot, "templates", "sensor-daemonset.yaml"), filepath.Join("chart", "templates", "sensor-daemonset.yaml")},
	}
	for _, p := range pairs {
		src, err := os.ReadFile(p.src)
		if err != nil {
			t.Fatalf("read %s: %v", p.src, err)
		}
		emb, err := os.ReadFile(p.embedded)
		if err != nil {
			t.Fatalf("read %s (run `make sync-hub-chart`): %v", p.embedded, err)
		}
		if !bytes.Equal(src, emb) {
			t.Errorf("%s differs from %s — run `make sync-hub-chart` and commit the result", p.embedded, p.src)
		}
	}
}
