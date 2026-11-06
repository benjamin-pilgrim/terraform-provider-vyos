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
	Endpoint             types.String `tfsdk:"endpoint"`
	ApiKey               types.String `tfsdk:"api_key"`
	AuthMode             types.String `tfsdk:"auth_mode"`
	BearerToken          types.String `tfsdk:"bearer_token"`
	ClientCertFile       types.String `tfsdk:"client_certificate_file"`
	ClientKeyFile        types.String `tfsdk:"client_key_file"`
	SkipSaving           types.Bool   `tfsdk:"skip_saving"`
	SaveFile             types.String `tfsdk:"save_file"`
	CACertFile           types.String `tfsdk:"ca_certificate_file"`
	CommitConfirmMinutes types.Int64  `tfsdk:"commit_confirm_minutes"`
	Insecure             types.Bool   `tfsdk:"insecure"`
}

func (p *VyOSProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vyos"
	resp.Version = p.version
}

func (p *VyOSProvider) Schema(ctx context.Context, request provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"auth_mode":               schema.StringAttribute{Optional: true, MarkdownDescription: "Authentication mode: api_key (default), bearer, mtls, or api_key_token (exchange key at /token and refresh before expiry). May also be set with VYOS_AUTH_MODE. Credentials for other modes must be unset."},
			"bearer_token":            schema.StringAttribute{Optional: true, Sensitive: true, MarkdownDescription: "Externally obtained bearer token, including an OIDC JWT. Requires auth_mode = bearer. May also be set with VYOS_BEARER_TOKEN. The provider does not renew externally issued tokens."},
			"client_certificate_file": schema.StringAttribute{Optional: true, MarkdownDescription: "PEM client certificate chain for auth_mode = mtls. May also be set with VYOS_CLIENT_CERTIFICATE_FILE. mTLS is restricted to TLS 1.2 for VyOS compatibility."},
			"client_key_file":         schema.StringAttribute{Optional: true, MarkdownDescription: "PEM private key file for auth_mode = mtls. May also be set with VYOS_CLIENT_KEY_FILE. The file must be readable by the provider process."},
			"ca_certificate_file":     schema.StringAttribute{Optional: true, MarkdownDescription: "PEM CA bundle to trust in addition to system roots. May also be set with VYOS_CA_CERTIFICATE_FILE."},
			"insecure":                schema.BoolAttribute{Optional: true, MarkdownDescription: "Disable certificate verification for isolated development only. Defaults to false. May also be set with VYOS_INSECURE."},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Endpoint of the VyOS HTTP API",
				Optional:            true,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API Key for the VyOS HTTP API",
				Optional:            true,
				Sensitive:           true,
			},
			"commit_confirm_minutes": schema.Int64Attribute{Optional: true, MarkdownDescription: "Optional rollback timer for create, update and destroy, in minutes (1–60). Default 0 disables it. Checks a fresh API connection and the changed configuration, confirms, verifies again, then saves. Connection or verification failures fail the operation without saving. Checks router access and configuration, not network traffic or the whole apply."},
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

	for name, unknown := range map[string]bool{"commit_confirm_minutes": config.CommitConfirmMinutes.IsUnknown(), "skip_saving": config.SkipSaving.IsUnknown(), "save_file": config.SaveFile.IsUnknown(), "ca_certificate_file": config.CACertFile.IsUnknown(), "insecure": config.Insecure.IsUnknown(), "auth_mode": config.AuthMode.IsUnknown(), "bearer_token": config.BearerToken.IsUnknown(), "client_certificate_file": config.ClientCertFile.IsUnknown(), "client_key_file": config.ClientKeyFile.IsUnknown()} {
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

	auth := authentication{
		Mode:     setting(config.AuthMode, "VYOS_AUTH_MODE"),
		Key:      api_key,
		Token:    setting(config.BearerToken, "VYOS_BEARER_TOKEN"),
		CertFile: setting(config.ClientCertFile, "VYOS_CLIENT_CERTIFICATE_FILE"),
		KeyFile:  setting(config.ClientKeyFile, "VYOS_CLIENT_KEY_FILE"),
	}
	if err := auth.validate(); err != nil {
		resp.Diagnostics.AddError("Invalid authentication configuration", err.Error())
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
	httpClient, err := newAuthenticatedHTTPClient(endpoint, caFile, insecure, auth)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API client configuration", err.Error())
		return
	}
	minutes := config.CommitConfirmMinutes.ValueInt64()
	if minutes < 0 || minutes > 60 {
		resp.Diagnostics.AddAttributeError(path.Root("commit_confirm_minutes"), "Invalid commit-confirm timer", "Use 0 to disable or a whole number from 1 to 60 minutes.")
		return
	}
	vyosConfig := vyos.New(&vyos.Client{HTTP: httpClient, Endpoint: endpoint, Key: auth.Key, BearerToken: auth.Token, ExchangeToken: auth.Mode == "api_key_token"}, skip_saving, save_file)

	vyosConfig.SetCommitConfirmMinutes(minutes)
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

// HTTPS is required so credentials are never sent over cleartext HTTP. Redirects
// are rejected to avoid forwarding credentials to another origin or protocol.
func newHTTPClient(endpoint, caFile string, insecure bool) (*http.Client, error) {
	return newAuthenticatedHTTPClient(endpoint, caFile, insecure, authentication{})
}

func newAuthenticatedHTTPClient(endpoint, caFile string, insecure bool, auth authentication) (*http.Client, error) {
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
	if auth.Mode == "mtls" {
		cert, err := tls.LoadX509KeyPair(auth.CertFile, auth.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load client certificate/private key pair (expected matching PEM files)")
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		// VyOS nginx uses ssl_client_verify on subsequent HTTP requests; upstream
		// currently restricts client authentication to TLS 1.2.
		transport.TLSClientConfig.MaxVersion = tls.VersionTLS12
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
