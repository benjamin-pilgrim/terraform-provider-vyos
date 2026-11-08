package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &ConfigResource{}
var _ resource.ResourceWithImportState = &ConfigResource{}
var _ resource.ResourceWithConfigure = &ConfigResource{}
var _ resource.ResourceWithValidateConfig = &ConfigResource{}

func NewConfigResource() resource.Resource { return &ConfigResource{} }

type ConfigResource struct{ vyosConfig *vyos.VyosConfig }
type ConfigResourceModel struct {
	Path  types.String `tfsdk:"path"`
	Value types.String `tfsdk:"value"`
	Id    types.String `tfsdk:"id"`
}

func (r *ConfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (r *ConfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Exclusively manages one VyOS configuration subtree. Update replaces the entire subtree; destroy removes it. Resource paths must not overlap.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Configuration identifier",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Nonempty configuration path with components separated by single spaces. Do not select an ancestor or descendant of another managed resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("{}"),
				Sensitive:           true,
				MarkdownDescription: "JSON configuration: objects, strings, or nonempty lists of strings. An empty object (the default) sets a flag. Null object members are omitted. Stored in Terraform state; protect state and plan files.",
			},
		},
	}

}

func (r *ConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.Path.IsUnknown() && !data.Path.IsNull() {
		if _, err := vyos.ParsePath(data.Path.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("path"), "Invalid configuration path", err.Error())
		}
	}
	if !data.Value.IsUnknown() && !data.Value.IsNull() {
		if _, err := vyos.DecodeValue(data.Value.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("value"), "Invalid configuration value", err.Error())
		}
	}
}

func (r *ConfigResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.vyosConfig, ok = req.ProviderData.(*vyos.VyosConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *vyos.VyosConfig, got %T", req.ProviderData))
	}
}

func (r *ConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	value, err := vyos.DecodeValue(data.Value.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration value", err.Error())
		return
	}
	err = r.vyosConfig.Set(ctx, data.Path.ValueString(), value)
	var applied *vyos.AppliedError
	var attempted *vyos.MutationError
	if err != nil && !errors.As(err, &applied) && !errors.As(err, &attempted) {
		resp.Diagnostics.AddError("Unable to create configuration", err.Error())
		return
	}
	data.Id = types.StringValue(data.Path.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to save configuration", err.Error())
	}
}

func (r *ConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	value, err := r.vyosConfig.Show(ctx, data.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read configuration", err.Error())
		return
	}
	if value == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if data.Value.IsNull() || data.Value.IsUnknown() || !vyos.Equivalent(data.Value.ValueString(), value) {
		raw, err := json.Marshal(value)
		if err != nil {
			resp.Diagnostics.AddError("Invalid router configuration", "Cannot encode configuration")
			return
		}
		data.Value = types.StringValue(string(raw))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	value, err := vyos.DecodeValue(data.Value.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration value", err.Error())
		return
	}
	if err = r.vyosConfig.Update(ctx, data.Path.ValueString(), value); err != nil {
		resp.Diagnostics.AddError("Unable to update configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.vyosConfig.Delete(ctx, data.Path.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to delete configuration", err.Error())
	}
}

func (r *ConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := vyos.ParsePath(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import path", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), req.ID)...)
}
