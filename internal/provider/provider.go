package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure VyOSProvider satisfies various provider interfaces.
var _ provider.Provider = &VyOSProvider{}

// VyOSProvider defines the provider implementation.
type VyOSProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// VyOSProviderModel describes the provider data model.
type VyOSProviderModel struct {
	Endpoint   types.String `tfsdk:"endpoint"`
	ApiKey     types.String `tfsdk:"api_key"`
	SkipSaving types.Bool   `tfsdk:"skip_saving"`
	SaveFile   types.String `tfsdk:"save_file"`
	CACertFile types.String `tfsdk:"ca_certificate_file"`
	Insecure   types.Bool   `tfsdk:"insecure"`
}

func (p *VyOSProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vyos"
	resp.Version = p.version
}

func (p *VyOSProvider) Schema(ctx context.Context, request provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"ca_certificate_file": schema.StringAttribute{Optional: true, MarkdownDescription: "PEM CA bundle to trust in addition to system roots. May also be set with VYOS_CA_CERTIFICATE_FILE."},
			"insecure":            schema.BoolAttribute{Optional: true, MarkdownDescription: "Disable certificate verification for isolated development only. Defaults to false. May also be set with VYOS_INSECURE."},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Endpoint of the VyOS HTTP API",
				Optional:            true,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API Key for the VyOS HTTP API",
				Optional:            true,
				Sensitive:           true,
			},
			"skip_saving": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Set to true to skip saving the config to disk.",
			},
			"save_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Remote file path to save the configuration to. Does not change the router boot file selection.",
			},
		},
	}
}

func (p *VyOSProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config VyOSProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	for name, unknown := range map[string]bool{"skip_saving": config.SkipSaving.IsUnknown(), "save_file": config.SaveFile.IsUnknown(), "ca_certificate_file": config.CACertFile.IsUnknown(), "insecure": config.Insecure.IsUnknown()} {
		if unknown {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider setting", "Provider settings must be known before configuring the client.")
		}
	}
	// Configuration values are now available.
	if config.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Unknown VyOS API Endpoint",
			"The provider cannot create the VyOS API client as there is an unknown configuration value for the VyOS API Endpoint. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the VYOS_ENDPOINT environment variable.",
		)
	}

	if config.ApiKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Unknown VyOS API key",
			"The provider cannot create the VyOS API client as there is an unknown configuration value for the VyOS API ApiKey. "+
				"Either target apply the source of the value first, set the value statically in the configuration, or use the VYOS_API_KEY environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("VYOS_ENDPOINT")
	api_key := os.Getenv("VYOS_API_KEY")
	save_file := os.Getenv("VYOS_SAVE_FILE")
	skip_saving := false
	skip_saving_str := os.Getenv("VYOS_SKIP_SAVING")
	if config.SkipSaving.IsNull() && skip_saving_str != "" {
		var err error = nil
		skip_saving, err = strconv.ParseBool(skip_saving_str)
		if err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("skip_saving"),
				"Unable to parse VYOS_SKIP_SAVING",
				"The provider cannot create the VyOS API client as there is an invalid value for the VYOS_SKIP_SAVING environment value.",
			)
		}
	}

	if !config.Endpoint.IsNull() {
		endpoint = config.Endpoint.ValueString()
	}

	if !config.ApiKey.IsNull() {
		api_key = config.ApiKey.ValueString()
	}

	if !config.SaveFile.IsNull() {
		save_file = config.SaveFile.ValueString()
	}

	if !config.SkipSaving.IsNull() {
		skip_saving = config.SkipSaving.ValueBool()
	}

	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Missing VyOS API Endpoint",
			"The provider cannot create the VyOS API client as there is an missing or empty value for the VyOS API Endpoint. "+
				"Set the endpoint value in the configuration or use the VYOS_ENDPOINT environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if api_key == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Missing VyOS API key",
			"The provider cannot create the VyOS API client as there is an missing or empty value for the VyOS API Key. "+
				"Set the API key value in the configuration or use the VYOS_API_KEY environment variable. "+
				"If either is already set, ensure the value is not empty.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	caFile := os.Getenv("VYOS_CA_CERTIFICATE_FILE")
	if !config.CACertFile.IsNull() {
		caFile = config.CACertFile.ValueString()
	}
	insecure := false
	if config.Insecure.IsNull() {
		if v := os.Getenv("VYOS_INSECURE"); v != "" {
			var err error
			insecure, err = strconv.ParseBool(v)
			if err != nil {
				resp.Diagnostics.AddError("Invalid VYOS_INSECURE", "Expected a boolean.")
				return
			}
		}
	} else {
		insecure = config.Insecure.ValueBool()
	}
	httpClient, err := newHTTPClient(endpoint, caFile, insecure)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API client configuration", err.Error())
		return
	}
	vyosConfig := vyos.New(&vyos.Client{HTTP: httpClient, Endpoint: endpoint, Key: api_key}, skip_saving, save_file)

	resp.DataSourceData = vyosConfig
	resp.ResourceData = vyosConfig
}

func (p *VyOSProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewConfigResource,
		NewConfigBatchResource,
	}
}

func (p *VyOSProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &VyOSProvider{
			version: version,
		}
	}
}

// HTTPS is required so the API key is never sent over cleartext HTTP. Redirects
// are rejected to avoid forwarding credentials to another origin or protocol.
func newHTTPClient(endpoint, caFile string, insecure bool) (*http.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read CA certificate file")
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA certificate file contains no PEM certificates")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, InsecureSkipVerify: insecure} // explicit development override
	return &http.Client{Transport: transport, Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
