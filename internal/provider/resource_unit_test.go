package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResourceFailureStateAndRedaction(t *testing.T) {
	ctx := context.Background()
	r := &ConfigResource{}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	if !sr.Schema.Attributes["value"].IsSensitive() {
		t.Fatal("value must be sensitive")
	}
	model := ConfigResourceModel{Path: types.StringValue("node x"), Value: types.DynamicValue(types.StringValue(`"SECRET"`)), Id: types.StringValue("node x")}
	state := tfsdk.State{Schema: sr.Schema}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	for _, failure := range []string{"retrieve", "configure", "config-file"} {
		t.Run(failure, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/"+failure {
					fmt.Fprint(w, `{"success":false,"error":"SECRET could not fetch config"}`)
				} else {
					fmt.Fprint(w, `{"success":true,"data":{}}`)
				}
			}))
			defer s.Close()
			r.vyosConfig = vyos.New(&vyos.Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET"}, false, "")
			read := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &read)
			if failure == "retrieve" {
				if !read.Diagnostics.HasError() || read.State.Raw.IsNull() {
					t.Fatal("failed read removed state")
				}
			}
			create := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
			if !create.Diagnostics.HasError() {
				t.Fatal("failed create accepted")
			}
			if failure == "config-file" && create.State.Raw.IsNull() {
				t.Fatal("save failure lost committed create identity")
			}
			update := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &update)
			if !update.Diagnostics.HasError() {
				t.Fatal("failed update accepted")
			}
			for _, d := range append(append(read.Diagnostics, create.Diagnostics...), update.Diagnostics...) {
				if strings.Contains(d.Detail(), "SECRET") || strings.Contains(d.Summary(), "SECRET") {
					t.Fatal("secret leaked")
				}
			}
		})
	}
}
