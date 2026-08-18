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

func TestFirstTenant(t *testing.T) {
	if got, err := firstTenant([]string{"a", "b"}); err != nil || got != "a" {
		t.Errorf("firstTenant = %q, %v, want a, nil", got, err)
	}
	if _, err := firstTenant(nil); err == nil {
		t.Error("empty tenant set must error")
	}
}
