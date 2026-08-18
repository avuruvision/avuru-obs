package clickhouse

import (
	"reflect"
	"testing"
)

func TestTenantsOrDefault(t *testing.T) {
	if got := tenantsOrDefault(nil, "default"); !reflect.DeepEqual(got, []string{"default"}) {
		t.Errorf("empty set = %v, want [default]", got)
	}
	if got := tenantsOrDefault([]string{"a", "b"}, "default"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("filled set = %v, want [a b]", got)
	}
}

func TestRequireTenants(t *testing.T) {
	if err := requireTenants([]string{"a", "b"}); err != nil {
		t.Errorf("non-empty set errored: %v", err)
	}
	if err := requireTenants(nil); err == nil {
		t.Error("empty tenant set must error")
	}
}
