package provider

import (
	"fmt"
	"os"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An explicit value (including empty) overrides the corresponding environment
// variable. Resolve all credentials first, then reject conflicts; never silently
// choose an identity when a stale environment variable supplies another one.
func setting(value types.String, env string) string {
	if !value.IsNull() {
		return value.ValueString()
	}
	return os.Getenv(env)
}

type authentication struct{ Mode, Key, Token, CertFile, KeyFile string }

func (a *authentication) validate() error {
	if a.Mode == "" {
		a.Mode = "api_key"
	}
	switch a.Mode {
	case "api_key", "api_key_token":
		if a.Key == "" {
			return fmt.Errorf("%s requires api_key or VYOS_API_KEY", a.Mode)
		}
		if a.Token != "" || a.CertFile != "" || a.KeyFile != "" {
			return fmt.Errorf("API key authentication conflicts with bearer_token and client certificate/key files; unset credentials for other modes")
		}
	case "bearer":
		if !vyos.ValidBearer(a.Token) {
			return fmt.Errorf("bearer mode requires a nonempty bearer_token or VYOS_BEARER_TOKEN containing a valid Bearer credential")
		}
		if a.Key != "" || a.CertFile != "" || a.KeyFile != "" {
			return fmt.Errorf("bearer authentication conflicts with api_key and client certificate/key files; unset credentials for other modes")
		}
	case "mtls":
		if a.CertFile == "" || a.KeyFile == "" {
			return fmt.Errorf("mtls mode requires both client_certificate_file and client_key_file (or their VYOS_CLIENT_CERTIFICATE_FILE and VYOS_CLIENT_KEY_FILE environment variables)")
		}
		if a.Key != "" || a.Token != "" {
			return fmt.Errorf("mtls authentication conflicts with api_key and bearer_token; unset credentials for other modes")
		}
	default:
		return fmt.Errorf("auth_mode must be api_key, bearer, mtls, or api_key_token")
	}
	return nil
}
