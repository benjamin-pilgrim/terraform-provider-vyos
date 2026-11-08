package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var legacyBatchEntryType = types.ObjectType{AttrTypes: map[string]attr.Type{"path": types.ListType{ElemType: types.StringType}, "value": types.StringType}}

func TestNativeConfigurationValidationAndRefresh(t *testing.T) {
	native := types.DynamicValue(valueFromRouter(map[string]any{"flag": map[string]any{}, "leaf": "one", "list": []any{"a", "b"}}))
	for _, text := range []string{"plain text", "443", "true"} {
		raw, err := configurationJSON(types.DynamicValue(types.StringValue(text)))
		want, _ := json.Marshal(text)
		if err != nil || raw != string(want) {
			t.Fatalf("literal scalar rejected: %q %v", text, err)
		}
	}
	raw, err := configurationJSON(native)
	if err != nil || !json.Valid([]byte(raw)) {
		t.Fatalf("invalid encoding: %q, %v", raw, err)
	}
	remote := map[string]any{"flag": map[string]any{}, "leaf": []any{"one"}, "list": []any{"a", "b"}}
	if refreshed := refreshConfiguration(native, remote); !refreshed.Equal(native) {
		t.Fatal("refresh changed equivalent native types")
	}
	remote["leaf"] = "changed"
	refreshed := refreshConfiguration(native, remote)
	if refreshed.Equal(native) {
		t.Fatal("refresh hid external drift")
	}
	if _, ok := refreshed.UnderlyingValue().(types.Object); !ok {
		t.Fatal("refresh converted native configuration to JSON string")
	}
	unknown := types.DynamicValue(types.ObjectValueMust(map[string]attr.Type{"leaf": types.StringType}, map[string]attr.Value{"leaf": types.StringUnknown()}))
	if _, err := configurationJSON(unknown); !errors.Is(err, errUnknownValue) {
		t.Fatalf("nested unknown was rejected: %v", err)
	}
	invalid := types.DynamicValue(types.ObjectValueMust(map[string]attr.Type{"leaf": types.BoolType}, map[string]attr.Value{"leaf": types.BoolValue(true)}))
	if _, err := configurationJSON(invalid); err == nil {
		t.Fatal("boolean leaf accepted")
	}
}

func TestUpgradeLegacyState(t *testing.T) {
	ctx := context.Background()
	t.Run("single", func(t *testing.T) {
		r := &ConfigResource{}
		upgrader := r.UpgradeState(ctx)[0]
		prior := tfsdk.State{Schema: *upgrader.PriorSchema}
		old := struct {
			ID    types.String `tfsdk:"id"`
			Path  types.String `tfsdk:"path"`
			Value types.String `tfsdk:"value"`
		}{types.StringValue("node x"), types.StringValue("node x"), types.StringValue(`{ "leaf": "one" }`)}
		if d := prior.Set(ctx, &old); d.HasError() {
			t.Fatal(d)
		}
		var schema resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schema)
		response := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schema.Schema}}
		upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &prior}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		var upgraded ConfigResourceModel
		if d := response.State.Get(ctx, &upgraded); d.HasError() {
			t.Fatal(d)
		}
		if !upgraded.Value.Equal(types.DynamicValue(old.Value)) || upgraded.Id != old.ID {
			t.Fatal("legacy string or identity changed")
		}
	})
	t.Run("batch", func(t *testing.T) {
		r := &ConfigBatchResource{}
		upgrader := r.UpgradeState(ctx)[0]
		object := types.ObjectValueMust(legacyBatchEntryType.AttrTypes, map[string]attr.Value{"path": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("node"), types.StringValue("a")}), "value": types.StringValue(`{ "leaf": "one" }`)})
		entries := types.MapValueMust(legacyBatchEntryType, map[string]attr.Value{"a": object})
		old := struct {
			ID      types.String `tfsdk:"id"`
			Entries types.Map    `tfsdk:"entries"`
		}{types.StringValue(`{"a":["node","a"]}`), entries}
		prior := tfsdk.State{Schema: *upgrader.PriorSchema}
		if d := prior.Set(ctx, &old); d.HasError() {
			t.Fatal(d)
		}
		var schema resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schema)
		response := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schema.Schema}}
		upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &prior}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		var upgraded ConfigBatchModel
		if d := response.State.Get(ctx, &upgraded); d.HasError() {
			t.Fatal(d)
		}
		if !upgraded.Entries.Equal(types.DynamicValue(entries)) || upgraded.ID != old.ID {
			t.Fatal("legacy batch changed")
		}
		decoded, d := decodeBatch(ctx, upgraded.Entries, true)
		if d.HasError() || decoded["a"].Value != `{ "leaf": "one" }` {
			t.Fatalf("cannot use upgraded batch: %v", d)
		}
	})
}
