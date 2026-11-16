package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

func TestBatchSelectorTransitionsDoNotReplace(t *testing.T) {
	ctx := context.Background()
	r := &ConfigBatchResource{}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	prior := batchModel(t, map[string]string{"a": `"one"`, "b": `"two"`})
	state := tfsdk.State{Schema: schema.Schema}
	state.Set(ctx, &prior)
	next := batchModel(t, map[string]string{"a": `"one"`, "c": `"three"`})
	next.ID = prior.ID
	plan := tfsdk.Plan{Schema: schema.Schema}
	plan.Set(ctx, &next)
	response := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, &response)
	if response.Diagnostics.HasError() || len(response.RequiresReplace) != 0 {
		t.Fatalf("selector transition must update in place: %v %v", response.RequiresReplace, response.Diagnostics)
	}
}
