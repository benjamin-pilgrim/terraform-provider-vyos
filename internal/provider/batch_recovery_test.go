package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A failed selector transition must leave enough persistent ownership for a
// fresh provider to clean up both the old and possibly applied new selections.
func TestFailedBatchTransitionsRemainDestroyable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after vyos.Batch
		live          string
		want          string
	}{
		{"path change with same label",
			vyos.Batch{"entry": {Path: []string{"node", "old"}, Value: `"old"`}},
			vyos.Batch{"entry": {Path: []string{"node", "new"}, Value: `"new"`}},
			`{"node":{"old":"old","new":"new","foreign":"keep"}}`,
			`{"node":{"foreign":"keep"}}`},
		{"narrowed parent",
			vyos.Batch{"entry": {Path: []string{"node", "parent"}, Value: `{"kept":"one","removed":"two"}`}},
			vyos.Batch{"entry": {Path: []string{"node", "parent", "kept"}, Value: `"one"`}},
			`{"node":{"parent":{"kept":"one","removed":"two"},"foreign":"keep"}}`,
			`{"node":{"foreign":"keep"}}`},
		{"whole to include",
			vyos.Batch{"entry": {Path: []string{"node", "list"}, Value: `["owned","formerly_owned"]`}},
			vyos.Batch{"entry": {Path: []string{"node", "list"}, Partial: true, Include: []string{"owned"}}},
			`{"node":{"list":["owned","formerly_owned"],"foreign":"keep"}}`,
			`{"node":{"foreign":"keep"}}`},
		{"removed include",
			vyos.Batch{"entry": {Path: []string{"node", "list"}, Partial: true, Include: []string{"owned"}}},
			vyos.Batch{},
			`{"node":{"list":["owned","foreign"]}}`,
			`{"node":{"list":["foreign"]}}`},
		{"include to whole",
			vyos.Batch{"entry": {Path: []string{"node", "list"}, Partial: true, Include: []string{"owned"}}},
			vyos.Batch{"entry": {Path: []string{"node", "list"}, Value: `["owned"]`}},
			`{"node":{"list":["owned","foreign"],"foreign":"keep"}}`,
			`{"node":{"foreign":"keep"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var live map[string]any
			if err := json.Unmarshal([]byte(tc.live), &live); err != nil {
				t.Fatal(err)
			}
			reject := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/configure" {
					if reject {
						fmt.Fprint(w, `{"success":false}`)
						return
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					var ops []struct {
						Op    string   `json:"op"`
						Path  []string `json:"path"`
						Value *string  `json:"value"`
					}
					if err := json.Unmarshal([]byte(r.FormValue("data")), &ops); err != nil {
						t.Error(err)
					}
					for _, op := range ops {
						if op.Op != "delete" {
							t.Errorf("destroy attempted %s", op.Op)
							continue
						}
						node := live
						for _, part := range op.Path[:len(op.Path)-1] {
							node = node[part].(map[string]any)
						}
						leaf := op.Path[len(op.Path)-1]
						if op.Value == nil {
							delete(node, leaf)
						} else {
							kept := []any{}
							for _, v := range node[leaf].([]any) {
								if v != *op.Value {
									kept = append(kept, v)
								}
							}
							if len(kept) == 0 {
								delete(node, leaf)
							} else {
								node[leaf] = kept
							}
						}
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": live})
			}))
			defer server.Close()
			fresh := func() *ConfigBatchResource {
				return &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL}, false, "")}
			}
			r := fresh()
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			model := func(entries vyos.Batch) ConfigBatchModel {
				models := map[string]batchEntryModel{}
				for name, entry := range entries {
					path, d := types.ListValueFrom(ctx, types.StringType, entry.Path)
					if d.HasError() {
						t.Fatal(d)
					}
					value := types.DynamicNull()
					if entry.Partial {
						value = refreshInclude(value, entry.Include)
					} else {
						var native any
						if err := json.Unmarshal([]byte(entry.Value), &native); err != nil {
							t.Fatal(err)
						}
						value = refreshConfiguration(value, native)
					}
					models[name] = batchEntryModel{Path: path, Value: value, Partial: entry.Partial}
				}
				values, d := packBatchModels(ctx, models)
				if d.HasError() {
					t.Fatal(d)
				}
				id, err := vyos.OwnedBatchIdentity(entries, "test-owner")
				if err != nil {
					t.Fatal(err)
				}
				return ConfigBatchModel{ID: types.StringValue(id), Entries: values}
			}
			prior, planned := model(tc.before), model(tc.after)
			state := tfsdk.State{Schema: schema.Schema}
			plan := tfsdk.Plan{Schema: schema.Schema}
			if d := state.Set(ctx, &prior); d.HasError() {
				t.Fatal(d)
			}
			if d := plan.Set(ctx, &planned); d.HasError() {
				t.Fatal(d)
			}
			failed := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &failed)
			if !failed.Diagnostics.HasError() {
				t.Fatal("expected rejected transition")
			}
			reject = false
			r = fresh()
			read := resource.ReadResponse{State: failed.State}
			r.Read(ctx, resource.ReadRequest{State: failed.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			deletion := resource.DeleteResponse{State: read.State}
			r.Delete(ctx, resource.DeleteRequest{State: read.State}, &deletion)
			if deletion.Diagnostics.HasError() {
				t.Fatal(deletion.Diagnostics)
			}
			got, err := json.Marshal(live)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("cleanup left %s, want %s", got, tc.want)
			}
		})
	}
}
