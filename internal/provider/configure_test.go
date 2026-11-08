package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestConfigureSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		attrs map[string]any
		valid bool
	}{
		{name: "secure defaults", valid: true},
		{name: "environment", env: map[string]string{"VYOS_ENDPOINT": "https://router.example", "VYOS_API_KEY": "test"}, valid: true},
		{name: "explicit settings override invalid environment", env: map[string]string{"VYOS_INSECURE": "bad", "VYOS_SKIP_SAVING": "bad"}, attrs: map[string]any{"insecure": false, "skip_saving": true}, valid: true},
		{name: "invalid insecure env", env: map[string]string{"VYOS_INSECURE": "bad"}},
		{name: "invalid saving env", env: map[string]string{"VYOS_SKIP_SAVING": "bad"}},
		{name: "missing endpoint", attrs: map[string]any{"endpoint": ""}},
		{name: "missing key", attrs: map[string]any{"api_key": ""}},
		{name: "unknown endpoint", attrs: map[string]any{"endpoint": tftypes.UnknownValue}},
		{name: "unknown saving", attrs: map[string]any{"skip_saving": tftypes.UnknownValue}},
		{name: "unknown CA", attrs: map[string]any{"ca_certificate_file": tftypes.UnknownValue}},
		{name: "unknown insecure", attrs: map[string]any{"insecure": tftypes.UnknownValue}},
		{name: "unknown save file", attrs: map[string]any{"save_file": tftypes.UnknownValue}},
		{name: "cleartext", attrs: map[string]any{"endpoint": "http://router.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p := &VyOSProvider{}
			var schema provider.SchemaResponse
			p.Schema(ctx, provider.SchemaRequest{}, &schema)
			for _, n := range []string{"VYOS_ENDPOINT", "VYOS_API_KEY", "VYOS_SAVE_FILE", "VYOS_SKIP_SAVING", "VYOS_CA_CERTIFICATE_FILE", "VYOS_INSECURE"} {
				t.Setenv(n, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			values := map[string]any{"endpoint": "https://router.example", "api_key": "test"}
			if tc.name == "environment" {
				values = map[string]any{}
			}
			for k, v := range tc.attrs {
				values[k] = v
			}
			attrs := map[string]tftypes.Value{}
			for k, a := range schema.Schema.Attributes {
				attrs[k] = tftypes.NewValue(a.GetType().TerraformType(ctx), values[k])
			}
			raw := tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), attrs)
			var resp provider.ConfigureResponse
			p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: raw}}, &resp)
			if resp.Diagnostics.HasError() == tc.valid {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if tc.valid && resp.ResourceData == nil {
				t.Fatal("missing configured client")
			}
		})
	}
}
