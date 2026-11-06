package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
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
	Partial      bool
}

func NewConfigBatchResource() resource.Resource { return &ConfigBatchResource{} }

func (r *ConfigBatchResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_batch"
}

func (r *ConfigBatchResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages named VyOS configuration entries in one configure request. Use value for whole-value ownership or include for ownership of selected list values. Uses the provider commit-confirm and saving settings. Unrelated configuration is preserved. Changing entry names or paths replaces the batch; changing ownership mode requires state migration. Values and includes update in place. VyOS can partially apply a failed request across components; a batch is not a whole-router transaction.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Batch identity. Import whole-value entries with path arrays, or partial entries with path and explicit include arrays.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"entries": schema.DynamicAttribute{
				Required: true, Sensitive: true, MarkdownDescription: "Object of named entries, each containing path and exactly one of value or include. value owns the whole subtree; include is a collection of strings and owns only those list values. Unrelated list values remain untouched. Ownership must not overlap. Values remain in Terraform state.",
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
		field := "value"
		if model.Partial {
			field = "include"
		}
		if requireValues && (model.Value.IsNull() || model.Value.IsUnknown()) {
			diagnostics.AddAttributeError(location.AtName(field), "Unknown configuration value", "Each batch entry must have a known configuration value before applying.")
		}
		entry := vyos.BatchEntry{Path: components, Partial: model.Partial}
		var err error
		if model.Partial {
			entry.Include, err = includeStrings(model.Value)
		} else {
			entry.Value, err = configurationJSON(model.Value)
		}
		if requireValues && err != nil {
			diagnostics.AddAttributeError(location.AtName(field), "Invalid configuration value", err.Error())
		}
		entries[name] = entry
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
				known[name] = vyos.BatchEntry{Path: components, Partial: model.Partial}
			}
		}
		field := "value"
		if model.Partial {
			field = "include"
		}
		if !model.Value.IsUnknown() && !model.Value.IsNull() {
			var err error
			if model.Partial {
				var members []string
				members, err = includeStrings(model.Value)
				if entry, ok := known[name]; ok && err == nil {
					entry.Include = members
					known[name] = entry
				}
			} else {
				_, err = configurationJSON(model.Value)
			}
			if err != nil && !errors.Is(err, errUnknownValue) {
				resp.Diagnostics.AddAttributeError(location.AtName(field), "Invalid configuration value", err.Error())
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
	priorEntries, err := vyos.ParseBatchIdentity(prior.ID.ValueString())
	priorIdentity, _ := vyos.BatchIdentity(priorEntries)
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch identity", err.Error())
		return
	}
	for _, entry := range entries {
		for _, old := range priorEntries {
			if slices.Equal(entry.Path, old.Path) && entry.Partial != old.Partial {
				resp.Diagnostics.AddAttributeError(path.Root("entries"), "Ownership mode changed", "Switch between value and include by removing the batch from Terraform state without destroying it, then importing it with the new ownership selectors. Direct replacement could delete values that should remain unmanaged.")
				return
			}
		}
	}
	if identity != priorIdentity {
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
	identity, err := vyos.OwnedBatchIdentity(entries, rand.Text())
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch identity", err.Error())
		return
	}
	attempted, err := r.vyosConfig.MutateOwnedBatch(ctx, entries, nil, "create", identity)
	if err != nil && resp.Private != nil {
		resp.Diagnostics.Append(recordPendingOperation(ctx, resp.Private, err)...)
	}
	if err == nil || attempted {
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
	priorEntries, d := decodeBatch(ctx, data.Entries, false)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, entry := range entries {
		if entry.Partial {
			entry.Include = priorEntries[name].Include
			entries[name] = entry
		}
	}
	current, err := r.vyosConfig.ShowOwnedBatch(ctx, entries, data.ID.ValueString())
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
		var refreshedValue types.Dynamic
		if entries[name].Partial {
			refreshedValue = refreshInclude(priorValue, value.([]string))
		} else {
			refreshedValue = refreshConfiguration(priorValue, value)
		}
		if !priorValue.Equal(refreshedValue) {
			unchanged = false
		}
		components, diagnostics := types.ListValueFrom(ctx, types.StringType, entries[name].Path)
		resp.Diagnostics.Append(diagnostics...)
		refreshed[name] = batchEntryModel{Path: components, Value: refreshedValue, originalPath: prior[name].originalPath, Partial: entries[name].Partial}
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
	identityEntries, err := vyos.ParseBatchIdentity(data.ID.ValueString())
	originalIdentity, _ := vyos.BatchIdentity(identityEntries)
	if err != nil {
		resp.Diagnostics.AddError("Invalid batch identity", err.Error())
		return
	}
	if identity != originalIdentity {
		resp.Diagnostics.AddError("Batch paths changed", "Changing batch entry names or paths requires replacement.")
		return
	}
	var prior ConfigBatchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	previous, d := decodeBatch(ctx, prior.Entries, true)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if attempted, err := r.vyosConfig.MutateOwnedBatch(ctx, entries, previous, "update", data.ID.ValueString()); err != nil {
		if attempted {
			owned, d := retainIncludeOwnership(ctx, prior.Entries, data.Entries)
			resp.Diagnostics.Append(d...)
			if !d.HasError() {
				prior.Entries = owned
				resp.Diagnostics.Append(resp.State.Set(ctx, &prior)...)
			}
		}
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
	owned, d := decodeBatch(ctx, data.Entries, true)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, entry := range entries {
		if entry.Partial {
			entry.Include = owned[name].Include
			entries[name] = entry
		}
	}
	if _, err := r.vyosConfig.MutateOwnedBatch(ctx, entries, entries, "delete", data.ID.ValueString()); err != nil {
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
	for _, entry := range entries {
		if entry.Partial && entry.Include == nil {
			resp.Diagnostics.AddError("Include values required for import", "Import partial entries with explicit include arrays, so only those values are adopted.")
			return
		}
	}
	identity, _ := vyos.OwnedBatchIdentity(entries, rand.Text())
	models := map[string]batchEntryModel{}
	for name, entry := range entries {
		components, diagnostics := types.ListValueFrom(ctx, types.StringType, entry.Path)
		resp.Diagnostics.Append(diagnostics...)
		value := types.DynamicNull()
		if entry.Partial {
			value = refreshInclude(types.DynamicNull(), entry.Include)
		}
		models[name] = batchEntryModel{Path: components, Value: value, Partial: entry.Partial}
	}
	values, diagnostics := packBatchModels(ctx, models)
	resp.Diagnostics.Append(diagnostics...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &ConfigBatchModel{ID: types.StringValue(identity), Entries: values})...)
}
