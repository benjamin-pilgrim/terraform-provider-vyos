package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &ConfigBatchResource{}
	_ resource.ResourceWithConfigure      = &ConfigBatchResource{}
	_ resource.ResourceWithValidateConfig = &ConfigBatchResource{}
	_ resource.ResourceWithModifyPlan     = &ConfigBatchResource{}
	_ resource.ResourceWithImportState    = &ConfigBatchResource{}
)

type ConfigBatchResource struct{ vyosConfig *vyos.VyosConfig }

type ConfigBatchModel struct {
	ID      types.String  `tfsdk:"id"`
	Entries types.Dynamic `tfsdk:"entries"`
}

type batchEntryModel struct {
	Path         types.List
	Value        types.Dynamic
	originalPath attr.Value
}

func NewConfigBatchResource() resource.Resource { return &ConfigBatchResource{} }

func (r *ConfigBatchResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_batch"
}

func (r *ConfigBatchResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages named, disjoint VyOS subtrees in one configure request. Uses the provider commit-confirm and saving settings. Unrelated configuration is preserved. Changing entry names or paths replaces the batch; value changes update it in place. VyOS can partially apply a failed request across components; a batch is not a whole-router transaction.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "JSON object mapping entry names to their path arrays; also used for import.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"entries": schema.DynamicAttribute{
				Required: true, Sensitive: true, MarkdownDescription: "Object of named entries, each containing path (an array of strings) and value (native HCL configuration or a legacy JSON string). Each entry exclusively owns its complete subtree. Paths must be disjoint. Values remain in Terraform state.",
			},
		},
	}
}

func (r *ConfigBatchResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.vyosConfig, ok = req.ProviderData.(*vyos.VyosConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *vyos.VyosConfig, got %T", req.ProviderData))
	}
}

// Paths determine identity; values may still be unknown while planning.
func decodeBatch(ctx context.Context, values types.Dynamic, requireValues bool) (vyos.Batch, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if values.IsNull() || values.IsUnknown() {
		diagnostics.AddAttributeError(path.Root("entries"), "Unknown batch paths", "Batch entry names and paths must be known when planning changes to an existing batch.")
		return nil, diagnostics
	}
	models, d := batchModels(ctx, values)
	diagnostics.Append(d...)
	entries := vyos.Batch{}
	for name, model := range models {
		location := path.Root("entries").AtMapKey(name)
		if model.Path.IsUnknown() || model.Path.IsNull() {
			diagnostics.AddAttributeError(location.AtName("path"), "Unknown batch path", "Path components must be known before applying a batch.")
			continue
		}
		var components []string
		diagnostics.Append(model.Path.ElementsAs(ctx, &components, false)...)
		if requireValues && (model.Value.IsNull() || model.Value.IsUnknown()) {
			diagnostics.AddAttributeError(location.AtName("value"), "Unknown configuration value", "Each batch entry must have a known configuration value before applying.")
		}
		raw, err := configurationJSON(model.Value)
		if requireValues && err != nil {
			diagnostics.AddAttributeError(location.AtName("value"), "Invalid configuration value", err.Error())
		}
		entries[name] = vyos.BatchEntry{Path: components, Value: raw}
	}
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	if _, err := vyos.BatchIdentity(entries); err != nil {
		diagnostics.AddAttributeError(path.Root("entries"), "Invalid batch paths", err.Error())
	}
	return entries, diagnostics
}

func (r *ConfigBatchResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data ConfigBatchModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Entries.IsNull() || data.Entries.IsUnknown() {
		return
	}
	models, d := batchModels(ctx, data.Entries)
	resp.Diagnostics.Append(d...)
	known := vyos.Batch{}
	for name, model := range models {
		location := path.Root("entries").AtMapKey(name)
		if model.Path.IsNull() {
			resp.Diagnostics.AddAttributeError(location.AtName("path"), "Invalid batch path", "Path must be a nonempty array of strings.")
		}
		if !model.Path.IsUnknown() && !model.Path.IsNull() {
			complete := true
			for index, component := range model.Path.Elements() {
				if component.IsNull() {
					resp.Diagnostics.AddAttributeError(location.AtName("path").AtListIndex(index), "Invalid path component", "Path components cannot be null.")
				}
				if component.IsUnknown() || component.IsNull() {
					complete = false
				}
			}
			if complete {
				var components []string
				resp.Diagnostics.Append(model.Path.ElementsAs(ctx, &components, false)...)
				known[name] = vyos.BatchEntry{Path: components}
			}
		}
		if !model.Value.IsUnknown() && !model.Value.IsNull() {
			if _, err := configurationJSON(model.Value); err != nil && !errors.Is(err, errUnknownValue) {
				resp.Diagnostics.AddAttributeError(location.AtName("value"), "Invalid configuration value", err.Error())
			}
		}
	}
	if len(known) > 0 || len(models) == 0 {
		if _, err := vyos.BatchIdentity(known); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("entries"), "Invalid batch paths", err.Error())
		}
	}
}

