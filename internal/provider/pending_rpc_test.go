package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestEarlyRefreshPreservesFailedTimedDeleteAcrossProviderInstances(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"vyos_config", "vyos_config_batch"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int64
			var deleted atomic.Bool
			var failVerification atomic.Bool
			api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.URL.Path == "/configure" {
					deleted.Store(true)
					failVerification.Store(true)
				}
				if req.URL.Path == "/retrieve" && failVerification.Swap(false) {
					fmt.Fprint(w, `{"success":false}`)
					return
				}
				if deleted.Load() {
					fmt.Fprint(w, `{"success":true,"data":{}}`)
				} else {
					fmt.Fprint(w, `{"success":true,"data":{"node":{"a":"old","x":"old"}}}`)
				}
			}))
			defer api.Close()
			newServer := func() tfprotov6.ProviderServer {
				server, err := providerserver.NewProtocol6WithError(New("test")())()
				if err != nil {
					t.Fatal(err)
				}
				schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
				if err != nil {
					t.Fatal(err)
				}
				typ := schemas.Provider.ValueType()
				values := map[string]any{"endpoint": api.URL, "api_key": "test", "insecure": true, "commit_confirm_minutes": 1, "skip_saving": false, "save_file": "", "ca_certificate_file": ""}
				attrs := map[string]tftypes.Value{}
				for name, fieldType := range typ.(tftypes.Object).AttributeTypes {
					attrs[name] = tftypes.NewValue(fieldType, values[name])
				}
				raw, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, attrs))
				if err != nil {
					t.Fatal(err)
				}
				response, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{TerraformVersion: "1.17.1", Config: &raw})
				if err != nil || protocolErrors(response.Diagnostics) {
					t.Fatalf("configure: %v %v", err, response.Diagnostics)
				}
				return server
			}
			server := newServer()
			var schema resource.SchemaResponse
			var model any
			if kind == "vyos_config" {
				(&ConfigResource{}).Schema(ctx, resource.SchemaRequest{}, &schema)
				model = &ConfigResourceModel{Id: types.StringValue("node x"), Path: types.StringValue("node x"), Value: types.DynamicValue(types.StringValue(`"old"`))}
			} else {
				(&ConfigBatchResource{}).Schema(ctx, resource.SchemaRequest{}, &schema)
				value := batchModel(t, map[string]string{"a": `"old"`})
				model = &value
			}
			state := tfsdk.State{Schema: schema.Schema}
			if d := state.Set(ctx, model); d.HasError() {
				t.Fatal(d)
			}
			typ := state.Raw.Type()
			prior, err := tfprotov6.NewDynamicValue(typ, state.Raw)
			if err != nil {
				t.Fatal(err)
			}
			absent, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
			if err != nil {
				t.Fatal(err)
			}
			result, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: kind, PriorState: &prior, PlannedState: &absent, Config: &absent})
			if err != nil || !protocolErrors(result.Diagnostics) || len(result.Private) == 0 {
				t.Fatalf("delete did not retain pending private data: %v %v", err, result)
			}
			before := calls.Load()
			refreshed, err := newServer().ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: kind, CurrentState: result.NewState, Private: result.Private})
			if err != nil || !protocolErrors(refreshed.Diagnostics) || calls.Load() != before {
				t.Fatalf("early retry accepted temporary router state: %v %v", err, refreshed)
			}
			value, err := refreshed.NewState.Unmarshal(typ)
			if err != nil || !value.Equal(state.Raw) {
				t.Fatal("early refresh lost resource ownership")
			}
		})
	}
}
func protocolErrors(diagnostics []*tfprotov6.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}
