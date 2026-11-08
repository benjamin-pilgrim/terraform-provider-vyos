package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func batchModel(t *testing.T, values map[string]string) ConfigBatchModel {
	t.Helper()
	models := map[string]batchEntryModel{}
	entries := vyos.Batch{}
	for name, value := range values {
		components := []string{"node", name}
		list, diagnostics := types.ListValueFrom(context.Background(), types.StringType, components)
		if diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		models[name] = batchEntryModel{Path: list, Value: types.StringValue(value)}
		entries[name] = vyos.BatchEntry{Path: components}
	}
	identity, err := vyos.BatchIdentity(entries)
	if err != nil {
		t.Fatal(err)
	}
	mapped, diagnostics := types.MapValueFrom(context.Background(), batchEntryType, models)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return ConfigBatchModel{ID: types.StringValue(identity), Entries: mapped}
}

func TestBatchCreateFailureRetainsIdentityOnlyAfterAttempt(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"retrieve", "existing", "configure", "config-file"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/"+failure {
					fmt.Fprint(w, `{"success":false,"error":"SECRET"}`)
				} else if failure == "existing" && req.URL.Path == "/retrieve" {
					fmt.Fprint(w, `{"success":true,"data":{"node":{"a":{}}}}`)
				} else {
					fmt.Fprint(w, `{"success":true,"data":{}}`)
				}
			}))
			defer server.Close()
			r := &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL, Key: "SECRET"}, false, "")}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			plan := tfsdk.Plan{Schema: schema.Schema}
			model := batchModel(t, map[string]string{"a": `"SECRET"`, "b": `{}`})
			if diagnostics := plan.Set(ctx, &model); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			response := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("failed create accepted")
			}
			attempted := failure == "configure" || failure == "config-file"
			if response.State.Raw.IsNull() == attempted {
				t.Fatalf("incorrect failed-create state: attempted=%v state=%v", attempted, response.State.Raw)
			}
			if attempted {
				var retained ConfigBatchModel
				if diagnostics := response.State.Get(ctx, &retained); diagnostics.HasError() || retained.ID != model.ID {
					t.Fatalf("lost batch cleanup identity: %v", diagnostics)
				}
			}
			for _, diagnostic := range response.Diagnostics {
				if strings.Contains(diagnostic.Detail(), "SECRET") {
					t.Fatal("secret leaked in diagnostic")
				}
			}
		})
	}
}

func TestBatchReadPartialDriftAndPlanIdentity(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"node":{"a":"external"}}}`)
	}))
	defer server.Close()
	r := &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL, Key: "test"}, false, "")}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	model := batchModel(t, map[string]string{"a": `"one"`, "b": `"two"`})
	state := tfsdk.State{Schema: schema.Schema}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var refreshed ConfigBatchModel
	if diagnostics := read.State.Get(ctx, &refreshed); diagnostics.HasError() || refreshed.ID != model.ID || len(refreshed.Entries.Elements()) != 1 {
		t.Fatalf("partial deletion lost identity/surviving state: %v", diagnostics)
	}
	for _, changedPaths := range []bool{false, true} {
		planned := model
		if changedPaths {
			planned = batchModel(t, map[string]string{"a": `"one"`, "c": `"three"`})
		}
		plan := tfsdk.Plan{Schema: schema.Schema}
		if diagnostics := plan.Set(ctx, &planned); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		response := resource.ModifyPlanResponse{Plan: plan}
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: read.State, Plan: plan}, &response)
		if response.Diagnostics.HasError() || (len(response.RequiresReplace) > 0) != changedPaths {
			t.Fatalf("incorrect replacement decision: changed paths=%v, response=%v", changedPaths, response)
		}
	}
}

func TestBatchImportArrayPaths(t *testing.T) {
	ctx := context.Background()
	r := &ConfigBatchResource{}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: `{"tag":["firewall","ipv4","name","name with spaces"]}`}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var imported ConfigBatchModel
	if diagnostics := response.State.Get(ctx, &imported); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	entries, diagnostics := decodeBatch(ctx, imported.Entries, false)
	if diagnostics.HasError() || entries["tag"].Path[3] != "name with spaces" {
		t.Fatalf("import split an array component: %v", diagnostics)
	}
}

func TestBatchValidationAndUnknownValues(t *testing.T) {
	ctx := context.Background()
	r := &ConfigBatchResource{}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	for _, tc := range []string{"valid unknown value", "valid unknown path component", "empty", "invalid JSON", "overlapping", "null entry", "null path component"} {
		t.Run(tc, func(t *testing.T) {
			model := batchModel(t, map[string]string{"a": `"one"`, "b": `{}`})
			var models map[string]batchEntryModel
			if diagnostics := model.Entries.ElementsAs(ctx, &models, false); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			switch tc {
			case "valid unknown value":
				entry := models["a"]
				entry.Value = types.StringUnknown()
				models["a"] = entry
			case "valid unknown path component":
				entry := models["a"]
				entry.Path, _ = types.ListValue(types.StringType, []attr.Value{types.StringValue("node"), types.StringUnknown()})
				models["a"] = entry
			case "null path component":
				entry := models["a"]
				entry.Path, _ = types.ListValue(types.StringType, []attr.Value{types.StringValue("node"), types.StringNull()})
				models["a"] = entry
			case "empty":
				models = map[string]batchEntryModel{}
			case "invalid JSON":
				entry := models["a"]
				entry.Value = types.StringValue("not JSON")
				models["a"] = entry
			case "overlapping":
				entry := models["a"]
				entry.Path, _ = types.ListValueFrom(ctx, types.StringType, []string{"node"})
				models["a"] = entry
			}
			model.Entries, _ = types.MapValueFrom(ctx, batchEntryType, models)
			if tc == "null entry" {
				model.Entries, _ = types.MapValue(batchEntryType, map[string]attr.Value{"null": types.ObjectNull(batchEntryType.AttrTypes)})
			}
			config := tfsdk.Config{Schema: schema.Schema}
			state := tfsdk.State{Schema: schema.Schema}
			if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			config.Raw = state.Raw
			response := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, &response)
			if response.Diagnostics.HasError() == strings.HasPrefix(tc, "valid unknown") {
				t.Fatalf("incorrect validation result: %v", response.Diagnostics)
			}
		})
	}
}

func TestBatchReadFailurePreservesState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"success":false,"error":"SECRET"}`)
	}))
	defer server.Close()
	r := &ConfigBatchResource{vyosConfig: vyos.New(&vyos.Client{HTTP: server.Client(), Endpoint: server.URL, Key: "SECRET"}, false, "")}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := batchModel(t, map[string]string{"a": `"one"`})
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	response := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) {
		t.Fatal("failed read discarded prior state")
	}
}
