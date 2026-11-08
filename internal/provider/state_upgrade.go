package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.ResourceWithUpgradeState = &ConfigResource{}
	_ resource.ResourceWithUpgradeState = &ConfigBatchResource{}
)

func legacyConfigSchema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":    schema.StringAttribute{Computed: true},
		"path":  schema.StringAttribute{Required: true},
		"value": schema.StringAttribute{Optional: true, Computed: true, Sensitive: true},
	}}
}
func legacyBatchSchema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true},
		"entries": schema.MapNestedAttribute{Required: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"path":  schema.ListAttribute{Required: true, ElementType: types.StringType},
			"value": schema.StringAttribute{Required: true, Sensitive: true},
		}}},
	}}
}
func (r *ConfigResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	prior := legacyConfigSchema()
	return map[int64]resource.StateUpgrader{0: {PriorSchema: &prior, StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		var old struct {
			ID    types.String `tfsdk:"id"`
			Path  types.String `tfsdk:"path"`
			Value types.String `tfsdk:"value"`
		}
		resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &ConfigResourceModel{Id: old.ID, Path: old.Path, Value: types.DynamicValue(old.Value)})...)
	}}}
}
func (r *ConfigBatchResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	prior := legacyBatchSchema()
	return map[int64]resource.StateUpgrader{0: {PriorSchema: &prior, StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		var old struct {
			ID      types.String `tfsdk:"id"`
			Entries types.Map    `tfsdk:"entries"`
		}
		resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Preserve the map's original type, path lists and JSON strings exactly.
		resp.Diagnostics.Append(resp.State.Set(ctx, &ConfigBatchModel{ID: old.ID, Entries: types.DynamicValue(old.Entries)})...)
	}}}
}
