package provider

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
)

func TestTLSVerification(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"success":true,"data":{"node":"ok"}}`) }))
	defer s.Close()
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, ca     string
		insecure, ok bool
	}{{"secure default", "", false, false}, {"custom CA", file, false, true}, {"development override", "", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := newHTTPClient(s.URL, tc.ca, tc.insecure)
			if err != nil {
				t.Fatal(err)
			}
			vc := vyos.New(&vyos.Client{HTTP: h, Endpoint: s.URL, Key: "test"}, false, "")
			_, err = vc.Show(context.Background(), "node")
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected TLS result: %v", err)
			}
		})
	}
	if _, err := newHTTPClient("https://example.com/api-prefix/", "", false); err != nil {
		t.Fatal("valid HTTPS prefix rejected", err)
	}
	if _, err := newHTTPClient("http://example.com", "", false); err == nil {
		t.Fatal("cleartext allowed")
	}
	for _, u := range []string{"https://user:pass@example.com", "https://example.com?key=secret", "https://example.com#x"} {
		if _, err := newHTTPClient(u, "", false); err == nil {
			t.Fatal("unsafe origin", u)
		}
	}
	if _, err := newHTTPClient(s.URL, "/does/not/exist", false); err == nil {
		t.Fatal("missing CA allowed")
	}
	if err := os.WriteFile(file, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newHTTPClient(s.URL, file, false); err == nil {
		t.Fatal("invalid CA allowed")
	}
}

func TestTLSHostnameAndRedirectProtection(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := newHTTPClient(source.URL, file, false)
	if err != nil {
		t.Fatal(err)
	}
	vc := vyos.New(&vyos.Client{HTTP: h, Endpoint: source.URL, Key: "SECRET"}, false, "")
	if _, err := vc.Show(context.Background(), "node"); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalls != 0 {
		t.Fatal("credentials forwarded to redirect target")
	}
	mismatched := strings.Replace(source.URL, "127.0.0.1", "localhost", 1)
	h, err = newHTTPClient(mismatched, file, false)
	if err != nil {
		t.Fatal(err)
	}
	vc = vyos.New(&vyos.Client{HTTP: h, Endpoint: mismatched, Key: "test"}, false, "")
	if _, err := vc.Show(context.Background(), "node"); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
}
