package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func includedModel(t *testing.T, values ...string) ConfigBatchModel {
	t.Helper()
	ctx := context.Background()
	list, _ := types.ListValueFrom(ctx, types.StringType, []string{"node", "dns"})
	models := map[string]batchEntryModel{"dns": {Path: list, Partial: true, Value: refreshInclude(types.DynamicNull(), values)}}
	entries, d := packBatchModels(ctx, models)
	if d.HasError() {
		t.Fatal(d)
	}
	identity, err := vyos.OwnedBatchIdentity(vyos.Batch{"dns": {Path: []string{"node", "dns"}, Partial: true, Include: values}}, "owner-test")
	if err != nil {
		t.Fatal(err)
	}
	return ConfigBatchModel{ID: types.StringValue(identity), Entries: entries}
}
func TestIncludeReadDoesNotAdoptOtherValuesOrLoseMissingOwnership(t *testing.T) {
	ctx := context.Background()
	var live atomic.Value
	live.Store(`{"success":true,"data":{"node":{"dns":["foreign","two"]}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, live.Load().(string)) }))
	defer server.Close()
	r := &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL}, false, "")}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	original := includedModel(t, "one", "two")
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(ctx, &original); d.HasError() {
		t.Fatal(d)
	}
	for _, remaining := range []int{1, 0} {
		if remaining == 0 {
			live.Store(`{"success":true,"data":{"node":{"dns":["foreign"]}}}`)
		}
		response := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &response)
		if response.Diagnostics.HasError() || response.State.Raw.IsNull() {
			t.Fatalf("missing values removed partial resource: %v", response.Diagnostics)
		}
		var refreshed ConfigBatchModel
		if d := response.State.Get(ctx, &refreshed); d.HasError() {
			t.Fatal(d)
		}
		entries, d := decodeBatch(ctx, refreshed.Entries, true)
		if d.HasError() || len(entries["dns"].Include) != remaining || refreshed.ID != original.ID {
			t.Fatalf("refresh adopted others or lost identity: %v", d)
		}
		state = response.State
	}
	// A different order or value set keeps the same ownership mode and path.
	plan := tfsdk.Plan{Schema: schema.Schema}
	if d := plan.Set(ctx, &original); d.HasError() {
		t.Fatal(d)
	}
	modify := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, &modify)
	if modify.Diagnostics.HasError() || len(modify.RequiresReplace) != 0 {
		t.Fatal("include repair required replacement")
	}
	models, d := batchModels(ctx, original.Entries)
	if d.HasError() {
		t.Fatal(d)
	}
	entry := models["dns"]
	entry.Partial = false
	entry.Value = types.DynamicValue(types.StringValue(`["one","two"]`))
	models["dns"] = entry
	changed := original
	changed.Entries, _ = packBatchModels(ctx, models)
	if d := plan.Set(ctx, &changed); d.HasError() {
		t.Fatal(d)
	}
	modify = resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, &modify)
	if !modify.Diagnostics.HasError() || len(modify.RequiresReplace) != 0 {
		t.Fatal("ownership-mode change was not blocked before replacement")
	}
}
func TestIncludeValidationAndCollectionTypes(t *testing.T) {
	ctx := context.Background()
	r := &ConfigBatchResource{}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	for _, tc := range []struct {
		name  string
		value attr.Value
		valid bool
	}{
		{"tuple", types.TupleValueMust([]attr.Type{types.StringType}, []attr.Value{types.StringValue("one")}), true},
		{"set", types.SetValueMust(types.StringType, []attr.Value{types.StringValue("one")}), true},
		{"empty", types.TupleValueMust([]attr.Type{}, []attr.Value{}), true},
		{"unknown set", types.SetUnknown(types.StringType), true},
		{"unknown list", types.ListUnknown(types.StringType), true},
		{"unknown", types.TupleValueMust([]attr.Type{types.StringType}, []attr.Value{types.StringUnknown()}), true},
		{"duplicate", types.ListValueMust(types.StringType, []attr.Value{types.StringValue("one"), types.StringValue("one")}), false},
		{"empty item", types.ListValueMust(types.StringType, []attr.Value{types.StringValue("")}), false},
		{"null", types.ListNull(types.StringType), false},
		{"null item", types.ListValueMust(types.StringType, []attr.Value{types.StringNull()}), false},
		{"scalar", types.StringValue("one"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := includedModel(t)
			models, _ := batchModels(ctx, model.Entries)
			entry := models["dns"]
			entry.Value = types.DynamicValue(tc.value)
			models["dns"] = entry
			model.Entries, _ = packBatchModels(ctx, models)
			state := tfsdk.State{Schema: schema.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			response := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &response)
			if response.Diagnostics.HasError() == tc.valid {
				t.Fatalf("validation: %v", response.Diagnostics)
			}
		})
	}
}

func TestIncludeFailedUpdateRetainsAttemptedOwnership(t *testing.T) {
	for _, failure := range []string{"configure", "config-file"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			var mu sync.Mutex
			live := []string{"foreign", "old"}
			failed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				req.ParseForm()
				if req.URL.Path == "/configure" {
					var operations []struct {
						Op    string `json:"op"`
						Value string `json:"value"`
					}
					if err := json.Unmarshal([]byte(req.FormValue("data")), &operations); err != nil {
						t.Error(err)
					}
					for _, operation := range operations {
						if operation.Op == "delete" {
							kept := []string{}
							for _, value := range live {
								if value != operation.Value {
									kept = append(kept, value)
								}
							}
							live = kept
						} else {
							live = append(live, operation.Value)
						}
					}
				}
				if !failed && req.URL.Path == "/"+failure {
					failed = true
					fmt.Fprint(w, `{"success":false}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"node": map[string]any{"dns": live}}})
			}))
			defer server.Close()
			fresh := func() *ConfigBatchResource {
				return &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL}, false, "")}
			}
			r := fresh()
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			prior := includedModel(t, "old")
			planned := includedModel(t, "new")
			state := tfsdk.State{Schema: schema.Schema}
			plan := tfsdk.Plan{Schema: schema.Schema}
			if d := state.Set(ctx, &prior); d.HasError() {
				t.Fatal(d)
			}
			if d := plan.Set(ctx, &planned); d.HasError() {
				t.Fatal(d)
			}
			update := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &update)
			if !update.Diagnostics.HasError() {
				t.Fatal("failed update accepted")
			}
			var retained ConfigBatchModel
			if d := update.State.Get(ctx, &retained); d.HasError() {
				t.Fatal(d)
			}
			entries, d := decodeBatch(ctx, retained.Entries, true)
			if d.HasError() || len(entries["dns"].Include) != 2 {
				t.Fatal("failed update lost possibly applied values", d)
			}
			r = fresh()
			read := resource.ReadResponse{State: update.State}
			r.Read(ctx, resource.ReadRequest{State: update.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			deletion := resource.DeleteResponse{State: read.State}
			r.Delete(ctx, resource.DeleteRequest{State: read.State}, &deletion)
			if deletion.Diagnostics.HasError() {
				t.Fatal(deletion.Diagnostics)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(live) != 1 || live[0] != "foreign" {
				t.Fatalf("failed update orphaned an included value: %v", live)
			}
		})
	}
}

func TestMixedIncludeImportRefresh(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"node":{"full":{"leaf":"value"},"dns":["owned","foreign"]}}}`)
	}))
	defer server.Close()
	r := &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL}, false, "")}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: `{"full":["node","full"],"dns":{"path":["node","dns"],"include":["owned"]}}`}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	refreshed := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &refreshed)
	if refreshed.Diagnostics.HasError() {
		t.Fatal(refreshed.Diagnostics)
	}
	var model ConfigBatchModel
	if d := refreshed.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	entries, d := decodeBatch(ctx, model.Entries, true)
	if d.HasError() || len(entries["dns"].Include) != 1 || entries["dns"].Include[0] != "owned" || entries["full"].Value != `{"leaf":"value"}` {
		t.Fatal("import failed to retain explicit ownership", d)
	}
	rejected := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: model.ID.ValueString()}, &rejected)
	if !rejected.Diagnostics.HasError() {
		t.Fatal("partial import without explicit selectors was accepted")
	}
}
