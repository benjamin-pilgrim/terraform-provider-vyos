package vyos

import (
	"context"
	"reflect"
	"testing"
)

// Ownership transitions delete departed configuration and retain the new scope.
func TestBatchSelectorTransitionsDeleteDepartedConfiguration(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"removed path", "narrowed parent", "whole to include"} {
		t.Run(kind, func(t *testing.T) {
			vc, f := newBatchFixture(t, false, "")
			var prior, next Batch
			switch kind {
			case "removed path":
				prior = Batch{"old": {Path: []string{"node", "old"}, Value: `"keep"`}, "retained": {Path: []string{"node", "retained"}, Value: `"same"`}}
				next = Batch{"renamed": {Path: []string{"node", "retained"}, Value: `"same"`}}
			case "narrowed parent":
				prior = Batch{"parent": {Path: []string{"node"}, Value: `{"kept":"one","released":"keep"}`}}
				next = Batch{"child": {Path: []string{"node", "kept"}, Value: `"one"`}}
			case "whole to include":
				prior = Batch{"whole": {Path: []string{"node", "values"}, Value: `["owned","foreign"]`}}
				next = Batch{"partial": {Path: []string{"node", "values"}, Partial: true, Include: []string{"owned"}}}
			}
			if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "same-owner"); err != nil {
				t.Fatal(err)
			}
			if attempted, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "same-owner"); err != nil || !attempted {
				t.Fatalf("departed configuration was not removed: attempted=%v err=%v", attempted, err)
			}
			switch kind {
			case "removed path":
				if got, _ := configNode(f.tree, []string{"node", "old"}); got != nil {
					t.Fatal("removed path survived", got)
				}
				if got, _ := configNode(f.tree, []string{"node", "retained"}); got != "same" {
					t.Fatal("retained path changed", got)
				}
			case "narrowed parent":
				if got, _ := configNode(f.tree, []string{"node", "released"}); got != nil {
					t.Fatal("departed child survived", got)
				}
				if got, _ := configNode(f.tree, []string{"node", "kept"}); got != "one" {
					t.Fatal("retained child changed", got)
				}
			case "whole to include":
				if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"owned"}) {
					t.Fatal("whole-to-include retained unselected owned values", got)
				}
			}
			if f.tree["unrelated"] != "keep" {
				t.Fatal("transition changed unrelated configuration")
			}
			// Released ownership must also stop blocking another resource in this client.
			released := Batch{"released": {Path: []string{"node", "old"}}}
			if kind == "narrowed parent" {
				released = Batch{"released": {Path: []string{"node", "released"}}}
			}
			if kind == "whole to include" {
				released = Batch{"released": {Path: []string{"node", "values"}, Partial: true, Include: []string{"foreign"}}}
			}
			if _, err := vc.ShowOwnedBatch(ctx, released, "other-owner"); err != nil {
				t.Fatalf("released path still blocks another resource: %v", err)
			}
		})
	}
}

func TestRenamedIncludeUpdatesMembersByPath(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	f.tree["node"] = map[string]any{"values": []string{"foreign", "old"}}
	prior := Batch{"old-label": {Path: []string{"node", "values"}, Partial: true, Include: []string{"old"}}}
	if _, err := vc.ShowOwnedBatch(ctx, prior, "owner"); err != nil {
		t.Fatal(err)
	}
	next := Batch{"new-label": {Path: []string{"node", "values"}, Partial: true, Include: []string{"new"}}}
	if _, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign", "new"}) {
		t.Fatalf("retained list selector did not update owned members: %v", got)
	}
}

func TestBatchCanRemoveEverySelector(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	prior := Batch{"old": {Path: []string{"node", "old"}, Value: `"keep"`}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	if attempted, err := vc.MutateOwnedBatch(ctx, Batch{}, prior, "update", "owner"); err != nil || !attempted {
		t.Fatalf("empty selector set should delete owned configuration: %v", err)
	}
	if value, _ := configNode(f.tree, []string{"node", "old"}); value != nil {
		t.Fatal("removing all selectors left configuration behind")
	}
	if _, err := vc.ShowOwnedBatch(ctx, prior, "other-owner"); err != nil {
		t.Fatal("released selector still blocks ownership", err)
	}
}

func TestBatchExplicitAbsenceDeletesOnlySelectedSubtree(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	f.tree["node"] = map[string]any{"remove": map[string]any{"value": "old"}, "keep": "foreign"}
	desired := Batch{"removed": {Path: []string{"node", "remove"}, Absent: true}}
	if attempted, err := vc.MutateOwnedBatch(ctx, desired, nil, "create", "owner"); err != nil || !attempted {
		t.Fatalf("explicit absence not applied: %v", err)
	}
	if node, _ := configNode(f.tree, []string{"node", "remove"}); node != nil {
		t.Fatal("explicit deletion retained configuration")
	}
	if node, _ := configNode(f.tree, []string{"node", "keep"}); node != "foreign" {
		t.Fatal("explicit deletion touched unrelated configuration")
	}
	values, err := vc.ShowOwnedBatch(ctx, desired, "owner")
	if err != nil || len(values) != 1 || values["removed"] != nil {
		t.Fatal("absent constraint disappeared on refresh", err)
	}
	if attempted, err := vc.MutateOwnedBatch(ctx, desired, desired, "update", "owner"); err != nil || attempted {
		t.Fatalf("satisfied absence was not a no-op: %v", err)
	}
}

// Removing an include entry deletes its members and preserves unrelated values.
func TestRemovingIncludeEntryDeletesOnlyItsMembers(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	f.tree["node"] = map[string]any{"values": []string{"foreign"}}
	prior := Batch{
		"released": {Path: []string{"node", "values"}, Partial: true, Include: []string{"released"}},
		"retained": {Path: []string{"node", "values"}, Partial: true, Include: []string{"retained"}},
	}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	next := Batch{"retained": prior["retained"]}
	if attempted, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner"); err != nil || !attempted {
		t.Fatalf("removed include was not deleted: %v", err)
	}
	if got := fixtureValues(t, f); !reflect.DeepEqual(got, []string{"foreign", "retained"}) {
		t.Fatalf("removed member survived or unrelated members changed: %v", got)
	}
}
