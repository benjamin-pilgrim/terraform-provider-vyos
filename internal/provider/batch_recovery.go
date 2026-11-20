package provider

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An uncertain write may have left either old or new selections on the router.
// Retain their union until refresh and retry, using whole ancestors to represent
// overlapping scopes and disjoint include members to represent shared leaves.
func retainBatchOwnership(ctx context.Context, prior, planned types.Dynamic) (types.Dynamic, diag.Diagnostics) {
	type selection struct {
		name    string
		model   batchEntryModel
		entry   vyos.BatchEntry
		planned bool
	}
	var diagnostics diag.Diagnostics
	var candidates []selection
	for i, source := range []types.Dynamic{planned, prior} {
		models, d := batchModels(ctx, source)
		diagnostics.Append(d...)
		entries, d := decodeBatch(ctx, source, true)
		diagnostics.Append(d...)
		for name, entry := range entries {
			candidates = append(candidates, selection{name, models[name], entry, i == 0})
		}
	}
	if diagnostics.HasError() {
		return prior, diagnostics
	}
	priority := func(e vyos.BatchEntry) int {
		if e.Absent {
			return 2
		}
		if e.Partial {
			return 1
		}
		return 0
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if priority(a.entry) != priority(b.entry) {
			return priority(a.entry) < priority(b.entry)
		}
		if len(a.entry.Path) != len(b.entry.Path) {
			return len(a.entry.Path) < len(b.entry.Path)
		}
		if a.planned != b.planned {
			return a.planned
		}
		return a.name < b.name
	})
	contains := func(parent, child []string) bool {
		return len(parent) <= len(child) && slices.Equal(parent, child[:len(parent)])
	}
	retained := map[string]batchEntryModel{}
	owned := vyos.Batch{}
	for _, candidate := range candidates {
		entry := candidate.entry
		covered := false
		target := ""
		for name, existing := range owned {
			if !existing.Partial && contains(existing.Path, entry.Path) ||
				entry.Absent && contains(entry.Path, existing.Path) {
				covered = true
				break
			}
			if entry.Partial && existing.Partial && slices.Equal(entry.Path, existing.Path) {
				if target == "" || name < target {
					target = name
				}
				members := entry.Include[:0:0]
				for _, member := range entry.Include {
					if !slices.Contains(existing.Include, member) {
						members = append(members, member)
					}
				}
				entry.Include = members
			}
		}
		if covered || entry.Partial && len(entry.Include) == 0 {
			continue
		}
		if target != "" {
			existing := owned[target]
			existing.Include = append(existing.Include, entry.Include...)
			model := retained[target]
			model.Value = refreshInclude(model.Value, existing.Include)
			retained[target], owned[target] = model, existing
			continue
		}
		name := candidate.name
		for index := 0; ; index++ {
			if _, exists := owned[name]; !exists {
				break
			}
			name = fmt.Sprintf("_previous_%d", index)
		}
		model := candidate.model
		if entry.Partial {
			model.Value = refreshInclude(model.Value, entry.Include)
		}
		retained[name], owned[name] = model, entry
	}
	result, d := packBatchModels(ctx, retained)
	diagnostics.Append(d...)
	return result, diagnostics
}
