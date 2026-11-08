package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func clientCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "terraform client"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	certDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &clientKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(t.TempDir(), "client.pem"), filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return certFile, keyFile, roots
}

func TestMTLSClient(t *testing.T) {
	certFile, keyFile, roots := clientCertificate(t)
	for _, tc := range []struct {
		name                                 string
		trustClient, trustServer, sendClient bool
		minVersion                           uint16
		ok                                   bool
	}{
		{"verified mTLS", true, true, true, tls.VersionTLS12, true},
		{"missing client", true, true, false, tls.VersionTLS12, false},
		{"untrusted client", false, true, true, tls.VersionTLS12, false},
		{"untrusted server", true, false, true, tls.VersionTLS12, false},
		{"TLS13 only", true, true, true, tls.VersionTLS13, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			configured := false
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.TLS.Version != tls.VersionTLS12 || len(r.TLS.VerifiedChains) == 0 {
					t.Error("unverified client or wrong TLS version")
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Has("key") || r.Header.Get("Authorization") != "" || r.Header.Get("X-Client-Verify") != "" {
					t.Error("unexpected auth header/key")
				}
				if r.URL.Path == "/configure" {
					configured = true
				}
				value := "old"
				if configured {
					value = "new"
				}
				fmt.Fprintf(w, `{"success":true,"data":{"node":{"x":%q}}}`, value)
			}))
			clientRoots := x509.NewCertPool()
			if tc.trustClient {
				clientRoots = roots
			}
			s.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots, MinVersion: tc.minVersion}
			s.StartTLS()
			defer s.Close()
			serverCA := filepath.Join(t.TempDir(), "server-ca.pem")
			if err := os.WriteFile(serverCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if !tc.trustServer {
				serverCA = ""
			}
			auth := authentication{Mode: "mtls", CertFile: certFile, KeyFile: keyFile}
			if !tc.sendClient {
				auth = authentication{}
			}
			h, err := newAuthenticatedHTTPClient(s.URL, serverCA, false, auth)
			if err != nil {
				t.Fatal(err)
			}
			defer h.CloseIdleConnections()
			vc := vyos.New(&vyos.Client{HTTP: h, Endpoint: s.URL}, false, "")
			for range 2 {
				_, err = vc.Show(context.Background(), "node x")
				if (err == nil) != tc.ok {
					t.Fatalf("TLS result: %v", err)
				}
				h.CloseIdleConnections()
			}
			if tc.ok {
				vc.SetCommitConfirmMinutes(1)
				if err := vc.Update(context.Background(), "node x", "new"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.ok && calls != 8 || !tc.ok && calls != 0 {
				t.Fatal("incorrect TLS calls", calls)
			}
		})
	}
	// Missing, malformed and mismatched PEM pairs fail before any network call.
	otherCert, otherKey, _ := clientCertificate(t)
	for _, pair := range [][2]string{{"/missing/cert", keyFile}, {certFile, "/missing/key"}, {certFile, certFile}, {otherCert, keyFile}, {certFile, otherKey}} {
		if _, err := newAuthenticatedHTTPClient("https://router.example", "", false, authentication{Mode: "mtls", CertFile: pair[0], KeyFile: pair[1]}); err == nil || strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Fatal("invalid pair or unsafe diagnostics", err)
		}
	}
}

func TestConfiguredAuthenticationRequests(t *testing.T) {
	certFile, keyFile, roots := clientCertificate(t)
	for _, mode := range []string{"api_key", "bearer", "api_key_token", "mtls"} {
		t.Run(mode, func(t *testing.T) {
			for _, name := range []string{"VYOS_ENDPOINT", "VYOS_API_KEY", "VYOS_AUTH_MODE", "VYOS_BEARER_TOKEN", "VYOS_CLIENT_CERTIFICATE_FILE", "VYOS_CLIENT_KEY_FILE", "VYOS_CA_CERTIFICATE_FILE", "VYOS_INSECURE", "VYOS_SKIP_SAVING", "VYOS_SAVE_FILE"} {
				t.Setenv(name, "")
			}
			exchanges := 0
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.URL.Path == "/token" {
					exchanges++
					if mode != "api_key_token" || r.Form.Get("key") != "SECRET" {
						t.Error("incorrect exchange credentials")
					}
					fmt.Fprint(w, `{"success":true,"data":{"token":"exchanged","expires_in":60}}`)
					return
				}
				switch mode {
				case "api_key":
					if r.Form.Get("key") != "SECRET" {
						t.Error("missing configured key")
					}
				case "bearer":
					if r.Header.Get("Authorization") != "Bearer external.jwt.token" {
						t.Error("missing supplied bearer")
					}
				case "api_key_token":
					if r.Header.Get("Authorization") != "Bearer exchanged" {
						t.Error("missing exchanged bearer")
					}
				case "mtls":
					if len(r.TLS.VerifiedChains) == 0 || r.TLS.Version != tls.VersionTLS12 {
						t.Error("missing configured mTLS")
					}
				}
				if mode != "api_key" && r.Form.Has("key") {
					t.Error("key leaked")
				}
				fmt.Fprint(w, `{"success":true,"data":{"node":"ok"}}`)
			}))
			if mode == "mtls" {
				s.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS12}
			}
			s.StartTLS()
			defer s.Close()
			serverCA := filepath.Join(t.TempDir(), "server.pem")
			if err := os.WriteFile(serverCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			values := map[string]any{"endpoint": s.URL, "ca_certificate_file": serverCA}
			// Exercise environment resolution as well as explicit attribute precedence.
			t.Setenv("VYOS_AUTH_MODE", mode)
			switch mode {
			case "api_key", "api_key_token":
				t.Setenv("VYOS_API_KEY", "stale")
				values["api_key"] = "SECRET"
			case "bearer":
				t.Setenv("VYOS_BEARER_TOKEN", "external.jwt.token")
			case "mtls":
				t.Setenv("VYOS_CLIENT_CERTIFICATE_FILE", certFile)
				t.Setenv("VYOS_CLIENT_KEY_FILE", keyFile)
			}
			ctx := context.Background()
			p := &VyOSProvider{}
			var schema provider.SchemaResponse
			p.Schema(ctx, provider.SchemaRequest{}, &schema)
			attrs := map[string]tftypes.Value{}
			for name, attr := range schema.Schema.Attributes {
				attrs[name] = tftypes.NewValue(attr.GetType().TerraformType(ctx), values[name])
			}
			raw := tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), attrs)
			var resp provider.ConfigureResponse
			p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: raw}}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			vc := resp.ResourceData.(*vyos.VyosConfig)
			if _, err := vc.Show(ctx, "node"); err != nil {
				t.Fatal(err)
			}
			if mode == "api_key_token" && exchanges != 1 {
				t.Fatal("no configured exchange")
			}
		})
	}
}
