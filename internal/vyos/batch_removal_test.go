package vyos

import (
	"context"
	"testing"
)

// Omission must remove previously owned router configuration without requiring
// an explicit absence entry, while leaving unrelated configuration untouched.
func TestBatchRemovedEntriesDeleteOwnedConfiguration(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	f.tree["node"] = map[string]any{"foreign": "keep"}
	prior := Batch{"owned": {Path: []string{"node", "owned"}, Value: `"old"`}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := vc.MutateOwnedBatch(ctx, Batch{}, prior, "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if got, _ := configNode(f.tree, []string{"node", "owned"}); got != nil {
		t.Fatalf("removed batch entry left owned router configuration behind: %v", got)
	}
	if got, _ := configNode(f.tree, []string{"node", "foreign"}); got != "keep" {
		t.Fatalf("removal changed unrelated configuration: %v", got)
	}
}

func TestBatchPathChangeDeletesOldPath(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	prior := Batch{"rule": {Path: []string{"rules", "110"}, Value: `{"source":"old"}`}}
	next := Batch{"rule": {Path: []string{"rules", "2"}, Value: `{"source":"new"}`}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	vc.SetCommitConfirmMinutes(2)
	if _, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if got, _ := configNode(f.tree, []string{"rules", "110"}); got != nil {
		t.Fatalf("old rule survived path change: %v", got)
	}
	if got, _ := configNode(f.tree, []string{"rules", "2", "source"}); got != "new" {
		t.Fatalf("new rule missing: %v", got)
	}
}

func TestBatchIncludeRemovalAndPathChangePreserveForeignMembers(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "moved"}[move], func(t *testing.T) {
			vc, f := newBatchFixture(t, false, "")
			ctx := context.Background()
			f.tree["node"] = map[string]any{"old": []string{"foreign", "owned"}, "new": []string{"other"}}
			prior := Batch{"list": {Path: []string{"node", "old"}, Partial: true, Include: []string{"owned"}}}
			if _, err := vc.ShowOwnedBatch(ctx, prior, "owner"); err != nil {
				t.Fatal(err)
			}
			next := Batch{}
			if move {
				next["list"] = BatchEntry{Path: []string{"node", "new"}, Partial: true, Include: []string{"owned"}}
			}
			if _, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner"); err != nil {
				t.Fatal(err)
			}
			old, _ := configNode(f.tree, []string{"node", "old"})
			members, _ := listValues(old)
			if len(members) != 1 || members[0] != "foreign" {
				t.Fatalf("old list = %v, want only foreign member", members)
			}
			newValue, _ := configNode(f.tree, []string{"node", "new"})
			newMembers, _ := listValues(newValue)
			set := stringSet(newMembers)
			if !set["other"] || set["owned"] != move {
				t.Fatalf("new list = %v", newMembers)
			}
		})
	}
}

func TestBatchRenamingEntriesDoesNotDeleteConfiguration(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	prior := Batch{"old": {Path: []string{"node", "value"}, Value: `"same"`}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	next := Batch{"new": prior["old"]}
	attempted, err := vc.MutateOwnedBatch(ctx, next, prior, "update", "owner")
	if err != nil || attempted {
		t.Fatalf("rename rewrote unchanged configuration: %v", err)
	}
	if got, _ := configNode(f.tree, []string{"node", "value"}); got != "same" {
		t.Fatalf("rename changed value: %v", got)
	}
}

func TestBatchFailedRemovalRetainsOwnershipForRetry(t *testing.T) {
	vc, f := newBatchFixture(t, false, "")
	ctx := context.Background()
	prior := Batch{"old": {Path: []string{"node", "owned"}, Value: `"old"`}}
	if _, err := vc.MutateOwnedBatch(ctx, prior, nil, "create", "owner"); err != nil {
		t.Fatal(err)
	}
	f.failure = "configure"
	if _, err := vc.MutateOwnedBatch(ctx, Batch{}, prior, "update", "owner"); err == nil {
		t.Fatal("expected deletion failure")
	}
	if _, err := vc.ShowOwnedBatch(ctx, prior, "other-owner"); err == nil {
		t.Fatal("failed removal relinquished ownership")
	}
	f.failure = ""
	if _, err := vc.MutateOwnedBatch(ctx, Batch{}, prior, "update", "owner"); err != nil {
		t.Fatal(err)
	}
	if got, _ := configNode(f.tree, []string{"node", "owned"}); got != nil {
		t.Fatalf("retry left configuration behind: %v", got)
	}
}
