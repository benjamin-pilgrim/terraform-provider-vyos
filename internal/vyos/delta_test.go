package vyos

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestBatchUpdateChangesOnlyChangedLeaves(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	old := map[string]any{"description": "unchanged", "from": map[string]any{
		"ROUTER":  map[string]any{"firewall": map[string]any{"name": "OLD"}},
		"TRAEFIK": map[string]any{"firewall": map[string]any{"name": "KEEP"}},
	}}
	f.tree["zone"] = old
	wanted := `{"description":"unchanged","from":{"ROUTER":{"firewall":{"name":"NEW"}},"TRAEFIK":{"firewall":{"name":"KEEP"}},"K8S":{"firewall":{"name":"NODES"}}}}`
	entries := Batch{"zone": {Path: []string{"zone"}, Value: wanted}}
	if _, err := vc.MutateBatch(context.Background(), entries, "update"); err != nil {
		t.Fatal(err)
	}
	expected := []map[string]any{
		{"op": "delete", "path": []string{"zone", "from", "ROUTER", "firewall", "name"}},
		{"op": "set", "path": []string{"zone", "from", "K8S", "firewall", "name"}, "value": "NODES"},
		{"op": "set", "path": []string{"zone", "from", "ROUTER", "firewall", "name"}, "value": "NEW"},
	}
	a, _ := json.Marshal(f.ops)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		t.Fatalf("unchanged leaves were rebuilt: got %s want %s", a, b)
	}
	if !Equivalent(wanted, f.tree["zone"]) {
		t.Fatal("update did not converge")
	}
	if !reflect.DeepEqual(f.calls, []string{"/retrieve", "/configure", "/config-file"}) {
		t.Fatalf("wrong calls: %v", f.calls)
	}
}

func TestDeltaRemovesAbsentSubtreeAndReplacesListsAndFlags(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	f.tree["node"] = map[string]any{"stale": map[string]any{"child": "remove"}, "members": []any{"old", "keep"}, "flag": map[string]any{}, "parent": map[string]any{"child": "remove"}}
	wanted := `{"members":["keep","new"],"flag":"enabled","parent":{}}`
	if _, err := vc.MutateBatch(context.Background(), Batch{"node": {Path: []string{"node"}, Value: wanted}}, "update"); err != nil {
		t.Fatal(err)
	}
	if !Equivalent(wanted, f.tree["node"]) {
		t.Fatalf("stale values retained: %#v", f.tree["node"])
	}
	for _, op := range f.ops {
		if reflect.DeepEqual(op["path"], []any{"node"}) {
			t.Fatal("entire subtree replaced")
		}
	}
}
