package vyos

import (
	"context"
	"reflect"
	"testing"
)

func includeBatch(values ...string) Batch {
	return Batch{"dns": {Path: []string{"node", "values"}, Partial: true, Include: values}}
}
func fixtureValues(t *testing.T, f *batchFixture) []string {
	t.Helper()
	node, err := configNode(f.tree, []string{"node", "values"})
	if err != nil {
		t.Fatal(err)
	}
	values, err := listValues(node)
	if err != nil {
		t.Fatal(err)
	}
	return values
}
func TestIncludePreservesOtherValues(t *testing.T) {
	for _, timed := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "commit-confirm"}[timed], func(t *testing.T) {
			ctx := context.Background()
			vc, f := newBatchFixture(t, false, "")
			if timed {
				vc.SetCommitConfirmMinutes(1)
			}
			f.tree["node"] = map[string]any{"values": []string{"foreign", "already-present"}}
			owned := includeBatch("already-present", "mine")
			if _, err := vc.MutateOwnedBatch(ctx, owned, nil, "create", "owner-a"); err != nil {
				t.Fatal(err)
			}
			if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign", "already-present", "mine"}) {
				t.Fatalf("create changed others: %v", got)
			}
			next := includeBatch("new")
			if _, err := vc.MutateOwnedBatch(ctx, next, owned, "update", "owner-a"); err != nil {
				t.Fatal(err)
			}
			if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign", "new"}) {
				t.Fatalf("update changed others: %v", got)
			}
			for _, op := range f.ops {
				if op["op"] == "delete" && op["value"] == nil {
					t.Fatal("include deleted the complete leaf")
				}
			}
			if _, err := vc.MutateOwnedBatch(ctx, next, next, "delete", "owner-a"); err != nil {
				t.Fatal(err)
			}
			if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign"}) {
				t.Fatalf("destroy changed others: %v", got)
			}
			if len(vc.managedPaths) != 0 {
				t.Fatal("destroy retained claims")
			}
		})
	}
}
func TestIncludeDriftAndOrder(t *testing.T) {
	ctx := context.Background()
	vc, f := newBatchFixture(t, false, "")
	f.tree["node"] = map[string]any{"values": []string{"b", "foreign", "a"}}
	entries := includeBatch("a", "b")
	if attempted, err := vc.MutateOwnedBatch(ctx, entries, nil, "create", "owner"); err != nil || attempted {
		t.Fatalf("adoption rewrote list: %v", err)
	}
	f.calls = nil
	if attempted, err := vc.MutateOwnedBatch(ctx, includeBatch("b", "a"), entries, "update", "owner"); err != nil || attempted {
		t.Fatal("order-only update rewrote list")
	}
	f.tree["node"].(map[string]any)["values"] = []string{"foreign", "b", "other"}
	current, err := vc.ShowOwnedBatch(ctx, entries, "owner")
	if err != nil || !reflect.DeepEqual(current["dns"], []string{"b"}) {
		t.Fatalf("refresh adopted extras or hid owned drift: %v %v", current, err)
	}
	if _, err := vc.MutateOwnedBatch(ctx, entries, includeBatch("b"), "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign", "b", "other", "a"}) {
		t.Fatalf("repair changed others: %v", got)
	}
	delete(f.tree["node"].(map[string]any), "values")
	current, err = vc.ShowOwnedBatch(ctx, entries, "owner")
	if err != nil || len(current) != 1 || len(current["dns"].([]string)) != 0 {
		t.Fatal("missing included values lost ownership")
	}
}
func TestIncludeOwnership(t *testing.T) {
	for _, kind := range []string{"duplicate", "whole", "ancestor", "subtree", "invalid empty", "invalid duplicate"} {
		t.Run(kind, func(t *testing.T) {
			vc, f := newBatchFixture(t, false, "")
			ctx := context.Background()
			f.tree["node"] = map[string]any{"values": []string{"foreign"}}
			if _, err := vc.MutateOwnedBatch(ctx, includeBatch("one"), nil, "create", "owner-a"); err != nil {
				t.Fatal(err)
			}
			entries := includeBatch("two")
			switch kind {
			case "duplicate":
				entries = includeBatch("one")
			case "whole":
				entries = Batch{"whole": {Path: []string{"node", "values"}, Value: `["two"]`}}
			case "ancestor":
				entries = Batch{"whole": {Path: []string{"node"}, Value: `{}`}}
			case "subtree":
				entries = Batch{"whole": {Path: []string{"node", "values", "child"}, Value: `{}`}}
			case "invalid empty":
				entries = includeBatch("")
			case "invalid duplicate":
				entries = includeBatch("two", "two")
			}
			if attempted, err := vc.MutateOwnedBatch(ctx, entries, nil, "create", "owner-b"); err == nil || attempted {
				t.Fatal("invalid ownership accepted")
			}
			if _, err := vc.MutateOwnedBatch(ctx, includeBatch("two"), nil, "create", "owner-b"); err != nil {
				t.Fatal("disjoint include owner rejected", err)
			}
			if _, err := vc.Show(ctx, "node values"); err == nil {
				t.Fatal("individual resource overlapped include ownership")
			}
		})
	}
}
func TestIncludeTransferAndEmpty(t *testing.T) {
	ctx := context.Background()
	vc, f := newBatchFixture(t, false, "")
	prior := Batch{"first": {Path: []string{"node", "values"}, Partial: true, Include: []string{"a"}}, "second": {Path: []string{"node", "values"}, Partial: true}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	next := Batch{"first": {Path: []string{"node", "values"}, Partial: true}, "second": {Path: []string{"node", "values"}, Partial: true, Include: []string{"a"}}}
	if attempted, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner"); err != nil || attempted {
		t.Fatalf("ownership transfer rewrote list: %v", err)
	}
	if !reflect.DeepEqual(fixtureValues(t, f), []string{"a"}) {
		t.Fatal("ownership transfer deleted value")
	}
	if _, err := vc.MutateOwnedBatch(ctx, priorEmpty(next), next, "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if len(fixtureValues(t, f)) != 0 || len(vc.managedPaths) != 0 {
		t.Fatal("empty include did not remove owned values")
	}
}

func priorEmpty(entries Batch) Batch {
	out := Batch{}
	for name, entry := range entries {
		entry.Include = nil
		out[name] = entry
	}
	return out
}
