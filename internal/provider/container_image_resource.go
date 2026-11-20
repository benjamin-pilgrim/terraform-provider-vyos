package provider

import (
	"context"
	"fmt"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithModifyPlan = &ContainerImageResource{}
var _ resource.ResourceWithConfigure = &ContainerImageResource{}
var _ resource.ResourceWithValidateConfig = &ContainerImageResource{}
var _ resource.ResourceWithImportState = &ContainerImageResource{}

func NewContainerImageResource() resource.Resource { return &ContainerImageResource{} }

type ContainerImageResource struct{ vyosConfig *vyos.VyosConfig }
type containerImageModel struct {
	Image types.String `tfsdk:"image"`
	ID    types.String `tfsdk:"id"`
}

func (*ContainerImageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_image"
}
func (*ContainerImageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Ensures a digest-pinned container image is cached on VyOS using the container-image API. Refresh detects a pruned image; apply pulls it again. Existing images are adopted without pulling. Destroy forgets the image without deleting shared cache content. Pulls do not use configuration commit-confirm or saving settings. Requires /container-image and show container image json support.",
		Attributes: map[string]schema.Attribute{
			"image": schema.StringAttribute{Required: true, MarkdownDescription: "Fully qualified repository@sha256:<64 lowercase hexadecimal digits> reference without a tag. Changing it requires replacement.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"id":    schema.StringAttribute{Computed: true, MarkdownDescription: "The immutable image reference.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}
func (*ContainerImageResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Image.IsNull() || m.Image.IsUnknown() {
		return
	}
	if err := vyos.ValidateContainerImage(m.Image.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("image"), "Invalid container image", err.Error())
	}
}
func (r *ContainerImageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.vyosConfig, ok = req.ProviderData.(*vyos.VyosConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *vyos.VyosConfig, got %T", req.ProviderData))
	}
}
func (r *ContainerImageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.vyosConfig.EnsureContainerImage(ctx, m.Image.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to cache container image", err.Error())
		return
	}
	m.ID = m.Image
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *ContainerImageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists, err := r.vyosConfig.ContainerImageExists(ctx, m.Image.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect container image", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
	}
}
func (*ContainerImageResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected container image update", "Image changes require replacement.")
}
func (*ContainerImageResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Images are shared across containers and installed VyOS versions. Destroy
	// releases Terraform management; image pruning is an explicit operator action.
}
func (*ContainerImageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := vyos.ValidateContainerImage(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid container image import", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("image"), req.ID)...)
}

func (*ContainerImageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m containerImageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), m.Image)...)
}
