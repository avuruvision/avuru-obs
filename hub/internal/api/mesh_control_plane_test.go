package api

import (
	"encoding/json"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// The two later readings follow the trio's rule: absent when the install
// does not collect them, present — zero included — when it does.
func TestControlPlaneListenerConflictsAndQueueAreOptional(t *testing.T) {
	t.Run("not collected", func(t *testing.T) {
		fake := &storagetest.Fake{ControlPlane: storage.MeshControlPlane{
			Available: true, State: storage.MeshControlPlaneOK, Kind: "istio", ConnectedProxies: 12,
		}}
		rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
		for _, key := range []string{"listenerConflicts", "queueP95Ms"} {
			if jsonHasKey(t, rec.Body.String(), key) {
				t.Errorf("%s was serialized on an install that does not collect it", key)
			}
		}
	})
	t.Run("collected", func(t *testing.T) {
		conflicts, queue := uint64(0), 12.5
		fake := &storagetest.Fake{ControlPlane: storage.MeshControlPlane{
			Available: true, State: storage.MeshControlPlaneOK, Kind: "istio", ConnectedProxies: 12,
			ListenerConflicts: &conflicts, QueueP95Ms: &queue,
		}}
		rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
		body := rec.Body.String()
		if !jsonHasKey(t, body, "listenerConflicts") {
			t.Error("a measured zero was omitted, which reads as 'not collected'")
		}
		var resp meshControlPlaneResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ListenerConflicts == nil || *resp.ListenerConflicts != 0 {
			t.Errorf("listenerConflicts = %v, want a present 0", resp.ListenerConflicts)
		}
		if resp.QueueP95Ms == nil || *resp.QueueP95Ms != 12.5 {
			t.Errorf("queueP95Ms = %v, want 12.5", resp.QueueP95Ms)
		}
	})
}
