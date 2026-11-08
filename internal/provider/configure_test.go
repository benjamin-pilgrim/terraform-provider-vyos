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
		{name: "bearer", attrs: map[string]any{"auth_mode": "bearer", "api_key": "", "bearer_token": "signed.jwt.token"}, valid: true},
		{name: "exchange", attrs: map[string]any{"auth_mode": "api_key_token"}, valid: true},
		{name: "bearer environment", env: map[string]string{"VYOS_AUTH_MODE": "bearer", "VYOS_BEARER_TOKEN": "signed.jwt.token"}, attrs: map[string]any{"api_key": ""}, valid: true},
		{name: "explicit auth overrides environment", env: map[string]string{"VYOS_AUTH_MODE": "bad", "VYOS_BEARER_TOKEN": "stale"}, attrs: map[string]any{"auth_mode": "api_key", "bearer_token": ""}, valid: true},
		{name: "stale key environment", env: map[string]string{"VYOS_API_KEY": "stale"}, attrs: map[string]any{"auth_mode": "bearer", "api_key": nil, "bearer_token": "signed.jwt.token"}},
		{name: "conflicting token", attrs: map[string]any{"bearer_token": "token"}},
		{name: "bearer conflict", attrs: map[string]any{"auth_mode": "bearer", "bearer_token": "token"}},
		{name: "missing bearer", attrs: map[string]any{"auth_mode": "bearer", "api_key": ""}},
		{name: "invalid bearer", attrs: map[string]any{"auth_mode": "bearer", "api_key": "", "bearer_token": "secret\r\nX-Header:bad"}},
		{name: "unknown bearer", attrs: map[string]any{"bearer_token": tftypes.UnknownValue}},
		{name: "unknown mode", attrs: map[string]any{"auth_mode": tftypes.UnknownValue}},
		{name: "unknown certificate", attrs: map[string]any{"client_certificate_file": tftypes.UnknownValue}},
		{name: "unknown private key", attrs: map[string]any{"client_key_file": tftypes.UnknownValue}},
		{name: "invalid mode", attrs: map[string]any{"auth_mode": "oidc"}},
		{name: "missing exchange key", attrs: map[string]any{"auth_mode": "api_key_token", "api_key": ""}},
		{name: "missing mtls pair", attrs: map[string]any{"auth_mode": "mtls", "api_key": ""}},
		{name: "partial mtls pair", attrs: map[string]any{"auth_mode": "mtls", "api_key": "", "client_certificate_file": "cert.pem"}},
		{name: "missing mtls files", attrs: map[string]any{"auth_mode": "mtls", "api_key": "", "client_certificate_file": "/missing/cert", "client_key_file": "/missing/key"}},
		{name: "mtls conflicts", attrs: map[string]any{"auth_mode": "mtls", "client_certificate_file": "cert.pem", "client_key_file": "key.pem"}},
		{name: "commit-confirm", attrs: map[string]any{"commit_confirm_minutes": 1}, valid: true},
		{name: "invalid timer", attrs: map[string]any{"commit_confirm_minutes": -1}},
		{name: "large timer", attrs: map[string]any{"commit_confirm_minutes": 61}},
		{name: "unknown timer", attrs: map[string]any{"commit_confirm_minutes": tftypes.UnknownValue}},
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
			for _, n := range []string{"VYOS_AUTH_MODE", "VYOS_BEARER_TOKEN", "VYOS_CLIENT_CERTIFICATE_FILE", "VYOS_CLIENT_KEY_FILE", "VYOS_ENDPOINT", "VYOS_API_KEY", "VYOS_SAVE_FILE", "VYOS_SKIP_SAVING", "VYOS_CA_CERTIFICATE_FILE", "VYOS_INSECURE"} {
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