func (r *ConfigBatchResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var prior, planned ConfigBatchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		return
	}
	entries, diagnostics := decodeBatch(ctx, planned.Entries, false)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, _ := vyos.BatchIdentity(entries)
	if identity != prior.ID.ValueString() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("entries"))
	}
}

func (r *ConfigBatchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ConfigBatchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	entries, diagnostics := decodeBatch(ctx, data.Entries, true)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	attempted, err := r.vyosConfig.MutateBatch(ctx, entries, "create")
	if err != nil && resp.Private != nil {
		resp.Diagnostics.Append(recordPendingOperation(ctx, resp.Private, err)...)
	}
	if err == nil || attempted {
		identity, _ := vyos.BatchIdentity(entries)
		data.ID = types.StringValue(identity)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to create configuration batch", err.Error()+" If a configure request was attempted, the batch identity is retained for cleanup because changes may have been partially applied.")
	}
}

func (r *ConfigBatchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if req.Private != nil {
		blocked, d := pendingOperation(ctx, req.Private, time.Now())
		resp.Diagnostics.Append(d...)
		if blocked {
			return
		}
	}

	var data ConfigBatchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	entries, err := vyos.ParseBatchIdentity(data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch identity", err.Error())
		return
	}
	current, err := r.vyosConfig.ShowBatch(ctx, entries)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read configuration batch", err.Error())
		return
	}
	if len(current) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	prior, d := batchModels(ctx, data.Entries)
	if !data.Entries.IsNull() {
		resp.Diagnostics.Append(d...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	refreshed := map[string]batchEntryModel{}
	unchanged := len(prior) == len(current)
	for name, value := range current {
		priorValue := types.DynamicNull()
		if model, ok := prior[name]; ok {
			priorValue = model.Value
		}
		refreshedValue := refreshConfiguration(priorValue, value)
		if !priorValue.Equal(refreshedValue) {
			unchanged = false
		}
		components, diagnostics := types.ListValueFrom(ctx, types.StringType, entries[name].Path)
		resp.Diagnostics.Append(diagnostics...)
		refreshed[name] = batchEntryModel{Path: components, Value: refreshedValue, originalPath: prior[name].originalPath}
	}
	if unchanged {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	var diagnostics diag.Diagnostics
	data.Entries, diagnostics = packBatchModels(ctx, refreshed)
	resp.Diagnostics.Append(diagnostics...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
}

func (r *ConfigBatchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if req.Private != nil {
		blocked, d := pendingOperation(ctx, req.Private, time.Now())
		resp.Diagnostics.Append(d...)
		if blocked {
			return
		}
	}

	var data ConfigBatchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	entries, diagnostics := decodeBatch(ctx, data.Entries, true)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, _ := vyos.BatchIdentity(entries)
	if identity != data.ID.ValueString() {
		resp.Diagnostics.AddError("Batch paths changed", "Changing batch entry names or paths requires replacement.")
		return
	}
	if _, err := r.vyosConfig.MutateBatch(ctx, entries, "update"); err != nil {
		if resp.Private != nil {
			resp.Diagnostics.Append(recordPendingOperation(ctx, resp.Private, err)...)
		}
		resp.Diagnostics.AddError("Unable to update configuration batch", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ConfigBatchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if req.Private != nil {
		blocked, d := pendingOperation(ctx, req.Private, time.Now())
		resp.Diagnostics.Append(d...)
		if blocked {
			return
		}
	}

	var data ConfigBatchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	entries, err := vyos.ParseBatchIdentity(data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch identity", err.Error())
		return
	}
	if _, err := r.vyosConfig.MutateBatch(ctx, entries, "delete"); err != nil {
		if resp.Private != nil {
			resp.Diagnostics.Append(recordPendingOperation(ctx, resp.Private, err)...)
		}
		resp.Diagnostics.AddError("Unable to delete configuration batch", err.Error())
	}
}

func (r *ConfigBatchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	entries, err := vyos.ParseBatchIdentity(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch import ID", err.Error())
		return
	}
	identity, _ := vyos.BatchIdentity(entries)
	models := map[string]batchEntryModel{}
	for name, entry := range entries {
		components, diagnostics := types.ListValueFrom(ctx, types.StringType, entry.Path)
		resp.Diagnostics.Append(diagnostics...)
		models[name] = batchEntryModel{Path: components, Value: types.DynamicNull()}
	}
	values, diagnostics := packBatchModels(ctx, models)
	resp.Diagnostics.Append(diagnostics...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &ConfigBatchModel{ID: types.StringValue(identity), Entries: values})...)
}
