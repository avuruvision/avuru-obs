package collection

import (
	"context"
	"testing"
)

func TestNoopApplier_NeverErrors(t *testing.T) {
	var a Applier = NoopApplier{}
	if err := a.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("NoopApplier.Apply: %v", err)
	}
}
