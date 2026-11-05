package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAccCommitConfirmConnectionLossOnDestroy(t *testing.T) {
	if os.Getenv("TF_ACC") == "" || os.Getenv("VYOS_NATIVE_CHECK") == "" {
		t.Skip("requires the isolated VM and native rollback checks")
	}
	testAccPreCheck(t)
	ctx := context.Background()
	httpClient, err := newHTTPClient(os.Getenv("VYOS_ENDPOINT"), os.Getenv("VYOS_CA_CERTIFICATE_FILE"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.CloseIdleConnections()
	client := &vyos.Client{HTTP: httpClient, Endpoint: os.Getenv("VYOS_ENDPOINT"), Key: os.Getenv("VYOS_API_KEY")}
	normal := vyos.New(client, false, "")
	normal.SetCommitConfirmMinutes(1)
	path := []string{"firewall", "ipv4", "name", "TF-PROVIDER-CONNECTION-LOSS"}
	entries := vyos.Batch{"test": {Path: path, Value: `{"default-action":"drop"}`}}
	if _, err := normal.MutateBatch(ctx, entries, "create"); err != nil {
		t.Fatal(err)
	}
	if err := testAccCheckArrayPath(path, `{"default-action":"drop"}`); err != nil {
		t.Fatal(err)
	}

	// Lose transport access immediately after VyOS accepts the deletion. This
	// exercises the provider's failure path while native console access remains.
	configured := false
	writesAfterLoss := 0
	base := httpClient.Transport
	brokenHTTP := *httpClient
	brokenHTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if configured {
			if req.URL.Path != "/retrieve" {
				writesAfterLoss++
			}
			return nil, errors.New("simulated lost connection")
		}
		response, err := base.RoundTrip(req)
		if req.URL.Path == "/configure" && err == nil {
			configured = true
		}
		return response, err
	})
	broken := vyos.New(&vyos.Client{HTTP: &brokenHTTP, Endpoint: client.Endpoint, Key: client.Key}, false, "")
	broken.SetCommitConfirmMinutes(1)
	attempted, err := broken.MutateBatch(ctx, entries, "delete")
	if !attempted || err == nil || writesAfterLoss != 0 {
		t.Fatalf("disconnect did not fail without confirmation/save: attempted=%v error=%v writes=%d", attempted, err, writesAfterLoss)
	}
	raw, _ := json.Marshal(path)
	output, err := exec.Command(os.Getenv("VYOS_NATIVE_CHECK"), "check-pending-array", string(raw), "null", `{"default-action":"drop"}`).CombinedOutput()
	if err != nil {
		t.Fatalf("lost connection did not leave deletion unsaved and timer running: %s", output)
	}
	fmt.Print(string(output))

	deadline := time.Now().Add(100 * time.Second)
	for {
		if err := testAccCheckArrayPath(path, `{"default-action":"drop"}`); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("VyOS did not restore the deleted configuration after loss of connectivity")
		}
		time.Sleep(2 * time.Second)
	}
	// A fresh apply can retry destroy after connectivity and configuration recover.
	if _, err := normal.MutateBatch(ctx, entries, "delete"); err != nil {
		t.Fatal(err)
	}
	if err := testAccCheckArrayPath(path, "null"); err != nil {
		t.Fatal(err)
	}
}
